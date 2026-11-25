package ui

// The Flashing screen's goldens and keys (docs/phase2-design.md 9.8): pictures of a scripted Job.

import (
	"context"
	"testing"
	"time"

	"meshflash/internal/flash"
)

// flashingAt is the Flashing screen for flow f (picked kind first, by keys), its Job the test's own, with cancel
// counted, at the picture given.
func flashingAt(t *testing.T, w, h int, f flow, picture func(p *flash.Progress), keys ...string) (*driver, *flash.Job, *int) {
	t.Helper()
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	cancels := new(int)
	j := flash.NewJob(flash.JobFlash, nil, func() { *cancels++ })
	fb.flashJob = func(flash.Request) *flash.Job { return j }
	d := confirmDriver(t, w, h, fb, f)
	d.keys(append(keys, "y")...)
	if d.m.screen != scrFlashing {
		t.Fatalf("y on Confirm: screen %d", d.m.screen)
	}
	// The next y (to "Stop now?") comes later than a held y's repeat would.
	d.advance(time.Second)
	if picture != nil {
		j.Set(picture)
		d.settle()
	}
	return d, j, cancels
}

// steps is a picture of the steps given, on the backend's clock at testNow.
func pictureOf(risk flash.Risk, s ...flash.Step) func(p *flash.Progress) {
	return func(p *flash.Progress) { p.Now, p.Risk, p.Steps = testNow, risk, s }
}

func stepDone(text string, took time.Duration) flash.Step {
	return flash.Step{Text: text, State: flash.StepDone, Took: took}
}

func stepNow(text string, ago time.Duration) flash.Step {
	return flash.Step{Text: text, State: flash.StepNow, Started: testNow.Add(-ago)}
}

// The mock: the touch and the erase done, writing 212 of 361 KB.
func TestFlashingWriting(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		writing := stepNow("Writing", 38*time.Second)
		writing.Done, writing.Total = 212_000, 361_000
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased,
			stepDone("Into the bootloader (1200-baud touch)", time.Second), stepDone("Erased", 10*time.Second), writing))
		d.check(w, h)
	})
}

// An erase shows no progress: its time, and how long it takes (Expect).
func TestFlashingErasing(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		erasing := stepNow("Erasing", 4*time.Second)
		erasing.Expect = 10 * time.Second
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased,
			stepDone("Into the bootloader (1200-baud touch)", time.Second), erasing))
		d.check(w, h)
	})
}

// Esc asks, in words that say what stopping leaves now: no working firmware.
func TestFlashingStopAsks(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		writing := stepNow("Writing", 38*time.Second)
		writing.Done, writing.Total = 212_000, 361_000
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased,
			stepDone("Into the bootloader (1200-baud touch)", time.Second), stepDone("Erased", 10*time.Second), writing))
		d.keys("esc")
		d.check(w, h)
	})
}

// Before anything is written, stopping leaves the old firmware.
func TestFlashingStopBeforeWrite(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, _ := flashingAt(t, w, h, heltecFlow(t, false), pictureOf(flash.RiskNone,
			stepNow("Into the bootloader", 2*time.Second)), "u")
		d.keys("esc")
		d.check(w, h)
	})
}

// y to "Stop now?": Stopping…, the step's note saying how long, no more keys but Help.
func TestFlashingStopping(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		erasing := stepNow("Erasing", 2*time.Second)
		erasing.Expect = 10 * time.Second
		d, j, cancels := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased,
			stepDone("Into the bootloader (1200-baud touch)", time.Second), erasing))
		d.keys("esc", "y")
		erasing.Note = "Stopping once the erase ends (about 8 s)"
		j.Set(pictureOf(flash.RiskErased, stepDone("Into the bootloader (1200-baud touch)", time.Second), erasing))
		d.settle()
		d.check(w, h)
		if *cancels != 1 {
			t.Errorf("cancelled %d times", *cancels)
		}
	})
}

// Ctrl+C asks the same, ending "…and meshflash quits."
func TestFlashingQuitAsks(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		writing := stepNow("Writing", 38*time.Second)
		writing.Done, writing.Total = 212_000, 361_000
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased,
			stepDone("Into the bootloader (1200-baud touch)", time.Second), stepDone("Erased", 10*time.Second), writing))
		d.keys("ctrl+c")
		d.check(w, h)
	})
}

// A retry's eight steps at 40×10: the oldest fold into "✓ n earlier steps", the running one stays in view.
func TestFlashingNRF52Retry(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		written := stepDone("Writing", 42*time.Second)
		written.Total = 361_000
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskWritten,
			stepDone("Press the board's reset button twice quickly", 6*time.Second),
			stepDone("Erased", 10*time.Second),
			written,
			stepDone("The board checked the CRC", 0),
			stepDone("Back on USB (ttyACM0)", 2*time.Second),
			stepDone("Running v1.18.0", 0),
			stepDone("Back to 115200 baud", 0),
			stepNow("Checking it answers as a repeater", 3*time.Second)))
		d.check(w, h)
	})
}

// An ESP32 that takes more than one reset to reach its bootloader says which attempt.
func TestFlashingConnectAttempt(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		into := stepNow("Into the bootloader", 3*time.Second)
		into.Note = "attempt 2 of 4"
		d, _, _ := flashingAt(t, w, h, heltecFlow(t, false), pictureOf(flash.RiskNone, into), "u")
		d.check(w, h)
	})
}

// The Job's end, with no key, moves to Result in the same Update; "Don't unplug", the question and Help go (S16).
func TestFlashingMessagesKeepUp(t *testing.T) {
	writing := stepNow("Writing", 38*time.Second)
	writing.Done, writing.Total = 212_000, 361_000
	d, j, _ := flashingAt(t, 45, 10, xiaoFlow(t, false), pictureOf(flash.RiskErased, writing))
	d.keys("esc")
	if d.m.confirm != confirmStop {
		t.Fatalf("esc: question %v", d.m.confirm)
	}
	j.Finish(flash.Outcome{Title: "Flashing stopped", Summary: []string{"The board stopped answering."},
		State: "Its app is erased.", Retry: true})
	d.send(j.Snapshot())
	if d.m.screen != scrResult || d.m.confirm != noConfirm || d.m.job != nil {
		t.Fatalf("at Done: screen %d, question %v", d.m.screen, d.m.confirm)
	}
	bodyLacks(d, "Don't unplug", "Stop now?")
	bodyHas(d, "Flashing stopped", "Its app is erased.")

	d2, j2, _ := flashingAt(t, 45, 10, xiaoFlow(t, false), pictureOf(flash.RiskErased, writing))
	d2.keys("?")
	if !d2.m.help {
		t.Fatal("? didn't open Help")
	}
	j2.Finish(flash.Outcome{OK: true, Title: "Flashed"})
	d2.settle()
	if d2.m.help || d2.m.screen != scrResult {
		t.Errorf("Help still open (%v) or not on Result (%d)", d2.m.help, d2.m.screen)
	}
}

// Esc on Flashing asks: n and ? say no and do nothing else; y cancels once (S20).
func TestStopNeedsYes(t *testing.T) {
	writing := stepNow("Writing", 38*time.Second)
	writing.Done, writing.Total = 212_000, 361_000
	d, _, cancels := flashingAt(t, 45, 10, xiaoFlow(t, false), pictureOf(flash.RiskErased, writing))
	for _, k := range []string{"n", "?"} {
		d.keys("esc", k)
		if *cancels != 0 || d.m.confirm != noConfirm || d.m.help {
			t.Fatalf("%s to Stop now?: cancelled %d, question %v, Help %v", k, *cancels, d.m.confirm, d.m.help)
		}
	}
	d.keys("esc", "y")
	if *cancels != 1 || !d.m.fl.stopping {
		t.Fatalf("y: cancelled %d", *cancels)
	}
	d.advance(time.Second)
	d.keys("esc", "y")
	if *cancels != 1 || d.m.confirm != noConfirm {
		t.Errorf("esc while stopping asked again or cancelled again: %d", *cancels)
	}
}

// The steps' times move between pictures with the screens' clock (S17).
func TestFlashingTimeMoves(t *testing.T) {
	erasing := stepNow("Erasing", 4*time.Second)
	erasing.Expect = 10 * time.Second
	d, _, _ := flashingAt(t, 45, 10, xiaoFlow(t, false), pictureOf(flash.RiskErased, erasing))
	bodyHas(d, "Erasing (0:04, about 10 s)")
	d.advance(3 * time.Second)
	bodyHas(d, "Erasing (0:07, about 10 s)")
}

// Ctrl+C then y while flashing quits and stops the flash; the frame logs where it was.
func TestFlashingQuitStops(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	j := flash.NewJob(flash.JobFlash, nil, cancel)
	fb.flashJob = func(flash.Request) *flash.Job { return j }
	d := confirmDriver(t, 45, 10, fb, xiaoFlow(t, false))
	d.keys("y")
	d.advance(time.Second)
	d.keys("ctrl+c", "y")
	if !d.quit || ctx.Err() == nil {
		t.Errorf("quit %v, stopped %v", d.quit, ctx.Err() != nil)
	}
}
