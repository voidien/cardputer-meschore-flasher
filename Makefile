# meshflash for the CardputerZero. CLAUDE.md says what each target is for.
#   make test        gofmt check, go vet (host and linux/arm64), race tests: no network, no serial ports
#   make reference   re-record the web flasher's own JavaScript in Node (reference/README.md)

GO ?= go
export GOTOOLCHAIN ?= go1.25.4

.PHONY: test reference

test:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	$(GO) vet ./...
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) vet ./...
	$(GO) test -race ./...

reference:
	$(MAKE) -C reference
