# reference: what the web flasher sends, recorded

meshflash must send the same bytes as MeshCore's web flasher (flasher.meshcore.dev at commit 45a5257). These
harnesses run the flasher's own JavaScript in Node 22, unmodified, against fake ports and fake boards, and record
everything it does: every byte, every DTR/RTS change, every sleep, and every file it picks from the catalog. The
recordings are committed in each package's `testdata/`, and the Go tests compare the Go code with them. Tests never
run Node or touch the network; only re-recording does.

| Folder | Runs | Records into | Go test |
| --- | --- | --- | --- |
| `catalog/` | `js/catalog.js` and `js/flash.js` (`loadCatalog`, `pickFlashFile`, `esp32Address`) over saved copies of the live catalogs | `internal/catalog/testdata/golden-*.json.gz` | `golden_test.go` |
| `esp32/` | `js/flash.js` `flashEsp32` and `lib/esp32.js` (esptool-js 0.5.4) against a fake ESP32 | `internal/esp/testdata/*.jsonl.gz` | `reference_test.go` |
| `nrf52/` | `lib/dfu.js` (vendored byte for byte in `nrf52/vendor/`) against a fake Adafruit bootloader | `internal/nrfdfu/testdata/*.jsonl*` | `reference_test.go` |

The setup console has no web-flasher code to record (its console only sends what you type). Its reference is
MeshCore's config tool, config.meshcore.io; `internal/nodecli/testdata/configtool/README.md` says how to record it.

Each folder's README says what its harness changes around the reference (only the fakes and the recording) and how
the Go test compares. Where Go departs from the reference on purpose (SPEC.md 6), the test checks that departure
too, so it can't creep in anywhere else.

## Re-recording

```bash
make reference          # from the repository root: fetches the flasher at 45a5257 into reference/.flasher, then records
git status              # nothing should change unless the reference or a fake changed
make test
```

Node 22 (`/opt/node22/bin/node` in the cloud session) and Python 3 are needed. `fetch-flasher.sh` clones the
flasher into `.flasher/` (git-ignored) and checks out the pinned commit; it is the only step that uses the network.
Re-recording twice gives byte-identical files: timestamps are left out and gzip runs with `-n`.

## Licences

The flasher's code is MIT (Copyright (c) 2025 Rastislav Vysoky). `nrf52/vendor/zip.min.js` is zip.js 2.7.57,
BSD-3-Clause; `nrf52/vendor/README.md` has both texts. The ESP32 recordings contain the bytes of Espressif's legacy
flasher stub (GPL-2.0-or-later), whose source is in `third_party/`. See `THIRD_PARTY_NOTICES`.
