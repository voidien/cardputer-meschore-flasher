# nRF52 reference recordings

The recordings `internal/nrfdfu` is tested against: the web flasher's own `lib/dfu.js` (flasher.meshcore.dev at
45a5257, vendored byte for byte in `vendor/`, see `vendor/README.md`) run in Node against a fake Web Serial port and
a fake Adafruit serial-DFU bootloader, recording every open, write, sleep, read timeout and close. The Go tests run
`internal/nrfdfu` against `FakeBootloader`, the Go port of the same fake, and must write the same bytes and sleep
the same durations in the same order (docs/meshcore-flashing.md 9.2 and 9.3).

## Re-recording

```bash
cd reference/nrf52
make record NODE=/opt/node22/bin/node   # Node 22 and Python 3; nothing touches the network
cd ../.. && go test ./internal/nrfdfu/
```

`make record` checks the vendored files' hashes, rebuilds the synthetic packages with `make_zips.py`, and writes
into `internal/nrfdfu/testdata`:

| Recording | Package | Scenario | What it shows |
| --- | --- | --- | --- |
| `tiny-1028.jsonl` | `tiny-1028.zip` | ok | 3 DATA frames with 0xC0 and 0xDB bytes to escape; the 500 ms erase floor |
| `tiny-4096.jsonl` | `tiny-4096.zip` | ok | exactly 8 DATA frames: two 102.4 ms sleeps back to back before STOP |
| `synth-40000.jsonl.gz` | `synth-40000.zip` | ok | 79 DATA frames, the sequence number wrapping ten times, a 965.677 ms erase wait, deflated zip entries |
| `touch.jsonl` | none | touch | `Dfu.forceDfuMode`: open at 1200, 100 ms, close, 1500 ms |
| `synth-40000-drop-ack-0.jsonl` | `synth-40000.zip` | drop-ack:0 | no ACK for START: dfu.js fails with "Read timeout" after its 5 s read timeout |
| `synth-40000-drop-ack-1.jsonl` | `synth-40000.zip` | drop-ack:1 | the same for INIT, after the erase wait |
| `synth-40000-bad-ack-2.jsonl` | `synth-40000.zip` | bad-ack:2 | a wrong ACK number for DATA#1: "Invalid ACK sequence. Expected 4, got 7" at once |
| `sd-bl.jsonl` | `sd-bl.zip` | ok | a SoftDevice + bootloader package: dfu.js opens the port, then throws a TypeError |

Each line is one event: `open {opts}`, `write {len, hex}`, `sleep {ms}`, `read-timeout {ms}`, `writer-close`,
`close`, `error {message}`, `done`, and at the end `bootloader` lines with what the fake made of START, the init
packet and the image at STOP. `python3 -I summarize.py <recording>` prints one as a listing of frames.

The same inputs give the same files: the zips have fixed timestamps, and the recordings hold no wall-clock times.
(Deflated entries depend on Python's zlib; the recordings depend only on the files inside.) Only small synthetic
packages are used; nothing here is real firmware.

## How it works

- `harness.mjs <zip> <out.jsonl> [scenario]` (from the research prototype `proto-nrf52/harness.mjs`) imports
  `vendor/dfu.js` unmodified, with zip.js's web workers off. A timer shim records every `setTimeout` dfu.js makes as
  a sleep and runs it at once. The one exception is the 5000 ms timer inside `getReader().read()`: it is recorded
  as a `read-timeout` when it fires, after 200 ms of real time (a fake ACK always arrives in the same turn, so it
  fires only when the fake stays silent).
- The fake bootloader mirrors the OTAFIX bootloader's `hci_slip.c`, `hci_transport.c`, `dfu_transport_serial.c`
  and `dfu_single_bank.c`: it waits for the first 0xC0, drops invalid frames without an ACK, re-ACKs a wrong
  sequence number with the unchanged expected one, ACKs before processing, keeps only whole words of a payload, and
  checks size and CRC-16 at STOP. Scenarios: `drop-ack:N` (frame N dropped unanswered), `bad-ack:N` (frame N's ACK
  is 3 too high), `bad-crc` (INIT dropped), `touch`.
- `make_zips.py` builds the packages laid out like MeshCore's (`adafruit-nrfutil dfu genpkg`, dfu_version 0.5).
- The Go fake (`internal/nrfdfu` `FakeBootloader`) behaves as this one on every recorded scenario, and goes further
  where the recordings can't look: it can refuse an init packet for another SoftDevice and reset, it leaves for the
  app after a good STOP (its port vanishes, as the real bootloader's does; dfu.js has closed the port by then), and
  it has Go-only scenarios (`lose-ack`, `late-ack`, `mute-from`, `unplug-at`) for meshflash's resends.

## Full-size checks

The research folder (`MESHFLASH_SAMPLES=/home/user/meshflash-research`) holds real MeshCore v1.18.0 packages for the
RAK4631 and the Xiao nRF52 and the research harness's recordings of dfu.js flashing them
(`proto-nrf52/out/rak4631-ok.jsonl`, 865 frames). `go test ./internal/nrfdfu/` compares with them only when
`MESHFLASH_SAMPLES` is set; they are never committed. The research also checked that adafruit-nrfutil sends the
same bytes as dfu.js (`proto-nrf52/nrfutil_ref.py`, docs/meshcore-flashing.md 5.8).
