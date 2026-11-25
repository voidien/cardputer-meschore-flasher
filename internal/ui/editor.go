package ui

// From the CardputerZero app kit, code/internal/ui/editor.go (the owner's own code; THIRD_PARTY_NOTICES): the text
// box, unchanged but for a limit that can count UTF-8 bytes (a node's name is 1–23 bytes and a password 1–15 bytes:
// SPEC.md 4) and Clear, which wipes a password's runes before dropping them.
//
// Phase 2 contract (docs/phase2-design.md): this file is fixed and complete (the integrator's). Builder C's find box
// on Choose board and builder D's Setup text boxes both use it; builder D's editor_test.go tests it.

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// editor is a text box: typing, Backspace, and the arrows (Fn+Z/C left and right, Fn+K/N start and end). Only Enter,
// Esc and Tab mean something else, and the screen handles those (S21). The cursor is a reverse-video cell that
// doesn't blink, so the screen stays still.
type editor struct {
	text  []rune
	pos   int
	limit int  // characters (or bytes, with bytes set); 0 for none
	bytes bool // limit counts UTF-8 bytes
	mask  bool // a password: shown as one • per character
}

// newEditor is a text box holding s, limited to limit characters (0: none).
func newEditor(s string, limit int) editor {
	e := editor{limit: limit}
	e.Set(s)
	return e
}

// newBytesEditor is a text box holding s, limited to limit UTF-8 bytes: a pasted or typed rune that would go past
// the limit is dropped, never cut in half.
func newBytesEditor(s string, limit int) editor {
	e := editor{limit: limit, bytes: true}
	e.Set(s)
	return e
}

func (e *editor) Value() string { return string(e.text) }
func (e *editor) Len() int      { return len(e.text) }

// Set replaces the text, clamped to the limit, with the cursor at its end.
func (e *editor) Set(s string) {
	e.text = e.fit(nil, []rune(s))
	e.pos = len(e.text)
}

// Clear empties the box, overwriting what it held first (a password must not linger in memory longer than needed).
func (e *editor) Clear() {
	for i := range e.text {
		e.text[i] = 0
	}
	e.text, e.pos = nil, 0
}

// fit is as much of ins as still fits after have.
func (e *editor) fit(have, ins []rune) []rune {
	if e.limit <= 0 {
		return ins
	}
	if !e.bytes {
		return ins[:min(len(ins), max(0, e.limit-len(have)))]
	}
	used := 0
	for _, r := range have {
		used += utf8.RuneLen(r)
	}
	n := 0
	for n < len(ins) && used+utf8.RuneLen(ins[n]) <= e.limit {
		used += utf8.RuneLen(ins[n])
		n++
	}
	return ins[:n]
}

// key applies a key to the text and says whether it was one.
func (e *editor) key(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		runes := msg.Runes
		if msg.Type == tea.KeySpace {
			runes = []rune{' '}
		}
		var ins []rune
		lineBreak := false
		for _, r := range runes {
			if r == '\r' || r == '\n' {
				// A pasted line break is a space: a field is one line. "\r\n" or a blank line is one space too.
				if lineBreak {
					continue
				}
				r, lineBreak = ' ', true
			} else {
				lineBreak = false
			}
			if r < ' ' {
				continue
			}
			ins = append(ins, r)
		}
		ins = e.fit(e.text, ins)
		e.text = append(e.text[:e.pos], append(ins, e.text[e.pos:]...)...)
		e.pos += len(ins)
	case tea.KeyBackspace:
		if e.pos > 0 {
			e.text = append(e.text[:e.pos-1], e.text[e.pos:]...)
			e.pos--
		}
	case tea.KeyDelete:
		if e.pos < len(e.text) {
			e.text = append(e.text[:e.pos], e.text[e.pos+1:]...)
		}
	case tea.KeyLeft:
		if e.pos > 0 {
			e.pos--
		}
	case tea.KeyRight:
		if e.pos < len(e.text) {
			e.pos++
		}
	case tea.KeyHome, tea.KeyCtrlA:
		e.pos = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		e.pos = len(e.text)
	case tea.KeyCtrlU:
		e.text = e.text[e.pos:]
		e.pos = 0
	default:
		return false
	}
	return true
}

// shown is the text as displayed: dots for a password.
func (e *editor) shown() []rune {
	if !e.mask {
		return e.text
	}
	return []rune(strings.Repeat("•", len(e.text)))
}

// lines wraps the text to w cells, with the cursor drawn when focused, and says which line the cursor is on. Lines
// break after a space where they can, and inside a word only when the word is wider than the box. A space that
// falls at the end of a full line isn't drawn (unless the cursor is on it), so no line starts with one.
func (e *editor) lines(w int, focused bool) ([]string, int) {
	if w < 2 {
		w = 2
	}
	text := e.shown()
	n := len(text)
	total := n
	if focused && e.pos == n {
		total++ // the cursor after the last character is a cell of its own
	}
	at := func(i int) rune {
		if i < n {
			return text[i]
		}
		return ' '
	}
	cursor := func(i int) bool { return focused && i == e.pos }
	starts := []int{0}
	hidden := map[int]bool{}
	start, used, lastSpace := 0, 0, -1
	for i := 0; i < total; i++ {
		r := at(i)
		rw := runewidth.RuneWidth(r)
		if used+rw > w && i > start {
			if r == ' ' && i < n && !cursor(i) {
				hidden[i] = true
				start, used, lastSpace = i+1, 0, -1
				starts = append(starts, start)
				continue
			}
			word := rw
			for j := lastSpace + 1; lastSpace >= start && j < i; j++ {
				word += runewidth.RuneWidth(at(j))
			}
			if r != ' ' && lastSpace >= start && word <= w {
				start, used = lastSpace+1, word-rw
			} else {
				start, used = i, 0
			}
			lastSpace = -1
			starts = append(starts, start)
		}
		used += rw
		if r == ' ' && i < n {
			lastSpace = i
		}
	}
	starts = append(starts, total)
	lines := make([]string, 0, len(starts)-1)
	cursorLine := 0
	for k := 0; k+1 < len(starts); k++ {
		var b strings.Builder
		for i := starts[k]; i < starts[k+1]; i++ {
			switch {
			case hidden[i]:
			case cursor(i):
				cursorLine = k
				b.WriteString(reverse.Render(string(at(i))))
			default:
				b.WriteRune(at(i))
			}
		}
		lines = append(lines, b.String())
	}
	return lines, cursorLine
}

// window is at most h of lines, keeping line `keep` in view (the tail when it's near the end).
func window(lines []string, h, keep int) []string {
	if len(lines) <= h {
		return lines
	}
	start := keep - h + 1
	if start < 0 {
		start = 0
	}
	if start+h > len(lines) {
		start = len(lines) - h
	}
	return lines[start : start+h]
}
