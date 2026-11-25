# meshflash

A MeshCore flasher for the M5Stack CardputerZero. Plug a LoRa board into the Zero's USB-A port, pick the board, the
firmware role and the version from MeshCore's official flasher catalog, and meshflash flashes it, verifies it, and sets
up repeaters and room servers over their serial console. Made for the field: flash and configure a repeater on a
hilltop with no laptop.

Status: in development. Phase 1 (the flashing and setup protocols) and Phase 2 (the screens, tested end to end against
a fake world, and tried on a PC in a demo build) are done. It has not yet run on the Zero or touched a real board:
that is Phase 3, with the owner. `SPEC.md` is the plan; `docs/meshcore-flashing.md` is the research.

## Installing

On a PC with Go 1.25 (`export GOTOOLCHAIN=go1.25.4` if the local Go is older) and `ssh` access to the Zero as `void`
(the host `CardPuter`, or `HOST=…`):

```bash
make build-arm64                     # dist/meshflash: one static binary for the Zero
make deploy HOST=CardPuter           # copies it to ~/.local/bin/meshflash and prints its --version
make install-desktop HOST=CardPuter  # the home-screen entry "MeshFlash" and its icon (asks for the Zero's sudo password)
```

`make deploy` needs no `sudo`; it adds `~/.local/bin` to the CLI app's `PATH` in `~/.bashrc` if it isn't there, so
`meshflash` also runs in the CLI app. `make install-desktop` asks for the Zero's `sudo` password, so the owner runs
it.

Serial ports belong to the `dialout` group, which `void` isn't in. Run this once on the Zero (meshflash shows the
same command when it can't open a port):

```bash
sudo usermod -aG dialout void
sudo reboot
```

## Using it

Start it from the home screen ("MeshFlash", drawing on the LCD) or type `meshflash` in the CLI app. The screens go
Board → Choose board → Role → Version → Confirm → Flashing → Result, and from Result or the Board, Setup. Nothing is
written to a board until you press `y` on a screen that says what will happen; nothing is fetched from the network
until you press `u` (update the catalog), `d` (download) or `y` on a file not saved yet.

| Where | Keys |
| --- | --- |
| Everywhere | `j`/`k` or the arrows move, Enter picks, Esc goes back (on the Board it quits), `?` Help, Ctrl+C quits (it asks first while flashing) |
| Board | `u` update the catalog, `s` set up a repeater or room server without flashing, `c` saved files, `r` look again |
| Choose board | `f` find a board by name |
| Version | `n` release notes, `d` download only |
| Confirm | `u` update or `w` full wipe (ESP32), `y` flash |
| Download, Update catalog | Esc stops it and goes back, without asking; after a failed update `u` tries again |
| Flashing | Esc stops, after a `y` |
| Result | `r` try again from the saved file, `s` set it up |
| Setup | Tab or `j`/`k` move, Enter edits a row, `p` radio presets, `y` sends; in a row being edited, Tab keeps it and opens the next, so Tab walks through the form (on the last row it keeps it and closes, ready for `y`; Shift+Tab goes back); the admin password is never saved |
| Saved files | `x` deletes a file, after a `y` |

A held `y` answers nothing: release it and press again. In the CLI app a held Esc counts once, so two Escs pressed
less than half a second apart act as one there. Each screen's Help lists its keys and what meshflash reads and writes.

**Screen lock.** Hold Tab for 3 seconds on any screen to lock meshflash: every key is then ignored, and a flash, a
setup or a download carries on. The screen goes dark after a few seconds. To unlock, press any key to wake it, then
Tab, then Enter. Holding Esc for 3 seconds still ends meshflash, locked or not (the launcher does that); a flash or
setup under way is stopped the safe way and `~/.cache/meshflash/screen.log` says what the board is left with.

meshflash keeps its settings in `~/.config/meshflash` and its catalog, downloaded files and `screen.log` in
`~/.cache/meshflash`.

## Trying it on a PC

The demo build runs the same screens over a fake world (a Heltec v4 running a repeater and a RAK 4631, the catalog
built in, no network, no serial ports, its own folders `~/.config/meshflash-demo` and `~/.cache/meshflash-demo`):

```bash
make run-small   # the demo in a 45×10 corner of this terminal, as in the CLI app
make demo        # builds dist/meshflash-demo
```

The demo never draws on a real screen: with `--screen` it draws only into a file named by `--framebuffer`.

## For developers

`make test` is the whole check (gofmt, vet on the host, arm64 and the demo build, race tests, and that the release
binary links no fake and no test code). `CLAUDE.md` lists the commands and the rules; `docs/phase2-design.md` is how
the screens were built.

MIT licence; see `THIRD_PARTY_NOTICES` for what it carries from others.
