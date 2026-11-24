# meshflash: spec

A MeshCore flasher for the owner's M5Stack CardputerZero. Plug a LoRa board into the Zero's USB-A port, pick the
board, the firmware role and the version from MeshCore's official flasher catalog, and meshflash flashes it, verifies
it, and for repeaters and room servers sets it up over its serial console. It's a field tool: flash and configure a
repeater on a hilltop with no laptop.

Written 24 Nov 2026 from `docs/meshcore-flashing.md` (the research, every fact with its source) and the CardputerZero
app kit (`docs/standards.md` S1–S38, `docs/making-apps.md`, `docs/device.md` in the kit). Where this spec and the code
disagree, the code wins: fix this file and add a line under "Changes to this spec".

The owner's answers to the open choices (24 Nov 2026) are written in, and listed in section 11.

## 1. Goals and non-goals

Goals:

- Flash MeshCore companion (Bluetooth or USB), repeater, room-server and KISS-modem firmware from the live catalog at
  `https://flasher.meshcore.io`, ZephCore from its own catalog at `https://zephcore.meshcore.dev`, and Ripple's
  builds marked "closed source, third-party", onto ESP32-family boards (through the ROM bootloader over SLIP, with an
  MD5 check of what was written) and nRF52 boards (Adafruit serial DFU from the catalog's ZIP, entered with the
  1200-baud touch).
- Work offline in the field: the catalog and every downloaded file stay on the SD card, each with the SHA-256 taken
  when it was downloaded and checked again before every flash.
- Set up a repeater or room server over its serial console: name, radio, TX power, path hash mode, admin password
  (sent to the node, never saved or logged).
- Never guess: suggest a board from USB IDs (and the ESP32's chip type once synced), and the owner confirms. Show
  board, role, version, size and update-or-wipe, then ask `y` before writing anything.
- One static Go binary (S4): no esptool, nrfutil, Python packages or anything else on the device.

Non-goals (for this version):

- Writing anything the catalog lists besides the firmware: never the nRF52 `erase` formatter, never an OTAFIX or other
  `bootloader` file, never a custom file. (The ESP32 merged "full wipe" image is firmware: see 6.2.)
- Setup of anything but MeshCore repeater and room-server firmware: companions are set up from the phone app, and
  KISS modem, Ripple and ZephCore builds have no text CLI that meshflash knows.
- MeshOS (not in the live catalog) and the RP2040/RP2350 "download only" boards (no serial bootloader).
- Bluetooth OTA, UF2 drag-and-drop, flashing over Wi-Fi.

## 2. What it talks to

| What | When | How |
| --- | --- | --- |
| `https://flasher.meshcore.io/config.json`, `/releases?repo=github` and `/releases?repo=github-ripple` | only when the owner presses `u` (update the catalog) | HTTPS GET, `User-Agent: Mozilla/5.0 (compatible; meshflash/<version>)` |
| `https://zephcore.meshcore.dev/config-zephcore.json` and `/releases?repo=github-zephcore` | with the same `u` | the same |
| The file's own site: `https://flasher.meshcore.io/releases/download/<tag>/<file>?repo=<key>` (or `/firmware/<file>` for files the catalog lists inline), or the same paths on `zephcore.meshcore.dev` for ZephCore | only when the owner presses `d` (download) or `y` on a file not yet saved | HTTPS GET, the same User-Agent. Refuse an HTML body, a body shorter than its `Content-Length`, or one under 50 KB |
| `https://github.com/meshcore-dev/MeshCore/releases/download/<tag>/<file>` | only when the flasher site fails for a MeshCore community file | the same; GitHub's `Content-Length` and MD5 header are checked too |
| The board | while a screen that needs it is open | `/dev/ttyACM*`, `/dev/ttyUSB*` (needs the `dialout` group, 9.3) |
| USB identity | when the Board screen opens or redraws after a plug-in | `/sys/class/tty/*` and the USB device above it, read-only, never opening the port |
| `~/.config/mesh/config.json` | when the setup form opens | read-only; never written (8.4) |

Nothing runs in the background (S23). No money is spent; Help says how much a catalog update downloads (about 1.5 MB)
and each file's size.

The User-Agent names meshflash and its version, as S3 wants, inside the browser form that MeshCore's FAQ (5.13) says
the flasher site needs before it serves a whole file: a departure from S3's exact form, agreed with the owner.

## 3. The flow

```
Board ─enter→ Choose board ─enter→ Role ─enter→ Version ─enter→ Confirm ─y→ Download? → Flash → Result
  │                                                (n notes)     (u/w pick update or wipe)      │
  ├─ u  Update catalog (Wi-Fi)                                                                  └─ s → Setup
  ├─ s  Set up a board already running repeater or room-server firmware (no flash)
  └─ c  Saved files
```

## 4. Screens

Designed at 45×10 (the CLI app); the same screens at 53×14 on the LCD get more rows of list and text, and still work
at 40×10 (S10). The top line is the status line (S13): the screen's subject on the left; on the right the battery
(LCD only) and the board's mark: `●` a board is plugged in and identified, `○` none, `…` busy talking to it. The
bottom line is the key hints (S12). Text wraps (S11). The mock-ups are close to, not exactly, what
the goldens will hold.

**Board** (start screen). Every plugged-in serial port, what its USB identity suggests, and what it's running when that
is known; the catalog's age. When a port appears and isn't a bootloader, meshflash asks it `ver`, `get role` and
`board` (read-only; repeater and room-server firmware answer, others stay silent within 2 s) and shows the answer.

```
meshflash                              87% ●
▸ ttyACM0 · XIAO nRF52840 (2886:8044)
  Could be: Xiao nRF52 WIO, Ikoka Stick,
  Ikoka Nano. Running repeater v1.17.1,
  “Hilltop 2”.
  ttyUSB0 · CP2102 (10c4:ea60)

Catalog from 24 Nov: newest v1.18.0
enter choose  u update  c files  ? help
```

With no board: "Plug a LoRa board into the USB-A port." With no catalog yet: "No catalog saved. Join Wi-Fi and press
u." With no permission to open ports: the exact `sudo` command (9.3). When more than three boards match a port's USB
identity, the port says how many ("Could be one of 27 boards") rather than listing them. A node that echoes what it's sent but answers
nothing (stuck in the middle of a command) says so: "press its reset button (or unplug it if it has no battery) and
press r". The probe never sends anything that could change the node, not even the blank line that would unstick it.

**Choose board.** The best matches first, then "All boards": the board the node itself names when it answers
`board`; then the boards the USB table is sure of for this identity; then, under "Might also be (unverified)", the
boards it only infers. Each group is in catalog order. The owner always picks. The cursor starts on the board picked
last time for this USB serial number (remembered, 8), else on the only match when there is exactly one; it is never
chosen for them.

```
Which board is on ttyACM0?             87% ●
The board says: Heltec V4
▸ Heltec v4
USB 303a:1001 also matches:
  Heltec v3
  Heltec Wireless Tracker
  …
Might also be (unverified):
  LilyGo T3S3
j/k move  enter pick  f find  ? help
```

`f` opens a one-line text box that filters the list as you type (Enter keeps it, Esc clears it).

**Role.** The roles this board has with at least one version, in three groups: MeshCore (Repeater, Room server,
Companion · Bluetooth, Companion · USB, KISS modem); ZephCore, marked "third-party port" (from its own catalog, matched
to the board by name; ZephCore-only boards appear in Choose board with only these); Ripple, marked "closed source,
third-party" (GUI, GUI with data on SD). The catalog's notice for the board is shown as plain text, links dropped,
including the T1000-E LoRaWAN warning that the web flasher never shows.

**Version.** Newest first, with "saved" and its size when the file is on the SD card. `n` (or Enter on a version)
shows its release notes, wrapped and paged with `j`/`k`/Space/`b`.

```
Repeater · Xiao nRF52 WIO              87% ●
▸ v1.18.0   saved · 361 KB
  v1.17.1
  v1.17.0
  v1.16.0
  …

enter next  n notes  d download  ? help
```

**Confirm.** Everything that's about to happen, and nothing written until `y`. There's no preselected kind: on an ESP32
the owner presses `u` (update: the app at 0x10000, keeps identity, contacts and Bluetooth pairing) or `w` (full wipe:
erase the whole chip and write the merged image at 0x0) before `y` does anything. An nRF52 has only an update (6.3),
so `y` works straight away.

```
Flash this?                            24% ●
Xiao nRF52 WIO on ttyACM0 (repeater 1.17.1)
Repeater v1.18.0 · 361 KB · update
Keeps identity, contacts and pairing.
File saved 24 Nov, SHA-256 97539d20… ✓
Battery 24%. Flashing takes about a minute;
plug the Zero in if you can.

y flash  esc back  ? help
```

Under 30% battery the screen says so and `y` asks once more ("Battery 24%. Flash anyway? y"). A file not yet saved
is downloaded first (Wi-Fi needed), with its progress and size, then the Confirm screen shows again with its SHA-256.

**Flashing.** Each step with a mark (`✓` done, `▸` now), the bytes written, and how long it's taken (S17). It says
"Don't unplug the board." Esc asks before stopping ("Stop now? The board will have no working firmware until it's
flashed again. y stops"). When the board vanishes or stops answering, the screen moves on at once to the result; it
never stays on "Flashing" (S16).

```
Flashing Repeater v1.18.0              87% …
Xiao nRF52 WIO on ttyACM0
✓ Into the bootloader (1200-baud touch)
✓ Erased (10 s)
▸ Writing 212 of 361 KB · 59% (0:38)

Don't unplug the board.

esc stop  ? help
```

**Result.** On success: what was written and how it was checked (ESP32: MD5 of the flash matches the file; nRF52: the
board checked the CRC and came back running the new version), then `s` to set it up (repeater and room server). On
failure: what state the board is in, in plain words, and how to recover it (from `docs/meshcore-flashing.md` §10),
with `r` to try again from the saved file (no Wi-Fi needed).

```
Flashing stopped                       87% ○
The board stopped answering while writing
(212 of 361 KB). Its old firmware is erased.
To recover: press the board's reset button
twice quickly, then press r. The file is
saved, so no Wi-Fi is needed.

r retry  esc back  ? help
```

**Setup** (repeater and room server only). A form, one field per row; Tab or `j`/`k` moves, Enter edits a field.

| Field | Default | Checks before sending |
| --- | --- | --- |
| Name | the node's current name, or empty | 1–23 bytes; none of `[ ] / \ : , ? *`; no leading or trailing space |
| Frequency, Bandwidth, Spreading factor, Coding rate (four rows) | your messenger's settings when `~/.config/mesh/config.json` has them (8.4); otherwise the last radio meshflash sent; otherwise the node's current values. Enter on Frequency opens a text box, on the other three a pick list of the allowed values. `p` lists MeshCore's presets (a copy built into meshflash, 7) and fills all four, and the path hash mode | only the bandwidths the radio accepts (7.8, 10.4, 15.6, 20.8, 31.25, 41.7, 62.5, 125, 250, 500 kHz); frequency within the firmware's range; SF 5–12; CR 5–8 |
| TX power | the node's current value | −9 dBm (the radio's minimum) up to the board's limit: MeshCore FAQ 7.7's values for amplified boards (Station G2, Ikoka E22-30S/33S, Heltec V4), 22 dBm for the rest; an Ikoka whose `board` reply and catalog name don't say which module it has gets 9, the safest of its modules |
| Path hash mode | from the preset (size − 1), else the node's current value; 1, 2 or 3 bytes | firmware v1.14.0 or newer; the row says "needs v1.14+" otherwise |
| Latitude, longitude | the node's current values; blank sends nothing | −90…90, −180…180 |
| Local advert | the node's current value: off, or every 60–240 minutes (the first setting saved turns off a new node's 2-minute local advert, so 1–59 shows as off) | 0, or 60–240 in even minutes (the node stores half the minutes and would round an odd value down) |
| Admin password | empty: you must type one | 1–15 bytes, no leading or trailing space. Shown as `•`, never saved, never logged, cleared when the screen closes |
| Guest password (room server only) | the node's current value | 1–15 bytes, always sent: a build's default is `hello` or blank, and a blank one lets anyone in |

`y` sends it: one command at a time, each line ending in CR, waiting for its `  -> ` reply; then reads every value
back and compares (numbers numerically); then `reboot`, waits for the board to come back, and checks `ver` and
`get radio`. The screen lists each step with `✓` or what went wrong (the password line shows `••••`). Setup sends only
commands the CLI reference documents for the firmware version it reads with `ver` (`docs/meshcore-flashing.md`
§7.5). When a step fails or the board goes away, the screen also says what the node is left with: nothing changed,
the settings before that step saved (and this one perhaps), or everything saved but not rebooted, and whether it is
still on its old radio until it restarts.

**Update catalog** (`u`): "Fetching the catalog… (0:03)", then "72 boards, newest v1.18.0, 1.6 MB" (70 MeshCore
boards and 2 that only ZephCore has). On failure the
saved catalog stays and the screen says why (no Wi-Fi, HTTP status).

**Saved files** (`c`): each saved file with its board, role, version, size and date; `x` deletes one after `y` (S20).

**Help** (`?`): every key of the screen it was opened from, meshflash's version, the servers (`flasher.meshcore.io`,
`zephcore.meshcore.dev`, GitHub as the fallback), the folders it uses, and the `dialout` command.

## 5. Keys

Outside text boxes every action is one plain key (S18); `j`/`k` and the arrows (Fn+F/Fn+X) always move; a tap on Esc
goes back (on Board, it quits); `?` is Help; Ctrl+C quits from anywhere (asking first while flashing).

| Screen | Keys |
| --- | --- |
| Board | `enter` choose this port's board, `u` update catalog, `s` set up without flashing, `c` saved files, `r` rescan |
| Choose board | `enter` pick, `f` find |
| Role | `enter` pick |
| Version | `enter` next, `n` notes, `d` download only |
| Notes | `j`/`k` line, Space/`b` page |
| Confirm | `u` update, `w` full wipe (ESP32), `y` flash |
| Flashing | `esc` stop (asks `y`) |
| Result | `s` set up, `r` retry, `enter` back to Board |
| Setup | Tab/`j`/`k` field, `enter` edit, `p` preset list, `y` send |
| Saved files | `x` delete (asks `y`) |
| Text box | Enter keep, Esc cancel, Tab next field, Backspace (S21) |

## 6. Flashing rules

### 6.1 Always

- Match the file to the chip before anything touches the board: ESP32 entries take only `.bin` files whose image
  header names the chip; nRF52 entries take only `.zip` DFU packages with an `application` and nothing else. Refuse
  anything else and say why.
- Check the saved file's SHA-256 against the one recorded at download, every time.
- Never write the catalog's `erase` or `bootloader` files; never offer "Erase Flash" or "Enter DFU mode" on their own.
- Warn and ask under 30% battery.
- Follow the board across resets by its USB serial number or USB path, never by tty name alone.
- When the board is unplugged or stops answering, say what state it's in and how to recover it (§10 of the research),
  and leave the Flashing screen.

### 6.2 ESP32

As the web flasher does (esptool-js 0.5.4 in `flasher.meshcore.io`): reset into the ROM bootloader, SYNC, read the
chip, upload the same flasher stub (Espressif's legacy stub v1.3.0, GPL-2.0-or-later: sent to the chip as data, its
licence and source shipped in `third_party/`), compressed write at 115200 baud, then a hard reset. A successful run
sends the reference's bytes except in three places: the added MD5 check; fewer leading SYNCs (meshflash reads the
whole boot banner first, the reference one byte of it); and the compressed data, which Go's `compress/zlib` packs
differently from the reference's pako (the stub inflates either, and the tests feed the Go code pako's stream to
compare the rest). The other departures change only failures:

- **Verify:** SPI_FLASH_MD5 over what was written, compared with the file's MD5, before the reset (the reference never
  verifies).
- **Chip guard:** stop when the chip read after SYNC isn't the chip the file's image header names (and the board's, where
  meshflash knows it: the catalog says only "esp32" for every ESP32-family board), before the stub is uploaded. Only
  the ESP32, ESP32-S3 and ESP32-C3 are flashed; other chips are named and refused.
- Check every reply's status; cap the connect attempts (at most 4 resets, none started after 10 s; the reference
  effectively never gives up) and then ask the owner to hold BOOT, press RESET and release BOOT; never drop bytes on a
  timeout. On a failure after SYNC, pulse RTS so the board leaves its bootloader, as the reference does once writing
  has started.
- **Update** (`u`): the app `.bin` at 0x10000, no erase. **Full wipe** (`w`): erase the whole chip, then the
  `-merged.bin` at 0x0. The erase is tied to the merged image and is reset for every flash (the reference can leave
  its "Erase device" box ticked and erase the chip under an app-only image). No other pair is written: an image at 0x0
  without the erase, an erase with an image anywhere else, or a merged image at 0x10000 is refused before the board
  is touched, and so is an image bigger than the flash the board reports.

### 6.3 nRF52

As the web flasher does (`lib/dfu.js`, byte-identical to adafruit-nrfutil): the 1200-baud touch, then serial DFU at
115200 from the catalog's ZIP. Departures:

- Check the ZIP before touching the board: manifest has only `application`; `firmware.bin` length a multiple of 4;
  the `.dat` init packet's device type is 0x0052 and its CRC-16 matches `firmware.bin`; its SoftDevice requirement
  matches the board's (from the catalog). Flashing refuses a package whose SoftDevice wasn't checked, because the
  bootloader refusing it erases the app and can leave an OTAFIX board in Bluetooth DFU mode.
- Count only the bootloader's exact 4-byte ACK as an answer, so a port that echoes (MeshCore's own console does) is
  never taken for the bootloader.
- Do the touch so it works on Linux (exclusive open, DTR dropped at 1200), then wait up to 10 s for the bootloader to
  appear with the same USB serial number, and send START only to a port positively identified as the bootloader
  ("nRF Serial"). A board already in its bootloader (double-pressed reset) is used without a touch.
- Resend a frame (same sequence number) up to twice when its ACK doesn't come in 5 s; the bootloader drops duplicates.
- After STOP, wait up to 15 s for the app to come back with the same serial number; on repeater and room-server
  firmware, check `ver` names the version just flashed (the board's CRC failure is otherwise silent). When the port
  goes away after STOP was sent but before its ACK arrived, the bootloader has most likely already started the new
  app: say so, and wait and check `ver` the same way rather than reporting the app erased.
- There's no full wipe on nRF52: the only way the catalog offers is its `erase` formatter, which meshflash never
  writes.

## 7. Setup over the serial console

From `docs/meshcore-flashing.md` §7: 115200 8N1 raw, DTR asserted, each command ending in CR, one at a time, 5 s per
reply, at most 128 bytes a command. The firmware's version (`ver`) picks the commands (`set path.hash.mode` only from
v1.14.0; frequency floor 300 MHz before v1.15.0). `set radio` takes effect after `reboot`; everything else at once.
The admin password is sent once, compared with the node's `password now: …` reply, and dropped. The clock is set with
`time <unix>` only when the Zero's clock is NTP-synchronised; otherwise setup says it skipped it.

The presets are a copy of MeshCore's 27 regional presets as served by `api.meshcore.nz` on 19 Nov 2026
(`docs/meshcore-flashing.md` §7.8), built in so setup works with no network. The list isn't in MeshCore's repository
and has no stated licence; the owner chose the built-in copy knowing that.

## 8. What it saves

All files are the owner's (0600 in 0700 folders), under its own name (S2).

| File | What |
| --- | --- |
| `~/.config/meshflash/config.json` | the last radio settings sent (frequency, bandwidth, SF, CR, TX power, path hash mode; never a password), and the catalog board picked for each USB serial number (serial → board name, nothing else) |
| `~/.config/meshflash/drafts.json` | an unsent setup form per board serial number, without passwords (S7) |
| `~/.cache/meshflash/catalog/` | `config.json`, `config-zephcore.json` and each `releases-<key>.json`, as fetched, with the time |
| `~/.cache/meshflash/files/` | each downloaded file under its catalog name, and `index.json`: name, URL, size, SHA-256, when |
| `~/.cache/meshflash/screen.log` | what went wrong, started afresh each run (S5). Never a password |

### 8.4 The messenger's radio settings (read-only)

meshflash reads `~/.config/mesh/config.json` and never writes it. Only the `radio` object is read; anything else in
the file is ignored, and a file that's missing, unreadable or out of range is skipped with a note on the setup form.

```json
{
  "radio": {
    "freq_mhz": 869.618,
    "bw_khz": 62.5,
    "sf": 8,
    "cr": 8,
    "tx_dbm": 22
  }
}
```

`freq_mhz` 150–2500, `bw_khz` one of the values in §4, `sf` 5–12, `cr` 5–8 (4/5 to 4/8), `tx_dbm` optional. A
messenger app that wants meshflash to offer its settings writes this. `docs/messenger-radio.md` will hold the same.

## 9. Build, install, permissions

### 9.1 Code

Go 1.25, Bubble Tea v1, module `meshflash`. From the kit: `internal/screen` copied whole; `internal/app` and
`internal/store` in shape; `internal/ui/text.go`, `editor.go` and the test driver. New:

| Package | What |
| --- | --- |
| `internal/catalog` | the catalog and releases, decoded in key order; versions resolved as `js/catalog.js` does; downloads with SHA-256; the saved copies |
| `internal/serialport` | Linux tty: raw mode, baud, DTR/RTS, exclusive open; an interface with a fake for tests |
| `internal/usbid` | sysfs scan and the USB ID table; candidates for a port |
| `internal/esp` | SLIP, ROM and stub commands, reset sequences, compressed write, MD5 |
| `internal/nrfdfu` | the DFU ZIP and init packet, HCI/SLIP frames, the 1200-baud touch, the transfer |
| `internal/nodecli` | the MeshCore serial CLI: `ver`, setup, read-back, version gates |
| `internal/flash` | one flash from start to end as steps, with what state a failure leaves the board in |
| `internal/ui` | the screens (no network or serial code in it) |
| `reference/` | the Node harnesses that run the web flasher's own JavaScript against fake ports, and their recordings |

### 9.2 Install

`make deploy HOST=CardPuter` copies the binary to `~/.local/bin/meshflash` (no `sudo`). `make install-desktop
HOST=CardPuter` installs the home-screen entry "MeshFlash" (`Exec=/home/void/.local/bin/meshflash --screen`) and its
100×100 icon `meshflash_100.png` (the owner runs it: `sudo`): a green LoRa board with its antenna and a lightning bolt
on its chip, on a teal tile (`packaging/make_icon.py`). The icon is drawn by code and shown to the owner first
(S33).

### 9.3 Permissions

Serial ports belong to group `dialout`, which `void` isn't in. The owner runs once:

```bash
sudo usermod -aG dialout void
sudo reboot
```

(A reboot, because the launcher's user manager keeps the groups it started with.) Until then meshflash shows this
command when it can't open a port.

### 9.4 Licence

meshflash's own code is MIT. `THIRD_PARTY_NOTICES` lists what it ports or carries: the web flasher's logic (MIT,
Rastislav Vysoky), the DFU code (BSD-3-Clause, Nordic Semiconductor, through `lib/dfu.js`), any esptool-js code
(Apache-2.0, Espressif), the legacy ESP32 flasher stub (GPL-2.0-or-later, with its source), and MeshCore's tables
(MIT).

## 10. Testing

- Unit tests use fakes only: a fake serial port with a fake ESP32 ROM and stub, a fake nRF52 bootloader, a fake
  MeshCore CLI (repeater and room-server echo rules), `httptest` for the flasher site, saved copies of the live
  catalog and releases. No test touches the network or a real serial port.
- The reference: the web flasher's own `js/flash.js`, `lib/esp32.js` and `lib/dfu.js` (flasher@45a5257) run in Node
  against the same fakes and record every byte, signal and sleep. The Go code's recording must match: byte for byte
  for nRF52; for ESP32 byte for byte except the number of leading SYNCs, the deflate stream (tests feed the Go code
  the reference's compressed stream; production uses Go's `compress/zlib`) and the added MD5 and status checks.
- `js/catalog.js` run in Node over the saved catalog and releases gives the resolution golden the Go catalog must
  match.
- Goldens of every screen at 53×14, 45×10 and 40×10 through the LCD parser (S10, S28); race tests; `gofmt`; arm64 vet
  (S31).
- S29 ("every route against the real server") becomes: every route against `httptest` serving the saved live copies,
  since tests never touch the network. The real site is first used on the device, with the owner.

## 11. The owner's choices

Asked as multiple choice with a recommendation first; answered 24 Nov 2026.

| Choice | Answer |
| --- | --- |
| Name | **meshflash** (home screen "MeshFlash") |
| Test boards | Heltec v4 (ESP32-S3, native USB); RAK WisBlock 4631, several Seeed Xiao nRF52, Seeed Wio Tracker L1 Pro (nRF52840) |
| Update or full wipe | **no preselection**: the owner picks every time |
| Radio settings | read `~/.config/mesh/config.json` in the format of 8.4 (no messenger writes it yet) |
| Ripple's closed-source builds | **listed, marked closed source** |
| Roles | companion, repeater, room server, **KISS modem and ZephCore** |
| ESP32 stub | **the same legacy stub as the web flasher** (GPL text and source shipped) |
| ESP32 full wipe | **offered as firmware**: chip erase plus the merged image |
| Downloads | the flasher sites, **GitHub as a fallback** for MeshCore community files |
| Radio presets | **a built-in copy** |
| Setup fields | the brief's five, the room-server guest password, **latitude/longitude and the local advert interval** |
| Node clock | set **only when the Zero's clock is synchronised** |
| Setup without flashing | **yes**, `s` on the Board screen |
| Probing a running board | **automatically** |
| TX power | **refused above the board's limit** |
| Status mark | **the board** |
| T1000-E warning | **shown only** |
| Licence | **MIT** |
| User-Agent | **`Mozilla/5.0 (compatible; meshflash/<version>)`** (departs from S3's exact form) |
| Device phase | **the owner runs the commands** and pastes back what they print |
| Ikoka TX limit with no module named (Phase 1) | **9 dBm**, the safest of its modules |
| ESP32 full wipe check (Phase 1) | **the catalog's `-merged.bin` name and the saved SHA-256** (no check inside the image, which could refuse ZephCore's) |
| A node name containing the admin password (Phase 1) | **allowed**: the name is public anyway |
| Ripple's release notes in test data (Phase 1) | **blanked**, and the branch history rewritten so they never appear |
| Icon (Phase 2) | **A: a LoRa board with its antenna and a lightning bolt** |
| A long board list (Phase 2) | **best matches first**: the board the node names, then sure USB matches, then "Might also be (unverified)" |
| Remember the board (Phase 2) | **yes, per USB serial number**, preselected next time and still confirmed |
| Radio on the Setup form (Phase 2) | **four rows**: Frequency, Bandwidth, Spreading factor, Coding rate |

Decided without asking (technical defaults; the owner can overrule any):

- ESP32 at 115200 baud like the reference (native-USB boards such as the Heltec v4 go at USB speed anyway).
- ESP32: fix esptool-js 0.5.4's failure bugs (endless connect, unchecked status, dropped bytes); add MD5 verify; send
  the same informational reads as the reference.
- nRF52: resend an unacknowledged frame up to twice; after flashing, wait for the board to come back and check `ver`.
- The setup console sends CR line endings and clean values (as MeshCore's config tool does), not the reference
  console's CR LF.

## 12. Phases

Each phase ends with a report and a stop.

1. **Protocols, against fakes.** Catalog resolution (golden from `js/catalog.js` in Node), the serial port layer, USB
   identification, ESP32 (golden recordings from `lib/esp32.js` in Node), nRF52 DFU (golden recordings from
   `lib/dfu.js`), the setup CLI against a fake node. No screens yet.
2. **Screens, against fakes.** Every screen with goldens at the three sizes; the whole flow end to end against a fake
   board and `httptest`; the icon (shown to the owner first) and the home-screen entry; a run with the screen drawn
   into a file on the PC.
3. **The device, with the owner.** The owner runs the commands: `dialout`, `make deploy`, a run on the device drawing
   into a file with keys from a FIFO (S30), memory (S9); then, with the owner watching, flashing and setting up a real
   board; the hand-over checklist (`docs/standards.md` in the kit).

## Changes to this spec

- 24 Nov 2026: first version.
- 24 Nov 2026, Phase 1 (the code wins): the Board screen's "stuck node" message and the probe never sending the blank
  line that ends region-load mode (that line makes the node route with an empty regions table); TX power's −9 dBm floor
  and 9 dBm for an Ikoka with no module named; the local advert in even minutes, 1–59 shown as off; Setup saying what
  the node is left with after a failure; 72 boards in the merged catalog; ESP32: the chip guard against the image
  header, the connect cap's numbers, the RTS pulse on failure, and only update-at-0x10000 or erase-plus-0x0 written;
  nRF52: the SoftDevice check enforced, only the exact ACK accepted, and a lost STOP ACK treated as "most likely
  worked".
- 24 Nov 2026: the owner's Phase 1 and Phase 2 answers (11): Choose board's order and the remembered board, the
  Board screen's count for long lists, four radio rows on the Setup form, the board picked per USB serial number saved
  in `config.json`, and the icon.
