package flash_test

// Stops that land where the board has already been changed, or nearly: each says what the board is really left with
// (a board in its bootloader, an app still erased), and the ports are closed whatever happened.

import (
	"context"
	"strings"
	"testing"
	"time"

	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/usbid"
)

// A stop during the 1200-baud touch: before DTR drops, the line goes back to 115200 first and the board is never
// touched; once it has dropped, the board resets into its bootloader all the same, and the outcome, the Risk and
// the tty's speed say so.
func TestFlashStopDuringTouch(t *testing.T) {
	t.Run("before DTR drops", func(t *testing.T) {
		rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
		r := newRig(t, rak)
		req := rakRepeater(t, r, rak)
		r.save(req)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.w.At(50*time.Millisecond, cancel) // the touch holds the port open at 1200 baud for 100 ms
		p := wait(t, r.b.Flash(ctx, req))
		o := p.Outcome
		if !o.Stopped || o.Summary[0] != "Stopped before anything was sent." ||
			o.State != "Nothing was sent to the board: it still has its old firmware." || p.Risk != flash.RiskNone {
			t.Fatalf("%s\nrisk %v\n%s", outcome(o), p.Risk, steps(p))
		}
		if s := r.w.Speed("ttyACM0"); s != 115200 {
			t.Errorf("ttyACM0 left at %d baud", s)
		}
		scan, err := r.b.Scan()
		if err != nil || len(scan.Ports) != 1 || scan.Ports[0].Kind != usbid.NRFApp {
			t.Errorf("the board isn't in its app: %+v, %v", scan.Ports, err)
		}
	})
	t.Run("after DTR dropped", func(t *testing.T) {
		rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
		r := newRig(t, rak)
		req := rakRepeater(t, r, rak)
		r.save(req)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.w.At(500*time.Millisecond, cancel) // in the 1.5 s the touch waits for the reset
		start := r.w.Clock().Now()
		p := wait(t, r.b.Flash(ctx, req))
		o := p.Outcome
		if !o.Stopped || o.Summary[0] != "Stopped just after the 1200-baud touch." ||
			o.State != "Nothing was written: it may be in its bootloader now, with its old firmware." ||
			o.Recovery != "Press its reset button once to start it." || p.Risk != flash.RiskBootloader {
			t.Fatalf("%s\nrisk %v\n%s", outcome(o), p.Risk, steps(p))
		}
		// The stop is seen within one of the touch's 250 ms waits, well inside the app's 1.5 s for a stop.
		if took := r.w.Clock().Now().Sub(start); took > time.Second {
			t.Errorf("the stop took %v", took)
		}
		if got := texts(p); len(got) == 0 || got[len(got)-1] != "Back to 115200 baud" {
			t.Errorf("the tty wasn't set back:\n%s", steps(p))
		}
	})
}

// failedPastStart is a RAK whose first flash stopped past START: its app is erased and its session stale.
func failedPastStart(t *testing.T) (*rig, *fakeworld.Board, flash.Request) {
	t.Helper()
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	rak.NextBootloader("mute-from:20")
	p := r.flash(req)
	if o := p.Outcome; o.State != "Its app is erased." || o.Board == nil {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	req.Port = *p.Outcome.Board
	return r, rak, req
}

// Stopped while a retry waits for the double press, the app erased by the earlier START: the question and
// screen.log say the board has no working firmware (RiskErased), and the outcome that its app is still erased, never
// that it keeps its old firmware.
func TestFlashRetryStoppedWaitingForDoublePress(t *testing.T) {
	r, _, req := failedPastStart(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := r.b.Flash(ctx, req)
	r.w.At(3*time.Second, func() {
		if p := j.Snapshot(); p.Risk != flash.RiskErased {
			t.Errorf("risk while waiting %v, want RiskErased", p.Risk)
		}
		cancel()
	})
	p := wait(t, j)
	o := p.Outcome
	if !o.Stopped || o.State != "Nothing more was sent: its app is still erased." ||
		o.Recovery != "Press r; meshflash then asks you to press reset twice quickly." || !o.Retry {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	if strings.Contains(r.log(), "old firmware") {
		t.Errorf("screen.log says it keeps its firmware: %q", r.log())
	}
}

// A battery board unplugged and plugged in again after a flash stopped past START may keep its bootloader session
// (OTAFIX has no exit on losing USB power): the scans that see its port go and come back don't make it fresh, so
// the Board still says why, and the next flash still asks for the double press rather than send START into it.
func TestFlashReplugKeepsStaleSession(t *testing.T) {
	r, rak, req := failedPastStart(t)
	r.w.Unplug(rak)
	if _, err := r.b.Scan(); err != nil {
		t.Fatal(err)
	}
	r.w.Plug(rak)
	scan, err := r.b.Scan()
	if err != nil || len(scan.Ports) != 1 {
		t.Fatalf("scan %+v, %v", scan, err)
	}
	if n := r.b.Probe(context.Background(), scan.Ports[0]); n.Problem != flash.InBootloader ||
		!strings.HasPrefix(n.Reason, "Its last flash stopped part-way") {
		t.Errorf("probe after the replug: %+v", n)
	}
	before := starts(rak)
	req.Port = scan.Ports[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.w.At(2*time.Second, cancel)
	p := wait(t, r.b.Flash(ctx, req))
	if p.Steps[0].Text != "Press the board's reset button twice quickly" || starts(rak) != before {
		t.Errorf("%d STARTs, steps:\n%s", starts(rak)-before, steps(p))
	}
}

// A panic in a setup's progress (here on the goroutine sending its commands) still closes the port: the setup after
// it opens the same board, rather than finding meshflash's own exclusive open in the way (the world's rules also
// check every port is closed).
func TestSetupPanicClosesPort(t *testing.T) {
	r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
	b.AfterCommands(2, func() { panic("a test's panic") })
	p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()}))
	if o := p.Outcome; o.OK || !strings.Contains(o.Summary[0], "meshflash went wrong") {
		t.Fatalf("%s", outcome(o))
	}
	p = wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()}))
	if !p.Outcome.OK {
		t.Fatalf("the setup after the panic: %s\n%s", outcome(p.Outcome), steps(p))
	}
}
