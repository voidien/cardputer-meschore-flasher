package ui

// The Saved files screen's goldens and keys (docs/phase2-design.md 9.11).

import (
	"testing"
	"time"

	"meshflash/internal/catalog"
)

// savedFiles are files the catalog lists (a Heltec v4 repeater's update image, which four boards share; a Xiao's
// package) and one it no longer does.
func savedFixture(t testing.TB) []catalog.Entry {
	h := flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4", "repeater", "v1.18.0")
	x := flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "repeater", "v1.17.1")
	return []catalog.Entry{
		savedFile(t, h, "v1.18.0", "flash-update", 361_000, time.Hour),
		savedFile(t, x, "v1.17.1", "flash", 352_000, 26*time.Hour),
		{Name: "old_repeater-v1.12.0-1a2b3c4.zip", URL: "https://flasher.meshcore.io/firmware/old.zip",
			Size: 340_000, SHA256: testSum, Saved: testNow.AddDate(-1, 0, 0)},
	}
}

func filesAt(t *testing.T, w, h int, saved []catalog.Entry) (*driver, *fakeBackend) {
	t.Helper()
	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	fb.saved = saved
	d := newDriver(t, w, h, fb)
	d.keys("c")
	return d, fb
}

func TestFilesList(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := filesAt(t, w, h, savedFixture(t))
		d.check(w, h)
	})
}

func TestFilesEmpty(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := filesAt(t, w, h, nil)
		d.check(w, h)
	})
}

func TestFilesDeleteAsks(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := filesAt(t, w, h, savedFixture(t))
		d.keys("j", "x")
		d.check(w, h)
	})
}

// x asks; any key but y says no and deletes nothing; y deletes the file asked about, once, and its row goes.
func TestDeleteNeedsYes(t *testing.T) {
	saved := savedFixture(t)
	d, fb := filesAt(t, 45, 10, saved)
	for _, k := range []string{"n", "?", "esc", "x"} {
		d.keys("x", k)
		if fb.calledAny("DeleteFile") || d.m.confirm != noConfirm || d.m.screen != scrFiles {
			t.Fatalf("%s to the question: deleted %v, question %v, screen %d", k, fb.deleted, d.m.confirm,
				d.m.screen)
		}
	}
	d.keys("j", "x", "y")
	if len(fb.deleted) != 1 || fb.deleted[0] != saved[1].Name {
		t.Fatalf("deleted %v", fb.deleted)
	}
	bodyLacks(d, saved[1].Name)
	if len(d.m.files.list) != 2 {
		t.Errorf("%d files listed after the delete", len(d.m.files.list))
	}
	d.keys("esc")
	if d.m.screen != scrBoard {
		t.Errorf("esc: screen %d", d.m.screen)
	}
}
