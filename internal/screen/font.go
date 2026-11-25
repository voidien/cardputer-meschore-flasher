// Package screen runs the client on the CardputerZero's LCD itself (`loom --screen`), drawing in a pixel font instead
// of running inside the CLI app, whose font and 45×10 grid are fixed (SPEC.md §8, Phase 5). The Bubble Tea screens are
// unchanged: each frame they produce is parsed into a grid of cells and drawn to the framebuffer, and key presses are
// read from the keyboard's input device and sent to the program as the key messages a terminal would have produced.
//
// Copied whole from the CardputerZero app kit (code/internal/screen, the owner's own code: THIRD_PARTY_NOTICES), for
// `meshflash --screen`. meshflash departs from the kit's copy in three places, and nowhere else (docs/phase2-design.md
// 18, SPEC.md "Changes to this spec"):
//
//   - The test page (testpage.go) names meshflash and shows every glyph meshflash uses (Glyphs), not Loom's
//     (owner question 10).
//   - The screen lock's Tab hold (run.go): with Options.TabHeld set, Tab acts once per press, its repeats dropped,
//     and held for HoldToLock (3 s) it sends TabHeld to the model. The kit's escHold became keyHold, with its clock
//     passed in so the holds are tested in virtual time.
//   - The dark screen (backlight.go, run.go): while the model is Dark (Dimmable), the screen is drawn black and the
//     backlight turned off, only ever the real one when drawing on /dev/fb0 (Options.Backlight); it is turned on again
//     before Run blanks the screen and returns, however the run ended.
package screen

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// DefaultFont is Terminus 6×12 as Debian's console-setup ships it, already on the device. It's read at run time rather
// than built into the binary.
const DefaultFont = "/usr/share/consolefonts/Uni2-Terminus12x6.psf.gz"

// Font is a fixed-size bitmap font: each glyph is H rows of RowBytes bytes, the leftmost pixel in the top bit.
type Font struct {
	W, H     int
	RowBytes int
	glyphs   [][]byte
	index    map[rune]int
	extra    map[rune][]byte // glyphs drawn here for characters the font lacks
}

// LoadFontFile reads a PSF font, gzipped or not.
func LoadFontFile(path string) (*Font, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadFont(f)
}

// LoadFont reads a PSF1 or PSF2 font with its Unicode table, gzipped or not.
func LoadFont(r io.Reader) (*Font, error) {
	br := bufio.NewReader(r)
	if head, err := br.Peek(2); err == nil && head[0] == 0x1f && head[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return parsePSF(io.LimitReader(zr, maxFontBytes))
	}
	return parsePSF(io.LimitReader(br, maxFontBytes))
}

// Limits on what a font file may ask for, so a damaged or hostile --font can't take the device's memory: Terminus
// 6×12 is 512 glyphs, under 10 KB, with about a thousand characters in its table.
const (
	maxFontBytes  = 1 << 20
	maxGlyphs     = 1 << 16
	maxGlyphSide  = 64
	maxCharacters = 1 << 16
)

var errNotPSF = errors.New("not a PSF font")

func parsePSF(r io.Reader) (*Font, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	switch {
	case len(data) >= 4 && data[0] == 0x36 && data[1] == 0x04:
		return parsePSF1(data)
	case len(data) >= 32 && binary.LittleEndian.Uint32(data) == 0x864ab572:
		return parsePSF2(data)
	}
	return nil, errNotPSF
}

func parsePSF1(data []byte) (*Font, error) {
	mode, h := data[2], int(data[3])
	n := 256
	if mode&1 != 0 {
		n = 512
	}
	f := &Font{W: 8, H: h, RowBytes: 1, index: map[rune]int{}}
	off := 4
	if h == 0 || len(data) < off+n*h {
		return nil, fmt.Errorf("PSF1 font is cut short")
	}
	for i := 0; i < n; i++ {
		f.glyphs = append(f.glyphs, data[off+i*h:off+(i+1)*h])
	}
	p := off + n*h
	if mode&2 == 0 {
		f.identity()
		return f, nil
	}
	for i := 0; i < n && p+1 < len(data); i++ {
		sequence := false
		for p+1 < len(data) {
			c := binary.LittleEndian.Uint16(data[p:])
			p += 2
			if c == 0xffff {
				break
			}
			// Single characters come first; 0xfffe starts sequences (a character with combining marks), which
			// aren't used: their characters aren't this glyph on their own.
			if c == 0xfffe {
				sequence = true
			}
			if !sequence {
				f.add(rune(c), i)
			}
		}
	}
	return f, nil
}

func parsePSF2(data []byte) (*Font, error) {
	hdr := func(i int) int { return int(binary.LittleEndian.Uint32(data[4*i:])) }
	headerSize, flags, n, size, h, w := hdr(2), hdr(3), hdr(4), hdr(5), hdr(6), hdr(7)
	// Each value is checked against its limit before any is multiplied, so nothing overflows.
	if w <= 0 || w > maxGlyphSide || h <= 0 || h > maxGlyphSide || n <= 0 || n > maxGlyphs ||
		headerSize < 32 || headerSize > len(data) {
		return nil, fmt.Errorf("PSF2 font has a bad header")
	}
	rowBytes := (w + 7) / 8
	if size != rowBytes*h || len(data)-headerSize < n*size {
		return nil, fmt.Errorf("PSF2 font has a bad header")
	}
	f := &Font{W: w, H: h, RowBytes: rowBytes, index: map[rune]int{}}
	for i := 0; i < n; i++ {
		f.glyphs = append(f.glyphs, data[headerSize+i*size:headerSize+(i+1)*size])
	}
	if flags&1 == 0 {
		f.identity()
		return f, nil
	}
	p := headerSize + n*size
	for i := 0; i < n && p < len(data); i++ {
		end := bytes.IndexByte(data[p:], 0xff)
		if end < 0 {
			end = len(data) - p
		}
		entry := data[p : p+end]
		p += end + 1
		// Single characters come first; 0xfe starts sequences (a character with combining marks), which aren't used.
		if j := bytes.IndexByte(entry, 0xfe); j >= 0 {
			entry = entry[:j]
		}
		for len(entry) > 0 {
			r, size := utf8.DecodeRune(entry)
			entry = entry[size:]
			if r != utf8.RuneError {
				f.add(r, i)
			}
		}
	}
	return f, nil
}

func (f *Font) identity() {
	for i := range f.glyphs {
		f.add(rune(i), i)
	}
}

func (f *Font) add(r rune, i int) {
	if _, ok := f.index[r]; !ok && len(f.index) < maxCharacters {
		f.index[r] = i
	}
}

// Has reports whether the font, or a glyph drawn here, covers r.
func (f *Font) Has(r rune) bool {
	if _, ok := f.index[r]; ok {
		return true
	}
	_, ok := f.extra[r]
	return ok
}

// Glyph is the bitmap for r: the font's, else one drawn here, else the font's "?", else blank.
func (f *Font) Glyph(r rune) []byte {
	if i, ok := f.index[r]; ok {
		return f.glyphs[i]
	}
	if g, ok := f.extra[r]; ok {
		return g
	}
	if i, ok := f.index['?']; ok {
		return f.glyphs[i]
	}
	return make([]byte, f.RowBytes*f.H)
}

// AddStandIns draws the characters the client shows that Terminus 6×12 lacks (docs/device.md, "Smaller text"): the
// pen in "✎ written by you" and the offline mark ○. Only for a 6×12 font; another size keeps its "?".
func (f *Font) AddStandIns() {
	if f.W != 6 || f.H != 12 {
		return
	}
	f.extra = map[rune][]byte{
		'○': rows6(
			"......",
			"......",
			"......",
			"..##..",
			".#..#.",
			"#....#",
			"#....#",
			".#..#.",
			"..##..",
			"......",
			"......",
			"......",
		),
		'✎': rows6(
			"......",
			"....#.",
			"...#.#",
			"..#.#.",
			"..##..",
			".#.#..",
			".##...",
			".#....",
			"#.....",
			"......",
			"......",
			"......",
		),
	}
}

// rows6 turns a picture of a 6-pixel-wide glyph ('#' is ink) into font rows.
func rows6(lines ...string) []byte {
	out := make([]byte, len(lines))
	for y, l := range lines {
		for x := 0; x < len(l) && x < 8; x++ {
			if l[x] == '#' {
				out[y] |= 0x80 >> x
			}
		}
	}
	return out
}
