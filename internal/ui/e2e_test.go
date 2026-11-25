package ui

// End-to-end tests of the screens over the real orchestration (flash.New) on builder B's fake world
// (docs/phase2-design.md 15): the boards, the flasher sites and the battery are the world's, the screens' clock is
// the driver's, and the world's Jobs run to their end on its virtual clock while settle waits (no real sleeps). Each
// test skips itself while the world is still the contract's placeholder (it reports "not built yet"), and checks at
// its end, through the driver, that the backend broke none of the world's rules.

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/nodecli"
)

// requireWorld skips the test while builder B's world isn't built (its placeholder's Deps say so).
func requireWorld(t *testing.T) {
	t.Helper()
	if _, err := fakeworld.New().Deps(t.TempDir(), "test", false); err != nil && strings.Contains(err.Error(), "not built yet") {
		t.Skip("the fake world is still the contract's placeholder: " + err.Error())
	}
}

// repeater1171 is what the owner's boards run before the tests flash them: MeshCore's repeater v1.17.1, “Hilltop 2”.
var repeater1171 = fakeworld.Firmware{Role: nodecli.RoleRepeater, Version: "v1.17.1", Name: "Hilltop 2"}

// e2eConfirm goes from the Board to Confirm by the keys the owner presses: Enter on the board's port, the board, the
// role (MeshCore's), the version.
func e2eConfirm(t *testing.T, d *driver, dev, role, ver string) {
	t.Helper()
	d.keys("enter")
	if d.m.screen != scrChoose {
		t.Fatalf("enter on the Board: screen %d, note %q\n%s", d.m.screen, d.m.note, d.view())
	}
	chooseTo(d, dev)
	d.keys("enter")
	if d.m.screen != scrRole {
		t.Fatalf("enter on %s: screen %d, note %q", dev, d.m.screen, d.m.note)
	}
	roleTo(d, catalog.SourceMeshCore, role)
	d.keys("enter")
	versionTo(d, ver)
	d.keys("enter")
	if d.m.screen != scrConfirm {
		t.Fatalf("enter on %s: screen %d", ver, d.m.screen)
	}
}

// e2eDownloadThenFlash is y (the file isn't saved: it downloads, and Confirm shows again with its SHA-256), then,
// past the half second Confirm waits, y again: the flash, to its end.
func e2eDownloadThenFlash(t *testing.T, d *driver) {
	t.Helper()
	bodyHas(d, "Not saved yet")
	d.do("y")
	if d.m.screen != scrConfirm {
		t.Fatalf("after the download: screen %d\n%s", d.m.screen, d.view())
	}
	bodyHas(d, "File saved", "SHA-256")
	d.advance(time.Second)
	d.do("y")
	if d.m.screen != scrResult {
		t.Fatalf("after the flash: screen %d\n%s", d.m.screen, d.view())
	}
}

// opens counts the opens of every fake port so far.
func portOpens(w *fakeworld.World) int {
	n := 0
	for _, e := range w.Wire().Events() {
		if e.Ev == "open" {
			n++
		}
	}
	return n
}

// Board → Choose board → Repeater → v1.18.0 → u → y (download) → y → Flashing → Result "Flashed", the MD5 checked;
// the fake ESP32's flash holds the image at 0x10000, nothing erased.
func TestE2EFlashHeltecV4Update(t *testing.T) {
	requireWorld(t)
	w := fakeworld.New()
	b := fakeworld.HeltecV4(repeater1171)
	w.Plug(b)
	d := newWorldDriver(t, 45, 10, w, true)
	e2eConfirm(t, d, "Heltec v4", "repeater", "v1.18.0")
	d.keys("u")
	bodyHas(d, "· update")
	requests := len(w.Requests())
	e2eDownloadThenFlash(t, d)
	o := d.m.res.o
	if !o.OK || o.Title != "Flashed" || !strings.Contains(strings.Join(o.Summary, " "), "MD5") {
		t.Errorf("Outcome %+v", o)
	}
	if got := len(w.Requests()); got <= requests {
		t.Errorf("the download fetched nothing (%d requests)", got)
	}
	esp := b.ESP()
	if fl := esp.Flash(); len(fl) <= 0x10000 || fl[0x10000] != 0xE9 || !bytes.Contains(fl[0x10000:], []byte("MESHFLASH-FAKE")) {
		t.Error("the image isn't at 0x10000")
	}
	if esp.Erased() {
		t.Error("an update erased the chip")
	}
	if r := b.Running(); r.Role != nodecli.RoleRepeater || !strings.Contains(r.Version, "1.18.0") {
		t.Errorf("the board runs %+v", r)
	}
	if o.Setup && !strings.HasPrefix(hintOf(d), "s set up") {
		t.Errorf("Result offers setup but the hint is %q", hintOf(d))
	}
}

// The same with w: the chip erased, the merged image at 0x0.
func TestE2EFlashHeltecV4Wipe(t *testing.T) {
	requireWorld(t)
	w := fakeworld.New()
	b := fakeworld.HeltecV4(repeater1171)
	w.Plug(b)
	d := newWorldDriver(t, 45, 10, w, true)
	e2eConfirm(t, d, "Heltec v4", "repeater", "v1.18.0")
	d.keys("w")
	bodyHas(d, "· full wipe")
	e2eDownloadThenFlash(t, d)
	if o := d.m.res.o; !o.OK || o.Title != "Flashed" {
		t.Errorf("Outcome %+v", o)
	}
	esp := b.ESP()
	if !esp.Erased() {
		t.Error("a full wipe didn't erase the chip")
	}
	if fl := esp.Flash(); len(fl) == 0 || fl[0] != 0xE9 {
		t.Error("the merged image isn't at 0x0")
	}
}

// An nRF52: the 1200-baud touch, the bootloader on USB, DFU, STOP, back on USB running v1.18.0 (the world checks the
// tty is left at 115200); Result offers s.
func TestE2EFlashRAK4631(t *testing.T) {
	requireWorld(t)
	w := fakeworld.New()
	b := fakeworld.RAK4631(repeater1171)
	w.Plug(b)
	d := newWorldDriver(t, 45, 10, w, true)
	e2eConfirm(t, d, "RAK WisBlock / WisMesh (RAK 4631)", "repeater", "v1.18.0")
	bodyHas(d, "· update")
	e2eDownloadThenFlash(t, d)
	o := d.m.res.o
	if !o.OK || o.Title != "Flashed" || !o.Setup || o.Board == nil {
		t.Fatalf("Outcome %+v", o)
	}
	if r := b.Running(); !strings.Contains(r.Version, "1.18.0") {
		t.Errorf("the board runs %+v", r)
	}
	if s := b.Sessions(); len(s) == 0 || !s[len(s)-1].Stopped || !s[len(s)-1].Valid {
		t.Errorf("bootloader sessions %+v", s)
	}
	if !strings.HasPrefix(hintOf(d), "s set up") || !strings.HasSuffix(titleOf(d), "●") {
		t.Errorf("hint %q, status %q", hintOf(d), titleOf(d))
	}
	d.keys("s")
	if d.m.screen != scrSetup {
		t.Errorf("s: screen %d", d.m.screen)
	}
}

// Unplugged mid-flash, at every stage: Result says what the board is left with and how to recover it, the mark is
// ○, and the screen is never left on Flashing (S16).
func TestE2EUnplugMidFlash(t *testing.T) {
	requireWorld(t)
	type stage struct {
		name  string
		board func() *fakeworld.Board
		dev   string
		kind  string // the key that picks the kind on an ESP32, "" for an nRF52
		cut   func(t *testing.T, w *fakeworld.World, b *fakeworld.Board)
	}
	rak := "RAK WisBlock / WisMesh (RAK 4631)"
	stages := []stage{
		{"ESP32 update while writing", func() *fakeworld.Board { return fakeworld.HeltecV4(repeater1171) },
			"Heltec v4", "u", func(t *testing.T, w *fakeworld.World, b *fakeworld.Board) { b.UnplugWhileWriting(3) }},
		{"ESP32 wipe while writing", func() *fakeworld.Board { return fakeworld.HeltecV4(repeater1171) },
			"Heltec v4", "w", func(t *testing.T, w *fakeworld.World, b *fakeworld.Board) { b.UnplugWhileWriting(3) }},
		{"nRF52 while writing", func() *fakeworld.Board { return fakeworld.RAK4631(repeater1171) },
			rak, "", func(t *testing.T, w *fakeworld.World, b *fakeworld.Board) { b.UnplugWhileWriting(10) }},
		{"nRF52 waiting for its bootloader", func() *fakeworld.Board { return fakeworld.RAK4631(repeater1171) },
			rak, "", func(t *testing.T, w *fakeworld.World, b *fakeworld.Board) {
				// The touch is the flash's first step; its bootloader shows about a second later.
				w.At(500*time.Millisecond, func() { w.Unplug(b) })
			}},
		{"nRF52 after the erase", func() *fakeworld.Board { return fakeworld.RAK4631(repeater1171) },
			rak, "", func(t *testing.T, w *fakeworld.World, b *fakeworld.Board) {
				b.NextBootloader("unplug-at:1") // INIT, the frame after START and its erase wait
			}},
		{"nRF52 after STOP", func() *fakeworld.Board { return fakeworld.RAK4631(repeater1171) },
			rak, "", func(t *testing.T, w *fakeworld.World, b *fakeworld.Board) {
				b.NextBootloader(fmt.Sprintf("unplug-at:%d", e2eFrames(t)-1)) // STOP is the session's last frame
			}},
	}
	for _, st := range stages {
		t.Run(st.name, func(t *testing.T) {
			w := fakeworld.New()
			b := st.board()
			w.Plug(b)
			d := newWorldDriver(t, 45, 10, w, true)
			e2eConfirm(t, d, st.dev, "repeater", "v1.18.0")
			if st.kind != "" {
				d.keys(st.kind)
			}
			d.do("y")
			d.advance(time.Second)
			st.cut(t, w, b)
			d.do("y")
			if d.m.screen != scrResult {
				t.Fatalf("screen %d after the flash ended\n%s", d.m.screen, d.view())
			}
			o := d.m.res.o
			if o.OK || o.State == "" || o.Recovery == "" {
				t.Errorf("Outcome %+v", o)
			}
			// The backend is Outcome.Log's one writer: the line is in screen.log once, the screens adding no copy.
			if n := strings.Count(d.logText(), o.Log); o.Log == "" || n != 1 {
				t.Errorf("screen.log has the Outcome's Log %q %d times:\n%s", o.Log, n, d.logText())
			}
			d.advance(time.Second) // a scan: the board is gone
			if !strings.HasSuffix(titleOf(d), "○") {
				t.Errorf("the mark is %q with the board unplugged", titleOf(d))
			}
		})
	}
}

// e2eFrames is how many frames a good nRF52 flash of the RAK's repeater v1.18.0 sends (START, INIT, the DATA frames,
// STOP), from a flash in a world of its own.
func e2eFrames(t *testing.T) int {
	t.Helper()
	w := fakeworld.New()
	b := fakeworld.RAK4631(repeater1171)
	w.Plug(b)
	d := newWorldDriver(t, 45, 10, w, true)
	e2eConfirm(t, d, "RAK WisBlock / WisMesh (RAK 4631)", "repeater", "v1.18.0")
	e2eDownloadThenFlash(t, d)
	s := b.Sessions()
	if len(s) == 0 || s[len(s)-1].Frames < 3 {
		t.Fatalf("sessions %+v", s)
	}
	return s[len(s)-1].Frames
}

// A retry after an nRF52 stopped part-way (4.4): r and y never START the old bootloader session; the first step
// asks for the double press, and once it comes (through World.At) the flash goes through.
func TestE2ERetryAfterStopped(t *testing.T) {
	requireWorld(t)
	w := fakeworld.New()
	b := fakeworld.RAK4631(repeater1171)
	w.Plug(b)
	b.NextBootloader("mute-from:20")
	d := newWorldDriver(t, 45, 10, w, true)
	e2eConfirm(t, d, "RAK WisBlock / WisMesh (RAK 4631)", "repeater", "v1.18.0")
	e2eDownloadThenFlash(t, d)
	if o := d.m.res.o; o.OK || !o.Retry || o.Title != "Flashing stopped" {
		t.Fatalf("the first flash: %+v", o)
	}
	sessions := len(b.Sessions())
	d.advance(time.Second)
	d.keys("r")
	if d.m.screen != scrConfirm || d.m.flow.kind != catalog.Update || d.m.flow.node != nil {
		t.Fatalf("r: screen %d, kind %v", d.m.screen, d.m.flow.kind)
	}
	w.At(5*time.Second, b.DoublePressReset)
	d.advance(time.Second)
	d.do("y")
	if d.m.screen != scrResult {
		t.Fatalf("after the retry: screen %d\n%s", d.m.screen, d.view())
	}
	if p := d.m.fl.p; len(p.Steps) == 0 || p.Steps[0].Text != "Press the board's reset button twice quickly" {
		t.Errorf("the retry's first step: %+v", p.Steps)
	}
	if o := d.m.res.o; !o.OK || o.Title != "Flashed" {
		t.Errorf("the retry: %+v", o)
	}
	if len(b.Sessions()) <= sessions {
		t.Error("the retry used no fresh bootloader session")
	}
}

// A RAK picked for an ESP32's bridge port can't be chosen; Prepare refuses it all the same; nothing is opened.
func TestE2EWrongFamilyRefused(t *testing.T) {
	requireWorld(t)
	w := fakeworld.New()
	w.Plug(fakeworld.HeltecV3(repeater1171))
	d := newWorldDriver(t, 45, 10, w, true)
	d.keys("enter")
	rak := "RAK WisBlock / WisMesh (RAK 4631)"
	chooseTo(d, rak)
	d.keys("enter")
	if d.m.screen != scrChoose || !strings.Contains(d.m.note, "nRF52") {
		t.Fatalf("enter on a RAK for a bridge port: screen %d, note %q", d.m.screen, d.m.note)
	}
	f := flowFor(t, d.m.flow.port, rak, "repeater", "v1.18.0")
	before := portOpens(w)
	p := d.m.backend.Prepare(flash.Request{Port: d.m.flow.port, Device: *f.dev, Firmware: *f.fw, Version: *f.ver,
		Kind: catalog.Update})
	if p.Problem == "" {
		t.Errorf("Prepare didn't refuse a RAK on a bridge port: %+v", p)
	}
	if portOpens(w) != before {
		t.Error("Prepare opened a port")
	}
}

// With no catalog saved: the Board says so, Enter says to press u, and nothing is fetched.
func TestE2ENoCatalog(t *testing.T) {
	requireWorld(t)
	w := fakeworld.New()
	w.Plug(fakeworld.HeltecV4(repeater1171))
	d := newWorldDriver(t, 45, 10, w, false)
	bodyHas(d, "No catalog saved. Join Wi-Fi and press u.")
	d.keys("enter")
	if d.m.screen != scrBoard || d.m.note != "Press u to fetch the catalog first." {
		t.Errorf("enter: screen %d, note %q", d.m.screen, d.m.note)
	}
	for i := 0; i < 5; i++ {
		d.advance(time.Second)
	}
	if r := w.Requests(); len(r) != 0 {
		t.Errorf("fetched with no u: %v", r)
	}
}

// u through httptest on the loopback interface: the catalog comes; then offline, and a 503: the saved one stays.
func TestE2ECatalogUpdate(t *testing.T) {
	requireWorld(t)
	w := fakeworld.New()
	srv := httptest.NewServer(w.Handler())
	t.Cleanup(srv.Close) // after the driver's own cleanups
	d := newWorldDriverOver(t, 45, 10, w, srv, false)
	bodyHas(d, "No catalog saved")
	d.do("u")
	bodyHas(d, "72 boards, newest v1.18.0")
	d.keys("enter")
	bodyHas(d, "Catalog from")
	saved := d.m.cat
	w.SetOffline(true)
	d.do("u")
	bodyHas(d, "The saved catalog stays.", "No network, or the server didn't answer.")
	d.keys("esc")
	w.SetOffline(false)
	w.FailPath("/mc/config.json", 503, "")
	d.do("u")
	bodyHas(d, "The saved catalog stays.", "answered HTTP 503.")
	d.keys("esc")
	if d.m.cat != saved {
		t.Error("a failed update replaced the catalog")
	}
	bodyHas(d, "Catalog from")
}

// The battery at 24%: the warning, the second question, and n doesn't flash; unreadable, the same.
func TestE2ELowBattery(t *testing.T) {
	requireWorld(t)
	w := fakeworld.New()
	w.Plug(fakeworld.RAK4631(repeater1171))
	w.SetBattery(24)
	d := newWorldDriver(t, 45, 10, w, true)
	e2eConfirm(t, d, "RAK WisBlock / WisMesh (RAK 4631)", "repeater", "v1.18.0")
	d.do("y") // the download
	d.advance(time.Second)
	bodyHas(d, "Battery 24%.")
	d.keys("y")
	if d.m.confirm != confirmLowBattery || !strings.Contains(d.view(), "Battery 24%. Flash anyway?") {
		t.Fatalf("y at 24%%: question %v\n%s", d.m.confirm, d.view())
	}
	d.keys("n")
	if d.m.screen != scrConfirm || d.m.job != nil {
		t.Fatalf("n flashed: screen %d", d.m.screen)
	}
	w.SetBatteryUnreadable()
	d.keys("esc", "enter") // Confirm again: Prepare reads the battery again
	bodyHas(d, "The battery level can't be read.")
	d.advance(time.Second)
	d.keys("y")
	if d.m.confirm != confirmLowBattery || !strings.Contains(d.view(), "Battery level unknown. Flash anyway?") {
		t.Fatalf("y, battery unreadable: question %v\n%s", d.m.confirm, d.view())
	}
	d.keys("n")
	if d.m.screen != scrConfirm || d.m.job != nil {
		t.Errorf("n flashed: screen %d", d.m.screen)
	}
}
