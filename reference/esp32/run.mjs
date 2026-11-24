// Runs the web flasher's own js/flash.js (+ lib/esp32.js = esptool-js 0.5.4) against FakeSerialPort/FakeEsp
// and records every byte, modem-line change and sleep.
//
// usage: node run.mjs <flasher-root> <chip esp32|esp32s3|esp32c3> <image|-> <address> <eraseAll 0|1> <out.jsonl> [pid]
//
// Environment:
//   MD5=1           make esptool-js verify with SPI_FLASH_MD5 (flash.js itself never asks it to)
//   IGNORE_RESETS=n the fake ignores the first n resets
//   REGS='{"0x3fcef14c":3}'  extra register values for the fake (eFuses, UARTDEV_BUF_NO)
//   POLLS=n         SPI_CMD reads that still see the busy bit after the RDID starts (default 0)
//   FLASH_ID=0x..   the flash's JEDEC ID as W0 holds it (default 0x1740ef: Winbond, 8 MB)
//   TIMES=1         keep the wall-clock "t" of each event (left out by default so recordings are stable)
//   MAX_MS          a safety timeout for the whole run
//
// Copied from the meshflash research prototype (proto-esp32/run.mjs). Added: the sleep recording, the fake's
// options and the stable output.
import { register } from 'node:module';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { FakeEsp, FakeSerialPort, now } from './fake.mjs';

const [rootArg, chip, imagePath, addrStr, eraseStr, outPath, pidStr] = process.argv.slice(2);
const root = resolve(rootArg);
const log = [];

// Record every sleep. esptool-js and flash.js sleep with `new Promise((resolve) => setTimeout(resolve, ms))`,
// so the timer's callback is a promise's native resolve function. Their read timeouts pass an arrow function
// that rejects, and so does the fake, so neither is recorded as a sleep.
const realSetTimeout = globalThis.setTimeout;
globalThis.setTimeout = function (fn, ms, ...args) {
  if (typeof fn === 'function' && Function.prototype.toString.call(fn).includes('[native code]')) {
    log.push({ t: now(), ev: 'sleep', ms });
  }
  return realSetTimeout(fn, ms, ...args);
};

register(new URL('./hooks.mjs', import.meta.url), { data: { root } });
const { flashEsp32 } = await import(`file://${root}/js/flash.js`);
if (process.env.MD5 === '1') {
  // Test-only variant: make esptool-js verify with SPI_FLASH_MD5 (flash.js itself never passes calculateMD5Hash).
  const { ESPLoader } = await import(`file://${root}/lib/esp32.js`);
  const { createHash } = await import('node:crypto');
  const orig = ESPLoader.prototype.writeFlash;
  ESPLoader.prototype.writeFlash = function (o) {
    return orig.call(this, { ...o, calculateMD5Hash: (bstr) => createHash('md5').update(Buffer.from(bstr, 'latin1')).digest('hex') });
  };
}

const esp = new FakeEsp(chip, log, {
  regs: JSON.parse(process.env.REGS ?? '{}'),
  polls: Number(process.env.POLLS ?? 0),
  flashId: Number(process.env.FLASH_ID ?? 0x1740ef),
});
const pid = pidStr ? Number(pidStr) : 0xea60;
esp.jtagLike = pid === 0x1001;
esp.ignoreResets = Number(process.env.IGNORE_RESETS ?? 0);
const port = new FakeSerialPort(esp, log, { usbVendorId: pid === 0x1001 ? 0x303a : 0x10c4, usbProductId: pid });
const image = imagePath === '-' ? new Uint8Array(4096).fill(0x5a) : readFileSync(imagePath);
const terminal = { lines: [], clean() {}, write(s) { this.lines.push(s); }, writeLine(s) { this.lines.push(s + '\n'); } };
let lastPct = -1;
const dump = () => {
  const keepTimes = process.env.TIMES === '1';
  const rows = log.map((l) => { if (keepTimes) return l; const { t, ...rest } = l; return rest; });
  writeFileSync(outPath, rows.map((l) => JSON.stringify(l)).join('\n') + '\n');
  writeFileSync(outPath + '.terminal.txt', terminal.lines.join(''));
};
if (process.env.MAX_MS) realSetTimeout(() => { log.push({ ev: 'error', msg: 'harness MAX_MS reached' }); dump(); process.exit(2); }, Number(process.env.MAX_MS));
try {
  await flashEsp32(port, new Blob([image]), {
    address: Number(addrStr), eraseAll: eraseStr === '1', terminal,
    onProgress: (p) => { const q = Math.floor(p); if (q !== lastPct && q % 25 === 0) { lastPct = q; log.push({ ev: 'progress', pct: q }); } },
    onLoader: () => {},
  });
  log.push({ ev: 'done' });
} catch (e) {
  log.push({ ev: 'error', msg: String(e) });
}
dump();
console.log(outPath, 'events', log.length, 'last', JSON.stringify(log.at(-1)));
process.exit(0);
