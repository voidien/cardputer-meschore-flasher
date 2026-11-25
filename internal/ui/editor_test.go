package ui

// Tests of the fixed text box (editor.go). TestEditorKeys and TestEditorWraps are the CardputerZero app kit's
// (code/internal/ui/keys_test.go and ui_test.go, the owner's own code; THIRD_PARTY_NOTICES), with the kit's runes
// helper renamed setupRunes (design section 11's prefixes); the byte limit and Clear are meshflash's additions.

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func setupRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// Every key the text box answers to, at the edges too: nothing panics at either end of the text, and the limit holds
// for pasted text as well as typed.
func TestEditorKeys(t *testing.T) {
	e := newEditor("lamp", 0)
	for _, k := range []tea.KeyType{tea.KeyLeft, tea.KeyLeft} {
		e.key(tea.KeyMsg{Type: k})
	}
	e.key(setupRunes("XY"))
	if e.Value() != "laXYmp" || e.pos != 4 {
		t.Fatalf("insert in the middle: %q at %d", e.Value(), e.pos)
	}
	e.key(tea.KeyMsg{Type: tea.KeyDelete})
	e.key(tea.KeyMsg{Type: tea.KeyBackspace})
	if e.Value() != "laXp" || e.pos != 3 {
		t.Fatalf("Delete then Backspace: %q at %d", e.Value(), e.pos)
	}
	e.key(tea.KeyMsg{Type: tea.KeyHome})
	e.key(tea.KeyMsg{Type: tea.KeyBackspace}) // nothing before the start
	e.key(tea.KeyMsg{Type: tea.KeyLeft})
	if e.Value() != "laXp" || e.pos != 0 {
		t.Fatalf("at the start: %q at %d", e.Value(), e.pos)
	}
	e.key(tea.KeyMsg{Type: tea.KeyEnd})
	e.key(tea.KeyMsg{Type: tea.KeyDelete}) // nothing after the end
	e.key(tea.KeyMsg{Type: tea.KeyRight})
	if e.Value() != "laXp" || e.pos != 4 {
		t.Fatalf("at the end: %q at %d", e.Value(), e.pos)
	}
	e.key(tea.KeyMsg{Type: tea.KeyCtrlA})
	if e.pos != 0 {
		t.Fatalf("Ctrl+A: at %d", e.pos)
	}
	e.key(tea.KeyMsg{Type: tea.KeyCtrlE})
	e.key(tea.KeyMsg{Type: tea.KeyLeft})
	e.key(tea.KeyMsg{Type: tea.KeyCtrlU}) // deletes what's before the cursor
	if e.Value() != "p" || e.pos != 0 {
		t.Fatalf("Ctrl+U: %q at %d", e.Value(), e.pos)
	}
	if e.key(tea.KeyMsg{Type: tea.KeyEnter}) || e.key(tea.KeyMsg{Type: tea.KeyTab}) ||
		e.key(tea.KeyMsg{Type: tea.KeyEsc}) {
		t.Error("Enter, Tab and Esc belong to the screen, not the text box")
	}

	paste := newEditor("", 0)
	paste.key(setupRunes("one\r\ntwo\tthree\x07\n\nfour"))
	if paste.Value() != "one twothree four" {
		t.Errorf("a pasted line break is a space and control characters are dropped: %q", paste.Value())
	}

	limited := newEditor("abc", 5)
	limited.key(tea.KeyMsg{Type: tea.KeyHome})
	limited.key(setupRunes("123456"))
	if limited.Value() != "12abc" || limited.pos != 2 {
		t.Errorf("a paste stops at the limit: %q at %d", limited.Value(), limited.pos)
	}
	limited.key(tea.KeyMsg{Type: tea.KeySpace})
	if limited.Value() != "12abc" {
		t.Errorf("a space past the limit: %q", limited.Value())
	}
	limited.Set("a much longer text")
	if limited.Value() != "a muc" || limited.pos != 5 {
		t.Errorf("Set keeps to the limit: %q at %d", limited.Value(), limited.pos)
	}

	pw := newEditor("hunter2", 0)
	pw.mask = true
	if lines, _ := pw.lines(45, false); len(lines) != 1 || lines[0] != "•••••••" {
		t.Errorf("a password shows as dots: %q", lines)
	}
}

func TestEditorWraps(t *testing.T) {
	e := newEditor("Mara lights the lamp and a supercalifragilistic word", 0)
	lines, at := e.lines(12, true)
	want := []string{"Mara lights ", "the lamp and", "a ", "supercalifra", "gilistic ", "word "}
	if strings.Join(lines, "|") != strings.Join(want, "|") || at != 5 {
		t.Errorf("lines %q, cursor on %d", lines, at)
	}
	e.pos = 4 // on the space after "Mara"
	if lines, at := e.lines(12, true); at != 0 || width(lines[0]) > 12 {
		t.Errorf("cursor on line %d: %q", at, lines)
	}
	e.pos = 24 // the space after "and", at the end of a full line: shown at the start of the next
	if lines, at := e.lines(12, true); at != 2 || !strings.HasPrefix(lines[2], " ") {
		t.Errorf("cursor on line %d: %q", at, lines)
	}
}

// A bytes editor counts UTF-8 bytes, as the node does (a name 1–23 bytes, a password 1–15): a rune that would go
// past the limit is dropped whole, typed, pasted or Set, never cut in half; and a rune editor still counts runes.
func TestEditorByteLimit(t *testing.T) {
	name := newBytesEditor("", 23)
	name.key(setupRunes("Hilltop 2"))
	name.key(setupRunes(" ääää ääää")) // 1 + 8 + 1 + 8 bytes: 27 in all, past the limit
	if got := name.Value(); len(got) > 23 || !utf8.ValidString(got) {
		t.Fatalf("typed past the limit: %q is %d bytes", got, len(got))
	}
	if got := name.Value(); got != "Hilltop 2 ääää ää" {
		t.Errorf("the box kept %q (%d bytes), want the runes that fit whole", got, len(got))
	}
	name.key(setupRunes("x")) // full: 9 + 1 + 8 + 1 + 4 = 23 bytes
	if name.Value() != "Hilltop 2 ääää ää" {
		t.Errorf("a full box took another rune: %q", name.Value())
	}

	pw := newBytesEditor("", 15)
	pw.key(setupRunes("abcdefghijklmn")) // 14 bytes
	pw.key(setupRunes("é"))              // 2 more would be 16
	if pw.Value() != "abcdefghijklmn" {
		t.Errorf("a two-byte rune past a 15-byte limit was cut or kept: %q", pw.Value())
	}
	pw.key(setupRunes("z"))
	if pw.Value() != "abcdefghijklmnz" {
		t.Errorf("one byte still fitted: %q", pw.Value())
	}

	set := newBytesEditor("€€€€€€€€", 23) // 24 bytes
	if got := set.Value(); got != "€€€€€€€" || set.pos != 7 {
		t.Errorf("Set clamps by bytes: %q at %d", got, set.pos)
	}

	mid := newBytesEditor("abc", 4)
	mid.key(tea.KeyMsg{Type: tea.KeyHome})
	mid.key(setupRunes("ä")) // 2 bytes after 3: no room
	mid.key(setupRunes("x"))
	if mid.Value() != "xabc" || mid.pos != 1 {
		t.Errorf("an insert at the start counts the bytes after the cursor too: %q at %d", mid.Value(), mid.pos)
	}

	runes := newEditor("", 3)
	runes.key(setupRunes("äöüß"))
	if runes.Value() != "äöü" {
		t.Errorf("a rune limit counts runes: %q", runes.Value())
	}
}

// Clear overwrites what the box held before dropping it (a password mustn't linger), and leaves an empty box that
// still works, limit and mask kept.
func TestEditorClear(t *testing.T) {
	pw := newBytesEditor("", 15)
	pw.mask = true
	pw.key(setupRunes("s3cret"))
	held := pw.text // the runes the box had
	pw.Clear()
	for i, r := range held[:cap(held)][:6] {
		if r != 0 {
			t.Errorf("rune %d still holds %q after Clear", i, r)
		}
	}
	if pw.Value() != "" || pw.pos != 0 || pw.Len() != 0 {
		t.Errorf("Clear left %q at %d", pw.Value(), pw.pos)
	}
	pw.key(setupRunes("again, and longer than fifteen"))
	if pw.Value() != "again, and long" {
		t.Errorf("after Clear the limit is kept: %q", pw.Value())
	}
	if lines, _ := pw.lines(40, false); lines[0] != strings.Repeat("•", 15) {
		t.Errorf("after Clear the mask is kept: %q", lines)
	}
	var none editor
	none.Clear() // a box never used
	none.Clear() // and twice
	if none.Value() != "" {
		t.Errorf("an empty box cleared: %q", none.Value())
	}
}
