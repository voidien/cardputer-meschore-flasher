package ui

// Builder C's test helpers for the screens' goldens and key tests, and the tests of how the screens sit in the frame:
// the scans every second, the status line's battery and mark, resizing, late answers, nothing fetched unless asked,
// the board remembered. Everything runs over the fake backend on the driver's clock (driver_test.go).

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/store"
)

// screensBackend is the fake backend the screens' tests start from: the committed catalog, the owner in the dialout
// group, and the ports given.
func screensBackend(t testing.TB, ports ...flash.Port) *fakeBackend {
	fb := newFakeBackend()
	fb.cat = testCatalog(t)
	fb.scan = flash.Scan{Ports: ports, Dialout: true}
	return fb
}

// repeaterNode is a probe's answer from a MeshCore repeater or room server.
func repeaterNode(t testing.TB, p flash.Port, role, ver, board, name string) flash.Node {
	t.Helper()
	v, err := nodecli.ParseVersion(ver)
	if err != nil {
		t.Fatal(err)
	}
	n := flash.Node{Port: p, Problem: flash.NoProblem, Info: nodecli.Info{Version: v, Role: role, Board: board}}
	if name != "" {
		n.Current, n.HasCurrent = nodecli.Settings{Name: name}, true
	}
	return n
}

// flowFor is what has been picked on the way to a flash, up to the version, as the keys from Board pick it.
func flowFor(t testing.TB, p flash.Port, dev, role, ver string) flow {
	t.Helper()
	d := testCatalog(t).Find(dev)
	if d == nil {
		t.Fatalf("no board %q in the catalog", dev)
	}
	f := flow{port: p, dev: d}
	for i := range d.Firmware {
		if d.Firmware[i].Role == role && d.Firmware[i].Source == catalog.SourceMeshCore {
			f.fw = &d.Firmware[i]
			break
		}
	}
	if f.fw == nil {
		t.Fatalf("%s has no MeshCore %s", dev, role)
	}
	for i := range f.fw.Versions {
		if f.fw.Versions[i].Name == ver {
			f.ver = &f.fw.Versions[i]
		}
	}
	if f.ver == nil {
		t.Fatalf("%s %s has no %s", dev, role, ver)
	}
	return f
}

// testSum is the SHA-256 the fake's saved files have.
const testSum = "97539d20c4b1f00e8a6d2b3c5e7f9a1b2c3d4e5f60718293a4b5c6d7e8f90123"

// testSize is the size a fake saved file has: an update image or DFU package, or an ESP32's merged image.
func testSize(k catalog.Kind) int64 {
	if k == catalog.Wipe {
		return 1_300_000
	}
	return 361_000
}

// prepareAs answers Prepare as the backend does for a file saved or not: Choose's pick (a refusal is the Problem),
// its record when saved, and then whatever edit changes.
func prepareAs(saved bool, edit func(req flash.Request, p *flash.Prepared)) func(flash.Request) flash.Prepared {
	return func(req flash.Request) flash.Prepared {
		var p flash.Prepared
		fl, err := catalog.Choose(req.Device, req.Version, req.Kind)
		if err != nil {
			p.Problem = strings.TrimPrefix(err.Error(), catalog.ErrRefused.Error()+": ")
		} else {
			p.Flash = fl
			if saved {
				p.Saved = true
				p.Entry = catalog.Entry{Name: fl.File.Base(), URL: fl.File.URL, Size: testSize(req.Kind),
					SHA256: testSum, Saved: testNow.Add(-5 * time.Hour)}
			}
		}
		if edit != nil {
			edit(req, &p)
		}
		return p
	}
}

// confirmDriver is Confirm for the flow given, Prepare answered as prepare says, the board plugged in.
func confirmDriver(t *testing.T, w, h int, fb *fakeBackend, f flow, opts ...func(*Options)) *driver {
	t.Helper()
	if len(fb.scan.Ports) == 0 {
		fb.scan.Ports = []flash.Port{f.port}
	}
	d := newDriver(t, w, h, fb, opts...)
	d.m.flow = f
	d.run(d.m.openConfirm(""))
	return d
}

// moveTo presses j or k until the selection (as sel reads it) is want; it fails the test if it can't get there.
func moveTo(d *driver, sel func() string, want string) {
	d.t.Helper()
	for _, k := range []string{"k", "j"} {
		for i := 0; i < 200 && sel() != want; i++ {
			before := sel()
			d.keys(k)
			if sel() == before {
				break
			}
		}
	}
	if sel() != want {
		d.t.Fatalf("couldn't move the selection to %q (it is on %q)", want, sel())
	}
}

// chooseTo moves Choose board's cursor to the board named.
func chooseTo(d *driver, name string) { moveTo(d, func() string { return d.m.choose.sel }, name) }

// roleTo moves the Role screen's cursor to MeshCore's role (or the source's).
func roleTo(d *driver, source, role string) {
	moveTo(d, func() string {
		order := d.m.roleOrder()
		if len(order) == 0 {
			return ""
		}
		f := d.m.flow.dev.Firmware[order[min(d.m.role.sel, len(order)-1)]]
		return f.Source + "/" + f.Role
	}, source+"/"+role)
}

// versionTo moves the Version screen's cursor to the version named.
func versionTo(d *driver, name string) {
	moveTo(d, func() string {
		vs := d.m.versions()
		if len(vs) == 0 {
			return ""
		}
		return vs[min(d.m.ver.sel, len(vs)-1)].Name
	}, name)
}

// called counts the fake's calls starting with prefix.
func callCount(fb *fakeBackend, prefix string) int {
	n := 0
	for _, c := range fb.called() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// bodyHas fails the test unless the screen shows want.
func bodyHas(d *driver, want ...string) {
	d.t.Helper()
	v := d.view()
	for _, s := range want {
		if !strings.Contains(v, s) {
			d.t.Errorf("the screen lacks %q:\n%s", s, v)
		}
	}
}

// bodyLacks fails the test if the screen shows any of these.
func bodyLacks(d *driver, gone ...string) {
	d.t.Helper()
	v := d.view()
	for _, s := range gone {
		if strings.Contains(v, s) {
			d.t.Errorf("the screen still shows %q:\n%s", s, v)
		}
	}
}

// hintOf is the screen's last line, the key hints.
func hintOf(d *driver) string {
	ls := strings.Split(d.view(), "\n")
	return ls[len(ls)-1]
}

// titleOf is the status line.
func titleOf(d *driver) string { return strings.Split(d.view(), "\n")[0] }

// storeBuilt says builder A's store is in this tree (its placeholder refuses every write): the tests that read
// config.json's bytes need it; the rest of theirs runs either way.
func storeBuilt(t *testing.T) bool {
	return store.New(t.TempDir(), t.TempDir()).RememberBoard("probe", "x") == nil
}

// The frame scans every second on every screen, so the mark follows the board with no key (S13, S16); Scan is
// the only call it makes, and no screen but Board and Choose board probes.
func TestScanEverySecond(t *testing.T) {
	fb := screensBackend(t, xiaoPort("ttyACM0"))
	fb.prepare = prepareAs(true, nil)
	d := confirmDriver(t, 45, 10, fb, flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "repeater", "v1.18.0"))
	probes := callCount(fb, "Probe")
	for _, s := range []screen{scrConfirm, scrVersion, scrRole, scrNotes, scrResult, scrUpdate, scrFiles} {
		d.m.screen = s
		if s == scrNotes {
			d.m.notes.ver = d.m.flow.ver
		}
		scans := callCount(fb, "Scan")
		d.advance(time.Second)
		if got := callCount(fb, "Scan"); got != scans+1 {
			t.Errorf("screen %d: %d scans in a second, want 1", s, got-scans)
		}
	}
	if got := callCount(fb, "Probe"); got != probes {
		t.Errorf("%d probes off the Board and Choose board", got-probes)
	}
	var frame int
	for _, m := range d.timersSet() {
		if _, ok := m.(frameScanMsg); ok {
			frame++
		}
	}
	if frame != 1 {
		t.Errorf("%d scan timers set, want 1", frame)
	}
}

// The status line's battery on the LCD (ShowBattery): "87% ●", the line exactly w wide, a long title cut before it.
func TestStatusLineBattery(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		fb := screensBackend(t, heltecV4Port("ttyACM0"))
		fb.battery, fb.hasBattery = 87, true
		d := newDriver(t, w, h, fb, func(o *Options) { o.ShowBattery = true })
		line := titleOf(d)
		if !strings.HasSuffix(line, " 87% ●") || width(line) != w {
			t.Errorf("status line %q (%d cells)", line, width(line))
		}
		d.keys("enter")
		line = titleOf(d)
		if !strings.HasSuffix(line, " 87% ●") || width(line) != w || !strings.HasPrefix(line, "Which board") {
			t.Errorf("Choose board's status line %q (%d cells)", line, width(line))
		}
		f := flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4 R8 + Expansion Kit (Touch)", "companionBle", "v1.18.0")
		d.m.flow = flow{port: f.port, dev: f.dev, fw: f.fw}
		d.run(d.m.openVersion())
		if line = titleOf(d); !strings.Contains(line, "…") || !strings.HasSuffix(line, " 87% ●") || width(line) != w {
			t.Errorf("a long title isn't cut before the battery: %q", line)
		}
		fb.set(func(f *fakeBackend) { f.battery = -1 })
		d.advance(30 * time.Second)
		if line = titleOf(d); strings.Contains(line, "%") {
			t.Errorf("a battery that can't be read shows a charge: %q", line)
		}
	})
}

// Every screen fits the grid at every size down to 30×6, and back.
func TestResize(t *testing.T) {
	for _, st := range hintStates {
		t.Run(st.name, func(t *testing.T) {
			d := st.make(t, 45, 10)
			for _, s := range []struct{ w, h int }{{40, 10}, {30, 6}, {53, 14}, {45, 10}} {
				d.send(tea.WindowSizeMsg{Width: s.w, Height: s.h})
				ls := strings.Split(d.view(), "\n")
				if len(ls) != s.h {
					t.Errorf("%dx%d: %d lines", s.w, s.h, len(ls))
				}
				for i, l := range ls {
					if width(l) > s.w {
						t.Errorf("%dx%d: line %d is %d cells: %q", s.w, s.h, i+1, width(l), l)
					}
				}
			}
		})
	}
}

// Confirm's board unplugged with no key: the mark goes ○, the body says so, y does nothing; plugged back, it's back.
func TestStatusMarkFollowsTheBoard(t *testing.T) {
	fb := screensBackend(t, xiaoPort("ttyACM0"))
	fb.prepare = prepareAs(true, nil)
	d := confirmDriver(t, 45, 10, fb, flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "repeater", "v1.18.0"))
	if !strings.HasSuffix(titleOf(d), "●") {
		t.Fatalf("plugged in, the mark is %q", titleOf(d))
	}
	fb.set(func(f *fakeBackend) { f.scan.Ports = nil })
	d.advance(time.Second)
	if !strings.HasSuffix(titleOf(d), "○") {
		t.Errorf("unplugged, the mark is %q", titleOf(d))
	}
	bodyHas(d, "The board isn't plugged in now.")
	d.keys("y")
	if fb.calledAny("Flash") || fb.calledAny("Download") || d.m.screen != scrConfirm {
		t.Errorf("y with the board unplugged did something: %v", fb.called())
	}
	fb.set(func(f *fakeBackend) { f.scan.Ports = []flash.Port{xiaoPort("ttyACM1")} })
	d.advance(time.Second)
	if !strings.HasSuffix(titleOf(d), "●") || !strings.Contains(d.view(), "on ttyACM1") {
		t.Errorf("plugged back on ttyACM1:\n%s", d.view())
	}
	bodyLacks(d, "isn't plugged in now")
}

// Answers that come too late change nothing: a probe of a port gone, a Prepare superseded, a picture of an old Job.
func TestLateAnswers(t *testing.T) {
	t.Run("probe", func(t *testing.T) {
		fb := screensBackend(t, xiaoPort("ttyACM0"))
		fb.nodes["ttyACM0"] = repeaterNode(t, xiaoPort("ttyACM0"), nodecli.RoleRepeater, "v1.17.1", "", "Hilltop 2")
		d := newDriver(t, 45, 10, fb)
		d.hold(true)
		d.keys("r")
		d.release(0) // the scan: the probe is asked, and held
		fb.set(func(f *fakeBackend) { f.scan.Ports = nil })
		d.advance(time.Second)
		d.release(d.heldCmds() - 1) // the scan without the port
		d.releaseAll()              // the probe's answer, for a port gone
		bodyLacks(d, "Hilltop 2", "ttyACM0")
		if d.m.busy != 0 {
			t.Errorf("busy %d after the probe answered", d.m.busy)
		}
	})
	t.Run("prepare", func(t *testing.T) {
		fb := screensBackend(t, heltecV4Port("ttyACM0"))
		fb.prepare = prepareAs(true, nil)
		d := confirmDriver(t, 45, 10, fb, flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4", "repeater", "v1.18.0"))
		d.hold(true)
		d.keys("u")
		d.keys("w")
		d.releaseAll()
		if p := d.m.conf.current(); p == nil || p.req.Kind != catalog.Wipe {
			t.Fatalf("the Prepared kept isn't the latest: %+v", p)
		}
		d.send(preparedMsg{gen: d.m.conf.gen - 1, req: d.m.request(), p: flash.Prepared{Problem: "stale"}})
		bodyLacks(d, "stale")
	})
	t.Run("job", func(t *testing.T) {
		fb := screensBackend(t, xiaoPort("ttyACM0"))
		fb.prepare = prepareAs(true, nil)
		j := newJob(flash.JobFlash)
		fb.flashJob = func(flash.Request) *flash.Job { return j }
		d := confirmDriver(t, 45, 10, fb, flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "repeater", "v1.18.0"))
		d.keys("y")
		old := newJob(flash.JobFlash)
		old.Finish(flash.Outcome{Title: "Old job"})
		d.send(old.Snapshot())
		if d.m.screen != scrFlashing || d.m.job != j {
			t.Errorf("an old Job's picture moved the screen: %v", d.m.screen)
		}
	})
}

// Nothing is fetched unless the owner asked: UpdateCatalog only after u, Download only after d or y.
func TestNothingFetchedUnlessAsked(t *testing.T) {
	fb := screensBackend(t, heltecV4Port("ttyACM0"))
	fb.prepare = prepareAs(false, nil)
	d := newDriver(t, 45, 10, fb)
	d.keys("enter")
	chooseTo(d, "Heltec v4")
	d.keys("enter")
	roleTo(d, catalog.SourceMeshCore, "repeater")
	d.keys("enter", "n", "esc", "enter", "u")
	for i := 0; i < 5; i++ {
		d.advance(time.Second)
	}
	if fb.calledAny("UpdateCatalog") || fb.calledAny("Download") {
		t.Fatalf("fetched unasked: %v", fb.called())
	}
	d.keys("y")
	if callCount(fb, "Download") != 1 {
		t.Errorf("y on a file not saved didn't download it once: %v", fb.called())
	}
	d.settle()
	d.keys("esc", "esc", "esc", "esc")
	if d.m.screen != scrBoard {
		t.Fatalf("esc four times reached screen %d", d.m.screen)
	}
	d.keys("u")
	if callCount(fb, "UpdateCatalog") != 1 {
		t.Errorf("u didn't update once: %v", fb.called())
	}
}

// The board picked is remembered for its serial number (config.json and the model's copy), never for a board
// without a useful one, and the cursor starts on it next time.
func TestRememberBoard(t *testing.T) {
	fb := screensBackend(t, xiaoPort("ttyACM0"), cp2102Port("ttyUSB0"))
	d := newDriver(t, 45, 10, fb)
	d.keys("enter")
	chooseTo(d, "Ikoka Nano")
	d.keys("enter")
	if got := d.m.cfg.Boards[xiaoPort("ttyACM0").Key]; got != "Ikoka Nano" {
		t.Errorf("remembered %q", got)
	}
	if storeBuilt(t) {
		cfg, err := d.st.LoadConfig()
		if err != nil || cfg.Boards[xiaoPort("ttyACM0").Key] != "Ikoka Nano" {
			t.Errorf("config.json: %+v, %v", cfg, err)
		}
	} else {
		t.Log("the store is a placeholder here: config.json's bytes are checked once builder A's store is in")
	}
	d.keys("esc", "esc")
	d.keys("enter")
	if d.m.choose.sel != "Ikoka Nano" {
		t.Errorf("the cursor starts on %q, not the board remembered", d.m.choose.sel)
	}
	d.keys("esc", "j", "enter")
	chooseTo(d, "Heltec v3")
	d.keys("enter")
	if len(d.m.cfg.Boards) != 1 {
		t.Errorf("a board without a useful serial was remembered: %v", d.m.cfg.Boards)
	}
}

// listed follows the backend's rule: another board of the same model, plugged into the same socket and given the
// same tty, is not the board picked when their serial numbers differ (its ● would claim a board the backend says
// isn't plugged in); a bridge with no useful serial is followed by its tty and socket.
func TestListedNeedsTheSameSerial(t *testing.T) {
	a := xiaoPort("ttyACM0")
	b := a
	b.Serial, b.Key = "0123456789ABCDEF", "0123456789ABCDEF"
	d := newDriver(t, 45, 10, screensBackend(t, b))
	if d.m.listed(a) || !d.m.listed(b) {
		t.Errorf("listed(the board picked) %v, listed(the one plugged in) %v", d.m.listed(a), d.m.listed(b))
	}
	bridge := cp2102Port("ttyUSB0")
	d2 := newDriver(t, 45, 10, screensBackend(t, bridge))
	if !d2.m.listed(bridge) {
		t.Error("a bridge with no useful serial isn't followed by its tty and socket")
	}
}
