package ui

// The Result screen's goldens and keys (docs/phase2-design.md 9.9): Outcomes in the backend's words (4.2's table),
// shown as they are.

import (
	"strings"
	"testing"
	"time"

	"meshflash/internal/flash"
)

// resultAt is Result after a flash of f (the kind picked by keys) that ended with o.
func resultAt(t *testing.T, w, h int, f flow, o flash.Outcome, keys ...string) (*driver, *fakeBackend) {
	t.Helper()
	fb := screensBackend(t)
	fb.prepare = prepareAs(true, nil)
	j := newJob(flash.JobFlash)
	fb.flashJob = func(flash.Request) *flash.Job { return j }
	d := confirmDriver(t, w, h, fb, f)
	d.keys(append(keys, "y")...)
	j.Finish(o)
	d.settle()
	if d.m.screen != scrResult {
		t.Fatalf("at the flash's end: screen %d", d.m.screen)
	}
	return d, fb
}

func resultGolden(t *testing.T, f func(t testing.TB) flow, o flash.Outcome, keys ...string) {
	t.Helper()
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := resultAt(t, w, h, f(t), o, keys...)
		d.check(w, h)
	})
}

func portPtr(p flash.Port) *flash.Port { return &p }

// An ESP32 update, the MD5 checked; a repeater, so s sets it up.
func TestResultESP32(t *testing.T) {
	resultGolden(t, heltec, flash.Outcome{OK: true, Title: "Flashed", Setup: true,
		Board: portPtr(heltecV4Port("ttyACM0")), Summary: []string{
			"Wrote heltec_v4_repeater-v1.18.0-81dbae5.bin (361 KB) at 0x10000, keeping identity, contacts and pairing.",
			"Checked: the MD5 of the flash matches the file.",
			"It restarted and is back on USB (ttyACM0).",
		}}, "u")
}

// An nRF52 that came back running the version just flashed.
func TestResultNRF52(t *testing.T) {
	resultGolden(t, xiao, flash.Outcome{OK: true, Title: "Flashed", Setup: true,
		Board: portPtr(xiaoPort("ttyACM0")), Summary: []string{
			"The board checked the CRC and came back on USB (ttyACM0) running v1.18.0.",
		}})
}

// The mock: an nRF52 that stopped answering while writing, gone from USB (○).
func TestResultUnplugged(t *testing.T) {
	resultGolden(t, xiao, flash.Outcome{Title: "Flashing stopped", Retry: true,
		Summary:  []string{"The board stopped answering while writing (212 of 361 KB)."},
		State:    "Its app is erased.",
		Recovery: "Press r; meshflash then asks you to press reset twice quickly."})
}

// An ESP32 unplugged while writing: esp's Recovery, nine lines at 40 columns.
func TestResultUnpluggedESP32(t *testing.T) {
	resultGolden(t, heltec, flash.Outcome{Title: "Flashing stopped", Retry: true,
		Summary: []string{"The board went away while writing (212 of 361 KB)."},
		State:   "Partially written: the board has no working firmware.",
		Recovery: "Hold BOOT, press and release RESET, then release BOOT, so it starts in its ROM bootloader; " +
			"then flash it again."}, "u")
}

// The owner stopped it.
func TestResultStopped(t *testing.T) {
	resultGolden(t, xiao, flash.Outcome{Title: "Flashing stopped", Stopped: true, Retry: true,
		Board:    portPtr(rakBootloaderPort("ttyACM0")),
		Summary:  []string{"You stopped it while it was writing (151 of 361 KB)."},
		State:    "Its app is erased.",
		Recovery: "Press r; meshflash then asks you to press reset twice quickly."})
}

// Refused before the board was touched: nothing changed on it.
func TestResultNotFlashed(t *testing.T) {
	resultGolden(t, xiao, flash.Outcome{Title: "Not flashed", Retry: true,
		Summary:  []string{"The board isn't plugged in."},
		State:    "Nothing was sent to the board: it still has its old firmware.",
		Recovery: "Plug it in and press r."})
}

// STOP was sent and its ACK lost, and the app didn't come back.
func TestResultMayHaveWorked(t *testing.T) {
	resultGolden(t, xiao, flash.Outcome{Title: "Flashing stopped", Retry: true,
		Summary: []string{"The board went away as the last of the image was sent, before it said it had it."},
		State:   "Most likely written, but the board didn't come back.",
		Recovery: "Press its reset button once; if it doesn't start, press reset twice quickly and flash it " +
			"again."})
}

// nRF52 firmware with no console: "Flashed", and why meshflash can't check the version (owner question 7).
func TestResultNoConsole(t *testing.T) {
	resultGolden(t, func(t testing.TB) flow {
		return flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "companionBle", "v1.18.0")
	}, flash.Outcome{OK: true, Title: "Flashed", Board: portPtr(xiaoPort("ttyACM0")), Summary: []string{
		"The board checked the CRC and its bootloader took the image.",
		"It came back on USB (ttyACM0).",
		"meshflash can't ask this firmware its version.",
	}})
}

func TestResultMD5Mismatch(t *testing.T) {
	resultGolden(t, heltec, flash.Outcome{Title: "Flashing stopped", Retry: true, Board: portPtr(heltecV4Port("ttyACM0")),
		Summary:  []string{"Wrote 361 KB at 0x10000 and read it back."},
		State:    "Written, but the flash doesn't match the file.",
		Recovery: "Flash it again; if it fails the same way, try a full wipe."}, "u")
}

func TestResultWrongVersion(t *testing.T) {
	resultGolden(t, xiao, flash.Outcome{Title: "Flashing stopped", Retry: true, Board: portPtr(xiaoPort("ttyACM0")),
		Summary:  []string{"The board checked the CRC and came back on USB (ttyACM0)."},
		State:    "It runs v1.17.1, not the version just flashed: the bootloader most likely refused the image.",
		Recovery: "Press reset twice quickly, then flash again."})
}

// A long Summary scrolls with j/k above State, Recovery and the retry line, which stay in view.
func TestResultScrolls(t *testing.T) {
	o := flash.Outcome{Title: "Flashing stopped", Retry: true,
		Summary: []string{
			"Into the bootloader with the 1200-baud touch, then the erase (10 s).",
			"Writing went on for 38 s, the board answering every frame, until frame 212 had no answer.",
			"It was sent again twice, with no answer either time, and then the port went away.",
		},
		State:    "Its app is erased.",
		Recovery: "Press r; meshflash then asks you to press reset twice quickly."}
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := resultAt(t, w, h, xiaoFlow(t, false), o)
		d.keys("j")
		d.check(w, h)
		if w <= 45 && d.m.res.scroll.top == 0 {
			t.Error("j didn't scroll")
		}
		bodyHas(d, "Its app is erased.")
	})
}

// A Summary sentence too long for what the pinned part leaves (45×10: an unplugged ESP32's State and Recovery) is
// never cut to its first lines running into the State: the generic retry line moves to scroll after the Summary,
// so the first sentence shows whole, and j brings the retry line into view.
func TestResultFirstSentenceWhole(t *testing.T) {
	o := flash.Outcome{Title: "Flashing stopped", Retry: true,
		Summary: []string{"The board went away while writing (212 of 361 KB) at 0x10000, after the erase of that " +
			"region."},
		State:    "Partially written: the board has no working firmware.",
		Recovery: "Plug it back in, hold BOOT, press RESET and release BOOT, then press r."}
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := resultAt(t, w, h, xiaoFlow(t, false), o)
		v := d.view()
		for _, l := range wrap(o.Summary[0], w) {
			if !strings.Contains(v, l) {
				t.Errorf("the Summary's line %q isn't on screen:\n%s", l, v)
			}
		}
		bodyHas(d, "Partially written")
		for range 4 {
			d.keys("j")
		}
		bodyHas(d, "Press r to try again")
	})
}

// enter and esc go back to the Board; s opens Setup on the board as it is now; r goes back to Confirm.
func TestResultKeys(t *testing.T) {
	ok := flash.Outcome{OK: true, Title: "Flashed", Setup: true, Board: portPtr(xiaoPort("ttyACM1")),
		Summary: []string{"Flashed."}}
	d, _ := resultAt(t, 45, 10, xiaoFlow(t, false), ok)
	if !strings.HasSuffix(titleOf(d), "●") {
		t.Errorf("the board back as ttyACM1 is the same board (its serial), but the mark is %q", titleOf(d))
	}
	d.keys("r")
	if d.m.screen != scrResult {
		t.Errorf("r with no Retry: screen %d", d.m.screen)
	}
	d.keys("s")
	if d.m.screen != scrSetup {
		t.Errorf("s: screen %d", d.m.screen)
	}
	d2, _ := resultAt(t, 45, 10, xiaoFlow(t, false), ok)
	d2.keys("enter")
	if d2.m.screen != scrBoard {
		t.Errorf("enter: screen %d", d2.m.screen)
	}
	failed := flash.Outcome{Title: "Flashing stopped", Retry: true, State: "Its app is erased.",
		Board: portPtr(xiaoPort("ttyACM0"))}
	d3, fb := resultAt(t, 45, 10, xiaoFlow(t, false), failed)
	d3.keys("s")
	if d3.m.screen != scrResult {
		t.Errorf("s with no Setup: screen %d", d3.m.screen)
	}
	d3.advance(time.Second)
	d3.keys("r")
	if d3.m.screen != scrConfirm || callCount(fb, "Prepare") != 2 {
		t.Errorf("r: screen %d, %v", d3.m.screen, fb.called())
	}
	bodyHas(d3, "Its app is erased.")
	d3.keys("esc", "esc", "esc", "esc")
	if d3.m.screen != scrBoard {
		t.Errorf("esc from Confirm back to the Board: screen %d", d3.m.screen)
	}
}
