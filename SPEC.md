# meshflash: spec

A MeshCore flasher for the owner's M5Stack CardputerZero. Plug a LoRa board into the Zero's USB-A port, pick the
board, the firmware role and the version from MeshCore's official flasher catalog, and meshflash flashes it, verifies
it, and for repeaters and room servers sets it up over its serial console. It's a field tool: flash and configure a
repeater on a hilltop with no laptop.

Written 24 Nov 2026 from `docs/meshcore-flashing.md` (the research, every fact with its source) and the CardputerZero
app kit (`docs/standards.md` S1–S38, `docs/making-apps.md`, `docs/device.md` in the kit). Where this spec and the code
disagree, the code wins: fix this file and add a line under "Changes to this spec".

The owner's answers to the open choices (24 and 25 Nov 2026) are written in, and listed in section 11.
`docs/phase2-design.md` is the plan for the screens, and agrees with this file.

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
| USB identity | every second, on every screen, so the status line's mark follows the board | `/sys/class/tty/*` and the USB device above it, read-only, never opening the port |
| `~/.config/mesh/config.json` | when the setup form opens | read-only; never written (8.4) |
| The LCD's backlight | with `--screen` only, while the screen lock is dark (4) | its sysfs `brightness`, written 0 and then back to what it was, if meshflash may (checked in Phase 3); never touched when drawing anywhere but `/dev/fb0`: a run into a file may be given a fake folder for testing (`--backlight-sys`), and one under `/sys` is refused there |

Nothing runs in the background (S23): the every-second USB read is sysfs only, opens nothing and goes nowhere. No money
is spent; Help says how much a catalog update downloads (about 1.5 MB), and a file's size shows once it is known
(downloading, saved): the catalog lists none.

The User-Agent names meshflash and its version, as S3 wants, inside the browser form that MeshCore's FAQ (5.13) says
the flasher site needs before it serves a whole file: a departure from S3's exact form, agreed with the owner.

## 3. The flow

```
Board ─enter→ Choose board ─enter→ Role ─enter→ Version ─enter→ Confirm ─y→ Download? → Flash → Result
  │                                                (n notes)     (u/w pick update or wipe)      │
  ├─ u  Update catalog (Wi-Fi)                                                                  └─ s → Setup
  ├─ s  Set up a board already running repeater or room-server firmware (no flash)
  └─ c  Saved files

Any screen ─Tab held 3 s→ locked ─any key, then Tab, then Enter→ the same screen
```

## 4. Screens

Designed at 45×10 (the CLI app); the same screens at 53×14 on the LCD get more rows of list and text, and still work
at 40×10 (S10). The top line is the status line (S13): the screen's subject on the left; on the right the battery
(LCD only) and the board's mark: `…` while meshflash talks to a board; else, on a screen about one board, `●` while
that board is plugged in and identified by its USB identity and `○` when it isn't (Board: the selected port; Choose
board to Flashing: the board picked; Result and Setup: the board as it is now); on Update catalog and Saved files, `●`
when any board is plugged in. The bottom line is the key hints (S12). Text wraps (S11), a word too wide for the line
(a file name) breaking after a "-", "_" or "/" where it can; at 40×10 long step lists fold their oldest steps into
"✓ 3 earlier steps", and Result's and Setup's end scroll with `j`/`k` a paragraph at a time (the owner's answer, 11),
what to do now kept in view. Lists scroll by whole items (Board's ports, Saved files, the Setup form's rows with what
is said under them), so no item shows only its first or last lines; Role's notices stay above its list, which scrolls
under them. The selected row starts with `▸`; group headings ("MeshCore", "All boards:", Help's "Keys") are in
reverse video. The mock-ups are close to, not exactly, what the goldens hold.

**Board** (start screen). Every plugged-in serial port, what its USB identity suggests, and what it's running when that
is known; the catalog's age. When a port appears and isn't a bootloader, meshflash asks it `ver`, `get role` and
`board` (read-only; repeater and room-server firmware answer, others stay silent within 2 s) and shows the answer;
from a repeater or room server it then reads its name with read-only `get`s (never the guest password). An nRF52 in
its bootloader is never opened: "In its bootloader, ready to flash.", or why a double press of reset is needed first.
A port another program holds says so. A USB bridge is named by its chip family ("CP210x"). Ports are asked one at a
time: "Asking it what it runs… (0:01)", the others "Waiting to ask it…". An nRF52 bootloader that needs a double press
of reset first says why: "Its last flash stopped part-way: …", "It was already in its bootloader when meshflash
started: …" (6.3), or "Looks like its bootloader, but meshflash can't confirm it: press reset twice quickly.". Until
the saved catalog is read the catalog line says "Reading the saved catalog…" (or "Can't read the saved catalog:
<why>."); a USB scan that fails says why, instead of "Plug a LoRa board…" or the dialout guess. When `s` is offered
the hint is "enter choose  s set up  u update  ? help", leaving `c` to Help so the hint fits 40 columns.

```
meshflash                              87% ●
▸ ttyACM0 · XIAO nRF52840 (2886:8044)
  Could be: Xiao nRF52 WIO, Ikoka Stick,
  Ikoka Nano. Running repeater v1.17.1,
  “Hilltop 2”.
  ttyUSB0 · CP210x (10c4:ea60)

Catalog from 24 Nov: newest v1.18.0
enter choose  s set up  u update  ? help
```

With no board: "Plug a LoRa board into the USB-A port." With no catalog yet: "No catalog saved. Join Wi-Fi and press
u." With no permission to open ports: the exact `sudo` command (9.3). When more than three boards match a port's USB
identity, the port says how many ("Could be one of 27 boards") rather than listing them. A node that echoes what it's
sent but answers nothing (stuck in the middle of a command) says so: "press its reset button (or unplug it if it has no
battery) and press r". The probe never sends anything that could change the node, not even the blank line that would
unstick it. `s` sets up a port that answered as a MeshCore repeater or room server (never ZephCore); on any other a
note says why not, by where its probe is: "Wait: meshflash is still asking it what it runs." while it is asked, "s
sets up a repeater or room server; this board didn't answer as one." for a silent one, and for one that can't be set
up the reason its line gives; the note goes once that port's answer comes. A node that answered but can't be set up
says so in one sentence: "Running repeater v1.10.0: flash v1.11.0 or newer to set it up here.", "Running ZephCore
repeater v1.18.2: meshflash sets up only MeshCore's own repeater and room-server firmware."

**Choose board.** The best matches first: the board the node itself names when it answers `board`; then the boards
the USB table is sure of for this identity; then, under "Might also be (unverified)", the boards it only infers; then
"All boards", every board not already above. Each group is in catalog order. Boards
meshflash can't flash on this port are listed grey with why ("Raspberry Pi Pico W · download only", "LilyGo T-Lora C6
· ESP32-C6", "RAK WisBlock 4631 · nRF52" on an ESP32's port), and Enter on one says why. The owner always picks. The
cursor starts on the board picked last time for this USB serial number (remembered, 8), else on the only match when
there is exactly one; it is never chosen for them. The group headings read "The board says: Heltec V4", "USB
303a:1001 matches:" ("also matches:" under a board the node named), "Might also be (unverified):" and "All boards:";
a grey board keeps its tag whole and only its name is cut ("RPI Pico W (rp2040) +… · download only").

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

`f` opens a one-line text box that filters the list as you type (Enter keeps it, Esc clears it); `↑`/`↓` still move
the cursor while it is open, and a filter kept shows as a grey "Find: <text>" line.

**Role.** The roles this board has with at least one version, in three groups: MeshCore (Repeater, Room server,
Companion · Bluetooth, Companion · USB, KISS modem); ZephCore, marked "third-party port" (from its own catalog, matched
to the board by name; ZephCore-only boards appear in Choose board with only these); Ripple, marked "closed source,
third-party" (GUI, GUI with data on SD). A role is named by the catalog's title and subtitle ("Companion ·
Bluetooth"). The selected role's notices are shown above the list as plain text, links dropped, including the T1000-E
LoRaWAN warning that the web flasher never shows: in normal text (they are warnings), and pinned there while the list
scrolls under them, so a role low in the list shows its own; on Role only, not repeated on Confirm (the owner's
answer, 11). When they don't all fit with the selected row (40×10), whole notices are left out, one saying the board
can be bricked kept first, then the shortest.

**Version.** Newest first, with "saved" and its size when the file is on the SD card; an ESP32 version, which has two
files, says which: "update saved · 361 KB", "wipe saved · 1.3 MB" or "both saved". Enter goes to Confirm; `n` shows
the release notes, wrapped and paged with `j`/`k` (two lines) and Space/`b`; `d` downloads without flashing (an ESP32
version's both files, so update and full wipe both work offline later).

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

Under 30% battery, or when the battery can't be read, the screen says so and a `y` that would flash asks once more
("Battery 24%. Flash anyway?", "Battery level unknown. Flash anyway?"). An nRF52 board whose SoftDevice the catalog
doesn't name (T-Echo, Wio Tracker L1 EINK, ThinkNode M1, Nano G2 Ultra) says what that risks and asks a second `y` the
same way. A held `y` answers nothing: a `y` less than 0.5 s after the previous `y` is ignored and the question stays,
so release and press again. A file not yet saved reads "Repeater v1.18.0 · update · not saved yet" and `y` downloads
it first (Wi-Fi needed), with its progress and size; then the Confirm screen shows again with its SHA-256, and `y`
flashes, once the new check is back and 0.5 s have passed. A download that failed says why in place of "Not saved
yet". When the board is unplugged the screen says so and `y` does nothing. Picking `u` or `w` while a `y`'s battery
reading is still out drops that `y`, so a newly picked kind is never written without a `y` of its own. On an nRF52
`u` does nothing and `w` says "An nRF52 has only an update.". When the body doesn't fit (40×10 with a question open),
the line saying what the kind does is left out first, then Prepare's notes; then the first line is shortened to the
board's name and the file line loses its size; past that the top lines go, never the file's check and the warnings
at the bottom.

**Download** (a file not saved yet, from Confirm's `y`, or a version's files from `d`): each file's name on a line of
its own (a label, cut with "…" when it is wider than the screen: the owner's answer, 11) and under it its bytes as they
come ("212 of 361 KB · 59% (0:12)"), where from ("From flasher.meshcore.io", or "From GitHub (the flasher site didn't
have it)"), and "Wi-Fi needed.". Esc stops it and goes back at once, without asking: nothing saved changes. At the end
it goes back where it came from.

**Flashing.** Each step with a mark (`✓` done, `▸` now), the bytes written, and how long it's taken (S17). It says
"Don't unplug the board." Esc asks before stopping, in words that say what stopping leaves: "Stop now? Nothing has
been written yet: the board keeps its firmware.", "Stop now? The board will have no working firmware until it's
flashed again.", "Stop now? It's already written; meshflash just won't check it.", "Stop now? Nothing is written yet;
the board stays in its bootloader until reset." (after the 1200-baud touch, or with the board found in its bootloader
or ROM). Ctrl+C asks the same, ending "…and
meshflash quits." (a flash these screens didn't start, which shouldn't happen, gets "Quit now? The flash under way
stops the safe way, and meshflash quits."); after `y` the screen says "Stopping…". The steps read, for an ESP32, "Into
the bootloader" (with "attempt 2 of 4" while it retries), "Flasher stub loaded (ESP32-S3)", "Erased the whole chip"
(a wipe), "Writing" then "Written", "Checked: MD5 matches", "Restarted"; for an nRF52, "Into the bootloader (1200-baud
touch)" or "Already in its bootloader" or "Press the board's reset button twice quickly", "Erasing" then "Erased",
"Written", "The board checked the CRC", "Waiting for it on USB" then "Back on USB (ttyACM0)", "Asking its version"
then "Running v1.18.0", "Back to 115200 baud" (a step under way says what it does, and only once done what it found);
a resent frame says "No answer: sending again (2 of 3)", a stop during the erase "Stopping once the erase ends",
beside the erase's own clock ("(0:04, about 10 s)"). When the board vanishes or stops answering, the screen moves on at
once to the result; it never
stays on "Flashing" (S16). If meshflash itself crashes, or the CLI app's terminal closes, mid-flash, the flash
finishes with no screen (up to about 3 minutes) and `screen.log` says how it ended (8).

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
board checked the CRC and came back running the new version; for nRF52 firmware with no console (companion, KISS,
ZephCore, Ripple), "Flashed" once the bootloader took the image and left, saying whether the board came back on USB and
that meshflash can't ask this firmware its version), then `s` to set it up (MeshCore repeater and room server). On
failure: what state the board is in, in plain words, and how to recover it (from `docs/meshcore-flashing.md` §10),
with `r` to try again from the saved file (no Wi-Fi needed): back to Confirm with the board and version kept; on an
ESP32 the kind is cleared (`u` or `w` again, then `y`), an nRF52 goes straight to `y`. After an nRF52 stopped
part-way, Result says "Press r; meshflash then asks you to press reset twice quickly." and Flashing waits for that
double press (asking again if meshflash didn't see it). The state and what to do stay in view while the rest scrolls a
paragraph at a time (each sentence whole, `j`/`k` one, Space/`b` a page), and so does "Press s to set it up."; when
they leave too little room for the Summary's first sentence, "Press r to try again: …" scrolls after the Summary
instead (the hint still says `r`). Enter and Esc go back to Board. The titles are "Flashed", "Flashed, not
checked", "Flashing stopped" and "Not flashed"; the state is one sentence that what to do never repeats.

```
Flashing stopped                       87% ○
The board stopped answering while writing
(212 of 361 KB).
Its app is erased.
Press r; meshflash then asks you to press
reset twice quickly.
Press r to try again: the file is saved, so
no Wi-Fi is needed.
r retry  esc back  ? help
```

**Setup** (MeshCore repeater and room server only, never ZephCore). A form, one field per row; Tab or `j`/`k` moves,
Enter edits a field, and Tab in a field being edited keeps it and opens the next, so Tab walks through the form; the
form scrolls to keep the field in view.

| Field | Default | Checks before sending |
| --- | --- | --- |
| Name | the node's current name, or empty | 1–23 bytes; none of `[ ] / \ : , ? *`; no leading or trailing space |
| Frequency, Bandwidth, Spreading factor, Coding rate (four rows) | your messenger's settings when `~/.config/mesh/config.json` has them (8.4); otherwise the last radio meshflash sent; otherwise the node's current values. Enter on Frequency opens a text box, on the other three a pick list of the allowed values. `p` lists MeshCore's presets (a copy built into meshflash, 7) and fills all four, and the path hash mode | only the bandwidths the radio accepts (7.8, 10.4, 15.6, 20.8, 31.25, 41.7, 62.5, 125, 250, 500 kHz); frequency within the firmware's range; SF 5–12; CR 5–8 |
| TX power | your messenger's `tx_dbm` (8.4), else the last TX power meshflash sent, else the node's current value; capped at the board's limit, with a note when it was | -9 dBm (the radio's minimum; an ASCII hyphen on screen) up to the board's limit: MeshCore FAQ 7.7's values for amplified boards (Station G2, Ikoka E22-30S/33S, Heltec V4), 22 dBm for the rest; an Ikoka whose `board` reply and catalog name don't say which module it has gets 9, the safest of its modules |
| Path hash mode | from the presets whose radio the form starts with, when they all agree on one (size − 1), else the last sent, else the node's current value; 1, 2 or 3 bytes | firmware v1.14.0 or newer; the row says "needs v1.14+" otherwise |
| Latitude, longitude | the node's current values; blank sends nothing | −90…90, −180…180 |
| Local advert | the node's current value: off, or every 60–240 minutes (the first setting saved turns off a new node's 2-minute local advert, so 1–59 shows as off) | 0, or 60–240 in even minutes (the node stores half the minutes and would round an odd value down) |
| Admin password | empty: you must type one | 1–15 bytes, no leading or trailing space. The row shows `••••` whatever its length, the text box one `•` per character; never saved, never logged, cleared when the screen closes and after sending |
| Guest password (room server only) | the node's current value | 1–15 bytes, always sent: a build's default is `hello` or blank, and a blank one lets anyone in. Shown as `••••` like the admin password |

`y` sends it: one command at a time, each line ending in CR, waiting for its `  -> ` reply; then reads every value
back and compares (numbers numerically); then `reboot`, waits for the board to come back, and checks `ver` and
`get radio`. The screen lists each step with `✓` or what went wrong (the password line shows `••••`). Setup sends only
commands the CLI reference documents for the firmware version it reads with `ver` (`docs/meshcore-flashing.md`
§7.5). When a step fails or the board goes away, the screen also says what the node is left with: nothing changed,
the settings before that step saved (and this one perhaps), or everything saved but not rebooted, and whether it is
still on its old radio until it restarts; `r` then goes back to the form with its values (the passwords typed again).
Esc while sending asks `y` before stopping, in words that say what the node keeps ("…, and switches to the new radio
when it restarts."; after the reboot "Stop now? It has restarted with the new settings; meshflash just won't check
them."). Ctrl+C while sending quits at once, without asking: the setup stops where it is (between two commands:
nothing more is written once it stops, the 100 ms gap before a command included, and a command already written has
its answer read, for 1 s at most (`nodecli.StopGrace`, the owner's answer, 11), so what the node took is known; one
whose answer never came "was sent just as it stopped, and may have been saved"), and `screen.log` says what the node
was sent, what it is left with (its radio saved but not rebooted: still on the old radio until it restarts, say) and
how to finish. What was typed and not sent is kept per board (8) and comes back when the form reopens: "Restored what
was typed for this board.", or "…for the board on this USB socket." for a board with no useful serial number, ending
"… as a room server." when the draft was typed for the other role.

The form in detail (the code, `internal/ui/setup*.go`): "Reading the node's settings… (0:02)" first ("Waiting for it
to start, then reading its settings…" right after a flash); the title "Set up repeater v1.18.0", "Setting up …" while
sending, and the end's title: "Set up", "Setup stopped" or "Set up, but it came back different". Each row is "▸ Label
value" (`✎` while it is being edited), its problem in grey under it as a sentence, and the TX cap's note ("22 dBm is
above this board's limit: 19 dBm."). Grey notes at the top: the messenger file's note, and "The Zero's clock isn't
synchronised: the node's clock won't be set.". An open text box has a grey hint under it ("Up to 23 bytes.", "In dBm,
-9 to 22 on this board.", "Up to 15 bytes. Sent to the node, never saved."). A blank latitude, longitude or local advert
shows a grey "not sent"; the local advert is typed as "off" or minutes and shows "every 120 minutes"; on v1.13.0 and
older the path hash row reads "needs v1.14+". On the form, nothing open, Tab, `j`, `k` and Shift+Tab move (Tab wraps
from the last row to the first; `j` and `k` stop at the ends) and open nothing. In an open text box or pick list Tab
keeps it and opens the next row, a text box or a pick list alike, so Tab walks through the whole form (the owner's
answer, 11); Shift+Tab keeps it and opens the row before. From the last row (Admin password, or Guest password on a
room server) Tab keeps it and closes it, the focus staying there, ready for `y`; Shift+Tab does the same on Name. A row
that can't open (the path hash before v1.14.0) is passed over without its note. A row whose text box or pick list
can't hold its value as it is (a node's name over 23 bytes, a bandwidth not in the list) stops the walk there with the
form closed, its value and its problem unchanged, so Tab never changes a row that wasn't opened; Enter opens it (11).
`p` lists the 27 presets; Enter applies one, moves to Frequency and says "Radio set from the <preset> preset.". A
preset gives the path hash default only when every preset with the form's radio agrees on one (the owner's answer,
11; EU/UK (Narrow), Netherlands (Limburg) and Switzerland share a radio but not a path hash, as do Canada and USA);
otherwise the last sent, then the node's. `y` with a problem left is refused: the focus goes to the first row with
one and the note names the rows. After a failure `r` goes back to the form with the focus on the admin password and
both passwords empty (the end says "(type the password again)", for a room server "(type both passwords again)").
Help can't open inside a text box (`?` is a letter there), so the form's Help also lists the text box's keys under
"In a text box:".

**Update catalog** (`u`): "Fetching the catalog… (0:03)", then "72 boards, newest v1.18.0, 1.6 MB" (70 MeshCore
boards and 2 that only ZephCore has). Esc while fetching stops it and goes back to the Board, which says "Update
stopped. The saved catalog stays.". On failure, or a stop, the saved catalog stays and the screen says why (no Wi-Fi,
HTTP status), or, with none saved, "No catalog is saved yet." ("The saved catalog is still damaged." for a damaged
one). A catalog that arrives whole after an Esc too late to stop it is the saved one by then, and is shown.

**Saved files** (`c`): each saved file with its board ("and 2 more boards" when several list it), role, version, size
and date; `x` deletes one after `y` (S20), and its row goes once it is deleted.

**Help** (`?`): every key of the screen it was opened from, the screen lock ("hold tab"), Ctrl+C, meshflash's version,
the servers (`flasher.meshcore.io`, `zephcore.meshcore.dev`, GitHub as the fallback), that it reads Linux's USB list
every second and opens a port only to ask a board what it runs, on the Board and Choose board screens, and to flash
or set it up, the folders it uses, the
messenger file it reads and never writes, what the lock does and that holding Esc 3 s still ends meshflash, locked
or not, in the CLI app as on the LCD, and the `dialout` commands ("Run these once").

**Screen lock** (any screen; the owner's request, 25 Nov 2026). Holding Tab 3 s locks meshflash: every key is then
ignored but the unlock, and a flash, a setup or a download carries on. The lock page shows meshflash's status line, what
is running and how long its step has run (S17: "Flashing · 42% (0:38)", "Setting up · step 4 of 20 (0:03)"), or the
screen's name when nothing runs, or how a flash or setup ended when it ended while locked, and how to unlock. A few
seconds (5 s) after locking, and after each key since, the screen goes dark: on the LCD the backlight off where
meshflash may switch it (else the screen drawn black), in the CLI app a blank screen. Unlocking is the home screen's:
any key wakes the screen, then Tab, then Enter. Nothing locks by itself. Holding Esc 3 s still ends meshflash, locked or
not, on the LCD and in the CLI app alike: the launcher does that, and meshflash can't stop it; a flash or setup under
way is stopped the safe way and `screen.log` says what the board is left with. The CardputerZero's own lock is the home
screen's ("Hold TAB on the HOME screen…", its guide's section 4), which can't act while an app started from it runs, so
meshflash has its own.

```
meshflash · locked                      87% …



            Flashing · 42% (0:38)




Tab then Enter to unlock
```

## 5. Keys

Outside text boxes every action is one plain key (S18), but one: holding Tab 3 s locks the screen (4), a held key the
owner asked for, departing from S18. A Tab press acts once as Tab before the lock (in a Setup text box or pick list,
it keeps it and opens the next row; with a question open, it answers no, and it closes Help), and a held Tab never
acts as more Tabs, before the lock or after it. In the CLI app, which never says when a key is let go, a Tab less than
0.5 s after the previous Tab counts as the held Tab repeating, so two Tabs pressed that quickly act as one there (11).
`j`/`k` and the arrows (Fn+F/Fn+X) always move; a tap on Esc goes back (on Board, it quits); `?` is Help; Ctrl+C quits
from anywhere, asking `y` first while flashing; a setup being sent is stopped where it is, with no question. A held `y`
answers nothing (4, Confirm). In the CLI app a held Esc's repeats do nothing (the owner's answer, 11): an Esc less than
0.5 s after the previous Esc, no key between, is dropped, so holding Esc for the launcher's quit never walks back
through the screens first (two Escs pressed that quickly act as one there; on the LCD every press counts). Locked, no
key does anything but the unlock.

| Screen | Keys |
| --- | --- |
| Board | `enter` choose this port's board, `u` update catalog, `s` set up without flashing, `c` saved files, `r` rescan |
| Choose board | `enter` pick, `f` find |
| Role | `enter` pick |
| Version | `enter` next, `n` notes, `d` download only |
| Notes | `j`/`k` two lines, Space/`b` page |
| Confirm | `u` update, `w` full wipe (ESP32), `y` flash (downloads first when not saved) |
| Download | `esc` stop and go back (at once, no question) |
| Flashing | `esc` stop (asks `y`) |
| Result | `s` set up, `r` retry, `j`/`k` a paragraph, Space/`b` a page, `enter` or `esc` back to Board |
| Setup | Tab/`j`/`k` field (Tab wraps; `j`/`k` stop at the ends; Shift+Tab back), `enter` edit, `p` preset list, `y` send; in an open text box or pick list, Tab keeps it and opens the next row (from the last row it keeps and closes; Shift+Tab the row before; a row whose box or list can't hold its value stops the walk, closed; Esc cancels); sending: `esc` stop (asks `y`); failed: `r` back to the form; done: `enter` or `esc` back to Board |
| Update catalog | `esc` stop while fetching, `u` try again, `enter` back |
| Saved files | `x` delete (asks `y`) |
| Help | `j`/`k` two lines, Space/`b` page, any other key back |
| Text box | Enter keep, Backspace (S21); in Setup, Esc cancels and Tab keeps it and opens the next row (Setup, above); in Choose board's find box, Esc clears the filter and Tab does nothing |
| Any screen | Tab held 3 s: lock |
| Locked | any key wakes the screen; then Tab, then Enter unlocks; nothing else |

## 6. Flashing rules

### 6.1 Always

- Match the file to the chip before anything touches the board: ESP32 entries take only `.bin` files whose image
  header names the chip; nRF52 entries take only `.zip` DFU packages with an `application` and nothing else. Refuse
  anything else and say why.
- Check the saved file's SHA-256 against the one recorded at download, every time.
- Never write the catalog's `erase` or `bootloader` files; never offer "Erase Flash" or "Enter DFU mode" on their own.
- Match the board picked to the port's chip family (ESP32 or nRF52) before anything touches it; a board that doesn't
  fit is refused with nothing sent.
- Warn and ask under 30% battery, and when the battery can't be read (on the CardputerZero, told by
  `/proc/device-tree/model` naming "Raspberry Pi Compute Module 0" (the owner's answer, 11; to confirm on the device
  in Phase 3), a fuel gauge that isn't listed counts as one that can't be read; only a PC has no battery).
- Open every port for probing, flashing and setup exclusively (TIOCEXCL), so nothing else can open it mid-flash.
- Follow the board across resets by its USB serial number or USB path, never by tty name alone.
- Leave a tty touched at 1200 baud at 115200 when the flash ends, however it ends, so the next program to open and
  close it doesn't reboot the board into its bootloader. A stop during the touch, before DTR drops, sets the line
  back to 115200 first, so nothing reaches the board; after it, the stop is seen within 250 ms and the board is said
  to be in its bootloader. Every port is closed however a flash or setup ends, a crash in it included.
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
  ("nRF Serial"). A board already in its bootloader (double-pressed reset) is used without a touch, if meshflash saw
  its bootloader appear in this run; one already there when meshflash started may hold a session a stopped flash
  left, so its flash first asks for a double press of reset.
- Never send START into a bootloader session that already had one (after a failure past START, a stop, a quit): a
  retry asks for reset pressed twice quickly and waits up to 60 s for the bootloader to go and come back (a fresh
  session), asking again if it didn't see it. Only a go-and-come-back seen during that wait counts: a board on its
  battery that is unplugged and plugged in again keeps its session (OTAFIX has no exit on losing USB power), so the
  Board screen keeps saying why until then. Nothing about it is saved across runs.
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
| `~/.config/meshflash/config.json` | the last radio settings sent (frequency, bandwidth, SF, CR, TX power, path hash mode; never a password; the TX power once its `set tx` was answered, until then the node's own), and the catalog board picked for each USB serial number (serial → board name, nothing else; nothing for a board with no useful serial number). Nothing about a flash that was stopped or cut short |
| `~/.config/meshflash/drafts.json` | an unsent setup form per board, without passwords (S7): kept by the board's USB serial number, or, for a board with no useful one (a CH340, a CP210x saying "0001"), by its USB socket and identity ("usb 1-1.2 10c4:ea60"); the rows the owner emptied are kept as emptied |
| `~/.cache/meshflash/catalog/` | `config.json`, `config-zephcore.json` and each `releases-<key>.json`, as fetched, with the time |
| `~/.cache/meshflash/files/` | each downloaded file under its catalog name, and `index.json`: name, URL, size, SHA-256, when |
| `~/.cache/meshflash/screen.log` | what went wrong, started afresh each run, in the CLI app too (S5); after a quit, an Esc hold or SIGTERM that stopped a flash or setup, what the board was sent and is left with and how to recover or finish; after a crash or a closed terminal mid-flash, which lets the flash finish with no screen (up to about 3 minutes), how it ended. Never a password |

The formats of `config.json` and `drafts.json` are `internal/store/types.go`'s, each written as one compact JSON line,
0600 in a 0700 folder (a wider folder is narrowed on the next save), to a temporary file that is flushed and renamed
into place, the folder flushed after. Nothing is written until something changes: reading, forgetting what was never
remembered and deleting a missing draft write no file. A damaged `config.json` (cut short, not JSON, the wrong shape)
reads as empty and the next save replaces it. One that can't be read at all (its permissions) is never written over:
meshflash starts without the remembered boards and radio, with a line in `screen.log`, and until it is fixed a board
picked or a radio sent isn't remembered, each time with a line in `screen.log`. A damaged or unreadable `drafts.json`
reads as no drafts, and the next draft saved replaces it; nothing is deleted from one that can't be read (the draft
would come back once it can). A draft keeps only the rows that differ from the form's defaults, and the rows emptied (`cleared`); a form
back at its defaults has no draft, and a setup that worked deletes it. It is saved when Esc leaves the form, when `y`
sends it, and at a quit or SIGTERM; a text box Esc has just cancelled keeps its text in the draft for 3.5 s (an Esc
hold's first tap, then its SIGTERM). The radio is saved whenever a setup's node took `set radio`, even if the setup
then failed or meshflash quit before it ended.

`screen.log` gets one line for each flash, setup, download or catalog update that failed or was stopped (a download
the owner stopped excepted): which step and why, written once, by the backend, whether or not a screen saw the end;
a line from the screens when a draft or the radio couldn't be saved; at a quit that stops a flash or setup, the
screens' line saying where it was and what stopping there leaves, then, once it has ended, how it ended (its title,
what it did, "Left with: …" and "What to do: …"). After a crash or a closed terminal a flash or setup is left to
finish with no screen (up to about 3 minutes) and its end written the same way, unless a SIGTERM (the launcher's, at
the end of an Esc hold) or a SIGINT (Ctrl+C in the CLI app's terminal, cooked again after a crash) comes first: it is
then stopped the safe way at once; a download then is stopped at once. If drawing on the LCD fails, the last picture is
replaced by one saying so (what was under way goes on, `screen.log` says how it ends, holding Esc quits).

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

Go 1.25, Bubble Tea v1, module `meshflash`. From the kit: `internal/screen` copied whole, then changed in four places
(its package doc lists them: the `--screen-test` page names meshflash and shows its glyphs; the screen lock's Tab hold;
the backlight while the lock is dark; a panic while drawing replaces the last picture rather than end the program); `internal/app` and `internal/store` in shape; `internal/ui/text.go`,
`editor.go` and the test driver. New:

| Package | What |
| --- | --- |
| `internal/catalog` | the catalog and releases, decoded in key order; versions resolved as `js/catalog.js` does; downloads with SHA-256; the saved copies |
| `internal/serialport` | Linux tty: raw mode, baud, DTR/RTS, exclusive open; an interface with a fake for tests |
| `internal/usbid` | sysfs scan and the USB ID table; candidates for a port |
| `internal/esp` | SLIP, ROM and stub commands, reset sequences, compressed write, MD5 |
| `internal/nrfdfu` | the DFU ZIP and init packet, HCI/SLIP frames, the 1200-baud touch, the transfer |
| `internal/nodecli` | the MeshCore serial CLI: `ver`, setup, read-back, version gates |
| `internal/flash` | the orchestration the screens ask: scan, probe, catalog, downloads, prepare, one flash from start to end as steps (with what state a failure leaves the board in), read a node, setup; the only Phase 2 package doing serial I/O |
| `internal/flash/fakeworld` | a fake world of boards, flasher sites, battery and clock, for the end-to-end tests and the demo build |
| `internal/store` | `config.json` and `drafts.json` (8) |
| `internal/ui` | the screens and the screen lock (no network or serial code in it) |
| `internal/app`, `cmd/meshflash` | the flags, the wiring, `screen.log`, the shutdown |
| `reference/` | the Node harnesses that run the web flasher's own JavaScript against fake ports, and their recordings |

The demo build (`go build -tags demo`, `make demo`) is a separate binary, `meshflash-demo`, with its own folders
(`~/.config/meshflash-demo`, `~/.cache/meshflash-demo`), a fake world and the committed catalog, for trying the
screens on a PC with no board and no network (`make run-small` runs it at 45×10): a Heltec v4 running repeater v1.17.1
named “Hilltop 2” and a RAK 4631 running companion firmware, battery 87%, the clock synchronised, files of a real size
fetched at about 256 KB/s and flashes paced like a real board's (an ESP32 update about 30 s). It refuses `--screen`
and `--screen-test` unless `--framebuffer` names a regular file, so it never draws on the LCD or switches its
backlight. The release binary `dist/meshflash` links no fake and no test code (`make test` checks for the fake world,
`testing` and `net/http/httptest`). `make icon` draws the icon.

The flags are the kit's (`--screen`, `--screen-test`, `--font`, `--framebuffer`, `--framebuffer-sys`, `--keyboard`,
`--version`) and `--backlight-sys` (a fake backlight folder, for testing a run into a file); an argument that isn't a
flag ("meshflash screen") is refused with the usage and exit status 2. `screen.log` is started afresh on every run,
0600 even over an older 0644 one, its first line "<name> <version> started <time>"; `--version` opens nothing.

### 9.2 Install

`make build-arm64` builds `dist/meshflash` (static, stripped, linux/arm64). `make deploy HOST=CardPuter` copies it to
`~/.local/bin/meshflash` under a temporary name and renames it, so a meshflash that is running isn't overwritten,
puts `~/.local/bin` on the CLI app's `PATH` in `~/.bashrc` if it isn't there, and ends by printing the installed
`--version` (no `sudo`). `make install-desktop
HOST=CardPuter` installs the home-screen entry "MeshFlash" (`Exec=/home/void/.local/bin/meshflash --screen`) and its
100×100 icon `meshflash_100.png` (the owner runs it: `sudo`): a green LoRa board with its antenna and a lightning bolt
on its chip, on a teal tile (`packaging/make_icon.py`). The icon is drawn by code and shown to the owner first
(S33); the owner approved `packaging/meshflash_100.png` on 25 Nov 2026 (11).

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
- Goldens of every screen at 53×14, 45×10 and 40×10 through the LCD parser (S10, S28), the lock page's too; race
  tests; `gofmt`; arm64 vet (S31).
- The screens' tests run on the test driver's own clock, timers and Job waits, and `internal/screen`'s held keys on a
  virtual clock: no real sleeps, but the kit's SIGTERM test's 200 ms in a child process and the copied
  `internal/screen` tests of the kit's own. No test touches a real backlight: a fake sysfs folder or none.
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

The Phase 2 plan's questions (`docs/phase2-design.md` section 16), answered 25 Nov 2026:

| Choice | Answer |
| --- | --- |
| The node's name on the Board screen (1) | **shown**: read with read-only commands right after a repeater or room server answers, never the guest password |
| "saved" on the Version list for an ESP32 version (2) | **say which**: "update saved · 361 KB", "wipe saved · 1.3 MB", "both saved" |
| `d` on an ESP32 version (3) | **both files** |
| `r` after a failed flash (4) | **back to Confirm**, board and version kept; on an ESP32 the kind cleared (`u` or `w` again, then `y`); an nRF52 straight to `y` |
| A held `y` (5) | **ignored**: a `y` less than 0.5 s after the previous `y` answers nothing and the question stays, for every `y` question (Confirm, battery, SoftDevice, Setup) |
| TX power and path hash defaults (6) | TX power **the messenger's, else the last sent, else the node's**, capped at the board's limit with a note; path hash **the preset's, else the last sent, else the node's** (which preset's: below, from the Phase 2 report) |
| nRF52 firmware meshflash can't ask its version (7) | **"Flashed"** when the bootloader took the image and left; Result says whether the board came back on USB and that meshflash can't ask this firmware its version |
| ESP32-C6 boards (8) | **listed grey**, marked "ESP32-C6", refused before any download |
| Esc held or the launcher's SIGTERM during a flash (9) | **stop the flash the safe way at once**, write the board's state and how to recover it to `screen.log`, exit; nothing new in `config.json` |
| The `--screen-test` page (10) | **names meshflash and shows every glyph it uses**, a departure from the kit's copy |
| The font for the PC run into a file (11) | **a stand-in 6×12 bitmap font** made in the scratchpad from one fetched through the Go module proxy (never in `go.mod`), so PC frames are 53×14; the real Terminus first in Phase 3 |
| Selected row and headings (12) | **`▸` on the selected row**, group headings in reverse video |
| Drafts for boards with no useful USB serial (13) | **kept by USB socket and identity** ("usb 1-1.2 10c4:ea60"); the form says "Restored what was typed for the board on this USB socket." |
| The other screen and key details (14) | **all ten approved** as written |
| `j`/`k` in Notes, Help and Result (15) | **two lines** (kept for Notes and Help; Result and Setup's end refined in the Phase 2 report's answers below) |
| Ctrl+C while a setup is being sent (16) | **quit at once, without asking** (not the recommendation): the setup stopped the safe way, `screen.log` saying what the node was sent, what state it is in and how to finish; Esc while sending still asks `y` |
| Retrying an nRF52 flash that stopped part-way (17) | **no Phase 1 change**: Result says "Press r; meshflash then asks you to press reset twice quickly."; after `r` and `y`, Flashing asks for the double press and waits (asking again if it wasn't seen) |
| An nRF52 already in its bootloader at start (18) | **fresh only if meshflash saw it appear in this run**; otherwise a double reset press is asked for when flashing it |
| A crash, or the CLI app's terminal closing, mid-flash (19) | **let the flash finish** with no screen (up to about 3 minutes), how it ended written to `screen.log` |
| nRF52 boards whose SoftDevice the catalog doesn't name (20) | **Confirm says so and asks a second `y`**, as for a low battery |
| A screen lock (asked for with 9) | **hold Tab 3 s to lock**, on every screen, as the CardputerZero's home screen does; locked, every key is ignored but the unlock |
| The lock's look | **dark a few seconds after locking** (and after each wake); any key lights a lock page: the status line, what is running ("Flashing · 42%", "Setting up · step 4 of 20", or the screen's name) and "Tab then Enter to unlock"; the backlight off if meshflash may (checked in Phase 3), else drawn black |
| Unlocking | **as the home screen**: any key wakes the screen, then Tab, then Enter |
| Auto-lock | **none**: it locks only on a Tab hold |

The Phase 2 report's questions, answered 25 Nov 2026:

| Choice | Answer |
| --- | --- |
| Scrolling Result and Setup's end (a refinement of 15) | **a paragraph at a time** with `j`/`k`, Space/`b` a page, so no sentence of the Summary is cut; Notes and Help keep the two-line step |
| A role's notices | **pinned above Role's list, in normal text**, on Role only (not repeated on Confirm) |
| A held Esc in the CLI app | **dropped as a held Tab's is**: an Esc less than 0.5 s after the previous Esc (no key between) does nothing |
| A stopped setup | **waits up to 1 s** (`nodecli.StopGrace`) for the node's answer to the command already written, so it says truly what the node took |
| Tab in a Setup text box or pick list | **keeps it and opens the next row**, a text box or a pick list alike, so Tab walks through the form; from the last row it keeps and closes, the focus staying there; Shift+Tab the same backwards, closing on Name; a row that can't open is passed over |
| Which preset gives the path hash default | **a preset's only when every preset with that radio agrees**; otherwise the last one sent, then the node's own |
| How meshflash knows it is on the CardputerZero | **`/proc/device-tree/model` naming "Raspberry Pi Compute Module 0"**, to be confirmed on the device in Phase 3 |
| A long file name on Download | **on one line, cut with "…"**, its bytes on the next |
| The other Phase 2 details | **approved as built**: a draft restored by serial says "Restored what was typed for this board."; grey hints under an open text box; a blank latitude, longitude or local advert shows a grey "not sent", and the local advert is typed as "off" or minutes; Help lists the text box's keys under "In a text box:"; Esc on Download stops and goes back at once; the Board's hint with `s` is "enter choose  s set up  u update  ? help", `c` only in Help; the lock page shows what is under way with its time; after a crash a download is stopped at once; an argument that isn't a flag is refused with the usage and exit 2; an unreadable `config.json` is left alone and nothing is remembered until it is fixed; locking from the Setup form lets the hold's first Tab act; the demo's Download names its fake host |
| The icon as drawn | **approved**: `packaging/meshflash_100.png` (S33) |
| CLAUDE.md's Commands | **updated**: `make test`'s whole check, `make demo`, `make run-small`, `make icon`, `make build-arm64`, and the PC run into a file |

Decided without asking (technical defaults; the owner can overrule any):

- ESP32 at 115200 baud like the reference (native-USB boards such as the Heltec v4 go at USB speed anyway).
- ESP32: fix esptool-js 0.5.4's failure bugs (endless connect, unchecked status, dropped bytes); add MD5 verify; send
  the same informational reads as the reference.
- nRF52: resend an unacknowledged frame up to twice; after flashing, wait for the board to come back and check `ver`.
- The setup console sends CR line endings and clean values (as MeshCore's config tool does), not the reference
  console's CR LF.
- The screen lock's timings: the lock page stays lit 5 s after locking and after each key; its middle line shows how
  long the running step has run ("Flashing · 42% (0:38)"), as S17 asks of every wait. A held key is taken to repeat
  every 33 ms after 250 ms (Linux's default; measured in Phase 3). In the CLI app, which sends a held key again and
  again and never its release, a Tab less than 0.5 s after the previous Tab is the held Tab repeating, so two Tabs
  pressed less than 0.5 s apart act as one there (on the Setup form the second moves nothing; on the lock page, Tab
  pressed that quickly after the Tab that woke it doesn't arm the unlock). The same 0.5 s marks a held `y` (5).
- The Tab press that starts a hold is an ordinary press: an open question takes it as "no" (the safe answer) and Help
  closes, before the lock.
- Setup's Tab walk stops, the form closed, on a row whose text box or pick list can't hold its value as it is (a
  node's name over 23 bytes: the node keeps 31; a bandwidth not in the list, which would open on the nearest), rather
  than opening it for the next Tab to keep changed: the row's value and its problem stay until it is opened (4 Setup).

## 12. Phases

Each phase ends with a report and a stop.

1. **Protocols, against fakes.** Catalog resolution (golden from `js/catalog.js` in Node), the serial port layer, USB
   identification, ESP32 (golden recordings from `lib/esp32.js` in Node), nRF52 DFU (golden recordings from
   `lib/dfu.js`), the setup CLI against a fake node. No screens yet.
2. **Screens, against fakes.** Every screen with goldens at the three sizes; the whole flow end to end against a fake
   board and `httptest`; the icon (shown to the owner first) and the home-screen entry; a run with the screen drawn
   into a file on the PC.
3. **The device, with the owner.** The owner runs the commands: `dialout`, `make deploy`, a run on the device drawing
   into a file with keys from a FIFO (S30), memory (S9), what `/proc/device-tree/model` says (6.1); then, with the
   owner watching, flashing and setting up a real board; the hand-over checklist (`docs/standards.md` in the kit).

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
- 25 Nov 2026: the owner's answers to the Phase 2 plan's twenty questions (11), all as recommended but Ctrl+C while a
  setup is being sent, which quits at once without asking (4 Setup, 5). Written in: the Board screen shows the node's
  name (4); which ESP32 file is saved, and `d` fetching both (4 Version); `r` after a failed flash (4 Result); a held
  `y` answers nothing (4 Confirm, 5); the Setup form's TX power and path hash defaults (4 Setup); "Flashed" for nRF52
  firmware with no console (4 Result); a second `y` for an nRF52 whose SoftDevice the catalog doesn't name (4
  Confirm); the nRF52 retry and an nRF52 already in its bootloader at start (6.3); drafts kept by USB socket for a
  board with no useful serial, and nothing saved about a stopped flash (8); `j`/`k` two lines (5); a crash or a closed
  terminal mid-flash lets the flash finish, logged (4 Flashing, 8).
- 25 Nov 2026: the screen lock, the owner's request: Tab held 3 s locks; dark a few seconds later; any key, then Tab,
  then Enter unlocks; no auto-lock; the launcher's Esc hold still ends meshflash (2, 3, 4, 5, 10, 11). A held key is a
  departure from S18, which the owner asked for (5). `internal/screen` departs from the kit's copy for it and for the
  test page (9.1).
- 25 Nov 2026, from the Phase 2 plan (`docs/phase2-design.md` 14), agreed with the owner's answer to its question 14:
  Enter on Version goes to Confirm and `n` shows the notes (4 said Enter showed them too); the status line's mark is
  defined, and the USB list is read every second on every screen (2, 4); Board doesn't open a bootloader, says when
  another program holds a port, offers `s` only on a repeater or room server, and names a bridge "CP210x" (4); Choose
  board's "All boards" and its grey boards (4); Role's title and subtitle, and its notices above the list (4); Confirm
  shows again after a download, asks when the battery can't be read, and says when the board is unplugged (4); "Stop
  now?" says what stopping leaves (4 Flashing); Result's Esc and pinned state, and the new mock-up (4); Setup's Esc
  while sending, `r` after a failure, `••••` for both passwords and the ASCII "-9 dBm" (4); Update catalog's Esc while
  fetching (4); long text at 40×10 folds or scrolls (4); Help's footer (4); exclusive opens, the family check and the
  tty back at 115200 after a touch (6.1); `screen.log` every run and the formats of `config.json` and `drafts.json`
  (8); the new packages and the demo build (9.1); the tests' clocks (10); sizes shown once known (2).
- 25 Nov 2026, after a check of the lock against the code (the code wins): the launcher's Esc hold ends meshflash in
  the CLI app too, so Help says so in both modes (4 Help, Screen lock); the lock page shows how long the running step
  has run (4, 11); a held Tab's repeats never act after the lock either, and the Tab that starts a hold answers an open
  question no (5, 11); in the CLI app two Tabs less than 0.5 s apart act as one, and the lock's timings (5, 11); a
  backlight under `/sys` is switched only when drawing on `/dev/fb0` (2).
- 25 Nov 2026, Phase 2 integration (the code wins): the screens' details the builders' code settles, written into 4,
  5, 8 and 9: Board's turn-by-turn asking, its three bootloader reasons (the third, "already in its bootloader when
  meshflash started", from owner answer 18), the catalog line while it is read, a failed USB scan, and the hint
  without `c` when `s` is offered; Choose board's headings, grey tags kept whole, arrows in the find box and the kept
  "Find:" line; Confirm waiting 0.5 s and for the new check after a download, a failed download's reason, `u`/`w`
  dropping a `y` whose battery reading is out, `u` on an nRF52, and what goes first at 40×10; Download as a screen of
  its own, Esc there stopping at once without asking; Flashing's and Result's words; lists, notices and the Setup form
  scrolling by whole items; Update catalog keeping a catalog that arrived after a late Esc; Saved files' rows; the
  Setup form's states, rows, notes, hints, keys (Tab in a text box keeps it and moves on with the box closed; Tab
  wraps, `j`/`k` stop), presets, the path hash default only when every preset with that radio agrees, `r` after a
  failure, the draft notes and Help's text-box keys (4, 5); how `config.json` and `drafts.json` are written, an
  unreadable `config.json` never written over and nothing remembered until it is fixed, a damaged or unreadable
  `drafts.json`, when a draft is kept, and what `screen.log` gets, each failure's line once and a download stopped at
  once after a crash (8); the demo build's world and pace, its refusal to draw anywhere but a file, the release
  binary's no-fake and no-test-code check, the flags (`--backlight-sys`; an argument that isn't a flag refused), and
  `make build-arm64` and `make deploy` (9). Owner questions on the ones the earlier answers don't settle are in the
  Phase 2 report; each is behind one function or constant so it is easy to change.
- 25 Nov 2026, after the Phase 2 check (the code wins, and where the check found the code wrong, the code was fixed
  first): Role's notices pinned above the list in normal text, whole ones left out at 40×10 (4 Role); Board's `s`
  note worded by the port's probe and gone once the answer comes, and a node that can't be set up said once, after
  what it runs (4 Board); the mock-up's hint with `s` (4 Board); download names as labels, long words broken after
  "-", "_" or "/" (4); Confirm's last drops at 40×10 (4 Confirm); Result's and Setup's end scrolling a paragraph at a
  time, the retry line scrolling when there is no room (4, 5); "Stopping once the erase ends" with no count, and the
  steps "Waiting for it on USB" and "Asking its version" (4 Flashing); the bootloader's "Stop now?" (4 Flashing); a
  stopped setup stopping between commands, writing nothing more and reading the answer to the command already sent,
  and the "Stop now?" after the reboot (4 Setup); "type both passwords again" for a room server (4 Setup); Update
  catalog's Esc note and "No catalog is saved yet." (4); Help's footer on where ports are opened (4 Help); a held
  Esc in the CLI app (5, 11); the touch's stop, every port closed after a crash, and a stale nRF52 session kept across
  a replug (6.1, 6.3); the battery on the Zero (6.1); the TX power remembered only once sent, a signal after a crash,
  and the LCD after a drawing panic (8); `internal/screen`'s fourth change (9.1).
- 25 Nov 2026: the owner's answers to the Phase 2 report (11). One changes the code: Tab in a Setup text box or pick
  list keeps it and opens the next row, a text box or a pick list alike, so Tab walks through the form; from the last
  row it keeps and closes, the focus staying there, Shift+Tab the same back to Name, and a row that can't open (the
  path hash before v1.14.0) is passed over without its note (4 Setup, 5). The rest record what was built as the
  owner's answers: Result and Setup's end a paragraph at a time, Notes and Help two lines; Role's notices on Role
  only; a held Esc dropped in the CLI app (moved from "Decided without asking"); a stopped setup waiting 1 s for the
  answer to the command already written; the path hash default from a preset only when every preset with that radio
  agrees (4 Setup's table now says so); the Zero told by `/proc/device-tree/model`, checked in Phase 3 (6.1, 12); a
  long file name on Download; the icon (9.2); CLAUDE.md's Commands; and the other details approved as built.
- 25 Nov 2026, the review of the Tab walk: a row whose text box or pick list can't hold its value as it is stops the
  walk there with the form closed, so Tab never changes a row that wasn't opened (4 Setup, 5; 11 "Decided without
  asking"); 5's "Text box" row spoke for Setup's boxes only and now says Choose board's find box clears on Esc and
  does nothing on Tab, as the code does; 11's record of the CLAUDE.md answer names `make test`'s whole check, as the
  CLAUDE.md change and design 16.1 do.
