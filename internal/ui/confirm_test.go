package ui

// The Confirm screen's goldens and its races (docs/phase2-design.md 9.6, 8.8), over the fake backend on the driver's
// clock.

import (
	"context"
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
)

// heltecFlow and xiaoFlow are the owner's two boards on their way to a repeater v1.18.0, their probes answered as a
// repeater v1.17.1 (nodeless when bare).
func heltecFlow(t testing.TB, bare bool) flow {
	f := flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4", "repeater", "v1.18.0")
	if !bare {
		n := repeaterNode(t, f.port, nodecli.RoleRepeater, "v1.17.1-d929643", "Heltec V4", "Hilltop 2")
		f.node = &n
	}
	return f
}

func xiaoFlow(t testing.TB, bare bool) flow {
	f := flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "repeater", "v1.18.0")
	if !bare {
		n := repeaterNode(t, f.port, nodecli.RoleRepeater, "v1.17.1-d929643", "Xiao nRF52840", "Hilltop 2")
		f.node = &n
	}
	return f
}

// confirmGolden is Confirm for f, Prepare answered as prepare says and the battery as given, after keys.
func confirmGolden(t *testing.T, f func(t testing.TB) flow, change func(fb *fakeBackend), keys ...string) {
	t.Helper()
	eachSize(t, func(t *testing.T, w, h int) {
		fb := screensBackend(t)
		fb.prepare = prepareAs(true, nil)
		if change != nil {
			change(fb)
		}
		d := confirmDriver(t, w, h, fb, f(t))
		d.keys(keys...)
		d.check(w, h)
	})
}

func heltec(t testing.TB) flow { return heltecFlow(t, false) }
func xiao(t testing.TB) flow   { return xiaoFlow(t, false) }

// An ESP32 has no kind until u or w: y does nothing yet (no preselection, SPEC.md 11).
func TestConfirmESP32NoKind(t *testing.T) { confirmGolden(t, heltec, nil) }

func TestConfirmESP32Update(t *testing.T) { confirmGolden(t, heltec, nil, "u") }

func TestConfirmESP32Wipe(t *testing.T) { confirmGolden(t, heltec, nil, "w") }

// The mock: an nRF52 has only an update, so y works straight away.
func TestConfirmNRF52(t *testing.T) { confirmGolden(t, xiao, nil) }

// A file not saved yet: y downloads it first.
func TestConfirmNotSaved(t *testing.T) {
	confirmGolden(t, xiao, func(fb *fakeBackend) { fb.prepare = prepareAs(false, nil) })
}

// Prepare still checking the file: its time shows (S17), y does nothing.
func TestConfirmPreparing(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		fb := screensBackend(t, xiaoPort("ttyACM0"))
		fb.prepare = prepareAs(true, nil)
		d := newDriver(t, w, h, fb)
		d.m.flow = xiaoFlow(t, false)
		d.hold(true)
		d.run(d.m.openConfirm(""))
		d.advance(time.Second)
		d.check(w, h)
		d.releaseAll()
		bodyLacks(d, "Checking the file")
	})
}

// No probe answered (a board running companion firmware, say): the board and its tty alone.
func TestConfirmNoProbe(t *testing.T) {
	confirmGolden(t, func(t testing.TB) flow { return xiaoFlow(t, true) }, nil)
}

// Prepared's notes, in plain words.
func TestConfirmNotes(t *testing.T) {
	confirmGolden(t, xiao, func(fb *fakeBackend) {
		fb.prepare = prepareAs(true, func(_ flash.Request, p *flash.Prepared) {
			p.Notes = []string{"The file needs SoftDevice S140 7.3.0, which the catalog says this board has."}
		})
	})
}

// The mock: 24%, the warning, the hint unchanged.
func TestConfirmLowBattery(t *testing.T) {
	confirmGolden(t, xiao, func(fb *fakeBackend) { fb.battery, fb.hasBattery = 24, true })
}

// y under 30% asks once more.
func TestConfirmLowBatteryAsks(t *testing.T) {
	confirmGolden(t, xiao, func(fb *fakeBackend) { fb.battery, fb.hasBattery = 24, true }, "y")
}

// A battery that can't be read asks as a low one does.
func TestConfirmBatteryUnknownAsks(t *testing.T) {
	confirmGolden(t, xiao, func(fb *fakeBackend) { fb.battery, fb.hasBattery = -1, true }, "y")
}

// An nRF52 board whose SoftDevice the catalog doesn't name asks a second y (owner question 20).
func TestConfirmUnchecked(t *testing.T) {
	confirmGolden(t, func(t testing.TB) flow {
		return flowFor(t, wioL1Port("ttyACM0"), "Seeed Studio Wio Tracker L1 EINK", "companionBle", "v1.18.0")
	}, func(fb *fakeBackend) {
		fb.prepare = prepareAs(true, func(_ flash.Request, p *flash.Prepared) {
			p.Ask = "The catalog doesn't say which SoftDevice this board has: if the file needs another, the " +
				"bootloader refuses it and its app is erased. Flash anyway?"
		})
	}, "y")
}

// A refusal replaces the file's check, and y does nothing.
func TestConfirmRefused(t *testing.T) {
	confirmGolden(t, xiao, func(fb *fakeBackend) {
		fb.prepare = prepareAs(true, func(_ flash.Request, p *flash.Prepared) {
			p.Problem = "the file needs SoftDevice S140 6.1.1 and this board has S140 7.3.0: its bootloader " +
				"would refuse it and erase the app"
		})
	}, "y")
}

// Unplugged: it says so, the mark is ○, y does nothing (TestStatusMarkFollowsTheBoard follows it there and back).
func TestConfirmBoardGone(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		fb := screensBackend(t)
		fb.prepare = prepareAs(true, nil)
		d := confirmDriver(t, w, h, fb, xiaoFlow(t, false))
		fb.set(func(f *fakeBackend) { f.scan.Ports = nil })
		d.advance(time.Second)
		d.keys("y")
		d.check(w, h)
		if fb.calledAny("Flash") {
			t.Error("flashed a board that isn't plugged in")
		}
	})
}

// r after a failed flash: Confirm again with the board and version, the State shown in place of the probe's answer,
// an ESP32's kind cleared (owner question 4).
func TestConfirmAfterFailedFlash(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		fb := screensBackend(t)
		fb.prepare = prepareAs(true, nil)
		j := newJob(flash.JobFlash)
		fb.flashJob = func(flash.Request) *flash.Job { return j }
		d := confirmDriver(t, w, h, fb, heltecFlow(t, false))
		d.keys("w", "y")
		if d.m.screen != scrFlashing {
			t.Fatalf("w, y: screen %d", d.m.screen)
		}
		board := heltecV4Port("ttyACM0")
		j.Finish(flash.Outcome{Title: "Flashing stopped", Summary: []string{"The board stopped answering while writing (212 of 1300 KB)."},
			State: "Partially written: the board has no working firmware.", Recovery: "Flash it again.", Retry: true,
			Board: &board})
		d.settle()
		d.keys("r")
		d.check(w, h)
		if d.m.screen != scrConfirm || d.m.flow.kind != 0 || d.m.flow.node != nil {
			t.Errorf("r: screen %d, kind %v, node %v", d.m.screen, d.m.flow.kind, d.m.flow.node)
		}
	})
}

// u, w and Esc; w on an nRF52 says it has only an update; y with no kind says to pick one.
func TestConfirmKeys(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	d := confirmDriver(t, 45, 10, fb, heltecFlow(t, false))
	d.keys("y")
	if d.m.note != "Press u to update or w for a full wipe first." || fb.calledAny("Flash") {
		t.Errorf("y with no kind: note %q", d.m.note)
	}
	d.keys("u")
	if d.m.flow.kind != catalog.Update {
		t.Errorf("u: %v", d.m.flow.kind)
	}
	d.keys("esc")
	if d.m.screen != scrVersion {
		t.Errorf("esc: screen %d", d.m.screen)
	}
	d2 := confirmDriver(t, 45, 10, fb, xiaoFlow(t, false))
	d2.keys("w")
	if d2.m.note != "An nRF52 has only an update." || d2.m.flow.kind != catalog.Update {
		t.Errorf("w on an nRF52: note %q, kind %v", d2.m.note, d2.m.flow.kind)
	}
}

// Two answers to u then w, released in reverse order: the screen shows the latest (Wipe) and flashes it.
func TestConfirmKindRace(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	fb.flashJob = func(flash.Request) *flash.Job { return newJob(flash.JobFlash) }
	d := confirmDriver(t, 45, 10, fb, heltecFlow(t, false))
	d.hold(true)
	d.keys("u", "w")
	if d.heldCmds() != 2 {
		t.Fatalf("%d Cmds held, want the two Prepares", d.heldCmds())
	}
	d.release(1)
	d.release(0)
	d.hold(false)
	bodyHas(d, "· full wipe", "Erases the whole chip")
	d.keys("y")
	if len(fb.requests) != 1 || fb.requests[0].Kind != catalog.Wipe {
		t.Fatalf("flashed %+v", fb.requests)
	}
}

// A kind picked while a y's battery reading is out drops that y: the new kind is never written on the old y.
func TestConfirmKindChangeDropsTheY(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	fb.flashJob = func(flash.Request) *flash.Job { return newJob(flash.JobFlash) }
	d := confirmDriver(t, 45, 10, fb, heltecFlow(t, false))
	d.keys("u")
	d.advance(time.Second)
	d.hold(true)
	d.keys("y", "w")
	if d.heldCmds() != 2 {
		t.Fatalf("%d Cmds held, want the battery reading and the Prepare", d.heldCmds())
	}
	d.release(1)
	d.release(0)
	d.hold(false)
	if fb.calledAny("Flash") {
		t.Fatalf("flashed the kind picked after the y: %+v", fb.requests)
	}
	bodyHas(d, "· full wipe")
	d.advance(time.Second)
	d.keys("y")
	if len(fb.requests) != 1 || fb.requests[0].Kind != catalog.Wipe {
		t.Errorf("y on the full wipe: %+v", fb.requests)
	}
}

// y does nothing until the latest Prepare has answered.
func TestConfirmYWaitsForPrepare(t *testing.T) {
	fb := screensBackend(t, xiaoPort("ttyACM0"))
	fb.prepare = prepareAs(true, nil)
	d := newDriver(t, 45, 10, fb)
	d.m.flow = xiaoFlow(t, false)
	d.hold(true)
	d.run(d.m.openConfirm(""))
	held := d.heldCmds()
	d.keys("y")
	if d.heldCmds() != held || fb.calledAny("Flash") || fb.calledAny("Download") {
		t.Fatalf("y before Prepare answered did something: %d Cmds held, %v", d.heldCmds(), fb.called())
	}
	d.releaseAll()
	d.advance(time.Second)
	d.keys("y")
	if callCount(fb, "Flash") != 1 {
		t.Errorf("y once Prepare answered: %v", fb.called())
	}
}

// One y, then another while its battery reading is out: one flash.
func TestEnterTwiceFlashesOnce(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	fb.battery, fb.hasBattery = 87, true
	fb.flashJob = func(flash.Request) *flash.Job { return newJob(flash.JobFlash) }
	d := confirmDriver(t, 45, 10, fb, xiaoFlow(t, false))
	d.hold(true)
	d.keys("y", "y")
	d.advance(time.Second)
	d.keys("y")
	d.releaseAll()
	d.keys("y")
	if n := callCount(fb, "Flash"); n != 1 {
		t.Errorf("%d flashes", n)
	}
}

// heldY presses y every 33 ms for d (a held key repeating, as Linux does after its first 250 ms).
func heldY(d *driver, upTo time.Duration) {
	for at := time.Duration(0); at < upTo; at += 33 * time.Millisecond {
		d.advance(33 * time.Millisecond)
		d.keys("y")
	}
}

// A held y answers no question and starts nothing (owner question 5): y every 33 ms for 2 s; then released, 0.6 s,
// and y flashes once.
func TestHeldYFlashesNothing(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	fb.battery, fb.hasBattery = 24, true
	fb.flashJob = func(flash.Request) *flash.Job { return newJob(flash.JobFlash) }
	d := confirmDriver(t, 45, 10, fb, xiaoFlow(t, false))
	d.keys("y")
	if d.m.confirm != confirmLowBattery {
		t.Fatalf("y at 24%%: question %v", d.m.confirm)
	}
	heldY(d, 2*time.Second)
	if fb.calledAny("Flash") || d.m.confirm != confirmLowBattery {
		t.Fatalf("a held y flashed (%v) or answered the question (%v)", fb.calledAny("Flash"), d.m.confirm)
	}
	d.advance(600 * time.Millisecond)
	d.keys("y")
	if n := callCount(fb, "Flash"); n != 1 || d.m.screen != scrFlashing {
		t.Errorf("%d flashes, screen %d", n, d.m.screen)
	}
}

// Both questions, each needing its own y (S20): n says no to the first; then y, y (released between) through both;
// a held y at the second answers nothing.
func TestLowBatteryAsksTwice(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, func(_ flash.Request, p *flash.Prepared) { p.Ask = "Flash anyway?" })
	fb.battery, fb.hasBattery = 24, true
	fb.flashJob = func(flash.Request) *flash.Job { return newJob(flash.JobFlash) }
	d := confirmDriver(t, 45, 10, fb, flowFor(t, wioL1Port("ttyACM0"), "Seeed Studio Wio Tracker L1 EINK",
		"companionBle", "v1.18.0"))
	d.keys("y", "n")
	if d.m.confirm != noConfirm || fb.calledAny("Flash") {
		t.Fatalf("n to the battery: question %v, %v", d.m.confirm, fb.called())
	}
	d.advance(time.Second)
	d.keys("y")
	d.advance(time.Second)
	d.keys("y")
	if d.m.confirm != confirmUnchecked {
		t.Fatalf("y to the battery: question %v", d.m.confirm)
	}
	heldY(d, time.Second)
	if d.m.confirm != confirmUnchecked || fb.calledAny("Flash") {
		t.Fatalf("a held y answered the second question")
	}
	d.advance(600 * time.Millisecond)
	d.keys("y")
	if n := callCount(fb, "Flash"); n != 1 {
		t.Errorf("%d flashes", n)
	}
}

// After a download, y waits for the new Prepared, and for half a second after Confirm shows again (design 8.8).
func TestConfirmYAfterDownloadWaits(t *testing.T) {
	setup := func(t *testing.T) (*driver, *fakeBackend, *flash.Job) {
		fb := screensBackend(t)
		fb.prepare = prepareAs(false, nil)
		j := newJob(flash.JobDownload)
		fb.download = func([]catalog.File) *flash.Job { return j }
		fb.flashJob = func(flash.Request) *flash.Job { return newJob(flash.JobFlash) }
		d := confirmDriver(t, 45, 10, fb, xiaoFlow(t, false))
		d.keys("y")
		if d.m.screen != scrDownload {
			t.Fatalf("y on a file not saved: screen %d", d.m.screen)
		}
		fb.set(func(f *fakeBackend) { f.prepare = prepareAs(true, nil) })
		d.advance(time.Second)
		return d, fb, j
	}
	t.Run("the new Prepared", func(t *testing.T) {
		d, fb, j := setup(t)
		d.hold(true)
		j.Finish(flash.Outcome{OK: true, Title: "Downloaded"})
		d.settle()
		if d.m.screen != scrConfirm {
			t.Fatalf("at the download's end: screen %d", d.m.screen)
		}
		d.keys("y")
		d.releaseAll()
		if fb.calledAny("Flash") {
			t.Fatal("y before the new Prepared flashed")
		}
		bodyHas(d, "File saved 24 Nov, SHA-256 97539d20… ✓")
		d.advance(600 * time.Millisecond)
		d.keys("y")
		if callCount(fb, "Flash") != 1 {
			t.Errorf("y once it was back: %v", fb.called())
		}
	})
	t.Run("half a second", func(t *testing.T) {
		d, fb, j := setup(t)
		j.Finish(flash.Outcome{OK: true, Title: "Downloaded"})
		d.settle()
		d.advance(300 * time.Millisecond)
		d.keys("y")
		if fb.calledAny("Flash") {
			t.Fatal("y 0.3 s after Confirm came back flashed")
		}
		d.advance(600 * time.Millisecond)
		d.keys("y")
		if callCount(fb, "Flash") != 1 {
			t.Errorf("y 0.9 s after: %v", fb.called())
		}
	})
}

// The flash is the one Confirm showed: the board, the version and the kind, Flash called in Update, the Job watched.
func TestConfirmFlashes(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	j := newJob(flash.JobFlash)
	fb.flashJob = func(flash.Request) *flash.Job { return j }
	d := confirmDriver(t, 45, 10, fb, heltecFlow(t, false))
	d.keys("u", "y")
	if len(fb.requests) != 1 {
		t.Fatalf("%d flashes", len(fb.requests))
	}
	r := fb.requests[0]
	if r.Device.Name != "Heltec v4" || r.Version.Name != "v1.18.0" || r.Kind != catalog.Update ||
		r.Port.Name != "ttyACM0" || r.Firmware.Role != "repeater" {
		t.Errorf("flashed %s %s %s %v on %s", r.Device.Name, r.Firmware.Role, r.Version.Name, r.Kind, r.Port.Name)
	}
	if d.m.job != j || d.m.screen != scrFlashing || !strings.HasSuffix(titleOf(d), "…") {
		t.Errorf("the Job isn't watched: screen %d, %q", d.m.screen, titleOf(d))
	}
}

// Ctrl+C while flashing asks first, in Flashing's words ending "…and meshflash quits."; n carries on; y quits,
// stopping the flash the safe way.
func TestQuitWhileFlashingAsks(t *testing.T) {
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	j := flash.NewJob(flash.JobFlash, nil, cancel)
	fb.flashJob = func(flash.Request) *flash.Job { return j }
	d := confirmDriver(t, 45, 10, fb, xiaoFlow(t, false))
	d.keys("y")
	j.Set(writing(151_000))
	d.settle()
	d.keys("ctrl+c")
	if d.m.confirm != confirmQuit {
		t.Fatalf("Ctrl+C while flashing: question %v", d.m.confirm)
	}
	bodyHas(d, "Stop now? The board will have no working")
	bodyHas(d, "meshflash quits.")
	d.keys("n")
	if d.quit || ctx.Err() != nil || d.m.confirm != noConfirm {
		t.Fatalf("n: quit %v, stopped %v", d.quit, ctx.Err() != nil)
	}
	d.advance(time.Second)
	d.keys("ctrl+c", "y")
	if !d.quit || ctx.Err() == nil {
		t.Errorf("y: quit %v, stopped %v", d.quit, ctx.Err() != nil)
	}
	if !strings.Contains(d.logText(), "quit while flashing") {
		t.Errorf("screen.log:\n%s", d.logText())
	}
}
