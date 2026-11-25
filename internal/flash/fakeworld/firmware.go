package fakeworld

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"meshflash/internal/catalog"
	"meshflash/internal/esp"
	"meshflash/internal/nodecli"
	"meshflash/internal/nrfdfu"
)

// The firmware the sites make up. No real firmware is in the repository (CLAUDE.md), and internal/catalog refuses a
// body under catalog.MinFileSize, so each file is made up on request, deterministically from its name: the right
// container for its kind (an ESP32 image, a merged image, a DFU package, a UF2), noise for the bulk, and a marker
// near the start saying what a board runs once flashed with it. The end marker lets a board tell a whole image from
// one written part-way: only a whole one runs.
const (
	markerStart = "MESHFLASH-FAKE "
	markerEnd   = "MESHFLASH-END"
	// appSize is a made-up app's size: over 52 KB, so the catalog takes it, and a whole number of words, so the DFU
	// package's image is too.
	appSize = 56 << 10
)

// marker is the line a made-up image carries: "MESHFLASH-FAKE repeater v1.18.0-81dbae5 #1a2b3c4d\n" ("-" for no
// role, " stuck" for a console left in region-load mode), the tag being the file's own, which its end marker repeats
// ("MESHFLASH-END #1a2b3c4d"): an image written part-way over another of the same size then has the other's end
// marker, with the other's tag, and doesn't run.
func marker(fw Firmware, tag uint32) string {
	role := fw.Role
	if role == "" {
		role = "-"
	}
	m := markerStart + role + " " + fw.Version
	if fw.Stuck {
		m += " stuck"
	}
	return m + fmt.Sprintf(" #%08x\n", tag)
}

func endMarker(tag uint32) string { return fmt.Sprintf("%s #%08x\n", markerEnd, tag) }

// parseMarker finds what a written image runs: the marker's firmware, and ok only when the image is whole (its own
// end marker follows).
func parseMarker(img []byte) (Firmware, bool) {
	i := bytes.Index(img, []byte(markerStart))
	if i < 0 {
		return Firmware{}, false
	}
	rest := img[i+len(markerStart):]
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 {
		return Firmware{}, false
	}
	f := strings.Fields(string(rest[:nl]))
	if len(f) < 3 || !strings.HasPrefix(f[len(f)-1], "#") {
		return Firmware{}, false
	}
	tag := f[len(f)-1]
	if !bytes.Contains(rest[nl:], []byte(markerEnd+" "+tag+"\n")) {
		return Firmware{}, false
	}
	fw := Firmware{Role: f[0], Version: f[1]}
	if fw.Role == "-" {
		fw.Role = ""
	}
	for _, flag := range f[2 : len(f)-1] {
		if flag == "stuck" {
			fw.Stuck = true
		}
	}
	return fw, true
}

var buildInName = regexp.MustCompile(`v?\d+\.\d+\.\d+-[0-9a-f]{4,40}`)

// firmwareOf is what a catalog file runs once flashed: its role as the console reports it (a MeshCore repeater or
// room server, else "companion": firmware with no console meshflash knows), and its version as `ver` would print it
// ("v1.18.0-81dbae5", or "1.18.2-zephcore" for a ZephCore build, whose console the world makes answer like
// MeshCore's: UNVERIFIED, recorded on hardware in Phase 3).
func firmwareOf(fi *fileInfo) Firmware {
	role := "companion"
	switch fi.role {
	case "repeater":
		role = nodecli.RoleRepeater
	case "roomServer":
		role = nodecli.RoleRoomServer
	}
	if fi.source == catalog.SourceZephCore {
		if role == nodecli.RoleRoomServer {
			role = "companion"
		}
		return Firmware{Role: role, Version: strings.TrimPrefix(fi.version, "v") + "-zephcore"}
	}
	ver := fi.version
	if m := buildInName.FindString(fi.base); m != "" && strings.HasPrefix(m, ver) {
		ver = m
	}
	return Firmware{Role: role, Version: ver}
}

// serve is a whole file's bytes, made for fi with fw's marker and, for a DFU package, SoftDevice sd; size is the
// app's (appSize in tests, a real image's in the demo).
func serve(fi *fileInfo, fw Firmware, sd uint16, size int) []byte {
	seed := seedOf(fi.base)
	switch {
	case fi.typ == "download" || strings.HasSuffix(strings.ToLower(fi.base), ".uf2"):
		return uf2File(seed)
	case strings.HasSuffix(strings.ToLower(fi.base), ".zip"):
		return dfuPackage(appImage(seed, fw, size), sd)
	case fi.typ == "flash-wipe":
		return mergedImage(chipOf(fi.device), seed, fw, size)
	}
	return espApp(chipOf(fi.device), seed, fw, size)
}

func seedOf(name string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(name))
	return h.Sum32()
}

// noise is deterministic bytes that don't compress (Numerical Recipes' LCG, as reference/esp32/mkimages.mjs).
func noise(seed uint32, n int) []byte {
	out := make([]byte, n)
	s := seed
	for i := range out {
		s = s*1664525 + 1013904223
		out[i] = byte(s >> 24)
	}
	return out
}

// appImage is the body of a made-up app: the marker, noise, the end marker; size bytes (a whole number of words).
func appImage(seed uint32, fw Firmware, size int) []byte {
	if size <= 0 {
		size = appSize
	}
	b := noise(seed, size&^3)
	copy(b, marker(fw, seed))
	end := endMarker(seed)
	copy(b[len(b)-len(end):], end)
	return b
}

// espImage is an ESP image as reference/esp32/mkimages.mjs builds one: esp_image_header_t naming the chip, one
// segment, then data.
func espImage(chip esp.Chip, entry, load uint32, data []byte) []byte {
	b := make([]byte, 24+8+len(data))
	b[0], b[1], b[2], b[3] = 0xE9, 1, 2, 0x20 // magic, one segment, DIO, 4 MB at 40 MHz
	binary.LittleEndian.PutUint32(b[4:], entry)
	b[8] = 0xEE // no WP pin
	binary.LittleEndian.PutUint16(b[12:], imageChipID(chip))
	binary.LittleEndian.PutUint16(b[17:], 0xFFFF) // any chip revision
	binary.LittleEndian.PutUint32(b[24:], load)
	binary.LittleEndian.PutUint32(b[28:], uint32(len(data)))
	copy(b[32:], data)
	return b
}

// imageChipID is the chip ID an image header carries (esp_image_header_t.chip_id; internal/esp's table).
func imageChipID(c esp.Chip) uint16 {
	switch c {
	case esp.ESP32S3:
		return 9
	case esp.ESP32C3:
		return 5
	case esp.ESP32C6:
		return 13
	}
	return 0
}

// espApp is a flash-update file: an app image for the chip, written at 0x10000.
func espApp(chip esp.Chip, seed uint32, fw Firmware, size int) []byte {
	img := espImage(chip, 0x40375a1c, 0x3c030020, appImage(seed, fw, size))
	if len(img) > 0x8000+2 && img[0x8000] == 0xAA {
		img[0x8000] = 0 // noise that looked like a partition table would make it a merged image to esp.ImageChip
	}
	return img
}

// mergedImage is a flash-wipe file: the second-stage bootloader where the chip boots it (0x1000 on the classic
// ESP32, 0x0 on the others), a partition table at 0x8000, and the same app as the update file at 0x10000.
func mergedImage(chip esp.Chip, seed uint32, fw Firmware, size int) []byte {
	out := bytes.Repeat([]byte{0xFF}, esp.AppAddress)
	at := 0
	if chip == esp.ESP32 {
		at = 0x1000
	}
	copy(out[at:], espImage(chip, 0x403ce000, 0x3fcd5820, noise(seed^0x5a5a5a5a, 4000)))
	pt := out[0x8000:]
	pt[0], pt[1], pt[2], pt[3] = 0xAA, 0x50, 0x00, 0x10 // an app partition (factory)
	binary.LittleEndian.PutUint32(pt[4:], esp.AppAddress)
	binary.LittleEndian.PutUint32(pt[8:], 0x300000)
	copy(pt[12:], "factory\x00")
	return append(out, espApp(chip, seed, fw, size)...)
}

// dfuPackage is a flash file for an nRF52: adafruit-nrfutil's legacy package, an application with its init packet
// (device type 0x0052, the SoftDevice it needs, the image's CRC-16), stored uncompressed.
func dfuPackage(bin []byte, sd uint16) []byte {
	if sd == 0 {
		sd = nrfdfu.SoftDeviceS140v6
	}
	dat := make([]byte, 14)
	binary.LittleEndian.PutUint16(dat[0:], nrfdfu.DeviceTypeNRF52)
	binary.LittleEndian.PutUint16(dat[2:], 0xFFFF)
	binary.LittleEndian.PutUint32(dat[4:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint16(dat[8:], 1)
	binary.LittleEndian.PutUint16(dat[10:], sd)
	binary.LittleEndian.PutUint16(dat[12:], nrfdfu.CRC16(bin))
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"manifest.json", []byte(`{"manifest":{"application":{"bin_file":"firmware.bin","dat_file":"firmware.dat"},"dfu_version":0.5}}`)},
		{"firmware.bin", bin},
		{"firmware.dat", dat},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Store})
		if err != nil {
			panic(err) // writing to memory can't fail
		}
		w.Write(f.data)
	}
	zw.Close()
	return b.Bytes()
}

// uf2File is a download-only file: UF2 blocks of noise (https://github.com/microsoft/uf2), never flashed.
func uf2File(seed uint32) []byte {
	const blocks = 112
	data := noise(seed, blocks*476)
	out := make([]byte, 0, blocks*512)
	for i := 0; i < blocks; i++ {
		blk := make([]byte, 512)
		le := binary.LittleEndian
		le.PutUint32(blk[0:], 0x0A324655)
		le.PutUint32(blk[4:], 0x9E5D5157)
		le.PutUint32(blk[8:], 0x2000) // familyID present
		le.PutUint32(blk[12:], uint32(0x26000+i*256))
		le.PutUint32(blk[16:], 256)
		le.PutUint32(blk[20:], uint32(i))
		le.PutUint32(blk[24:], blocks)
		le.PutUint32(blk[28:], 0xADA52840) // nRF52840
		copy(blk[32:32+476], data[i*476:])
		le.PutUint32(blk[508:], 0x0AB16F30)
		out = append(out, blk...)
	}
	return out
}
