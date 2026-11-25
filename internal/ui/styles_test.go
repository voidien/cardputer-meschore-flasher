package ui

// The screens' styles and glyphs (S14, S15; docs/phase2-design.md 15): only the 16 basic colors and reverse video show
// in the CLI app, and only the glyphs internal/screen's test page shows (screen.Glyphs) are checked on the device's
// font.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	lcd "meshflash/internal/screen"
)

// sgr finds every escape sequence that sets a style.
var sgr = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// Under the CLI app's profile (ANSI): a heading reversed, the text box's cursor reversed, secondary text grey;
// nothing in 256 colors or true color, and no bold, dim, italic or underline, which the CLI app can't show.
func TestStyles(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	views := map[string]string{}
	d := chooseAt(t, 45, 10, xiaoPort("ttyACM0"), nil, nil)
	views["choose"] = d.view()
	d.keys("f")
	views["find box"] = d.view()
	views["board"] = probedBoard(t, 45, 10).view()
	views["role"] = roleAt(t, 45, 10, wioL1Port("ttyACM0"), "Seeed Studio Wio Tracker L1 Pro").view()
	f, _ := filesAt(t, 45, 10, savedFixture(t))
	views["files"] = f.view()
	h := probedBoard(t, 45, 10)
	h.keys("?")
	views["help"] = h.view()

	if !strings.Contains(views["choose"], "\x1b[7mUSB 2886:8044 matches:") {
		t.Errorf("Choose board's heading isn't reversed:\n%q", views["choose"])
	}
	if !strings.Contains(views["role"], "\x1b[7mMeshCore") {
		t.Errorf("Role's heading isn't reversed:\n%q", views["role"])
	}
	if !strings.Contains(views["help"], "\x1b[7mKeys") {
		t.Errorf("Help's heading isn't reversed")
	}
	if !strings.Contains(views["find box"], "Find: \x1b[7m \x1b[") {
		t.Errorf("the find box's cursor isn't reversed:\n%q", views["find box"])
	}
	for _, name := range []string{"choose", "board", "files"} {
		if !strings.Contains(views[name], "\x1b[90m") {
			t.Errorf("%s has no grey secondary text", name)
		}
	}
	for name, v := range views {
		for _, m := range sgr.FindAllStringSubmatch(v, -1) {
			for _, p := range strings.Split(m[1], ";") {
				switch p {
				case "", "0", "7", "27", "90", "39":
				default:
					t.Errorf("%s uses the style %q (only reverse and grey show in the CLI app)", name, m[0])
				}
			}
		}
	}
}

// Every rune in every golden is ASCII or one of screen.Glyphs (what --screen-test shows, owner question 10).
func TestGlyphs(t *testing.T) {
	allowed := map[rune]bool{}
	for _, r := range lcd.Glyphs {
		if !unicode.IsSpace(r) {
			allowed[r] = true
		}
	}
	n := 0
	err := filepath.WalkDir("testdata", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(path, ".golden") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		for i, line := range strings.Split(string(b), "\n") {
			for _, r := range line {
				if (r < 0x80 && (r >= ' ' || r == '\t')) || allowed[r] {
					continue
				}
				t.Errorf("%s line %d: %q (U+%04X) isn't one of the glyphs the device's font is checked for",
					path, i+1, r, r)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no goldens found")
	}
}
