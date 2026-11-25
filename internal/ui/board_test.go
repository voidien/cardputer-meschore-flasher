package ui

// The Board screen's goldens and key tests (docs/phase2-design.md 9.1), over the fake backend.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/usbid"
)

// boardGolden is the Board with the ports given, each probe answered as answers says (by tty), then f's changes.
func boardGolden(t *testing.T, ports []flash.Port, answers map[string]flash.Node, f func(fb *fakeBackend)) {
	t.Helper()
	eachSize(t, func(t *testing.T, w, h int) {
		boardAt(t, w, h, ports, answers, f).check(w, h)
	})
}

func boardAt(t *testing.T, w, h int, ports []flash.Port, answers map[string]flash.Node, f func(fb *fakeBackend)) *driver {
	t.Helper()
	fb := screensBackend(t, ports...)
	for k, n := range answers {
		fb.nodes[k] = n
	}
	if f != nil {
		f(fb)
	}
	return newDriver(t, w, h, fb)
}

func TestBoardNoBoard(t *testing.T) { boardGolden(t, nil, nil, nil) }

func TestBoardNoCatalog(t *testing.T) {
	boardGolden(t, []flash.Port{heltecV4Port("ttyACM0")}, nil, func(fb *fakeBackend) { fb.cat = nil })
}

func TestBoardDamagedCatalog(t *testing.T) {
	boardGolden(t, []flash.Port{heltecV4Port("ttyACM0")}, nil, func(fb *fakeBackend) {
		fb.cat, fb.catErr = nil, fmt.Errorf("%w: config.json no longer matches what was saved", catalog.ErrDamaged)
	})
}

// No permission to open ports: the dialout commands, each on its own line.
func TestBoardNoPermission(t *testing.T) {
	boardGolden(t, []flash.Port{rakPort("ttyACM0")},
		map[string]flash.Node{"ttyACM0": {Port: rakPort("ttyACM0"), Problem: flash.NoPermission}},
		func(fb *fakeBackend) { fb.scan.Dialout, fb.scan.Hint = false, usbid.DialoutHint })
}

// The mock: a Xiao running repeater v1.17.1 “Hilltop 2” and a CP2102 that stays silent.
func TestBoardProbed(t *testing.T) {
	boardGolden(t, []flash.Port{xiaoPort("ttyACM0"), cp2102Port("ttyUSB0")}, map[string]flash.Node{
		"ttyACM0": repeaterNode(t, xiaoPort("ttyACM0"), nodecli.RoleRepeater, "v1.17.1-d929643", "Xiao nRF52840",
			"Hilltop 2"),
	}, nil)
}

// A Heltec v4's USB identity is the ESP32-S3's own: 27 boards share it.
func TestBoardManyMatches(t *testing.T) {
	boardGolden(t, []flash.Port{heltecV4Port("ttyACM0")}, nil, nil)
}

// A probe under way says so, with its time (S17), and the mark is "…".
func TestBoardProbing(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := boardAt(t, w, h, []flash.Port{heltecV4Port("ttyACM0")}, nil, nil)
		d.hold(true)
		d.keys("r")
		d.release(0) // the scan; the probe is held
		d.advance(time.Second)
		d.check(w, h)
		d.releaseAll()
	})
}

// A second port waits its turn: one probe at a time.
func TestBoardQueued(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := boardAt(t, w, h, []flash.Port{xiaoPort("ttyACM0"), cp2102Port("ttyUSB0")}, nil, nil)
		d.hold(true)
		d.keys("r")
		d.release(0)
		d.advance(time.Second)
		d.check(w, h)
		d.releaseAll()
	})
}

func TestBoardStuck(t *testing.T) {
	boardGolden(t, []flash.Port{heltecV4Port("ttyACM0")},
		map[string]flash.Node{"ttyACM0": {Port: heltecV4Port("ttyACM0"), Problem: flash.Stuck}}, nil)
}

// An nRF52 in its bootloader is never opened: ready to flash.
func TestBoardBootloader(t *testing.T) {
	boardGolden(t, []flash.Port{rakBootloaderPort("ttyACM0")}, nil, nil)
}

func TestBoardBootloaderStale(t *testing.T) {
	boardGolden(t, []flash.Port{rakBootloaderPort("ttyACM0")}, map[string]flash.Node{"ttyACM0": {
		Port: rakBootloaderPort("ttyACM0"), Problem: flash.InBootloader,
		Reason: "Its last flash stopped part-way: meshflash asks you to press reset twice quickly when you flash it.",
	}}, nil)
}

func TestBoardBootloaderUnconfirmed(t *testing.T) {
	boardGolden(t, []flash.Port{rakBootloaderUnconfirmedPort("ttyACM0")}, map[string]flash.Node{"ttyACM0": {
		Port: rakBootloaderUnconfirmedPort("ttyACM0"), Problem: flash.InBootloader,
		Reason: "Looks like its bootloader, but meshflash can't confirm it: press reset twice quickly.",
	}}, nil)
}

func TestBoardBusy(t *testing.T) {
	boardGolden(t, []flash.Port{cp2102Port("ttyUSB0")},
		map[string]flash.Node{"ttyUSB0": {Port: cp2102Port("ttyUSB0"), Problem: flash.Busy}}, nil)
}

// A repeater too old to set up says why, and s says it can't.
func TestBoardNotSettable(t *testing.T) {
	n := repeaterNode(t, rakPort("ttyACM0"), nodecli.RoleRepeater, "v1.10.0", "RAK 4631", "")
	n.Problem, n.Reason = flash.NotSettable, "flash v1.11.0 or newer to set it up here." // the backend's words
	boardGolden(t, []flash.Port{rakPort("ttyACM0")}, map[string]flash.Node{"ttyACM0": n}, nil)
}

// ZephCore answers like MeshCore, but meshflash never sets it up: s says why not (SPEC.md 4).
func TestBoardZephCoreNoSetup(t *testing.T) {
	n := repeaterNode(t, rakPort("ttyACM0"), nodecli.RoleRepeater, "1.18.2-zephcore", "RAK 4631", "")
	n.Problem = flash.NotSettable
	n.Reason = "meshflash sets up only MeshCore's own repeater and room-server firmware." // the backend's words
	eachSize(t, func(t *testing.T, w, h int) {
		fb := screensBackend(t, rakPort("ttyACM0"))
		fb.nodes["ttyACM0"] = n
		d := newDriver(t, w, h, fb)
		d.keys("s")
		d.check(w, h)
		if d.m.screen != scrBoard || fb.calledAny("ReadNode") {
			t.Errorf("s on ZephCore left the Board (%d) or read the node", d.m.screen)
		}
	})
}

func TestBoardProbeFailed(t *testing.T) {
	boardGolden(t, []flash.Port{heltecV4Port("ttyACM0")}, map[string]flash.Node{"ttyACM0": {
		Port: heltecV4Port("ttyACM0"), Problem: flash.Failed, Reason: "the port stopped answering mid-reply"}}, nil)
}

// The catalog line while the saved catalog is still being read, and when it can't be read at all (not damaged, not
// missing): SPEC 4 Board's words, which no golden holds.
func TestBoardCatalogLine(t *testing.T) {
	d := newBareDriver(t)
	d.hold(true)
	d.start(45, 10, screensBackend(t, heltecV4Port("ttyACM0")), nil)
	bodyHas(d, "Reading the saved catalog…")
	d.releaseAll()
	bodyLacks(d, "Reading the saved catalog")
	bodyHas(d, "Catalog from 24 Nov: newest v1.18.0")

	d = boardAt(t, 45, 10, []flash.Port{heltecV4Port("ttyACM0")}, nil, func(fb *fakeBackend) {
		fb.cat, fb.catErr = nil, errors.New("open config.json: permission denied")
	})
	bodyHas(d, "Can't read the saved catalog: ")
	bodyLacks(d, "No catalog saved", "is damaged")
}

func TestBoardScanError(t *testing.T) {
	boardGolden(t, nil, nil, func(fb *fakeBackend) {
		fb.scanErr = errors.New("open /sys/class/tty: permission denied")
	})
}

// Five ports: the list scrolls to keep the selection in view, the catalog line staying at the bottom.
func TestBoardScrolls(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		ports := []flash.Port{heltecV4Port("ttyACM0"), rakPort("ttyACM1"), xiaoPort("ttyACM2"),
			cp2102Port("ttyUSB0"), ch340Port("ttyUSB1")}
		d := boardAt(t, w, h, ports, nil, nil)
		d.keys("j", "j", "j", "j")
		d.check(w, h)
		if p, _ := d.m.boardSelected(); p.Name != "ttyUSB1" {
			t.Errorf("selected %s", p.Name)
		}
	})
}

// A port that goes takes its lines with it at the next scan, with no key (S16); the selection follows its port.
func TestBoardPortGoneDropsItsLines(t *testing.T) {
	fb := screensBackend(t, heltecV4Port("ttyACM0"), xiaoPort("ttyACM1"))
	fb.nodes["ttyACM1"] = repeaterNode(t, xiaoPort("ttyACM1"), nodecli.RoleRepeater, "v1.17.1", "", "Hilltop 2")
	d := newDriver(t, 45, 10, fb)
	d.keys("j")
	bodyHas(d, "ttyACM0", "Hilltop 2")
	fb.set(func(f *fakeBackend) { f.scan.Ports = []flash.Port{xiaoPort("ttyACM1")} })
	d.advance(time.Second)
	bodyLacks(d, "ttyACM0", "one of 27")
	bodyHas(d, "▸ ttyACM1", "Hilltop 2")
	fb.set(func(f *fakeBackend) { f.scan.Ports = nil })
	d.advance(time.Second)
	bodyLacks(d, "ttyACM1", "Hilltop 2")
	bodyHas(d, "Plug a LoRa board into the USB-A port.")
	if !strings.HasSuffix(titleOf(d), "○") {
		t.Errorf("no board, the mark is %q", titleOf(d))
	}
}

// A probe answer clears "Asking…" with no key (S16), and the dialout block goes once a probe opened a port.
func TestBoardMessagesKeepUp(t *testing.T) {
	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	fb.scan.Dialout, fb.scan.Hint = false, usbid.DialoutHint
	d := newDriver(t, 45, 10, fb)
	bodyLacks(d, "dialout") // the probe opened the port: the group must be there after all
	d.hold(true)
	d.keys("r")
	d.release(0)
	bodyHas(d, "Asking it what")
	if !strings.HasSuffix(titleOf(d), "…") {
		t.Errorf("probing, the mark is %q", titleOf(d))
	}
	d.releaseAll()
	bodyLacks(d, "Asking")
}

// The keys: enter needs a catalog; s needs a repeater or room server; u, c and esc leave; r asks every port again.
func TestBoardKeys(t *testing.T) {
	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	fb.cat = nil
	d := newDriver(t, 45, 10, fb)
	d.keys("enter")
	if d.m.screen != scrBoard || d.m.note != "Press u to fetch the catalog first." {
		t.Errorf("enter with no catalog: screen %d, note %q", d.m.screen, d.m.note)
	}
	d.keys("s")
	if d.m.note != "s sets up a repeater or room server; this board didn't answer as one." {
		t.Errorf("s on a silent board: %q", d.m.note)
	}
	probes := callCount(fb, "Probe")
	d.keys("r")
	if callCount(fb, "Probe") != probes+1 {
		t.Errorf("r didn't ask the port again: %v", fb.called())
	}
	d.keys("c")
	if d.m.screen != scrFiles {
		t.Errorf("c: screen %d", d.m.screen)
	}
	d.keys("esc", "esc")
	if !d.quit {
		t.Error("esc on the Board didn't quit")
	}
}

// s on a repeater cancels the probes and opens Setup for that board with the board remembered for it.
func TestBoardSetUp(t *testing.T) {
	fb := screensBackend(t, xiaoPort("ttyACM0"))
	fb.nodes["ttyACM0"] = repeaterNode(t, xiaoPort("ttyACM0"), nodecli.RoleRepeater, "v1.17.1", "", "Hilltop 2")
	d := newDriver(t, 45, 10, fb, func(o *Options) {
		o.Config.Boards = map[string]string{xiaoPort("ttyACM0").Key: "Seeed Studio Xiao nRF52 WIO"}
	})
	if hintOf(d) != "enter choose  s set up  u update  ? help" {
		t.Errorf("a settable port's hint: %q", hintOf(d))
	}
	d.keys("s")
	if d.m.screen != scrSetup {
		t.Fatalf("s didn't open Setup: screen %d", d.m.screen)
	}
	if d.m.board.probing != "" {
		t.Error("a probe still runs on Setup")
	}
}

// s while the port is still being asked says to wait, not that it didn't answer; once the answer comes (a repeater,
// with no key pressed) the note goes, since it no longer says what is true (S16), and s is offered.
func TestBoardSetUpWhileAsking(t *testing.T) {
	fb := screensBackend(t, xiaoPort("ttyACM0"))
	fb.nodes["ttyACM0"] = repeaterNode(t, xiaoPort("ttyACM0"), nodecli.RoleRepeater, "v1.17.1", "", "Hilltop 2")
	d := newDriver(t, 45, 10, fb)
	d.hold(true)
	d.keys("r")
	d.release(0) // the scan; the probe is held
	bodyHas(d, "Asking it what")
	d.keys("s")
	if d.m.note != "Wait: meshflash is still asking it what it runs." {
		t.Errorf("s while asking: %q", d.m.note)
	}
	d.releaseAll()
	if d.m.note != "" || hintOf(d) != "enter choose  s set up  u update  ? help" {
		t.Errorf("after the answer: note %q, hint %q", d.m.note, hintOf(d))
	}
}

// s on a node too old to set up says why in the backend's words, which follow the port's "Running …: " there.
func TestBoardSetUpNotSettableSaysWhy(t *testing.T) {
	n := repeaterNode(t, rakPort("ttyACM0"), nodecli.RoleRepeater, "v1.10.0", "RAK 4631", "")
	n.Problem, n.Reason = flash.NotSettable, "flash v1.11.0 or newer to set it up here."
	fb := screensBackend(t, rakPort("ttyACM0"))
	fb.nodes["ttyACM0"] = n
	d := newDriver(t, 45, 10, fb)
	if got := nodeSays(n); got != "Running repeater v1.10.0: flash v1.11.0 or newer to set it up here." {
		t.Errorf("the port says %q", got)
	}
	d.keys("s")
	if d.m.note != "Flash v1.11.0 or newer to set it up here." {
		t.Errorf("s on an old repeater: %q", d.m.note)
	}
}
