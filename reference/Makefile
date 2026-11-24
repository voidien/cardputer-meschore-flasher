# Re-records what the reference JavaScript does, into each package's testdata (README.md here says why and how).
#   make            the web flasher's catalog, ESP32 and nRF52 paths (needs Node 22 and Python 3)
#   make check      the same, then fails if any committed recording changed
#
# The setup console's reference (config.meshcore.io) is recorded separately: internal/nodecli/testdata/configtool.

TESTDATA := internal/catalog/testdata internal/esp/testdata internal/nrfdfu/testdata

.PHONY: record check flasher

record: flasher
	$(MAKE) -C catalog record
	$(MAKE) -C esp32 record
	$(MAKE) -C nrf52 record

# Clones the flasher the first time; after that it only checks out the pinned commit.
flasher:
	./fetch-flasher.sh

check: record
	@git -C .. diff --quiet -- $(TESTDATA) && test -z "$$(git -C .. status --porcelain -- $(TESTDATA))" || \
		{ echo "a recording changed:"; git -C .. status --short -- $(TESTDATA); exit 1; }
	@echo "every recording matches the committed one"
