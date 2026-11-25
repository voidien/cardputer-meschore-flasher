package ui

// Choose board's goldens and key tests (docs/phase2-design.md 9.2), over the fake backend.

import (
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/usbid"
)

// t096Port is a Heltec Mesh Node T096 running its app: an identity the USB table knows for one board alone.
func t096Port(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x239a, PID: 0x8029, BCD: 0x0100,
		Manufacturer: "Heltec", Product: "HT-n5262G", Serial: "C41F0B2E9A7D3365",
		Interface: usbid.InterfaceNRFApp, Driver: "cdc_acm", USBPath: "1-1.3"})
}

// ftdiPort is a USB bridge the table doesn't know (an FTDI FT232R): no candidates, kind unknown.
func ftdiPort(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x0403, PID: 0x6001, BCD: 0x0600,
		Manufacturer: "FTDI", Product: "FT232R USB UART", Serial: "A50285BI", Driver: "ftdi_sio", USBPath: "1-1.4"})
}

// chooseAt is Choose board for the port given (its probe answered as node says, when set), with f's changes first.
func chooseAt(t *testing.T, w, h int, p flash.Port, node *flash.Node, f func(fb *fakeBackend), opts ...func(*Options)) *driver {
	t.Helper()
	fb := screensBackend(t, p)
	if node != nil {
		fb.nodes[p.Name] = *node
	}
	if f != nil {
		f(fb)
	}
	d := newDriver(t, w, h, fb, opts...)
	d.keys("enter")
	if d.m.screen != scrChoose {
		t.Fatalf("enter on the Board didn't open Choose board: screen %d, note %q", d.m.screen, d.m.note)
	}
	return d
}

// The mock: a Heltec v4 saying "Heltec V4" (its `board` reply): that board first, the cursor on it.
func TestChooseBoardSays(t *testing.T) {
	n := repeaterNode(t, heltecV4Port("ttyACM0"), nodecli.RoleRepeater, "v1.17.1", "Heltec V4", "Hilltop 2")
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, heltecV4Port("ttyACM0"), &n, nil)
		d.check(w, h)
		if d.m.choose.sel != "Heltec v4" {
			t.Errorf("the cursor is on %q", d.m.choose.sel)
		}
	})
}

// The board picked last time for this serial number: the cursor starts there, and nothing is picked.
func TestChooseRemembered(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, xiaoPort("ttyACM0"), nil, nil, func(o *Options) {
			o.Config.Boards = map[string]string{xiaoPort("ttyACM0").Key: "Ikoka Nano"}
		})
		d.check(w, h)
		if d.m.choose.sel != "Ikoka Nano" || d.m.screen != scrChoose {
			t.Errorf("cursor %q, screen %d", d.m.choose.sel, d.m.screen)
		}
	})
}

// An identity the table knows for one board alone: the cursor starts on it (design 9.2's rule; the RAK 4631 the
// design names shares its identity with four other boards in the table, so a T096 shows the rule).
func TestChooseOnlyMatch(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, t096Port("ttyACM0"), nil, nil)
		d.check(w, h)
		if d.m.choose.sel != "Heltec Mesh Node T096" {
			t.Errorf("the cursor is on %q", d.m.choose.sel)
		}
	})
}

// f opens the find box: typing filters every group.
func TestChooseFind(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, xiaoPort("ttyACM0"), nil, nil)
		d.keys("f")
		d.typeText("xiao")
		d.check(w, h)
		if !d.m.screenTextBox() {
			t.Error("the find box isn't a text box")
		}
	})
}

func TestChooseFindNoMatch(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, xiaoPort("ttyACM0"), nil, nil)
		d.keys("f")
		d.typeText("xyz")
		d.check(w, h)
	})
}

// Enter keeps the filter, Esc clears it; in the box j, k, y and ? are letters.
func TestChooseFindKeys(t *testing.T) {
	d := chooseAt(t, 45, 10, xiaoPort("ttyACM0"), nil, nil)
	d.keys("f")
	d.typeText("j?k")
	if d.m.choose.filter != "j?k" || d.m.help {
		t.Fatalf("filter %q, Help %v", d.m.choose.filter, d.m.help)
	}
	d.keys("backspace", "backspace", "backspace")
	d.typeText("ikoka")
	d.keys("enter")
	if d.m.screenTextBox() || d.m.choose.filter != "ikoka" || !strings.HasPrefix(d.m.choose.sel, "Ikoka") {
		t.Fatalf("Enter: box open %v, filter %q, cursor %q", d.m.screenTextBox(), d.m.choose.filter, d.m.choose.sel)
	}
	bodyHas(d, "Find: ikoka")
	bodyLacks(d, "All boards")
	d.keys("f", "esc")
	if d.m.choose.filter != "" || d.m.screenTextBox() {
		t.Errorf("Esc didn't clear the filter: %q", d.m.choose.filter)
	}
	bodyHas(d, "All boards")
}

// A port the table doesn't know: every board under "All boards:", the nRF52 boards grey (the touch would go to who
// knows what).
func TestChooseUnknown(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, ftdiPort("ttyUSB0"), nil, nil)
		chooseTo(d, "Seeed Studio Wio Tracker L1 Pro")
		d.check(w, h)
	})
}

// Enter on a download-only board says why, and stays.
func TestChooseDownloadOnly(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, heltecV4Port("ttyACM0"), nil, nil)
		chooseTo(d, "RPI Pico W (rp2040) + WaveShare SX1262")
		d.keys("enter")
		d.check(w, h)
		if d.m.screen != scrChoose {
			t.Errorf("Enter on a download-only board left: screen %d", d.m.screen)
		}
	})
}

// The ESP32-C6 boards (ZephCore's): listed grey, refused before any download (owner question 8).
func TestChooseC6(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, heltecV4Port("ttyACM0"), nil, nil)
		chooseTo(d, "Seeed Studio Xiao C6")
		d.keys("enter")
		d.check(w, h)
		if d.m.screen != scrChoose {
			t.Errorf("Enter on an ESP32-C6 left: screen %d", d.m.screen)
		}
	})
}

// On an nRF52's port the ESP32 boards are grey, and Enter on one says why.
func TestChooseGreysOtherFamily(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := chooseAt(t, w, h, xiaoPort("ttyACM0"), nil, nil)
		chooseTo(d, "Heltec v4")
		d.keys("enter")
		d.check(w, h)
	})
}

// The probe carries on from the Board: "Asking…" first; when it answers, "The board says:" comes at the top and the
// cursor moves to that board, since the owner hasn't moved it.
func TestChooseProbing(t *testing.T) {
	n := repeaterNode(t, heltecV4Port("ttyACM0"), nodecli.RoleRepeater, "v1.17.1", "Heltec V4", "Hilltop 2")
	eachSize(t, func(t *testing.T, w, h int) {
		fb := screensBackend(t, heltecV4Port("ttyACM0"))
		fb.nodes["ttyACM0"] = n
		d := newDriver(t, w, h, fb)
		d.hold(true)
		d.keys("r")
		d.release(0) // the scan; the probe is held
		d.keys("enter")
		d.advance(time.Second)
		d.check(w, h)
		first := d.m.choose.sel
		d.releaseAll()
		bodyHas(d, "The board says: Heltec V4")
		bodyLacks(d, "Asking")
		if first == "Heltec v4" || d.m.choose.sel != "Heltec v4" {
			t.Errorf("the cursor was on %q, then %q", first, d.m.choose.sel)
		}
	})
}

// When the owner has moved the cursor, the answer doesn't move it.
func TestChooseAnswerKeepsTheCursor(t *testing.T) {
	n := repeaterNode(t, heltecV4Port("ttyACM0"), nodecli.RoleRepeater, "v1.17.1", "Heltec V4", "")
	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	fb.nodes["ttyACM0"] = n
	d := newDriver(t, 45, 10, fb)
	d.hold(true)
	d.keys("r")
	d.release(0)
	d.keys("enter", "j", "j")
	moved := d.m.choose.sel
	d.releaseAll()
	if d.m.choose.sel != moved {
		t.Errorf("the answer moved the cursor from %q to %q", moved, d.m.choose.sel)
	}
}

// Enter on a board that fits goes on to its roles, the probes stopped; Esc goes back to the Board.
func TestChooseKeys(t *testing.T) {
	d := chooseAt(t, 45, 10, heltecV4Port("ttyACM0"), nil, nil)
	chooseTo(d, "Heltec v4")
	d.keys("enter")
	if d.m.screen != scrRole || d.m.flow.dev == nil || d.m.flow.dev.Name != "Heltec v4" {
		t.Fatalf("Enter on Heltec v4: screen %d", d.m.screen)
	}
	if d.m.flow.port.Key == "" || d.m.cfg.Boards[d.m.flow.port.Key] != "Heltec v4" {
		t.Errorf("not remembered: %v", d.m.cfg.Boards)
	}
	d.keys("esc", "esc")
	if d.m.screen != scrBoard {
		t.Errorf("esc, esc: screen %d", d.m.screen)
	}
	if d.m.flow.dev != nil {
		t.Error("the flow wasn't let go on the Board")
	}
}

// Every catalog board is listed exactly once, whatever the port.
func TestChooseListsEveryBoardOnce(t *testing.T) {
	c := testCatalog(t)
	for _, p := range []flash.Port{heltecV4Port("ttyACM0"), rakPort("ttyACM0"), xiaoPort("ttyACM0"),
		cp2102Port("ttyUSB0"), ch340Port("ttyUSB0"), ftdiPort("ttyUSB0"), t096Port("ttyACM0")} {
		d := chooseAt(t, 45, 10, p, nil, nil)
		seen := map[string]int{}
		for _, r := range d.m.chooseRows() {
			if r.dev != nil {
				seen[r.dev.Name]++
			}
		}
		for _, dev := range c.Devices {
			if seen[dev.Name] != 1 {
				t.Errorf("%s: %s listed %d times", p.VIDPID(), dev.Name, seen[dev.Name])
			}
		}
		for _, r := range d.m.chooseRows() {
			if r.dev == nil {
				continue
			}
			if ok, _, _ := flash.Fits(*r.dev, p); ok != r.ok {
				t.Errorf("%s: %s fits %v, listed as %v", p.VIDPID(), r.dev.Name, ok, r.ok)
			}
			if r.dev.Type == catalog.NoFlash && r.ok {
				t.Errorf("%s: download-only %s isn't grey", p.VIDPID(), r.dev.Name)
			}
		}
	}
}
