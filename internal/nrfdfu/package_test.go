package nrfdfu

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// mkDat builds a legacy init packet.
func mkDat(devType uint16, sdReq []uint16, crc uint16) []byte {
	le := binary.LittleEndian
	b := le.AppendUint16(nil, devType)
	b = le.AppendUint16(b, 0xFFFF)
	b = le.AppendUint32(b, 0xFFFFFFFF)
	b = le.AppendUint16(b, uint16(len(sdReq)))
	for _, s := range sdReq {
		b = le.AppendUint16(b, s)
	}
	return le.AppendUint16(b, crc)
}

// synthImage is n bytes of the tiny fixtures' pattern.
func synthImage(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 0xC0)
	}
	return b
}

type zipEntry struct {
	name    string
	data    []byte
	deflate bool
}

func mkZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		method := zip.Store
		if e.deflate {
			method = zip.Deflate
		}
		f, err := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mkManifest(parts map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"manifest": parts})
	return b
}

var appPart = map[string]any{"bin_file": "firmware.bin", "dat_file": "firmware.dat"}

// goodZip is an application package laid out like MeshCore's.
func goodZip(t *testing.T, bin, dat []byte) []byte {
	return mkZip(t,
		zipEntry{name: "manifest.json", data: mkManifest(map[string]any{"application": appPart, "dfu_version": 0.5})},
		zipEntry{name: "firmware.dat", data: dat},
		zipEntry{name: "firmware.bin", data: bin})
}

func TestCRC16(t *testing.T) {
	for _, c := range []struct {
		in   []byte
		want uint16
	}{
		{[]byte("123456789"), 0x29B1}, // CRC-16/CCITT-FALSE's check value
		{nil, 0xFFFF},
		{synthImage(1028), 0x0086}, // tiny-1028, docs/meshcore-flashing.md 5.5
	} {
		if got := CRC16(c.in); got != c.want {
			t.Errorf("CRC16(%d bytes) = %#04x, want %#04x", len(c.in), got, c.want)
		}
	}
}

func TestOpenPackageFixtures(t *testing.T) {
	for _, c := range []struct {
		name string
		size int
		crc  uint16
	}{
		{"tiny-1028", 1028, 0x0086},
		{"tiny-4096", 4096, 0x5426},
		{"synth-40000", 40000, 0x7836},
	} {
		p := readPackage(t, testdata(c.name+".zip"))
		want := InitPacket{DeviceType: 0x0052, DeviceRev: 0xFFFF, AppVersion: 0xFFFFFFFF, SDReq: []uint16{0x00B6}, CRC16: c.crc}
		if len(p.Bin) != c.size || len(p.Dat) != 14 || p.Init.DeviceType != want.DeviceType || p.Init.DeviceRev != want.DeviceRev ||
			p.Init.AppVersion != want.AppVersion || len(p.Init.SDReq) != 1 || p.Init.SDReq[0] != 0x00B6 || p.Init.CRC16 != want.CRC16 {
			t.Errorf("%s: bin %d, dat %x, init %+v", c.name, len(p.Bin), p.Dat, p.Init)
		}
	}
}

// TestOpenPackageRefuses checks every refusal, each before any port is touched.
func TestOpenPackageRefuses(t *testing.T) {
	bin := synthImage(1028)
	dat := mkDat(0x0052, []uint16{0x00B6}, CRC16(bin))
	big := make([]byte, MaxImage+4) // zeros deflate to almost nothing
	bigDat := mkDat(0x0052, []uint16{0x00B6}, CRC16(big))
	manifest := func(parts map[string]any) zipEntry {
		return zipEntry{name: "manifest.json", data: mkManifest(parts)}
	}
	files := []zipEntry{{name: "firmware.dat", data: dat}, {name: "firmware.bin", data: bin}}
	with := func(m zipEntry, more ...zipEntry) []byte { return mkZip(t, append([]zipEntry{m}, more...)...) }

	for _, c := range []struct {
		name string
		zip  []byte
		want error
		says string
	}{
		{"not a zip", []byte("<html>Not found</html>"), ErrNotPackage, "not a zip"},
		{"no manifest", mkZip(t, files...), ErrNotPackage, "no manifest.json"},
		{"manifest not JSON", with(zipEntry{name: "manifest.json", data: []byte("{")}, files...), ErrNotPackage, "not a DFU manifest"},
		{"no application", with(manifest(map[string]any{"dfu_version": 0.5}), files...), ErrNotApplication, "no application"},
		{"softdevice", with(manifest(map[string]any{"application": appPart, "softdevice": appPart}), files...), ErrNotApplication, "SoftDevice"},
		{"bootloader", with(manifest(map[string]any{"application": appPart, "bootloader": appPart}), files...), ErrNotApplication, "bootloader"},
		{"softdevice_bootloader", with(manifest(map[string]any{"softdevice_bootloader": appPart}), files...), ErrNotApplication, "SoftDevice and bootloader"},
		{"unknown part", with(manifest(map[string]any{"application": appPart, "extra": appPart}), files...), ErrNotApplication, `"extra"`},
		{"bin missing", with(manifest(map[string]any{"application": appPart}), files[0]), ErrMissingFile, "firmware.bin"},
		{"dat missing", with(manifest(map[string]any{"application": appPart}), files[1]), ErrMissingFile, "firmware.dat"},
		{"not .bin and .dat", with(manifest(map[string]any{"application": map[string]any{"bin_file": "firmware.hex", "dat_file": "firmware.dat"}}), files...), ErrNotPackage, "firmware.hex"},
		{"duplicate names", with(manifest(map[string]any{"application": appPart}), append(files, files[1])...), ErrNotPackage, "two files"},
		{"size not words", goodZip(t, bin[:1027], mkDat(0x52, []uint16{0xB6}, CRC16(bin[:1027]))), ErrBadImage, "not a multiple of 4"},
		{"empty image", goodZip(t, nil, mkDat(0x52, []uint16{0xB6}, 0xFFFF)), ErrBadImage, "empty"},
		{"image too big", mkZip(t, manifest(map[string]any{"application": appPart}), zipEntry{name: "firmware.dat", data: bigDat},
			zipEntry{name: "firmware.bin", data: big, deflate: true}), ErrBadImage, "too big"},
		{"dat too short", goodZip(t, bin, dat[:12]), ErrBadInitPacket, "at least 14"},
		{"dat too long", goodZip(t, bin, mkDat(0x52, make([]uint16, 30), CRC16(bin))), ErrBadInitPacket, "too big"},
		{"dat length wrong for sd_req", goodZip(t, bin, append(append([]byte(nil), dat...), 0, 0)), ErrBadInitPacket, "1 SoftDevice IDs and a CRC make 14"},
		{"no SoftDevice listed", goodZip(t, bin, append(mkDat(0x52, nil, CRC16(bin)), 0, 0)), ErrBadInitPacket, "no SoftDevice"},
		{"device type", goodZip(t, bin, mkDat(0x0051, []uint16{0xB6}, CRC16(bin))), ErrBadInitPacket, "0x0051"},
		{"CRC", goodZip(t, bin, mkDat(0x52, []uint16{0xB6}, CRC16(bin)^1)), ErrCRCMismatch, "0x0086"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := OpenPackage(c.zip)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, %v; want %v", p, err, c.want)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("%q doesn't say %q", err, c.says)
			}
		})
	}
}

func TestOpenPackageDeflatedAndTwoSoftDevices(t *testing.T) {
	bin := synthImage(2048)
	dat := mkDat(0x52, []uint16{SoftDeviceS140v6, SoftDeviceS140v7}, CRC16(bin))
	z := mkZip(t,
		zipEntry{name: "manifest.json", data: mkManifest(map[string]any{"application": appPart}), deflate: true},
		zipEntry{name: "firmware.dat", data: dat, deflate: true},
		zipEntry{name: "firmware.bin", data: bin, deflate: true})
	p, err := OpenPackage(z)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Init.SDReq) != 2 || p.Init.SDReq[1] != SoftDeviceS140v7 || !bytes.Equal(p.Dat, dat) {
		t.Errorf("init %+v", p.Init)
	}
	// A 16-byte .dat plus dfu.js's two zeros is 22 bytes: the bootloader keeps 16 of them after the type, the
	// whole .dat.
	if !initArrivesWhole(p.Dat) {
		t.Error("16-byte init packet would be cut")
	}
}

func TestCheckSoftDevice(t *testing.T) {
	bin := synthImage(1028)
	pkg := func(sd ...uint16) *Package {
		p, err := OpenPackage(goodZip(t, bin, mkDat(0x52, sd, CRC16(bin))))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	v6 := pkg(SoftDeviceS140v6)
	if note, err := v6.CheckSoftDevice(SoftDeviceS140v6); err != nil || note != "" {
		t.Errorf("v6 on v6: %q, %v", note, err)
	}
	note, err := v6.CheckSoftDevice(SoftDeviceS140v7)
	if !errors.Is(err, ErrWrongSoftDevice) || note != "" {
		t.Fatalf("v6 on v7: %q, %v", note, err)
	}
	if !strings.Contains(err.Error(), "S140 6.1.1") || !strings.Contains(err.Error(), "S140 7.3.0") {
		t.Errorf("message %q doesn't name both SoftDevices", err)
	}
	// Unknown: allowed, but said.
	note, err = v6.CheckSoftDevice(0)
	if err != nil || !strings.Contains(note, "isn't known") || !strings.Contains(note, "S140 6.1.1") {
		t.Errorf("unknown: %q, %v", note, err)
	}
	if _, err := pkg(SoftDeviceAny).CheckSoftDevice(0x0101); err != nil {
		t.Errorf("0xFFFE on any: %v", err)
	}
	if _, err := pkg(SoftDeviceS140v6, SoftDeviceS140v7).CheckSoftDevice(SoftDeviceS140v7); err != nil {
		t.Errorf("v6 or v7 on v7: %v", err)
	}
}

func TestInitPacketAlwaysArrivesWhole(t *testing.T) {
	// Every length ParseInitPacket accepts (12 + 2n) survives the bootloader's word truncation with dfu.js's padding.
	for n := 1; 12+2*n <= maxInitPacket; n++ {
		if dat := make([]byte, 12+2*n); !initArrivesWhole(dat) {
			t.Errorf("a %d-byte init packet would be cut", len(dat))
		}
	}
	// And the rule is real: an odd length that is 1 mod 4 would lose its last byte.
	if initArrivesWhole(make([]byte, 17)) {
		t.Error("17 bytes should be cut")
	}
}
