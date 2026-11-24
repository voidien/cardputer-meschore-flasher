// Makes the small synthetic ESP images the recordings flash. No real firmware is committed: each image is an ESP
// image header (magic E9, one segment, the chip's IMAGE_CHIP_ID) and pseudo-random bytes from a fixed seed, laid
// out as MeshCore's files are (docs/meshcore-flashing.md 4.9):
//
//   esp32-app.bin     ESP32 app, written at 0x10000; 5002 bytes, so it needs 2 bytes of 0xFF padding
//   esp32-merged.bin  ESP32 merged image, written at 0x0: 0xFF up to 0x1000, then a bootloader with chip 0
//   s3-app.bin        ESP32-S3 app (chip 9) at 0x10000; 36 KB of noise, so the deflate stream is 3 blocks
//   c3-merged.bin     ESP32-C3 merged image (chip 5): bootloader at 0x0
//
// usage: node mkimages.mjs <out dir>
import { writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';

const dir = process.argv[2];
mkdirSync(dir, { recursive: true });

// A fixed LCG (Numerical Recipes constants), so every run makes the same bytes.
function noise(seed, n) {
  const out = new Uint8Array(n);
  let s = seed >>> 0;
  for (let i = 0; i < n; i++) {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    out[i] = s >>> 24;
  }
  return out;
}

// esp_image_header_t (24 bytes) + one segment header + data.
function image(chipId, entry, load, data) {
  const b = new Uint8Array(24 + 8 + data.length);
  const dv = new DataView(b.buffer);
  b[0] = 0xe9; b[1] = 1; b[2] = 2 /* DIO */; b[3] = 0x20 /* 4 MB, 40 MHz */;
  dv.setUint32(4, entry, true);
  b[8] = 0xee; // no WP pin
  dv.setUint16(12, chipId, true);
  dv.setUint16(17, 0xffff, true); // max chip revision: any
  dv.setUint32(24, load, true);
  dv.setUint32(28, data.length, true);
  b.set(data, 32);
  return b;
}

const ff = (n) => new Uint8Array(n).fill(0xff);
const cat = (...parts) => Buffer.concat(parts.map((p) => Buffer.from(p)));

writeFileSync(join(dir, 'esp32-app.bin'), image(0, 0x400d1a2c, 0x3f400020, noise(1, 4970)));
writeFileSync(join(dir, 'esp32-merged.bin'), cat(ff(0x1000), image(0, 0x40080624, 0x3fff0030, noise(2, 2999))));
writeFileSync(join(dir, 's3-app.bin'), image(9, 0x40375a1c, 0x3c030020, noise(3, 36001)));
writeFileSync(join(dir, 'c3-merged.bin'), image(5, 0x403ce000, 0x3fcd5820, noise(4, 5000)));
