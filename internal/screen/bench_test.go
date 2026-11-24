package screen

import (
	"os"
	"strings"
	"testing"
)

// What a key press costs on the screen: the model's frame parsed and the rows that changed drawn. Run on the device
// (a cross-compiled test binary) to see it with its CPU; it uses the real Terminus there, or LOOM_FONT elsewhere.
func benchScreen(b *testing.B) *Screen {
	path := os.Getenv("LOOM_FONT")
	if path == "" {
		path = DefaultFont
	}
	f, err := LoadFontFile(path)
	if err != nil {
		b.Skip("no Terminus here (set LOOM_FONT):", err)
	}
	f.AddStandIns()
	s, err := NewScreen(&memFB{b: make([]byte, 640*170)}, device, f)
	if err != nil {
		b.Fatal(err)
	}
	return s
}

// readerFrame is a reader screen as the client draws it: the status line, a grey beat, prose, a reversed chapter
// title and the key hints; typed changes one line, as typing in Compose does.
func readerFrame(rows int, typed string) string {
	lines := []string{"The Lighthouse at Saltmere                          ●",
		"\x1b[7m                    The Storm                    \x1b[0m",
		"\x1b[90m▸ Mara climbs the tower as the storm comes in\x1b[0m"}
	for len(lines) < rows-2 {
		lines = append(lines, "The lamps along the quay guttered as Mara climbs the")
	}
	lines = append(lines, "“"+typed+"\x1b[7m \x1b[0m”", "w beat · c go on · i ideas · ? more")
	return strings.Join(lines, "\n")
}

func BenchmarkDrawWholeFrame(b *testing.B) {
	s := benchScreen(b)
	frame := readerFrame(s.Rows, "Mara rings the bell")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.prev = nil
		if err := s.Draw(Parse(frame, s.Cols, s.Rows)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDrawAKeyTyped(b *testing.B) {
	s := benchScreen(b)
	frames := []string{readerFrame(s.Rows, "Mara rings the bel"), readerFrame(s.Rows, "Mara rings the bell")}
	s.Draw(Parse(frames[0], s.Cols, s.Rows))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Draw(Parse(frames[(i+1)%2], s.Cols, s.Rows)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClear(b *testing.B) {
	s := benchScreen(b)
	for i := 0; i < b.N; i++ {
		if err := s.Clear(); err != nil {
			b.Fatal(err)
		}
	}
}
