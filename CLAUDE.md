# meshflash

A MeshCore flasher for the owner's M5Stack CardputerZero (Raspberry Pi CM0, arm64 Debian 13, 320×170 screen,
46-key keyboard). `SPEC.md` is the plan; read it first. `docs/meshcore-flashing.md` is the research behind every
protocol detail, with a source for each fact. The CardputerZero app kit (`~/Projects/cardputer-app-kit` on the owner's
PC; `/home/user/cardputer-app-kit` in the cloud session) holds the standards this app meets (`docs/standards.md`,
S1–S38), the guide (`docs/making-apps.md`) and the device facts (`docs/device.md`).

## Ask first

- The owner wants clarifying questions, not guesses: multiple choice, the recommended option first, before any choice
  that changes what they'll see, the keys, what gets saved, or anything hard to undo.
- Where `SPEC.md` and the code disagree, the code wins: fix `SPEC.md`, add a line under "Changes to this spec", and
  say so in the report.
- Work one phase at a time (`SPEC.md` §12). End each phase with a short report and stop.

## Commands

```bash
export GOTOOLCHAIN=go1.25.4   # if the local Go is older
make test          # gofmt check, go vet (host, linux/arm64, -tags demo), go test -race ./..., and the release binary
                   # linking no fake world and no test code — no network, no serial ports
make reference     # re-record the web flasher's JavaScript in Node (needs Node 22 and a flasher checkout; see reference/)
make demo          # dist/meshflash-demo: the screens over a fake world (its own folders, no network, no serial ports)
make run-small     # the demo build in a 45×10 terminal (the CLI app's grid); never the real binary on a PC
make icon          # redraw packaging/meshflash_100.png (needs Pillow); show the owner before committing it (S33)
make build-arm64   # dist/meshflash for the device; make deploy and make install-desktop are run with the owner (Phase 3)
make device-check  # Phase 3 (docs/phase3.md), from the T14 over ssh (HOST=CardPuter): read-only facts from both
make device-run    # the deployed meshflash on the device drawing into a file, keys from a FIFO (S30): only on the
                   # owner's go, with every board unplugged
make device-shot   # a picture of the real LCD and its text, while MeshFlash is open (reads /dev/fb0 only)
make device-mem    # VmHWM and VmRSS of a running meshflash (S9), just before quitting it
make device-logs   # screen.log, config.json, drafts.json and keyrate.txt from the device
make device-tools  # copies keyrate.py to ~/meshflash-check/ on the device, for the CLI app (on the owner's go)
```

The PC run into a file (no LCD): `dist/meshflash-demo --screen --framebuffer <file> --framebuffer-sys <folder with
virtual_size 320,170 and bits_per_pixel 16> --keyboard <a FIFO of evdev key events> --font <a 6×12 PSF>`, with `HOME`
set to a throwaway folder. The demo refuses any `--framebuffer` that isn't a regular file.

## Hard rules

- **Never draw on the real LCD unless the owner is watching**: test `--screen` with `--framebuffer <a file>`.
- **Never touch a real board** except in the device phase with the owner watching.
- **Anything needing sudo on the device is the owner's to run.**
- **Tests never touch the network or a real serial port**: fakes (`serialport.Fake`, `httptest`) and recorded
  fixtures only, in virtual time (`serialport.VirtualClock`), never real sleeps.
- **Never store or log a password.** The admin password is sent to the node and dropped; it never reaches
  `screen.log`, a draft, a test golden or a progress message.
- **Never write anything the catalog lists besides the firmware**: no nRF52 `erase` formatter, no `bootloader` file,
  no custom file. Match every file to the chip before touching the board.
- **Ask before changing any server the app talks to** (it talks to flasher.meshcore.io, zephcore.meshcore.dev and
  GitHub's MeshCore releases; it changes none of them).
- The flashing is Go in this app: one static binary (`CGO_ENABLED=0`), no esptool, nrfutil or pip on the device.
- The web flasher's JavaScript (flasher.meshcore.dev @ 45a5257) is the reference: the Go code must send the same
  bytes, and `reference/` records them. Departures are listed in `SPEC.md` §6 and in each package's doc comment.

## Code

- Go 1.25, module `meshflash`, standard library plus `golang.org/x/sys` (and Bubble Tea from Phase 2).
- `internal/serialport` is the only code that touches a tty; everything else takes a `serialport.Port` and a
  `serialport.Clock`. `internal/ui` never does network or serial I/O itself.
- Comments say why. Ported code names where it came from and its licence (see `THIRD_PARTY_NOTICES`).
- Fixtures stay small: no real firmware in the repository. Tests that use the full-size samples in
  `/home/user/meshflash-research` run only when `MESHFLASH_SAMPLES` points there.

## Commits

One concern per commit, the reason in the message. Push the branch; no pull request. Update `CHANGELOG.md` for
anything the owner would notice and `README.md` for installing or using it.
