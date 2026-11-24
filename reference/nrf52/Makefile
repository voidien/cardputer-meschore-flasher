# Re-records the web flasher's nRF52 path (lib/dfu.js from flasher.meshcore.dev@45a5257, vendored byte for byte in
# vendor/) in Node against the fake bootloader in harness.mjs, into internal/nrfdfu/testdata. See README.md.
#
#   make record    needs Node 22 and Python 3; nothing here touches the network
#   make zips      only rebuild the synthetic packages

NODE   ?= $(shell command -v node 2>/dev/null || echo /opt/node22/bin/node)
PYTHON ?= python3
OUT    ?= ../../internal/nrfdfu/testdata

.PHONY: record zips check-vendor

check-vendor:
	@cd vendor && sha256sum -c --quiet SHA256SUMS

zips:
	mkdir -p $(OUT)
	$(PYTHON) -I make_zips.py $(OUT)

# $(call rec,zip,scenario,name): one recording; a ":" can't go in a make target or a portable file name, so the
# file is named <name>.
define rec
	$(NODE) harness.mjs $(OUT)/$(1) $(OUT)/$(3).jsonl $(2)
endef

record: check-vendor zips
	$(call rec,tiny-1028.zip,ok,tiny-1028)
	$(call rec,tiny-4096.zip,ok,tiny-4096)
	$(call rec,synth-40000.zip,ok,synth-40000)
	gzip -9nf $(OUT)/synth-40000.jsonl
	$(NODE) harness.mjs - $(OUT)/touch.jsonl touch
	$(call rec,synth-40000.zip,drop-ack:0,synth-40000-drop-ack-0)
	$(call rec,synth-40000.zip,drop-ack:1,synth-40000-drop-ack-1)
	$(call rec,synth-40000.zip,bad-ack:2,synth-40000-bad-ack-2)
	$(call rec,sd-bl.zip,ok,sd-bl)
