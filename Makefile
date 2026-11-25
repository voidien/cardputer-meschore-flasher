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

GO   ?= go
export GOTOOLCHAIN ?= go1.25.4
HOST ?= CardPuter

.PHONY: test reference build-arm64 deploy install-desktop demo run-small icon

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
