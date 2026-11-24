// Records what MeshCore's own setup tool, config.meshcore.io (lib/serial-cli.js at aff42ad8, MIT, Copyright (c)
// 2026 Rastislav Vysoky), writes for each command and which reply it takes from a byte stream, so the Go reply
// parser (internal/nodecli findReply) can be compared with it. The tool's file runs unmodified: its SerialCLI gets
// a stand-in port and writer, and the stream is fed to its processIncomingData, once whole and once a byte at a
// time (the browser hands over reads in any size).
//
//   node record.mjs <config.meshcore.io checkout> streams.json > golden.json
//
// streams.json comes from the Go test (go test ./internal/nodecli -run TestConfigTool -update-streams).
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import path from 'node:path';

const [dir, streamsPath] = process.argv.slice(2);
if (!dir || !streamsPath) {
  console.error('usage: node record.mjs <config.meshcore.io checkout> streams.json > golden.json');
  process.exit(2);
}
const { SerialCLI } = await import(pathToFileURL(path.join(dir, 'lib/serial-cli.js')).href);

// feed runs one command through a fresh SerialCLI and gives what it wrote and the reply it resolved with, or null
// when it hadn't resolved once every byte was in (it would time out after 5 s).
async function feed(cmd, rx, bytewise) {
  const cli = new SerialCLI();
  const written = [];
  cli.port = {};
  cli.writer = { write: async (d) => { written.push(...d); } };
  let reply = null;
  const done = cli.sendCommand(cmd).then((r) => { reply = r; }, () => {});
  await new Promise((r) => setImmediate(r)); // let executeNextCommand write
  const text = new TextDecoder().decode(rx);
  if (bytewise) {
    for (const ch of text) cli.processIncomingData(ch);
  } else {
    cli.processIncomingData(text);
  }
  await Promise.race([done, new Promise((r) => setImmediate(r))]);
  return { tx: Buffer.from(written).toString('hex'), reply };
}

const cases = JSON.parse(readFileSync(streamsPath, 'utf8'));
for (const c of cases) {
  const rx = Buffer.from(c.rx, 'hex');
  const whole = await feed(c.cmd, rx, false);
  const bytewise = await feed(c.cmd, rx, true);
  c.tx = whole.tx;
  c.whole = whole.reply;
  c.bytewise = bytewise.reply;
}
process.stdout.write(JSON.stringify(cases, null, 1) + '\n');
// The unresolved commands' 5 s timers would keep Node alive; everything is recorded.
process.exit(0);
