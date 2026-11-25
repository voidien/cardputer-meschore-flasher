package ui

// Help over each screen (S19; docs/phase2-design.md 8.6, 9.12): the screen's own keys, the frame's lock and Ctrl+C
// lines, the footer. The frame draws it (frame.go); these goldens show each screen's keys in it.

import (
	"strings"
	"testing"
	"time"

	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
)

// helpGolden opens Help over the screen make shows.
func helpGolden(t *testing.T, make func(t *testing.T, w, h int) *driver) {
	t.Helper()
	eachSize(t, func(t *testing.T, w, h int) {
		d := make(t, w, h)
		d.keys("?")
		if !d.m.help {
			t.Fatal("? didn't open Help")
		}
		d.check(w, h)
	})
}

func probedBoard(t *testing.T, w, h int) *driver {
	return boardAt(t, w, h, []flash.Port{xiaoPort("ttyACM0"), cp2102Port("ttyUSB0")}, map[string]flash.Node{
		"ttyACM0": repeaterNode(t, xiaoPort("ttyACM0"), nodecli.RoleRepeater, "v1.17.1", "", "Hilltop 2"),
	}, nil)
}

func TestHelpBoard(t *testing.T) { helpGolden(t, probedBoard) }

func TestHelpChoose(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver { return chooseAt(t, w, h, xiaoPort("ttyACM0"), nil, nil) })
}

func TestHelpRole(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver {
		return roleAt(t, w, h, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO")
	})
}

func TestHelpVersion(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver {
		d, _ := versionAt(t, w, h, xiaoFlow(t, true), nil)
		return d
	})
}

func TestHelpNotes(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver { return notesAt(t, w, h, notesOf(t, "v1.14.1")) })
}

func TestHelpConfirm(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver {
		fb := screensBackend(t)
		fb.prepare = prepareAs(true, nil)
		return confirmDriver(t, w, h, fb, heltecFlow(t, false))
	})
}

func TestHelpDownload(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver {
		d, _, _ := downloadAt(t, w, h)
		return d
	})
}

func TestHelpFlashing(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver {
		d, _, _ := flashingAt(t, w, h, xiaoFlow(t, false), pictureOf(flash.RiskErased, stepNow("Erasing", time.Second)))
		return d
	})
}

func TestHelpResult(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver {
		d, _ := resultAt(t, w, h, xiaoFlow(t, false), flash.Outcome{Title: "Flashing stopped", Retry: true,
			Summary: []string{"The board stopped answering while writing (212 of 361 KB)."},
			State:   "Its app is erased."})
		return d
	})
}

func TestHelpUpdate(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver {
		d, _ := updateAt(t, w, h, func(fb *fakeBackend) { fb.updateErr = errNetworkDown })
		return d
	})
}

func TestHelpFiles(t *testing.T) {
	helpGolden(t, func(t *testing.T, w, h int) *driver {
		d, _ := filesAt(t, w, h, savedFixture(t))
		return d
	})
}

// The footer, at the end of Help: the name and version, the servers, the folders, the lock, the dialout commands.
func TestHelpScrolledToTheEnd(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := probedBoard(t, w, h)
		d.keys("?")
		for i := 0; i < 30; i++ {
			d.keys(" ")
		}
		d.check(w, h)
		bodyHas(d, "sudo reboot")
	})
}

// Help names the program it is: the demo build's own name and folders (S2).
func TestHelpNamesTheProgram(t *testing.T) {
	d := newDriver(t, 45, 10, screensBackend(t), func(o *Options) { o.Name = "meshflash-demo" })
	if !strings.HasPrefix(titleOf(d), "meshflash-demo ") {
		t.Errorf("the Board's title: %q", titleOf(d))
	}
	keys, footer := d.m.helpLines()
	all := strings.Join(append(keys, footer...), "\n")
	for _, want := range []string{"meshflash-demo 0.1.0 for CardputerZero", "~/.config/meshflash-demo",
		"~/.cache/meshflash-demo",
		// Where it opens a port: the probes run on Choose board too, and a flash and a setup open one.
		"on the Board and Choose board screens, and to flash or set it up."} {
		if !strings.Contains(all, want) {
			t.Errorf("Help lacks %q:\n%s", want, all)
		}
	}
}

// j/k scroll two lines, Space and b a page, the ends hold, and any other key closes Help and does nothing else.
func TestHelpScrolls(t *testing.T) {
	d := probedBoard(t, 45, 10)
	d.keys("?")
	for i := 0; i < 40; i++ {
		d.keys(" ")
	}
	bodyHas(d, "sudo reboot")
	end := d.m.helpTop
	d.keys("k")
	if d.m.helpTop != end-scrollStep {
		t.Errorf("k at the end: %d, want %d", d.m.helpTop, end-scrollStep)
	}
	for i := 0; i < 40; i++ {
		d.keys("b")
	}
	if d.m.helpTop != 0 {
		t.Errorf("b to the top: %d", d.m.helpTop)
	}
	d.keys("j")
	if d.m.helpTop != scrollStep {
		t.Errorf("j: %d", d.m.helpTop)
	}
	d.keys("q")
	if d.m.help || d.m.screen != scrBoard || d.quit {
		t.Errorf("q: Help %v, screen %d, quit %v", d.m.help, d.m.screen, d.quit)
	}
	d.keys("?", "esc")
	if d.m.help || d.quit {
		t.Errorf("esc in Help: Help %v, quit %v (it should only close Help)", d.m.help, d.quit)
	}
}

// Every screen's Help lists keys in the frame's form: the key padded to 15 runes, then what it does.
func TestHelpKeysLineUp(t *testing.T) {
	for _, st := range hintStates {
		d := st.make(t, 45, 10)
		if d.m.screen == scrSetup {
			continue
		}
		for _, l := range d.m.screenHelp() {
			r := []rune(l)
			if len(r) <= 15 || r[14] != ' ' || r[15] == ' ' {
				t.Errorf("%s: Help line %q isn't a key padded to 15", st.name, l)
			}
		}
	}
}
