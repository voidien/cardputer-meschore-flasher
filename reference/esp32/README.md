# reference/esp32: the web flasher's ESP32 path, recorded

`internal/esp` must send the same bytes as MeshCore's web flasher. This harness runs the flasher's own
`js/flash.js` and `lib/esp32.js` (esptool-js 0.5.4, unmodified) from `../.flasher` (flasher.meshcore.dev at
45a5257) in Node, against a fake Web Serial port and a fake ESP32, and records every byte, every DTR/RTS change and
every sleep. The recordings go to `internal/esp/testdata/`, where `reference_test.go` compares the Go code with them.

| File | What |
| --- | --- |
| `run.mjs` | runs `flashEsp32` once and writes a `.jsonl` recording (usage at its top) |
| `fake.mjs` | `FakeSerialPort` (the Web Serial calls esptool-js makes) and `FakeEsp`, a ROM bootloader and stub; `internal/esp/fake.go` is its Go port |
| `hooks.mjs` | maps the flasher's absolute imports (`/lib/…`, `/js/…`) onto the checkout, so `js/flash.js` runs unmodified |
| `mkimages.mjs` | makes the small synthetic images flashed (no real firmware is committed) |
| `decode.py` | prints a recording as a command listing: `python3 -I decode.py ../../internal/esp/testdata/esp32-update.jsonl.gz` |
| `Makefile` | `make record` |

These were copied from the research prototype (`proto-esp32/` in the research folder) and extended: sleeps are
recorded (a timer whose callback is a promise's native `resolve` is a sleep; esptool-js's read timeouts and the
fake's own timers pass arrow functions), the fake's register values, flash ID and busy polls are options, and the
wall-clock times are left out so a re-recording only changes when the reference does.

## Re-recording

Needs Node 22 (`/opt/node22/bin/node` in the cloud session) and the flasher checkout:

```bash
../fetch-flasher.sh      # clones flasher.meshcore.dev into ../.flasher at 45a5257 (git-ignored)
make record              # or: make record NODE=/opt/node22/bin/node
```

`make record` writes the images and, for each case, a plain recording and an `MD5=1` one (esptool-js's own
`calculateMD5Hash` switched on: the web flasher never passes it, so this shows where SPI_FLASH_MD5 goes), gzipped
with `gzip -9n` so the files are the same on every run:

| Recording | Chip | Image | Address | Erase | USB PID |
| --- | --- | --- | --- | --- | --- |
| `esp32-update` | ESP32 | `esp32-app.bin` (5002 B, padded to 5004) | 0x10000 | no | 0xea60 (CP2102) |
| `esp32-wipe` | ESP32 | `esp32-merged.bin` (0xFF to 0x1000, bootloader with chip 0) | 0x0 | yes | 0xea60 |
| `s3-update` | ESP32-S3 | `s3-app.bin` (36 KB of noise: 3 deflate blocks) | 0x10000 | no | 0xea60 |
| `c3-jtag-wipe` | ESP32-C3 | `c3-merged.bin` (bootloader at 0x0) | 0x0 | yes | 0x1001 (USB-Serial-JTAG reset) |
| `s3-otg-eco0` | ESP32-S3 | `s3-app.bin` | 0x10000 | no | 0xea60, with UARTDEV_BUF_NO = 3 (0x800 stub blocks), ECO0 eFuses, 2 busy SPI polls |

What the fake pins, so recordings are stable (docs/meshcore-flashing.md 9.1): the boot banner comes in one chunk
(which makes esptool-js send exactly 2 SYNCs); eFuses read 0 unless `REGS` says otherwise; UART_CLKDIV is 347 on
the ESP32 (40 MHz crystal); the flash ID is 0x1740EF (8 MB); SPI_CMD reads busy `POLLS` times (0); it answers the
first reset. `internal/esp`'s `FakeESP` has the same defaults; if you change one here, change it there, and keep
the `recordings` table in `internal/esp/reference_test.go` in step with the Makefile.

The recordings contain the flasher stub's bytes (GPL-2.0-or-later, sent by the reference during MEM_DATA); its
source is in `third_party/esptool-legacy-flasher-stub-v1.3.0/`. The research prototype's full-size recordings of
real MeshCore files are compared too, when `MESHFLASH_SAMPLES` points at the research folder
(`internal/esp/samples_test.go`).
