// Records the catalog golden for internal/catalog: runs the web flasher's own js/catalog.js (loadCatalog,
// roleValue, renderNotice, hasVersions) and js/flash.js (pickFlashFile, esp32Address) from flasher.meshcore.dev at
// 45a5257, unmodified (MIT, (c) 2025 Rastislav Vysoky), over saved copies of the catalog and /releases lists, and
// writes what they produce as gzipped JSON. The Go package must resolve the same inputs to exactly the same result.
//
// usage: node run.mjs <flasher-root> <host> <out.json.gz> <request>=<input.json.gz> ...
//   <host> is what js/site.js sees as location.host: flasher.meshcore.io loads /config.json,
//   zephcore.meshcore.dev loads /config-zephcore.json.
//   Each <request> is a URL exactly as js/catalog.js fetches it ("/config.json", "/releases?repo=github"); the
//   fetch shim serves it from the gzipped file. Anything else answers 404, which aborts loadCatalog as on the site.
//
// Every input is data: nothing in it is run.
import { register } from 'node:module';
import { readFileSync, writeFileSync } from 'node:fs';
import { gunzipSync, gzipSync } from 'node:zlib';
import { createHash } from 'node:crypto';
import { resolve as resolvePath } from 'node:path';

const [rootArg, host, outPath, ...routes] = process.argv.slice(2);
if (!rootArg || !host || !outPath || routes.length === 0) {
  console.error('usage: node run.mjs <flasher-root> <host> <out.json.gz> <request>=<input.json.gz> ...');
  process.exit(2);
}
// The hook and the file: URLs below need an absolute path.
const root = resolvePath(rootArg);

register(new URL('./hooks.mjs', import.meta.url), { data: { root } });

// js/site.js reads location.host and location.search when it is first imported.
globalThis.location = { host, search: '', pathname: '/' };

const served = new Map();
const inputs = {};
for (const route of routes) {
  // The request itself contains "=" (/releases?repo=github), the file name doesn't: split at the last one.
  const at = route.lastIndexOf('=');
  const request = route.slice(0, at);
  const body = gunzipSync(readFileSync(route.slice(at + 1)));
  served.set(request, body);
  inputs[request] = createHash('sha256').update(body).digest('hex');
}

// The browser's fetch, as js/util.js fetchJson uses it: ok, status, json().
const requests = [];
globalThis.fetch = async (url) => {
  requests.push(String(url));
  const body = served.get(String(url));
  if (!body) return new Response('not found', { status: 404 });
  return new Response(body, { status: 200, headers: { 'content-type': 'application/json' } });
};

const { loadCatalog, hasVersions, roleValue, renderNotice } = await import(`file://${root}/js/catalog.js`);
const { pickFlashFile, esp32Address } = await import(`file://${root}/js/flash.js`);

const config = await loadCatalog();

// What the reference would flash for every version it offers: the "Flash!" button with the ESP32 "Erase device"
// box unticked (update) and ticked (wipe; eraseAll is the same box), and the nRF52 path (it ignores the box).
const pick = (file, esp32) => {
  if (!file) return null;
  const p = { type: file.type, name: file.name, title: file.title };
  if (esp32) p.address = esp32Address(file);
  return p;
};

const firmware = [];
config.device.forEach((device, d) => {
  device.firmware.forEach((fw, f) => {
    if (!hasVersions(fw)) return;
    const versions = [];
    for (const [name, version] of Object.entries(fw.version)) {
      if (version.files.length === 0) continue;
      const v = { version: name };
      if (device.type === 'esp32') {
        v.update = pick(pickFlashFile(version.files, { esp32: true, wipe: false }), true);
        v.wipe = pick(pickFlashFile(version.files, { esp32: true, wipe: true }), true);
      } else if (device.type === 'nrf52') {
        v.flash = pick(pickFlashFile(version.files, { esp32: false, wipe: false }), false);
      }
      versions.push(v);
    }
    firmware.push({
      device: d,
      deviceName: device.name,
      deviceType: device.type,
      firmware: f,
      role: fw.role,
      title: roleValue(config, fw, 'title'),
      subTitle: roleValue(config, fw, 'subTitle'),
      notice: renderNotice(config, device, fw),
      versions,
    });
  });
});

const out = {
  recordedWith: 'flasher.meshcore.dev 45a5257 js/catalog.js + js/flash.js, reference/catalog/run.mjs',
  host,
  inputs,
  requests,
  resolved: config,
  firmware,
};
writeFileSync(outPath, gzipSync(JSON.stringify(out, null, 1) + '\n', { level: 9 }));
console.log(`${outPath}: ${config.device.length} devices, ${firmware.length} firmware entries with versions, ` +
  `requests ${requests.join(' ')}`);
process.exit(0);
