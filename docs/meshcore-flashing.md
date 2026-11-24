# MeshCore flashing: how the web flasher does it, and what our Go app must do

Written 2026-11-24; corrected the same day after two adversarial reviews (sections 2-11). Audience: the Go programmer building the CardputerZero flasher app, and the owner.

This document is built from six research reports: catalog, hosting and licences, ESP32, nRF52, serial CLI, and USB IDs. Nothing here was tested on real hardware. Every fact names its source.

**How to read the citations**

- `flasher@45a5257 js/flash.js:26-34` means file `js/flash.js`, lines 26-34, of the repository `flasher` at commit `45a5257`. Section 1.1 lists every repository and commit.
- When no commit is given, `flasher` means 45a5257, `MeshCore` means 75b04ef0, and `ejs` means esptool-js tag v0.5.4 (ce2c3f4).
- Local copies live under `scratchpad/src/` (full path `/tmp/claude-0/-home-user-cardputer-meschore-flasher/681ca635-dba8-545f-af86-3226b3104afe/scratchpad/src`). The full research reports are in `scratchpad/research/`. **The scratchpad is temporary.** Copy anything you need into the repository before it is cleared.
- **UNVERIFIED** means no source could confirm the fact. **(source inference)** means it is derived from source code but nobody has seen it on hardware.
- "The reference" means the web flasher's JavaScript at flasher@45a5257.

## Contents

0. The short version
1. Sources and licences
2. The flasher sites
3. The catalog
4. ESP32 flashing
5. nRF52 flashing
6. Board identification
7. Setup over the serial console
8. FAQ 5.13 in short
9. Recording the reference
10. Recovery
11. Open questions for the owner

---

## 0. The short version

1. **Read the live catalog.** `https://flasher.meshcore.io/config.json` has 70 devices. The git copy at flasher@45a5257 has 67, and one of them is a broken merge stub. The repo history is a series of "sync prod -> git" commits, so production is the truth (section 3.11).
2. **Versions come from a second request.** For each firmware the flasher calls `GET /releases?repo=<key>` and matches file names with unanchored regexes, in JSON key order (`flasher js/catalog.js:26-69`). Go's `regexp` gives exactly the same matches (checked on 4041 pairs). Go maps lose key order, so decode in order (section 3.4).
3. **ESP32 reference = esptool-js 0.5.4, unmodified.** It always uploads a GPL-2.0-or-later flasher stub, runs at 115200 baud, never checks status bytes and never verifies the flash. "Update" writes the app `.bin` at 0x10000. "Erase device" writes the `-merged.bin` at 0x0 after a full chip erase (section 4).
4. **Compressed bytes differ in Go.** pako level 9 equals zlib 1.3 level 9. Go's `compress/zlib` gives a different stream (803,567 vs 781,373 bytes on one image). Byte-equal tests need a zlib-identical deflater or an injected stream (section 4.6).
5. **nRF52 reference = dfu.js, byte-identical to adafruit-nrfutil.** The bootloader erases the app at START and only checks the image at INIT. A wrong zip therefore leaves the board with no app. On the OTAFIX bootloader the board then comes up in Bluetooth-only DFU with no USB port, and only a double-press of reset brings USB back. A CRC failure at STOP is silent (section 5).
6. **Never write the catalog's `erase` or `bootloader` files.** Refuse any DFU zip whose manifest lacks `application` or has `softdevice`, `bootloader` or `softdevice_bootloader` (section 3.9).
7. **Setup works only on repeater and room-server firmware.** Send one command at a time, ending in CR. `set tx` accepts any value. `password` accepts an empty value, which opens admin access to anyone (section 7).
8. **USB IDs only narrow the choice.** Radio variants cannot be told apart over USB. The user must always confirm the board. The owner's user `void` is not in `dialout` yet (section 6).
9. **The catalog hides a safety warning.** The T1000-E entry carries "Please do not flash MeshCore firmware to LoRaWAN version of the t1000e, it can permantently brick the device" (sic), but the web flasher never shows it (section 3.10).
10. **Nothing in the catalog or `/releases` carries a size, hash or signature.** The app must check files itself (section 2.6).

---

## 1. Sources and licences

### 1.1 Repositories

| Short name | Repository | Commit (date) | Licence | Used for |
|---|---|---|---|---|
| flasher | github.com/meshcore-dev/flasher.meshcore.dev (same HEAD as meshcore-dev/flasher.meshcore.io and recrof/MeshCoreFlasher, by `git ls-remote`) | 45a5257bf792 (2026-09-26) | MIT, (c) 2025 Rastislav Vysoky (`LICENSE:1-3`) | the reference JS and the git catalog |
| MeshCore | github.com/meshcore-dev/MeshCore | 75b04ef06def (2026-11-14). The CLI files equal tag `repeater-v1.18.0` = 81dbae59 | MIT, (c) 2025 Scott Powell / rippleradios.com (`license.txt:1-3`) | firmware, variants, docs, CLI |
| ejs | github.com/espressif/esptool-js, tag v0.5.4 | ce2c3f4 (2025-01-14); HEAD a07ee04 also read | Apache-2.0. `LICENSE:190` says "Copyright 2019 Espressif Systems (Shanghai) CO LTD"; only `README.md:83` says 2023. Use the LICENSE line | what `flasher lib/esp32.js` is |
| esptool | github.com/espressif/esptool | 70637fa (v5.7.0); tags v4.8.0 = fbb74ec, v4.8.1 = 1e9df29 | GPL-2.0-or-later (SPDX header `esptool/__init__.py:4`; `LICENSE` is the GPLv2 text) | protocol facts only; **do not port** |
| legacy stub | github.com/espressif/esptool-legacy-flasher-stub, tag v1.3.0 | 624b5e7 | GPL-2.0-or-later | the stub the reference uploads |
| esp-flasher-stub | github.com/espressif/esp-flasher-stub (+ submodule esp-stub-lib 59c006b) | 232a1e3 | Apache-2.0 OR MIT | licence-clean stub alternative |
| esp-pylib | github.com/espressif/esp-pylib | d21553f | Apache-2.0 (`LICENSE:1-3`) | reset sequences, USB constants |
| nrfutil | github.com/adafruit/Adafruit_nRF52_nrfutil | 7fdfe15 | BSD-3-Clause, (c) 2015 Nordic Semiconductor (`license.txt:1-27`) | what `flasher lib/dfu.js` ports |
| bootloader | github.com/adafruit/Adafruit_nRF52_Bootloader | HEAD 0ee8343; tags 0.9.2 = eefbe6b, 0.6.1 = 97fbda5 | MIT, (c) 2016 Adafruit | bootloader behaviour |
| OTAFIX | oltaco Adafruit_nRF52_Bootloader_OTAFIX | tag 0.9.2-OTAFIX2.3-BP1.4 = a62825b; 0.9.2-OTAFIX2.2-BP1.3 = 0c0ef40 | MIT, (c) 2016 Adafruit Industries (`LICENSE:1-3`) | the bootloader the catalog ships |
| nrf-core | meshcore-Adafruit_nRF52_Arduino (pinned at `MeshCore/platformio.ini:88`) and its TinyUSB submodule Adafruit_TinyUSB_Arduino | d541301; 7b30ff9 | | 1200-baud touch, DTR rule |
| nRF52-Flash-Format | github.com/meshcore-dev/nRF52-Flash-Format | 03eb1d8 | MIT | what the `erase` zips contain |
| config tool | github.com/meshcore-dev/config.meshcore.io | aff42ad8 (2026-09-27) | MIT, (c) 2026 Rastislav Vysoky (`LICENSE:3`) | setup reference JS |
| RadioLib | jgromes/RadioLib (pinned at `MeshCore/platformio.ini:23`) | 6d893483 | | TX power and bandwidth limits |
| platform-espressif32 | tag v6.11.0 (pinned at `MeshCore/platformio.ini:60`) | 7401cc4 | | board JSONs |
| arduino-esp32 | tag 2.0.17. Verified: platform-espressif32 v6.11.0 pins `framework-arduinoespressif32` `~3.20017.0` = arduino-esp32 2.0.17 (`platform-espressif32-v6.11.0/platform.json:81-85`) | 5e19e08 | | ESP32 USB identity |
| others | platformio-core d8c2135; builder-framework-arduino-nrf5 0d9c4bb; platform-nordicnrf52 278176a; systemd v257 = 70bae76; Linux v6.12 and v6.18 (raw files); raspberrypi/linux rpi-6.18.y 3eddc99; meshtastic/python 795a0f0; espressif/usb-pids df2956b; picotool 2041936; arduino-pico 5081582 | | | USB, udev, kernel facts |
| npm | esptool-js 0.5.4; @zip.js/zip.js 2.7.57 | | Apache-2.0; BSD-3 | identity checks |

### 1.2 Live pages (GET only, through the web_fetch tool)

All were fetched on 2026-11-24. Where the tool served a cached copy, its date is given.

| URL | What | Saved as (under `scratchpad/src/`) | Bytes, sha256 |
|---|---|---|---|
| https://flasher.meshcore.io/config.json?_cb=20261124 | live catalog | `catalog-live2/flasher.meshcore.io_config.json__cb_20261124` | 138597, `921e37a7f7832762d514e0ef74b25ea800e913e7ee062d6e87ccdbc8e778efae` |
| https://flasher.meshcore.io/config.json (cache dated 2026-11-22; identical after JSON normalisation) | live catalog | `catalog-live/flasher.meshcore.io_config.json` | 138596, `3a623d1a…` |
| https://flasher.meshcore.io/releases?repo=github&_cb=20261124 | MeshCore releases v1.18.0 … v1.11.0 | `catalog-live2/flasher.meshcore.io_releases_repo_github__cb_20261124` | 860249, `ed56e66023f2db1cce5ed9805ef7c06742e7df27b91b8e316de1887f13e411b0` |
| https://flasher.meshcore.io/releases?repo=github (cache dated 2026-09-21) | stale list v1.17.1 … v1.10.0 | `catalog-live/flasher.meshcore.io_releases_repo_github` | 804231, `e29d0f19…` |
| https://flasher.meshcore.io/releases?repo=github-ripple | Ripple releases | `catalog-live/flasher.meshcore.io_releases_repo_github-ripple` | 20422, `2366569f…` |
| https://flasher.meshcore.io/config-zephcore.json?_cb=20261124 | ZephCore catalog (identical to git) | `catalog-live2/flasher.meshcore.io_config-zephcore.json__cb_20261124` | 27680, `b9182ba8…` |
| https://zephcore.meshcore.dev/releases?repo=github-zephcore | ZephCore releases | `catalog-zeph/zephcore.meshcore.dev_releases_repo_github-zephcore` | 548482, `1d7eceab…` |
| https://flasher.meshcore.io/ , https://flasher.meshcore.dev/ , https://zephcore.meshcore.dev/ , https://flasher.meshcore.co.uk/ , https://meshcore.io/flasher | host checks | (rendered text only) | |
| https://docs.meshcore.io/faq/ (copy dated 2026-11-16) | FAQ 5.13 | | |
| https://docs.meshcore.io/cli_commands/ (copy dated 2026-10-16) | CLI reference | | |
| https://api.meshcore.nz/api/v1/config (copy dated 2026-11-19) | radio presets | | |
| https://github.com/meshcore-dev/MeshCore/releases/download/… (HEAD requests, curl) | release asset sizes and MD5 | | section 2.5 |

The production JavaScript could not be fetched: the tool returned HTTP 200 with no content for every `.js` URL. That production runs exactly flasher@45a5257 is therefore **UNVERIFIED**. It is likely: the live page renders `{{ fwValue(selected.firmware, 'icon') }}`, and `fwValue(` appears 8 times in 45a5257 `index.html` and 0 times in the previous commit b84f889 (catalog report §1).

Other URLs cited in this document: https://buymeacoffee.com/ripplebiz/some-big-news, https://buymeacoffee.com/ripplebiz/ripple-ultra-firmware-t-deck, https://buymeacoffee.com/ripplebiz/e/361676, https://github.com/meshcore-dev/MeshCore/issues/1392, https://github.com/meshcore-dev/MeshCore/issues/3478, https://github.com/liquidraver/ZephCore/blob/master/docs/PROVIDER_CATALOG.md, https://meshtastic.org/docs/getting-started/flashing-firmware/nrf52/nrf52-erase/, https://store.meshcore.co.uk/products/meshos-meshcore-t-deck-license, https://meshcore.co.uk/meshos.html, the openocd-esp32 `doc/usb_adapters/esp_usb_jtag/303a_1001_esp_usb_jtag.txt`, https://docs.espressif.com/projects/rust/no_std-training/02_1_hardware.html, the heltec.org pages for WiFi LoRa 32 V3 and V4 and Wireless Stick Lite, the Wireless Paper V0.4 schematic (resource.heltec.cn), wiki.lilygo.cc T3 LoRa32, lilygo.cc T-Beam, espboards.dev T-Beam, the Elecrow ThinkNode M2 datasheet, a DongLoRa commit (git.swaits.com), https://github.com/microsoft/WSL/issues/7649, https://blog.oddbit.com/post/2022-02-13-wemos-udev-rules/, https://github.com/raspberrypi/linux/issues/7151. All fetched 2026-11-24.

### 1.3 Licences, and what the Go port may use

| Source | Licence | May we port it? | Notice to ship |
|---|---|---|---|
| flasher `js/catalog.js`, `js/flash.js`, `js/app.js` logic, `js/commands.js`, `lib/console.js` | MIT (Vysoky 2025) | Yes | The MIT notice for "Copyright (c) 2025 Rastislav Vysoky" |
| flasher `lib/dfu.js` | MIT (Vysoky) on top of BSD-3 (Nordic 2015). It says "adapted from" nrfutil `dfu_transport_serial.py`, `util.py`, `crc16.py` (`lib/dfu.js:3,27,66,86`) | Yes | Both the MIT notice and the full BSD-3 notice and disclaimer for "Copyright (c) 2015, Nordic Semiconductor". Never use Nordic's name to endorse the app (BSD-3 clause 3). |
| nrfutil `nordicsemi/dfu/signing.py` | Nordic proprietary header (`signing.py:1-9`) | **No.** Not needed for serial DFU | n/a |
| esptool-js 0.5.4 code (protocol, resets, chip tables) | Apache-2.0 (`LICENSE:190`: "Copyright 2019 Espressif Systems (Shanghai) CO LTD"; README says 2023; no NOTICE file) | Yes, as Apache-2.0 code. Or write it from the protocol description; facts and constants are not copyrightable | Apache-2.0 text, keep the copyright, add "modified" notices in ported files (Apache §4(b)) |
| esptool (Python) | GPL-2.0-or-later | **No**, unless the whole app becomes GPL | n/a |
| legacy stub v1.3.0 (the blob esptool-js 0.5.4 uploads) | GPL-2.0-or-later (Cesanta 2016, Espressif 2016-2022; `flasher_stub/stub_flasher.c:1-7`) | Only with GPL compliance | GPLv2 text plus the v1.3.0 source shipped alongside (§3(a)). The Go code can stay MIT or Apache, because the stub runs on another CPU and is sent as data (mere aggregation). This is the hosting report's reading, **not legal advice**. |
| esp-flasher-stub (v1.2.2 / npm 1.5.0 / v2.0.0) | Apache-2.0 OR MIT (Espressif 2025) | Yes | MIT or Apache notice. Its bytes differ from the reference stub. |
| pako 2.1.0 (inside `lib/esp32.js`) | MIT AND Zlib | Only if byte-identical deflate is wanted | MIT and Zlib notices |
| zip.js 2.7.57 (`lib/zip.min.js`) | BSD-3 (Gildas Lormeau 2023) | Not needed: use Go `archive/zip` (BSD-3, The Go Authors) | n/a |
| `lib/polyfill/serial.js` | Apache-2.0, Google 2019 (`:1-16`) | Not needed | n/a |
| flasher `img/*.svg` | unknown provenance | **Do not ship** | n/a |
| MeshCore firmware and docs | MIT | Command strings and facts: yes. The firmware is downloaded at run time, not redistributed | MIT notice if doc text or tables are copied |
| config.meshcore.io (setup tool, aff42ad8) | MIT, (c) 2026 Rastislav Vysoky (`LICENSE:3`) | Yes | MIT notice if its setup sequence, `ver` parsing or preset-to-`path.hash.mode` mapping (sections 7.9, 7.10) is ported |
| Radio preset list (MeshCore App API, https://api.meshcore.nz/api/v1/config) | **UNVERIFIED**: no licence or terms found. It is not in the MeshCore repo; `MeshCore docs/radio_presets.md:1-65` has only 3 example entries and says the presets are "stored on the MeshCore App API server" (`:3`) | Fetch at run time; shipping the 27-entry snapshot needs its own clearance (11.E2) | unknown |

Facts that matter here:

- The flasher's MIT licence does **not** cover its vendored files under `lib/`: esp32.js, zip.min.js, polyfill/serial.js, vue and beer. The repo has no NOTICE or third-party list (hosting report §B1).
- `flasher lib/esp32.js` is byte-identical to `bundle.js` of the npm package esptool-js@0.5.4: 178644 bytes, sha256 `2a896d5e520ea9b6ea9900223c2460df797c3b2287f441daa4e50168a50b17e6`. It has not changed since the flasher's first commit de525c9 (2025-02-23) (esp32 report §0).
- The npm tarball and the web flasher ship the GPL stub with no GPL text (hosting report §B3).

### 1.4 Notices to ship

Put a `THIRD_PARTY_NOTICES` file next to the binary, or show it on an About screen. It must contain:

1. MIT, (c) 2025 Rastislav Vysoky, for the logic ported from the flasher.
2. BSD-3-Clause, (c) 2015 Nordic Semiconductor, with its disclaimer, for the DFU code.
3. If any esptool-js code is ported: the Apache-2.0 text and Espressif's copyright, plus "modified" notes in those Go files.
4. If the legacy stub is embedded: the GPLv2 text, and the esptool-legacy-flasher-stub v1.3.0 source (or a source archive) shipped with the app.
5. If esp-flasher-stub is embedded instead: its MIT or Apache-2.0 licence files.
6. If pako's deflate is ported: MIT and Zlib notices.
7. MIT, (c) 2025 Scott Powell / rippleradios.com, if MeshCore doc text or tables are copied into the app. This does **not** cover the radio preset list: that comes from the MeshCore App API (https://api.meshcore.nz/api/v1/config), whose licence and terms are **UNVERIFIED** (section 7.8, 11.E2).
8. MIT, (c) 2026 Rastislav Vysoky (config.meshcore.io `LICENSE:3`), if the config tool's setup sequence, `ver` parsing or preset-to-`path.hash.mode` mapping is ported (sections 7.9, 7.10).

How far these duties apply depends on whether the app is published (open question 11.A7).

### 1.5 Where the research reports disagree

| Topic | One report says | The other says | Which wins, and why |
|---|---|---|---|
| Origin of the stub in esptool-js 0.5.4 | ESP32 report: esptool v4.6.1 stubs, per the stub README (marked UNVERIFIED) | Hosting report: all 11 stubs equal esptool v4.8.0/v4.8.1 `stub_flasher/1/*.json`, which are legacy stub v1.3.0; the README is stale | **Hosting.** It compared SHA-256 of the decoded text and data plus the entry point. A README claim is weaker. Both agree the licence is GPL-2.0-or-later. |
| Go deflate vs pako | Hosting report: "almost certainly" different, not tested (UNVERIFIED) | ESP32 report: measured. Go 1.24 BestCompression 803,567 bytes vs pako 781,373 on the Heltec v2 merged image | **ESP32.** It measured. |
| Merged-image address test | ESP32 report: 0x0 if the file *name* ends in `-merged.bin` | Catalog report: the test runs on `file.title ?? file.file?.name ?? ''` (`flasher js/flash.js:21-23`) | **Catalog.** It quotes the code. For release files the title is the file name, so both give the same result. For inline files the title is descriptive text, so only `type === 'flash-wipe'` gives 0x0. |
| Heltec Wireless Paper USB path | ESP32 report: listed among boards with `ARDUINO_USB_CDC_ON_BOOT=1` | USB-IDs report: UART0 through a CP2102 bridge (schematic V0.4) | **USB-IDs.** In `MeshCore/variants/heltec_wireless_paper/platformio.ini:8` the flag is commented out: `;-D ARDUINO_USB_CDC_ON_BOOT=1 ; this breaks Serial` (checked by the writer). |
| nRF52 DTR rule in MeshCore's own core | CLI report: UNVERIFIED, TinyUSB submodule not checked out | nRF52 report: cloned the core's pinned TinyUSB submodule 7b30ff9 and cites it | **nRF52.** It read the pinned code. Still UNVERIFIED on hardware. |
| Count of live catalog regexes | Catalog report: 554 distinct | Hosting report: 556 | Probably distinct patterns versus all patterns. It does not matter: both checked that every pattern is RE2-compatible. |
| How many releases `/releases` returns | Catalog report: newest 10 per type (inference) | Hosting report: 30 per repo, GitHub's default `per_page` (inference) | **Neither is verified.** The ZephCore list has 30 entries of which 14 have no type, and the Ripple list has 24 entries in 12 types. The app must not depend on either rule. |
| ESP32 ROM-only stub question | Hosting report: recommends ROM-only, no stub (licence) | ESP32 report: only the stub gives byte parity and a chip erase; leaves the choice to the owner | A recommendation, not a fact. Section 4.11 weighs both and recommends one. |
| nRF52 file types | nRF52 report: nRF52 uses `flash` and `flash-update` (Ripple) | Catalog report: live nRF52 entries use only `flash` and `download`; `flash-update` on nRF52 is git-only (inline Ripple Wio L1 Pro) | **Catalog.** It counted the live catalog. |

---

## 2. The flasher sites

### 2.1 Which address is current

**`https://flasher.meshcore.io` is the current, canonical address.** Evidence:

- The flasher README title is `## flasher.meshcore.io` (`flasher README.md:1`). Commit 5872e59 (2026-04-03) replaced "used on: https://flasher.meshcore.co.uk/".
- `flasher js/site.js:1-2`: "the same flasher is served on flasher.meshcore.io and zephcore.meshcore.dev".
- MeshCore docs point only to `.io`: `MeshCore docs/faq.md:111` and lines 122, 235, 357, 592, 619, 708, 736, 765. Commit 9ec08228 (2026-04-05) replaced every `.co.uk` and `.dev` link. `MeshCore README.md:55,83` link to https://meshcore.io/flasher.
- The flasher repo has no CNAME file.

Other hosts (fetched 2026-11-24):

| Host | What it is | Use it? |
|---|---|---|
| flasher.meshcore.dev | Same page and same `/releases?repo=github` JSON as `.io`. Whether it redirects to `.io` is **UNVERIFIED**. | No. Hard-code `.io`. |
| zephcore.meshcore.dev | Same app, ZephCore catalog (`config-zephcore.json`, chosen by `flasher js/site.js:8-9`) | Only if ZephCore is wanted (11.A4) |
| flasher.meshcore.co.uk | Now a separate UK community site ("Web Flasher — MeshCore": store, MeshOS, maps). `/configurator/`, `/config.json` and `/releases` returned HTTP 403. | **No** |
| meshcore.io/flasher | "MeshCore Flasher" page. Whether it embeds the flasher (`?iframe=1`, `site.js:11`) is **UNVERIFIED**. | No |

### 2.2 Endpoints

All paths are relative to `https://flasher.meshcore.io`.

| Path | Returns | Source |
|---|---|---|
| `/config.json` | the catalog (section 3) | `flasher js/catalog.js:51-52` |
| `/<name>.json` | another catalog via `?config=<name>`; characters outside `[a-z_-]` are stripped | `js/site.js:7-9` |
| `/releases?repo=<key>` | release list for one source key (`github`, `github-ripple`, `github-zephcore`) | `js/catalog.js:55-58` |
| `/releases/download/<tag>/<file>?repo=<key>` | a release file. The `?repo=` suffix is added by `matchReleaseFiles`. | `js/catalog.js:26-47` |
| `/firmware/<name>` | inline catalog files, nRF52 erase images, OTAFIX bootloaders | `config.json:2` (`staticPath`), `js/catalog.js:93`, `js/app.js:88-95,257` |

A failed catalog or releases request aborts the whole catalog load: `fetchJson` throws on `!res.ok` (`js/util.js:11-16`), and `flasher.js:7` is a top-level `const config = await loadCatalog();`.

### 2.3 The `/releases` JSON

```json
[{ "version": "v1.18.0",
   "type": "room-server",
   "name": "Room Server Firmware v1.18.0",
   "notes": "Release notes: https://blog.meshcore.io/2026/10/15/release-1-18-0",
   "files": [ { "name": "Ebyte_EoRa-S3_room_server-v1.18.0-81dbae5-merged.bin",
                "url":  "/releases/download/room-server-v1.18.0/Ebyte_EoRa-S3_room_server-v1.18.0-81dbae5-merged.bin" } ] }]
```

- Release keys are exactly `version, type, name, notes, files`. File keys are exactly `name, url`. Counted in both saved lists: 2026-11-24, 30 releases and 5274 files; 2026-09-21, 30 releases and 4923 files (`catalog-live2/…_cb_20261124`, `catalog-live/…_repo_github`, counted with `python3 -I`). **No size, hash or date is published.**
- The list is newest first.
- For `github`: `type` is `companion`, `repeater` or `room-server`. `version` is the tag minus its `<type>-` prefix. `name` is the GitHub release title (`MeshCore .github/workflows/firmware-builder.yml:87`). `notes` is the release body and may contain `\r\n`.
- The 2026-11-24 list has 30 releases: v1.18.0 … v1.11.0, 10 per type. The 2026-09-21 copy had v1.17.1 … v1.10.0. So old versions drop off over time.
- Every `url` is `/releases/download/<tag>/<name>`, the same path as on GitHub.
- `github-ripple`: types `wio-tracker-l2`, `wio-tracker-l1`, `thinknode-m9`, `t5-pro`, `t-watch-ultra`, `t-watch-plus`, `t-pager`, `t-deck-prov11`, `t-deck-plus`, `t-deck-max`, `meshpager-x2`, `heltec-v4r8`. Versions look like `v12.3`. Asset names carry no commit hash. The backing repository is **UNVERIFIED**.
- `github-zephcore`: version `1.18.2` from tag `1.18.2-zephcore`. Older entries have no `type` and are skipped by the client.
- The server code is not public. That it wraps the GitHub Releases API is an inference (**UNVERIFIED**).

### 2.4 How files are served

- `/firmware/FLASH_ERASE_nrf52_softdevice_v6.zip` returned the ZIP directly.
- `/releases/download/companion-v1.17.1/Heltec_v3_companion_radio_ble-v1.17.1-d929643.bin?repo=github` returned HTTP 200 with a binary body. Whether the host proxies or redirects to GitHub is **UNVERIFIED**.
- `MeshCore docs/faq.md:599-603` warns that `wget` may get "a very small file (10K bytes …)" unless it sends `--user-agent="Mozilla/5.0" --content-disposition`. User-agent filtering is the likely cause (**UNVERIFIED**). **The Go client must send a browser-like User-Agent and must reject HTML or tiny bodies.** Writing an HTML error page to flash can strand a board.
- GitHub direct works (HEAD only, from the research container): `https://github.com/meshcore-dev/MeshCore/releases/download/<tag>/<name>` answers 302 to `release-assets.githubusercontent.com/...` (link valid about 1 hour). That answers 200 with `Content-Length` and `X-Ms-Blob-Content-Md5` (base64 MD5). The MD5 header comes from Azure storage and is not a documented GitHub contract. A made-up name gives 404.
- The GitHub API (`api.github.com`) was blocked for the research session, so its per-asset `digest` field is **UNVERIFIED**.
- Static `/firmware/` files have no fallback host.

Asset naming (`MeshCore build.sh:130-181`): `<pio env>-<vX.Y.Z>-<7-hex commit>`.

| Platform | Files |
|---|---|
| ESP32 | `.bin` (app) and `-merged.bin` |
| nRF52 | `.zip` (DFU) and `.uf2` (family 0xADA52840) |
| STM32 | `.bin` and `.hex` |
| RP2040 | `.bin` and `.uf2` |

Releases are created as drafts and published by hand (`firmware-builder.yml:84-90`). Capitalisation varies: `Ebyte_EoRa-S3_Repeater-…` but `Heltec_v3_repeater-…`.

### 2.5 Sizes and hashes

From GitHub (HEAD requests, hosting report §A4):

| Asset | Bytes | MD5 (base64, from `X-Ms-Blob-Content-Md5`) |
|---|---|---|
| `repeater-v1.18.0/Heltec_v3_repeater-v1.18.0-81dbae5.bin` | 1174704 | `3yiGqsQs3ExWGFy/84k/Wg==` |
| `repeater-v1.18.0/Heltec_v3_repeater-v1.18.0-81dbae5-merged.bin` | 1240240 | |
| `repeater-v1.18.0/RAK_4631_repeater-v1.18.0-81dbae5.zip` | 441727 | |
| `companion-v1.17.1/Heltec_v3_companion_radio_ble-v1.17.1-d929643.bin` | 1271840 | `QqGo/YKWbW9kLNnLftDcGQ==` |

Samples downloaded from GitHub release `repeater-v1.18.0` and kept in the scratchpad. The writer computed these sha256 values from the saved copies; the first MD5 above matches the saved file.

| File (under `scratchpad/src/`) | Bytes | sha256 |
|---|---|---|
| `fw-samples-esp32/Heltec_v2_repeater-v1.18.0-81dbae5-merged.bin` | 1223776 (0x12ac60) | `5816692ba1a70341c961965758b89b2024011543941c5910de98480ab836f811` |
| `fw-samples-esp32/Heltec_v3_repeater-v1.18.0-81dbae5-merged.bin` | 1240240 | `396c8e2a65ffc8ce7618eed05574afae7cb8d63faeb3f23def9ce2260e869ef7` |
| `fw-samples-esp32/Heltec_v3_repeater-v1.18.0-81dbae5.bin` | 1174704 (0x11ecb0) | `c34b200b40760ca473868510b6c7089a8215587a5320d19dae1c30d883566349` |
| `fw-samples-esp32/Xiao_C3_repeater-v1.18.0-81dbae5-merged.bin` | 1306752 (0x13f080) | `b23b04802609343e7fd62768ecf03cc303f43daf7ba58090f34f21946a4ebcaa` |
| `fw-samples-nrf52/RAK_4631_repeater-v1.18.0-81dbae5.zip` | 441727 | `a5505147bd31aa83fdfb681525a25747250d670d46ca93e958db084b6fb9ec95` |
| `fw-samples-nrf52/RAK_4631_repeater-v1.18.0-81dbae5.uf2` | 882176 | `1a0c9adb8218c7b0e66e600706d532749875f912fe3f59481a2f916628e80328` |
| `fw-samples-nrf52/Xiao_nrf52_repeater-v1.18.0-81dbae5.zip` | 361571 | `97539d2029900719dc250a222cad7f2149ec90d2b3e360a07b4488cfd89430eb` |
| `otafix-sample/bl.zip` (OTAFIX `lilygo_techo_bootloader-0.9.2-OTAFIX2.2-BP1.3_s140_6.1.1.zip`, from GitHub) | 190874 | `308e6bb90e39fc970d1f5ca645af8099e20e3ee7defcd18119e43a2f1ab747a5` |

Inside the RAK zip: `firmware.bin` 440,924 B, `firmware.dat` 14 B, `manifest.json` 465 B, all STORED. Inside the Xiao zip: `firmware.bin` 360,768 B (nRF52 report §1.2).

### 2.6 Integrity checks the app must do itself

Nothing is published, so integrity comes from transport and from the files:

1. HTTP status 200, a non-HTML `Content-Type`, and the body length equal to `Content-Length` when one is sent.
2. Reject tiny bodies (the hosting report suggests under about 50 KB for a `.bin`).
3. ESP32: the image starts with `E9` (app at offset 0; merged image at 0x0 on S3/C3, at 0x1000 on ESP32) and the chip ID in the header matches the detected chip (section 4.9). The ESP32 image format also carries an appended SHA-256 (hosting report §A6; not checked further).
4. nRF52: ZIP CRC32s, then CRC-16 of `firmware.bin` equals the `.dat` trailer (section 5.1).
5. Optional: for `repo=github`, compare with GitHub's `Content-Length` and MD5 header.

### 2.7 Production versus git

| | git 45a5257 | live 2026-11-24 |
|---|---|---|
| devices | 67 (esp32 37, nrf52 29, noflash 1) | 70 (esp32 38, nrf52 30, noflash 2) |
| roles | includes `meshos` | no `meshos` |
| `github` entries companion/repeater/room-server | 126/62/60 | 132/65/62 |
| `github-ripple` entries | 18 | 23 |
| inline-version firmware | 60 | 59 |

- Only live: Heltec v4 R8 + Expansion Kit (Touch); Seeed Studio Wio Tracker L2 Pro; Elecrow ThinkNode M4; RPI Pico W (rp2040) and (rp2350) + WaveShare SX1262.
- Only git: RPI Pico 2040 + WaveShare SX1262 (renamed live), and a broken second "LilyGo T-Lora Pager" (section 3.10).
- Changed live: ThinkNode M5 repeater regex now `[Rr]epeater`; ThinkNode M9 `Ultra-Thinknode-?M9-`; T-Deck Pro v1.1 "[2nd Edition]" now matches `Ultra-TDeckProV1.1-2nd-…`; Wio Tracker L1 Pro Ripple moved from inline v10.1…v7.12 to `github-ripple` type `wio-tracker-l1`; Xiao nRF52 bootloader list gained `.uf2` files.
- The repo history is mostly "sync prod -> git" commits (69a57cf, ea3dae3, 4d696db, 56892c5). **Production is the source of truth.**

---

## 3. The catalog

Line references in this section are `flasher@45a5257` unless noted.

### 3.1 Top level

The keys are exactly `staticPath, role, notice, maker, device`, in that order (`config.json:2,3,46,52,75`).

| Key | Type | Value | Used by JS |
|---|---|---|---|
| `staticPath` | string | `"/firmware"` (`config.json:2`) | `js/catalog.js:93`, `js/app.js:92,95,257` |
| `role` | object: role key → `{icon, title, subTitle?, tooltip?}` | defaults for display (section 3.7) | `js/catalog.js:72` |
| `notice` | object: key → HTML | `otafixRecommended`, `otafixNeeded`, `rippleUserGuide`, `kissRadioNotice` (`config.json:47-50`) | `js/catalog.js:75-82` |
| `maker` | object: key → `{name}` | 7 keys | **never read** |
| `device` | array | 67 git, 70 live | everywhere |

### 3.2 Device fields

Counts are git / live.

| Field | Count | Meaning | JS use |
|---|---|---|---|
| `name` | 67/70 | display name; its slug is the URL segment (`js/router.js:18`) | sort, filter, router |
| `type` | 67/70 | `esp32` \| `nrf52` \| `noflash` | picks the flashing backend (`js/app.js:89,250,280`) |
| `firmware` | 67/70 | array of firmware objects | |
| `maker` | 67/70 | maker key; 5 values (`gat-iot`, `Ikoka`, `keepteen`, `uniteng`, `muziworks`) are missing from `maker` | unused |
| `tooltip`, `icon` | 67/70 | picture HTML, list icon | display |
| `erase` | 29/30, nrf52 only | nRF52 formatter zip under `/firmware/` | "Erase Flash" button (section 3.9) |
| `bootloader` | 21/21, nrf52 only | OTAFIX bootloader file names (`.uf2`, `.zip`) | download links only |
| `group` | 5/5 | "LilyGo T3 S3", "LilyGo T-Beam" | **never read** (`git log -S'.group'`) |
| `class` | 1/0 | `"meshos"` on the broken git stub only | unused |

### 3.3 Firmware fields, file objects, release sources

| Firmware field | Meaning |
|---|---|
| `class` | `community` \| `ripple` (\| `meshos`, used by no entry). Groups the role list (`js/app.js:72-83`). |
| `role` | key into `role`: companionBle, companionUsb, repeater, roomServer, kissRadio, gui, guiSD |
| `title`, `subTitle` | override the role's text, e.g. "Repeater - 30dBm", "[SX1262] Ripple GUI" (`roleValue`, `js/catalog.js:72`). **Key on `role`, not on the title.** |
| `notice` | key into `notice`, or literal HTML if the key is unknown (`js/catalog.js:76`) |
| `version` | inline versions: version name → `{files: [file], notes?}` |
| any key starting `github` | release source `{type, files: {fileType: regex}}`. `releaseSourceKey` takes the **first** such key (`js/catalog.js:22`). No entry has both a source and `version`. |

An inline file object is exactly `{type, name, title}`. `type` is `flash-wipe`, `flash-update`, `flash` or `download`. `name` is a file under `staticPath`, or a path starting with `/`. `title` is display text, for example "Combined app+partition+bootloader firmware bin".

A release-source object is normally `{type, files}`. The one exception is the T1000-E `companionBle` entry, whose `github` object also has a `notice` that the JS never reads (section 3.10).

### 3.4 How versions resolve

`loadCatalog()` (`js/catalog.js:51-69`):

1. Fetch `/<configName>.json`. `configName` is `config`, or `config-zephcore` on host `zephcore.meshcore.dev` (`js/site.js:2,8-9`).
2. Collect the distinct release-source keys over all firmware (`js/catalog.js:22,53-55`).
3. Fetch `/releases?repo=<key>` for each, in parallel. Any failure aborts the load (`js/catalog.js:56-58`).
4. For every firmware with a source, **replace** `version` with `matchReleaseFiles(...)` (`js/catalog.js:60-64`).
5. Drop every device none of whose firmware has a version with at least one file (`js/catalog.js:49,66`).

`matchReleaseFiles(releases, source, key)` (`js/catalog.js:26-47`), exactly:

```
patterns = for each [fileType, re] in source.files, in JSON key order: [fileType, new RegExp(re)]   // no flags
versions = {}                                                       // insertion-ordered
for [type, re] in patterns:                 // OUTER loop: patterns
  for release in releases:                  // INNER loop: release-list order (newest first)
    if release.type !== source.type: continue        // missing type never matches
    versions[release.version] ??= {notes: release.notes, files: []}
    for file in release.files:
      if re.test(file.name):                         // unanchored substring search, case-sensitive
        versions[release.version].files.push({type, name: file.url + '?repo=' + key, title: file.name})
delete every version with 0 files
```

Consequences:

- Version order is release-list order, newest first. The default version is the **first** version with files (`js/app.js:105`). The version menu lists only versions with files (`index.html:104-106`).
- Within one version, files are ordered by pattern order, then by file order.
- A firmware with no matching version is hidden from the role list (`js/app.js:76`) and ignored by the router (`js/router.js:42`).
- Version keys (`v1.18.0`, `1.17.1`, `v12.3`) are not integer-like, so JS keeps insertion order.

**For Go:**

- `encoding/json` into a map loses key order. Walk the JSON tokens with `json.Decoder`, or keep ordered slices. This matters because the order of `files` decides which file is picked (section 3.6).
- Use `regexp.MustCompile(re).MatchString(name)` (substring search). All live regexes compile under RE2 and give the identical match set to JS `RegExp.test`: 3981 (git) and 4041 (live) pattern/file pairs over 3 release lists (`scripts/regexcheck/main.go`, `scripts/regex_pairs.py` under `scratchpad/src/`).
- Reproduce the catalog's misses (section 3.10). Do not silently fix them, or the app offers files the reference does not.

### 3.5 File URLs

`firmwareUrl(config, file) = file.name.startsWith('/') ? file.name : config.staticPath + '/' + file.name` (`js/catalog.js:93`). Both forms are relative to the page origin.

| File source | Final URL |
|---|---|
| release file | `https://flasher.meshcore.io/releases/download/<type>-<version>/<file>?repo=<key>`, e.g. `…/releases/download/repeater-v1.18.0/Heltec_v3_repeater-v1.18.0-81dbae5.bin?repo=github` |
| inline file | `https://flasher.meshcore.io/firmware/<name>` |
| erase image | `/firmware/<erase>`; its download link swaps the first `.zip` for `.uf2` (`js/app.js:91-92,257`) |
| bootloader | `/firmware/<bootloader[i]>` (`js/app.js:95`) |

Regex shapes in the live catalog:

- MeshCore ESP32: `flash-wipe: "<Board>_<role>.*?-merged\.bin"`, `flash-update: "<Board>_<role>.*?-[a-f0-9]{7}\.bin"`.
- MeshCore nRF52: `flash: "….zip"`, `download: "….uf2"`.
- Repeater patterns use `[rR]epeater` (some `[Rr]`).
- Ripple: `"Ultra-<Dev>-[^-]+-merged\.bin"` / `"Ultra-<Dev>-[^-]+\.bin"`.
- Constructs used: `.*?`, `.*`, `\.`, `[..]`, `[..]+`, `[a-f0-9]{7}`, `(_SX1276)?`, `(?:ProMicro|Faketec)`. No anchors, look-arounds or backreferences.

### 3.6 File types, and which file is flashed

| Type | ESP32 | nRF52 | Flashed by the reference? |
|---|---|---|---|
| `flash-wipe` | merged image `*-merged.bin`: second-stage bootloader + partition table + boot_app0 + app (`MeshCore merge-bin.py:14-38`). Written at **0x0** with a chip erase. | not used live | yes, ESP32 only, when "Erase device" is ticked |
| `flash-update` | app-only `.bin`, written at **0x10000** | git only (inline Ripple Wio L1 Pro zip) | yes |
| `flash` | used on ESP32 only by the broken git stub | Adafruit nrfutil DFU `.zip` (`application` manifest) | yes |
| `download` | not used on ESP32 live | `.uf2` for drag-and-drop | **never** (Download menu only, `js/app.js:85-99`) |

Live: every ESP32 entry has exactly `flash-wipe` + `flash-update` (162 each). Every nRF52 entry has `flash` + `download` (169 each). Every noflash entry has `download` only (10).

```js
// flasher@45a5257 js/flash.js:7,19-34
const ESP32_APP_ADDRESS = 0x10000;
export const isMergedImage = (filename) => /-merged\.bin$/.test(filename);
export function esp32Address(file) {
  return file.type === 'flash-wipe' || isMergedImage(file.title ?? file.file?.name ?? '') ? 0 : ESP32_APP_ADDRESS;
}
export function pickFlashFile(files, { esp32, wipe }) {
  const flashFiles = files.filter(f => f.type.startsWith('flash'));
  if(esp32) {
    const preferred = flashFiles.find(f => f.type === (wipe ? 'flash-wipe' : 'flash-update'));
    if(preferred) return preferred;
  }
  return flashFiles[0];
}
```

- **ESP32 "update"** (box unticked): `flash-update` at 0x10000, no erase.
- **ESP32 "Erase device"** (box ticked): `flash-wipe` at 0x0 and `eraseAll: true`. One boolean ties together three things: the merged file, address 0x0, and a full-chip erase (`js/app.js:302-308`).
- **nRF52**: ignores `wipe` and sends `flashFiles[0]` by DFU with `new Dfu(port)` (`eraseBeforeUpdate = false`, `lib/dfu.js:124-128`; `js/app.js:310-313`).
- **noflash**: no Flash button (`index.html:163`).
- **Custom file** (`js/app.js:222-246`): `.bin` means esp32, anything else nrf52. A name ending `-merged.bin` shows an alert and sets `wipe = true`. The file becomes `{type: 'flash', file}`.
- **No code path resets `selected.wipe`** (`js/app.js:28,132,235`). The user can untick it (`index.html:119-122`, `v-model="selected.wipe"`), but it stays ticked when the user moves to another ESP32 device. The pre-refactor code did reset it (`git show b84f889:flasher.js`, line 384).
- **`eraseAll: selected.wipe` is applied whatever file is picked** (`js/app.js:281,302-305`). With the box ticked, a custom non-merged `.bin`, or the broken git stub's `.zip` on an ESP32 entry, falls through `pickFlashFile` to `flashFiles[0]` and gets `esp32Address` 0x10000 (`js/flash.js:21-34`). Result: a full chip erase, then a write at 0x10000 only. The second-stage bootloader and the partition table are gone, so the board does not boot.
- **The Go app must tie the chip erase to the merged-image / 0x0 choice, and reset "erase" for every new flash.**

### 3.7 Roles

Source: `config.json:3-45`.

| Role key | Title / subtitle | Kind | Entries git / live | Text CLI? |
|---|---|---|---|---|
| `companionBle` | Companion / Bluetooth | companion (BLE) | 62/65 | no |
| `companionUsb` | Companion / USB | companion (USB, binary frames) | 62/65 | no |
| `repeater` | Repeater | repeater | 63/66 | **yes** |
| `roomServer` | Room Server | room server | 61/63 | **yes** |
| `kissRadio` | KISS Radio Modem | KISS TNC (`MeshCore examples/kiss_modem/`, MIT) | 54/54 | no |
| `gui` | Ripple GUI | Ripple standalone GUI (closed) | 14/16 | no |
| `guiSD` | Ripple GUI: data on SD card | Ripple, SD variant (closed) | 10/12 | no |
| `meshos` | MeshOS | git only (`config.json:4-7`), no entries | 0/0 | n/a |

- Release type by convention: companionBle/Usb → `companion`, repeater → `repeater`, roomServer → `room-server`.
- "Configure via USB" is shown only for `repeater` and `roomServer` (`index.html:57`) and opens https://config.meshcore.io (`js/app.js:169-171`).
- All 54 kissRadio entries are inline `*_kiss_modem-1.17.1-45c0c323.*` files at `/firmware/`. `45c0c323` is MeshCore commit 45c0c32354826b03 (2026-08-17), which is after tag v1.17.1 (d9296435, 2026-08-14). The "1.17.1" label is approximate.

### 3.8 Classes, and every third-party or closed-source entry

`firmwareClasses` (`js/catalog.js:6-19`), in display order:

- `ripple`: "Freemium firmware provided by Ripple Radios (buymeacoffee.com/ripplebiz)".
- `meshos`: "Freemium firmware provided by Andy Kirby".
- `community`: "Open Source Community firmware (github.com/meshcore-dev/MeshCore)".

Live use: ripple 28, community 313, meshos 0.

**Ripple is closed source and freemium.** Evidence: `js/catalog.js:9`; `MeshCore docs/faq.md:511` ("Everything is open source except the T-Deck firmware and Liam's native mobile apps"); `faq.md:98,184,400-402` (free download, paid unlock code, $10 per T-Deck); Scott Powell, "Some Big News" (buymeacoffee.com/ripplebiz/some-big-news); "Ripple 'Ultra' Firmware for T-Deck" (buymeacoffee.com/ripplebiz/ripple-ultra-firmware-t-deck). Its licence and redistribution terms are **UNVERIFIED**. The `github-ripple` file is not in meshcore-dev/MeshCore (404). Its notes match Ripple's Buy-Me-a-Coffee posts word for word (e.g. buymeacoffee.com/ripplebiz/e/361676).

**Every Ripple entry in the live catalog (all class `ripple`, all role `gui` or `guiSD`):**

| Device | Type | Ripple roles | Source | Community firmware too? |
|---|---|---|---|---|
| Heltec v4 + Expansion Kit (Touch) | esp32 | gui, gui "Ripple GUI: LittleFS" | inline v11.0 … v8.2 (`/firmware/Ultra-HeltecV4-*.bin`) | yes |
| Heltec v4 R8 + Expansion Kit (Touch) *(live only)* | esp32 | gui, guiSD | github-ripple: heltec-v4r8 | yes |
| LilyGo T-Deck | esp32 | gui, guiSD | github-ripple: t-deck-plus | yes |
| LilyGo T-Deck Max | esp32 | gui, guiSD | github-ripple: t-deck-max | **no** |
| LilyGo T-Deck Pro | esp32 | gui, guiSD | inline v10.1 | **no** |
| LilyGo T-Deck Pro v1.1 | esp32 | gui ×2, guiSD ×2 ([1st Edition], [2nd Edition]) | github-ripple: t-deck-prov11 | **no** |
| LilyGo T5 E-Paper S3 Pro (H752-XX) | esp32 | gui, guiSD | github-ripple: t5-pro | **no** |
| LilyGo T-Lora Pager | esp32 | gui ×2, guiSD ×2 ([SX1262], [LR1121]) | github-ripple: t-pager | **no** |
| LilyGo T-Display Pro | esp32 | gui | inline v7.5, v6.8 | **no** |
| LilyGo T-Watch S3 Plus | esp32 | gui "Ripple GUI: LittleFS" | github-ripple: t-watch-plus | **no** |
| LilyGo T-Watch Ultra | esp32 | guiSD ×2 | github-ripple: t-watch-ultra | **no** |
| Seeed Studio Wio Tracker L1 Pro | **nrf52** | gui | github-ripple: wio-tracker-l1 (git: inline v10.1 … v7.12) | yes |
| Seeed Studio Wio Tracker L2 Pro *(live only)* | esp32 | gui, guiSD | github-ripple: wio-tracker-l2 | **no** |
| Elecrow ThinkNode M9 | esp32 | gui | github-ripple: thinknode-m9 | **no** |

That is 28 entries on 14 devices. 10 devices have only Ripple firmware. If Ripple is excluded, those 10 devices disappear.

**Other third-party firmware:**

- **MeshOS** (Andy Kirby): paid-licence firmware (store.meshcore.co.uk, meshcore.co.uk/meshos.html). Only its class (`js/catalog.js:11-14`) and role (`config.json:4-7`) remain in git; no entries; not live. Commit 5872e59 (2026-04-03) had it on T-Deck, T-Lora Pager and T-Display P4.
- **ZephCore** (liquidraver/ZephCore, MIT): a separate catalog `config-zephcore.json` on zephcore.meshcore.dev. Its README calls it a "community port, not the official firmware" and says "About 99% of this code was written by AI". The MeshCore RFC calls it "a hard fork" (https://github.com/meshcore-dev/MeshCore/issues/3478). Section 3.11.

**Static files of uncertain origin** (contents **UNVERIFIED**): the inline Ripple binaries at `/firmware/Ultra-*`; the `*_QSPIFlash_Format-v1.2.zip` erase images; the flasher-hosted copies of the OTAFIX bootloaders.

### 3.9 The `erase` and `bootloader` fields: the app must never write them

**`erase`** is an nRF52 formatter application, sent as a normal DFU application:

| Value | Devices (git/live) |
|---|---|
| `FLASH_ERASE_nrf52_softdevice_v6.zip` | 18/19 |
| `FLASH_ERASE_nrf52_softdevice_v7.zip` | 5/5 |
| `WioTrackerL1_QSPIFlash_Format-v1.2.zip` | 2 |
| `LilyGo_T-Echo_`, `Xiao_nrf52_`, `ThinkNode_M1_`, `Nano_G2_Ultra_QSPIFlash_Format-v1.2.zip` | 1 each |

- `FLASH_ERASE_*` comes from github.com/meshcore-dev/nRF52-Flash-Format (MIT, 03eb1d8). It is linked at 0x26000 (S140 v6) or 0x27000 (v7), up to 0xED000 (`boards/nrf52840_s140_v6.ld:8`, `_v7.ld:8`).
- On boot it runs `InternalFS.format()`, then `CustomLFS ExtraFS(0xD4000, 0x19000, 128).format()`, then `enterUf2Dfu()` (`src/main.cpp:8-21`). So it **replaces MeshCore and wipes the identity, contacts and settings**. MeshCore must be flashed again afterwards (`js/app.js:270`).
- It must match the SoftDevice version. Meshtastic warns that a wrong-SD erase "can overwrite part of the SoftDevice". Whether that applies through Adafruit DFU is **UNVERIFIED**.

**`bootloader`** lists OTAFIX bootloaders by oltaco. The upstream zip has manifest `{"manifest":{"dfu_version":0.5,"softdevice_bootloader":{"bin_file":"sd_bl.bin","dat_file":"sd_bl.dat","sd_size":151016,"bl_size":39000,…}}}`: SoftDevice + bootloader, no application.

**Every place the reference touches these fields** (no other read exists in `js/` or `index.html`):

| # | Where | Field | Writes to the device? |
|---|---|---|---|
| 1 | `js/app.js:248-277` `nrfErase()`, from the "Erase Flash" button (`index.html:143`) | `device.erase` | **Yes.** Downloads `/firmware/<erase>` and DFU-flashes it as an application (mode 4) |
| 2 | `js/app.js:89-93` | `device.erase` | no (download link to the `.uf2`) |
| 3 | `js/app.js:94-96` | `device.bootloader[]` | no (download links) |
| 4 | `js/catalog.js:75-82` → `index.html:100` | `device.bootloader[0]` | no (link in OTAFIX notices) |
| 5 | Custom Firmware, `js/app.js:222-246` + `:279-318` | any user file | **Yes for an erase zip.** No for an OTAFIX zip: `lib/dfu.js:409-416` reads `manifest.application.bin_file`, which throws a TypeError after the port opens (`:385`) but before any DFU packet |

**Rules for the Go app:**

1. Never download, offer or send a file named in `erase` or `bootloader`.
2. Never offer "Erase Flash" or "Enter DFU mode" as separate actions.
3. Refuse a DFU zip whose manifest has no `application`, or has `bootloader`, `softdevice` or `softdevice_bootloader`. A complete nrfutil port would flash those and could strand the board.
4. ESP32 `flash-wipe` rewrites the ESP32 second-stage bootloader and partition table. Whether that is allowed is an owner decision (11.B2).

### 3.10 Hidden entries and catalog defects

**Hidden entries.** With the 2026-11-24 releases, 17 live entries (14 at git HEAD) resolve to zero versions and are hidden. No device is dropped. With the stale 2026-09-21 list, the live ThinkNode M4 *was* dropped, because its assets exist only from v1.18.0. Devices can therefore appear and disappear between runs.

| Device | Hidden roles | Why |
|---|---|---|
| LilyGo T-Deck | roomServer | no matching asset |
| LilyGo T-Beam (SX1262), T-Beam 1.2 (SX1276), T-Beam Supreme | companionUsb | no matching asset |
| Heltec Vision Master E290 | companionBle, companionUsb | no matching asset |
| Seeed Wio Tracker L1 EINK | companionUsb, repeater, roomServer | no matching asset |
| Seeed Xiao C3 | one of two repeater entries | regex `Xiao_C3_sx1268_[rR]epeater` matches nothing (`config.json:4168`) |
| Elecrow ThinkNode M2 | repeater | regex `ThinkNode_M2_repeater` is case-sensitive; assets say `_Repeater-` (`config.json:4467`) |
| UnitEng Nano G2 Ultra | repeater, roomServer | they use release type `companion` (`config.json:5016-5035`) |
| RPI Pico W (rp2350) | companionBle, companionUsb, repeater, roomServer | no build |

**Defects:**

| Defect | Where | Reference effect | Go app should |
|---|---|---|---|
| A second "LilyGo T-Lora Pager", `type: esp32`, `class: meshos`, holding T-Echo Card nRF52 room-server and kiss zips. Left by merge 92f0c12 / b84f889. Its slug collides with the real Pager (`js/router.js:38-43`). | git `config.json:1510-1552` | would write an nRF52 `.zip` to an ESP32 at 0x10000 | reject non-`.bin` ESP32 files, non-`.zip` nRF52 flash files, zips without `application` |
| T1000-E warning nested in `github.notice`: `<article class='tertiary-container'>Please do not flash MeshCore firmware to LoRaWAN version of the t1000e, it can permantently brick the device</article>` | `config.json:3921` (git and live), on the companionBle entry only | never shown (`renderNotice` reads only `firmware.notice`, `js/catalog.js:75-82`) | show it, for every T1000-E role, with a confirm step (11.A6) |
| T-Deck Pro v1.1 "[2nd Edition] Ripple GUI" used the 1st-edition regex | git (fixed live) | wrong image offered | use live; show the resolved file name |
| OTAFIX notice on 8 entries whose device has no `bootloader` (incl. esp32 Heltec v4 R8, MeshSolar, MeshTracker X1, R1 Neo) | git and live (the same 8 entries in both; counted by the fixer's script over `flasher config.json` and the live copy) | link to `/firmware/` | show plain text, no links |
| 5 maker keys missing from `maker` | gat-iot, Ikoka, keepteen, uniteng, muziworks | none | do not depend on `maker` |
| Duplicate device names | git (T-Lora Pager ×2) | router uses the role to tell them apart | key devices by index |
| Changelog regex `/^Release notes:'/` has a stray quote | `js/catalog.js:84-91` | never matches; `#123` becomes a MeshCore PR link even in Ripple notes | cosmetic |

### 3.11 ZephCore catalog, noflash, group

**config-zephcore.json** (1037 lines; identical live and git):

- Keys `staticPath, role, notice, device`; no `maker`.
- Roles: `companion` ("Unified Companion firmware: Chat via mobile phone App, Web Client, CLI") and `repeater` (`config-zephcore.json:3-14`).
- 29 devices (nrf52 19, esp32 10), 58 entries, all class community, all with source key `github-zephcore` (`type: "zephcore"`). nRF52 uses `flash` `<board>-<role>-[^-]+\.zip` + `download` `.uf2`; ESP32 uses `flash-wipe` / `flash-update`.
- `erase` = `ZephCore_SoftDevice_v6_formatter.zip` or `v7` (19 devices). Same OTAFIX bootloaders.
- Boards not in the official catalog: LilyGo T-Lora C6, Seeed Xiao C6.
- Releases (2026-11-24): 30 entries, 16 typed (1.18.2 … 1.16.5), 14 untyped. URLs `/releases/download/<ver>-zephcore/<board>-<role>-<7hex>.<ext>`. All 29 devices resolve to 1.18.2.
- ZephCore's own docs say the official erase only partially wipes a ZephCore node (https://github.com/liquidraver/ZephCore/blob/master/docs/PROVIDER_CATALOG.md).
- **It is not official MeshCore.** Its CLI is not covered by section 7.

**noflash** devices: live "RPI Pico W (rp2040) + WaveShare SX1262" and "RPI Pico W (rp2350) + WaveShare SX1262". Only `download` `.uf2` files. The reference shows no Flash button (`index.html:163`). RP2040/RP2350 boards are flashed by copying the UF2 onto the BOOTSEL drive (`2e8a:0003` / `2e8a:000f`, no tty), which is outside this app.

**group** is on 5 devices ("LilyGo T3 S3" on T3 S3 SX126x and SX127x; "LilyGo T-Beam" on T-Beam SX1262, T-Beam 1.2 SX1276, T-Beam 1W). No JS reads it; its meaning is **UNVERIFIED**. The reference lists grouped devices separately.

**What the reference UI shows** (`index.html:81-188`): notice, version menu with changelog, ESP32 "Erase device" checkbox (tooltip: "DO NOT carry out a full erase if you are simply updating your MeshCore device, otherwise it will erase your MeshCore identity for that device."), nRF52 "Enter DFU mode" and "Erase Flash" buttons, Flash!, Download. No size, checksum or date. Error texts: `js/flash.js:13` "Could not download the firmware file from the server, reported: HTTP `<status>`.", `:70` "Failed to initialize. Did you place the device into firmware download mode?", `:81` "ESP32 flashing failed: …", `:95` "nRF flashing failed: …. Please reset the device and try again."

---

## 4. ESP32 flashing

### 4.1 What the reference is

- `flasher lib/esp32.js` is esptool-js **0.5.4, unmodified, including its bugs** (section 1.3; esp32 report §0). Upstream fixed several bugs in v0.5.5, v0.6.0, v0.6.1 and v0.7.0.
- `flashEsp32` (`flasher js/flash.js:43-87`):

```js
transport = new Transport(port, true)
options = { transport, terminal, compress: true, eraseAll,
            flashSize: 'keep', flashMode: 'keep', flashFreq: 'keep',
            baudrate: 115200, romBaudrate: 115200, enableTracing: false,
            fileArray: [{ data: <binary string>, address }], reportProgress }
loader = new ESPLoader(options); loader.hr = new HardReset(transport)
await loader.main()          // reset, SYNC, detect, info reads, stub upload
await loader.flashId()       // SPI RDID through register pokes
await loader.writeFlash(options)
await delay(100); await loader.after('hard_reset'); await delay(100)
finally: pulseRts (RTS=1, 100 ms, RTS=0); await transport.disconnect()
```

- The firmware is passed as a binary string, one character per byte (`js/util.js:19-29`).
- `port.open({baudRate: 115200})`; all other options are `undefined`, so Web Serial defaults apply (8N1, no flow control, buffer 255; **UNVERIFIED**) (`ejs src/webserial.ts:415-426`). The port is never reopened.

### 4.2 File, address, erase

- Update: `flash-update` app `.bin` at **0x10000**, no erase.
- Erase device: `flash-wipe` `-merged.bin` at **0x0**, with ERASE_FLASH first.
- Rule and code: section 3.6. `eraseAll` is honoured only when the stub runs (`ejs esploader.ts:1319-1321`).
- The "keep" settings change no image byte. `_updateImageFlashParams` returns early unless `address == chip.BOOTLOADER_FLASH_OFFSET`. On ESP32 that offset is 0x1000, which we never write. On S3/C3 at 0x0 all three values are "keep", so it logs "Not changing the image" and returns it unchanged (`ejs esploader.ts:1252-1302`). **The bytes flashed are exactly the downloaded file, padded with 0xFF to a multiple of 4.**

### 4.3 Reset into the ROM bootloader

**Strategy** (`ejs esploader.ts:582-605`):

- If `getPid() === 0x1001`: `[UsbJtagSerialReset]`. Only the PID is checked, not the VID (`ejs esploader.ts:140`, `webserial.ts:86-88`).
- Otherwise: `[ClassicReset(50 ms), ClassicReset(550 ms)]`, alternating per connect attempt.
- esptool-py's "UnixTight" reset does not exist in 0.5.4.

**Line meaning** on the usual auto-reset circuit: RTS asserted = EN low (chip in reset); DTR asserted = IO0 low (download strap). `setDTR(s)` sends `{dataTerminalReady:s}`. `setRTS(s)` sends `{requestToSend:s}` and then **always re-sends** `{dataTerminalReady:<last DTR>}` (`ejs webserial.ts:391-408`).

**ClassicReset(d)** (`ejs reset.ts:71-89`; same as esptool-py's classic reset, `esp-pylib serial_reset.py:296-329`):

| t (ms) | Call | setSignals sent |
|---|---|---|
| 0 | setDTR(false) | DTR=0 |
| 0 | setRTS(true) | RTS=1, DTR=0 |
| +100 | setDTR(true) | DTR=1 |
| +100 | setRTS(false) | RTS=0, DTR=1 → EN released with IO0 low: ROM download mode |
| +100+d (d = 50 or 550) | setDTR(false) | DTR=0 |

**UsbJtagSerialReset** (`ejs reset.ts:105-129`):

| Step | Calls | setSignals sent |
|---|---|---|
| 1 | setRTS(false); setDTR(false) | RTS=0, DTR=0, DTR=0 |
| 2 | sleep 100 | |
| 3 | setDTR(true); setRTS(false) | DTR=1, RTS=0, DTR=1 |
| 4 | sleep 100 | |
| 5 | setRTS(true); setDTR(false); setRTS(true) | RTS=1, DTR=1, DTR=0, RTS=1, DTR=0 |
| 6 | sleep 100 | |
| 7 | setRTS(false); setDTR(false) | RTS=0, DTR=0, DTR=0 |

esp-pylib does the last step in reverse order (`serial_reset.py:367-391`).

**One connect attempt** (`ejs esploader.ts:523-573,613-641`):

1. Reset.
2. `newRead(inWaiting() > 0 ? inWaiting() : 1, 3000)` (`ejs esploader.ts:528-529`; `webserial.ts:221-244,261-263`). With an empty buffer (the first attempt) it waits for one chunk, returns 1 byte of it and keeps the rest buffered (bug 4.10a). On a later attempt it returns everything already buffered, including leftover banner bytes and SYNC replies, which changes how many SYNCs a fake records.
3. Up to 5 SYNC tries with no delay between them. esptool-py waits 50 ms and flushes input first (`esptool loader.py:778-787`).
4. Each SYNC: read the first reply with a **100 ms** timeout, then 7 more at 3000 ms. The ROM sends 8 replies per SYNC. If every value is 0, a stub is already running and the upload is skipped.

`connect` is called with `attempts = romBaudrate` = 115200, so it effectively never gives up (`ejs esploader.ts:648`; fixed in v0.5.5, e624d24).

**After flashing** (`ejs reset.ts:146-161`; `flasher js/flash.js:36-40,83-86`). 0.5.4's HardReset never asserts RTS, so flash.js adds its own pulse. Recorded, relative to the FLASH_DEFL_END reply:

| t (ms) | Event |
|---|---|
| +200 | RTS=0, DTR=0 (`after('hard_reset')`) |
| +300 | RTS=1, DTR=0 (pulse) |
| +400 | RTS=0, DTR=0; `reader.cancel()`; `port.close()` |

**Linux notes for Go** (esp32 report §11; nRF52 report §9.1):

- Use `TIOCMBIS` / `TIOCMBIC` for one line at a time, to mirror single-member `setSignals`. Re-assert DTR after every RTS change if the trace must match.
- `open()` on Linux raises DTR and RTS. `close()` drops them when `HUPCL` is set. Web Serial traces do not show this; how Chrome handles it is **UNVERIFIED**. Set the lines explicitly right after open, and consider clearing `HUPCL`.

### 4.4 SLIP and packet formats

**SLIP** (`ejs webserial.ts:144-158`): frame = `C0` + escaped packet + `C0`. Escapes: `DB` → `DB DD`, `C0` → `DB DC`, over the whole packet including the header. One frame per write. On reading, a non-`C0` byte outside a frame throws "Invalid head of packet".

**Command, host to chip** (`ejs esploader.ts:408-446`):

| Offset | Size | Field |
|---|---|---|
| 0 | 1 | `00` (direction) |
| 1 | 1 | op |
| 2 | 2 | data length, LE16 |
| 4 | 4 | checksum, LE32. Non-zero only for MEM_DATA, FLASH_DATA, FLASH_DEFL_DATA |
| 8 | n | data |

Checksum = XOR of the payload bytes after the 16-byte data header, seeded with **0xEF** (`ejs esploader.ts:728-733`).

**Response, chip to host:**

| Offset | Size | Field |
|---|---|---|
| 0 | 1 | `01` |
| 1 | 1 | op (echo) |
| 2 | 2 | data length, LE16 |
| 4 | 4 | value, LE32 (READ_REG result; ROM SYNC gives 0x20120707) |
| 8 | n | data, then status bytes |

- ROM status: 4 bytes `[status, error, 0, 0]`, status 0 = OK, 1 = fail. Stub status: 2 bytes, documented as `[status, error]` (esptool `docs/en/advanced-topics/serial-protocol.rst:102-110,174`); the legacy stub actually sends `[error, status]`, with the error code itself in byte 0 (below). Either way byte 0 ≠ 0 means failure.
- ROM errors: 0x05 invalid message, 0x07 checksum, 0x08 flash write, 0x0B deflate, 0x0C Adler-32.
- Stub errors: 0xC0 bad data length, 0xC1 bad checksum, 0xC2 bad block size, 0xC3 invalid command, 0xC4 SPI op failed, 0xC5 SPI unlock failed, 0xC6 not in flash mode, 0xC7 inflate error, 0xC8 not enough data, 0xC9 too much data, 0xFF not implemented (`legacy stub include/stub_flasher.h:98-109`).
- esptool-py reads status at `data[resp_len : resp_len+2]` (`esptool loader.py:653-701`).
- **esptool-js 0.5.4 never checks status.** `readPacket` skips up to 100 frames shorter than 8 bytes or with `p[0] != 1`, and returns the first whose op matches (any frame when op is null). `checkCommand` returns `data` if longer than 4 bytes, else `value` (`ejs esploader.ts:373-397,666-680`).
- ROM SYNC reply, verified from the esptool docs trace (`serial-protocol.rst:557-573`): `c0 01 08 04 00 07 07 12 20 00 00 00 00 c0`.
- Stub hello after MEM_END: `c0 4f 48 41 49 c0` ("OHAI").

**Stub reply bytes and length rules** (legacy stub v1.3.0 at 624b5e7, `flasher_stub/stub_flasher.c`; `MAX_WRITE_BLOCK = 0x4000`, `include/stub_flasher.h:14`):

- The stub's 2 trailing bytes are `[error, status]` (`:228-229,372-373`): byte 0 is the error code (0 = OK), byte 1 is normally 0. esptool-py treats a non-zero byte 0 as failure.
- FLASH_BEGIN, FLASH_DEFL_BEGIN and MEM_BEGIN must be **exactly 16 bytes**: `verify_data_len(command, 16) || handle_…()` (`:170-173,288-305,356`). Because C's `||` yields 0 or 1, a 20-byte request (or a failing handler) is answered with error byte **0x01**, not 0xC0.
- With 16 bytes, a block_size word > 0x4000 gives **0xC2** (`:288-289,301-302`).
- Any packet with `data_len > 0x4000 + 16` is answered with error `C0` and status `EE`, and is not executed (`:217-221`).
- **Deferred errors.** A FLASH_DEFL_DATA reply is sent on receipt, before the block is inflated and written. Its error byte is `fs.last_error` from earlier blocks, overwritten by this block's own receipt checks (length word, then XOR checksum 0xC1) (`stub_flasher.c:307-326`). Inflate, SPI and size errors of a block are stored in `fs.last_error` (`stub_write_flash.c:358,405,421,460,464`) and appear only in the **next** DATA reply or in FLASH_DEFL_END (`:468-479`: `NOT_IN_FLASH_MODE`, `NOT_ENOUGH_DATA` if `remaining > 0`, else `last_error`).
- **FLASH_BEGIN(0,0) clears them.** `handle_flash_begin` sets `last_error = ESP_OK` and `remaining = total_size` = 0 (`stub_write_flash.c:200-206`). The reference sends READ_REG, then FLASH_BEGIN(0,0), then FLASH_DEFL_END (`ejs esploader.ts:1402-1404,1433-1440`), so **FLASH_DEFL_END always answers OK, and an error in the last block is lost** even when status bytes are checked.

**Expected RX stream** (what a Go fake must send and the Go parser must accept; `proto-esp32/out/esp32-small.decoded.txt` lines 10-30):

- Each ROM SYNC gets 8 identical replies. With the banner leftover (bug 4.10a) two SYNCs go out and 16 replies queue up. The leftover SYNC replies are still arriving when READ_REG 0x40001000 is sent.
- `readPacket` (`ejs esploader.ts:373-397`) skips up to 100 frames that are shorter than 8 bytes, have `p[0] != 1`, or carry another op, and returns the first with the wanted op. **Exception:** a non-matching reply with `data[0] != 0` and `data[1] == 0x05` (ROM "invalid message", `:108`) flushes input and throws `unsupported command error` (`:390-393`). The Go parser should follow the same skip-by-op rule, with this one abort.

**Timeouts are per chunk, not per command** (`ejs webserial.ts:192-244,294-305`). `read(timeout)` calls `newRead(…, timeout)` for each chunk, and each chunk read races its own fresh timer. A timeout runs the generator's `finally`, which **empties the internal buffer** (`:215-216`; `read()` does the same in its own `finally`, `:380-381`), so partial bytes are lost. A slow multi-chunk reply (8 SYNC answers, the boot banner) can therefore take longer than the nominal timeout without failing. A Go port with per-command deadlines behaves differently on slow replies. The Go code should state which model it follows; per-command deadlines with a no-drop buffer are recommended (bug 4.10b), with the timeout values of 4.8 as the per-command budget.

### 4.5 Command sequence (stub path, as the reference sends it)

Recorded by the Node prototype on a fake ESP32 (`proto-esp32/out/esp32-small.decoded.txt`; S3: `s3-update`; C3: `c3-jtag-wipe`). All integers LE.

| # | Step | Op | Request data | Chk | Timeout (ms) | Expected reply |
|---|---|---|---|---|---|---|
| 0 | open 115200 8N1 | – | – | – | – | – |
| 1 | reset (4.3) | – | – | – | – | boot banner |
| 2 | read and keep banner bytes | – | – | – | 3000 | – |
| 3 | SYNC, ≤5 per reset | 08 | `07 07 12 20` + 32×`55` | 0 | 100, then 7×3000 | 8× `01 08 04 00 07071220 00000000` |
| 4 | chip detect: READ_REG 0x40001000 | 0A | `00 10 00 40` | 0 | 3000 | value = magic (4.9) |
| 5 | info READ_REGs (table below) | 0A | address | 0 | 3000 | value |
| 6a | MEM_BEGIN text | 05 | `<size><ceil(size/0x1800)><0x1800><text_start>` | 0 | 3000 | OK |
| 6b | MEM_DATA ×n | 07 | `<len><seq><0><0>` + ≤0x1800 bytes | XOR^EF | 3000 | OK |
| 6c | MEM_BEGIN data + MEM_DATA | 05 / 07 | as above with `data_start` | | 3000 | OK |
| 6d | MEM_END | 06 | `<0><entry>` | 0 | **200** | OK, then `OHAI` within 3000; status is now 2 bytes |
| 7 | flash ID (table below) | 0A / 09 | WRITE_REG data = `<addr><val><0xFFFFFFFF><0>` | 0 | 3000 | W0: byte 0 manufacturer, byte 2 size code (0x12…0x18 = 256 KB…16 MB) |
| 8 | if eraseAll: ERASE_FLASH | D0 | (empty) | 0 | **120000** | OK |
| 9 | FLASH_DEFL_BEGIN | 10 | `<size><ceil(c/0x4000)><0x4000><addr>` (size = uncompressed padded size; c = compressed size) | 0 | 3000 | OK |
| 10 | FLASH_DEFL_DATA, seq 0…n-1 | 11 | `<len><seq><0><0>` + next ≤0x4000 bytes of the zlib stream; last block not padded | XOR^EF | block 0: 5000; block n: max(3000, 40000 ms/MB × inflated size of block n-1) | OK (receipt checks of this block plus any deferred error of earlier blocks, 4.4) |
| 11 | wait for last write: READ_REG 0x40001000 | 0A | `00 10 00 40` | 0 | last block timeout | magic. Means only that the stub has processed every queued block; it carries **no flash status** |
| 12 | FLASH_BEGIN(0,0) | 02 | `<0><0><0x4000><0>` (4 words) | 0 | 3000 | OK; **resets the stub's deferred error** (4.4) |
| 13 | FLASH_DEFL_END, stay in loader | 12 | `<1>` | 0 | 3000 | always OK after step 12 |
| 14 | reset tail (4.3) and close | – | – | – | – | app boots |

There is no SPI_ATTACH, no SPI_SET_PARAMS and no CHANGE_BAUDRATE. The stub attaches the flash itself and sets 16 MB parameters (64 MB on S3) (`legacy stub flasher_stub/stub_flasher.c:495-528`, `include/stub_flasher.h:22-29`). Both baud rates are 115200, so `changeBaud` is skipped (`ejs esploader.ts:1208-1210`).

**Exact wire bytes recorded:**

| Packet | Wire |
|---|---|
| SYNC (46 bytes) | `c0 00 08 24 00 00 00 00 00 07 07 12 20` + 32×`55` + `c0` |
| READ_REG 0x40001000 | `c0 00 0a 04 00 00 00 00 00 00 10 00 40 c0` |
| MEM_BEGIN, ESP32 text | `c0 00 05 10 00 00 00 00 00 cc 0d 00 00 01 00 00 00 00 18 00 00 00 e0 0b 40 c0` |
| MEM_END, ESP32 | `c0 00 06 08 00 00 00 00 00 00 00 00 00 ec e5 0b 40 c0` |
| WRITE_REG MISO_DLEN=0x17, ESP32 | `c0 00 09 10 00 00 00 00 00 2c 20 f4 3f 17 00 00 00 ff ff ff ff 00 00 00 00 c0` |
| ERASE_FLASH | `c0 00 d0 00 00 00 00 00 00 c0` |
| FLASH_DEFL_BEGIN, S3 app 0x11ecb0 B at 0x10000 | `c0 00 10 10 00 00 00 00 00 b0 ec 11 00 30 00 00 00 00 40 00 00 00 00 01 00 c0` |
| FLASH_BEGIN(0,0) | `c0 00 02 10 00 00 00 00 00 00 00 00 00 00 00 00 00 00 40 00 00 00 00 00 00 c0` |
| FLASH_DEFL_END(1) | `c0 00 12 04 00 00 00 00 00 01 00 00 00 c0` |

**Info READ_REGs** sent by `main()` (`getChipDescription`, `getChipFeatures`, `getCrystalFreq`, `readMac` twice, `postConnect`; `ejs esploader.ts:1195-1204`). Not needed to flash.

| Chip | Addresses, in order | Depends on values? |
|---|---|---|
| ESP32 | 0x3ff5a00c, 0x3ff5a00c, 0x3ff5a014, 0x3ff6607c, 0x3ff5a00c, 0x3ff5a00c, 0x3ff5a00c, 0x3ff5a010, 0x3ff5a018, 0x3ff40014, 0x3ff5a004, 0x3ff5a008, 0x3ff5a004, 0x3ff5a008 | no |
| ESP32-S3 (eFuse values all 0) | 0x60007058, 0x60007050, 0x6000706c, 0x60007058, 0x60007058, 0x60007050, 0x6000706c, 0x60007058, 0x60007050, 0x60007050, 0x60007050, 0x60007054, 0x60007054, 0x60007054, 0x60007044, 0x60007048, 0x60007044, 0x60007048, then postConnect 0x3fcef14c | **yes** (`isEco0`, `ejs targets/esp32s3.ts:38-103,191-197`). If `0x3fcef14c & 0xff == 3` (USB-OTG), the RAM block becomes 0x800 |
| ESP32-C3 | 0x60008850, 0x60008850, 0x60008850, 0x60008854, 0x60008844, 0x60008848, 0x60008844, 0x60008848 | no |

**Stub segments** (from `lib/esp32.js`; full values in `scratchpad/research/tools/bundled_stubs.json`):

| Chip | text (bytes @ address) | data (bytes @ address) | entry | MEM_DATA blocks |
|---|---|---|---|---|
| ESP32 | 3532 @ 0x400be000 | 156 @ 0x3ffdeba8 | 0x400be5ec | 1 + 1 |
| ESP32-S3 | 5292 @ 0x40378000 | 252 @ 0x3fcb2bfc | 0x40378a80 | 1 + 1 (3 + 1 with 0x800 blocks) |
| ESP32-C3 | 3868 @ 0x40380000 | 160 @ 0x3fc96bb4 | 0x403806d0 | 1 + 1 |
| ESP32-C6 (not recorded) | 3792 @ 0x40800000 | 160 @ 0x40852bb0 | 0x408006c4 | |

**Flash ID** (`runSpiflashCommand(0x9F, [], 24)`, `ejs esploader.ts:941-1041,1446-1453`):

| Register | ESP32 (base 0x3ff42000) | S3 / C3 (base 0x60002000) |
|---|---|---|
| CMD | +0x00 | +0x00 |
| USR | +0x1c | +0x18 |
| USR2 | +0x24 | +0x20 |
| MISO_DLEN | +0x2c | +0x28 |
| W0 | +0x80 | +0x58 |

Sequence: READ_REG USR, USR2; WRITE_REG MISO_DLEN = 0x17; USR = 0x90000000; USR2 = 0x7000009F; W0 = 0; CMD = 0x00040000; poll READ_REG CMD up to 10 times until bit 18 clears; READ_REG W0; restore USR, then USR2. The poll count depends on the device.

### 4.6 Compression

- pako 2.1.0, `deflate(data, {level: 9})`, a zlib stream (`78 DA` … Adler-32) (`ejs esploader.ts:1343-1346`).
- pako level 9 output equals zlib 1.3 level 9. On the Heltec v2 merged image: 781,373 bytes, sha256 prefix `f3c0f3153cb3f970`; `zlib.compress(img, 9) == stream` is true.
- Go 1.24 `compress/zlib` BestCompression: 803,567 bytes, a different stream (`proto-esp32/cmpdefl.py`, `proto-esp32/godefl/`). Block count, block contents and checksums all differ.
- The ROM and stub inflaters accept any valid zlib stream. So for the hardware, Go's own deflate is fine. Only byte-equal tests are affected. Options: (1) port zlib's deflate (pako `deflate.js`, `trees.js`, MIT/Zlib); (2) make the compressor an interface and inject the reference stream in tests; (3) compare the inflated data and the framing only (11.B3).

### 4.7 Verify (SPI_FLASH_MD5, 0x13)

- **The reference never verifies.** flash.js does not pass `calculateMD5Hash`, so no 0x13 is sent and "Hash of data verified" never prints (`ejs esploader.ts:1336-1340,1420-1429`; `flasher js/flash.js:45-58`). The only checks are the per-block XOR (not even read back by 0.5.4) and the stub's zlib Adler-32.
- Request: `<addr><size><0><0>`, timeout `max(3000, 8000 ms/MB × size)`.
- Stub reply: 16 raw MD5 bytes + 2 status bytes (length 0x12). ROM reply: 32 ASCII hex chars + 4 status bytes (length 0x24). Whether the ROM uses lowercase is **UNVERIFIED**; compare case-insensitively. esptool-js 0.5.4 decodes the ROM form wrongly (`ejs esploader.ts:1079-1091`; `esptool loader.py:1609-1627`).
- Where it fits (recorded with a test-only injection, `proto-esp32/out/esp32-small-md5.decoded.txt`): after the post-write READ_REG 0x40001000 and before FLASH_BEGIN(0,0):

```
TX SPI_FLASH_MD5  c0 00 13 10 00 00 00 00 00 00 00 01 00 00 10 00 00 00 00 00 00 00 00 00 00 c0
RX                c0 01 13 12 00 00 00 00 00 27 f6 81 f0 2f 6d 10 9b 2a 2c 41 2b c6 91 2f 80 00 00 c0
```

- Compare with `md5(padded image)`. For a merged image at 0x0 the size is the whole padded image, including the 4 KB of 0xFF before the ESP32 bootloader.
- **Recommendation:** add this verify step. It is a deliberate deviation, but it costs one command and catches a bad write (11.B4).

### 4.8 Timeouts (ms)

`timeoutPerMb(x, n) = max(3000, x · n / 1e6)` (`ejs esploader.ts:110-116,774-781`).

| Command | Timeout |
|---|---|
| default (READ_REG, WRITE_REG, MEM_BEGIN, MEM_DATA, FLASH_BEGIN, FLASH_DEFL_END, extra SYNC reads) | 3000 |
| first SYNC reply | 100 |
| boot-log read after reset | 3000 |
| MEM_END | 200 |
| "OHAI" | 3000 |
| ERASE_FLASH | 120000 |
| FLASH_DEFL_BEGIN | stub 3000; ROM `timeoutPerMb(30000, write_size)` |
| FLASH_DEFL_DATA | stub: 5000 for block 0, then `timeoutPerMb(40000, inflated bytes of previous block)`; ROM: the block's own value |
| SPI_FLASH_MD5 | `timeoutPerMb(8000, size)` |

esptool-py uses 40 s/MB for erase and erase-plus-write and 8 s/MB for MD5 (`esptool loader.py:93-107`). At 115200 baud a 1.2 MB merged image takes about 70 s (esp32 report, open questions).

### 4.9 Per-chip facts

**Chips in the catalog** (catalog regex → MeshCore env → board JSON `mcu`; `proto-esp32/chipmap.py`):

| Chip | Catalog devices |
|---|---|
| ESP32 (classic) | LilyGo T-Beam SX1262 and 1.2 SX1276, LilyGo LoRa32 V2.1_1.6, Heltec v2 |
| ESP32-S3 | Heltec v3, v4, v4 R8, WSL3, Wireless Tracker v1 and v2, Wireless Paper, Vision Master E213 and E290; LilyGo T-Deck, T3-S3 (SX126x, SX127x), T-Beam 1W, T-Beam Supreme; RAK 3112; Xiao S3 WIO; ThinkNode M2, M5, M7; Station G2, G3 |
| ESP32-C3 | Seeed Xiao C3 |
| unknown | Ripple-only devices: **UNVERIFIED**, probably S3 (ThinkNode M9 is S3, `MeshCore boards/thinknode_m9.json:26`) |

No C6, S2 or H2 device is in the catalog.

**Constants:**

| Chip | Magic at 0x40001000 | IMAGE_CHIP_ID | Bootloader offset | SPI_REG_BASE / W0 | eFuse base | USB |
|---|---|---|---|---|---|---|
| ESP32 | 0x00f01d83 | 0 | **0x1000** | 0x3ff42000 / 0x80 | 0x3ff5a000 | bridge only |
| ESP32-S2 | 0x000007c6 | 2 | 0x1000 | 0x3f402000 / 0x58 | 0x3f41a000 | USB-OTG |
| ESP32-S3 | 0x00000009 | 9 | **0x0** | 0x60002000 / 0x58 | 0x60007000 | USB-Serial-JTAG, USB-OTG |
| ESP32-C3 | 0x6921506f, 0x1b31506f, 0x4881606f, 0x4361606f | 5 | **0x0** | 0x60002000 / 0x58 | 0x60008800 | USB-Serial-JTAG |
| ESP32-C6 | 0x2ce0806f | 13 | 0x0 | **0x60003000** / 0x58 | 0x600B0800 | USB-Serial-JTAG |
| ESP32-H2 | 0xd7b73e80 | 16 | 0x0 | **0x60003000** / 0x58 | **0x600B0800** (inherited from C6) | USB-Serial-JTAG |

Sources: `ejs targets/esp32*.ts`; `esptool targets/esp32.py:18-28,106`, `esp32s2.py:17-32`, `esp32s3.py:17-44`, `esp32c3.py:17-42`, `esp32c6.py:32,38,42`, `esp32h2.py:10` (`class ESP32H2ROM(ESP32C6ROM)`, no override). **esptool-js 0.5.4 has wrong values for C6 and H2** (C6 SPI_REG_BASE 0x60002000; H2 SPI_REG_BASE 0x60002000 and EFUSE_BASE 0x60008800; `ejs v0.5.4 targets/esp32c6.ts:7,24`, `esp32h2.ts:7,24`), fixed upstream in 5a8dfa0 and 0654ab5 (esptool-js a07ee04 `targets/esp32c6.ts:8,18`, `esp32h2.ts:8,18`). Neither chip is in the catalog. **The magic column for S3, C3, C6 and H2 comes from esptool-js only:** esptool 5.7 sets `USES_MAGIC_VALUE = False` for them and identifies them by GET_SECURITY_INFO (`esp32s3.py:25`, `esp32c3.py:25`, `esp32c6.py:23`, `esp32h2.py:20`). Other magics in `magic2Chip` (`ejs esploader.ts:20-78`): C2 0x6f51306f, 0x7c41a06f; C61 0x2421606f, 0x33f0206f, 0x4f81606f; C5 0x1101406f, 0x63e1406f; ESP8266 0xfff0c101; P4 0x0, 0x0addbad0, 0x7039ad9.

0.5.4 bug: an unknown magic sets `chip = null`, and `main()` later crashes with a TypeError (`ejs esploader.ts:631-640`). 0.5.4 never sends GET_SECURITY_INFO (0x14); esptool-py uses its chip_id for S3, C3, C6 and H2 (`esptool loader.py:910-985`; reply `<I flags><B><7B><I chip_id><I api>`, `:1252-1325`).

**Merged images start at 0x0.** `merge-bin.py` passes no `--target-offset` (`MeshCore merge-bin.py:14-38`; pipeline `build.sh:159-163`, `platformio.ini:58-62`). esptool's default target offset is 0 and gaps are filled with 0xFF (esptool v4.8.1 `__init__.py:671-676`, `cmds.py:1408-1439`). The release images confirm it (`proto-esp32/imghdr.py`, `parts.py`):

| Image | 0x0000-0x0FFF | Bootloader | 0x8000 | 0xE000 | 0x10000 |
|---|---|---|---|---|---|
| Heltec v2 merged (ESP32, 0x12ac60 B) | all 0xFF | at 0x1000 (E9, mode 2 DIO, size/freq 0x30, chip 0) | partition table (`AA 50`) | otadata `01 00 00 00 …` | app (E9, 7 segments) |
| Heltec v3 merged (S3, 0x12ecb0 B) | | at 0x0 (`E9 03 02 3F`, chip 9) | partition table | otadata | app |
| Xiao C3 merged (C3, 0x13f080 B) | | at 0x0 (`E9 03 02 2F`, chip 5) | partition table | otadata | app |
| Heltec v3 app only | | | | | E9 header at offset 0, chip 9; written at 0x10000 |

**Partition tables in these images:**

| Board | nvs | otadata | app0 | app1 | spiffs | coredump |
|---|---|---|---|---|---|---|
| 8 MB (Heltec v2, v3) | 0x9000 / 0x5000 | 0xe000 / 0x2000 | 0x10000 / 0x330000 | 0x340000 / 0x330000 | 0x670000 / 0x180000 | 0x7f0000 / 0x10000 |
| Xiao C3 (4 MB) | 0x9000 / 0x5000 | 0xe000 / 0x2000 | 0x10000 / 0x140000 | 0x150000 / 0x140000 | 0x290000 / 0x160000 | **0x3f0000** / 0x10000 |

- The merged image does **not** reach spiffs. A merged write without a chip erase keeps spiffs. That the MeshCore identity lives there is **UNVERIFIED** (esp32 report §7.3). It would match the UI warning (`index.html:124-126`) and FAQ 5.13 ("Merged bin overwrites everything including the bootloader and existing Bluetooth pairing database, but keeps configurations").
- Writer's note: the merged image runs from 0x0 through app0, so its 0xFF fill also covers nvs at 0x9000. That fits the FAQ's "overwrites … Bluetooth pairing database".

### 4.10 esptool-js 0.5.4 bugs: copy or fix?

| # | Bug | Effect on the wire | Upstream fix | Go advice |
|---|---|---|---|---|
| a | After the first reset only 1 byte of boot log is returned; the rest of the chunk stays buffered (`ejs esploader.ts:528-529`, `webserial.ts:221-244,320-331`). Later attempts drain what is buffered (4.3 step 2) | The first SYNC fails with "Invalid head of packet (0x74)"; a second SYNC follows. The count varies with chunking (1 to 5+). | – | Drain the banner properly. Tests must collapse leading SYNCs. |
| b | A timed-out read leaves an orphaned `reader.read()` that later swallows a chunk (`ejs webserial.ts:192-218`) | If the first reset fails, the loader never syncs again (prototype `esp32-ignore1`: 12 SYNCs, 3 s apart, never connects) | v0.6.0 a6b8074, v0.6.1 cd69e72 | **Fix.** The Go read path must never drop bytes. |
| c | `connect` gets 115200 as the attempt count (`ejs esploader.ts:648`) | retries practically forever | v0.5.5 e624d24 | **Fix.** Cap attempts; tell the user to hold BOOT. |
| d | No status checks; no data-block retry (esptool-py retries 3 times) | NAKs such as 0xC1 go unnoticed | checked in ejs HEAD (`esploader.ts:915-946`) | **Fix.** Check status; verify with MD5. |
| e | ROM MD5 reply decoded wrongly | unreachable with the stub | – | Decode both forms. |
| f | Without the stub, `flashBegin` adds a 5th word for every chip, ESP32 included (`ejs esploader.ts:805-807`), and `flashDeflBegin` adds it only for S2, S3, C3, C2 (`:846-854`), not C6 or H2. esptool v4.8.1 (the release that ships legacy stub v1.3.0) adds it only for the ROM of chips with `SUPPORTS_ENCRYPTED_FLASH` (S2, S3, C3, C6, H2; not ESP32) (`git show v4.8.1:esptool/loader.py` lines 914-915, 1112-1115). esptool 5.7 adds it when `IS_STUB` or the chip is not ESP32/ESP8266 (`loader.py:1187-1189,1555-1557`), for its own newer stub | with the legacy stub only 4 words are sent, which is correct | – | With the legacy stub: **always 4 words (16 bytes)** for FLASH_BEGIN and FLASH_DEFL_BEGIN (4.11). ROM mode: 5th word on S2, S3, C3, C6, H2, never on ESP32. |
| g | "TRACE" lines print even with tracing off | console noise | v0.6.0 fef8330 | ignore |
| h | HardReset does not pulse RTS | flash.js adds `pulseRts` | – | Copy flash.js's tail. |
| i | No USB-Serial-JTAG watchdog disable; S3 force-download flag not cleared (esptool-py: `targets/esp32s3.py:99-100,347-353`, clears RTC_CNTL_OPTION1_REG 0x6000812C bit 0) | after a flash over USB-Serial-JTAG an S3 may stay in download mode until power-cycled (**UNVERIFIED** for MeshCore boards) | – | Clear the flag before the final reset if this shows up on hardware. |

### 4.11 Stub versus ROM-only, and recommendation

**What the ROM can do without a stub** (`esptool loader.py:160-172,1528-1629`): every ESP32-family ROM supports FLASH_DEFL_BEGIN/DATA/END (0x10-0x12), SPI_FLASH_MD5 (0x13) and CHANGE_BAUDRATE. **Stub-only:** ERASE_FLASH 0xD0, ERASE_REGION 0xD1, READ_FLASH 0xD2, RUN_USER_CODE 0xD3 (`loader.py:1641-1655`).

| Item | ROM | Stub |
|---|---|---|
| status bytes | 4 | 2 |
| block size | **0x400** (`loader.py:345`) | 0x4000 (`loader.py:2222`). esptool-js 0.5.4 uses 0x4000 even before the stub (`ejs esploader.ts:165`); **do not copy that in ROM mode**. Whether the ROM accepts 0x4000 is **UNVERIFIED**. |
| FLASH_DEFL_BEGIN `write_size` | rounded up to whole blocks; the ROM **erases the whole region up front** | exact; erases while writing |
| FLASH_BEGIN / FLASH_DEFL_BEGIN 5th word `0` | S2, S3, C3, C6, H2 ROMs; never ESP32 (esptool-py, `loader.py:1187-1189,1555-1557`). 0.5.4 differs (bug 4.10f) | **never.** The legacy stub requires exactly 16 bytes; 20 bytes get error byte 0x01 (`stub_flasher.c:170-173,288-305`). esptool 5.7 sends it to its own newer stub only |
| data block ACK | after the block is written | on receipt; inflate/SPI errors are deferred to the next DATA reply or FLASH_DEFL_END, and FLASH_BEGIN(0,0) clears them (4.4) |
| FLASH_DEFL_END to stay in loader | **not sent** ("skip sending flash_finish to ROM loader, as this exits the bootloader", `loader.py:1595-1599`) | sent |
| MD5 reply | 32 ASCII hex | 16 raw bytes |
| full-chip erase | **impossible** (0xD0 is stub-only). Emulation by FLASH_BEGIN over the whole flash is **UNVERIFIED** on hardware; budget about 40 s/MB | ERASE_FLASH |

**A ROM-only flow must add** (esptool `cmds.py:2035-2110,2201-2250`):

1. **SPI_ATTACH 0x0D**: `<I hspi_arg><B 0><B 0><B 0><B 0>`, 8 bytes for the ROM. `hspi_arg` = 0 on S2, S3, C3, C6. On ESP32 it comes from eFuse SPI pads (READ_REG 0x3ff5a014 and 0x3ff5a00c; value `(hd<<24)|(cs<<18)|(d<<12)|(q<<6)|clk`, 0 for normal modules) (`loader.py:1812-1826`; `targets/esp32.py:307-319`).
2. **SPI_SET_PARAMS 0x0B**: `<I 0><I total_size><I 0x10000><I 0x1000><I 0x100><I 0xFFFF>`, 24 bytes. For "keep", use the detected size from RDID byte 2 (0x14 = 1 MB … 0x18 = 16 MB) (`loader.py:1910-1937`).
3. **Disable watchdogs on USB-Serial-JTAG** (PID 0x1001), or the RTC WDT can reset the chip mid-flash (`esptool targets/esp32s3.py:313-329`, `esp32c3.py:228-244`). Each write is WRITE_REG `<addr><val><0xFFFFFFFF><0>`:

| Step | ESP32-S3 | ESP32-C3 | Value |
|---|---|---|---|
| unlock WDT | 0x600080B0 | 0x600080A8 | 0x50D83AA1 |
| WDT config 0 | 0x60008098 | 0x60008090 | 0 |
| relock WDT | 0x600080B0 | 0x600080A8 | 0 |
| unlock SWD | 0x600080B8 | 0x600080B0 | 0x8F1D312A |
| SWD config: READ_REG, then write value \| 1<<31 (auto-feed) | 0x600080B4 | 0x600080AC | |
| relock SWD | 0x600080B8 | 0x600080B0 | 0 |

The stub disables the watchdogs itself (`legacy stub stub_flasher.c:478-480`).

**The two reports differ** (section 1.5). The hosting report recommends ROM-only, so that nothing GPL and no third-party blob ships. The ESP32 report shows that only the stub can (a) send the same bytes as the reference and (b) do a chip erase.

**Recommendation of this document: use the stub, exactly as the reference does, provided the owner accepts the GPL notice duty.** Reasons:

1. The brief requires the Go code to send the same bytes as the reference. After chip detection, only the stub path can.
2. The stub path is what the web flasher runs on every ESP32 board today. The ROM-only path has several **UNVERIFIED** parts: block size, the up-front erase time, the watchdog sequence on USB-Serial-JTAG, and the erase emulation.
3. "Erase device" needs ERASE_FLASH, which only the stub has.
4. The GPL duty is small: ship the GPLv2 text and the legacy stub v1.3.0 source with the app. The Go code keeps its own licence (mere aggregation; not legal advice). For a personal, unpublished build there is nothing to ship.

Add to the stub path: status checks, a capped connect loop, MD5 verify (4.7), and a chip guard (6.5). These change behaviour on failures, not the bytes of a successful run.

**Status checks alone do not prove the write.** The READ_REG after the last block carries no flash status, and the reference's FLASH_BEGIN(0,0) clears the stub's deferred error before FLASH_DEFL_END, so an inflate or SPI error in the last block is never reported (4.4). Two options (11.B8):

1. **Always run SPI_FLASH_MD5** after the READ_REG (4.7). This adds one command before FLASH_BEGIN(0,0); the other bytes stay as the reference sends them. **Recommended.**
2. Send FLASH_DEFL_END(1) **before** FLASH_BEGIN(0,0) and check its status (`NOT_ENOUGH_DATA` 0xC8 or the deferred error). This changes the order of the bytes sent.

If the owner rejects GPL: use ROM-only with the extra commands above, 0x400 blocks and esptool-py's 5th-word rule. Then drop "Erase device", or define it as "write the merged image at 0x0" with no chip erase. The licence-clean esp-flasher-stub (Apache-2.0 OR MIT) is a third option, but its bytes differ from the reference and its tests would need esptool-js ≥ 0.7.0 as the reference.

---

## 5. nRF52 flashing

Line references are `flasher@45a5257 lib/dfu.js` unless noted. "OTAFIX" means Adafruit_nRF52_Bootloader_OTAFIX at a62825b; "nrfutil" means Adafruit_nRF52_nrfutil at 7fdfe15.

**Main result:** the nRF52 report ran the vendored `lib/dfu.js` (Node 22.23.5) and nrfutil's unmodified `DfuTransportSerial` against a fake bootloader on `RAK_4631_repeater-v1.18.0-81dbae5.zip`. Both sent 865 frames, 454,160 bytes on the wire, and the concatenated bytes are **identical**. They differ only in timing, DTR handling and ACK checking (section 5.8).

### 5.1 Which file, and what is in it

- The catalog `flash` `.zip` is flashed (`flasher js/flash.js:26-34`). Example, RAK4631: `"flash": "RAK_4631_[rR]epeater.*?\\.zip"`, `"download": "RAK_4631_[rR]epeater.*?\\.uf2"` (`flasher config.json:3637-3643`).
- The `.uf2` is never flashed. On the RAK sample it holds the same image: 1723 blocks of 256 bytes, family `0xADA52840`, addresses `0x26000..0x91B00` (built by `MeshCore build.sh:165-169`).
- The zip is built by `adafruit-nrfutil dfu genpkg --dev-type 0x0052 --sd-req <board sd_fwid> --application firmware.hex firmware.zip` (`platform-nordicnrf52 builder/main.py:126-147`). Defaults: dev_rev 0xFFFF, app_version 0xFFFFFFFF, dfu_ver 0.5 (`nrfutil nordicsemi/dfu/package.py:80-84`). With dfu_ver ≤ 0.5 the init packet carries a CRC16, not a hash (`package.py:196-198`).

RAK zip, all entries STORED (`proto-nrf52/inspect_zip.py`):

```
manifest.json 465 B, firmware.dat 14 B, firmware.bin 440924 B
{"manifest":{"application":{"bin_file":"firmware.bin","dat_file":"firmware.dat",
  "init_packet_data":{"application_version":4294967295,"device_revision":65535,"device_type":82,
  "firmware_crc16":18911,"softdevice_req":[182]}},"dfu_version":0.5}}
firmware.dat = 52 00 | ff ff | ff ff ff ff | 01 00 | b6 00 | df 49
Xiao_nrf52 zip: firmware.bin 360768 B, firmware.dat = 52 00 ff ff ff ff ff ff 01 00 23 01 9e e0
```

**The `.dat` init packet** (little-endian; built by `nrfutil nordicsemi/dfu/init_packet.py:69-131`, parsed by `OTAFIX src/dfu_init.c:105-188`):

| Offset | Size | Field | MeshCore value | Bootloader check |
|---|---|---|---|---|
| 0 | u16 | device_type | 0x0052 | must be exactly 0x0052 (`dfu_init.c:84,160-163`) |
| 2 | u16 | device_rev | 0xFFFF | only for SoftDevice/bootloader updates (must be 52840) |
| 4 | u32 | app_version | 0xFFFFFFFF | not checked |
| 8 | u16 | sd_req count n | 1 | |
| 10 | n×u16 | sd_req list | 0x00B6 (S140 6.1.1) or 0x0123 (S140 7.3.0) | must contain the installed SoftDevice FWID or 0xFFFE, else NRF_ERROR_INVALID_DATA (`dfu_init.c:177-187`) |
| 10+2n | u16 | CRC16 of firmware.bin | CRC-16/CCITT-FALSE, init 0xFFFF | checked at STOP (`dfu_init.c:191-213`) |

CRC check values: RAK 0x49DF, Xiao 0xE09E (both equal the `.dat` trailer and the manifest). `"123456789"` gives 0x29B1. The same algorithm is in the bootloader (`Adafruit_nRF52_Bootloader lib/sdk/components/libraries/crc16/crc16.c:46-60`), `dfu.js:68-80` and `nrfutil crc16.py:29-45`.

**How dfu.js reads the zip** (`dfu.js:376-417`):

1. It opens the port first (`await this.port.open({ baudRate: 115200 })`, line 385), then parses the zip.
2. zip.js reads each entry. `manifest.json` → JSON; names ending `.bin` or `.dat` → bytes (lines 387-402).
3. It needs `manifest.manifest.application.bin_file` and `.dat_file` (lines 409-413). No `softdevice`, `bootloader` or `softdevice_bootloader` support. The OTAFIX zip makes it throw `TypeError: Cannot read properties of undefined (reading 'bin_file')` after the port opens and before any write (`proto-nrf52/out/blzip.jsonl`).
4. It checks neither `dfu_version`, nor `init_packet_data`, nor the CRC. The `.dat` is sent verbatim.

**Checks the Go app should add before START** (none changes the bytes sent):

1. Parse and check the zip **before** opening the port (nrfutil's order).
2. The manifest has `application` and nothing else (3.9).
3. `len(firmware.bin) % 4 == 0` (`OTAFIX dfu_single_bank.c:297-299`).
4. `.dat`: length ≥ 14, device_type 0x0052, trailer == CRC16(firmware.bin).
5. sd_req fits the board's SoftDevice. The catalog `erase` name gives it: `…softdevice_v6` → 0x00B6, `…_v7` → 0x0123. The MeshCore board JSON `build.softdevice.sd_fwid` gives it too (11.C3).
6. The port is positively identified as the bootloader (5.2).

### 5.2 Entering the bootloader: the 1200-baud touch

**In the web flasher** it is a separate button, "Enter DFU mode": `Dfu.forceDfuMode(await serialAPI.requestPort({}))` (`flasher js/app.js:217-220`). That is `open({baudRate:1200})` → sleep 100 ms → `close()` → sleep 1500 ms (`dfu.js:4-6,270-284`). "Flash!" then calls `requestPort` again (`js/app.js:296`). The user picks the bootloader's port by hand. There is no waiting or matching.

**On the board** (MeshCore's core d541301 with TinyUSB 7b30ff9):

- `tud_cdc_line_state_cb(instance, dtr, rts)` checks `!dtr`, instance 0 and `coding.bit_rate == 1200`, then calls `TinyUSB_Port_EnterDFU()` (`Adafruit_TinyUSB_Arduino src/arduino/Adafruit_USBD_CDC.cpp:269-286`).
- That calls `enterSerialDfu()` (`src/arduino/ports/nrf/Adafruit_TinyUSB_nrf.cpp:91-94`): `GPREGRET = 0x4E` (DFU_MAGIC_SERIAL_ONLY_RESET) and `NVIC_SystemReset()` (`meshcore-Adafruit_nRF52_Arduino cores/nRF5/wiring.c:26,90-92,103-107`).
- **The trigger is DTR going low while the line coding is 1200.** The WebUSB polyfill shows the USB control requests (`flasher lib/polyfill/serial.js:23-24,234-276,314-330,413-430`):
  1. SET_LINE_CODING (0x20): `B0 04 00 00 00 00 08` (1200 baud, 1 stop bit, no parity, 8 data bits);
  2. SET_CONTROL_LINE_STATE (0x22) wValue 0x0001 (DTR on);
  3. on close, SET_CONTROL_LINE_STATE wValue 0x0000.

**In the bootloader** (`OTAFIX src/main.c:223-325`):

- GPREGRET 0x4E → `serial_only_dfu` → `usb_init(true)`: **CDC only**, PID = `USB_DESC_CDC_ONLY_PID` (`usb_desc.c:126`).
- `bootloader_dfu_start(false, 3000, true)`: a 3 s timer, cancelled if USB is mounted when it fires, or if a DFU packet arrived (`OTAFIX bootloader.c:81-100,323-353`).
- So with a USB host the board **stays in the bootloader** until DFU completes or it is reset. If the host does not configure it within 3 s, it goes back to the app. Upstream 0.6.1 and 0.9.2 behave the same.
- The bootloader has no line-state handler, so a touch on a board that is already in the bootloader does nothing.

**Linux recipe** (kernel v6.12 sources; nRF52 report §9.1):

1. `open("/dev/ttyACMn", O_RDWR|O_NOCTTY|O_NONBLOCK)`. The kernel raises DTR and RTS (`linux tty_port.c:524-529`, `cdc-acm.c:665-683`).
2. `TCGETS`; set raw mode and B1200 in/out; keep `HUPCL|CLOCAL|CREAD`; `TCSETS`. cdc-acm sends SET_LINE_CODING with 1200 (`cdc-acm.c:1080-1116`).
3. Sleep 100 ms.
4. Optionally drop DTR with `TIOCMBIC TIOCM_DTR` (PlatformIO does `setDTR(False)` before close, `platformio-core pioupload.py:47-49`).
5. `close()`. With `HUPCL` (cdc-acm default `init_termios.c_cflag = B9600|CS8|CREAD|HUPCL|CLOCAL`, `cdc-acm.c:2082-2084`) the tty layer drops DTR and RTS (`tty_port.c:372-376`). The board resets into the bootloader.
6. Sleep 1500 ms, as dfu.js does.

**DTR drops only on the last close.** `tty_port_close_start` decrements `port->count` and returns 0 while it is still above 0 (`linux v6.12 tty_port.c:625-650`), so `tty_port_close` skips the shutdown that lowers DTR and RTS (`:365-376,715-720`). If ModemManager, a console, or a second fd of our own holds the port, close() leaves DTR high and **the touch silently does nothing**. So: open with `TIOCEXCL` (blocks other non-root opens), make sure no other process holds the port (for example by scanning `/proc/*/fd` for the tty), and drop DTR explicitly with `TIOCMBIC TIOCM_DTR` while the line is at 1200 (step 4 is then required, not optional). This is source inference, **UNVERIFIED** on hardware.

**Termios persists per tty minor** (cdc-acm has no `TTY_DRIVER_RESET_TERMIOS`, `cdc-acm.c:2072-2073`). Every later open must set 115200 explicitly. **Never close a port that is still at 1200**, or the MeshCore app reboots into DFU.

**Finding the bootloader's port:**

- cdc-acm takes the **lowest free minor** (`cdc-acm.c:88-97`). The old minor is freed only when its last fd closes (`:745`). If our process closed the app port first, the bootloader usually comes back as the same `ttyACMn`. If anything (us, ModemManager, a console) still holds it, it becomes another number.
- `/dev/serial/by-id/usb-<mfr>_<product>_<serial>-if00` **changes**, because manufacturer and product change. The serial part stays.
- The USB serial number is the same in app and bootloader: 16 upper-case hex characters of FICR DEVICEID, `printf("%08X%08X", DEVICEID[1], DEVICEID[0])` in both (`OTAFIX src/usb/usb_desc.c:130-142`; `Adafruit_TinyUSB_nrf.cpp:96-103`, `Adafruit_USBD_Device.cpp:278-298`). Source analysis only; **UNVERIFIED on hardware**.
- Rule: remember the serial before the touch. Poll `/sys/class/tty/ttyACM*` every 100-250 ms for a device with that `serial`, the bootloader VID:PID, and interface string `nRF Serial`. Deadline about 10 s (PlatformIO uses 5 s, `pioupload.py:55-85`).
- **Refuse to send START unless the port is positively identified as the bootloader.**

Other tools: nrfutil `--touch 1200` does the same 0.1 s / 1.5 s, then reopens the **same path** at 115200 and sleeps 0.1 s (`nrfutil dfu_transport_serial.py:96-127`). PlatformIO opens at 1200, sets DTR=0, closes, sleeps 0.4 s, then waits ≤ 5 s for a new port (`pioupload.py:43-85`).

**Other ways into the bootloader:**

- Double-press reset within 500 ms (`DFU_DBL_RESET_DELAY`): CDC + mass storage, UF2 PID, no timeout (`OTAFIX main.c:115-118,238,273-279,312-316`). Serial DFU works here too, with no touch.
- The "Erase Flash" firmware ends in `enterUf2Dfu()` (GPREGRET 0x57) (`nRF52-Flash-Format src/main.cpp:9-20`).
- MeshCore CLI `get bootloader.ver` replies `> 0.9.2-OTAFIX2.2` or `> unknown` (`MeshCore src/helpers/CommonCLI.cpp:783-790`, `NRF52Board.cpp:393-410`). It can detect OTAFIX before flashing.

### 5.3 HCI / SLIP framing

Encoder: `dfu.js:43-64,88-114`, identical to `nrfutil util.py:82-102,158-179` and `dfu_transport_serial.py:339-373`.

Frame = `C0 | SLIP( H0 H1 H2 H3 | payload | CRC_lo CRC_hi ) | C0`. SLIP: `C0` → `DB DC`, `DB` → `DB DD`, over header, payload and CRC.

| Byte | Value | Meaning |
|---|---|---|
| H0 | `seq \| ((seq+1)&7)<<3 \| 0x40 \| 0x80` | seq, ack, data-integrity bit (DIP=1), reliable bit (RP=1) (`dfu.js:15-16,43-50`) |
| H1 | `0x0E \| (L&0x0F)<<4` | packet type 14 (vendor specific), low 4 bits of payload length L (`dfu.js:17`) |
| H2 | `(L>>4)&0xFF` | high 8 bits of L (12-bit length, without header and CRC) |
| H3 | `(-(H0+H1+H2))&0xFF` | header checksum: `(H0+H1+H2+H3) & 0xFF == 0` |
| CRC | CRC-16/CCITT-FALSE, init 0xFFFF, over the **unescaped** header and payload | sent **low byte first** (`dfu.js:106-109`) |

- **Sequence**: static, pre-incremented mod 8 in the constructor, reset to 0 at the start of `dfuUpdate` (`dfu.js:89-92,382`). So **the first frame has seq 1**, then 2…7, 0, 1…
- H0 per seq: 1 `D1`, 2 `DA`, 3 `E3`, 4 `EC`, 5 `F5`, 6 `FE`, 7 `C7`, 0 `C8`. None needs escaping. H3 and CRC bytes can be `C0` or `DB`.
- **What the bootloader accepts** (`bootloader lib/sdk/components/libraries/hci/hci_transport.c:109-154`, same in OTAFIX): decoded length > 4; DIP and RP set; `(H1 & 0x0F) == 14`; header sum ≡ 0; CRC over `len-2` bytes matches. **The 12-bit length is not checked.** A bad frame is dropped with **no ACK**.
- Bootloader SLIP decoder: ignores everything until the first `C0`; an empty frame (`C0 C0`) is ignored (`hci_slip.c:258-273,320-326`). A DATA frame decodes to 4+4+512+2 = 522 bytes.
- Buffers: the compiled memory pool is `lib/sdk/components/libraries/hci/hci_mem_pool.c` (OTAFIX `Makefile:164`; `hci_mem_pool.c:50,68`), sized by `src/sdk_config.h:68-69`: **HCI RX buffer 600 bytes, HCI RX queue 16 entries** (upstream 0ee8343 `src/sdk_config.h:66-67`, same values). The `hci_mem_pool_internal.h` files under `sdk11/…/bootloader_dfu/` are not used by it. Separately, the DFU data queue has `MAX_BUFFERS = 4` and reports full at 3 entries (`OTAFIX lib/sdk11/components/libraries/bootloader_dfu/dfu_transport_serial.c:24,32-33`).
- **ACK before queueing.** The HCI layer ACKs a frame before handing it to the DFU layer. `rpc_transport_event_handler` then tries `data_queue_element_alloc`; if the DFU queue is full, the frame is freed with `hci_transport_rx_pkt_consume` after it was already ACKed (`dfu_transport_serial.c:256-283`). **The Go DFU code must keep exactly one frame in flight and never pipeline**, as dfu.js does (it reads each ACK before the next write, `dfu.js:157-171`).

**ACK from the bootloader** (`hci_transport.c:190-212`): 4 bytes `[expected<<3, 00, 00, (-(expected<<3))&0xFF]`, no CRC, SLIP-framed, never escaped. **Every ACK is exactly 6 bytes on the wire:**

| `expected` | ACK bytes |
|---|---|
| 0 | `C0 00 00 00 00 C0` |
| 1 | `C0 08 00 00 F8 C0` |
| 2 | `C0 10 00 00 F0 C0` |
| 3 | `C0 18 00 00 E8 C0` |
| 4 | `C0 20 00 00 E0 C0` |
| 5 | `C0 28 00 00 D8 C0` |
| 6 | `C0 30 00 00 D0 C0` |
| 7 | `C0 38 00 00 C8 C0` |

- The bootloader starts with `expected = 1` (`hci_transport.c:56,615`).
- A valid frame with `seq == expected`: `expected` += 1 (mod 8), and the ACK is sent **at once, before the DFU layer processes the packet** (`hci_transport.c:271-302`).
- A valid frame with the wrong seq: the ACK is re-sent with the unchanged `expected`, and the frame is dropped (`hci_transport.c:304-314`).
- So after the host sends seq `s`, the good ACK carries `(s+1)%8`. **An ACK means "received", never "accepted".** No DFU status is ever returned, and an ACKed frame can still be dropped if the DFU queue is full (5.3, buffers).

### 5.4 Packet sequence and timing

Every payload starts with a u32 LE packet type. The bootloader dispatches on `payload[0]` (`OTAFIX dfu_transport_serial.c:267`) and takes the body as `floor(payload_len/4) - 1` words (`:271`). **Any non-multiple-of-4 tail is silently cut off.**

| # | Write (payload, LE) | Then |
|---|---|---|
| 0 | open the tty at 115200, raw 8N1, no flow control, do not touch DTR/RTS (`dfu.js:385`) | |
| 1 | START `03000000` `04000000` (mode 4 = application) `00000000` (SD size) `00000000` (BL size) `<appSize u32>`, L = 20 (`dfu.js:286-296,425`) | read ACK; sleep `max(500, (appSize/4096 + 1) × 89.7)` ms, **float division** (`dfu.js:298-301`) |
| 2 | INIT `01000000` + `.dat` (14 B) + `0000`, L = 20 (`dfu.js:259-267,428`) | read ACK; sleep 102.4 ms (`FLASH_PAGE_WRITE_TIME = (4096/4) × 0.0001 s`, `dfu.js:9-12,350`) |
| 3…N+2 | DATA `04000000` + `firmware[i×512 : (i+1)×512]`, L = 516, last chunk shorter (`dfu.js:13,337-345`) | read ACK; if `(i+1) % 8 == 0` sleep 102.4 ms (one 4 KiB page) (`dfu.js:362-365`) |
| N+3 | (no write) | sleep 102.4 ms (`dfu.js:368-369`) |
| N+4 | STOP `05000000`, L = 4 (`dfu.js:371-373`) | read ACK |
| end | close: reader.cancel, writer.close, port.close; errors ignored (`dfu.js:438-469`) | nothing more |

- Each frame goes out in one write and its ACK is read **before** any sleep (`dfu.js:157-171`).
- **The 2 zero bytes after the `.dat` matter.** 4+14+2 = 20 → 4 words → `.dat` + `0000`, CRC intact. Without them: 18 → 3 words → the CRC is cut off → `NRF_ERROR_INVALID_LENGTH` (`OTAFIX dfu_init.c:121-126`), and that comes **after** the erase.
- `appSize` must be a multiple of 4. Both samples are (440,924 and 360,768).
- `DFU_ERASE_PAGE = 6` and `eraseFlash()` exist but run only with `new Dfu(port, true)`, which `app.js` never uses (`dfu.js:23,305-329`; `js/app.js:264,311`). Never send type 6.
- When the DATA count is a multiple of 8, two 102.4 ms sleeps come back to back (`fixtures/tiny-4096.zip`).

**Recorded run, RAK4631 repeater v1.18.0** (`proto-nrf52/out/rak4631-ok.jsonl`):

```
OPEN {"baudRate":115200}
W START seq=1 len=20 wire=28  c0d14e01e0030000000400000000000000000000005cba0600fa06c0
SLEEP 9745.677 ms                         # (440924/4096+1)*89.7
W INIT  seq=2 len=20 wire=28  c0da4e01d7010000005200ffffffffffff0100b600df490000bbd2c0
SLEEP 102.4
W DATA#1..#8   (seq 3,4,5,6,7,0,1,2; len 516; wire 524..529 depending on escapes)
SLEEP 102.4
… (sleep after every 8th DATA) …
W DATA#862 seq=0 len=96 (92 data bytes) wire=104
SLEEP 102.4
W STOP seq=1 len=4 wire=12  c0d14e00e1050000007482c0
WRITER-CLOSE, CLOSE
```

Totals: 865 frames, 454,160 wire bytes, 862 DATA frames, 110 sleeps totalling 20.907 s. For the Xiao zip (360,768 B) the erase wait is 7,990.308 ms.

**What the bootloader does meanwhile** (OTAFIX serial path):

- START (`dfu_single_bank.c:280-337`): rejects if app is combined with SD/BL, any size is not word-sized, sizes are too big, or the state is not IDLE. Then it **erases `ceil(size/4096)` pages of bank 0 in a blocking loop** (`:117-129`) and marks bank 0 invalid. For 440,924 B that is 108 pages at up to ~85-90 ms each. dfu.js waits 9.75 s: a thin margin. USB is not serviced during the erase; an early frame waits in USB NAK and its ACK arrives after the erase, which must still be inside dfu.js's 5 s read timeout.
- INIT (`dfu_single_bank.c:415-438,442-…`): stores up to 64 bytes (OTAFIX `m_init_packet[64]`, line 34), then checks device_type and sd_req.
- DATA (`dfu_single_bank.c:341-400`): copied into a 4 KiB page cache; the previous page is written when a new page starts (about 42 ms, blocking; `OTAFIX src/flash_nrf5x.c:36-72`). More data than announced → `NRF_ERROR_DATA_SIZE`, and all later data is blocked.
- The bootloader ignores line coding and control lines (no handlers in `src/`); SLIP runs via `tud_cdc_rx_cb` (`hci_slip.c:361-368`). USB NAK gives the back-pressure.

**ACK handling in dfu.js** (`dfu.js:131-230`):

- Each `reader.read()` has a **5000 ms** timeout (`DEFAULT_SERIAL_PORT_TIMEOUT*1000*5`, line 140). On timeout: `Error("Read timeout")`.
- It reads until it has seen two `C0`. Bytes after the second `C0` in the same chunk are dropped.
- It SLIP-decodes the bytes between them; needs ≥ 2 bytes ("Received incomplete ACK."); `ack = (b0>>3)&7`.
- If this is not the first ACK of the session and `ack != (last+1)%8`: "Invalid ACK sequence. Expected X, got Y". **The first ACK is accepted whatever its value** (`lastAck` reset to -1 at line 381).
- The ACK header checksum is not verified. There is no overall deadline.
- On any error: seq := 0, close the port, fail. **No retransmission, no retry.** The message is `nRF flashing failed: <e>. Please reset the device and try again.` (`flasher js/flash.js:89-97`).

### 5.5 Golden vectors

```
RAK_4631_repeater-v1.18.0-81dbae5.zip (appSize 440924, .dat 5200ffffffffffff0100b600df49):
 START  c0d14e01e0030000000400000000000000000000005cba0600fa06c0
 INIT   c0da4e01d7010000005200ffffffffffff0100b600df490000bbd2c0
 DATA#1 header c0e34e20af04000000…
 DATA#862 seq 0, L=96
 STOP (seq 1) c0d14e00e1050000007482c0
 erase wait 9745.677 ms; 862 DATA; 865 frames; 454160 wire bytes
proto-nrf52/fixtures/tiny-1028.zip (fw[i]=(7i+0xC0)&0xFF, n=1028, crc 0x0086):
 START  c0d14e01e003000000040000000000000000000000040400005ab9c0     (sleep 500)
 INIT   c0da4e01d7010000005200ffffffffffff0100b60086000000cbd7c0     (sleep 102.4)
 DATA#1 c0e34e20af04000000dbdcc7ced5dce3…b2b959fdc0 (528 B wire)
 DATA#2 c0ec4e20a604000000dbdcc7ced5dce3…b2b91d31c0 (528 B wire)
 DATA#3 c0f58e007d04000000dbdcc7ced55c3ac0            (17 B)          (sleep 102.4)
 STOP   c0fe4e00b405000000bb16c0
CRC16 check: "123456789" -> 0x29B1
```

The first ACK a real bootloader sends is `C0 10 00 00 F0 C0` (expected 2, after START seq 1).

### 5.6 Failure behaviour

Every error while processing START or INIT goes through `APP_ERROR_CHECK` → `app_error_fault_handler` → `NVIC_SystemReset()` (`OTAFIX dfu_transport_serial.c:218-231`, `src/main.c:392-396`). The host already has the ACK, so it only notices on the *next* packet: a timeout, or an I/O error as the USB device vanishes.

| Event | Board state afterwards | What the host sees |
|---|---|---|
| START rejected for size: app combined with SD/BL, a size not word-aligned, or too big (`dfu_single_bank.c:290-316`) | reset **before** the erase; the old app boots | timeout or I/O error on INIT |
| START rejected for state: `NRF_ERROR_INVALID_STATE` when the state is not IDLE (`dfu_single_bank.c:333`) | A non-IDLE state exists only after an earlier START was processed (`dfu_init` sets IDLE, `:268`; START sets PREPARING and erases, `:100,117-129`), so **the app is already erased**. After the reset `valid_app == false`: OTAFIX goes to Bluetooth OTA mode (`src/main.c:289-291`) | timeout or I/O error on INIT. Recovery: double-press reset, reflash |
| INIT rejected (device_type, sd_req not matching the installed SoftDevice, truncated init) | **app already erased.** After the reset `valid_app == false`. OTAFIX 2.1+: **Bluetooth OTA DFU mode, no USB serial port, no UF2 drive** (`OTAFIX src/main.c:289-291`; `README.md:109-121`). Upstream 0.9.2: USB CDC+MSC DFU, no timeout | timeout or I/O error on the first DATA |
| CRC mismatch at STOP | result ignored (`(void)` at `dfu_transport_serial.c:234-235`); no activation; bank stays invalid; stays in serial DFU | **STOP is ACKed; dfu.js reports success** |
| More data than announced | `NRF_ERROR_DATA_SIZE`; later data blocked | (not observed by host) |
| Host stops mid-transfer, board stays powered | stays in serial DFU indefinitely: OTAFIX and 0.9.2 have **no inactivity timer**. Upstream HEAD has 360 s and exit on VBUS loss (`bootloader lib/sdk11/…/dfu_bank_internal.h:43`, `bootloader.c:104-160`), but the catalog does not ship it | a new session starts at seq 1 while the bootloader expects some k (`hci_transport.c:271-314`; dfu.js accepts any first ACK, `dfu.js:223-227`). **k = 1:** START is accepted and hits a non-IDLE state → `NRF_ERROR_INVALID_STATE` (`dfu_single_bank.c:333`) → reset → app invalid → OTAFIX Bluetooth OTA mode. **k = 2:** START is dropped and re-ACKed with 2; INIT (seq 2) is accepted and ACKed with 3, which dfu.js accepts. If the old session had sent DATA, `dfu_init_pkt_complete` returns `NRF_ERROR_INVALID_STATE` (`dfu_single_bank.c:415-424`) → reset; the host sees a 5 s timeout or an I/O error on DATA#1. If the old session stopped between START and INIT (state RDY), the INIT is accepted against the **old** START and its image size, and the transfer continues (source inference, **UNVERIFIED**). **Any other k:** `Invalid ACK sequence. Expected k+1, got k` on INIT. In every case reset the board first |

Measured with the harness (`proto-nrf52/out/xiao-*.jsonl`):

| Scenario | Result |
|---|---|
| no ACK for START (`drop-ack:0`) | `Read timeout` after 5.0 s; 1 write; port closed |
| no ACK for INIT (`drop-ack:1`) | `Read timeout` 5.0 s after the INIT write |
| wrong ACK number on DATA#1 (`bad-ack:2`) | immediate `Invalid ACK sequence. Expected 4, got 7` |

**So a retry needs the board reset first.** If START already erased the app, on OTAFIX the owner must double-press reset to get a USB DFU port back (section 10.2).

### 5.7 Success signal and verification

- At STOP the bootloader checks `received == size` and the CRC-16 of bank 0 against the init packet (`dfu_single_bank.c:482-505`, `dfu_init.c:191-213`). On success it saves `{bank_0 VALID, crc, size}`, calls `usb_teardown()` (`OTAFIX main.c:318-323`) and jumps to the app (`main.c:202`). Every later boot recomputes the CRC (`OTAFIX bootloader.c:148-180`).
- The only visible success signal: the bootloader's USB device disappears and the app's appears (app VID:PID and product, interface "TinyUSB Serial", same serial number).
- dfu.js closes the port right after the STOP ACK and verifies nothing. nrfutil sleeps `get_activate_wait_time()` after STOP (`nrfutil dfu_transport_serial.py:164-176`, `dfu.py:212-215`). With `--singlebank` and no SoftDevice: a fixed 0.0897 + 0.1024 = 0.1921 s. Otherwise it depends on the size: `max(0.5, (size//4096+1)×0.0897) + (size//4096+1)×0.1024` s, i.e. 20.747 s for RAK (440,924 B) and 17.097 s for Xiao (360,768 B).
- The web flasher's progress bar overshoots: it reaches 100 % after DATA 842 of 862, and "Flashing complete!" appears about 20 frames early (`dfu.js:355-359`; `index.html:50-71`).
- **Recommendation:** after STOP, wait (about 10 s) for the app to re-enumerate with the same serial and "TinyUSB Serial". Then, for repeater and room-server firmware, send `ver` (section 7). This catches the silent CRC failure. It is an extension (11.C2).

### 5.8 dfu.js versus nrfutil

| # | Aspect | dfu.js | nrfutil |
|---|---|---|---|
| 1 | frame bytes, seq start 1, 512-B chunks, INIT `+0000` | identical | identical |
| 2 | erase wait | `(size/4096+1)×0.0897` s, float: 9745.68 ms (RAK) | `(size//4096+1)×0.0897`, int: 9687.6 ms (`py:164-166`) |
| 3 | page sleeps | before DATA#1, after #8, #16, …, plus one at the end | after DATA#1, #9, #17, … (`count%8==0`, `py:228-229`), plus one at the end |
| 4 | after STOP | close at once | close, then sleep 0.1921 s (singlebank) or the size-dependent default (20.747 s RAK, 17.097 s Xiao; 5.7) |
| 5 | ACK number check | yes, except the first ACK | **none** (bug: `last_ack` is a local `None`, `py:252-253`) |
| 6 | ACK timeout | 5 s per read, no overall cap | 1 s window, 6-byte reads with a 1 s serial timeout (`py:57`) |
| 7 | retries | none | none |
| 8 | DTR | never touched | without touch: DTR low 50 ms, then high (`py:129-136`) |
| 9 | touch | separate button; user re-picks the port | `--touch 1200`, then reopens the same path |
| 10 | package types | application only | SD+BL, SD, BL, app (`dfu.py:220-235`) |
| 11 | zip parse vs open | opens port, then parses | parses first |

---

## 6. Board identification

### 6.1 The reference does none

Every port picker in the web flasher is `serialAPI.requestPort()` / `requestPort({})` with no filters (`flasher js/app.js:174,218,264,296`). The user picks the board in the catalog by hand. The only USB ID the reference reads is the PID, to choose the ESP32 reset (`ejs esploader.ts:140,584`).

So board suggestion is an **extension**. It never changes the bytes sent. **It must never select a board silently; the user always confirms.**

### 6.2 What decides a board's USB identity

**ESP32 family, app mode.** MeshCore's CLI uses Arduino `Serial` (`MeshCore examples/simple_repeater/main.cpp:39`). Which `Serial` it is depends on two build flags (arduino-esp32 2.0.17):

| `ARDUINO_USB_MODE` | `ARDUINO_USB_CDC_ON_BOOT` | `Serial` is | Source |
|---|---|---|---|
| 1 | 1 | HWCDC: the chip's fixed USB-Serial-JTAG | `cores/esp32/HWCDC.h:110-114`, `HWCDC.cpp:605-609` |
| 0 | 1 | USBCDC: TinyUSB on USB-OTG | `USBCDC.h:141-142` |
| any | 0 (default) | UART0; the USB connector goes through a bridge chip | `HardwareSerial.h:326-332` |

Classic ESP32 has no USB, so it always uses a bridge. PlatformIO takes the board JSON from MeshCore's `boards/` first (`platformio-core platformio/platform/base.py:143-147`).

**HWCDC (USB-Serial-JTAG)** is fixed hardware (`esp-idf docs api-guides/usb-serial-jtag-console.rst:17`):

- `303a:1001`, bcdDevice 1.01, manufacturer "Espressif", product "USB JTAG/serial debug unit", serial = MAC as `XX:XX:XX:XX:XX:XX`, 3 interfaces (CDC if0/if1, JTAG if2) (openocd-esp32 `303a_1001_esp_usb_jtag.txt`).
- by-id name: `usb-Espressif_USB_JTAG_serial_debug_unit_60:55:F9:C0:27:18-if00` (esp-rs training page).
- The same identity in the app **and** in ROM download mode. USB cannot tell them apart; probe instead (SYNC answers only in ROM, the CLI only in the app).
- Board JSON `hwids` do not apply in this mode.

**TinyUSB CDC** (`arduino-esp32 cores/esp32/USB.cpp:24-38,170-175`): VID 0x303A, PID 0x0002 by default, manufacturer "Espressif Systems", product = board JSON `name` (`tools/platformio-build-esp32s3.py:344`), serial = MAC as 12 hex digits, CDC interface "TinyUSB CDC" (`USBCDC.cpp:29`). PlatformIO does not turn `hwids` into USB_VID/PID (`platform-espressif32 builder/main.py:488-505`). Overrides to `303a:1001` come from `pins_arduino.h`: `MeshCore variants/lilygo_tbeam_1w/pins_arduino.h:6-7`, `thinknode_m2:11-12`, `thinknode_m5:11-12`, `thinknode_m7:6-7`, `thinknode_m9:11-12`, `heltec_v4_r8:6-7`, `heltec_rc32:6-7`, and arduino-esp32 `variants/esp32s3/pins_arduino.h:7-8`. E213, E290 and Tracker v2 define none, so they are `303a:0002`.

**TinyUSB reset into ROM** (`arduino-esp32 USBCDC.cpp:193-233,254-258`; `esp32-hal-tinyusb.c:402-414,471,488-497`):

- The line-state sequence (DTR,RTS) = (0,1) → (1,1) → (1,0) → (0,0), or a line coding of 1200 baud, reboots into ROM.
- First it switches the PHY to USB-Serial-JTAG and sets `RTC_CNTL_FORCE_DOWNLOAD_BOOT`. The ROM then appears as **a new USB device**, `303a:1001`, with new strings and a new serial format. The tty may change.
- Simulated against esptool-js 0.5.4 (source inference, **UNVERIFIED on hardware**): `ClassicReset` produces (0,1) → (1,1) → (1,0) → (0,0) and **does** reboot E213, E290 and Tracker v2 (`303a:0002`), which then re-enumerate. `UsbJtagSerialReset` produces (1,0) → (0,0) → (1,0) → (1,1) → (0,1) → (0,0) and **does not** reset T-Beam 1W, Station G2 and Station G3 (TinyUSB at `303a:1001`). The user would then have to hold BOOT, press RESET and pick the new port.
- **Tell real USB-Serial-JTAG from TinyUSB by the product string, not by the PID.**
- Never leave a TinyUSB ESP32-S3 port at 1200 baud: that also reboots it into ROM (source inference).

**Other ROM identities:** bridge boards keep their identity. An ESP32-S3 ROM over USB-OTG would be `303a:0009` (`esptool loader.py:1343-1347`, `targets/esp32s3.py:17-19`); not expected from MeshCore boards. `10c4:ea64` (CP2102C) needs special reset handling (`esp-pylib esp_pylib/constants.py:28-38`); no catalog board is known to use it.

**nRF52840 app:** VID:PID = board JSON `build.hwids[0]`, product = `build.usb_product`, manufacturer = `vendor` (`builder-framework-arduino-nrf5 adafruit.py:233-239`). CDC interface "TinyUSB Serial" (`Adafruit_USBD_CDC.cpp:105`). No MeshCore env overrides them.

**nRF52840 bootloader:** VID = `USB_DESC_VID`; PID = `USB_DESC_UF2_PID` (double-reset, CDC+MSC) or `USB_DESC_CDC_ONLY_PID` (after the touch); manufacturer/product = `BLEDIS_MANUFACTURER` / `BLEDIS_MODEL`; interface strings "nRF Serial" and "nRF UF2" (`OTAFIX src/usb/usb_desc.c:58-60,126,153-157`). Serial = the app's serial.

**Bridges:**

| Chip | VID:PID | Linux driver | Node | Strings |
|---|---|---|---|---|
| CP2102 / CP2104 | 10c4:ea60 | cp210x (`linux v6.18 drivers/usb/serial/cp210x.c:170`) | ttyUSB | "Silicon Labs" / "CP2102 USB to UART Bridge Controller" / serial "0001" (often not unique) (WSL#7649) |
| CH340 | 1a86:7523 | ch341 (`ch341.c:90`) | ttyUSB | no manufacturer, "USB2.0-Serial", **no serial**; by-id `usb-1a86_USB2.0-Serial-if00-port0` (oddbit blog) |
| CH340K | 1a86:7522 | ch341 (`ch341.c:89`) | ttyUSB | **UNVERIFIED** |
| CH9102(F) | 1a86:55d4 | not in ch341's table (`ch341.c:88-93`) → cdc_acm (`cdc-acm.c:2006-2012`) | **ttyACM** | **UNVERIFIED** (Meshtastic lists 55d4 as ttyACM, `meshtastic/supported_device.py:50-66,113-121`) |

### 6.3 Per-device table

"HWCDC" = `303a:1001`, "Espressif" / "USB JTAG/serial debug unit", the same in app and ROM. "`board`" is the reply of the MeshCore `board` CLI command (`getManufacturerName()`). Full table with file:line per row: `scratchpad/research/usb-ids.md` §3.

**ESP32 devices:**

| Catalog device | MCU | USB path (evidence) | App VID:PID, product | ROM VID:PID | `board` reply |
|---|---|---|---|---|---|
| Heltec v4, Heltec v4 + Expansion Kit (Touch) | S3 | HWCDC (`MeshCore boards/heltec_v4.json:9`; heltec.org "Removed CP2102") | 303a:1001 | 303a:1001 | "Heltec V4 OLED"/"TFT", "Heltec V4.3 …" |
| Heltec v4 R8, v4 R8 + Expansion Kit *(live only)* | S3 | HWCDC (`heltec_v4_r8.json:9`) | 303a:1001 | 303a:1001 | "Heltec V4 R8 OLED"/"TFT" |
| LilyGo T-Deck | S3 | HWCDC (`t-deck.json:9` + env CDC_ON_BOOT; Meshtastic verified 303a:1001) | 303a:1001 | 303a:1001 | "LilyGo T-Deck" |
| LilyGo T3 S3 (SX126x), (SX127x) | S3 | HWCDC (`t3_s3_v1_x.json:9`) | 303a:1001 | 303a:1001 | "Generic ESP32" |
| LilyGo T-Beam Supreme (SX1262) | S3 | HWCDC (`t_beams3_supreme.json:9`) | 303a:1001 | 303a:1001 | "LilyGo T-Beam" |
| Heltec Wireless Tracker | S3 | HWCDC (env `CDC_ON_BOOT=1`, `heltec_tracker/platformio.ini:8`) | 303a:1001 (source inference) | 303a:1001 | **"Heltec V3"** |
| RAK WisBlock 3112 | S3 | HWCDC (`rak3112/platformio.ini:10`) | 303a:1001 (source inference) | 303a:1001 | "RAK 3112" |
| Seeed Xiao S3 WIO | S3 | HWCDC (`platform-espressif32-v6.11.0/boards/seeed_xiao_esp32s3.json:12-13`, USB_MODE=1, CDC_ON_BOOT=1; selected by `board = seeed_xiao_esp32s3`, `variants/xiao_s3_wio/platformio.ini:3`; not in `MeshCore/boards`) | 303a:1001 | 303a:1001 | "Xiao S3 WIO" |
| Seeed Xiao C3 | **C3** | HWCDC (`platform-espressif32-v6.11.0/boards/seeed_xiao_esp32c3.json:9-10`, USB_MODE=1, CDC_ON_BOOT=1; selected by `board = seeed_xiao_esp32c3`, `variants/xiao_c3/platformio.ini:3`) | 303a:1001 | 303a:1001 | "Xiao C3" |
| LilyGo T-Beam 1W | S3 | TinyUSB (`t_beam_1w.json:8`; `pins_arduino.h:6-7`) | 303a:1001, "LilyGo TBeam-1W" (source inference) | 303a:1001 | "LilyGo T-Beam 1W" |
| UnitEng Station G2 | S3 | TinyUSB (`station-g2.json:8`; the kiss env uses HWCDC) | 303a:1001, "BQ Station G2" (source inference) | 303a:1001 | "Station G2" |
| UnitEng/BQ Voyage Station G3 | S3 | TinyUSB (`station-g3-esp32.json:8`) | 303a:1001, "Station G3 ESP32" (source inference) | 303a:1001 | "Station G3 ESP32" |
| Heltec Vision Master E213 | S3 | TinyUSB (`heltec_e213.json:9`) | **303a:0002**, "Heltec Vision Master E213" (source inference) | 303a:1001 | "Heltec E213" |
| Heltec Vision Master E290 | S3 | TinyUSB | **303a:0002**, "Heltec Vision Master E290" (source inference) | 303a:1001 | "Heltec E290" |
| Heltec Wireless Tracker v2 | S3 | TinyUSB (`heltec_tracker_v2.json:8`) | **303a:0002**, "heltec_tracker v2" (source inference) | 303a:1001 | "Heltec Tracker V2" |
| Heltec v2 | ESP32 | CP2102 (Meshtastic `supported_device.py:131-147`) | 10c4:ea60 | same | "Heltec V2" |
| Heltec v3 | S3 | CP2102 (devkitc JSON, no CDC_ON_BOOT; heltec.org) | 10c4:ea60 | same | "Heltec V3" |
| Heltec WSL3 | S3 | CP2102 (heltec.org WSL V3) | 10c4:ea60 | same | "Heltec V3" |
| Heltec Wireless Paper | S3 | CP2102 (schematic V0.4 U5; flag commented out at `variants/heltec_wireless_paper/platformio.ini:8`) | 10c4:ea60 | same | "Heltec Wireless Paper" |
| LilyGo T-Beam (SX1262), T-Beam 1.2 (SX1276) | ESP32 | CH9102; older revisions CP2104 (lilygo.cc, espboards.dev) | 1a86:55d4 (ttyACM) or 10c4:ea60 (ttyUSB) | same | "LilyGo T-Beam" |
| LilyGo LoRa32 V2.1_1.6 | ESP32 | CH9102 (wiki.lilygo.cc) | 1a86:55d4 (ttyACM) | same | "LILYGO T-LoRa V2.1-1.6" |
| Elecrow ThinkNode M2 | S3 | CH340K (Elecrow datasheet; DongLoRa) | 1a86:7522 (ttyUSB) | same | "Elecrow ThinkNode M2" |
| Elecrow ThinkNode M5, M7 | S3 | UART0 behind a bridge; chip **UNVERIFIED** | **UNVERIFIED** | same | "Elecrow ThinkNode M5" / "M7" |
| Ripple-only: T-Deck Max, T-Deck Pro, T-Deck Pro v1.1, T5 Pro, T-Lora Pager, T-Display Pro, T-Watch S3 Plus, T-Watch Ultra, Wio Tracker L2 Pro | **UNVERIFIED** | no MeshCore variant checked | **UNVERIFIED** | 303a:1001 if native (**UNVERIFIED**) | n/a |
| Elecrow ThinkNode M9 (Ripple-only **in the catalog**) | S3 (`boards/thinknode_m9.json:26`) | MeshCore variant exists (added 87bf371c, 2026-07-13; envs repeater, room server, companion BLE/USB/WiFi, kiss at `variants/thinknode_m9/platformio.ini:59,76,93,115,135,160`). Board JSON sets `ARDUINO_USB_CDC_ON_BOOT=0`, `ARDUINO_USB_MODE=0` (`boards/thinknode_m9.json:11-12`), so `Serial` is UART0 and the CLI needs a bridge chip (chip **UNVERIFIED**). No community release assets: 0 file names contain `M9` in the 2026-11-24 `/releases?repo=github` list | **UNVERIFIED** | **UNVERIFIED** | "Elecrow ThinkNode M9" (`variants/thinknode_m9/ThinkNodeM9Board.cpp:21-22`) |

**nRF52840 devices:**

| Catalog device | App VID:PID | App manufacturer / product | Bootloader VID:PID (UF2 / after touch), product | `board` reply |
|---|---|---|---|---|
| LilyGo T-Echo | 239A:8029 | "Nordic" / "NRF52 DK" | 239A:0029 / 0029, "T-Echo v1.0" | "LilyGo T-Echo" |
| LilyGo T-Echo Lite, T-Echo Card | 239A:8029 | "Nordic" / "NRF52 DK" | **UNVERIFIED** | "LilyGo T-Echo Lite" / "Card" |
| RAK WisBlock / WisMesh (RAK 4631) | 239A:8029 | "RAKwireless" / "WisCore RAK4631 Board" | 239A:0029 / 002A, "WisBlock RAK4631" | "RAK 4631" |
| GAT-IoT GAT562 Tracker, GAT562 30s | 239A:8029 | "RAKwireless" / "WisCore RAK4631 Board" | 239A:0029 / 002A | "GAT562 Mesh Tracker Pro" / "GAT562 30S Mesh Kit" |
| RAK WisMesh Tag | 239A:8029 | "RAKwireless" / "WisCore RAK4631 Board" | 239A:0029 / 002A, "WisMesh Tag" | "RAK WisMesh Tag" |
| Muzi Works R1 Neo | 239A:8029 | "RAKwireless" / "WisCore RAK4631 Board" | **UNVERIFIED** | "muzi works R1 Neo" |
| RAK WisMesh 1W Booster (3401 + 13302) | 239A:8029 | "RAKwireless" / "WisCore RAK3401 Board" | 239A:0029 / 002A, "WisBlock RAK3401" | "RAK 3401" |
| Heltec T114 | 239A:8029 | "Heltec" / "HT-n5262" | 239A:0071, "HT-n5262" | "Heltec T114" |
| Heltec Mesh Node T1 | 239A:8029 | "Heltec" / "HT-n5262T1" | 239A:0071, "T1" | "Heltec T1" |
| Heltec Mesh Node T096 | 239A:8029 | "Heltec" / "HT-n5262G" | 239A:0071, "Tracker" | "Heltec T096" |
| Heltec MeshSolar / MeshTower | 239A:8029 | "Heltec" / "HT-n5262" | **UNVERIFIED** | "Heltec Mesh Solar" |
| Heltec MeshPocket | **239A:4405** | "Heltec" / "HT-n5262" | **UNVERIFIED** | "Heltec MeshPocket" |
| Heltec MeshTower V2 | **239A:4405** | "Heltec" / "HT-n5262" | **UNVERIFIED** | "Heltec Tower V2" |
| Seeed SenseCAP T1000-E | 239A:8029 | "Seeed Studio" / "T1000-E-BOOT" | **2886:0057**, "T1000-E" | "Seeed Tracker T1000-E" |
| Seeed SenseCAP MeshTracker X1 | 239A:8029 | "Seeed Studio" / "X1-BOOT" | **UNVERIFIED** | "Seeed SenseCAP MeshTracker X1" |
| UnitEng Nano G2 Ultra | 239A:8029 | "BQ Consulting" / "BQ nRF52840" | **UNVERIFIED** | "Nano G2 Ultra" |
| Elecrow ThinkNode M1, M3 | 239A:4405 | "ELECROW" / "elecrow_eink" | 239A:00DA, "ThinkNodeM1" / "ThinkNode-M3" | "Elecrow ThinkNode-M1" / "Elecrow ThinkNode M3" |
| Elecrow ThinkNode M4 *(live only)* | 239A:4405 | "ELECROW" / "elecrow_thinknode_m4" | **UNVERIFIED** | "Elecrow ThinkNode M4" |
| Elecrow ThinkNode M6 | 239A:4405 | "ELECROW" / "elecrow_solar" | 239A:00DA, "ThinkNodeM6" | "Elecrow ThinkNode M6" |
| ProMicro nrf52 (faketec) | **239A:00B3** | "Nologo" / "ProMicro NRF52840" | **239A:00B3** (same PID) | "ProMicro DIY" |
| Keepteen LT1 | 239A:00B3 | "Keepteen" / "Keepteen LT1" | catalog ships the RAK4631 bootloader: 239A:0029 / 002A | "Keepteen LT1" |
| Seeed Xiao nRF52 WIO | 2886:8044 | "Seeed Studio" / "XIAO nRF52840" | 2886:0044 (Sense 0045) | "Seeed Xiao-nrf52" |
| Ikoka Stick, Ikoka Nano | 2886:8044 | "Seeed Studio" / "XIAO nRF52840" | 2886:0044 | "Ikoka Stick-E22-22dBm (Xiao_nrf52)" (or 30dBm, 33dBm); Nano likewise |
| Seeed SenseCAP Solar Node P1 | 2886:0059 | "Seeed Studio" / "XIAO nRF52840" | 2886:0044, "SenseCAP Solar Node P1" | "Seeed SenseCap Solar" |
| Seeed Wio Tracker L1 Pro, L1 EINK | **2886:1667** | "Seeed Studio" / "Seeed Wio Tracker L1" | **2886:1667** (same PID), "Wio Tracker L1" | "Seeed Wio Tracker L1" (both) |

Sources: app columns from `MeshCore boards/*.json` (`hwids[0]`, `vendor`, `usb_product`); bootloader columns from `OTAFIX src/boards/<b>/board.h`. Vendor factory bootloaders may differ and are **UNVERIFIED**.

**Other:** RPI Pico W rp2040: app 2e8a:f00a "Pico W" (arduino-pico `boards.txt:280-290`; under PlatformIO **UNVERIFIED**), BOOTSEL 2e8a:0003, no tty. RPI Pico W rp2350: BOOTSEL 2e8a:000f; no MeshCore env at HEAD. The git-only second "LilyGo T-Lora Pager": exclude.

### 6.4 Reverse lookup

| VID:PID | Strings | Mode | Candidates | Narrow by |
|---|---|---|---|---|
| 303a:1001 | "USB JTAG/serial debug unit", MAC serial with colons | app (HWCDC) **or** ROM of any native S3/C3 | app: Heltec v4 family, T-Deck, T3 S3 ×2, T-Beam Supreme, Wireless Tracker, RAK3112, Xiao S3, Xiao C3. ROM: those plus T-Beam 1W, E213, E290, Tracker v2, Station G2/G3, Ripple-only S3 boards | chip (C3 → Xiao C3); `board` in the app |
| 303a:1001 | product = board name, "Espressif Systems", 12-hex serial, "TinyUSB CDC" | app (TinyUSB) | "LilyGo TBeam-1W", "BQ Station G2", "Station G3 ESP32" | product decides |
| 303a:0002 | "Heltec Vision Master E213" / "…E290" / "heltec_tracker v2" | app (TinyUSB) | E213, E290, Tracker v2 | product decides |
| 303a:0009 | – | S3 ROM over USB-OTG | any S3 | not expected |
| 10c4:ea60 | "CP2102 USB to UART Bridge Controller" | app and ROM | Heltec v2, v3, WSL3, Wireless Paper, older T-Beams | chip: ESP32 → v2 or old T-Beam; S3 → v3, WSL3, Paper. `board` |
| 1a86:55d4 | **UNVERIFIED** | app and ROM | T-Beam SX1262, T-Beam 1.2 SX1276, LoRa32 V2.1_1.6 | `board`: T-Beam vs LoRa32; **the T-Beam radio cannot be told** |
| 1a86:7522 | – | app and ROM | ThinkNode M2 (maybe M5/M7, **UNVERIFIED**) | `board` |
| 1a86:7523 | "USB2.0-Serial" | app and ROM | none confirmed (M5/M7 possible) | `board`, chip |
| 239a:8029 | product as in 6.3 | app | 16 devices: T-Echo, T-Echo Lite, T-Echo Card, RAK4631, GAT562 Tracker, GAT562 30s, WisMesh Tag, R1 Neo, RAK3401 1W Booster, T114, T1, T096, MeshSolar/MeshTower, T1000-E, MeshTracker X1, Nano G2 Ultra | product, then `board` |
| 239a:4405 | "HT-n5262" or "elecrow_*" | app | MeshPocket, Tower V2; ThinkNode M1/M3, M4, M6 | product, `board` |
| 239a:00b3 | "ProMicro NRF52840" | app **and** bootloader | ProMicro | interface "TinyUSB Serial" vs "nRF Serial" |
| 239a:00b3 | "Keepteen LT1" | app | Keepteen LT1 | unique |
| 239a:0029 / 002a | "WisBlock RAK4631" / "WisBlock RAK3401" / "WisMesh Tag" | bootloader | RAK4631, GAT562 ×2, Keepteen LT1; 1W Booster; WisMesh Tag | serial number seen before the touch |
| 239a:0029 | "T-Echo v1.0" | bootloader | T-Echo | |
| 239a:0071 | "HT-n5262" / "T1" / "Tracker" | bootloader | T114 / T1 / T096 | product |
| 239a:00da | "ThinkNodeM1" / "ThinkNode-M3" / "ThinkNodeM6" | bootloader | M1 / M3 / M6 | product |
| 2886:8044 | "XIAO nRF52840" | app | Xiao nRF52 WIO, Ikoka Stick, Ikoka Nano | `board` (also gives Ikoka dBm) |
| 2886:0059 | "XIAO nRF52840" | app | SenseCAP Solar P1 | unique |
| 2886:0044 / 0045 | "XIAO nRF52840 (Sense)" / "SenseCAP Solar Node P1" | bootloader | Xiao nRF52, Ikoka ×2 / Solar P1 | product |
| 2886:0057 | "T1000-E" | bootloader | T1000-E | |
| 2886:1667 | "Seeed Wio Tracker L1" (app) / "Wio Tracker L1" (bootloader) | app **and** bootloader | Wio L1 Pro, Wio L1 EINK | mode by product or interface; Pro vs EINK **only by the user** |
| 2e8a:0003 / 000f | BOOTSEL | ROM, no tty | Pico W | not flashable here |

### 6.5 Narrowing further

1. **ESP32 chip type.** The flash flow reads it anyway: READ_REG 0x40001000 after SYNC (section 4.9). **The reference never checks it against the selected device** (`flasher js/flash.js:43-87`). A Go check "detected chip ≠ catalog MCU → stop" is a cheap guard against writing an ESP32 image to an S3 (11.D3).
2. **The `board` CLI command** on repeater and room-server firmware (`MeshCore src/helpers/CommonCLI.cpp:285-286`). Companion firmware returns the same string in its device-info frame (`examples/companion_radio/MyMesh.cpp:1069,2184-2185`). It only works if MeshCore already runs. It **cannot** separate: T-Beam SX1262 vs SX1276 ("LilyGo T-Beam"), T3 S3 SX126x vs SX127x ("Generic ESP32"), Heltec v3 vs WSL3 vs Wireless Tracker ("Heltec V3"), Wio L1 Pro vs EINK, T114 with or without display. It does separate the Ikoka power variants (the string names 22, 30 or 33 dBm) and Heltec v4 OLED vs TFT, which USB alone cannot.
3. **Serial number as a key across resets.** nRF52: app serial = bootloader serial. S3 TinyUSB → ROM: 12 hex digits vs the same MAC with colons; normalise both (source inference). HWCDC: identical. Bridges: CP210x "0001" and CH340 (none) are not unique; use the USB path (`/dev/serial/by-path`, or the sysfs path such as `1-1`).

**Radio variants cannot be told apart over USB. The user must confirm.** Flashing the wrong radio variant gives a node that does not work.

### 6.6 Reading the identity from sysfs (no root, no tty open)

- `/sys/class/tty/ttyACMn/device` is the USB **interface** (cdc-acm registers the tty under the control interface, `linux v6.18 drivers/usb/class/cdc-acm.c:1529-1530`). `device/..` is the USB device. `device/interface` holds the interface string ("TinyUSB Serial", "TinyUSB CDC", "nRF Serial"; empty for USB-Serial-JTAG, whose iInterface is 0) (`drivers/usb/core/sysfs.c:1143-1155`).
- `/sys/class/tty/ttyUSBn/device` is the usb-serial **port**. `device/..` is the interface and `device/../..` the USB device (`usb-serial.c:1075`, `bus.c:53-54`).
- **Rule:** `realpath(/sys/class/tty/<tty>/device)`, then walk up to the first directory that has an `idVendor` file.
- In that directory: `idVendor`, `idProduct`, `bcdDevice` are 4 lower-case hex digits plus newline (`sysfs.c:690-705`). `manufacturer`, `product`, `serial` are strings plus newline, and each file is **absent** when the device has no such string (`sysfs.c:124-143,829-845`). All are world-readable.
- The bound driver is `basename(readlink(<interface>/driver))`: `cdc_acm`, `cp210x` or `ch341`.
- `/dev/serial/by-id/usb-<manufacturer or VID>_<product>_<serial>-if<NN>`, plus `-port<N>` for ttyUSB only; spaces and `/` become `_` (`systemd v257 rules.d/60-serial.rules:11-26`). `/dev/serial/by-path/` is stable per physical port.
- Helper: `scratchpad/src/scripts/usbids/list_usb_ttys.py [SYSFS_ROOT]`. It reads sysfs only and never opens a tty. It was tested against a fake tree (`scripts/usbids/fakesys/`), not real hardware.

### 6.7 Permissions: the `dialout` group

- udev gives `ttyACM*` and `ttyUSB*` to group `dialout`, mode 0660: `crw-rw---- root dialout` (`systemd v257 rules.d/50-udev-default.rules.in:42`; `src/udev/udev-node.c:599-600`).
- The owner's user `void` (uid 1000) is in `video`, `input`, `sudo`, **not `dialout`** (kit `code/docs/device.md:28`). Without it, `open()` fails with `EACCES`.

**The command for the owner:**

```
sudo usermod -aG dialout void
sudo reboot
```

Why a reboot: the launcher `APPLaunch` is a user unit run by `user@1000.service` (`User=%i`, `PAMName=systemd-user`; `systemd v257 units/user@.service.in:17-21`; kit `device.md:74-75`). Supplementary groups are read when that service starts (`man/systemd.exec.xml:660-663`), and a user manager cannot change them later (`:630-638`).

| Action after `usermod` | Enough? |
|---|---|
| `systemctl --user restart APPLaunch` | **No** (inference from the systemd docs; **UNVERIFIED** on the device) |
| log out | only if `user@1000.service` really stops: no lingering, and `UserStopDelaySec` (default 10 s) has passed (`man/logind.conf.xml:189-196`, `man/loginctl.xml:186-195`). Check `loginctl show-user void -p Linger` and `loginctl list-sessions` |
| `sudo systemctl restart user@1000.service` | yes. It kills all of void's processes; whether APPLaunch returns by itself is **UNVERIFIED** |
| reboot | **yes, and simplest** |

Check: `grep Groups /proc/$(pgrep -f M5CardputerZero-APPLaunch | head -1)/status` must list the dialout GID. In the app: `os.Getgroups()` and a test `open()`; on `EACCES` show "add void to dialout, then reboot". Look the GID up with `os/user.LookupGroup("dialout")`, which works with `CGO_ENABLED=0` (Debian's GID is normally 20; **UNVERIFIED** here).

**Alternative without a group change:** a udev rule, for example `/etc/udev/rules.d/70-meshcore-flasher.rules`:

```
SUBSYSTEM=="tty", KERNEL=="ttyACM[0-9]*|ttyUSB[0-9]*", OWNER="void"
```

It applies at the next plug-in or after `sudo udevadm trigger`, with no restart. Syntax from `man/udev.xml`; **UNVERIFIED** on the device.

**Temporary alternative (MeshCore FAQ 6.8, `MeshCore docs/faq.md:719-726`):** `sudo setfacl -m u:$USER:rw /dev/ttyUSB0`, given for the web flasher's "Failed to open serial port" error on Linux. The ACL is lost when the board is unplugged, when it re-enumerates (an nRF52 touch or an ESP32 TinyUSB reset), and whenever the tty name changes. Use it only for a one-off test, not as the app's fix.

### 6.8 Things that can steal the port, and kernel drivers

- **ModemManager** probes new ttyACM devices. **brltty** claims `10c4:ea60` devices with manufacturer "Silicon Labs" (`/lib/udev/rules.d/85-brltty.rules`, quoted at koen.vervloesem.eu, 2023). Whether either is installed is **UNVERIFIED**; check `systemctl is-active ModemManager` and `dpkg -l brltty`. A probe can disturb the ROM SYNC, the 1200 touch or the CLI. The usual fix is `ENV{ID_MM_DEVICE_IGNORE}="1"` in a udev rule.
- Raspberry Pi defconfigs build `CONFIG_USB_SERIAL_CP210X=m` and `CONFIG_USB_SERIAL_CH341=m` (`raspberrypi/linux bcm2711_defconfig` rpi-6.18.y `:1328-1337`). The owner runs the Debian-packaged `6.18.39+rpt-rpi-v8` kernel, so these defconfig lines do not necessarily describe its config. `CONFIG_USB_ACM` is not in any of the four defconfigs checked, yet Pi kernels do bind `cdc_acm` (raspberrypi/linux#7151). How it is enabled is **UNVERIFIED**. That issue also reports a 6.12.47 regression where no `/dev/ttyACM` node appeared.
- The owner runs `6.18.39+rpt-rpi-v8` (kit `device.md:25`). Phase-3 check: `modinfo cdc_acm cp210x ch341`, or `grep -E 'CONFIG_USB_ACM|CONFIG_USB_SERIAL_CP210X|CONFIG_USB_SERIAL_CH341' /boot/config-$(uname -r)`. **Do not use `ls /sys/bus/usb/drivers/` with no board attached:** a modular driver's directory appears only once the module is loaded, because the module's init calls `usb_register` (`linux v6.18 drivers/usb/serial/cp210x.c:2198`, `ch341.c:890`: `module_usb_serial_driver` → `usb-serial.c:1505` `usb_register`; `drivers/usb/class/cdc-acm.c:2103,2123`), and these modules autoload only when a matching device is plugged in. That `ls` is valid only while the board is plugged in.

### 6.9 Suggested algorithm (an extension)

1. List `/sys/class/tty/ttyACM*` and `ttyUSB*`; read their identity (6.6).
2. Classify each port: `esp-hwcdc`, `esp-tinyusb`, `esp-bridge`, `nrf-app`, `nrf-bootloader`, `unknown`.
3. Look up candidates by VID:PID + product, falling back to VID:PID only. Keep the catalog's order.
4. Pre-select only when exactly one candidate remains (11.D1). Always offer "Not sure / show all". Always ask the user to confirm.
5. ESP32: after chip detect, stop if the chip differs from the device's MCU (11.D3).
6. Optional: send `board` to a running repeater or room server (11.D2).
7. Keep the table as data. Regenerate it with `scratchpad/src/scripts/usbids/map_devices.py` when the live catalog changes.

---

## 7. Setup over the serial console

Line references are `MeshCore@75b04ef0` unless noted. The CLI files are byte-identical to tag `repeater-v1.18.0` (81dbae59).

### 7.1 Which roles have a CLI

| Role | What USB serial speaks | Setup by text CLI? |
|---|---|---|
| repeater | text CLI | **yes** |
| roomServer | text CLI | **yes** |
| companionUsb | binary frames: to the device `'<'` + LE16 length + payload, from the device `'>'` + LE16 length + payload (`src/helpers/ArduinoSerialInterface.cpp:24-73`) | no |
| companionBle | nothing usable on USB, except a button-triggered "CLI Rescue" mode (FAQ 6.7, `faq.md:717`; replies prefixed `"  "`) | no |
| kissRadio | binary KISS at 115200 8N1 (`docs/kiss_modem_protocol.md:5-7`) | no |
| Ripple gui/guiSD, ZephCore | not covered | no |

The catalog agrees: repeater and room server are "Configured via Config/Console" (`flasher config.json:30-39`).

A companion USB build can still report its version (**UNVERIFIED** on hardware): send `3C 02 00 16 03` (`CMD_DEVICE_QUERY` 0x16, app protocol 3). The reply code 0x0D carries `FIRMWARE_VER_CODE` at byte 1, the build date at bytes 8-19, `getManufacturerName()` at 20-59, `FIRMWARE_VERSION` at 60-79 and `path_hash_mode` at 81 (`examples/companion_radio/MyMesh.cpp:30,88,1056-1075`; `docs/companion_protocol.md:183-198,694-709`).

### 7.2 Line format

- **115200 8N1.** `Serial.begin(115200)` (`examples/simple_repeater/main.cpp:39`). On native USB the baud rate is ignored, but use 115200 anyway (nRF52 at 1200 enters DFU).
- **Send the command, then CR.** LF is ignored. A line ending only in LF is never processed.
- **The firmware echoes input.** The reply line is `"  -> " + reply + "\r\n"` (`main.cpp:149-166`). Commands with an empty reply print no `"  -> "` line: `reboot`, `poweroff`/`shutdown`, `clkreboot`, and others. `get acl` and `log` print raw lines.
- **No login on serial.** Every command runs with full admin rights, because `sender_timestamp == 0` ("there is no sender_timestamp via serial!", `main.cpp:159`; gating at `CommonCLI.cpp:280,475-480,607,711`). The admin password protects only remote LoRa logins.
- **One command at a time.** Wait for the `"  -> "` line or a timeout (the config tool uses 5000 ms and a 100 ms gap, `config.meshcore.io lib/serial-cli.js:27-28`).

| | Repeater (`examples/simple_repeater/main.cpp`) | Room server (`examples/simple_room_server/main.cpp`) |
|---|---|---|
| line buffer | `char command[160]` (`:25`): at most 158 command characters | `MAX_POST_TEXT_LEN+1` = 152 bytes (`MyMesh.h:84`): at most 150 |
| echo | every byte except LF | **every byte, LF included** (`:115-122`) |
| stops reading at CR | yes (`:135-143`) | **no**: `a\rb\r` written at once becomes one command `a\rb` |
| after a completed line | prints `\n`, then the reply line | reply line only |
| buffer full | v1.18.0: truncated to 158 chars (`:144-147`); before v1.18.0 undefined behaviour | |
| boot banner | `Repeater ID: <64 hex>\r\n` (`:97-98`) | `Room ID: <64 hex>\r\n` (`:78-79`) |

Echo example, room server: send `ver\r` → `ver\r  -> v1.18.0-…\r\n`; send `ver\r\n` → `ver\r\n  -> …\r\n`.

**Parsing (both roles):** leading spaces skipped; **trailing spaces kept** (they become part of names and passwords) (`simple_repeater/MyMesh.cpp:1233`). An optional 3-character tag `xx|` is reflected: `01|ver` → `  -> 01|v1.18.0-…` (`MyMesh.cpp:1235-1239`). Commands match by prefix `memcmp`, so an unknown newer key can be answered by an older one (e.g. `get radio.rxgain` answered as `get radio` on old firmware). v1.18.0 matches the radio `get` keys exactly instead (7.5). **Gate commands on the version.** Never send user text that starts like `ab|…` as a whole command.

**nRF52 (TinyUSB) drops all output while DTR is low** (`Adafruit_TinyUSB_Arduino src/arduino/Adafruit_USBD_CDC.cpp:240-258`; `src/class/cdc/cdc_device.c:132-135`). Open the CLI port at 115200 with DTR asserted (Linux asserts it on open).

**Linux:** use raw mode (cfmakeraw: no ICRNL, ONLCR, OCRNL, no echo). CR/LF translation breaks the protocol.

### 7.3 Setup commands (v1.18.0)

Each reply is printed as `"  -> " + reply + "\r\n"`.

| Purpose | Command | Arguments and firmware check | Reply | Takes effect | Read back | Read-back reply |
|---|---|---|---|---|---|---|
| Name | `set name <n>` | stored in 32 bytes, so ≤ 31 bytes, longer silently cut. v1.12.0 refuses `[ ] / \ : , ? *` (`git show repeater-v1.12.0:src/helpers/CommonCLI.cpp:17-23`); v1.13.0+ refuses `[ ] \ : , ? *` (`CommonCLI.cpp:22-28`). **An empty name (`set name ` with a trailing space) is accepted on every version** (`:530-534`) | `OK` / `Error, bad chars` | next advert | `get name` | `> <n>` |
| Radio | `set radio <MHz>,<kHz>,<sf>,<cr>` | freq 150.0–2500.0 (300.0 before v1.15.0); bw 7.0–500.0; sf 5–12; cr 5–8 (4/5…4/8); a missing part counts as 0 (`CommonRadioPrefs.cpp:39-58`, ranges at `:48`) | `OK - reboot to apply` / `Error, invalid radio params` | **after reboot** | `get radio` | `> 869.6179809,62.5,8,5` |
| Frequency only (serial only) | `set freq <MHz>` | **no range check** (`CommonCLI.cpp:607-610`) | `OK - reboot to apply` | after reboot | `get freq` | `> 869.6179809` |
| TX power | `set tx <dBm>` | `atoi`, **no range check** (`CommonRadioPrefs.cpp:137-146`) | always `OK` | immediately | `get tx` | `> 22`; a negative value prints by version (7.4) |
| Path hash mode (v1.14.0+) | `set path.hash.mode <n>` | 0 = 1-byte, 1 = 2-byte, 2 = 3-byte hash in this node's adverts; ≥ 3 refused; non-numeric parses as 0; default 0 (`CommonRadioPrefs.cpp:175-189`) | `OK` / `Error, must be 0,1, or 2` | next advert | `get path.hash.mode` | `> 0` |
| Admin password | `password <pw>` | ≤ 15 bytes, silently cut; **empty accepted** (`CommonCLI.cpp:267-272`) | `password now: <pw as stored>` | immediately | none | – |
| Guest password | `set guest.password <pw>` | ≤ 15 bytes (`:513-516`). Default depends on the build env (7.6): **always set it and read it back** | `OK` | immediately | `get guest.password` | `> <pw>` (`> ` when blank) |
| Latitude / longitude | `set lat <deg>`, `set lon <deg>` | `atof`, no range check (`:542-549`) | `OK` | next advert | `get lat`, `get lon` | `> 51.5074005`, `> 0.0` |
| Flood advert now | `advert` | | `OK - Advert sent` (sent after 1500 ms) | | | |
| Zero-hop advert now (v1.14.0+) | `advert.zerohop` | | `OK - zerohop advert sent` | | | |
| Periodic zero-hop adverts | `set advert.interval <0 or 60-240>` (minutes) | `0` is accepted and disables periodic zero-hop adverts; stored as minutes/2, so odd minutes round down (`:503-512`) | `OK` / `Error: interval range is 60-240 minutes` | | | |
| Reboot | `reboot` | | **no reply** (`:196-197`) | | | |
| Set clock | `time <epoch>` | digits only; must be later than the current clock (`:228-238`) | `OK - clock set: HH:MM - D/M/YYYY UTC` / `(ERR: clock cannot go backwards)` | immediately | `clock` | `HH:MM - D/M/YYYY UTC` (**no** `> `) |
| Clock sync | `clock sync` | uses the sender timestamp, 0 on serial | always `ERR: clock cannot go backwards` on serial. **Do not use.** | | | |

**Identification:**

| Command | Reply | Available | Source |
|---|---|---|---|
| `ver` | `v1.18.0-81dbae5 (Build: 15-Oct-2026)` | all versions | `CommonCLI.cpp:283-284` |
| `board` | plain name, **no** `> `, e.g. `Heltec V3`, `RAK 4631`, `Generic ESP32` | repeater v1.9.1+, room server v1.10.0+ | `:285-286` |
| `get role` | `> repeater` / `> room_server` | v1.4.2+ | `:753-754` |
| `get public.key` | `> ` + 64 upper-case hex | all | `:750-752` |
| `get bootloader.ver` | `> 0.9.2-OTAFIX2.2` or `> unknown`; `Error: unsupported` off nRF52 | nRF52 builds | `:783-793` |

Parse `ver` with `v?(\d+)\.(\d+)\.(\d+)`, as the config tool does (`config.meshcore.io src/gui.js:24-29`). The `vX.Y.Z-<hash>` part equals the substring in the flashed file name. The hash can exceed 7 characters if 7 would be ambiguous. The build date is the CI run date (**UNVERIFIED** per release).

**Reboot rules.** Only `set radio`, `set freq` and `set prv.key` need a reboot (`docs/cli_commands.md:216,261,368`). Every setting is saved to flash **before** the reply is printed (`CommonCLI.cpp:162-168,184-187`), so `reboot` right after `OK` loses nothing.

**Side effect of the first save.** A factory-fresh repeater or room server sends a zero-hop advert every 2 minutes (`advert_interval = 1`, `simple_repeater/MyMesh.cpp:904`). The first time any setting is saved, an interval below 60 minutes is forced to 0, which disables it (`CommonCLI.cpp:160-168`; `docs/cli_commands.md:669-674`). To keep periodic zero-hop adverts, send `set advert.interval 60..240` (11.E4). The flood advert interval stays at 47 h (`MyMesh.cpp:905`).

**Factory reset:** `erase` (serial only) replies `File system erase: OK` or `File system erase: Err` (`CommonCLI.cpp:280-282`).

### 7.4 Number formats in read-backs

Frequency prints through float32 with 7 decimals (`ftoa`, `src/helpers/TxtDataHelpers.cpp:57-146`); bandwidth with 3 decimals, trailing zeros removed (`ftoa3`, `:148-159`). **Compare numerically** (|Δf| < 0.0005, |Δbw| < 0.01), never as text.

| Frequency | Printed | | Bandwidth | Printed (v1.11.0+) |
|---|---|---|---|---|
| 869.618 | `869.6179809` | | 62.5 | `62.5` |
| 910.525 | `910.5250244` | | 250 | `250` (before v1.11.0: `250.0`) |
| 915.8 | `915.7999877` | | 7.8 | `7.8` |
| 433.65 | `433.6499938` | | | |
| 917.375 | `917.375` | | | |
| 921 | `921.0` | | | |

`get tx` for a negative stored value (e.g. −5) depends on the version:

| Version | Prints | Source |
|---|---|---|
| v1.11.0, v1.12.0 | `> 251` (field is `uint8_t`) | `git show repeater-v1.12.0:src/helpers/CommonCLI.h:22`, `CommonCLI.cpp:329` |
| v1.13.0 … v1.17.1 | `> -5` (field `int8_t`, printed as `(int32_t)`) | `git show repeater-v1.17.1:src/helpers/CommonCLI.cpp:898-899`; v1.13.0 `CommonCLI.h:22` |
| v1.18.0 | `> 251` (`uint8_t getTxPower()`) | `CommonRadioPrefs.cpp:137-139`, `CommonRadioPrefs.h:38`, `CommonCLI.h:33` |

Read-back of a negative TX power therefore needs a version branch: compare `(value + 256) % 256`.

### 7.5 Changes by firmware version

The live releases list offers v1.11.0 … v1.18.0. Since v1.10.0 the three role tags of a version point to the same commit: v1.10.0 9405e8be, v1.11.0 6d321932, v1.12.0 e738a747, v1.13.0 295f67d4, v1.14.0 9f1a3eaf, v1.14.1 467959cc, v1.15.0 dee3e26a, v1.16.0 07a3ca9e, v1.17.0 727fc051, v1.17.1 d9296435, v1.18.0 81dbae59 (`git tag` in MeshCore; 107 tags; full list in `scratchpad/research/cli.md` §(d)).

| Item | Changed in | Before |
|---|---|---|
| `set radio` SF minimum 5 | v1.11.0 | 7 |
| `get radio` bandwidth via `ftoa3` (`250`) | v1.11.0 | `ftoa` (`250.0`) |
| `set name` refuses `[ ] / \ : , ? *` | v1.12.0 (`/` allowed again from v1.13.0) | no check |
| `owner.info` | v1.12.0 | – |
| `get/set path.hash.mode` | v1.14.0 (a66773ba) | `??: path.hash.mode` / `unknown config: path.hash.mode N` |
| `advert.zerohop` | v1.14.0 | treated as a flood `advert` |
| `radio.rxgain` | v1.14.1 | – |
| `set radio` frequency minimum 150 MHz | v1.15.0 | 300 |
| prefs file `/prefs.json` | v1.17.0 (a0b52be4); `/com_prefs` read once and left in place | `/com_prefs` |
| radio handlers moved to `CommonRadioPrefs.cpp`; `get radio`, `freq`, `af`, `dutycycle`, `int.thresh`, `cad`, `radio.rxgain`, `rxdelay`, `agc.reset.interval`, `path.hash.mode`, `multi.acks` matched by exact `strcmp` (`MeshCore@81dbae59 src/helpers/CommonRadioPrefs.cpp:32,60,65,81,102,112,122,148,163,175,191`); `get tx` prints a negative value as 256+n (7.4) | v1.18.0 | prefix `memcmp` (e.g. `memcmp(config, "radio", 5)`, `git show repeater-v1.17.1:src/helpers/CommonCLI.cpp:859`). On v1.18.0 `get radio ` with a trailing space, or a suffixed key, gets `??: …` |
| repeater buffer-full fix | v1.18.0 | undefined behaviour on full buffer |
| `board` | repeater v1.9.1, room server v1.10.0 | `Unknown command` |
| repeater stops at CR and prints `\n` | v1.10.0 | echoed LF, kept reading |
| repeater line buffer 160 B | v1.8.0 | 80 |
| `get role` | v1.4.2 | – |
| `time` reply `OK - clock set: …` | v1.7.1 | `(OK - clock set!)` |
| `Unknown command` | v1.6.2 | `Unknown: <cmd>` |

**Setup branches for the versions the live list offers (v1.11.0 … v1.18.0):**

| Behaviour | v1.11.0 | v1.12.0 | v1.13.0 | v1.14.0, v1.14.1 | v1.15.0 … v1.17.1 | v1.18.0 |
|---|---|---|---|---|---|---|
| `set/get path.hash.mode` | no (`unknown config: …` / `??: …`) | no | no | **yes** | yes | yes |
| `set radio` frequency floor | 300 MHz | 300 | 300 | 300 | **150** | 150 |
| `set name` refuses | nothing | `[ ] / \ : , ? *` | `[ ] \ : , ? *` | same | same | same |
| empty name (`set name `) | accepted | accepted | accepted | accepted | accepted | accepted |
| `get tx` for −5 | `> 251` | `> 251` | `> -5` | `> -5` | `> -5` | `> 251` |
| radio `get` key match | prefix | prefix | prefix | prefix | prefix | **exact** |
| `radio.rxgain` | no | no | no | v1.14.1 only | yes | yes |
| prefs file | `/com_prefs` | same | same | same | `/prefs.json` from v1.17.0 | `/prefs.json` |

Sources: the rows above in this section and 7.3-7.4. The app should refuse every character of the v1.12.0 set on all versions, refuse an empty name, and compare `get tx` modulo 256.

**Downgrade warning:** below v1.17.0 the firmware reads the old `/com_prefs`, so a downgrade silently loads stale or default settings. Always re-apply and read back after flashing.

### 7.6 Limits, defaults and error replies

**Name.** At most 31 bytes stored. Adverts carry the name in `MAX_ADVERT_DATA_SIZE = 32` (`src/MeshCore.h:12`; `AdvertDataHelpers.cpp:4-29`), and repeaters and room servers include lat/lon by default (`ADVERT_LOC_PREFS`, `simple_repeater/MyMesh.cpp:925`; `CommonCLI.cpp:170-181`), even 0,0. **In practice the limit is 23 bytes** (`docs/cli_commands.md:324`). The config tool shows 24/32, off by one (`gui.js:124-128`). Refuse `[ ] / \ : , ? *` on every version (v1.12.0 refuses `/` and answers `Error, bad chars`), refuse an empty name (accepted by every version, `CommonCLI.cpp:530-534`), and refuse leading or trailing spaces.

**Passwords.** Admin default `password` (`ADMIN_PASSWORD`, `simple_repeater/MyMesh.cpp:32-34`; FAQ 3.3). Room-server guest default: `hello` **only where the build env defines `ROOM_PASSWORD`**, because the code sets it under `#ifdef ROOM_PASSWORD` (`simple_room_server/MyMesh.cpp:668-670`), e.g. `Heltec_v3_room_server` (`variants/heltec_v3/platformio.ini:115`). It is **blank** for the room servers of Xiao_nrf52 (`variants/xiao_nrf52/platformio.ini:100-111`), LilyGo T-Echo, T-Echo Card, T-Echo Lite, Ikoka Stick, Nano and Handheld, SenseCap Solar and T-Beam SX1276 (`grep -c ROOM_PASSWORD` = 0 in those `platformio.ini` files). FAQ 3.4 says `hello`. The MeshCore App API lists `remote_management.repeaters.guest_login_passwords: [""]` (https://api.meshcore.nz/api/v1/config, copy dated 2026-11-19), so apps try a blank guest login by default. **The app must always set the guest password and read it back.** Repeater guest default blank, which lets a blank-password login get guest access. Both at most 15 bytes. **An empty admin password is accepted**, and a blank login is checked against it (`simple_repeater/MyMesh.cpp:92-105`), so anyone gets admin over LoRa. **The app must refuse empty, over-long, and leading/trailing-space passwords.**

**Error replies:**

| Case | Reply |
|---|---|
| bad radio parameters | `Error, invalid radio params` |
| `set path.hash.mode` ≥ 3 | `Error, must be 0,1, or 2` |
| bad name characters | `Error, bad chars` |
| `set advert.interval` out of range | `Error: interval range is 60-240 minutes` |
| `set flood.advert.interval` out of range | `Error: interval range is 3-168 hours` |
| `time` in the past | `(ERR: clock cannot go backwards)` |
| unknown `set` key | `unknown config: <key and value>` |
| unknown `get` key | `??: <key>` |
| unknown command | `Unknown command` |
| `set tx`, `set lat`, `set lon`, `set freq`, over-long name or password | **always succeed**: values are cut or not checked at all |

**Bandwidth.** The CLI accepts any value from 7 to 500, but at boot `setParams()` ignores RadioLib errors (SX1262: `src/helpers/radiolib/CustomSX1262Wrapper.h:15-24`; LLCC68 likewise, `CustomLLCC68Wrapper.h:11-21`). RadioLib maps the value with `uint8_t bw_div2 = bw / 2 + 0.01f` and accepts only the results for 7.8, 10.4, 15.6, 20.8, 31.25, 41.7, 62.5, 125, 250, 500 kHz (`RadioLib src/modules/SX126x/SX126x_config.cpp:44-87`). So it accepts ranges (e.g. 62.0-63.9 → 62.5), and any other value is rejected at boot. **Offer only the exact values.**

**Clock.** Without an RTC the clock starts at 1715770351 (15 May 2024) on every boot (`src/helpers/ArduinoHelpers.h:11`). `time` never goes backwards; `clkreboot` resets it to 1715770351 and reboots (`CommonCLI.cpp:198-201`). 162 variant files use `AutoDiscoverRTCClock rtc_clock` (`grep -rl` over `variants/`), which writes the time into a DS3231, RV3028, PCF8563 or RX8130 RTC when one is found (`src/helpers/AutoDiscoverRTCClock.cpp:85-100`). **On a board with an RTC a wrong future time survives a reboot; only `clkreboot` clears it.** Without an RTC a reboot clears it too.

### 7.7 TX power

- No CLI check. RadioLib: SX1262 −9..22 dBm (`RadioLib SX1262.cpp:208-214`); SX1276 −4..15 (RFO) or 2..17/20 (PA_BOOST). A stored out-of-range value is rejected at boot, so the radio stays at `LORA_TX_POWER` while `get tx` still shows the stored value. Default e.g. 22 on Heltec V3 (`variants/heltec_v3/platformio.ini:20`), fallback 20.
- Docs say 1-22 (`docs/cli_commands.md:226`); the code has no check.
- **FAQ 7.7** (`faq.md:812-828`): boards with an external power amplifier need low chip settings. "Incorrect power settings can permanently damage your radio hardware."

| Board | Setting (dBm) | Result |
|---|---|---|
| Station G2 | 19 | US max 36.5 dBm |
| Station G2 | 16 / 15 | 1 dB compression point, US / EU |
| Station G2 | 10 / 9 | 1 W, US / EU |
| Ikoka Stick E22-900M30S | 19 | "DO NOT EXCEED" |
| Ikoka Stick E22-900M33S | 9 | "DO NOT EXCEED" |
| Heltec V4 | 10 | standard output, 22 dBm |
| Heltec V4 | 22 | high output, 28 dBm |

**The app must enforce TX limits itself** (11.E5).

### 7.8 Radio presets

`MeshCore docs/radio_presets.md:1-65` points to https://api.meshcore.nz/api/v1/config; clients must expect missing keys and send a proper User-Agent. That file holds only 3 example entries and says the presets are "stored on the MeshCore App API server" (`:3`), so **MeshCore's MIT licence does not cover the live list below. Its licence and terms of use are UNVERIFIED** (1.3, 11.E2). The config tool maps a preset to `set radio` and sets `path.hash.mode = path_hash_size − 1` (`gui.js:280-302`). Do this only on v1.14.0+.

Live list (fetched 2026-11-24, copy dated 2026-11-19; values are strings in the JSON; CR 5 = 4/5):

| Title | MHz | BW kHz | SF | CR | path_hash_size |
|---|---|---|---|---|---|
| Australia | 915.800 | 250 | 10 | 5 | 2 |
| Australia (Narrow) | 916.575 | 62.5 | 7 | 7 | 2 |
| Australia (Mid) | 915.075 | 125 | 9 | 5 | 2 |
| Australia: SA, WA | 923.125 | 62.5 | 8 | 8 | 2 |
| Australia: QLD | 923.125 | 62.5 | 8 | 5 | 2 |
| Brazil | 923.125 | 62.5 | 8 | 8 | – |
| Canada | 910.525 | 62.5 | 7 | 5 | 3 |
| Costa Rica | 910.525 | 125 | 11 | 5 | – |
| EU/UK (Narrow) | 869.618 | 62.5 | 8 | 8 | – |
| EU/UK (Deprecated) | 869.525 | 250 | 11 | 5 | – |
| Czech Republic (Narrow) | 869.432 | 62.5 | 7 | 5 | 2 |
| EU 433MHz (Long Range) | 433.650 | 250 | 11 | 5 | – |
| EU 433MHz (Narrow) | 433.650 | 62.5 | 8 | 8 | – |
| Hungary | 869.618 | 62.5 | 7 | 5 | 2 |
| Netherlands | 869.618 | 62.5 | 7 | 5 | – |
| Netherlands (Limburg) | 869.618 | 62.5 | 8 | 8 | 2 |
| New Zealand (Narrow) | 917.375 | 62.5 | 7 | 5 | 2 |
| New Zealand (Gisborne) | 917.375 | 250 | 11 | 5 | 1 |
| Portugal 433 | 433.375 | 62.5 | 9 | 6 | – |
| Portugal 868 | 869.618 | 62.5 | 7 | 6 | – |
| Slovakia | 869.618 | 62.5 | 7 | 5 | 2 |
| Switzerland | 869.618 | 62.5 | 8 | 8 | – |
| Thailand | 921 | 62.5 | 7 | 5 | – |
| USA | 910.525 | 62.5 | 7 | 5 | – |
| USA - Southern California | 927.875 | 62.5 | 7 | 5 | 3 |
| Vietnam (Narrow) | 920.250 | 62.5 | 8 | 5 | – |
| Vietnam (Deprecated) | 920.250 | 250 | 11 | 5 | – |

Firmware default with no prefs file: `869.618,62.5,8,5` (`platformio.ini:29-31`; `simple_repeater/MyMesh.cpp:15-16`; `docs/cli_commands.md:214`).

### 7.9 What the reference tools send

- **Web flasher console** (`flasher lib/console.js:37,67-76`): opens at 115200, writes `command + "\r\n"`, splits input on `\r\n`. Its autocomplete list is `js/commands.js:2-160`. It has no setup automation.
- **config.meshcore.io** (aff42ad8), the "Repeater Setup" tool the flasher opens. `lib/serial-cli.js`: opens at 115200 (`:50-58`); writes `command + "\r"` (`:416`); one command in flight, 5000 ms timeout, 100 ms gap (`:27-28,358-361,398-413`); the reply is the text between the first `"  -> "` after the echo and the next `\r\n`, trimmed (`:228-303`); "collect" mode ends after 400 ms of silence (`:313-339`).
  - **Connect** (`src/gui.js:444-517,1047-1053`): `time <Date.now()/1000|0>`; `ver`, `clock`, `get role`, `get public.key`, `get prv.key`; then `get <key>` for: name, repeat, allow.read.only, radio, tx, af, rxdelay, txdelay, direct.txdelay, flood.max, flood.max.unscoped, flood.max.advert, flood.advert.interval, advert.interval, guest.password, lat, lon, int.thresh, agc.reset.interval, multi.acks, owner.info, path.hash.mode, loop.detect, cad, radio.rxgain, adc.multiplier. Only 5 keys are version-gated and skipped on older firmware: `owner.info` (1.12.0), `path.hash.mode` (1.14.0), `loop.detect` (1.14.0), `flood.max.unscoped` (1.16.0), `flood.max.advert` (1.16.0) (`varMinVersion`, `gui.js:12-21`; `supportsVar`, `:92-96`). All other keys (`radio.rxgain`, `cad`, `agc.reset.interval`, `int.thresh`, `multi.acks`, …) are sent on every version; on v1.14.0, for example, `get radio.rxgain` is answered by the prefix `get radio` handler. Then `region`, `region home`, `region default`, `get acl`.
  - **Save** (`gui.js:523-611`): `set <key> <value>` for changed keys only, in the same order (booleans as `on`/`off`). Radio is sent as `${freq},${bw + '.0'},${sf},${cr}`, which gives **`62.5.0`** (harmless: `strtof` stops at the second dot). Then `set prv.key` if imported, `password <pw>` if entered, region and ACL commands (6 s wait if the ACL changed), a full re-read, and a confirm-then-`reboot` if radio or prv.key changed.

Both line endings work. CR alone gives the cleanest echo.

### 7.10 Suggested setup sequence (derived from the facts above)

1. **Open** the tty raw at 115200 8N1 with DTR and RTS asserted. Discard input for about 1.5 s (ESP32 ROM banner, `Repeater ID: …`). Opening may reset ESP32 boards with an auto-reset circuit (**UNVERIFIED** per board). Then send `ver\r` every 1 s until a `"  -> v"` line arrives, for up to about 10 s. **First boot after a full ESP32 erase is slower:** `SPIFFS.begin(true)` formats the empty partition and a new key pair is generated before the `Repeater ID` banner (`simple_repeater/main.cpp:76,87-95`). How long that takes is **UNVERIFIED**; use a separate, longer first-boot timeout (for example 60 s) after a "Erase device" flash.
2. **Identify:** `ver` (compare with the flashed file name), `get role` (expect `> repeater` or `> room_server`), `board` (display only).
3. **Clock:** `time <unix>`, only if the Cardputer's clock is trusted (11.E1). Treat `(ERR: clock cannot go backwards)` as non-fatal.
4. **Settings,** one at a time, each waiting for its `"  -> "` line (5 s):
   - `set name <n>` → `OK`
   - `set radio <f>,<bw>,<sf>,<cr>` → `OK - reboot to apply`
   - `set tx <dBm>` → `OK` (value checked by the app first)
   - v1.14.0+: `set path.hash.mode <m>` → `OK`
   - optional: `set lat <x>`, `set lon <y>` → `OK`
   - `set guest.password <g>` → `OK`. **Always send it** for room servers: the default is `hello` or blank depending on the build env (7.6)
   - `password <p>` → `password now: <p>` (compare the echo)
   - optional: `set advert.interval <60-240>` → `OK`
5. **Read back** and compare: `get name`, `get radio` (numerically), `get tx` (modulo 256, 7.4), `get path.hash.mode` (v1.14.0+), `get guest.password`, `get lat`, `get lon`.
6. **Reboot and confirm:** `reboot\r` (no reply), close, wait for the port to come back (nRF52 and native-USB S3 re-enumerate; the name may change, **UNVERIFIED**), reopen, `ver`, `get radio`. After boot the firmware sends a zero-hop advert by itself after 16 s (`main.cpp:117-120`, `ENABLE_ADVERT_ON_BOOT=1` in `platformio.ini:32`).

**Command length cap.** The repeater accepts lines up to 158 characters (`char command[160]`, `simple_repeater/main.cpp:25,144-147`) and the room server up to 150 (`MAX_POST_TEXT_LEN` = 160-9, `simple_room_server/MyMesh.h:84`). But `set radio` copies its argument with `strcpy` into `char tmp[132]` (`CommonRadioPrefs.cpp:40-41`), so a `set radio` argument over 131 characters (a line over 141) overflows the firmware's stack. **Hard-cap every command at 128 bytes** and build `set radio` only from validated numbers.

**Region `load` mode.** After `region load` (`CommonCLI.cpp:911-912` → `startRegionsLoad`, `simple_repeater/MyMesh.cpp:1147-1152`), every following line is swallowed with no reply until a blank line arrives (`:1199-1231`; room server `simple_room_server/MyMesh.cpp:899-932`). To the app this looks like a timeout on every command. Setup never sends `region load`. If a command times out with no `"  -> "` line, send one bare `\r`: it ends load mode with `OK - loaded N regions`, then resend the command. Ending load mode is a change: `region load` starts an empty table (`RegionMap::resetFrom` sets `num_regions = 0`, `src/helpers/RegionMap.h:52`), and the blank line makes it the table the node routes with (`region_map = temp_map`, `simple_repeater/MyMesh.cpp:1199-1205`; room server `:899-905`) until it restarts and loads the saved one, or someone runs `region save`. So only the setup run sends the escape, and its `reboot` restores the saved table; the read-only probe never sends it. (Corrected 24 Nov 2026: this said the map was unchanged.)

---

## 8. FAQ 5.13 in short

**Where:** `MeshCore docs/faq.md:584-646` (TOC entry `:64`), "5.13. Q: Can I use a Raspberry Pi to update a MeshCore radio?". The entry and its line `picocom -b 115200 /dev/ttyUSB0 --imap lfcrlf` were added in 5e553b1f (2025-03-31), removed by 2818749a ("revert file", 2025-05-01), restored by 4196fd4a (2025-05-16) and re-indented by cc05ae555 (2026-06-02, whitespace only) (`git log -S"picocom -b 115200 /dev/ttyUSB0 --imap lfcrlf" -- docs/faq.md`; `git show 2818749a:docs/faq.md` has no `picocom`; `git blame -L642,642 docs/faq.md`). The live page https://docs.meshcore.io/faq/ (fetched 2026-11-24, copy dated 2026-11-16) has the same number, in "5. General" between 5.12 and 5.14, and the same text word for word. The site is built from `MeshCore/docs` with mkdocs (`.github/workflows/github-pages.yml:27-28`).

**What it says:**

| Step | ESP32 (e.g. Heltec V3) | nRF52 (e.g. RAK, T114) |
|---|---|---|
| 1. Download | from https://flasher.meshcore.io, e.g. `wget https://flasher.meshcore.io/releases/download/companion-v1.7.1/Heltec_v3_companion_radio_ble-v1.7.1-165fb33.bin`. If wget gets only ~10K bytes: `wget --user-agent="Mozilla/5.0" --content-disposition "<url>"` | the ZIP, e.g. `RAK_4631_companion_radio_ble-v1.7.1-165fb33.zip` |
| 2. Find the port | usually `/dev/ttyUSB0` | usually `/dev/ttyACM0` |
| 3. Install | `pip install esptool --break-system-packages` | `pip install adafruit-nrfutil --break-system-packages` |
| 4. Flash | non-merged: `esptool.py -p /dev/ttyUSB0 --chip esp32-s3 write_flash 0x10000 <non-merged_firmware>.bin`; merged: `… write_flash 0x00000 <merged_firmware>.bin` | `adafruit-nrfutil --verbose dfu serial --package RAK_4631_companion_radio_usb-v1.7.1-165fb33.zip -p /dev/ttyACM0 -b 115200 --singlebank --touch 1200` |
| Console | `sudo apt install picocom`; `picocom -b 115200 /dev/ttyUSB0 --imap lfcrlf`; then https://docs.meshcore.io/cli_commands | same |

The FAQ's notes on ESP32 files: "Non-merged bin keeps the existing Bluetooth pairing database." "Merged bin overwrites everything including the bootloader and existing Bluetooth pairing database, but keeps configurations."

**What it means for the app:**

- It confirms the addresses: app `.bin` at 0x10000, merged at 0x0 (section 4.2).
- It confirms the nRF52 path: serial DFU at 115200 with `--singlebank --touch 1200` (section 5).
- It confirms the CR line ending: picocom sends CR on Enter; `--imap lfcrlf` only changes how received LF is shown (section 7.2).
- It confirms the User-Agent problem on the flasher host (section 2.4).
- `--chip esp32-s3` and `/dev/ttyUSB0` are examples only. The console example says `/dev/ttyUSB0` even for nRF52 boards, which appear as `/dev/ttyACM0`.
- Our app replaces esptool, nrfutil and pip with Go code.

Related FAQ entries: 6.8 Linux "Failed to open serial port": `sudo setfacl -m u:$USER:rw /dev/ttyUSB0` (`:719-726`; temporary, section 6.7); 3.1 setup via config.meshcore.io or the console (`faq.md:225-241`); 3.2 `set lat`/`set lon` (`:244-251`); 3.3 admin password, default `password` (`:253-256`); 3.4 room-server guest password, default `hello` (`:259-262`; true only for build envs that define `ROOM_PASSWORD`, section 7.6); 3.5 `get prv.key` / `set prv.key`, reboot after (`:264-270`); 3.9.4 path.hash.mode (`:312-329`); 6.7 companion "CLI Rescue" mode (`:717`); 7.7 TX power on PA boards (`:812-828`).

---

## 9. Recording the reference

The goal: run the reference JavaScript in Node against fake serial ports, record every byte, and compare the Go app's bytes with it. Two working prototypes exist in the scratchpad. **Copy them into the repository before the scratchpad is cleared.** They were run with Node v22.23.5 (`/opt/node22/bin/node`).

### 9.1 ESP32: `scratchpad/src/proto-esp32/`

- `hooks.mjs`: a `module.register` resolve hook that maps the flasher's absolute imports (`/lib/…`, `/js/…`) onto a checkout. The real `js/flash.js` and `lib/esp32.js` run **unmodified**.
- `fake.mjs`:
  - `FakeSerialPort`: `open(opts)`; a `readable` getter that returns a **new unlocked stream after `cancel()`** (esptool-js's `disconnect()` waits for `port.readable.locked` to clear, `ejs webserial.ts:436-455`); `writable` used per packet (`getWriter`, `write`, `releaseLock`); `setSignals()` with one key per call; `getInfo()` returning `{usbVendorId, usbProductId}` (PID 0x1001 selects the USB-JTAG reset); `close()`.
  - `FakeEsp`: tracks DTR/RTS; enters ROM mode when RTS is released while DTR is asserted (or on any reset with PID 0x1001) and sends a banner after 5 ms; answers SYNC 8 times with `01 08 04 00 07 07 12 20 00 00 00 00`; answers READ_REG/WRITE_REG from a register map (SPI_CMD write makes W0 = flash ID 0x1740EF); sends `OHAI` after MEM_END and switches to 2 status bytes; handles ERASE_FLASH, FLASH_BEGIN, FLASH_DEFL_* (checks seq and checksum, inflates with `zlib.inflateSync`) and SPI_FLASH_MD5 (ROM ASCII, stub raw).
- `run.mjs`: `node run.mjs <flasher root> esp32|esp32s3|esp32c3 <image|-> <addr> <eraseAll 0|1> <out.jsonl> [pid]`. Writes a `.jsonl` of every tx/rx chunk (hex), signal, open, close and cancel event with ms timestamps, plus the terminal text. `MD5=1` injects `calculateMD5Hash`; `IGNORE_RESETS=n` ignores the first n resets; `MAX_MS` is a safety timeout.
- `decode.py` turns a `.jsonl` into a command listing; `cmpdefl.py` and `godefl/` compare deflate streams.
- Globals needed: `Blob`, `TextDecoder`, `TextEncoder`, `ReadableStream`, `WritableStream`, `setTimeout`, `atob` (all in Node 22). Call `process.exit()` at the end: esptool-js leaves 3000 ms timers pending.

| Recording (`out/`) | Chip | Image | Result |
|---|---|---|---|
| `esp32-small` | ESP32 | 4 KB at 0x10000 | done |
| `esp32-small-md5` | ESP32 | same, with MD5 verify | done |
| `esp32-merged-wipe` | ESP32 | Heltec v2 merged at 0, eraseAll | done in ~2 s; flash MD5 = file MD5 `06f8c79bc2ccf41f33c58bbe0bd8a466` |
| `s3-update` | S3 | Heltec v3 app at 0x10000 | done |
| `c3-jtag-wipe` | C3 | Xiao C3 merged at 0, PID 0x1001, eraseAll | done |
| `esp32-ignore1` | ESP32 | first reset ignored | never syncs (bug 4.10b) |

**A fake must pin these for stable goldens:** banner chunking (one chunk gives 2 SYNCs), eFuse values (they change the S3 READ_REG list), the value at 0x3fcef14c (3 means 0x800 RAM blocks), the SPI_CMD poll count, the flash ID. It must also answer the first reset; a fake that stays silent and wakes later reproduces "never syncs".

**ROM-only transcripts:** `main()` always uploads the stub, so a ROM-only reference needs a harness that calls `loader.detectChip()` and `loader.writeFlash()` without `main()`/`runStub()`, keeping `IS_STUB = false` (`ejs esploader.ts:797-857`). Remember 0.5.4 then still uses 0x4000 blocks (hosting report §B7). Not built yet.

### 9.2 nRF52: `scratchpad/src/proto-nrf52/`

- `vendor/dfu.js` and `vendor/zip.min.js`: byte-identical copies (dfu.js sha256 `08d23a67…98bc2`), with `vendor/package.json` = `{"type":"module"}`.
- `harness.mjs <zip> <out.jsonl> [ok|touch|drop-ack:N|bad-ack:N|bad-crc]`: calls `zip.configure({useWebWorkers:false})`; builds a Node `Blob`; provides a fake Web Serial port and a fake bootloader (SLIP wait-for-start, `is_rx_pkt_valid`, the seq/ACK rule, word truncation, init parsing, CRC check at STOP). A timer shim logs every `setTimeout` as a sleep and runs it at once, except dfu.js's 5000 ms read timeout, so a 440 KB flash records in about 0.4 s. (A sleep of exactly 5000 ms would be misclassified; patching `Dfu.prototype.getReader` is cleaner.)
- Output JSONL: `open{opts}`, `write{len,hex}`, `sleep{ms}`, `writer-close`, `close`, `error`. The fake confirmed `received 440924, crc 0x49DF, valid`.
- `nrfutil_ref.py` (run with `python3 -I`): drives nrfutil and compares bytes. `make_tiny_zip.py` makes the fixtures `tiny-1028.zip` (3 DATA frames, with C0 and DB bytes) and `tiny-4096.zip` (exactly 8 DATA frames, double sleep). Zip timestamps vary between builds: compare transcripts, not zip hashes.
- Recordings: `out/rak4631-ok.jsonl`, `tiny-1028.jsonl`, `tiny-4096.jsonl`, `xiao-touch.jsonl`, `xiao-drop-ack:0.jsonl`, `xiao-drop-ack:1.jsonl`, `xiao-bad-ack:2.jsonl`, `blzip.jsonl`.

Minimal fake bootloader for Go tests:

```
state: expected=1; SLIP decoder waits for the first C0
on frame p (unescaped):
  if len<=4 || !(p0&0x40) || !(p0&0x80) || (p1&0x0F)!=14 || (p0+p1+p2+p3)&0xFF!=0
     || crc16(p[:len-2]) != le16(p[len-2:])           -> drop, no ACK
  if (p0&7) != expected                               -> send ACK(expected), drop
  expected=(expected+1)&7; send ACK(expected); process payload p[4:len-2]
ACK(n) = C0, n<<3, 00, 00, (-(n<<3))&0xFF, C0
```

### 9.3 What can be byte-equal

| Part | Byte-equal? | Note |
|---|---|---|
| ESP32 SLIP framing, headers, checksums | yes | |
| ESP32 reset line sequence | yes, as an ordered list of (line, level, delay) | Linux open/close line changes are extra (4.3) |
| number of SYNCs | **no** | collapse leading SYNCs in tests |
| ESP32 info READ_REGs | yes if copied; value-dependent on S3 | not needed to flash (11.B6) |
| stub upload | yes, with the same stub (legacy v1.3.0) | |
| flash-ID sequence | yes, except the poll count | |
| ERASE_FLASH, FLASH_BEGIN(0,0), FLASH_DEFL_END | yes | |
| FLASH_DEFL_BEGIN | **only with a zlib-identical deflater** | word 2 is `ceil(compressed/0x4000)` (`ejs esploader.ts:826,842`). Heltec v2 merged: pako 781,373 B → 48 (0x30) blocks; Go 1.24 `compress/zlib` BestCompression 803,567 B → 50 (0x32) blocks (`scratchpad/src/checker-godefl/main.go`) |
| FLASH_DEFL_DATA payloads | **only with a zlib-identical deflater** | 4.6 |
| nRF52 frames | **yes, all** | dfu.js = nrfutil, proven |
| nRF52 sleeps | as an ordered list of durations | |
| timing | no | |

### 9.4 Licence care for test data

- A transcript that includes the ESP32 stub upload contains the GPL stub. Store a SHA-256 of the MEM_DATA payloads instead of the bytes (hosting report §B7), or ship the GPL text and source with the test data.
- `lib/esp32.js` is Apache-2.0 with embedded GPL stubs; `lib/zip.min.js` is BSD-3. If the harness vendors them, keep their notices. Fetching flasher@45a5257 at test time avoids committing them (a suggestion, not tested).

### 9.5 Catalog and regex checks

- `scratchpad/src/scripts/catalog_analyze.py` mirrors `matchReleaseFiles`, `pickFlashFile` and `esp32Address` in Python and prints the census, tables and checks. `catalog_compare.py` diffs git vs live. `catalog_zero_diag.py` explains zero-version entries.
- `scripts/regexcheck/main.go` + `scripts/regex_pairs.py` prove that Go RE2 and JS/Python give the same (pattern, file) pairs.
- **Not done:** running `js/catalog.js` itself in Node against the saved catalog and release files to produce a golden resolution. This section is incomplete for the catalog.

### 9.6 CLI: no harness yet

No Node harness was built for the setup console. The references to record are `config.meshcore.io lib/serial-cli.js` (aff42ad8; `command + "\r"`) and `flasher lib/console.js` (`command + "\r\n"`). A fake device must echo per role (7.2) and answer with `"  -> " + reply + "\r\n"`. **This section is incomplete.**

### 9.7 Caveat

The live site's JavaScript could not be fetched. The harnesses use flasher@45a5257, which is very likely what production runs, but this is **UNVERIFIED** (section 1.2).

---

## 10. Recovery

Nothing in this section was tried on hardware. It is derived from source code.

### 10.1 ESP32

The ROM download mode is in mask ROM. Flashing cannot remove it. It is always reachable by holding BOOT (IO0) while pressing and releasing RESET (EN), and on most boards by the auto-reset lines. So an ESP32 is never permanently bricked by a bad write; it is unusable until reflashed (esp32 report, risks).

| Interrupted or failed at | State of the board | How to recover |
|---|---|---|
| reset, SYNC, stub upload, flash ID (before any erase or write) | flash untouched | power-cycle or reset; the old firmware boots. Retry. |
| first reset fails, board silent | still running the app, or not in download mode | the reference would never sync (bug 4.10b). The Go app must cap retries and ask the user to hold BOOT, press RESET, release BOOT. TinyUSB boards at 303a:1001 (T-Beam 1W, Station G2/G3) probably need this every time (source inference). |
| update path (app at 0x10000), during data | bootloader and partition table intact; app0 partly written; spiffs untouched | enter ROM (auto-reset or BOOT+RESET); flash the app `.bin` at 0x10000 again. No identity loss. |
| "Erase device", during ERASE_FLASH or data | flash partly or fully erased; identity gone; bootloader/partition table missing or partial | the chip cannot boot an app; enter ROM (often BOOT+RESET); flash the merged image at 0x0 again |
| after the last data block | the stub writes while receiving. A returned READ_REG 0x40001000 means only that the stub has processed every queued block. An inflate or SPI error in the last block is cleared by FLASH_BEGIN(0,0), so FLASH_DEFL_END still answers OK (4.4). **Only SPI_FLASH_MD5 proves the image is complete** | verify with MD5 (4.7, 4.11); on a mismatch, flash again; reset |
| app boots old firmware after an update | otadata still points to app1 from an earlier OTA update; an app write at 0x10000 does not reset otadata (esp32 report, risks) | writing the merged image (which includes otadata at 0xE000) should fix it, without a chip erase (writer's inference from the merged layout, **UNVERIFIED**) |
| TinyUSB board (E213, E290, Tracker v2) during reset | the app's USB device vanishes; the ROM appears as a new 303a:1001 device | re-find the port (6.5: MAC serial); continue in ROM |
| after flashing over USB-Serial-JTAG (S3) | may stay in download mode (force-download flag not cleared; **UNVERIFIED** for MeshCore boards) | unplug and replug (power-cycle) |
| ROM-only mode, timeout too short | aborted halfway; possibly half-written bootloader on the merged path | use esptool-py's 40 s/MB budget; reflash via ROM |
| wrong chip image written (no chip guard) | foreign image; with "Erase device" also a foreign bootloader at 0x0 | BOOT+RESET, flash the right merged image at 0x0 |

### 10.2 nRF52

The bootloader lives in its own flash region and DFU never touches it here (application-only zips). Recovery always goes through the bootloader. **Battery-powered boards stay powered when USB is unplugged**, so "unplug" is not a reset for them: press reset or switch them off.

| Interrupted or failed at | State of the board | How to recover |
|---|---|---|
| during the 1200 touch | in the app, or in the bootloader (CDC-only; it stays while USB is mounted; back to the app if not configured within 3 s) | reset or power-cycle; the app is valid and boots. Retry the touch. |
| touch does nothing | the app is crashed or its USB stack is dead; or the board was already in the bootloader | double-press reset within 500 ms: CDC + UF2 drive, no timeout; serial DFU works there without a touch (5.2) |
| no ACK to START (5 s timeout) | wrong port (not the bootloader), or a bootloader with stale state | check the port is "nRF Serial"; reset the board; touch again |
| START rejected for size (not word-aligned, too big, app with SD/BL; `dfu_single_bank.c:290-316`) | reset before the erase; old app boots | fix the cause; retry |
| START rejected for state (`NRF_ERROR_INVALID_STATE`, `:333`): a stale session already ran START | **app already erased**, then reset; OTAFIX: Bluetooth OTA mode (`src/main.c:289-291`) | double-press reset → USB DFU → reflash |
| INIT rejected (wrong board zip, SoftDevice not in sd_req, truncated init) | **app erased**, then reset. OTAFIX 2.1+: Bluetooth OTA DFU mode, **no USB serial, no UF2 drive**. Upstream 0.9.2: USB DFU | OTAFIX: **double-press reset** → USB CDC + UF2 drive → flash the **correct** zip by serial DFU (no touch). Or Bluetooth OTA from a phone. |
| unplugged or host stopped after START (erase done), board loses power | bank 0 invalid | on power-up: OTAFIX → Bluetooth OTA mode → double-press reset → reflash |
| host stopped after START, board still powered | stays in serial DFU with stale seq/state, indefinitely (no inactivity timer in OTAFIX or 0.9.2) | reset (single press). With the app erased, OTAFIX then goes to Bluetooth OTA mode → double-press reset → reflash. **Never retry in the same bootloader session.** |
| STOP ACKed but CRC wrong | bootloader stays in serial DFU; bank invalid; dfu.js would report success | the app never re-enumerates (5.7). Reset → (OTAFIX Bluetooth mode) → double-press → reflash |
| "Erase Flash" firmware was run (by another tool) | identity, contacts and settings wiped; board ends in the UF2 bootloader | flash MeshCore again |

**Manual fallback:** in double-reset mode the board shows a UF2 drive ("nRF UF2"). Copying the catalog's `download` `.uf2` onto it installs the same app image (5.1). This needs a computer that mounts the drive; the app does not do it.

### 10.3 Setup interrupted

- Each setting is saved before its reply (7.3). An interrupted setup leaves the settings sent so far. Re-run the whole sequence and read back.
- `set radio` takes effect only after `reboot`. Until then `get radio` shows the new stored value while the radio still uses the old one.
- A wrong `set tx` value is stored but rejected by the radio at boot; `get tx` still shows it. Set a valid value.
- A clock set into the future: on boards with an RTC (most variants use `AutoDiscoverRTCClock`, 7.6) only `clkreboot` clears it; without an RTC a reboot clears it too.
- nRF52: a port closed while at 1200 baud reboots the app into DFU. The app is still valid; a reset brings it back.
- Factory reset: `erase` on the serial console (7.3).

### 10.4 Rules for the Go app

1. Validate everything before the first destructive step: file type against device type, zip manifest, `.dat`, chip ID, port identity.
2. Never retry inside a stale nRF52 bootloader session. Reset first.
3. Re-find ports by USB serial number or USB path, never by tty name alone.
4. Keep the downloaded file and the chosen device, so a retry needs no network.
5. Show the manual steps on screen: BOOT + RESET for ESP32; double-press reset for nRF52 (OTAFIX).
6. Never use the catalog's `erase` or `bootloader` files as a "recovery".

---

## 11. Open questions for the owner

**A. Catalog, sources, licences**

1. Fetch the live catalog (`https://flasher.meshcore.io/config.json` and `/releases?repo=…`) at run time, ship a pinned and validated snapshot, or both with the snapshot as a fallback? Live and git differ (70 vs 67 devices, and git has a broken stub).
2. Download MeshCore files through the flasher host (`/releases/download/<tag>/<file>?repo=github`, as the reference does) or directly from `github.com/meshcore-dev/MeshCore/releases/download/<tag>/<file>`? May GitHub be a fallback when the flasher host is down? (`/firmware/` files have no fallback.)
3. Offer Ripple firmware at all? It is closed source and freemium; its upstream and redistribution terms are unverified. If yes, only behind a "third-party, closed source" label? The entries (section 3.8): Heltec v4 + Expansion Kit (Touch), Heltec v4 R8 + Expansion Kit (Touch), LilyGo T-Deck, T-Deck Max, T-Deck Pro, T-Deck Pro v1.1, T5 E-Paper S3 Pro (H752-XX), T-Lora Pager, T-Display Pro, T-Watch S3 Plus, T-Watch Ultra, Seeed Wio Tracker L1 Pro, Wio Tracker L2 Pro, Elecrow ThinkNode M9. Without Ripple, 10 of these devices have nothing to flash.
4. Include ZephCore (third-party MIT port on zephcore.meshcore.dev, its own CLI) or the kissRadio role? The brief names only companion, repeater and room server.
5. Hide the noflash RP2040/RP2350 devices, or list them as "not supported / download only"?
6. Show the T1000-E LoRaWAN warning as a blocking confirm on every T1000-E role? That deviates from the reference UI.
7. Will the app be published (for example on GitHub) or kept for personal use? This decides how far the GPL, Apache-2.0 and BSD notice duties apply.

**B. ESP32**

1. Stub or ROM-only? This document recommends the legacy GPL stub v1.3.0, as the reference sends it (byte parity, chip erase), with the GPL text and source shipped. Alternatives: ROM-only (no blob, no chip erase, more unverified parts) or esp-flasher-stub (Apache/MIT, bytes differ).
2. "Erase device" writes the ESP32 second-stage bootloader and partition table at 0x0 after a full chip erase, and destroys the identity. Does the "never write a bootloader" rule forbid it? Options: never offer it; offer it behind a confirm step for first install or recovery; offer it only when the board does not already run MeshCore. Without a stub: emulate the erase (unverified) or write the merged image with no erase.
3. Byte-equal tests and deflate: port zlib's deflate to Go, inject the reference stream in tests, or compare only inflated data and framing?
4. Add SPI_FLASH_MD5 verification after the last block (a deviation; recommended)?
5. Copy the esptool-js 0.5.4 bugs (endless connect, no status check, no block retry, boot-log leftover) or fix them? Fixing means the tests must normalise SYNCs and status handling. Recommended: fix.
6. Send the informational commands (chip description, features, MAC twice, S3 postConnect read, flash-ID sequence) or skip them? They are not needed to flash; on S3 they depend on eFuse values.
7. Stay at 115200 (about 70 s for a 1.2 MB merged image) or switch the stub to 460800/921600 with CHANGE_BAUDRATE (faster, no longer matches the reference)?
8. How to prove the write (4.4, 4.11): always run SPI_FLASH_MD5 after the last block (recommended; one extra command), or send FLASH_DEFL_END(1) before FLASH_BEGIN(0,0) and check its status (changes the order of bytes sent)? Status checks alone cannot catch an error in the last block.
9. Timeout model (4.4): esptool-js times out per received chunk and drops buffered bytes on a timeout. Should the Go code use per-command deadlines with a no-drop buffer (recommended), or mimic the per-chunk model for closer timing parity with the Node recordings?

**C. nRF52**

1. Strict parity, or may the Go DFU resend a frame with the same seq after an ACK timeout? The bootloader handles duplicates safely (`hci_transport.c:304-314`); the success path's bytes do not change.
2. After STOP, wait for the app to re-enumerate (same USB serial, "TinyUSB Serial") and run `ver` / `get bootloader.ver`? The reference verifies nothing, and a CRC failure is invisible to it.
3. Refuse to flash when the zip's sd_req does not match the board's SoftDevice (from the catalog `erase` name or the board JSON)? A mismatch erases the app and, on OTAFIX, leaves the board in Bluetooth-only mode.
4. Support a board that is already in the bootloader (after a double-press or the Erase Flash firmware), detected by "nRF Serial", and skip the touch?
5. Confirm that the app must never offer the nRF52 "Erase Flash" (the `erase` formatter), which wipes identity, contacts and settings.

**D. Board identification and the device**

1. Pre-select a board when USB ID + product string give exactly one candidate, or only filter the list? Confirmation stays either way.
2. May the app send the MeshCore `board` command to a board that already runs repeater or room-server firmware, to narrow the choice? The reference never does.
3. May the app refuse to flash when the ESP32 chip read after SYNC does not match the selected device's MCU?
4. Which boards do you actually have, and which bootloader do the nRF52 ones run (OTAFIX 2.x, upstream, vendor)? Their real VID:PID and strings should be captured with `list_usb_ttys.py` in Phase 3.
5. Permissions: `sudo usermod -aG dialout void` plus reboot (standard), or a udev rule giving `void` the ttyACM/ttyUSB nodes (no reboot)? Is lingering on for `void` (`loginctl show-user void -p Linger`), or is there an autologin session?
6. Are ModemManager or brltty installed? If yes, may setup add an `ID_MM_DEVICE_IGNORE` udev rule for the LoRa boards?
7. Does the 6.18.39 kernel have `cdc_acm`, `cp210x` and `ch341`? Check with `modinfo cdc_acm cp210x ch341` or `grep -E 'CONFIG_USB_ACM|CONFIG_USB_SERIAL_CP210X|CONFIG_USB_SERIAL_CH341' /boot/config-$(uname -r)`; `ls /sys/bus/usb/drivers/` is valid only while a board is plugged in (6.8).

**E. Setup**

1. Set the node's clock with `time <unix>` from the CardputerZero's clock? The kit records that clock as NTP-synchronised (time zone `Etc/GMT-6`, which does not affect Unix time) (`cardputer-app-kit code/docs/device.md:30`). Open point: should the app refuse to send `time` when NTP has not synchronised yet (for example after a boot with no network)? On boards with an RTC a wrong future time survives reboots and only `clkreboot` clears it (7.6).
2. Radio presets: fetch the live list from api.meshcore.nz (needs network), build in the 27-entry snapshot of 2026-11-19, or both, plus a custom entry? The list comes from the MeshCore App API, not the MeshCore repo; its licence and terms are **UNVERIFIED**, so shipping the snapshot needs its own clearance (1.3, 7.8).
3. When a preset has `path_hash_size`, set `path.hash.mode = size − 1` automatically on v1.14.0+, as config.meshcore.io does?
4. The first saved setting turns off the factory 2-minute zero-hop advert. Send `set advert.interval` (60-240 minutes) to keep periodic local adverts, or leave them off?
5. TX power: keep a per-board maximum (SX1262 22 dBm; PA boards per FAQ 7.7) and a per-region legal limit, since `set tx` checks nothing?
6. Copy the reference quirks byte for byte (`\r` vs `\r\n`; `62.5.0`) for the Node tests, or send clean values such as `set radio 869.618,62.5,8,8`?
7. Offer latitude/longitude (with the 23-byte name limit), owner info, and a repeater guest password (blank by default, which gives guest access to a blank login)?
8. Support configuring firmware older than v1.14 (no path.hash.mode; under 300 MHz refused before v1.15), or only the versions the app flashes?

