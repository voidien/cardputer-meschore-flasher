package usbid

// The USB ID table: every board in the live catalog of 24 Nov 2026 that a USB identity can point to, in the
// catalog's own order (so Candidates keeps that order by walking the table), with the identities it shows.
//
// Built from docs/meshcore-flashing.md §6.2-6.4 and the full research behind it (usb-ids.md §3-4), which give a
// source for every row:
//
//   - nRF52 apps: VID:PID = MeshCore boards/<board>.json build.hwids[0], product = build.usb_product
//     (builder-framework-arduino-nrf5 adafruit.py:233-239); CDC interface "TinyUSB Serial" (Adafruit_USBD_CDC.cpp:105).
//   - nRF52 bootloaders: Adafruit_nRF52_Bootloader_OTAFIX src/boards/<board>/board.h: USB_DESC_VID, USB_DESC_UF2_PID
//     (double reset) and USB_DESC_CDC_ONLY_PID (after the 1200-baud touch), product = BLEDIS_MODEL; CDC interface
//     "nRF Serial" (src/usb/usb_desc.c:153-157). Vendors' factory bootloaders may differ: UNVERIFIED.
//   - ESP32-S3/C3 on the chip's USB-Serial-JTAG (ARDUINO_USB_MODE=1, ARDUINO_USB_CDC_ON_BOOT=1): fixed hardware,
//     303a:1001 "Espressif" / "USB JTAG/serial debug unit", the MAC as serial, the same in the app and in ROM
//     (openocd-esp32 303a_1001_esp_usb_jtag.txt; esp-idf usb-serial-jtag-console.rst:17).
//   - ESP32-S3 on TinyUSB (USB_MODE=0, CDC_ON_BOOT=1): VID 303a, PID 0x0002 unless pins_arduino.h sets one,
//     product = the board JSON's name (arduino-esp32 2.0.17 USB.cpp:24-38). In ROM it comes back as a new USB
//     device, the USB-Serial-JTAG at 303a:1001 (esp32-hal-tinyusb.c:402-414,471). Source inference: UNVERIFIED on
//     hardware.
//   - Bridges: Heltec, LilyGo and Elecrow pages; Meshtastic's supported_device.py for 1a86:55d4 and 10c4:ea60.
//
// Names are spelled exactly as the live catalog spells them ("Heltec Heltec Wireless Paper" included); table_test.go
// checks each against testdata/catalog-names.txt, a copy of the live catalog's names. When the catalog changes, save
// it in the research samples, regenerate that list with
//
//	MESHFLASH_SAMPLES=/home/user/meshflash-research \
//	  go test ./internal/usbid -run TestCatalogNamesMatchSamples -args -update
//
// and walk the research's scripts/usbids/map_devices.py over the new catalog to find the new boards' identities.
// TestTableMatchesResearchMap (also gated on MESHFLASH_SAMPLES) checks the rows against that script's output,
// scripts/usbids/map_live.json: chips, nRF52 app identities and each ESP32 board's USB path.

// Chip is the microcontroller on a board, named as esptool names ESP32 chips.
type Chip string

const (
	// ChipUnknown is a board whose chip nobody has checked: the Ripple-only boards.
	ChipUnknown Chip = ""
	// ESP32 is the original ESP32: no USB of its own, so always behind a bridge chip.
	ESP32 Chip = "esp32"
	// ESP32S3 has both USB-Serial-JTAG and USB-OTG (TinyUSB).
	ESP32S3 Chip = "esp32s3"
	// ESP32C3 has only USB-Serial-JTAG.
	ESP32C3 Chip = "esp32c3"
	// NRF52840 is every nRF52 board in the catalog: native USB in the app and in the bootloader.
	NRF52840 Chip = "nrf52840"
	// RP2040 is the Raspberry Pi Pico W. meshflash can't flash it (the catalog marks it noflash), but a Pico W
	// running repeater firmware can still be set up over its serial console.
	RP2040 Chip = "rp2040"
)

// Mode is what a board is running while it shows a given USB identity.
type Mode string

const (
	// ModeApp: firmware is running (MeshCore or anything else).
	ModeApp Mode = "app"
	// ModeBootloader: an nRF52 in its Adafruit bootloader, after the 1200-baud touch or a double press of reset.
	ModeBootloader Mode = "bootloader"
	// ModeROM: an ESP32 in its ROM download mode.
	ModeROM Mode = "rom"
	// ModeEither: the identity is the same in the app and in ROM or the bootloader, so USB can't tell which is
	// running. Probe instead: the ROM answers SYNC, a MeshCore app answers its CLI.
	ModeEither Mode = "either"
)

// USB vendor IDs, product IDs and strings the rules below look at.
const (
	vidEspressif = 0x303a
	vidAdafruit  = 0x239a
	vidSeeed     = 0x2886
	vidSiLabs    = 0x10c4
	vidWCH       = 0x1a86
	vidRPi       = 0x2e8a

	pidUSBJTAG   = 0x1001 // the ESP32-S3/C3's USB-Serial-JTAG
	pidS3ROMOTG  = 0x0009 // an ESP32-S3 ROM over USB-OTG (esptool loader.py:1343-1347): not expected, see Candidates
	pidTinyUSB   = 0x0002 // arduino-esp32's default TinyUSB PID (USB.cpp:27-29)
	pidCP210x    = 0xea60
	pidCH340     = 0x7523
	pidCH340K    = 0x7522
	pidCH9102    = 0x55d4
	pidRAKBoot   = 0x0029 // Adafruit's UF2-mode PID, used by the RAK, T-Echo and Feather bootloaders
	pidRAKBootCD = 0x002a // the same bootloaders after the 1200-baud touch (CDC only)

	productUSBJTAG = "USB JTAG/serial debug unit"
)

// The CDC interface strings that tell an nRF52's bootloader from its app, the one sure sign when both share a
// VID:PID (ProMicro, Wio Tracker L1). Port.Interface holds the port's.
const (
	// InterfaceNRFBootloader is the Adafruit bootloader's (OTAFIX src/usb/usb_desc.c:156). SPEC.md §6.3 sends DFU
	// START only to a port that shows it; Classify also calls a port with a bootloader-only VID:PID NRFBootloader,
	// so code that must be strict checks this string itself.
	InterfaceNRFBootloader = "nRF Serial"
	// InterfaceNRFApp is Adafruit TinyUSB's, in the app (Adafruit_USBD_CDC.cpp:105).
	InterfaceNRFApp = "TinyUSB Serial"
)

// bridges are the USB-to-UART chips on catalog boards: the board's UART0 is the console, and the chip's DTR/RTS
// drive EN and IO0, so the identity is the same in the app and in ROM.
var bridges = map[[2]uint16]string{
	{vidSiLabs, pidCP210x}: "CP210x", // CP2102/CP2104; linux cp210x.c:170
	{vidWCH, pidCH340}:     "CH340",  // ch341.c:90
	{vidWCH, pidCH340K}:    "CH340K", // ch341.c:89
	{vidWCH, pidCH9102}:    "CH9102", // not in ch341's table, so cdc_acm binds it and it's a ttyACM (cdc-acm.c:2006-2012)
}

// usbID is one USB identity a board shows in one mode.
type usbID struct {
	vid, pid uint16
	// product narrows the VID:PID when it's set: only a port with exactly this product string matches. "" matches any
	// product, for bridges (whose strings say nothing about the board) and identities whose strings nobody knows.
	product    string
	mode       Mode
	unverified bool
	note       string // shown to the owner next to the board's name: plain English, no sources
}

// device is one catalog board and the USB identities it can show.
type device struct {
	name string // exactly as the live catalog spells it
	chip Chip
	ids  []usbID
}

// hwcdc is the chip's own USB-Serial-JTAG, used by the app when the board JSON (or env) sets ARDUINO_USB_MODE=1 and
// ARDUINO_USB_CDC_ON_BOOT=1. Fixed hardware: the ROM shows exactly the same identity.
func hwcdc() usbID {
	return usbID{vid: vidEspressif, pid: pidUSBJTAG, product: productUSBJTAG, mode: ModeEither}
}

// hwcdcInferred is hwcdc for a board whose USB-Serial-JTAG the research only infers from its build (a generic devkitc
// board JSON plus an env's CDC_ON_BOOT=1, research §6.3 "source inference"): nobody has seen which USB its connector
// is wired to, so the row is unverified like the other source inferences.
func hwcdcInferred() usbID {
	id := hwcdc()
	id.unverified = true
	id.note = "Worked out from the firmware's source, not yet seen on a real board."
	return id
}

// tinyUSB is an ESP32-S3 app on arduino-esp32's TinyUSB serial: the product is the board JSON's name.
func tinyUSB(pid uint16, product string) usbID {
	return usbID{vid: vidEspressif, pid: pid, product: product, mode: ModeApp, unverified: true,
		note: "Worked out from the firmware's source, not yet seen on a real board."}
}

// romJTAG is a TinyUSB board after it resets into ROM: the ROM brings up the USB-Serial-JTAG, a new USB device.
func romJTAG() usbID {
	return usbID{vid: vidEspressif, pid: pidUSBJTAG, product: productUSBJTAG, mode: ModeROM,
		note: "Only in ROM download mode; its firmware shows a different USB identity."}
}

// rippleROM is a Ripple-only board: no MeshCore variant was checked, so its USB is a guess from the catalog saying
// ESP32. If it has native USB, its ROM is the USB-Serial-JTAG (usb-ids.md §4).
func rippleROM() usbID {
	return usbID{vid: vidEspressif, pid: pidUSBJTAG, product: productUSBJTAG, mode: ModeROM, unverified: true,
		note: "Ripple-only board: its USB identity and chip are unverified."}
}

func bridge(vid, pid uint16, note string, unverified bool) usbID {
	return usbID{vid: vid, pid: pid, mode: ModeEither, note: note, unverified: unverified}
}

func nrfApp(vid, pid uint16, product string) usbID {
	return usbID{vid: vid, pid: pid, product: product, mode: ModeApp}
}

func nrfBoot(vid, pid uint16, product string) usbID {
	return usbID{vid: vid, pid: pid, product: product, mode: ModeBootloader}
}

// rakBoot is the RAK-style OTAFIX bootloader: 239a:0029 after a double press, 239a:002a after the touch.
func rakBoot(product string) []usbID {
	return []usbID{nrfBoot(vidAdafruit, pidRAKBoot, product), nrfBoot(vidAdafruit, pidRAKBootCD, product)}
}

func unverifiedID(id usbID, note string) usbID {
	id.unverified = true
	id.note = note
	return id
}

// The ESP32 bridge rows' notes.
const (
	noteTBeamCP2104 = "Older T-Beam revisions have a CP2104 instead of the CH9102."
	noteMaybeCH340  = "Unverified: which bridge chip this board has isn't known."
)

// table lists the boards in the live catalog's order (its "device" array). Catalog boards with no row are in
// notIdentified, with the reason.
var table = []device{
	// heltec_v4.json:9 (USB_MODE=1, CDC_ON_BOOT=1); heltec.org: "Removed CP2102".
	{"Heltec v4 + Expansion Kit (Touch)", ESP32S3, []usbID{hwcdc()}},
	// heltec_v4_r8.json:9. Its pins_arduino.h sets 303a:1001, which only TinyUSB would use.
	{"Heltec v4 R8 + Expansion Kit (Touch)", ESP32S3, []usbID{hwcdc()}},
	// t-deck.json:9 + env CDC_ON_BOOT=1; Meshtastic saw 303a:1001 on a real one (supported_device.py:229-239).
	{"LilyGo T-Deck", ESP32S3, []usbID{hwcdc()}},
	{"LilyGo T-Deck Max", ChipUnknown, []usbID{rippleROM()}},
	{"LilyGo T-Deck Pro", ChipUnknown, []usbID{rippleROM()}},
	{"LilyGo T-Deck Pro v1.1", ChipUnknown, []usbID{rippleROM()}},
	{"LilyGo T5 E-Paper S3 Pro (H752-XX)", ChipUnknown, []usbID{rippleROM()}},
	{"LilyGo T-Lora Pager", ChipUnknown, []usbID{rippleROM()}},
	{"LilyGo T-Display Pro", ChipUnknown, []usbID{rippleROM()}},
	{"LilyGo T-Watch S3 Plus", ChipUnknown, []usbID{rippleROM()}},
	{"LilyGo T-Watch Ultra", ChipUnknown, []usbID{rippleROM()}},
	// The Wio Tracker L1 keeps 2886:1667 in its bootloader: the product ("Seeed Wio Tracker L1" in the app,
	// "Wio Tracker L1" in the bootloader) and the interface string tell them apart.
	// App: seeed-wio-tracker-l1.json:10,28. Bootloader: OTAFIX wio_tracker_l1/board.h:51,56-58.
	{"Seeed Studio Wio Tracker L1 Pro", NRF52840, []usbID{
		nrfApp(vidSeeed, 0x1667, "Seeed Wio Tracker L1"),
		nrfBoot(vidSeeed, 0x1667, "Wio Tracker L1"),
	}},
	{"Seeed Studio Wio Tracker L2 Pro", ChipUnknown, []usbID{rippleROM()}},
	// t3_s3_v1_x.json:9; LilyGo's wiki: "USB Mode CDC and JTAG".
	{"LilyGo T3 S3 (SX126x)", ESP32S3, []usbID{hwcdc()}},
	{"LilyGo T3 S3 (SX127x)", ESP32S3, []usbID{hwcdc()}},
	// T-Beams: a CH9102 now (LilyGo: "Serial Chip CH9102"; Meshtastic supported_device.py:50-66), a CP2104 on older
	// revisions (espboards.dev). The CH9102's strings are UNVERIFIED, so its rows don't use them.
	{"LilyGo T-Beam (SX1262)", ESP32, []usbID{
		bridge(vidWCH, pidCH9102, "", false),
		bridge(vidSiLabs, pidCP210x, noteTBeamCP2104, false),
	}},
	{"LilyGo T-Beam 1.2 (SX1276)", ESP32, []usbID{
		bridge(vidWCH, pidCH9102, "", false),
		bridge(vidSiLabs, pidCP210x, noteTBeamCP2104, false),
	}},
	// t_beam_1w.json:8,40; variants/lilygo_tbeam_1w/pins_arduino.h:6-7.
	{"LilyGo T-Beam 1W", ESP32S3, []usbID{tinyUSB(pidUSBJTAG, "LilyGo TBeam-1W"), romJTAG()}},
	// t_beams3_supreme.json:9.
	{"LilyGo T-Beam Supreme (SX1262)", ESP32S3, []usbID{hwcdc()}},
	// App: t-echo.json:10,16. Bootloader: OTAFIX lilygo_techo/board.h:49,54-56 (0029 in both bootloader modes).
	{"LilyGo T-Echo", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x8029, "NRF52 DK"),
		nrfBoot(vidAdafruit, pidRAKBoot, "T-Echo v1.0"),
	}},
	// The catalog ships no bootloader for the Lite and the Card; the research assumes the T-Echo's.
	{"LilyGo T-Echo Lite", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x8029, "NRF52 DK"),
		unverifiedID(nrfBoot(vidAdafruit, pidRAKBoot, "T-Echo v1.0"),
			"Unverified: its bootloader is assumed to be the T-Echo's."),
	}},
	{"LilyGo T-Echo Card", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x8029, "NRF52 DK"),
		unverifiedID(nrfBoot(vidAdafruit, pidRAKBoot, "T-Echo v1.0"),
			"Unverified: its bootloader is assumed to be the T-Echo's."),
	}},
	// wiki.lilygo.cc: "CH9102 USB-to-serial"; Meshtastic supported_device.py:113-121.
	{"LilyGo LoRa32 V2.1_1.6", ESP32, []usbID{bridge(vidWCH, pidCH9102, "", false)}},
	// App: rak4631.json:10,28. Bootloader: OTAFIX wiscore_rak4631_board/board.h:53,58-60.
	{"GAT-IoT GAT562 Tracker", NRF52840, append([]usbID{
		nrfApp(vidAdafruit, 0x8029, "WisCore RAK4631 Board"),
	}, rakBoot("WisBlock RAK4631")...)},
	{"GAT-IoT GAT562 30s", NRF52840, append([]usbID{
		nrfApp(vidAdafruit, 0x8029, "WisCore RAK4631 Board"),
	}, rakBoot("WisBlock RAK4631")...)},
	// heltec_e213.json:9,32 (USB_MODE=0, CDC_ON_BOOT=1); its pins_arduino.h sets no USB_PID, so 0x0002.
	{"Heltec Vision Master E213", ESP32S3, []usbID{tinyUSB(pidTinyUSB, "Heltec Vision Master E213"), romJTAG()}},
	{"Heltec Vision Master E290", ESP32S3, []usbID{tinyUSB(pidTinyUSB, "Heltec Vision Master E290"), romJTAG()}},
	// Meshtastic supported_device.py:131-147.
	{"Heltec v2", ESP32, []usbID{bridge(vidSiLabs, pidCP210x, "", false)}},
	// esp32-s3-devkitc-1.json:8 sets no CDC_ON_BOOT, so the console is UART0; heltec.org: "USB to Serial Chip CP2102".
	{"Heltec v3", ESP32S3, []usbID{bridge(vidSiLabs, pidCP210x, "", false)}},
	{"Heltec v4", ESP32S3, []usbID{hwcdc()}},
	{"Heltec v4 R8", ESP32S3, []usbID{hwcdc()}},
	// App: heltec_t096.json:10,16. Bootloader: OTAFIX heltec_t096/board.h:108,113-115.
	{"Heltec Mesh Node T096", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x8029, "HT-n5262G"),
		nrfBoot(vidAdafruit, 0x0071, "Tracker"),
	}},
	// Bootloader: OTAFIX heltec_t1/board.h:107,112-114.
	{"Heltec Mesh Node T1", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x8029, "HT-n5262T1"),
		nrfBoot(vidAdafruit, 0x0071, "T1"),
	}},
	// Bootloader: OTAFIX heltec_t114/board.h:90,95-97.
	{"Heltec T114", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x8029, "HT-n5262"),
		nrfBoot(vidAdafruit, 0x0071, "HT-n5262"),
	}},
	// heltec_tracker_v2.json:8,28.
	{"Heltec Wireless Tracker v2", ESP32S3, []usbID{tinyUSB(pidTinyUSB, "heltec_tracker v2"), romJTAG()}},
	// MeshPocket, MeshSolar and MeshTower V2: the catalog ships no bootloader for them, so their bootloader's USB
	// identity is unknown (the board JSONs' extra hwids are only hints) and only the app rows are here, apart from
	// the one guess the research makes for the MeshTower V2.
	// heltec_mesh_pocket.json:10,15; heltec_mesh_solar.json; heltec_tower_v2.json.
	{"Heltec MeshPocket", NRF52840, []usbID{nrfApp(vidAdafruit, 0x4405, "HT-n5262")}},
	{"Heltec MeshSolar / MeshTower", NRF52840, []usbID{nrfApp(vidAdafruit, 0x8029, "HT-n5262")}},
	{"Heltec MeshTower V2", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x4405, "HT-n5262"),
		// The live catalog's KISS modem file for it is the MeshSolar's (Heltec_mesh_solar_kiss_modem-1.17.1-45c0c323,
		// built on heltec_mesh_solar.json: 239a:8029 "HT-n5262"; research scripts/usbids/map_live.json), so a
		// MeshTower V2 running it shows the MeshSolar's identity.
		{vid: vidAdafruit, pid: 0x8029, product: "HT-n5262", mode: ModeApp,
			note: "Shows this when running the KISS modem firmware, which is the MeshSolar's."},
		// Its hwids list 239a:0071, Heltec's bootloader PID. The product it would show there is unknown, so this
		// row matches any product.
		unverifiedID(nrfBoot(vidAdafruit, 0x0071, ""), "Unverified: its bootloader's USB identity isn't known."),
	}},
	// devkitc board + env CDC_ON_BOOT=1 "need for Serial" (heltec_tracker/platformio.ini:8): source inference.
	{"Heltec Wireless Tracker", ESP32S3, []usbID{hwcdcInferred()}},
	// Schematic V0.4: U5 is a CP2102; the CDC_ON_BOOT flag is commented out (heltec_wireless_paper/platformio.ini:8).
	{"Heltec Heltec Wireless Paper", ESP32S3, []usbID{bridge(vidSiLabs, pidCP210x, "", false)}},
	// heltec.org WSL V3: "USB to Serial Chip CP2102".
	{"Heltec WSL3", ESP32S3, []usbID{bridge(vidSiLabs, pidCP210x, "", false)}},
	// App: seeed-xiao-afruitnrf52-nrf52840.json:10,28. Bootloader: OTAFIX xiao_nrf52840_ble/board.h:53,58-60.
	{"Ikoka Stick", NRF52840, []usbID{
		nrfApp(vidSeeed, 0x8044, "XIAO nRF52840"),
		nrfBoot(vidSeeed, 0x0044, "XIAO nRF52840"),
	}},
	{"Ikoka Nano", NRF52840, []usbID{
		nrfApp(vidSeeed, 0x8044, "XIAO nRF52840"),
		nrfBoot(vidSeeed, 0x0044, "XIAO nRF52840"),
	}},
	// App: keepteen_lt1.json:10,32. The catalog's bootloader for it is the RAK4631's.
	{"Keepteen LT1", NRF52840, append([]usbID{
		nrfApp(vidAdafruit, 0x00b3, "Keepteen LT1"),
	}, withNote(rakBoot("WisBlock RAK4631"), "With the catalog's RAK4631 bootloader installed.")...)},
	// devkitc board + env CDC_ON_BOOT=1 (rak3112/platformio.ini:10): source inference.
	{"RAK WisBlock 3112", ESP32S3, []usbID{hwcdcInferred()}},
	// rak4631.json; Meshtastic also saw 239a:8029 (supported_device.py:199-208).
	{"RAK WisBlock / WisMesh (RAK 4631)", NRF52840, append([]usbID{
		nrfApp(vidAdafruit, 0x8029, "WisCore RAK4631 Board"),
	}, rakBoot("WisBlock RAK4631")...)},
	// App: rak3401.json. Bootloader: OTAFIX wiscore_rak3401/board.h:53,58-60.
	{"RAK WisMesh 1W Booster (3401 + 13302)", NRF52840, append([]usbID{
		nrfApp(vidAdafruit, 0x8029, "WisCore RAK3401 Board"),
	}, rakBoot("WisBlock RAK3401")...)},
	// Bootloader: OTAFIX wismesh_tag/board.h:53,58-60.
	{"RAK WisMesh Tag", NRF52840, append([]usbID{
		nrfApp(vidAdafruit, 0x8029, "WisCore RAK4631 Board"),
	}, rakBoot("WisMesh Tag")...)},
	// App: seeed_sensecap_solar.json:10,27. Bootloader: OTAFIX sensecap_solar_p1/board.h:51,56-58.
	{"Seeed Studio SenseCAP Solar Node P1", NRF52840, []usbID{
		nrfApp(vidSeeed, 0x0059, "XIAO nRF52840"),
		nrfBoot(vidSeeed, 0x0044, "SenseCAP Solar Node P1"),
	}},
	// App: tracker-t1000-e.json:10,16. Bootloader: OTAFIX t1000_e/board.h:49,54-56.
	{"Seeed Studio SenseCAP T1000-E", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x8029, "T1000-E-BOOT"),
		nrfBoot(vidSeeed, 0x0057, "T1000-E"),
	}},
	// App: seeed-mesh-tracker-x1.json:10,17. Its hwids include 2886:0057, the T1000-E's bootloader.
	{"Seeed Studio SenseCAP MeshTracker X1", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x8029, "X1-BOOT"),
		unverifiedID(nrfBoot(vidSeeed, 0x0057, "T1000-E"),
			"Unverified: its bootloader is assumed to be the T1000-E's."),
	}},
	{"Seeed Studio Wio Tracker L1 EINK", NRF52840, []usbID{
		nrfApp(vidSeeed, 0x1667, "Seeed Wio Tracker L1"),
		nrfBoot(vidSeeed, 0x1667, "Wio Tracker L1"),
	}},
	// The C3 has only the USB-Serial-JTAG, so the board JSON's 2886:0046 never applies (seeed_xiao_esp32c3.json:7).
	{"Seeed Studio Xiao C3", ESP32C3, []usbID{hwcdc()}},
	// seeed_xiao_esp32s3.json:9; its 2886:0056 would only apply under TinyUSB.
	{"Seeed Studio Xiao S3 WIO", ESP32S3, []usbID{hwcdc()}},
	// The Sense bootloader: OTAFIX xiao_nrf52840_ble_sense/board.h:53,58-60.
	{"Seeed Studio Xiao nRF52 WIO", NRF52840, []usbID{
		nrfApp(vidSeeed, 0x8044, "XIAO nRF52840"),
		nrfBoot(vidSeeed, 0x0044, "XIAO nRF52840"),
		nrfBoot(vidSeeed, 0x0045, "XIAO nRF52840 Sense"),
	}},
	// App: thinknode_m1.json:10,24. Bootloader: OTAFIX thinknode_m1/board.h:48,53-55.
	{"Elecrow ThinkNode M1", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x4405, "elecrow_eink"),
		nrfBoot(vidAdafruit, 0x00da, "ThinkNodeM1"),
	}},
	// UART0 behind a CH340K (Elecrow's datasheet; DongLoRa: "CH340K (1a86:7522)"). Its pins_arduino.h sets
	// 303a:1001, but with CDC_ON_BOOT=0 TinyUSB never starts (ESP32-S3-WROOM-1-N4.json:7).
	{"Elecrow ThinkNode M2", ESP32S3, []usbID{bridge(vidWCH, pidCH340K, "", false)}},
	// Bootloader: OTAFIX thinknode_m3/board.h:47,52-54.
	{"Elecrow ThinkNode M3", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x4405, "elecrow_eink"),
		nrfBoot(vidAdafruit, 0x00da, "ThinkNode-M3"),
	}},
	// thinknode_m4.json; the catalog ships no bootloader for it.
	{"Elecrow ThinkNode M4", NRF52840, []usbID{nrfApp(vidAdafruit, 0x4405, "elecrow_thinknode_m4")}},
	// M5 and M7 have their console on UART0 behind a bridge whose chip is UNVERIFIED; a CH340-family chip, as on the
	// M2, is the research's best guess (usb-ids.md §4).
	{"Elecrow ThinkNode M5", ESP32S3, []usbID{
		bridge(vidWCH, pidCH340K, noteMaybeCH340, true),
		bridge(vidWCH, pidCH340, noteMaybeCH340, true),
	}},
	// App: thinknode_m6.json. Bootloader: OTAFIX thinknode_m6/board.h:47,52-54.
	{"Elecrow ThinkNode M6", NRF52840, []usbID{
		nrfApp(vidAdafruit, 0x4405, "elecrow_solar"),
		nrfBoot(vidAdafruit, 0x00da, "ThinkNodeM6"),
	}},
	{"Elecrow ThinkNode M7", ESP32S3, []usbID{
		bridge(vidWCH, pidCH340K, noteMaybeCH340, true),
		bridge(vidWCH, pidCH340, noteMaybeCH340, true),
	}},
	// station-g3-esp32.json:8,31; the 303a:1001 comes from arduino-esp32's variants/esp32s3/pins_arduino.h:7-8.
	{"UnitEng/BQ Voyage Station G3", ESP32S3, []usbID{tinyUSB(pidUSBJTAG, "Station G3 ESP32"), romJTAG()}},
	// station-g2.json:8,31. Its KISS modem build switches to the USB-Serial-JTAG (station_g2/platformio.ini:249),
	// so the USB-Serial-JTAG identity is either that firmware or the ROM.
	{"UnitEng Station G2", ESP32S3, []usbID{
		tinyUSB(pidUSBJTAG, "BQ Station G2"),
		{vid: vidEspressif, pid: pidUSBJTAG, product: productUSBJTAG, mode: ModeEither,
			note: "Shows this in ROM download mode, and when running the KISS modem firmware."},
	}},
	// nano-g2-ultra.json:10,28; the catalog ships no bootloader for it.
	{"UnitEng Nano G2 Ultra", NRF52840, []usbID{nrfApp(vidAdafruit, 0x8029, "BQ nRF52840")}},
	// Its env uses rak4631.json; the catalog ships no bootloader for it.
	{"Muzi Works R1 Neo", NRF52840, append([]usbID{
		nrfApp(vidAdafruit, 0x8029, "WisCore RAK4631 Board"),
	}, withUnverified(rakBoot("WisBlock RAK4631"), "Unverified: its bootloader is assumed to be the RAK4631's.")...)},
	// ProMicro keeps 239a:00b3 and the same strings in its bootloader (promicro_nrf52840.json:10,32; OTAFIX
	// promicro_nrf52840/board.h:51,56-58): only the interface string tells them apart.
	{"ProMicro nrf52 (faketec)", NRF52840, []usbID{
		{vid: vidAdafruit, pid: 0x00b3, product: "ProMicro NRF52840", mode: ModeEither},
	}},
	// arduino-pico's Pico W identity (boards.txt:280-290). MeshCore builds it with another platform, so UNVERIFIED.
	{"RPI Pico W (rp2040) + WaveShare SX1262", RP2040, []usbID{
		unverifiedID(usbID{vid: vidRPi, pid: 0xf00a, product: "Pico W", mode: ModeApp},
			"Unverified, and meshflash can't flash it: only set up firmware already on it."),
	}},
}

// notIdentified lists the live catalog's boards with no table row, and why. A test checks that every catalog board is
// in one list or the other, so a new board can't be forgotten silently.
var notIdentified = map[string]string{
	"Elecrow ThinkNode M9": "Ripple-only in the catalog; its console is behind a bridge chip nobody has identified " +
		"(MeshCore boards/thinknode_m9.json:11-12)",
	"RPI Pico W (rp2350) + WaveShare SX1262": "no MeshCore build to read an identity from; its BOOTSEL ROM " +
		"(2e8a:000f) has no tty",
}

func withNote(ids []usbID, note string) []usbID {
	for i := range ids {
		ids[i].note = note
	}
	return ids
}

func withUnverified(ids []usbID, note string) []usbID {
	for i := range ids {
		ids[i] = unverifiedID(ids[i], note)
	}
	return ids
}

// DeviceChip is the chip on a catalog board, for checking the chip an ESP32 reports after SYNC against the board the
// owner picked (SPEC.md §6.2, "Chip guard"): the catalog itself only says "esp32" for the whole family. ok is false
// when the board isn't in the table or nobody has checked its chip.
func DeviceChip(name string) (chip Chip, ok bool) {
	for _, d := range table {
		if d.name == name {
			return d.chip, d.chip != ChipUnknown
		}
	}
	return ChipUnknown, false
}
