package ui

// The key hints of every screen but Setup, in every state, at every size (S12; docs/phase2-design.md 15,
// TestHintsFit): never cut, and "? help" in each but a text box's, a question's, Help's and the lock page's.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
)

// hintState is one screen in one state.
type hintState struct {
	name string
	make func(t *testing.T, w, h int) *driver
	// noHelp says the hint rightly has no "? help": a text box (? is a letter there), a question, Help.
	noHelp bool
}

// hintStates are the screens in each of their states, for TestHintsFit, TestResize and TestHelpKeysLineUp.
var hintStates = []hintState{
	{name: "board, no board", make: func(t *testing.T, w, h int) *driver { return boardAt(t, w, h, nil, nil, nil) }},
	{name: "board, ports", make: func(t *testing.T, w, h int) *driver {
		return boardAt(t, w, h, []flash.Port{heltecV4Port("ttyACM0"), cp2102Port("ttyUSB0")}, nil, nil)
	}},
	{name: "board, settable", make: probedBoard},
	{name: "board, settable, no catalog", make: func(t *testing.T, w, h int) *driver {
		d := probedBoard(t, w, h)
		d.m.cat = nil
		return d
	}},
	{name: "board, no catalog", make: func(t *testing.T, w, h int) *driver {
		return boardAt(t, w, h, []flash.Port{heltecV4Port("ttyACM0")}, nil, func(fb *fakeBackend) { fb.cat = nil })
	}},
	{name: "board, probing", make: func(t *testing.T, w, h int) *driver {
		d := boardAt(t, w, h, []flash.Port{heltecV4Port("ttyACM0")}, nil, nil)
		d.hold(true)
		d.keys("r")
		d.release(0)
		return d
	}},
	{name: "choose", make: func(t *testing.T, w, h int) *driver {
		return chooseAt(t, w, h, xiaoPort("ttyACM0"), nil, nil)
	}},
	{name: "choose, find box", noHelp: true, make: func(t *testing.T, w, h int) *driver {
		d := chooseAt(t, w, h, xiaoPort("ttyACM0"), nil, nil)
		d.keys("f")
		d.typeText("ik")
		return d
	}},
	{name: "choose, filter kept", make: func(t *testing.T, w, h int) *driver {
		d := chooseAt(t, w, h, xiaoPort("ttyACM0"), nil, nil)
		d.keys("f")
		d.typeText("ik")
		d.keys("enter")
		return d
	}},
	{name: "role", make: func(t *testing.T, w, h int) *driver {
		return roleAt(t, w, h, wioL1Port("ttyACM0"), "Seeed Studio Wio Tracker L1 Pro")
	}},
	{name: "version", make: func(t *testing.T, w, h int) *driver {
		d, _ := versionAt(t, w, h, heltecFlow(t, true), nil)
		return d
	}},
	{name: "notes", make: func(t *testing.T, w, h int) *driver { return notesAt(t, w, h, notesOf(t, "v1.14.1")) }},
	{name: "confirm, ESP32, no kind", make: confirmState(true, heltec, nil)},
	{name: "confirm, ESP32, saved", make: confirmState(true, heltec, nil, "u")},
	{name: "confirm, ESP32, not saved", make: confirmState(false, heltec, nil, "w")},
	{name: "confirm, ESP32, refused", make: confirmState(true, heltec, func(fb *fakeBackend) {
		fb.prepare = prepareAs(true, func(req flash.Request, p *flash.Prepared) {
			if req.Kind == catalog.Wipe {
				p.Problem = "this version has no full-wipe image"
			}
		})
	}, "w")},
	{name: "confirm, nRF52, saved", make: confirmState(true, xiao, nil)},
	{name: "confirm, nRF52, not saved", make: confirmState(false, xiao, nil)},
	{name: "confirm, nRF52, refused", make: confirmState(true, xiao, func(fb *fakeBackend) {
		fb.prepare = prepareAs(true, func(_ flash.Request, p *flash.Prepared) { p.Problem = "the file has changed" })
	})},
	{name: "confirm, preparing", make: func(t *testing.T, w, h int) *driver {
		fb := screensBackend(t, xiaoPort("ttyACM0"))
		fb.prepare = prepareAs(true, nil)
		d := newDriver(t, w, h, fb)
		d.m.flow = xiaoFlow(t, false)
		d.hold(true)
		d.run(d.m.openConfirm(""))
		return d
	}},
	{name: "confirm, board gone", make: func(t *testing.T, w, h int) *driver {
		fb := screensBackend(t)
		fb.prepare = prepareAs(true, nil)
		d := confirmDriver(t, w, h, fb, xiaoFlow(t, false))
		fb.set(func(f *fakeBackend) { f.scan.Ports = nil })
		d.advance(time.Second)
		return d
	}},
	{name: "confirm, low battery question", noHelp: true, make: confirmState(true, xiao, func(fb *fakeBackend) {
		fb.battery, fb.hasBattery = 24, true
	}, "y")},
	{name: "download", make: func(t *testing.T, w, h int) *driver {
		d, _, j := downloadAt(t, w, h)
		j.Set(downloading(212_000, 361_000, ""))
		d.settle()
		return d
	}},
	{name: "flashing", make: func(t *testing.T, w, h int) *driver {
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased, stepNow("Erasing", time.Second)))
		return d
	}},
	{name: "flashing, stopping", make: func(t *testing.T, w, h int) *driver {
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased, stepNow("Erasing", time.Second)))
		d.keys("esc", "y")
		return d
	}},
	{name: "flashing, stop question", noHelp: true, make: func(t *testing.T, w, h int) *driver {
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased, stepNow("Erasing", time.Second)))
		d.keys("esc")
		return d
	}},
	{name: "result, set up", make: resultState(flash.Outcome{OK: true, Title: "Flashed", Setup: true,
		Board: portPtr(xiaoPort("ttyACM0")), Summary: []string{"Flashed."}})},
	{name: "result, set up, scrolls", make: resultState(flash.Outcome{OK: true, Title: "Flashed", Setup: true,
		Board: portPtr(xiaoPort("ttyACM0")), Summary: longSummary})},
	{name: "result, done", make: resultState(flash.Outcome{OK: true, Title: "Flashed", Summary: []string{"Flashed."}})},
	{name: "result, done, scrolls", make: resultState(flash.Outcome{OK: true, Title: "Flashed", Summary: longSummary})},
	{name: "result, retry", make: resultState(flash.Outcome{Title: "Flashing stopped", Retry: true,
		State: "Its app is erased."})},
	{name: "result, retry, scrolls", make: resultState(flash.Outcome{Title: "Flashing stopped", Retry: true,
		State: "Its app is erased.", Summary: longSummary})},
	{name: "update, fetching", make: func(t *testing.T, w, h int) *driver {
		d := newDriver(t, w, h, screensBackend(t))
		d.hold(true)
		d.keys("u")
		return d
	}},
	{name: "update, done", make: func(t *testing.T, w, h int) *driver {
		d, _ := updateAt(t, w, h, func(fb *fakeBackend) { fb.updated = testCatalog(t) })
		return d
	}},
	{name: "update, failed", make: func(t *testing.T, w, h int) *driver {
		d, _ := updateAt(t, w, h, func(fb *fakeBackend) { fb.updateErr = errNetworkDown })
		return d
	}},
	{name: "files", make: func(t *testing.T, w, h int) *driver {
		d, _ := filesAt(t, w, h, savedFixture(t))
		return d
	}},
	{name: "files, empty", make: func(t *testing.T, w, h int) *driver {
		d, _ := filesAt(t, w, h, nil)
		return d
	}},
	{name: "files, delete question", noHelp: true, make: func(t *testing.T, w, h int) *driver {
		d, _ := filesAt(t, w, h, savedFixture(t))
		d.keys("x")
		return d
	}},
}

// longSummary is a Summary too long for 40×10, so Result scrolls.
var longSummary = []string{
	"Into the bootloader with the 1200-baud touch, then the erase (10 s).",
	"Writing went on for 38 s, the board answering every frame, until frame 212 had no answer.",
	"It was sent again twice, with no answer either time, and then the port went away.",
	"The board then came back on USB as its bootloader.",
}

// confirmState makes Confirm for f with its file saved or not, after keys.
func confirmState(saved bool, f func(testing.TB) flow, change func(fb *fakeBackend), keys ...string) func(t *testing.T, w, h int) *driver {
	return func(t *testing.T, w, h int) *driver {
		fb := screensBackend(t)
		fb.prepare = prepareAs(saved, nil)
		if change != nil {
			change(fb)
		}
		d := confirmDriver(t, w, h, fb, f(t))
		d.keys(keys...)
		return d
	}
}

// resultState makes Result for a Xiao flash that ended with o.
func resultState(o flash.Outcome) func(t *testing.T, w, h int) *driver {
	return func(t *testing.T, w, h int) *driver {
		d, _ := resultAt(t, w, h, xiaoFlow(t, false), o)
		return d
	}
}

func TestHintsFit(t *testing.T) {
	for _, st := range hintStates {
		t.Run(st.name, func(t *testing.T) {
			eachSize(t, func(t *testing.T, w, h int) {
				d := st.make(t, w, h)
				hint := ansi.Strip(hintOf(d))
				if width(hint) > w || (width(hint) == w && strings.HasSuffix(hint, "…")) {
					t.Errorf("the hint is cut: %q", hint)
				}
				if _, own := d.m.screenView(w, h-2); !d.m.help && d.m.confirm == noConfirm && own != hint {
					t.Errorf("the hint shown %q isn't the screen's %q", hint, own)
				}
				switch has := strings.HasSuffix(hint, "? help"); {
				case st.noHelp && has:
					t.Errorf("%q offers Help where ? does nothing", hint)
				case !st.noHelp && !has:
					t.Errorf("%q has no \"? help\"", hint)
				}
				if len(strings.Fields(hint)) > 0 && strings.Contains(hint, "   ") {
					t.Errorf("%q has three spaces running", hint)
				}
			})
		})
	}
}
