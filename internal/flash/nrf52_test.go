package flash_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/nodecli"
	"meshflash/internal/nrfdfu"
)

const rakName = "RAK WisBlock / WisMesh (RAK 4631)"

func rakRepeater(t *testing.T, r *rig, rak *fakeworld.Board) flash.Request {
	return request(t, r.port(rak), rakName, catalog.SourceMeshCore, "repeater", catalog.Update)
}

// starts counts the bootloader sessions that had START.
func starts(b *fakeworld.Board) int {
	n := 0
	for _, s := range b.Sessions() {
		if s.Start != nil {
			n++
		}
	}
	return n
}

func TestFlashRAK4631(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", "Hilltop 2"))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	p, seen := r.flashSeeing(req, 500*time.Millisecond, 4500*time.Millisecond)
	o := p.Outcome
	if !o.OK || o.Title != "Flashed" || !o.Setup || o.Board == nil || o.Board.Name != "ttyACM0" {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	want := []string{"Into the bootloader (1200-baud touch)", "Erased", "Written", "The board checked the CRC",
		"Back on USB (ttyACM0)", "Running v1.18.0", "Back to 115200 baud"}
	if !slices.Equal(texts(p), want) {
		t.Errorf("steps:\n%s", steps(p))
	}
	if p.Steps[1].Expect != nrfdfu.EraseWait(56<<10) {
		t.Errorf("the erase step expects %v", p.Steps[1].Expect)
	}
	if got := []flash.Risk{seen[0].Risk, seen[1].Risk, p.Risk}; !slices.Equal(got,
		[]flash.Risk{flash.RiskNone, flash.RiskErased, flash.RiskWritten}) {
		t.Errorf("risks %v: touching, writing, at the end", got)
	}
	if got := rak.Running(); got.Version != "v1.18.0-81dbae5" || got.Name != "Hilltop 2" {
		t.Errorf("it runs %+v", got)
	}
	if s := r.w.Speed("ttyACM0"); s != 115200 {
		t.Errorf("ttyACM0 left at %d baud", s)
	}
	if n := starts(rak); n != 1 {
		t.Errorf("%d sessions had START", n)
	}
	// Flashed and back in its app: its session is fresh again, so a probe says nothing about a stopped flash.
	if n := r.b.Probe(context.Background(), r.port(rak)); n.Problem != flash.NoProblem {
		t.Errorf("probe after the flash: %+v", n)
	}
}

// The bootloader comes up under a new tty name while ttyACM0 is still held (another program had it open); the app
// comes back on ttyACM0, the touched name, once that lets go, and the flash leaves it at 115200 baud.
func TestFlashBootloaderOnNewTty(t *testing.T) {
	for _, tc := range []struct {
		name, role string
	}{{"repeater", "repeater"}, {"companion", "companionBle"}} {
		t.Run(tc.name, func(t *testing.T) {
			rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
			r := newRig(t, rak)
			req := request(t, r.port(rak), rakName, catalog.SourceMeshCore, tc.role, catalog.Update)
			r.save(req)
			r.w.HoldTty("ttyACM0", true)
			var bootTty atomic.Value
			r.w.At(3*time.Second, func() {
				bootTty.Store(rak.Tty())
				r.w.HoldTty("ttyACM0", false)
			})
			p := r.flash(req)
			if !p.Outcome.OK {
				t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
			}
			if got := bootTty.Load(); got != "ttyACM1" {
				t.Errorf("the bootloader came up on %v", got)
			}
			if p.Outcome.Board == nil || p.Outcome.Board.Name != "ttyACM0" {
				t.Errorf("the app came back on %+v", p.Outcome.Board)
			}
			if s := r.w.Speed("ttyACM0"); s != 115200 {
				t.Errorf("ttyACM0 left at %d baud", s)
			}
			opens := r.w.Opens()
			if opens[0] != "ttyACM0 at 1200 exclusive" || opens[1] != "ttyACM1 at 115200 exclusive" ||
				!slices.Contains(opens[2:], "ttyACM0 at 115200 exclusive") {
				t.Errorf("opens %q", opens)
			}
		})
	}
}

// A companion app has no console to ask, so the flash itself opens its tty at 115200 at the end; the Result says it
// came back and that meshflash can't ask its version (owner question 7).
func TestFlashCompanionComesBack(t *testing.T) {
	rak := fakeworld.RAK4631(fakeworld.Firmware{Role: "companion"})
	r := newRig(t, rak)
	req := request(t, r.port(rak), rakName, catalog.SourceMeshCore, "companionBle", catalog.Update)
	r.save(req)
	p := r.flash(req)
	o := p.Outcome
	sum := strings.Join(o.Summary, " ")
	if !o.OK || o.Title != "Flashed" || o.Setup || !strings.Contains(sum, "It came back on USB (ttyACM0).") ||
		!strings.Contains(sum, "meshflash can't ask this firmware its version.") ||
		!strings.Contains(sum, "its bootloader took the image") {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if slices.Contains(texts(p), "Running v1.18.0") {
		t.Errorf("asked a companion its version:\n%s", steps(p))
	}
	if s := r.w.Speed("ttyACM0"); s != 115200 {
		t.Errorf("ttyACM0 left at %d baud", s)
	}
}

// A ZephCore repeater on an nRF52 is flashed like a companion: no version check, never set up (4.1).
func TestFlashZephCoreNoSetup(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := request(t, r.port(rak), rakName, catalog.SourceZephCore, "repeater", catalog.Update)
	r.save(req)
	p := r.flash(req)
	if o := p.Outcome; !o.OK || o.Setup || slices.Contains(texts(p), "Running "+req.Version.Name) {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if got := rak.Running(); got.Version != "1.18.2-zephcore" {
		t.Errorf("it runs %+v", got)
	}
}

// A board already in its bootloader that the backend saw appear (plugged in after its first scan) is fresh: no
// touch, straight to DFU (owner question 18).
func TestFlashAlreadyInBootloader(t *testing.T) {
	w := fakeworld.New()
	r := newRigOver(t, w, false)
	if _, err := r.b.Scan(); err != nil {
		t.Fatal(err)
	}
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	rak.DoublePressReset() // before it is plugged in: it starts in its bootloader
	w.Plug(rak)
	port := r.port(rak)
	if !port.InBootloader() {
		t.Fatalf("not in its bootloader: %+v", port)
	}
	if n := r.b.Probe(context.Background(), port); n.Problem != flash.InBootloader || n.Reason != "" {
		t.Errorf("probe %+v", n)
	}
	req := rakRepeater(t, r, rak)
	r.save(req)
	p := r.flash(req)
	if !p.Outcome.OK || p.Steps[0].Text != "Already in its bootloader" {
		t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
	}
	for _, o := range w.Opens() {
		if strings.Contains(o, "at 1200") {
			t.Errorf("touched a board in its bootloader: %q", w.Opens())
		}
	}
	if slices.Contains(texts(p), "Back to 115200 baud") {
		t.Errorf("no touch, but the step:\n%s", steps(p))
	}
}

// A bootloader already listed at the backend's first scan may hold a session a stopped flash left before a restart,
// so the flash asks for a double press and sends no START until the board's bootloader went and came back (design
// 4.3 and 4.4.5).
func TestFlashBootloaderAtStartAsksDoublePress(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	rak.DoublePressReset()
	r := newRig(t, rak) // plugged in before the backend's first scan
	port := r.port(rak)
	n := r.b.Probe(context.Background(), port)
	if n.Problem != flash.InBootloader || !strings.Contains(n.Reason, "press reset twice quickly") {
		t.Errorf("probe %+v", n)
	}
	req := rakRepeater(t, r, rak)
	r.save(req)
	var startsBefore atomic.Int32
	startsBefore.Store(-1)
	r.w.At(5*time.Second, func() {
		startsBefore.Store(int32(starts(rak)))
		rak.DoublePressReset()
	})
	p := r.flash(req)
	if !p.Outcome.OK || p.Steps[0].Text != "Press the board's reset button twice quickly" ||
		p.Steps[0].State != flash.StepDone {
		t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
	}
	if got := startsBefore.Load(); got != 0 {
		t.Errorf("%d STARTs before the double press", got)
	}
	if s := rak.Sessions(); len(s) != 2 || s[0].Start != nil || s[1].Start == nil {
		t.Errorf("sessions %+v", s)
	}
}

// After a flash that stopped past START, a retry never sends START into that session: it asks for the double press
// and waits; with none, it ends "Not flashed" after FreshBootloaderWait with nothing more sent; with one (World.At),
// it flashes (4.4, owner question 17).
func TestFlashRetryNeedsFreshBootloader(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	rak.NextBootloader("mute-from:20")
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Flashing stopped" || o.State != "Its app is erased." ||
		o.Recovery != "Press r; meshflash then asks you to press reset twice quickly." || !o.Retry || o.Board == nil {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if !strings.Contains(r.log(), "Flashing stopped") {
		t.Errorf("screen.log %q", r.log())
	}
	if n := r.b.Probe(context.Background(), *o.Board); n.Problem != flash.InBootloader ||
		n.Reason != "Its last flash stopped part-way: meshflash asks you to press reset twice quickly when you flash it." {
		t.Errorf("probe of the stale session: %+v", n)
	}

	// r, with no double press: 60 s, then not flashed, and the old session got nothing more.
	frames := rak.Sessions()[0].Frames
	req.Port = *o.Board
	start := r.w.Clock().Now()
	p = r.flash(req)
	o = p.Outcome
	if o.Title != "Not flashed" || o.State != "Nothing more was sent: its app is still erased." ||
		o.Recovery != "Press reset twice quickly, then press r." || !o.Retry ||
		p.Steps[0].Text != "Press the board's reset button twice quickly" || p.Steps[0].State != flash.StepFailed {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if took := r.w.Clock().Now().Sub(start); took < flash.FreshBootloaderWait || took > flash.FreshBootloaderWait+time.Second {
		t.Errorf("waited %v", took)
	}
	if s := rak.Sessions(); len(s) != 1 || s[0].Frames != frames {
		t.Errorf("sent into the old session: %d frames, then %d (%d sessions)", frames, s[0].Frames, len(s))
	}

	// r again, and the owner presses twice: it flashes.
	r.w.At(4*time.Second, rak.DoublePressReset)
	p = r.flash(req)
	if !p.Outcome.OK || p.Steps[0].Text != "Press the board's reset button twice quickly" ||
		p.Steps[0].State != flash.StepDone {
		t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
	}
	if s := rak.Sessions(); len(s) != 2 || s[1].Start == nil || !s[1].Valid {
		t.Errorf("sessions %+v", s)
	}
}

// A frame whose acknowledgement doesn't come is sent again (the bootloader drops the copy it already has), and the
// flash goes on.
func TestFlashDropAck(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	rak.NextBootloader("drop-ack:5")
	p := r.flash(req)
	if !p.Outcome.OK {
		t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
	}
	if s := rak.Sessions(); len(s) != 1 || s[0].Dropped != 1 || !s[0].Valid {
		t.Errorf("sessions %+v", s)
	}
	for _, s := range p.Steps {
		if s.Note != "" {
			t.Errorf("a note left on a finished step: %q (%q)", s.Text, s.Note)
		}
	}
}

// While a frame is sent again, the running step says so: "No answer: sending again (2 of 3)". Each wait for an
// acknowledgement is one jump of the clock, so the world's At sees the step as each wait ends.
func TestFlashResendNote(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	rak.NextBootloader("mute-from:20") // DATA frame 19 and everything after go unanswered
	var mu sync.Mutex
	var j *flash.Job
	var notes []string
	look := func() {
		mu.Lock()
		defer mu.Unlock()
		p := j.Snapshot()
		notes = append(notes, p.Steps[len(p.Steps)-1].Note)
	}
	r.w.At(12*time.Second, look)
	r.w.At(17*time.Second, look)
	mu.Lock()
	j = r.b.Flash(context.Background(), req)
	mu.Unlock()
	p := wait(t, j)
	if p.Outcome.Title != "Flashing stopped" {
		t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(notes, []string{"No answer: sending again (2 of 3)", "No answer: sending again (3 of 3)"}) {
		t.Errorf("notes %q", notes)
	}
}

// The board unplugged during the DFU session: its app is erased; it is gone.
func TestFlashNRF52Unplugged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   func(*fakeworld.Board)
		state string
	}{
		{"unplug-at", func(b *fakeworld.Board) { b.NextBootloader("unplug-at:12") }, "Its app is erased."},
		{"while writing", func(b *fakeworld.Board) { b.UnplugWhileWriting(30) }, "Its app is erased."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
			r := newRig(t, rak)
			req := rakRepeater(t, r, rak)
			r.save(req)
			tc.set(rak)
			p := r.flash(req)
			o := p.Outcome
			if o.Title != "Flashing stopped" || o.State != tc.state || o.Board != nil || !o.Retry ||
				!strings.Contains(strings.Join(o.Summary, " "), "went away while writing") {
				t.Fatalf("%s\n%s", outcome(o), steps(p))
			}
			if rak.Tty() != "" {
				t.Errorf("still on USB as %s", rak.Tty())
			}
		})
	}
}

// A package for another SoftDevice is refused before anything touches the board: nothing is even opened.
func TestFlashSoftDeviceRefusedBeforeTouch(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	fl, err := catalog.Choose(req.Device, req.Version, req.Kind)
	if err != nil {
		t.Fatal(err)
	}
	r.w.ServeSoftDevice(fl.File.Base(), nrfdfu.SoftDeviceS140v7)
	r.save(req)
	if pr := r.b.Prepare(req); pr.Saved || !strings.Contains(pr.Problem, "SoftDevice") {
		t.Errorf("prepared %+v", pr)
	}
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Not flashed" || o.State != "Nothing was sent to the board: it still has its old firmware." ||
		!strings.Contains(o.Recovery, "SoftDevice") || len(p.Steps) != 0 {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if opens := r.w.Opens(); len(opens) != 0 {
		t.Errorf("opened %q", opens)
	}
}

// STOP's acknowledgement lost as the bootloader leaves (MayHaveWorked): the app came back, so it is flashed.
func TestFlashMayHaveWorked(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	rak.NextBootloader("lose-ack:" + stopFrame(t, r, req))
	p := r.flash(req)
	if o := p.Outcome; !o.OK || !o.Setup || !slices.Contains(texts(p), "The board most likely checked the CRC") {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
}

// stopFrame is the STOP frame's number in a session flashing req's file: START 0, INIT 1, the DATA frames, STOP.
func stopFrame(t *testing.T, r *rig, req flash.Request) string {
	t.Helper()
	fl, _ := catalog.Choose(req.Device, req.Version, req.Kind)
	data, _, err := r.d.Catalog.ReadVerified(fl.File.Base())
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := nrfdfu.OpenPackage(data)
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(2 + (len(pkg.Bin)+511)/512)
}

// MayHaveWorked, and nothing came back in AppWait: most likely written, the board didn't come back.
func TestFlashMayHaveWorkedNeverBack(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	rak.NextBootloader("lose-ack:" + stopFrame(t, r, req))
	// STOP goes out about 5.6 s in; its answer is waited for 5 s, and the app would be back a second after that.
	r.w.At(11*time.Second, func() { r.w.Unplug(rak) })
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Flashing stopped" || o.State != "Most likely written, but the board didn't come back." ||
		!strings.Contains(o.Recovery, "press reset twice quickly") || !o.Retry {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
}

// The board came back running another version than the one written: the bootloader most likely refused it.
func TestFlashWrongVersionAfterStop(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	fl, _ := catalog.Choose(req.Device, req.Version, req.Kind)
	r.w.ServeAs(fl.File.Base(), repeater("v1.17.1-d929643", ""))
	r.save(req)
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Flashing stopped" || o.OK || o.Setup || !o.Retry ||
		o.State != "It runs v1.17.1, not the version just flashed: the bootloader most likely refused the image." ||
		o.Recovery != "Press reset twice quickly, then flash again." {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
}

// The board came back on USB but its console never answered: flashed, not checked.
func TestFlashCameBackSilent(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	fl, _ := catalog.Choose(req.Device, req.Version, req.Kind)
	r.w.ServeAs(fl.File.Base(), fakeworld.Firmware{Role: "companion", Version: "v1.18.0"})
	r.save(req)
	start := r.w.Clock().Now()
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Flashed, not checked" || o.State != "It came back on USB but didn't answer as a repeater." ||
		o.Recovery != "Press reset once; if it still doesn't answer, flash it again." || !o.Retry || o.Setup {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if took := r.w.Clock().Now().Sub(start); took < nodecli.ConfirmWait {
		t.Errorf("asked for only %v", took)
	}
}

// The touch did nothing (the app ignores it): not flashed, and its tty is left at 115200, not 1200.
func TestFlashTouchDidNothing(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	rak.IgnoreTouch(true)
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Not flashed" || o.State != "Nothing was sent: it still has its old firmware." ||
		o.Recovery != "Press reset twice quickly, then press r." || !o.Retry {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if got := texts(p); !slices.Equal(got, []string{"Into the bootloader (1200-baud touch)", "Back to 115200 baud"}) ||
		p.Steps[1].State != flash.StepDone {
		t.Errorf("steps:\n%s", steps(p))
	}
	if s := r.w.Speed("ttyACM0"); s != 115200 {
		t.Errorf("ttyACM0 left at %d baud", s)
	}
}

// Stopped mid-write: the Job says so, the app is erased, and the session is stale for the next flash.
func TestFlashNRF52Stopped(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	var mu sync.Mutex
	var j *flash.Job
	r.w.At(4500*time.Millisecond, func() {
		mu.Lock()
		defer mu.Unlock()
		j.Cancel()
	})
	mu.Lock()
	j = r.b.Flash(context.Background(), req)
	mu.Unlock()
	p := wait(t, j)
	o := p.Outcome
	if !o.Stopped || o.Title != "Flashing stopped" || o.State != "Its app is erased." || !o.Retry {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if p.Risk != flash.RiskErased {
		t.Errorf("risk %v", p.Risk)
	}
	if last := p.Steps[len(p.Steps)-1]; last.Text != "Back to 115200 baud" || last.State != flash.StepDone {
		t.Errorf("steps:\n%s", steps(p))
	}
	if n := r.b.Probe(context.Background(), r.port(rak)); n.Problem != flash.InBootloader || n.Reason == "" {
		t.Errorf("probe %+v", n)
	}
}

// Unplugged at each wait of an nRF52 flash: during the bootloader's wait after the touch, during the erase, and
// after STOP. Each ends with what the board is left with, and none leaves the Job running.
func TestFlashNRF52UnpluggedWhileWaiting(t *testing.T) {
	for _, tc := range []struct {
		name         string
		at           time.Duration
		title, state string
	}{
		{"waiting for the bootloader", 2 * time.Second, "Not flashed", "Nothing was sent: it still has its old firmware."},
		{"erasing", 3 * time.Second, "Flashing stopped", "Its app is erased."},
		{"after STOP", 6 * time.Second, "Flashing stopped", "Most likely written, but the board didn't come back."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
			r := newRig(t, rak)
			req := rakRepeater(t, r, rak)
			r.save(req)
			r.w.At(tc.at, func() { r.w.Unplug(rak) })
			p := r.flash(req)
			o := p.Outcome
			if o.Title != tc.title || o.State != tc.state || o.Board != nil || !o.Retry {
				t.Fatalf("%s\n%s", outcome(o), steps(p))
			}
		})
	}
}

// The touch's own open refused (another program holds the port): nothing reached the board, no wait.
func TestFlashTouchBusy(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	rak.SetBusy(true)
	start := r.w.Clock().Now()
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Not flashed" || o.Summary[0] != "Another program has the board's port open." || !o.Retry {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if took := r.w.Clock().Now().Sub(start); took > time.Second {
		t.Errorf("waited %v", took)
	}
	rak.SetBusy(false)
}

// A Xiao nRF52840 (SoftDevice S140 v7, bootloader 2886:0045): the same flash, matched to its own SoftDevice.
func TestFlashXiao(t *testing.T) {
	xiao := fakeworld.XiaoNRF52(repeater("v1.17.1", "Hilltop 2"))
	r := newRig(t, xiao)
	port := r.port(xiao)
	if len(port.Candidates) != 3 {
		t.Errorf("candidates %+v", port.Candidates)
	}
	req := request(t, port, "Seeed Studio Xiao nRF52 WIO", catalog.SourceMeshCore, "repeater", catalog.Update)
	r.save(req)
	if pr := r.b.Prepare(req); !pr.Saved || pr.Problem != "" || pr.Ask != "" {
		t.Fatalf("%+v", pr)
	}
	p := r.flash(req)
	if !p.Outcome.OK || !p.Outcome.Setup {
		t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
	}
	if s := xiao.Sessions(); len(s) != 1 || s[0].SDReq[0] != nrfdfu.SoftDeviceS140v7 {
		t.Errorf("sessions %+v", s)
	}
}
