package ui

// The Download screen's goldens and keys (docs/phase2-design.md 9.7): pictures of a scripted Job, delivered with
// settle.

import (
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
)

// downloadAt is the Download screen, from Confirm's y on the Xiao's repeater v1.18.0 not saved yet, its Job the
// test's own.
func downloadAt(t *testing.T, w, h int) (*driver, *fakeBackend, *flash.Job) {
	t.Helper()
	fb := screensBackend(t)
	fb.prepare = prepareAs(false, nil)
	j := newJob(flash.JobDownload)
	fb.download = func([]catalog.File) *flash.Job { return j }
	d := confirmDriver(t, w, h, fb, xiaoFlow(t, false))
	d.keys("y")
	if d.m.screen != scrDownload {
		t.Fatalf("y on a file not saved: screen %d", d.m.screen)
	}
	return d, fb, j
}

const xiaoFile = "Xiao_nrf52_repeater-v1.18.0-81dbae5.zip"

func downloading(done, total int64, note string) func(p *flash.Progress) {
	return func(p *flash.Progress) {
		p.Now = testNow
		p.Steps = []flash.Step{{Text: xiaoFile, State: flash.StepNow, Done: done, Total: total,
			Started: testNow.Add(-12 * time.Second), Note: note}}
	}
}

func TestDownloadProgress(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j := downloadAt(t, w, h)
		j.Set(downloading(212_000, 361_000, ""))
		d.settle()
		d.check(w, h)
	})
}

func TestDownloadUnknownSize(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j := downloadAt(t, w, h)
		j.Set(downloading(212_000, -1, ""))
		d.settle()
		d.check(w, h)
	})
}

// The flasher site didn't have it: the backend fell back to GitHub (its step's From is the file's GitHub URL), and
// the screen says so; before that, the step's From is the flasher site's URL, and the screen names that site.
func TestDownloadGitHub(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j := downloadAt(t, w, h)
		f := d.m.dl.files[0]
		if f.Fallback == "" {
			t.Fatalf("%s has no GitHub fallback in the test catalog", f.Base())
		}
		j.Set(downloading(40_000, 361_000, ""))
		j.Set(func(p *flash.Progress) { p.Steps[0].From = f.URL })
		d.settle()
		if from := d.m.downloadFrom(); from != "From "+host(f.URL) || strings.Contains(from, "GitHub") {
			t.Errorf("fetching from the flasher site, the screen says %q", from)
		}
		j.Set(func(p *flash.Progress) { p.Steps[0].From = f.Fallback })
		d.settle()
		d.check(w, h)
	})
}

// A failed download goes back to Confirm, which shows why in its body.
func TestDownloadFailed(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j := downloadAt(t, w, h)
		j.Set(downloading(40_000, 361_000, ""))
		d.settle()
		j.Finish(flash.Outcome{Title: "Download failed",
			Summary: []string{"Couldn't download: no network, or the server didn't answer."}})
		d.settle()
		d.check(w, h)
		if d.m.screen != scrConfirm {
			t.Errorf("screen %d", d.m.screen)
		}
	})
}

// The Job's end moves the screen on with no key (S16): back to Confirm, which checks the file again.
func TestDownloadLeavesAtDone(t *testing.T) {
	d, fb, j := downloadAt(t, 45, 10)
	j.Set(downloading(361_000, 361_000, ""))
	d.settle()
	fb.set(func(f *fakeBackend) { f.prepare = prepareAs(true, nil) })
	prepares := callCount(fb, "Prepare")
	j.Finish(flash.Outcome{OK: true, Title: "Downloaded"})
	d.settle()
	if d.m.screen != scrConfirm || d.m.job != nil {
		t.Fatalf("at Done: screen %d, job %v", d.m.screen, d.m.job)
	}
	if callCount(fb, "Prepare") != prepares+1 {
		t.Errorf("Confirm didn't check the file again: %v", fb.called())
	}
	bodyHas(d, "File saved 24 Nov, SHA-256 97539d20… ✓")
	bodyLacks(d, "Wi-Fi needed", "Not saved yet")
}

// Esc stops the download with no question and goes back at once; the stopped Job's end shows nothing new.
func TestDownloadEscStops(t *testing.T) {
	stopped := 0
	fb := screensBackend(t)
	fb.prepare = prepareAs(false, nil)
	j := flash.NewJob(flash.JobDownload, nil, func() { stopped++ })
	fb.download = func([]catalog.File) *flash.Job { return j }
	d := confirmDriver(t, 45, 10, fb, xiaoFlow(t, false))
	d.keys("y")
	j.Set(downloading(40_000, 361_000, ""))
	d.settle()
	d.keys("esc")
	if stopped != 1 || d.m.screen != scrConfirm || d.m.confirm != noConfirm {
		t.Fatalf("esc: stopped %d, screen %d, question %v", stopped, d.m.screen, d.m.confirm)
	}
	d.advance(time.Second)
	d.keys("y")
	if callCount(fb, "Download") != 1 {
		t.Errorf("y while the stopped download ends started another: %v", fb.called())
	}
	j.Finish(flash.Outcome{Stopped: true, Title: "Download stopped", Summary: []string{"Stopped."}})
	d.settle()
	if d.m.screen != scrConfirm || d.m.job != nil {
		t.Errorf("after the stop: screen %d", d.m.screen)
	}
	bodyLacks(d, "Stopped.")
}

// An ESP32 version's two files from Version's d: both steps, the first done.
func TestDownloadTwoFiles(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		f := flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4", "repeater", "v1.18.0")
		d, fb := versionAt(t, w, h, f, nil)
		j := newJob(flash.JobDownload)
		fb.download = func([]catalog.File) *flash.Job { return j }
		d.keys("d")
		j.Set(func(p *flash.Progress) {
			p.Now = testNow
			p.Steps = []flash.Step{
				{Text: "heltec_v4_repeater-v1.18.0-81dbae5.bin", State: flash.StepDone, Total: 361_000,
					Took: 31 * time.Second},
				{Text: "heltec_v4_repeater-v1.18.0-81dbae5-merged.bin", State: flash.StepNow, Done: 400_000,
					Total: 1_300_000, Started: testNow.Add(-35 * time.Second)},
			}
		})
		d.settle()
		d.check(w, h)
	})
}
