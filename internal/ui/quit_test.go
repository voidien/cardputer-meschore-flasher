package ui

// The integrator's tests of how the frame quits with a Job running (frame.go's ctrlC, quitNow, Shutdown, logStop):
// owner question 16, Ctrl+C while a setup is being sent quits at once, stopping it where it is, and screen.log says
// what the node was sent and is left with. No builder owns this file. Like lock_test.go they stay on the Board screen;
// the Jobs are the test's, never delivered to the screens, and the frame reads where they are with Job.Snapshot.

import (
	"context"
	"strings"
	"testing"

	"meshflash/internal/flash"
)

// runningJob is a Job of kind watched by the frame, whose cancel the test can see, at the picture set.
func runningJob(d *driver, kind flash.JobKind, picture func(p *flash.Progress)) (*flash.Job, context.Context) {
	d.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	j := flash.NewJob(kind, nil, cancel)
	d.run(d.m.watch(j))
	j.Set(picture)
	return j, ctx
}

func radioSaved(p *flash.Progress) {
	p.Planned, p.Risk = 20, flash.RiskRadioSent
	p.Steps = []flash.Step{
		{Text: "Set name to “Hilltop 2”", State: flash.StepDone},
		{Text: "Set radio 869.618 MHz, 62.5 kHz, SF8, CR8", State: flash.StepDone},
		{Text: "Set admin password ••••", State: flash.StepNow},
	}
}

func TestCtrlCWhileSettingUpQuitsAtOnce(t *testing.T) {
	d := newDriver(t, 45, 10, newFakeBackend())
	_, ctx := runningJob(d, flash.JobSetup, radioSaved)
	d.keys("ctrl+c")
	if !d.quit || d.m.confirm != noConfirm {
		t.Fatalf("Ctrl+C while setting up asked (%v) instead of quitting", d.m.confirm)
	}
	if ctx.Err() == nil {
		t.Error("the setup wasn't stopped")
	}
	log := d.logText()
	for _, want := range []string{
		"meshflash: quit while setting up (step 3 of 20: Set admin password ••••): stopping it the safe way.",
		"Its new radio is saved but it hasn't restarted, so it stays on its old radio until it does",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("screen.log lacks %q:\n%s", want, log)
		}
	}
	// internal/app calls Shutdown after Run: the line isn't written twice.
	if d.m.Shutdown() == nil {
		t.Error("Shutdown didn't return the Job for internal/app to wait for")
	}
	if n := strings.Count(d.logText(), "stopping it the safe way"); n != 1 {
		t.Errorf("the stop was logged %d times", n)
	}
}

// Before the node has saved anything, the log says so.
func TestCtrlCBeforeTheSetupSavedAnything(t *testing.T) {
	d := newDriver(t, 45, 10, newFakeBackend())
	runningJob(d, flash.JobSetup, func(p *flash.Progress) {
		p.Steps = []flash.Step{{Text: "Read the node's version", State: flash.StepNow}}
	})
	d.keys("ctrl+c")
	if !d.quit || !strings.Contains(d.logText(), "Nothing had been saved on the node.") {
		t.Errorf("quit %v, screen.log:\n%s", d.quit, d.logText())
	}
}

// An Esc hold or SIGTERM ends Run without the model seeing a quit: Shutdown stops the flash and writes the line.
func TestShutdownLogsWhatTheFlashLeaves(t *testing.T) {
	d := newDriver(t, 45, 10, newFakeBackend())
	j, ctx := runningJob(d, flash.JobFlash, writing(151_000))
	if got := d.m.Shutdown(); got != j {
		t.Fatal("Shutdown didn't return the running Job")
	}
	if ctx.Err() == nil {
		t.Error("the flash wasn't stopped")
	}
	want := "meshflash: told to end (Esc held, or SIGTERM) while flashing (step 3: Writing): stopping it the safe " +
		"way. It has no working firmware until it is flashed again."
	if !strings.Contains(d.logText(), want) {
		t.Errorf("screen.log lacks %q:\n%s", want, d.logText())
	}
}

// A download stops with the quit and needs no line: nothing saved changes.
func TestCtrlCWhileDownloadingQuitsQuietly(t *testing.T) {
	d := newDriver(t, 45, 10, newFakeBackend())
	_, ctx := runningJob(d, flash.JobDownload, func(p *flash.Progress) {
		p.Steps = []flash.Step{{Text: "heltec_v4_repeater.bin", State: flash.StepNow, Done: 1000, Total: 361_000}}
	})
	d.keys("ctrl+c")
	if !d.quit || ctx.Err() == nil {
		t.Errorf("quit %v, download stopped %v", d.quit, ctx.Err() != nil)
	}
	if strings.Contains(d.logText(), "stopping it") {
		t.Errorf("a download's stop was logged:\n%s", d.logText())
	}
}

// Ctrl+C on a flash these screens didn't start (it shouldn't happen) asks all the same, in the words that are sure
// whatever its picture says (SPEC 4 Flashing); n leaves it running.
func TestCtrlCOnAFlashFromElsewhereAsks(t *testing.T) {
	d := newDriver(t, 45, 10, newFakeBackend())
	_, ctx := runningJob(d, flash.JobFlash, writing(151_000))
	d.keys("ctrl+c")
	want := "Quit now? The flash under way stops the safe way, and meshflash quits."
	if d.m.confirm != confirmQuit || d.m.confirmText() != want {
		t.Errorf("question %v %q, want %q", d.m.confirm, d.m.confirmText(), want)
	}
	d.keys("n")
	if d.quit || ctx.Err() != nil {
		t.Errorf("n: quit %v, the flash stopped %v", d.quit, ctx.Err() != nil)
	}
}
