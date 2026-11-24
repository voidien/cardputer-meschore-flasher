package esp

import (
	"fmt"
	"math"
	"strings"
)

// Chip is an ESP chip, named as MeshCore's board files name the MCU ("esp32", "esp32s3", "esp32c3").
type Chip string

// The chips esptool-js 0.5.4 can detect. Only ESP32, ESP32S3 and ESP32C3 (the chips in MeshCore's catalog) can be
// flashed; the others are detected so an error can name them.
const (
	ESP32    Chip = "esp32"
	ESP32S2  Chip = "esp32s2"
	ESP32S3  Chip = "esp32s3"
	ESP32C2  Chip = "esp32c2"
	ESP32C3  Chip = "esp32c3"
	ESP32C5  Chip = "esp32c5"
	ESP32C6  Chip = "esp32c6"
	ESP32C61 Chip = "esp32c61"
	ESP32H2  Chip = "esp32h2"
	ESP32P4  Chip = "esp32p4"
	ESP8266  Chip = "esp8266"
)

// String is the chip's marketing name ("ESP32-S3"), or the raw value for an unknown one.
func (c Chip) String() string {
	if d := chipByName(c); d != nil {
		return d.name
	}
	return string(c)
}

// Supported says whether meshflash can flash this chip: it carries the flasher stub and the reads for it.
func (c Chip) Supported() bool {
	d := chipByName(c)
	return d != nil && d.stubFile != ""
}

// ParseChip accepts a chip as MeshCore and esptool write it ("esp32s3", "ESP32-S3", "esp32-s3").
func ParseChip(s string) (Chip, bool) {
	k := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
	if d := chipByName(Chip(k)); d != nil {
		return d.chip, true
	}
	return "", false
}

// ChipInfo is what the reference's informational reads find out (esptool-js main(): getChipDescription,
// getChipFeatures, getCrystalFreq, readMac).
type ChipInfo struct {
	// Chip is the chip detected.
	Chip Chip
	// Description is the package and revision: "ESP32-D0WD (revision 1)", "ESP32-S3 (QFN56) (revision v0.1)".
	Description string
	// Features are esptool-js's feature list ("Wi-Fi", "BT", "Dual Core", …).
	Features []string
	// CrystalMHz is the crystal's frequency, 40 or 26.
	CrystalMHz int
	// MAC is the base MAC address, "aa:bb:cc:dd:ee:ff". A USB-Serial-JTAG port's USB serial number is this MAC, so
	// it ties the board to its USB identity.
	MAC string
}

// chipDef holds what this package knows about a chip (esptool-js 0.5.4 src/targets/*.ts and magic2Chip,
// Apache-2.0, Espressif; docs/meshcore-flashing.md 4.9).
type chipDef struct {
	chip       Chip
	name       string
	magics     []uint32
	imageID    int    // the chip ID in an image header; -1 for the ESP8266, whose images have none
	bootloader uint32 // where the second-stage bootloader sits in flash

	// The rest is set only for the chips meshflash flashes.
	stubFile string
	// SPI flash controller registers used by the flash-ID read (runSpiflashCommand).
	spiBase, spiUSR, spiUSR2, spiW0, spiMISODlen uint32
	// info sends the reference's informational reads, in its order, and postConnect's.
	info func(*Loader) (ChipInfo, error)
}

var chips = []*chipDef{
	{chip: ESP32, name: "ESP32", magics: []uint32{0x00f01d83}, imageID: 0, bootloader: 0x1000,
		stubFile: "stub_flasher_32.json",
		spiBase:  0x3ff42000, spiUSR: 0x1c, spiUSR2: 0x24, spiW0: 0x80, spiMISODlen: 0x2c,
		info: esp32Info},
	{chip: ESP32S2, name: "ESP32-S2", magics: []uint32{0x000007c6}, imageID: 2, bootloader: 0x1000},
	{chip: ESP32S3, name: "ESP32-S3", magics: []uint32{0x00000009}, imageID: 9, bootloader: 0x0,
		stubFile: "stub_flasher_32s3.json",
		spiBase:  0x60002000, spiUSR: 0x18, spiUSR2: 0x20, spiW0: 0x58, spiMISODlen: 0x28,
		info: esp32s3Info},
	{chip: ESP32C2, name: "ESP32-C2", magics: []uint32{0x6f51306f, 0x7c41a06f}, imageID: 12, bootloader: 0x0},
	{chip: ESP32C3, name: "ESP32-C3", magics: []uint32{0x6921506f, 0x1b31506f, 0x4881606f, 0x4361606f}, imageID: 5, bootloader: 0x0,
		stubFile: "stub_flasher_32c3.json",
		spiBase:  0x60002000, spiUSR: 0x18, spiUSR2: 0x20, spiW0: 0x58, spiMISODlen: 0x28,
		info: esp32c3Info},
	{chip: ESP32C5, name: "ESP32-C5", magics: []uint32{0x1101406f, 0x63e1406f}, imageID: 23, bootloader: 0x2000},
	{chip: ESP32C6, name: "ESP32-C6", magics: []uint32{0x2ce0806f}, imageID: 13, bootloader: 0x0},
	{chip: ESP32C61, name: "ESP32-C61", magics: []uint32{0x2421606f, 0x33f0206f, 0x4f81606f}, imageID: 20, bootloader: 0x0},
	{chip: ESP32H2, name: "ESP32-H2", magics: []uint32{0xd7b73e80}, imageID: 16, bootloader: 0x0},
	{chip: ESP32P4, name: "ESP32-P4", magics: []uint32{0x0, 0x0addbad0, 0x7039ad9}, imageID: 18, bootloader: 0x2000},
	{chip: ESP8266, name: "ESP8266", magics: []uint32{0xfff0c101}, imageID: -1, bootloader: 0x0},
}

func chipByName(c Chip) *chipDef {
	for _, d := range chips {
		if d.chip == c {
			return d
		}
	}
	return nil
}

func chipByMagic(magic uint32) *chipDef {
	for _, d := range chips {
		for _, m := range d.magics {
			if m == magic {
				return d
			}
		}
	}
	return nil
}

func chipByImageID(id int) *chipDef {
	for _, d := range chips {
		if d.imageID == id {
			return d
		}
	}
	return nil
}

// The informational reads below port esptool-js 0.5.4 src/targets/esp32.ts, esp32s3.ts and esp32c3.ts (Apache-2.0,
// Copyright 2019 Espressif Systems (Shanghai) CO LTD; modified: Go, errors returned, the strings built the same
// way). They must send the same READ_REGs in the same order: on the ESP32-S3 which registers are read depends on
// the values (isEco0), so each function follows the original's calls one by one.

func macString(mac0, mac1 uint32) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		(mac1>>8)&0xff, mac1&0xff, (mac0>>24)&0xff, (mac0>>16)&0xff, (mac0>>8)&0xff, mac0&0xff)
}

// regReader reads registers until the first error, so the ports below read like the TypeScript.
type regReader struct {
	l   *Loader
	err error
}

func (r *regReader) read(addr uint32) uint32 {
	if r.err != nil {
		return 0
	}
	v, err := r.l.ReadReg(addr)
	r.err = err
	return v
}

func esp32Info(l *Loader) (ChipInfo, error) {
	r := &regReader{l: l}
	const efuseBase = 0x3ff5a000
	efuse := func(n uint32) uint32 { return r.read(efuseBase + 4*n) }
	pkgVersion := func() uint32 {
		w3 := efuse(3)
		return (w3>>9)&0x07 + ((w3>>2)&0x1)<<3
	}
	chipRevision := func() int {
		w3, w5, apb := efuse(3), efuse(5), r.read(0x3ff66000+0x7c)
		if (w3>>15)&1 != 0 {
			if (w5>>20)&1 != 0 {
				if (apb>>31)&1 != 0 {
					return 3
				}
				return 2
			}
			return 1
		}
		return 0
	}

	// getChipDescription
	desc := []string{"ESP32-D0WDQ6", "ESP32-D0WD", "ESP32-D2WD", "", "ESP32-U4WDH", "ESP32-PICO-D4", "ESP32-PICO-V3-02"}
	pkg := pkgVersion()
	rev := chipRevision()
	rev3 := rev == 3
	if efuse(3)&1 != 0 {
		desc[0], desc[1] = "ESP32-S0WDQ6", "ESP32-S0WD"
	}
	if rev3 {
		desc[5] = "ESP32-PICO-V3"
	}
	name := "Unknown ESP32"
	if pkg <= 6 {
		name = desc[pkg]
	}
	if rev3 && (pkg == 0 || pkg == 1) {
		name += "-V3"
	}
	info := ChipInfo{Chip: ESP32, Description: fmt.Sprintf("%s (revision %d)", name, rev)}

	// getChipFeatures
	f := []string{"Wi-Fi"}
	w3 := efuse(3)
	if w3&(1<<1) == 0 {
		f = append(f, "BT")
	}
	if w3&1 != 0 {
		f = append(f, "Single Core")
	} else {
		f = append(f, "Dual Core")
	}
	if w3&(1<<13) != 0 {
		if w3&(1<<12) != 0 {
			f = append(f, "160MHz")
		} else {
			f = append(f, "240MHz")
		}
	}
	pkg = pkgVersion()
	if pkg == 2 || pkg == 4 || pkg == 5 || pkg == 6 {
		f = append(f, "Embedded Flash")
	}
	if pkg == 6 {
		f = append(f, "Embedded PSRAM")
	}
	if (efuse(4)>>8)&0x1f != 0 {
		f = append(f, "VRef calibration in efuse")
	}
	if (w3>>14)&1 != 0 {
		f = append(f, "BLK3 partially reserved")
	}
	scheme := []string{"None", "3/4", "Repeat (UNSUPPORTED)", "Invalid"}
	f = append(f, "Coding Scheme "+scheme[efuse(6)&0x3])
	info.Features = f

	// getCrystalFreq: the ROM set UART_CLKDIV for 115200 baud from the crystal, so the divider gives it back.
	div := r.read(0x3ff40014) & 0xfffff
	ets := float64(115200) * float64(div) / 1e6
	info.CrystalMHz = 26
	if ets > 33 {
		info.CrystalMHz = 40
	}
	if math.Abs(float64(info.CrystalMHz)-ets) > 1 {
		l.logf("WARNING: Unsupported crystal in use")
	}

	// readMac, twice, as main() does.
	info.MAC = macString(efuse(1), efuse(2))
	_ = macString(efuse(1), efuse(2))
	return info, r.err
}

func esp32s3Info(l *Loader) (ChipInfo, error) {
	r := &regReader{l: l}
	const efuseBase = 0x60007000
	const block1, block2 = efuseBase + 0x44, efuseBase + 0x5c
	rawMinor := func() uint32 {
		hi := (r.read(block1+4*5) >> 23) & 0x01
		low := (r.read(block1+4*3) >> 18) & 0x07
		return hi<<3 + low
	}
	blkMajor := func() uint32 { return r.read(block2+4*4) & 0x03 }
	blkMinor := func() uint32 { return (r.read(block1+4*3) >> 24) & 0x07 }
	isEco0 := func(minorRaw uint32) bool {
		return minorRaw&0x7 == 0 && blkMajor() == 1 && blkMinor() == 1
	}
	rawMajor := func() uint32 { return (r.read(block1+4*5) >> 24) & 0x03 }
	majorVersion := func() uint32 {
		if isEco0(rawMinor()) {
			return 0
		}
		return rawMajor()
	}
	minorVersion := func() uint32 {
		if isEco0(rawMinor()) {
			return 0
		}
		return rawMinor()
	}
	pkgVersion := func() uint32 { return (r.read(block1+4*3) >> 21) & 0x07 }

	// getChipDescription
	major := majorVersion()
	minor := minorVersion()
	name := map[uint32]string{0: "ESP32-S3 (QFN56)", 1: "ESP32-S3-PICO-1 (LGA56)"}[pkgVersion()]
	if name == "" {
		name = "unknown ESP32-S3"
	}
	info := ChipInfo{Chip: ESP32S3, Description: fmt.Sprintf("%s (revision v%d.%d)", name, major, minor)}

	// getChipFeatures
	f := []string{"Wi-Fi", "BLE"}
	flashCap := (r.read(block1+4*3) >> 27) & 0x07
	flashVendor := map[uint32]string{1: "XMC", 2: "GD", 3: "FM", 4: "TT", 5: "BY"}[r.read(block1+4*4)&0x07]
	switch flashCap {
	case 0:
	case 1:
		f = append(f, "Embedded Flash 8MB ("+flashVendor+")")
	case 2:
		f = append(f, "Embedded Flash 4MB ("+flashVendor+")")
	default:
		f = append(f, "Unknown Embedded Flash ("+flashVendor+")")
	}
	psramCap := (r.read(block1+4*4) >> 3) & 0x03
	psramVendor := map[uint32]string{1: "AP_3v3", 2: "AP_1v8"}[(r.read(block1+4*4)>>7)&0x03]
	switch psramCap {
	case 0:
	case 1:
		f = append(f, "Embedded PSRAM 8MB ("+psramVendor+")")
	case 2:
		f = append(f, "Embedded PSRAM 2MB ("+psramVendor+")")
	default:
		f = append(f, "Unknown Embedded PSRAM ("+psramVendor+")")
	}
	info.Features = f
	info.CrystalMHz = 40

	// readMac, twice.
	mac := func() string { return macString(r.read(block1), r.read(block1+4)&0xffff) }
	info.MAC = mac()
	_ = mac()

	// postConnect: over the S3's own USB-OTG the ROM has less RAM to receive into, so the stub goes up in 0x800-byte
	// blocks (UARTDEV_BUF_NO == UARTDEV_BUF_NO_USB).
	if r.read(0x3fcef14c)&0xff == 3 {
		l.ramBlock = 0x800
	}
	return info, r.err
}

func esp32c3Info(l *Loader) (ChipInfo, error) {
	r := &regReader{l: l}
	const block1 = 0x60008800 + 0x44

	// getChipDescription: getPkgVersion, then getChipRevision, each reading word 3.
	desc := "unknown ESP32-C3"
	if (r.read(block1+4*3)>>21)&0x07 == 0 {
		desc = "ESP32-C3"
	}
	rev := (r.read(block1+4*3) & (0x7 << 18)) >> 18
	info := ChipInfo{Chip: ESP32C3, Description: fmt.Sprintf("%s (revision %d)", desc, rev)}

	// getChipFeatures
	f := []string{"Wi-Fi", "BLE"}
	flashCap := (r.read(block1+4*3) >> 27) & 0x07
	vendor := map[uint32]string{1: "XMC", 2: "GD", 3: "FM", 4: "TT", 5: "ZBIT"}[r.read(block1+4*4)&0x07]
	if flashCap != 0 {
		size := map[uint32]string{1: "Embedded Flash 4MB", 2: "Embedded Flash 2MB", 3: "Embedded Flash 1MB", 4: "Embedded Flash 8MB"}[flashCap]
		if size == "" {
			size = "Unknown Embedded Flash"
		}
		f = append(f, size+" ("+vendor+")")
	}
	info.Features = f
	info.CrystalMHz = 40

	mac := func() string { return macString(r.read(block1), r.read(block1+4)&0xffff) }
	info.MAC = mac()
	_ = mac()
	return info, r.err
}
