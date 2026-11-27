# Phase 3: meshflash on the CardputerZero

The runbook for the device phase (SPEC.md 12). A Claude Code session on the T14 runs the git commands, `make test`
and the read-only `make device-*` targets; it runs `make deploy`, `make device-tools` and `make device-run`, which
write to the device without `sudo`, only after you say go for that step, with you there (SPEC.md 11, 27 Nov).
Everything with `sudo`, `ssh-copy-id`, anything on the LCD and any real board are yours, done by you. Before each
command it runs, Claude says what it does and what it touches.

Each `make device-*` target runs on the T14, from this repository, and reaches the device with your ssh alias
(`CardPuter`, or `make … HOST=<alias>`) in BatchMode: a key login, never a password prompt (`ssh-copy-id CardPuter`
once, if it isn't set up; yours to run). Each prints a short summary (Claude reads it; when you run one, paste it
into chat), keeps its full output under `dist/phase3/` (git-ignored), and names any PNG. The scripts are in
`scripts/device/` (Python 3.9+, standard library only; their docstrings say what each reads and writes).

What no script ever does: run `sudo`; draw on the LCD; open a serial port (the meshflash that `make device-run` starts
would ask a plugged-in board what it runs, so that run needs every board unplugged: step 5); switch the backlight;
read or write the real `~/.config/meshflash` or `~/.cache/meshflash` (only `make device-logs` reads them); print or
store a password; change any server (the only network use is the catalog update in step 5 and the reachability
check in step 2, both reads).

## 1. The T14 checkout

- **Who, where:** Claude, on the T14, in this repository, on the branch this work is on (`git status` names it).
- **Run:** `git pull`, then `make test`.
- **Touches:** the T14 only (`make test` builds and runs the tests: no network but Go fetching go1.25.4 once if the
  T14's Go is older, no serial port).
- **Result:** the last lines of `make test`.

## 2. The facts

- **Who, where:** Claude, on the T14.
- **Run:** `make device-check`
- **Touches:** reads only: on the T14 Python, Go, git and `dist/meshflash`; on the device (one ssh) the system, the
  device-tree model, groups, serial ports, battery, backlight permissions (tested, never written), fb0's size, the
  keyboards, the Terminus font, the home screen's folders, memory and disk, a deployed meshflash, and a HEAD request
  to flasher.meshcore.io, zephcore.meshcore.dev and github.com.
- **Result:** the summary (all of it is in `dist/phase3/check.log`).

## 3. Serial ports: the dialout group (SPEC 9.3)

- **Who, where:** you, on the device (`ssh CardPuter`, or the CLI app).
- **Run:** `sudo usermod -aG dialout void`, then `sudo reboot` (the launcher keeps the groups it started with, so a
  new login isn't enough).
- **Touches:** `/etc/group`, and the device restarts.
- **Then:** after the reboot, Claude runs `make device-check` again: "launcher … has dialout: yes".
- **27 Nov 2026:** not needed. Step 2 found `void` and the running launcher already in `dialout`.

## 4. Deploy

- **Who, where:** Claude, on the T14, after you say go.
- **Run:** `make deploy`
- **Touches:** builds `dist/meshflash`, copies it to the device's `~/.local/bin/meshflash` (no `sudo`), adds the
  `~/.local/bin` line to `~/.bashrc` if it isn't there, prints the installed `--version`.
- **Result:** the version line.

## 5. The scripted run into a file (S30)

- **Who, where:** Claude, on the T14, after you say go and that every board is unplugged. **Unplug every board from
  the device first, and plug none in until the run has ended**: the run refuses to start while any `/dev/ttyACM*` or
  `/dev/ttyUSB*` exists, and stops within a moment if one appears, but meshflash looks for boards every second and
  asks a new one what it runs straight away, so it may already have opened a board plugged in mid-run.
- **Run:** `make device-run` (about a minute and a half).
- **Touches:** on the device, `/tmp/meshflash-s30` only (removed at the end, whatever happens), where the deployed
  meshflash draws into a file with keys from a FIFO and a throwaway HOME; it presses `u`, so the device fetches the
  catalog from flasher.meshcore.io and zephcore.meshcore.dev (about 1.5 MB, into that folder). It builds
  `dist/meshflash` first and refuses a deployed binary that isn't the same (the checkout changed since `make deploy`:
  run it again). It never touches the LCD or the backlight, and with every board unplugged, no serial port. Ctrl+C
  stops it; it then removes the folder on the device.
- **Result:** `dist/phase3/run-<time>/summary.txt` (the summary without the progress lines: its last line names it),
  and `frames.png` from the same folder (every frame on one sheet).

## 6. The home-screen entry

- **Who, where:** you, on the T14; it asks for the device's `sudo` password.
- **Run:** `make install-desktop`
- **Touches:** `/usr/share/APPLaunch/applications/meshflash.desktop` and the icon in
  `/usr/share/APPLaunch/share/images/`.
- **Paste:** "MeshFlash is on the home screen."

## 7. On the LCD, with you watching

No board plugged in yet. This is the first time meshflash draws on the real screen and switches the real backlight.

- **Open MeshFlash** from the home screen. You should see the Board ("Plug a LoRa board into the USB-A port.") with
  the battery on the status line.
- **While it is up**, Claude runs `make device-shot` on the T14 (reads `/dev/fb0` only) and reads its text and PNG.
- **The lock:** hold Tab 3 s: the lock page ("meshflash · locked", "Board", "Tab then Enter to unlock"). Wait: about
  5 s later the screen goes dark. Press any key: it lights again. Tab, then Enter: unlocked. Tell me: did the
  backlight go off, or was the screen only drawn black? Did waking and unlocking work as described?
- **Quit:** hold Esc 3 s: meshflash ends and the home screen comes back, drawn cleanly. Tell me what showed.
- Then Claude runs `make device-logs` on the T14: the run's `screen.log` says whether the backlight could be switched.

## 8. The CLI app

- **On the T14:** Claude runs `make device-tools` after you say go (copies `keyrate.py` to `~/meshflash-check/` on
  the device and checks the copy; no `sudo`).
- **In the CLI app:** type `meshflash`. The Board at 45×10. Try `?`, `j`/`k`, Esc (on the Board it quits), and a
  3 s Tab hold (it should lock as on the LCD). Tell me what you saw.
- **In the CLI app:** `python3 ~/meshflash-check/keyrate.py`. Hold Tab about 4 s, then Esc about 2 s (not 3: at 3 s
  the launcher ends the program), then y about 2 s. It shows when each key repeated and what meshflash makes of it,
  and saves `~/meshflash-check/keyrate.txt`.
- **On the T14:** Claude runs `make device-logs`, which brings `keyrate.txt` back.

## 9. A real board, with you watching

- **Which board first is your call** (the Heltec v4, an ESP32-S3; the RAK 4631, a Xiao nRF52 or the Wio Tracker L1
  Pro, nRF52840). Plug it into the USB-A port.
- **Flash it:** open MeshFlash (from the home screen, or `meshflash` in the CLI app): Board, Choose board, Role,
  Version, Confirm, `y`. For a repeater or room server, then Setup (`s` on Result). Tell me what each screen showed
  and anything that surprised you, and say when a screen is worth keeping: Claude runs `make device-shot`.
- **Before quitting it:** Claude runs `make device-mem` on the T14: its peak memory after a real flash and setup.
- **After quitting:** Claude runs `make device-logs`. These files never hold a password: the admin password is sent
  to the node and kept nowhere.
- **Touches:** the board (written only after your `y`), the real `~/.config/meshflash` and `~/.cache/meshflash`
  (the catalog, the downloaded files, `screen.log`), and the network for the catalog and the firmware.

## What Phase 3 must settle

| Check | From |
| --- | --- |
| Peak memory (VmHWM) under 40 MB (S9) | step 5's run (no board), and `make device-mem` after a real flash and setup (step 9) |
| `/proc/device-tree/model` names "Raspberry Pi Compute Module 0", so meshflash knows the Zero (and its battery) | step 2: the text and its bytes (it ends in a NUL) |
| Every character the screens use draws in Terminus (or meshflash's stand-ins for ○ ✎ ✓), none as "?" | step 5's test page check |
| Whether void may write the backlight, and whether the lock's dark screen switches it off or only draws black | step 2 (permissions), step 7 (seen), `screen.log` |
| How the CLI app repeats a held key, against meshflash's 0.5 s rules for a held Tab (lock), Esc and y | step 8, `keyrate.py` |
| An ESP32-S3 on USB-Serial-JTAG (the Heltec v4) keeping its port across the reset into its ROM, and flashing | step 9 |
| How long a node takes to answer after a full wipe (FirstBootWait, 20 s now) | step 9, a full wipe, `screen.log` |
| ZephCore's reply to `ver`, if you have a ZephCore board | step 9 |
| The catalog update from the device, the deployed binary the same as `dist/meshflash` (S3, S34), the screen left blank on exit (S8), the home-screen entry opening it (S32) | steps 2, 5, 6, 7 |
