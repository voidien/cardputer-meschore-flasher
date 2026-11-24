// Runs the web flasher's lib/dfu.js (vendor/dfu.js, a byte-identical copy from flasher.meshcore.dev@45a5257) in
// Node 22 against a fake Web Serial port and a fake Adafruit serial-DFU bootloader, and records every open, write,
// sleep, read timeout and close as JSON lines. internal/nrfdfu's tests compare the Go code's recording with these.
//
//   node harness.mjs <dfu.zip> <out.jsonl> [scenario]
//
// scenario: ok (default) | touch | drop-ack:N | bad-ack:N | bad-crc
//   touch       Dfu.forceDfuMode(port): the 1200-baud touch (the zip is not read)
//   drop-ack:N  the N-th frame the bootloader receives (from 0) is dropped: not processed, not acknowledged
//   bad-ack:N   the N-th frame is processed, but its ACK carries the wrong number (expected + 3)
//   bad-crc     the second frame (INIT) is dropped as if its CRC were wrong
//
// Copied from the research prototype (meshflash-research/proto-nrf52/harness.mjs) and changed so a recording is the
// same on every run: no wall-clock times; dfu.js's 5000 ms read timeout is recorded as a "read-timeout" event and
// fires after 200 ms of real time instead of 5 s (a fake ACK always arrives within the same microtask turn, so the
// timer only ever fires when the fake stays silent); and the fake bootloader's verdict at STOP is recorded.
import fs from 'node:fs';

const [zipPath, outPath, scenario = 'ok'] = process.argv.slice(2);
const events = [];
const ev = (e) => events.push(e);

// --- Timer shim: record every sleep() and run it at once; dfu.js's read timeout is recorded when it fires ---
const READ_TIMEOUT_MS = 5000; // DEFAULT_SERIAL_PORT_TIMEOUT * 1000 * 5 (dfu.js:140)
const realSetTimeout = globalThis.setTimeout;
globalThis.setTimeout = (fn, ms, ...args) => {
  // The read timer is the setTimeout inside getReader().read() (dfu.js:136-140); every other one is sleep().
  if (ms === READ_TIMEOUT_MS && /at Object\.read \(.*dfu\.js:/.test(new Error().stack)) {
    return realSetTimeout(() => { ev({ t: 'read-timeout', ms }); fn(...args); }, 200);
  }
  ev({ t: 'sleep', ms });
  return realSetTimeout(fn, 0, ...args);
};

const { Dfu } = await import('./vendor/dfu.js');
const zip = await import('./vendor/zip.min.js');
zip.configure({ useWebWorkers: false });

// --- CRC16 (bootloader crc16_compute = CRC-16/CCITT-FALSE) ---
function crc16(buf, crc = 0xFFFF) {
  for (const b of buf) {
    crc = ((crc >> 8) & 0xFF) | ((crc << 8) & 0xFF00);
    crc ^= b;
    crc ^= (crc & 0xFF) >> 4;
    crc ^= (crc << 12) & 0xFFFF;
    crc ^= ((crc & 0xFF) << 5) & 0xFFFF;
  }
  return crc & 0xFFFF;
}

// --- Fake bootloader (mirrors hci_slip.c + hci_transport.c + dfu_transport_serial.c + dfu_single_bank.c) ---
class FakeBootloader {
  constructor(onAck) {
    this.onAck = onAck;
    this.state = 'wait_start'; this.buf = []; this.esc = false;
    this.expected = 1;          // INITIAL_ACK_NUMBER_EXPECTED
    this.rxCount = 0; this.dfu = { st: 'IDLE', init: [], data: [] };
    this.log = [];
  }
  feed(bytes) {
    for (const b of bytes) {
      if (this.state === 'wait_start') { if (b === 0xC0) this.state = 'rx'; continue; }
      if (this.esc) { this.esc = false; this.buf.push(b === 0xDC ? 0xC0 : b === 0xDD ? 0xDB : b); continue; }
      if (b === 0xDB) { this.esc = true; continue; }
      if (b === 0xC0) { if (this.buf.length) this.frame(Uint8Array.from(this.buf)); this.buf = []; continue; }
      this.buf.push(b);
    }
  }
  frame(p) {
    const n = this.rxCount++;
    const valid = p.length > 4 && (p[0] & 0x40) && (p[0] & 0x80) && (p[1] & 0x0F) === 14 &&
      ((p[0] + p[1] + p[2] + p[3]) & 0xFF) === 0 &&
      crc16(p.subarray(0, p.length - 2)) === (p[p.length - 2] | (p[p.length - 1] << 8));
    const lenField = (p[1] >> 4) | (p[2] << 4);
    const seq = p[0] & 7;
    if (!valid) { this.log.push({ n, drop: 'invalid' }); return; }                // no ACK
    if (scenario === 'bad-crc' && n === 1) { this.log.push({ n, drop: 'simulated-crc' }); return; }
    if (scenario === `drop-ack:${n}`) { this.log.push({ n, drop: 'simulated-no-ack' }); return; }
    if (seq !== this.expected) { this.ack(); this.log.push({ n, dup: seq }); return; } // re-ACK current expected
    this.expected = (this.expected + 1) & 7;
    if (scenario === `bad-ack:${n}`) { this.expected = (this.expected + 3) & 7; }
    this.ack();
    this.dfuPacket(p.subarray(4, p.length - 2), lenField);
  }
  ack() {
    const h = [this.expected << 3, 0, 0, 0]; h[3] = (~(h[0] + h[1] + h[2]) + 1) & 0xFF;
    const out = [0xC0]; for (const b of h) { if (b === 0xC0) out.push(0xDB, 0xDC); else if (b === 0xDB) out.push(0xDB, 0xDD); else out.push(b); }
    out.push(0xC0);
    queueMicrotask(() => this.onAck(Uint8Array.from(out)));
  }
  dfuPacket(pl, lenField) {
    const type = pl[0];                                  // bootloader looks at byte 0 only
    const words = Math.floor(pl.length / 4) - 1;         // dfu_transport_serial.c:271
    const body = pl.subarray(4, 4 + words * 4);
    const d = this.dfu;
    if (lenField !== pl.length) this.log.push({ warn: 'len-field', lenField, actual: pl.length });
    if (type === 3) {
      const v = new DataView(pl.buffer, pl.byteOffset, pl.length);
      d.start = { mode: v.getUint32(4, true), sd: v.getUint32(8, true), bl: v.getUint32(12, true), app: v.getUint32(16, true) };
      d.st = 'RDY'; this.log.push({ start: d.start, wordSized: d.start.app % 4 === 0 });
    } else if (type === 1) {
      d.init.push(...body); d.st = 'RX_DATA';
      const ip = Uint8Array.from(d.init); const sdn = ip[8] | (ip[9] << 8);
      const ext = ip.subarray(10 + 2 * sdn);
      d.crcExpected = ext[0] | (ext[1] << 8);
      this.log.push({ init_hex: Buffer.from(ip).toString('hex'), dev_type: ip[0] | (ip[1] << 8), sd_req: [...Array(sdn)].map((_, i) => ip[10 + 2 * i] | (ip[11 + 2 * i] << 8)), ext_len: ext.length, crc: d.crcExpected });
    } else if (type === 4) {
      d.data.push(body);
    } else if (type === 5) {
      const img = Buffer.concat(d.data.map(x => Buffer.from(x)));
      const ok = img.length === d.start.app && crc16(img) === d.crcExpected;
      this.log.push({ stop: true, received: img.length, crc: crc16(img), valid: ok });
    } else this.log.push({ unknown_type: type });
  }
}

// --- Fake Web Serial port ---
class FakePort {
  constructor() { this.readable = null; this.writable = null; this.boot = null; }
  async open(opts) {
    ev({ t: 'open', opts });
    if (scenario === 'touch') { this.readable = new ReadableStream(); this.writable = new WritableStream(); return; }
    let ctl; this.readable = new ReadableStream({ start(c) { ctl = c; } });
    this.boot = new FakeBootloader((bytes) => { try { ctl.enqueue(bytes); } catch {} });
    this.writable = new WritableStream({
      write: (chunk) => { ev({ t: 'write', len: chunk.length, hex: Buffer.from(chunk).toString('hex') }); this.boot.feed(chunk); },
      close: () => ev({ t: 'writer-close' }),
    });
  }
  async close() { ev({ t: 'close' }); this.readable = null; this.writable = null; }
}

const port = new FakePort();
let error = null;
try {
  if (scenario === 'touch') {
    await Dfu.forceDfuMode(port);
  } else {
    const blob = new Blob([fs.readFileSync(zipPath)]);
    const dfu = new Dfu(port);
    const progress = [];
    await dfu.dfuUpdate(blob, (p) => progress.push(p));
    ev({ t: 'done', progressLast: progress.at(-1), progressCount: progress.length });
  }
} catch (e) { error = String(e && e.message || e); ev({ t: 'error', message: error }); }

// What the fake bootloader made of it: START's sizes, the init packet, and the image check at STOP.
if (port.boot) {
  for (const l of port.boot.log) {
    if ('start' in l || 'init_hex' in l || 'stop' in l || 'warn' in l) ev({ t: 'bootloader', ...l });
  }
}

fs.writeFileSync(outPath, events.map(e => JSON.stringify(e)).join('\n') + '\n');
const writes = events.filter(e => e.t === 'write');
const sleeps = events.filter(e => e.t === 'sleep');
console.log(JSON.stringify({
  scenario, error, writes: writes.length, bytesWritten: writes.reduce((a, w) => a + w.len, 0),
  sleeps: sleeps.length, sleepTotalMs: +sleeps.reduce((a, s) => a + s.ms, 0).toFixed(3),
  distinctSleeps: [...new Set(sleeps.map(s => s.ms))],
  bootLog: port.boot ? port.boot.log.slice(0, 6).concat(port.boot.log.slice(-3)) : null,
}));
