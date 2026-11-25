# Changelog

## 0.1.0 (unreleased)

- Phase 1: the protocols, tested against fakes: catalog, serial port, USB identification, ESP32 and nRF52 flashing,
  and repeater/room-server setup over the serial console. Nothing to run yet: the screens come in Phase 2.
- Phase 2: the screens, against a fake world. Board, Choose board, Role, Version, Notes, Confirm, Download,
  Flashing, Result, Setup, Update catalog, Saved files and Help, at 53×14 (the LCD), 45×10 and 40×10 (the CLI app);
  the whole flow tested end to end, flashing and setting up fake boards in virtual time, never touching a real one.
- A screen lock: hold Tab 3 s; any key, then Tab, then Enter unlocks; the screen goes dark meanwhile, and a flash or
  setup carries on. Ctrl+C while a setup is being sent quits at once, writing to `screen.log` what the node was left
  with; while flashing it asks first.
- What was typed on the Setup form and not sent is kept per board (never a password) and comes back when the form
  reopens; the last radio sent and the board picked for each USB serial number are remembered.
- `screen.log` in `~/.cache/meshflash`, started afresh each run: one line for each flash, setup, download or catalog
  update that failed, and after a quit, an Esc hold or a crash, what a board was left with and how to recover it.
- Installing: `make build-arm64`, `make deploy` (no `sudo`) and `make install-desktop` (the home-screen entry
  "MeshFlash" and its icon: a green LoRa board with its antenna and a lightning bolt, on a teal tile).
- A demo build for a PC, `make demo` and `make run-small`: the same screens over fake boards, with no network and no
  serial ports; it never draws on a real screen.
- `--screen-test` names meshflash and shows the characters its screens use; `--backlight-sys` (for testing); an
  argument that isn't a flag is refused with the usage.
- After the Phase 2 check: a setup stopped by Esc and `y`, Ctrl+C or the end of meshflash stops between commands,
  sending nothing more and waiting (1 s at most) for the answer to the command already sent, so it says truly whether
  the node took its new radio; the TX power is remembered only once the node took it. A stop during the nRF52
  1200-baud touch, while a retry waits for the double press, or while an ESP32-S3 restarts into its ROM says what the
  board is really left with (in its bootloader, or its app still erased), and a board unplugged and plugged in again
  after a flash stopped part-way still asks for the double press. After a crash with a flash still running, holding
  Esc (or Ctrl+C in the CLI app) stops it the safe way and `screen.log` says how it ended; the LCD no longer keeps a
  stale "Flashing" picture if drawing fails. On the CardputerZero a battery that isn't listed asks before flashing.
- Screens: Role keeps the selected role's warnings above the list, in normal text; Board's `s` says to wait while the
  board is still being asked; a download's file name has a line of its own, cut with "…" when it is too long; Result
  and Setup's end scroll a paragraph at a time, never cutting a sentence; Confirm keeps the file's check at 40×10;
  Update catalog no longer says a saved catalog stays when there is none, and says so when Esc stops it; steps under way say what they do ("Waiting for it on
  USB", "Asking its version"); in the CLI app a held Esc no longer walks back through every screen.
