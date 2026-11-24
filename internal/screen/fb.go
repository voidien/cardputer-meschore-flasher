package screen

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Geometry is a framebuffer's size: Width×Height pixels, Stride bytes a row, 16 bits a pixel (RGB565, little-endian).
type Geometry struct {
	Width, Height, Stride int
}

// FramebufferGeometry reads fb0's size from sysfs (the device's is 320×170, 640 bytes a row).
func FramebufferGeometry(sys string) (Geometry, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(sys + "/" + name)
		return strings.TrimSpace(string(b)), err
	}
	size, err := read("virtual_size")
	if err != nil {
		return Geometry{}, err
	}
	w, h, ok := strings.Cut(size, ",")
	if !ok {
		return Geometry{}, fmt.Errorf("odd framebuffer size %q", size)
	}
	var g Geometry
	if g.Width, err = strconv.Atoi(w); err != nil {
		return Geometry{}, err
	}
	if g.Height, err = strconv.Atoi(h); err != nil {
		return Geometry{}, err
	}
	bpp, err := read("bits_per_pixel")
	if err != nil {
		return Geometry{}, err
	}
	if bpp != "16" {
		return Geometry{}, fmt.Errorf("the framebuffer has %s bits a pixel; only 16 is supported", bpp)
	}
	g.Stride = g.Width * 2
	if s, err := read("stride"); err == nil {
		if n, err := strconv.Atoi(s); err == nil && n >= g.Width*2 {
			g.Stride = n
		}
	}
	return g, nil
}

// palette is the 16 basic colors plus the default foreground and background, as RGB565. Close to what the CLI app
// shows: grey (8) is a mid grey, white (7) a light grey, and the default text a near white on black.
var palette = [18]uint16{
	rgb(0, 0, 0), rgb(205, 49, 49), rgb(13, 188, 121), rgb(229, 229, 16),
	rgb(36, 114, 200), rgb(188, 63, 188), rgb(17, 168, 205), rgb(204, 204, 204),
	rgb(128, 128, 128), rgb(241, 76, 76), rgb(35, 209, 139), rgb(245, 245, 67),
	rgb(59, 142, 234), rgb(214, 112, 214), rgb(41, 184, 219), rgb(255, 255, 255),
	DefaultFG: rgb(230, 230, 230),
	DefaultBG: rgb(0, 0, 0),
}

func rgb(r, g, b uint8) uint16 {
	return uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
}

// Screen draws grids of cells to the whole framebuffer, centred. Only the text rows that changed since the last draw
// are written. The launcher's bar isn't kept: it stops drawing while an app it started runs, so the bar would only be
// a frozen picture (docs/device.md, "Apps started from the home screen").
type Screen struct {
	out        io.WriterAt
	geo        Geometry
	font       *Font
	Cols, Rows int
	left, top  int
	prev       Grid
	band       []byte // one text row's pixels, Stride × font height
}

// NewScreen fits as many cells as the font allows into the framebuffer (53×14 in Terminus 6×12 on the device).
func NewScreen(out io.WriterAt, geo Geometry, font *Font) (*Screen, error) {
	cols, rows := geo.Width/font.W, geo.Height/font.H
	if cols < 20 || rows < 5 {
		return nil, fmt.Errorf("a %d×%d font gives only %d×%d on this screen", font.W, font.H, cols, rows)
	}
	return &Screen{
		out: out, geo: geo, font: font, Cols: cols, Rows: rows,
		left: (geo.Width - cols*font.W) / 2,
		top:  (geo.Height - rows*font.H) / 2,
		band: make([]byte, geo.Stride*font.H),
	}, nil
}

// Clear paints the whole screen, margins too, in the default background and forgets what was drawn. Whatever was
// there before (the launcher's "loading" picture) goes.
func (s *Screen) Clear() error {
	s.prev = nil
	bg := palette[DefaultBG]
	line := make([]byte, s.geo.Stride)
	for i := 0; i+1 < s.geo.Width*2; i += 2 {
		line[i], line[i+1] = byte(bg), byte(bg>>8)
	}
	for y := 0; y < s.geo.Height; y++ {
		if _, err := s.out.WriteAt(line, int64(y*s.geo.Stride)); err != nil {
			return err
		}
	}
	return nil
}

// Draw writes the rows of g that differ from the last grid drawn.
func (s *Screen) Draw(g Grid) error {
	for y := 0; y < s.Rows && y < len(g); y++ {
		if s.prev != nil && y < len(s.prev) && sameRow(s.prev[y], g[y]) {
			continue
		}
		if err := s.drawRow(y, g[y]); err != nil {
			return err
		}
	}
	s.prev = cloneGrid(g)
	return nil
}

func (s *Screen) drawRow(y int, row []Cell) error {
	fw, fh := s.font.W, s.font.H
	// The band covers the full width; the margins either side are background.
	bg := palette[DefaultBG]
	for i := 0; i < len(s.band); i += 2 {
		s.band[i], s.band[i+1] = byte(bg), byte(bg>>8)
	}
	for x := 0; x < s.Cols; x++ {
		c := blank
		if x < len(row) {
			c = row[x]
		}
		fg, bgc := palette[c.FG%18], palette[c.BG%18]
		if c.Reverse {
			fg, bgc = bgc, fg
		}
		glyph := s.font.Glyph(c.R)
		for gy := 0; gy < fh; gy++ {
			rowBits := glyph[gy*s.font.RowBytes : (gy+1)*s.font.RowBytes]
			base := gy*s.geo.Stride + (s.left+x*fw)*2
			for gx := 0; gx < fw; gx++ {
				px := bgc
				if rowBits[gx/8]&(0x80>>(gx%8)) != 0 {
					px = fg
				}
				s.band[base+gx*2], s.band[base+gx*2+1] = byte(px), byte(px>>8)
			}
		}
	}
	_, err := s.out.WriteAt(s.band, int64((s.top+y*fh)*s.geo.Stride))
	return err
}

func sameRow(a, b []Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneGrid(g Grid) Grid {
	out := make(Grid, len(g))
	for y := range g {
		out[y] = append([]Cell(nil), g[y]...)
	}
	return out
}
