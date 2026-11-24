# meshflash: spec

A MeshCore flasher for the owner's M5Stack CardputerZero. Plug a LoRa board into the Zero's USB-A port, pick the
board, the firmware role and the version from MeshCore's official flasher catalog, and meshflash flashes it, verifies
it, and for repeaters and room servers sets it up over its serial console. It's a field tool: flash and configure a
repeater on a hilltop with no laptop.

Written 24 Nov 2026 from `docs/meshcore-flashing.md` (the research, every fact with its source) and the CardputerZero
app kit (`docs/standards.md` S1–S38, `docs/making-apps.md`, `docs/device.md` in the kit). Where this spec and the code
disagree, the code wins: fix this file and add a line under "Changes to this spec".

Choices marked **(asked)** are waiting on the owner; the recommended default is written in, and section 11 lists them.

## 1. Goals and non-goals

Goals:

- Flash MeshCore companion (Bluetooth or USB), repeater and room-server firmware from the live catalog at
  `https://flasher.meshcore.io`, onto ESP32-family boards (through the ROM bootloader over SLIP, with an MD5 check of
  what was written) and nRF52 boards (Adafruit serial DFU from the catalog's ZIP, entered with the 1200-baud touch).
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
- Ripple's closed-source GUI builds, ZephCore, MeshOS, and the RP2040/RP2350 "download only" boards **(asked)**.
- Companion setup (companions are set up from the phone app), Bluetooth OTA, UF2 drag-and-drop, flashing over Wi-Fi.

## 2. What it talks to

| What | When | How |
| --- | --- | --- |
| `https://flasher.meshcore.io/config.json` and `/releases?repo=github` | only when the owner presses `u` (update the catalog) | HTTPS GET, `User-Agent: meshflash/<version>` (S3) |
| `https://flasher.meshcore.io/releases/download/<tag>/<file>?repo=github` (and `/firmware/<file>` for inline catalog files) | only when the owner presses `d` (download) or `y` on a file not yet saved | HTTPS GET, the same User-Agent. Refuse an HTML body, a body shorter than its `Content-Length`, or one under 50 KB |
| `https://api.meshcore.nz/api/v1/config` (MeshCore's radio presets) **(asked)** | with the catalog update (`u`) | HTTPS GET; cached with the catalog |
| The board | while a screen that needs it is open | `/dev/ttyACM*`, `/dev/ttyUSB*` (needs the `dialout` group, 9.3) |
| USB identity | when the Board screen opens or redraws after a plug-in | `/sys/class/tty/*` and the USB device above it, read-only, never opening the port |
| `~/.config/mesh/config.json` | when the setup form opens | read-only; never written (8.4) |

Nothing runs in the background (S23). No money is spent; Help says how much a catalog update downloads (about 1 MB)
and each file's size.

## 3. The flow

```
Board ─enter→ Choose board ─enter→ Role ─enter→ Version ─enter→ Confirm ─y→ Download? → Flash → Result
  │                                                (n notes)     (u/w pick update or wipe)      │
  ├─ u  Update catalog (Wi-Fi)                                                                  └─ s → Setup
  ├─ s  Set up a board already running repeater or room-server firmware (no flash) (asked)
  └─ c  Saved files
```

## 4. Screens

Designed at 45×10 (the CLI app); the same screens at 53×14 on the LCD get more rows of list and text, and still work
at 40×10 (S10). The top line is the status line (S13): the screen's subject on the left; on the right the battery
(LCD only) and the board's mark: `●` a board is plugged in and identified, `○` none, `…` busy talking to it
**(asked)**. The bottom line is the key hints (S12). Text wraps (S11). The mock-ups are close to, not exactly, what
the goldens will hold.

**Board** (start screen). Every plugged-in serial port, what its USB identity suggests, and what it's running when that
is known; the catalog's age.

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
u." With no permission to open ports: the exact `sudo` command (9.3).

**Choose board.** The catalog's boards that match the USB identity first, then "All boards". The owner always picks;
when exactly one matches it is preselected, never chosen for them.

```
Which board is on ttyACM0?             87% ●
Matches USB 2886:8044:
▸ Seeed Studio Xiao nRF52 WIO
  Ikoka Stick
  Ikoka Nano
  All 62 boards…


j/k move  enter pick  f find  ? help
```

`f` opens a one-line text box that filters the list as you type (Enter keeps it, Esc clears it).

**Role.** The roles this board has in the catalog with at least one version: Repeater, Room server, Companion
(Bluetooth), Companion (USB) (and KISS modem **(asked)**). The catalog's notice for the board is shown here as plain
text (the T1000-E LoRaWAN warning, which the web flasher never shows, also asks `y` before going on (asked)).

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
| Radio | your messenger's settings when `~/.config/mesh/config.json` has them (8.4); otherwise the last radio meshflash sent; otherwise a MeshCore preset; or Manual | only the bandwidths the radio accepts (7.8, 7.8, 10.4, 15.6, 20.8, 31.25, 41.7, 62.5, 125, 250, 500 kHz); frequency within the firmware's range; SF 5–12; CR 5–8 |
| TX power | the node's current value | at most the board's limit (asked) |
| Path hash mode | from the preset (size − 1), else the node's current value; 1, 2 or 3 bytes | firmware v1.14.0 or newer; the row says "needs v1.14+" otherwise |
| Admin password | empty: you must type one | 1–15 bytes, no leading or trailing space. Shown as `•`, never saved, never logged, cleared when the screen closes |
| Guest password (room server) (asked) | the node's current value | 1–15 bytes |

`y` sends it: one command at a time, each line ending in CR, waiting for its `  -> ` reply; then reads every value
back and compares (numbers numerically); then `reboot`, waits for the board to come back, and checks `ver` and
`get radio`. The screen lists each step with `✓` or what went wrong (the password line shows `••••`). Setup sends only
commands the CLI reference documents for the firmware version it reads with `ver` (`docs/meshcore-flashing.md`
§7.5).

**Update catalog** (`u`): "Fetching the catalog… (0:03)", then "70 boards, newest v1.18.0, 1.0 MB". On failure the
saved catalog stays and the screen says why (no Wi-Fi, HTTP status).

**Saved files** (`c`): each saved file with its board, role, version, size and date; `x` deletes one after `y` (S20).

**Help** (`?`): every key of the screen it was opened from, meshflash's version, the server
(`flasher.meshcore.io`), the folders it uses, and the `dialout` command.

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
chip, upload the flasher stub **(asked: the legacy stub is GPL-2.0)**, compressed write at 115200 baud, then a hard
reset. Departures, each changing only failures, not the bytes of a successful run, except the added MD5 check:

- **Verify:** SPI_FLASH_MD5 over what was written, compared with the file's MD5, before the reset (the reference never
  verifies).
- **Chip guard:** stop when the chip read after SYNC isn't the board's chip.
- Check every reply's status; cap the connect attempts (the reference effectively never gives up) and then ask the
  owner to hold BOOT, press RESET and release BOOT; never drop bytes on a timeout.
- **Update** (`u`): the app `.bin` at 0x10000, no erase. **Full wipe** (`w`): erase the whole chip, then the
  `-merged.bin` at 0x0. The erase is tied to the merged image and is reset for every flash (the reference can leave
  its "Erase device" box ticked and erase the chip under an app-only image).

### 6.3 nRF52

As the web flasher does (`lib/dfu.js`, byte-identical to adafruit-nrfutil): the 1200-baud touch, then serial DFU at
115200 from the catalog's ZIP. Departures:

- Check the ZIP before touching the board: manifest has only `application`; `firmware.bin` length a multiple of 4;
  the `.dat` init packet's device type is 0x0052 and its CRC-16 matches `firmware.bin`; its SoftDevice requirement
  matches the board's (from the catalog).
- Do the touch so it works on Linux (exclusive open, DTR dropped at 1200), then wait up to 10 s for the bootloader to
  appear with the same USB serial number, and send START only to a port positively identified as the bootloader
  ("nRF Serial"). A board already in its bootloader (double-pressed reset) is used without a touch.
- Resend a frame (same sequence number) up to twice when its ACK doesn't come in 5 s; the bootloader drops duplicates.
- After STOP, wait up to 15 s for the app to come back with the same serial number; on repeater and room-server
  firmware, check `ver` names the version just flashed (the board's CRC failure is otherwise silent).
- There's no full wipe on nRF52: the only way the catalog offers is its `erase` formatter, which meshflash never
  writes.

## 7. Setup over the serial console

From `docs/meshcore-flashing.md` §7: 115200 8N1 raw, DTR asserted, each command ending in CR, one at a time, 5 s per
reply, at most 128 bytes a command. The firmware's version (`ver`) picks the commands (`set path.hash.mode` only from
v1.14.0; frequency floor 300 MHz before v1.15.0). `set radio` takes effect after `reboot`; everything else at once.
The admin password is sent once, compared with the node's `password now: …` reply, and dropped. The clock is set with
`time <unix>` only when the Zero's clock is synchronised **(asked)**.

## 8. What it saves

All files are the owner's (0600 in 0700 folders), under its own name (S2).

| File | What |
| --- | --- |
| `~/.config/meshflash/config.json` | the last radio settings sent (frequency, bandwidth, SF, CR, TX power, path hash mode; never a password) |
| `~/.config/meshflash/drafts.json` | an unsent setup form per board serial number, without passwords (S7) |
| `~/.cache/meshflash/catalog/` | `config.json`, `releases-github.json` (and the presets), as fetched, with the time |
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
100×100 icon `meshflash_100.png` (the owner runs it: `sudo`). The icon is drawn by code and shown to the owner first
(S33).

### 9.3 Permissions

Serial ports belong to group `dialout`, which `void` isn't in. The owner runs once:

```bash
sudo usermod -aG dialout void
sudo reboot
```

(A reboot, because the launcher's user manager keeps the groups it started with.) Until then meshflash shows this
command when it can't open a port.

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

## 11. Open choices (asked)

The owner answers these as multiple choice; the recommendation is first. Answers go in here with the date.

Answered 24 Nov 2026:

- Name: **meshflash**.
- Test boards: Heltec v4 (ESP32-S3, native USB), RAK WisBlock 4631, several Seeed Xiao nRF52, Seeed Wio Tracker L1
  Pro (all nRF52840).
- Update or wipe: **no preselection**; the owner picks every time.
- Radio settings: read `~/.config/mesh/config.json` in the format of 8.4 (no messenger writes it yet).

To ask:

1. Ripple's closed-source GUI builds (28 entries on 14 boards; 10 of the boards have nothing else): hide; or list
   them marked "closed source".
2. Roles beyond companion, repeater and room server: none; KISS modem; KISS modem and ZephCore.
3. ESP32 flasher stub: the legacy stub the web flasher sends (GPL-2.0, its text and source shipped in the repo);
   ROM only (no stub, no full-chip erase, more untested parts); Espressif's newer MIT/Apache stub (bytes differ from
   the reference).
4. Full wipe on ESP32 (merged image with the second-stage bootloader, after a chip erase): offer it; offer it without
   the chip erase; never.
5. Downloads: the flasher site only; or GitHub as a fallback when the site fails.
6. Radio presets: fetch MeshCore's list from `api.meshcore.nz` with the catalog; a built-in copy; none.
7. Setup fields: the five in the brief plus the room-server guest password; also latitude/longitude and the local
   advert interval; only the five.
8. The node's clock: set it when the Zero's clock is synchronised; never.
9. Set up a board already running repeater or room-server firmware, without flashing: yes; no.
10. Asking a running board `ver`, `get role` and `board` to sharpen the suggestion: automatically when it's plugged
    in; only on a key; never.
11. TX power: refuse above the board's known limit (22 dBm for most, lower for amplified boards); warn only; no limit.
12. The status line's mark: the board's connection; the Wi-Fi.
13. The T1000-E LoRaWAN warning: show it and ask `y`; show it only.
14. The app's licence (the repository is public): MIT; Apache-2.0; GPL-3.0.
15. The device phase: attach your PC through Remote Control so I can run `make deploy` and the drive scripts with you
    watching; or you run the commands I give you.

## Changes to this spec

- 24 Nov 2026: first version.
