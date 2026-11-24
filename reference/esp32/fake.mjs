// Fake Web Serial port + fake Espressif ROM/stub responder, for recording what esptool-js sends.
//
// Copied from the meshflash research prototype (proto-esp32/fake.mjs) and extended for stable recordings:
// the register values (eFuses, UARTDEV_BUF_NO), the flash ID and the number of SPI_CMD polls that read "busy"
// are options, and the boot banner is always sent as one chunk. internal/esp/fake.go is the Go port of FakeEsp
// with the same defaults; keep the two in step.
import { createHash } from 'node:crypto';
import { inflateSync } from 'node:zlib';

const t0 = Date.now();
export const now = () => Date.now() - t0;
const hex = (b) => Buffer.from(b).toString('hex');

export function slipEncode(pkt) {
  const out = [0xc0];
  for (const b of pkt) {
    if (b === 0xc0) out.push(0xdb, 0xdc);
    else if (b === 0xdb) out.push(0xdb, 0xdd);
    else out.push(b);
  }
  out.push(0xc0);
  return Uint8Array.from(out);
}

export const CHIPS = {
  esp32: {
    magic: 0x00f01d83, spiBase: 0x3ff42000, w0: 0x80,
    banner: 'ets Jun  8 2016 00:22:57\r\n\r\nrst:0x1 (POWERON_RESET),boot:0x3 (DOWNLOAD_BOOT(UART0/UART1/SDIO_REI_REO_V2))\r\nwaiting for download\r\n',
    regs: { 0x3ff40014: 347 /* UART_CLKDIV -> 40 MHz xtal at 115200 */ },
  },
  esp32s3: {
    magic: 0x00000009, spiBase: 0x60002000, w0: 0x58,
    banner: 'ESP-ROM:esp32s3-20210327\r\nBuild:Mar 27 2021\r\nrst:0x1 (POWERON),boot:0x0 (DOWNLOAD(USB/UART0))\r\nwaiting for download\r\n',
    regs: {},
  },
  esp32c3: {
    magic: 0x1b31506f, spiBase: 0x60002000, w0: 0x58,
    banner: 'ESP-ROM:esp32c3-api1-20210207\r\nBuild:Feb  7 2021\r\nrst:0x1 (POWERON),boot:0x0 (DOWNLOAD(USB/UART0/1))\r\nwaiting for download\r\n',
    regs: {},
  },
};

export class FakeEsp {
  // regs: extra register values ({address: value}) on top of the chip's defaults, e.g. eFuse words.
  // polls: how many READ_REGs of SPI_CMD still see the USR (busy) bit after the RDID is started.
  constructor(chipName, log, { flashId = 0x1740ef, flashSize = 8 << 20, romSyncVal = 0x20120707, regs = {}, polls = 0 } = {}) {
    this.chip = CHIPS[chipName];
    this.log = log;
    this.mode = 'off';            // off | rom | stub | app
    this.dtr = false; this.rts = false;
    this.rx = []; this.inPkt = false; this.esc = false;
    this.flash = new Uint8Array(flashSize).fill(0xff);
    this.regs = new Map(Object.entries({ ...this.chip.regs, ...regs }).map(([k, v]) => [Number(k), v >>> 0]));
    this.regs.set(0x40001000, this.chip.magic);
    this.flashId = flashId;
    this.romSyncVal = romSyncVal;
    this.polls = polls;
    this.busyReads = 0;
    this.defl = null;
    this.send = () => {};
  }
  get statusLen() { return this.mode === 'stub' ? 2 : 4; }

  signals({ dataTerminalReady, requestToSend }) {
    const prevRts = this.rts;
    if (dataTerminalReady !== undefined) this.dtr = dataTerminalReady;
    if (requestToSend !== undefined) this.rts = requestToSend;
    // RTS asserted = EN low (reset); DTR asserted = IO0 low (boot strap). Release EN with IO0 low -> ROM download mode.
    if (prevRts && !this.rts) {
      if (this.ignoreResets > 0) { this.ignoreResets--; this.mode = 'app'; return; }
      if (this.dtr || this.jtagLike) {
        this.mode = 'rom'; this.defl = null;
        setTimeout(() => this.send(new TextEncoder().encode(this.chip.banner), 'banner'), 5);
      } else {
        this.mode = 'app';
      }
    }
  }

  write(bytes) {
    for (const b of bytes) {
      if (b === 0xc0) {
        if (this.inPkt && this.rx.length) { this.handle(Uint8Array.from(this.rx)); this.rx = []; this.inPkt = false; }
        else { this.inPkt = true; this.rx = []; }
        continue;
      }
      if (!this.inPkt) continue;
      if (this.esc) { this.rx.push(b === 0xdc ? 0xc0 : 0xdb); this.esc = false; }
      else if (b === 0xdb) this.esc = true;
      else this.rx.push(b);
    }
  }

  reply(op, val = 0, data = new Uint8Array(0), status = 0, err = 0) {
    const st = new Uint8Array(this.statusLen); st[0] = status; st[1] = err;
    const body = new Uint8Array(data.length + st.length); body.set(data); body.set(st, data.length);
    const pkt = new Uint8Array(8 + body.length);
    pkt[0] = 1; pkt[1] = op; pkt[2] = body.length & 0xff; pkt[3] = body.length >> 8;
    new DataView(pkt.buffer).setUint32(4, val >>> 0, true);
    pkt.set(body, 8);
    setImmediate(() => this.send(slipEncode(pkt), `resp op=0x${op.toString(16)}`));
  }

  finishDefl() {
    if (!this.defl || !this.defl.parts.length) return;
    const raw = inflateSync(Buffer.concat(this.defl.parts));
    this.flash.set(raw, this.defl.off);
    this.log.push({ t: now(), ev: 'flash-written', off: this.defl.off, len: raw.length, md5: createHash('md5').update(raw).digest('hex') });
    this.defl = null;
  }

  readReg(a) {
    if (a === this.chip.spiBase && this.busyReads > 0) { this.busyReads--; return 1 << 18; }
    return this.regs.get(a) ?? 0;
  }

  handle(p) {
    if (this.mode !== 'rom' && this.mode !== 'stub') return; // not in a loader: ignore
    const dv = new DataView(p.buffer, p.byteOffset, p.byteLength);
    const op = p[1], size = dv.getUint16(2, true), chk = dv.getUint32(4, true);
    const data = p.subarray(8, 8 + size);
    const w = (i) => new DataView(data.buffer, data.byteOffset).getUint32(i * 4, true);
    const xor = (d) => d.reduce((s, b) => s ^ b, 0xef);
    this.log.push({ t: now(), ev: 'cmd', op, size, chk, words: Array.from({ length: Math.min(6, size >> 2) }, (_, i) => w(i)) });
    switch (op) {
      case 0x08: { // SYNC
        const val = this.mode === 'rom' ? this.romSyncVal : 0;
        for (let i = 0; i < 8; i++) this.reply(op, val);
        return;
      }
      case 0x0a: return this.reply(op, this.readReg(w(0)));
      case 0x09: { // WRITE_REG (address,value,mask,delay)*
        for (let i = 0; i + 16 <= size; i += 16) {
          const a = w(i / 4), v = w(i / 4 + 1);
          this.regs.set(a, v);
          if (a === this.chip.spiBase && (v & (1 << 18))) { // SPI_CMD_USR: run "RDID", clear busy bit
            this.regs.set(this.chip.spiBase + this.chip.w0, this.flashId);
            this.regs.set(a, 0);
            this.busyReads = this.polls;
          }
        }
        return this.reply(op);
      }
      case 0x05: return this.reply(op);
      case 0x07: return this.reply(op, 0, undefined, xor(data.subarray(16)) === chk ? 0 : 1, xor(data.subarray(16)) === chk ? 0 : 7);
      case 0x06: {
        this.reply(op);
        setTimeout(() => { this.mode = 'stub'; this.send(slipEncode(new TextEncoder().encode('OHAI')), 'OHAI'); }, 2);
        return;
      }
      case 0x0d: case 0x0b: case 0x0f: return this.reply(op);
      case 0xd0: this.flash.fill(0xff); return this.reply(op);
      case 0x02: return this.reply(op);
      case 0x10: this.defl = { size: w(0), blocks: w(1), bs: w(2), off: w(3), parts: [], seq: 0 }; return this.reply(op);
      case 0x11: {
        const payload = data.subarray(16);
        const ok = xor(payload) === chk && this.defl && w(1) === this.defl.seq;
        if (ok) { this.defl.parts.push(Buffer.from(payload)); this.defl.seq++; }
        return this.reply(op, 0, undefined, ok ? 0 : 1, ok ? 0 : (this.mode === 'stub' ? 0xc1 : 7));
      }
      case 0x12: this.finishDefl(); return this.reply(op);
      case 0x13: {
        this.finishDefl(); // the real stub has already written every block by the time MD5 is asked
        const md5 = createHash('md5').update(this.flash.subarray(w(0), w(0) + w(1))).digest();
        return this.reply(op, 0, this.mode === 'stub' ? md5 : new TextEncoder().encode(md5.toString('hex')));
      }
      default: return this.reply(op, 0, undefined, 1, this.mode === 'stub' ? 0xff : 5);
    }
  }
}

// Minimal Web Serial SerialPort look-alike (open/close/readable/writable/setSignals/getInfo).
export class FakeSerialPort {
  constructor(esp, log, info = { usbVendorId: 0x10c4, usbProductId: 0xea60 }) {
    this.esp = esp; this.log = log; this.info = info;
    this._readable = null; this._writable = null; this.opened = false; this.ctrl = null;
    esp.send = (bytes, why) => {
      this.log.push({ t: now(), ev: 'rx', why, len: bytes.length, hex: hex(bytes) });
      if (this.ctrl) { try { this.ctrl.enqueue(Uint8Array.from(bytes)); } catch { /* stream gone */ } }
    };
  }
  getInfo() { return this.info; }
  _makeReadable() {
    this._readable = new ReadableStream({
      start: (c) => { this.ctrl = c; },
      cancel: () => { this.log.push({ t: now(), ev: 'readable-cancel' }); this.ctrl = null; this._readable = null; },
    });
  }
  get readable() { if (!this.opened) return null; if (!this._readable) this._makeReadable(); return this._readable; }
  get writable() { return this.opened ? this._writable : null; }
  async open(opts) {
    this.log.push({ t: now(), ev: 'open', opts: Object.fromEntries(Object.entries(opts).filter(([, v]) => v !== undefined)) });
    this.opened = true;
    this._makeReadable();
    this._writable = new WritableStream({
      write: (chunk) => {
        const b = Uint8Array.from(chunk);
        this.log.push({ t: now(), ev: 'tx', len: b.length, hex: hex(b) });
        this.esp.write(b);
      },
    });
  }
  async close() { this.log.push({ t: now(), ev: 'close' }); this.opened = false; this._readable = null; this._writable = null; this.ctrl = null; }
  async setSignals(sig) { this.log.push({ t: now(), ev: 'signals', ...sig }); this.esp.signals(sig); }
}
