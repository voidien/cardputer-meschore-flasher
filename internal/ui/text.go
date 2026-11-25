package ui

// From the CardputerZero app kit, code/internal/ui/text.go (the owner's own code; THIRD_PARTY_NOTICES): the kit's
// wrapping, cutting and wording helpers, unchanged but for proseLines (left out: release notes aren't prose) and the
// helpers added at the end for meshflash (indented and scrolled from the kit's table.go and tabletop.go, heading,
// byteSize, took, day).
//
// Phase 2 contract (docs/phase2-design.md): this file is fixed (the integrator's). Builders C and D both use these;
// a builder's own helpers go in their own files (builder D's named setup…, preset… or pick…).

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// Only the 16 basic colors and reverse video show in the CLI app (docs/device.md): bold, dim and underline don't.
var (
	grey    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	reverse = lipgloss.NewStyle().Reverse(true)
)

// heading draws a group heading ("MeshCore", "All boards:", Help's "Keys"): in reverse video, as S14 says for
// headings (owner question 12; the selected row keeps its "▸ " mark, as the SPEC mock-ups show). Style after
// wrapping and cutting: s is one line.
func heading(s string) string { return reverse.Render(s) }

// width is how many cells s takes.
func width(s string) int { return ansi.StringWidth(s) }

// cut shortens a line that's too wide, ending it with "…". For labels only: text is wrapped, never cut.
func cut(s string, w int) string {
	if w < 1 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// fitHint gives the first key hint that fits in w cells, else the last (shortest) one, which the caller cuts.
func fitHint(w int, hints ...string) string {
	for _, h := range hints {
		if width(h) <= w {
			return h
		}
	}
	return hints[len(hints)-1]
}

// wrap breaks plain text into lines of at most w cells: words stay whole where they fit, longer ones are broken.
// Each line of the input starts a new line.
func wrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, wrapLine(line, w)...)
	}
	return out
}

func wrapLine(s string, w int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	var cur strings.Builder
	curW := 0
	for _, word := range words {
		ww := cells(word)
		for ww > w {
			if curW > 0 {
				lines = append(lines, cur.String())
				cur.Reset()
				curW = 0
			}
			head, rest := cutWidth(word, w)
			lines = append(lines, head)
			word, ww = rest, cells(rest)
		}
		switch {
		case ww == 0:
		case curW == 0:
			cur.WriteString(word)
			curW = ww
		case curW+1+ww <= w:
			cur.WriteByte(' ')
			cur.WriteString(word)
			curW += 1 + ww
		default:
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(word)
			curW = ww
		}
	}
	if curW > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

// cells is how many cells a word takes: its length when it's plain ASCII, as most prose is.
func cells(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 || s[i] < ' ' {
			return runewidth.StringWidth(s)
		}
	}
	return len(s)
}

// cutWidth splits s after at most w cells (at least one rune).
func cutWidth(s string, w int) (string, string) {
	used := 0
	for i, r := range s {
		rw := runewidth.RuneWidth(r)
		if used+rw > w && i > 0 {
			return s[:i], s[i:]
		}
		used += rw
	}
	return s, ""
}

// plain drops Markdown emphasis markers (*x*, **x**, _x_, __x__), which the CLI app can't show as bold or italic.
// It pairs runs of the same marker in one pass: a run opens when text follows it and closes when text precedes it,
// and _ only at a word's edge, so "2 * 3", "snake_case" and an unmatched * stay as they are.
func plain(s string) string {
	if !strings.ContainsAny(s, "*_") {
		return s
	}
	return stripMarks(stripMarks(s, '*'), '_')
}

func stripMarks(s string, mark byte) string {
	type run struct{ at, n int }
	var open []run
	var drop []run
	// The characters either side of a run, whole (a curly quote is punctuation, ë a letter); the text's edges count
	// as spaces.
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; i < len(s); {
		if s[i] != mark {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == mark {
			j++
		}
		before, after := ' ', ' '
		if i > 0 {
			before, _ = utf8.DecodeLastRuneInString(s[:i])
		}
		if j < len(s) {
			after, _ = utf8.DecodeRuneInString(s[j:])
		}
		canOpen, canClose := !unicode.IsSpace(after), !unicode.IsSpace(before)
		if mark == '_' {
			canOpen, canClose = canOpen && !word(before), canClose && !word(after)
		}
		closed := false
		for k := len(open) - 1; canClose && k >= 0; k-- {
			if open[k].n == j-i {
				drop = append(drop, open[k], run{i, j - i})
				open, closed = open[:k], true
				break
			}
		}
		if !closed && canOpen {
			open = append(open, run{i, j - i})
		}
		i = j
	}
	if len(drop) == 0 {
		return s
	}
	sort.Slice(drop, func(i, j int) bool { return drop[i].at < drop[j].at })
	var b strings.Builder
	b.Grow(len(s))
	from := 0
	for _, r := range drop {
		b.WriteString(s[from:r.at])
		from = r.at + r.n
	}
	b.WriteString(s[from:])
	return b.String()
}

// center puts s in the middle of w cells (left padding only).
func center(s string, w int) string {
	pad := (w - width(s)) / 2
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

// ago words a time: "just now", "5 min ago", "2 days ago", "3 Mar 2026".
func ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 2*time.Hour:
		return "1 hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d/time.Hour))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
	}
	return t.Local().Format("2 Jan 2006")
}

// clock words when a saved copy was made: "14:32" today, else "3 Mar 14:32".
func clock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("2 Jan 15:04")
}

// plural words a count: "1 passage", "412 passages".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// elapsed words a duration as m:ss.
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// indented wraps text to w-2 cells and puts two spaces before each line (wrap drops leading spaces, so an indent goes
// on after wrapping). From the kit's table.go.
func indented(text string, w int) []string {
	lines := wrap(text, w-2)
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return lines
}

// scrolled is the h lines of lines starting at *top, after moving *top so that it is in range (a scrolling text's
// View clamps its offset this way). From the kit's tabletop.go.
func scrolled(lines []string, top *int, h int) []string {
	*top = max(0, min(*top, len(lines)-h))
	return lines[*top:min(len(lines), *top+h)]
}

// byteSize words a size as the screens show sizes, in decimal units: "980 B", "361 KB", "1.3 MB" (999 600 bytes is
// "1.0 MB", never "1000 KB").
func byteSize(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 999_500:
		return fmt.Sprintf("%d KB", (n+500)/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}

// took words how long a finished step took: "10 s" under a minute, else as elapsed ("1:05"), rounded to the second
// first (59.6 s is "1:00", never "60 s").
func took(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return elapsed(d)
}

// day words a date: "24 Nov" this year, "24 Nov 2025" another.
func day(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.Year() == now.Year() {
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}
