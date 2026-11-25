package ui

// The Update catalog screen's goldens and keys (docs/phase2-design.md 9.10).

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
)

// updateAt is u on the Board, UpdateCatalog answered as f sets it.
func updateAt(t *testing.T, w, h int, f func(fb *fakeBackend)) (*driver, *fakeBackend) {
	t.Helper()
	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	if f != nil {
		f(fb)
	}
	d := newDriver(t, w, h, fb)
	d.keys("u")
	return d, fb
}

// The fetch under way, with its time (S17).
func TestUpdateFetching(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		fb := screensBackend(t, heltecV4Port("ttyACM0"))
		d := newDriver(t, w, h, fb)
		d.hold(true)
		d.keys("u")
		d.advance(3 * time.Second)
		d.check(w, h)
		d.releaseAll()
	})
}

func TestUpdateDone(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := updateAt(t, w, h, func(fb *fakeBackend) { fb.updated = testCatalog(t) })
		d.check(w, h)
	})
}

func TestUpdateOffline(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := updateAt(t, w, h, func(fb *fakeBackend) {
			fb.updateErr = &catalog.FetchError{URL: "https://flasher.meshcore.io/config.json",
				Err: fmt.Errorf("%w: dial tcp: lookup flasher.meshcore.io: no such host", catalog.ErrUnreachable)}
		})
		d.check(w, h)
	})
}

func TestUpdateHTTPError(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := updateAt(t, w, h, func(fb *fakeBackend) {
			fb.updateErr = &catalog.FetchError{URL: "https://flasher.meshcore.io/config.json", Status: 503,
				Err: fmt.Errorf("%w: HTTP 503", catalog.ErrBadAnswer)}
		})
		d.check(w, h)
	})
}

// A failed or stopped update over a damaged catalog says it is still damaged (SPEC 4 Update catalog), not that it
// stays or that there is none.
func TestUpdateDamagedStaysDamaged(t *testing.T) {
	damaged := func(fb *fakeBackend) {
		fb.cat, fb.catErr = nil, fmt.Errorf("%w: config.json no longer matches what was saved", catalog.ErrDamaged)
		fb.updateErr = &catalog.FetchError{URL: "https://flasher.meshcore.io/config.json",
			Err: fmt.Errorf("%w: dial tcp: lookup flasher.meshcore.io: no such host", catalog.ErrUnreachable)}
	}
	d, _ := updateAt(t, 45, 10, damaged)
	bodyHas(d, "The saved catalog is still damaged.", "No network, or the server didn't answer.")
	bodyLacks(d, "The saved catalog stays.", "No catalog is saved yet.")

	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	damaged(fb)
	d = newDriver(t, 45, 10, fb)
	d.hold(true)
	d.keys("u", "esc")
	if d.m.screen != scrBoard {
		t.Fatalf("Esc while fetching: screen %d", d.m.screen)
	}
	// The note wraps: compare the words.
	if v := strings.Join(strings.Fields(d.view()), " "); !strings.Contains(v,
		"Update stopped. The saved catalog is still damaged.") {
		t.Errorf("the Board after Esc:\n%s", d.view())
	}
	d.releaseAll()
}

// "Fetching…" goes when the answer comes, with no key (S16), and Help closes as it does at a Job's end.
func TestUpdateMessagesKeepUp(t *testing.T) {
	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	fb.updated = testCatalog(t)
	d := newDriver(t, 45, 10, fb)
	d.hold(true)
	d.keys("u", "?")
	if !d.m.help {
		t.Fatal("? didn't open Help")
	}
	d.releaseAll()
	if d.m.help {
		t.Error("Help stayed open when the answer came")
	}
	bodyLacks(d, "Fetching")
	bodyHas(d, "72 boards, newest v1.18.0")
}

// Esc while fetching stops it and goes back; the saved catalog stays; a failed update keeps the catalog and u tries
// again; a good one replaces it.
func TestUpdateKeys(t *testing.T) {
	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	before := fb.cat
	fb.updateErr = errNetworkDown
	d := newDriver(t, 45, 10, fb)
	d.hold(true)
	d.keys("u", "esc")
	if d.m.screen != scrBoard || d.m.upd.fetching {
		t.Fatalf("esc while fetching: screen %d", d.m.screen)
	}
	if d.m.note != "Update stopped. The saved catalog stays." {
		t.Errorf("esc while fetching says %q", d.m.note)
	}
	d.releaseAll()
	if d.m.cat != before || d.m.screen != scrBoard {
		t.Errorf("the stopped update changed the catalog or the screen (%d)", d.m.screen)
	}
	d.keys("u")
	bodyHas(d, "The saved catalog stays.", "No network")
	if d.m.cat != before {
		t.Error("a failed update replaced the catalog")
	}
	newer := *testCatalog(t)
	newer.Fetched = testNow
	fb.set(func(f *fakeBackend) { f.updated, f.updateErr = &newer, nil })
	d.keys("u")
	if d.m.cat != &newer {
		t.Error("the new catalog isn't the one shown")
	}
	d.keys("enter")
	if d.m.screen != scrBoard {
		t.Errorf("enter: screen %d", d.m.screen)
	}
	if callCount(fb, "UpdateCatalog") != 3 {
		t.Errorf("%d updates", callCount(fb, "UpdateCatalog"))
	}
}

// The catalog line after the first start with none, and after an update.
func TestUpdateFromNoCatalog(t *testing.T) {
	fb := screensBackend(t)
	fb.cat, fb.updated = nil, testCatalog(t)
	d := newDriver(t, 45, 10, fb)
	bodyHas(d, "No catalog saved. Join Wi-Fi and press u.")
	d.keys("u", "enter")
	bodyHas(d, "Catalog from 24 Nov: newest v1.18.0")
}

// errNetworkDown is UpdateCatalog with no network.
var errNetworkDown = &catalog.FetchError{URL: "https://flasher.meshcore.io/config.json", Err: catalog.ErrUnreachable}

// With no catalog saved, a failed update never says a saved one stays: the Board and Update catalog agree.
func TestUpdateFailsWithNoCatalog(t *testing.T) {
	fb := screensBackend(t)
	fb.cat, fb.updateErr = nil, errNetworkDown
	d := newDriver(t, 45, 10, fb)
	bodyHas(d, "No catalog saved.")
	d.keys("u")
	bodyHas(d, "No catalog is saved yet.", "No network")
	bodyLacks(d, "The saved catalog stays.")
}
