package esp

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The embedded stubs are the legacy flasher stub v1.3.0 as esptool-js 0.5.4 bundles it: the SHA-256 of each
// decoded segment and the entry point, as the research checked them against esptool v4.8.x
// (research tools/bundled_stubs.json; stubs/README.md).
func TestStubs(t *testing.T) {
	for _, tc := range []struct {
		chip                 Chip
		text, data           string
		textStart, dataStart uint32
		entry                uint32
		textLen, dataLen     int
	}{
		{ESP32, "c3d3ddfa74e6b41710863f07a2d0496bb6f758a2011bf1d9cdd5b280479db6b9", "d28a40ec32f40afd656ddbc1a89b0cc042203548dfa255617b55067d80350f2f",
			0x400be000, 0x3ffdeba8, 0x400be5ec, 3532, 156},
		{ESP32S3, "14ab7c0beb0520624f7c14f4547f9c42657e651f50c19106cb510477751c8e29", "0ed9e45f3c15a144281066dc7c50db6ae4e936bad0fea257d7453a6ec16752f5",
			0x40378000, 0x3fcb2bfc, 0x40378a80, 5292, 252},
		{ESP32C3, "9b8c66b2975c18c28464869050541b847e683fe0b5345b3163176ee55c7372f7", "d8a11ba0605663eda7b3f00331ff62d904762faf91507fdfbe25ca539b1ba214",
			0x40380000, 0x3fc96bb4, 0x403806d0, 3868, 160},
	} {
		s, err := loadStub(chipByName(tc.chip).stubFile)
		if err != nil {
			t.Fatal(err)
		}
		sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
		if sum(s.text) != tc.text || sum(s.data) != tc.data || len(s.text) != tc.textLen || len(s.data) != tc.dataLen {
			t.Errorf("%s: text %s (%d), data %s (%d)", tc.chip, sum(s.text), len(s.text), sum(s.data), len(s.data))
		}
		if s.textStart != tc.textStart || s.dataStart != tc.dataStart || s.entry != tc.entry {
			t.Errorf("%s: addresses %#x %#x %#x", tc.chip, s.textStart, s.dataStart, s.entry)
		}
	}
}
