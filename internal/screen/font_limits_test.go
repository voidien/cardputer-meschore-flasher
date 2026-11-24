package screen

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// A damaged or hostile --font is refused with an error, not a crash or the device's memory.
func TestFontHeadersOutOfBounds(t *testing.T) {
	header := func(n, size, h, w uint32) []byte {
		var b bytes.Buffer
		for _, v := range []uint32{0x864ab572, 0, 32, 0, n, size, h, w} {
			binary.Write(&b, binary.LittleEndian, v)
		}
		b.Write(make([]byte, 4096))
		return b.Bytes()
	}
	cases := map[string][]byte{
		"a count and size that overflow when multiplied": header(1<<32-1, 1<<31, 1<<31, 8),
		"more glyphs than any font has":                  header(1<<20, 1, 1, 1),
		"glyphs larger than a screen's worth":            header(1, 4*1024, 1024, 32),
		"more glyphs than the file holds":                header(8192, 1, 1, 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadFont(bytes.NewReader(data)); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// A PSF1 font's Unicode table may follow a glyph's characters with sequences (0xFFFE, then a character and its
// combining marks); those characters aren't that glyph on their own.
func TestPSF1SequencesArentSingleCharacters(t *testing.T) {
	const h = 8
	var b bytes.Buffer
	b.Write([]byte{0x36, 0x04, 0x02, h}) // PSF1, 256 glyphs, with a Unicode table
	for i := 0; i < 256; i++ {
		g := make([]byte, h)
		g[0] = byte(i) // tell the glyphs apart
		b.Write(g)
	}
	table := make([][]uint16, 256)
	table['X'] = []uint16{'X', 0xfffe, 'B', 0x0301} // X, then the sequence B + combining acute
	table['B'] = []uint16{'B'}
	for _, entry := range table {
		for _, c := range entry {
			binary.Write(&b, binary.LittleEndian, c)
		}
		binary.Write(&b, binary.LittleEndian, uint16(0xffff))
	}
	f, err := LoadFont(&b)
	if err != nil {
		t.Fatal(err)
	}
	if g := f.Glyph('B'); g[0] != 'B' {
		t.Errorf("B is drawn with glyph %d, want its own (%d)", g[0], 'B')
	}
	if g := f.Glyph('X'); g[0] != 'X' {
		t.Errorf("X is drawn with glyph %d", g[0])
	}
}
