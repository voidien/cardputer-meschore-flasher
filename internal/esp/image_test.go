package esp

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestImageChip(t *testing.T) {
	for _, tc := range []struct {
		file string
		addr uint32
		want Chip
	}{
		{"esp32-app.bin", 0x10000, ESP32},
		{"esp32-merged.bin", 0x0, ESP32},
		{"s3-app.bin", 0x10000, ESP32S3},
		{"c3-merged.bin", 0x0, ESP32C3},
	} {
		got, err := ImageChip(readTestdata(t, tc.file), tc.addr)
		if err != nil || got != tc.want {
			t.Errorf("%s at %#x: %q, %v; want %q", tc.file, tc.addr, got, err, tc.want)
		}
	}
}

func header(chipID uint16) []byte {
	h := make([]byte, 64)
	h[0], h[1], h[2], h[3] = 0xE9, 3, 2, 0x20
	binary.LittleEndian.PutUint16(h[12:], chipID)
	return h
}

func TestImageChipRefuses(t *testing.T) {
	ff := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = 0xFF
		}
		return b
	}
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	merged := func(boot, app []byte) []byte { // a merged S3-style image: bootloader at 0, app at 0x10000
		b := cat(boot, ff(AppAddress-len(boot)), app)
		return b
	}
	for _, tc := range []struct {
		name string
		data []byte
		addr uint32
	}{
		{"not an image", []byte("<!doctype html><html>not firmware</html>"), 0x10000},
		{"empty", nil, 0x0},
		{"all 0xFF", ff(0x3000), 0x0},
		{"unknown chip ID", header(77), 0x10000},
		{"no segments", func() []byte { h := header(9); h[1] = 0; return h }(), 0x10000},
		// An ESP32 app written at 0x0 would leave the ESP32 without a bootloader at 0x1000.
		{"ESP32 app at 0x0", readTestdata(t, "esp32-app.bin"), 0x0},
		// An S3 bootloader at 0x1000 is in the wrong place for an S3.
		{"S3 bootloader at 0x1000", cat(ff(0x1000), header(9)), 0x0},
		{"merged with another chip's app", merged(header(9), header(5)), 0x0},
		{"ESP32 merged image at the app address", readTestdata(t, "esp32-merged.bin"), 0x10000},
		{"S3 merged image at the app address", merged(withPartitions(header(9)), header(9)), 0x10000},
	} {
		if chip, err := ImageChip(tc.data, tc.addr); !errors.Is(err, ErrImage) {
			t.Errorf("%s: %q, %v; want ErrImage", tc.name, chip, err)
		}
	}
	// The same merged layout with matching chips is fine.
	if chip, err := ImageChip(merged(header(9), header(9)), 0x0); err != nil || chip != ESP32S3 {
		t.Errorf("matching merged image: %q, %v", chip, err)
	}
}

// withPartitions puts a partition table's first entry (nvs at 0x9000) at 0x8000 after a bootloader.
func withPartitions(boot []byte) []byte {
	b := make([]byte, 0x8000+32)
	for i := range b {
		b[i] = 0xFF
	}
	copy(b, boot)
	copy(b[0x8000:], []byte{0xAA, 0x50, 0x01, 0x02, 0x00, 0x90, 0x00, 0x00, 0x00, 0x50, 0x00, 0x00, 'n', 'v', 's', 0})
	return b
}

func TestPadImage(t *testing.T) {
	for n, want := range map[int]int{0: 0, 1: 4, 4: 4, 5002: 5004, 7127: 7128} {
		b := make([]byte, n)
		p := padImage(b)
		if len(p) != want {
			t.Errorf("pad %d: %d bytes, want %d", n, len(p), want)
		}
		for _, c := range p[n:] {
			if c != 0xFF {
				t.Errorf("pad %d: padding %x", n, p[n:])
			}
		}
	}
}
