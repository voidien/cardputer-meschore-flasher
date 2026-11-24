package screen

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/mattn/go-runewidth"
)

// Default colors: a cell's FG or BG is one of the 16 basic colors (0-15), or these for the terminal's own.
const (
	DefaultFG = 16
	DefaultBG = 17
)

// Cell is one character on the screen. Reverse swaps FG and BG when drawn.
type Cell struct {
	R       rune
	FG, BG  uint8
	Reverse bool
}

var blank = Cell{R: ' ', FG: DefaultFG, BG: DefaultBG}

// Grid is the screen as rows of cells.
type Grid [][]Cell

// NewGrid is a blank grid.
func NewGrid(cols, rows int) Grid {
	g := make(Grid, rows)
	for y := range g {
		g[y] = make([]Cell, cols)
		for x := range g[y] {
			g[y][x] = blank
		}
	}
	return g
}

// Parse lays out a frame as Bubble Tea's View gives it (lines of text with SGR color codes, which is all lipgloss
// writes) on a grid. Lines past the last row and characters past the last column are dropped (the screens wrap their
// own text, so that's only ever padding); other escape sequences are skipped.
func Parse(view string, cols, rows int) Grid {
	g := NewGrid(cols, rows)
	lines := strings.Split(strings.ReplaceAll(view, "\r\n", "\n"), "\n")
	style := blank
	for y, line := range lines {
		if y >= rows {
			break
		}
		x := 0
		for i := 0; i < len(line); {
			if line[i] == 0x1b {
				n, params, final := escape(line[i:])
				i += n
				if final == 'm' {
					style = sgr(style, params)
				}
				continue
			}
			r, w, size := cluster(line[i:])
			i += size
			if w == 0 {
				continue
			}
			if x+w > cols {
				x = cols
				continue
			}
			c := style
			c.R = r
			g[y][x] = c
			// A wide character the font can't draw anyway: the cells after its first stay blank in the same colors.
			c.R = ' '
			for k := 1; k < w; k++ {
				g[y][x+k] = c
			}
			x += w
		}
	}
	return g
}

// cluster measures the character at the start of s, which isn't an escape sequence: the rune to draw for it, the cells
// it takes and its length in bytes. A character is a grapheme cluster (a letter and its accents, an emoji and its skin
// tone, a family joined by zero-width joiners), measured as runewidth.StringWidth measures it, which is what the
// screens' wrapping planned with; measuring each rune alone made 👍🏽 four cells and pushed the rest of its line off
// the edge. It's drawn as its first rune that takes a cell. Controls take no cells.
func cluster(s string) (r rune, w, size int) {
	if s[0] < utf8.RuneSelf && (len(s) == 1 || s[1] < utf8.RuneSelf) {
		// Plain ASCII, most of every frame: one byte, one cluster (only CR LF joins, and lines are split on LF).
		if s[0] < 0x20 || s[0] == 0x7f {
			return 0, 0, 1
		}
		return rune(s[0]), 1, 1
	}
	c := graphemes.FromString(s).First() // never runs into an escape sequence: ESC is a control, which ends a cluster
	if c == "" {
		c = s[:1] // never stall on bytes the segmenter won't take
	}
	first, _ := utf8.DecodeRuneInString(c)
	if first < 0x20 || first == 0x7f || (first >= 0x80 && first < 0xa0) {
		return 0, 0, len(c)
	}
	r = first
	for _, cr := range c {
		if runewidth.RuneWidth(cr) > 0 {
			r = cr
			break
		}
	}
	return r, runewidth.StringWidth(c), len(c)
}

// escape measures an escape sequence at the start of s: its length, a CSI sequence's parameters and final byte.
func escape(s string) (n int, params string, final byte) {
	if len(s) < 2 {
		return len(s), "", 0
	}
	switch s[1] {
	case '[':
		for j := 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1, s[2:j], s[j]
			}
		}
		return len(s), "", 0
	case ']', 'P', '_', '^':
		// OSC and the other string sequences run to BEL or ST (ESC \).
		for j := 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1, "", 0
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2, "", 0
			}
		}
		return len(s), "", 0
	}
	return 2, "", 0
}

// sgr applies Select Graphic Rendition parameters ("0;7;90") to a style. Bold, dim, italic and underline don't show
// on the device's terminal either, so they're ignored here too.
func sgr(c Cell, params string) Cell {
	if params == "" {
		params = "0"
	}
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		p, err := strconv.Atoi(ps[i])
		if err != nil {
			continue
		}
		switch {
		case p == 0:
			c = blank
		case p == 7:
			c.Reverse = true
		case p == 27:
			c.Reverse = false
		case p >= 30 && p <= 37:
			c.FG = uint8(p - 30)
		case p >= 90 && p <= 97:
			c.FG = uint8(p - 90 + 8)
		case p == 39:
			c.FG = DefaultFG
		case p >= 40 && p <= 47:
			c.BG = uint8(p - 40)
		case p >= 100 && p <= 107:
			c.BG = uint8(p - 100 + 8)
		case p == 49:
			c.BG = DefaultBG
		case p == 38 || p == 48:
			// 256-color and true color aren't used (the client keeps to the 16), but their arguments must be skipped.
			if i+1 < len(ps) && ps[i+1] == "5" {
				i += 2
			} else if i+1 < len(ps) && ps[i+1] == "2" {
				i += 4
			}
		}
	}
	return c
}

// Text is the grid's characters, one line per row with trailing spaces removed: for tests and logs.
func (g Grid) Text() string {
	var b strings.Builder
	for y, row := range g {
		if y > 0 {
			b.WriteByte('\n')
		}
		var line strings.Builder
		for _, c := range row {
			line.WriteRune(c.R)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
	}
	return b.String()
}
