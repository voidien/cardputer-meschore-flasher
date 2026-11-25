# meshflash for the CardputerZero. CLAUDE.md says what each target is for.
#   make test             gofmt check, go vet (host, linux/arm64 and the demo build), race tests, and a check that the
#                         release binary links no fake and no test code: no network, no serial ports
#   make reference        re-record the web flasher's own JavaScript in Node (reference/README.md)
#   make build-arm64      the program for the device → dist/meshflash (static, stripped)
#   make deploy           copy it to the device as ~/.local/bin/meshflash (HOST=CardPuter by default)
#   make install-desktop  the home-screen entry "MeshFlash" and its icon (the device's sudo password: the owner runs it)
#   make demo             the demo build → dist/meshflash-demo: the screens over a fake world, for a PC
#   make run-small        the demo build in a 45×10 terminal on the PC (the CLI app's grid)
#   make icon             redraw packaging/meshflash_100.png (needs Pillow)
# Phase 3, on the device (docs/phase3.md): the owner runs these from the PC; each reaches HOST with ssh in BatchMode,
# needs no sudo, prints a summary to paste and keeps its output under dist/phase3/. make test runs none of them.
#   make device-check     read-only facts from the PC and the device, to paste first
#   make device-run       the deployed meshflash on the device, drawing into a file with keys from a FIFO (S30)
#   make device-shot      a picture of the real LCD and its text (reads /dev/fb0 only)
#   make device-mem       VmHWM and VmRSS of a running meshflash (S9), just before quitting it
#   make device-logs      screen.log, config.json, drafts.json and keyrate.txt from the device
#   make device-tools     copies keyrate.py to ~/meshflash-check/ on the device, for the CLI app

GO   ?= go
PYTHON ?= python3
export GOTOOLCHAIN ?= go1.25.4
HOST ?= CardPuter

.PHONY: test reference build-arm64 deploy install-desktop demo run-small icon
.PHONY: device-check device-run device-shot device-mem device-logs device-tools

# The last step: the release binary for the device links no fake world (internal/flash/fakeworld, the demo build's),
# so fake boards can never show on the device, and no test code (package testing, net/http/httptest). A go list that
# fails fails the target rather than pass the check.
test:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	$(GO) vet ./...
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) vet ./...
	$(GO) vet -tags demo ./...
	$(GO) test -race ./...
	$(GO) test -race -tags demo ./cmd/meshflash
	@deps=$$(GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) list -deps ./cmd/meshflash) || exit 1; \
	for p in meshflash/internal/flash/fakeworld testing net/http/httptest; do \
		if echo "$$deps" | grep -qx "$$p"; then echo "dist/meshflash would link $$p"; exit 1; fi; \
	done

reference:
	$(MAKE) -C reference

# Static, no cgo, stripped: one file to copy (S4).
build-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build ./...
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o dist/meshflash ./cmd/meshflash

# Installs as ~/.local/bin/meshflash, copied in under another name first so a meshflash that is running isn't
# overwritten (S34), and puts ~/.local/bin on the PATH of the CLI app's shell, which reads ~/.bashrc and not
# ~/.profile (the kit's docs/device.md), unless it is there already. No sudo. Ends with the installed --version (S3).
deploy: build-arm64
	ssh -o BatchMode=yes $(HOST) 'mkdir -p ~/.local/bin'
	scp -q -o BatchMode=yes dist/meshflash $(HOST):.local/bin/meshflash.new
	ssh -o BatchMode=yes $(HOST) 'cd ~/.local/bin && chmod 755 meshflash.new && mv meshflash.new meshflash'
	ssh -o BatchMode=yes $(HOST) 'grep -qs "HOME/.local/bin" ~/.bashrc || printf "\n%s\n%s\n" "# ~/.local/bin (CardputerZero apps)" "export PATH=\"\$$HOME/.local/bin:\$$PATH\"" >> ~/.bashrc'
	ssh -o BatchMode=yes $(HOST) '~/.local/bin/meshflash --version'

# The home-screen entry "MeshFlash" (packaging/meshflash.desktop.in, `meshflash --screen`), started as an app of its
# own drawing the LCD itself: the launcher stops drawing and acting on keys until it exits (the kit's docs/device.md,
# "Apps started from the home screen"). Also installs its icon (packaging/meshflash_100.png, drawn by
# packaging/make_icon.py) beside the launcher's own. APPLaunch reads only its own root-owned folders, so this asks for
# the device's sudo password (hence -t), in one sudo session: the owner runs it, after make deploy.
install-desktop:
	scp -q packaging/meshflash.desktop.in packaging/meshflash_100.png $(HOST):/tmp/
	ssh -t $(HOST) 'sudo install -m 644 /tmp/meshflash_100.png /usr/share/APPLaunch/share/images/ && sed "s|@BIN@|$$HOME/.local/bin/meshflash|" /tmp/meshflash.desktop.in | sudo tee /usr/share/APPLaunch/applications/meshflash.desktop >/dev/null && rm -f /tmp/meshflash.desktop.in /tmp/meshflash_100.png && echo "MeshFlash is on the home screen."'

# The same screens over a fake world (a Heltec v4 and a RAK 4631, the committed catalog), with folders of its own
# (~/.config/meshflash-demo): for a PC with no board and no network. With --screen it draws only into a file.
demo:
	$(GO) build -tags demo -o dist/meshflash-demo ./cmd/meshflash

# The demo build, never the real one, which on a PC would probe the PC's own serial ports and reach the real servers.
# Tells it its terminal is 45×10 (it draws in the top-left corner) and puts the size back afterwards.
run-small:
	@size=$$(stty size); stty rows 10 cols 45; $(GO) run -tags demo ./cmd/meshflash; stty rows $${size% *} cols $${size#* }

icon:
	python3 packaging/make_icon.py

# Phase 3 (docs/phase3.md, scripts/device/): thin calls of the scripts, which say what each reads and writes. The
# run needs dist/meshflash to compare with the deployed copy (it refuses one that differs: make deploy).
device-check:
	$(PYTHON) scripts/device/check.py --host $(HOST)

device-run: build-arm64
	$(PYTHON) scripts/device/screendrive.py --host $(HOST)

device-shot:
	$(PYTHON) scripts/device/shot.py --host $(HOST)

device-mem:
	$(PYTHON) scripts/device/mem.py --host $(HOST)

device-logs:
	$(PYTHON) scripts/device/logs.py --host $(HOST)

# keyrate.py runs in the CLI app on the device, not over ssh: it measures how the CLI app sends a held key. The
# copy goes over the scripts' own ssh (its timeouts, its plain-words failures) and is checked by its sha256.
device-tools:
	$(PYTHON) scripts/device/tools.py --host $(HOST)
