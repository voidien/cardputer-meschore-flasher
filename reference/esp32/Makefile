# Re-records the web flasher's ESP32 path (js/flash.js + lib/esp32.js = esptool-js 0.5.4) in Node against the
# fake ESP32 in fake.mjs, into internal/esp/testdata. See README.md.
#
#   make record    needs Node 22 and ../.flasher (run ../fetch-flasher.sh first)

NODE    ?= $(shell command -v node 2>/dev/null || echo /opt/node22/bin/node)
FLASHER ?= ../.flasher
OUT     ?= ../../internal/esp/testdata
WORK    ?= out

.PHONY: record images clean

images:
	mkdir -p $(OUT)
	$(NODE) mkimages.mjs $(OUT)

# $(call rec,chip,image,environment,address,eraseAll,name[,pid]) records <name> and its MD5=1 variant
define rec
	$(3) $(NODE) run.mjs $(FLASHER) $(1) $(OUT)/$(2) $(4) $(5) $(WORK)/$(6).jsonl $(7)
	gzip -9nc $(WORK)/$(6).jsonl > $(OUT)/$(6).jsonl.gz
	MD5=1 $(3) $(NODE) run.mjs $(FLASHER) $(1) $(OUT)/$(2) $(4) $(5) $(WORK)/$(6)-md5.jsonl $(7)
	gzip -9nc $(WORK)/$(6)-md5.jsonl > $(OUT)/$(6)-md5.jsonl.gz
endef

# The ESP32-S3 on USB-OTG (UARTDEV_BUF_NO = 3, so 0x800-byte RAM blocks), an ECO0 eFuse layout (it changes
# which eFuse words esptool-js reads) and 2 busy polls of SPI_CMD.
S3_OTG_ECO0 := REGS='{"0x3fcef14c":3,"0x6000706c":1,"0x60007050":16777216}' POLLS=2

record: images
	@test -f $(FLASHER)/js/flash.js || { echo "no flasher checkout at $(FLASHER): run ../fetch-flasher.sh"; exit 1; }
	mkdir -p $(WORK)
	$(call rec,esp32,esp32-app.bin,,0x10000,0,esp32-update)
	$(call rec,esp32,esp32-merged.bin,,0x0,1,esp32-wipe)
	$(call rec,esp32s3,s3-app.bin,,0x10000,0,s3-update)
	$(call rec,esp32c3,c3-merged.bin,,0x0,1,c3-jtag-wipe,0x1001)
	$(call rec,esp32s3,s3-app.bin,$(S3_OTG_ECO0),0x10000,0,s3-otg-eco0)

clean:
	rm -rf $(WORK)
