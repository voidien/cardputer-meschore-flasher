package flash_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
)

func TestFlashESP32Update(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", "Hilltop 2"))
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
	fl := r.save(req)
	p, seen := r.flashSeeing(req, 200*time.Millisecond, 1500*time.Millisecond)
	o := p.Outcome
	if !o.OK || o.Title != "Flashed" || !o.Setup || o.Retry || o.Board == nil || o.Board.Name != "ttyACM0" {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	want := []string{"Into the bootloader", "Flasher stub loaded (ESP32-S3)", "Written", "Checked: MD5 matches",
		"Restarted"}
	if !slices.Equal(texts(p), want) {
		t.Errorf("steps:\n%s", steps(p))
	}
	if !strings.Contains(strings.Join(o.Summary, " "), "MD5 of the flash matches") {
		t.Errorf("summary %q", o.Summary)
	}
	if got := []flash.Risk{seen[0].Risk, seen[1].Risk, p.Risk}; !slices.Equal(got,
		[]flash.Risk{flash.RiskNone, flash.RiskErased, flash.RiskWritten}) {
		t.Errorf("risks %v: connecting, writing, at the end", got)
	}
	e := v4.ESP()
	if e.Erased() {
		t.Error("an update erased the chip")
	}
	data, _, err := r.d.Catalog.ReadVerified(fl.File.Base())
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Flash()[0x10000 : 0x10000+len(data)]; !bytes.Equal(got, data) {
		t.Error("the flash doesn't hold the file at 0x10000")
	}
	if got := v4.Running(); got.Version != "v1.18.0-81dbae5" || got.Name != "Hilltop 2" {
		t.Errorf("it runs %+v", got)
	}
}

func TestFlashESP32Wipe(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", "Hilltop 2"))
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Wipe)
	fl := r.save(req)
	p, seen := r.flashSeeing(req, 200*time.Millisecond, 15*time.Second)
	o := p.Outcome
	if !o.OK || o.Title != "Flashed" || !o.Setup || !strings.Contains(o.Summary[0], "Erased the whole chip") {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	want := []string{"Into the bootloader", "Flasher stub loaded (ESP32-S3)", "Erased the whole chip", "Written",
		"Checked: MD5 matches", "Restarted"}
	if !slices.Equal(texts(p), want) {
		t.Errorf("steps:\n%s", steps(p))
	}
	if p.Steps[2].Took < 10*time.Second {
		t.Errorf("the chip erase took %v of the world's time", p.Steps[2].Took)
	}
	if got := []flash.Risk{seen[0].Risk, seen[1].Risk, p.Risk}; !slices.Equal(got,
		[]flash.Risk{flash.RiskNone, flash.RiskErased, flash.RiskWritten}) {
		t.Errorf("risks %v: connecting, writing, at the end", got)
	}
	e := v4.ESP()
	data, _, _ := r.d.Catalog.ReadVerified(fl.File.Base())
	if !e.Erased() || !bytes.Equal(e.Flash()[:len(data)], data) {
		t.Error("the chip wasn't erased and the merged image written at 0x0")
	}
	// A full wipe loses the node's settings: it runs the new firmware with a new node's name.
	if got := v4.Running(); got.Version != "v1.18.0-81dbae5" || got.Name == "Hilltop 2" {
		t.Errorf("it runs %+v", got)
	}
}

// A Heltec v3 behind its CP2102: the classic reset through the bridge's auto-reset circuit.
func TestFlashBridgeBoard(t *testing.T) {
	v3 := fakeworld.HeltecV3(repeater("v1.17.1", ""))
	r := newRig(t, v3)
	port := r.port(v3)
	if port.Name != "ttyUSB0" || port.Label != "CP210x" || port.Key != "" {
		t.Errorf("port %+v", port)
	}
	req := request(t, port, "Heltec v3", catalog.SourceMeshCore, "repeater", catalog.Update)
	r.save(req)
	p := r.flash(req)
	if o := p.Outcome; !o.OK || !o.Setup || o.Board == nil || o.Board.Name != "ttyUSB0" {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if got := v3.Running(); got.Version != "v1.18.0-81dbae5" {
		t.Errorf("it runs %+v", got)
	}
}

// An ESP32-S3 app on TinyUSB reboots into its ROM as a new USB device: the flash follows it by its serial number
// to the ROM's port, flashes it there, and finds the app's port again after the hard reset.
func TestFlashTinyUSBReenumerates(t *testing.T) {
	tr := fakeworld.HeltecTrackerV2(repeater("v1.17.1", ""))
	r := newRig(t, tr)
	port := r.port(tr)
	if port.Kind.String() != "esp-tinyusb" {
		t.Fatalf("port %+v", port)
	}
	req := request(t, port, "Heltec Wireless Tracker v2", catalog.SourceMeshCore, "repeater", catalog.Update)
	r.save(req)
	p := r.flash(req)
	o := p.Outcome
	if !o.OK || o.Board == nil || o.Board.Kind.String() != "esp-tinyusb" {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if p.Steps[0].Text != "Into the bootloader" || p.Steps[0].State != flash.StepDone {
		t.Errorf("steps:\n%s", steps(p))
	}
	opens := r.w.Opens()
	if len(opens) != 2 || opens[0] != "ttyACM0 at 115200 exclusive" || opens[1] != "ttyACM0 at 115200 exclusive" {
		t.Errorf("opens %q", opens)
	}
	if got := tr.Running(); got.Version != "v1.18.0-81dbae5" {
		t.Errorf("it runs %+v", got)
	}
}

// A board whose resets never reach its ROM: nothing touched, and how to start the bootloader by hand.
func TestFlashESP32NoBootloader(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	v4.ESP().IgnoreResets = 10
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
	r.save(req)
	var at []time.Duration
	for d := 500 * time.Millisecond; d < 10*time.Second; d += 500 * time.Millisecond {
		at = append(at, d)
	}
	p, seen := r.flashSeeing(req, at...)
	o := p.Outcome
	if o.Title != "Not flashed" || o.State != "Flash untouched." || !o.Retry ||
		!strings.HasPrefix(o.Recovery, "The board didn't start its bootloader: hold BOOT") {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if p.Risk != flash.RiskNone {
		t.Errorf("risk %v", p.Risk)
	}
	var notes []string
	for _, p := range seen {
		if len(p.Steps) > 0 && !slices.Contains(notes, p.Steps[0].Note) {
			notes = append(notes, p.Steps[0].Note)
		}
	}
	if !slices.Equal(notes, []string{"", "attempt 2 of 4", "attempt 3 of 4", "attempt 4 of 4"}) {
		t.Errorf("notes while connecting %q", notes)
	}
	if !strings.Contains(r.log(), "Not flashed") {
		t.Errorf("screen.log %q", r.log())
	}
}

// Unplugged while writing: partly written, and how to recover; the board is gone.
func TestFlashESP32Unplugged(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
	r.save(req)
	v4.UnplugWhileWriting(2)
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Flashing stopped" || !strings.HasPrefix(o.State, "Partially written: ") || o.Board != nil ||
		!strings.HasPrefix(o.Recovery, "To recover, hold BOOT") || !o.Retry ||
		!strings.Contains(o.Summary[0], "went away while writing") {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if strings.HasPrefix(o.Recovery, o.State) {
		t.Errorf("Recovery repeats State")
	}
	if p.Risk != flash.RiskErased {
		t.Errorf("risk %v", p.Risk)
	}
	if v4.Tty() != "" {
		t.Errorf("still on USB as %s", v4.Tty())
	}
	r.w.Plug(v4)
	if got := v4.Running(); got.Role != "" {
		t.Errorf("a part-written image runs: %+v", got)
	}
}

// The flash's MD5 doesn't match the file: written, but not right.
func TestFlashESP32MD5Mismatch(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	v4.ESP().CorruptWrite = true
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
	r.save(req)
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Flashing stopped" || o.State != "Written, but the flash doesn't match the file, so the board may not start." ||
		!strings.HasPrefix(o.Recovery, "Flash it again") || !o.Retry {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
}

// A file for another chip than the board's: the ROM's chip read stops it before anything is written.
func TestFlashESP32WrongChip(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v2", catalog.SourceMeshCore, "repeater", catalog.Update) // a classic ESP32
	r.save(req)
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Not flashed" || o.State != "Flash untouched: the old firmware is still there." ||
		!strings.Contains(o.Recovery, "choose the board and the file that match it") || o.Retry != true {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if v4.ESP().Count(0x10) != 0 {
		t.Error("FLASH_DEFL_BEGIN sent")
	}
}

// Stopped mid-write: the Job says so, and what the board is left with.
func TestFlashESP32Stopped(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
	r.save(req)
	var mu sync.Mutex
	var j *flash.Job
	r.w.At(1500*time.Millisecond, func() {
		mu.Lock()
		defer mu.Unlock()
		j.Cancel()
	})
	mu.Lock()
	j = r.b.Flash(context.Background(), req)
	mu.Unlock()
	p := wait(t, j)
	o := p.Outcome
	if !o.Stopped || o.Title != "Flashing stopped" || !strings.HasPrefix(o.State, "Partially written: ") || !o.Retry {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if v4.ESP().Mode() != "app" {
		t.Errorf("left in %q: the stop pulses RTS so the board leaves its bootloader", v4.ESP().Mode())
	}
}
