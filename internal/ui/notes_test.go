package ui

// The Notes screen's goldens and keys (docs/phase2-design.md 9.5).

import (
	"testing"

	"meshflash/internal/catalog"
)

// notesAt is the release notes of the version given (of the Heltec v4 + Expansion Kit's repeater).
func notesAt(t *testing.T, w, h int, v *catalog.Version) *driver {
	t.Helper()
	f := flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4 + Expansion Kit (Touch)", "repeater", "v1.18.0")
	d := newDriver(t, w, h, screensBackend(t, f.port))
	d.m.flow = flow{port: f.port, dev: f.dev, fw: f.fw}
	d.m.screen = scrVersion
	d.m.openNotes(v)
	return d
}

func notesOf(t *testing.T, ver string) *catalog.Version {
	f := flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4 + Expansion Kit (Touch)", "repeater", ver)
	return f.ver
}

// A long one, scrolled two lines at a time (owner question 15) and a page with Space.
func TestNotes(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := notesAt(t, w, h, notesOf(t, "v1.14.1"))
		d.keys("j", "j", " ")
		d.check(w, h)
		if d.m.notes.top == 0 {
			t.Error("didn't scroll")
		}
	})
}

// v1.18.0's notes are one link: it breaks where it must.
func TestNotesLink(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) { notesAt(t, w, h, notesOf(t, "v1.18.0")).check(w, h) })
}

func TestNotesEmpty(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		v := *notesOf(t, "v1.18.0")
		v.Notes = ""
		notesAt(t, w, h, &v).check(w, h)
	})
}

// The lines: each line its own, a list item hanging, emphasis dropped, nothing past w.
func TestNotesLines(t *testing.T) {
	got := notesLines("Fixes:\n* **Radio** now resets after a long transmit\n  - nested item\n\nsnake_case stays", 20)
	want := []string{"Fixes:", "* Radio now resets", "  after a long", "  transmit", "  - nested item", "",
		"snake_case stays"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: %q, want %q", i, got[i], want[i])
		}
	}
}

// j/k scroll two lines, Space and b a page, Esc goes back; the scroll stops at both ends.
func TestNotesKeys(t *testing.T) {
	d := notesAt(t, 45, 10, notesOf(t, "v1.14.1"))
	d.keys("k")
	if d.m.notes.top != 0 {
		t.Errorf("k at the top: %d", d.m.notes.top)
	}
	d.keys("j")
	if scrollStep != 2 || d.m.notes.top != 2 {
		t.Errorf("j: %d (scrollStep %d), want two lines (owner question 15)", d.m.notes.top, scrollStep)
	}
	for i := 0; i < 20; i++ {
		d.keys(" ")
	}
	end := d.m.notes.top
	d.keys("b")
	if d.m.notes.top >= end {
		t.Errorf("b from the end: %d (end %d)", d.m.notes.top, end)
	}
	d.keys("esc")
	if d.m.screen != scrVersion {
		t.Errorf("esc: screen %d", d.m.screen)
	}
}
