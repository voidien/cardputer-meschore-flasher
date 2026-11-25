package ui

// Builder D's end-to-end tests of Setup (docs/phase2-design.md 15): the real orchestration (flash.New) over builder
// B's fake world, through the real frame and builder C's Board, in virtual time. They skip while the world is still
// the contract's placeholder (it reports "not built yet"); the draft checks also skip while builder A's store is.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/nodecli"
)

// setupWorld is a new fake world, or skips the test while builder B's world is the contract's placeholder.
func setupWorld(t *testing.T) *fakeworld.World {
	t.Helper()
	w := fakeworld.New()
	if _, err := w.Deps(t.TempDir(), "test", false); err != nil {
		if strings.Contains(err.Error(), "not built yet") {
			t.Skip("builder B's fake world isn't built yet: " + err.Error())
		}
		t.Fatal(err)
	}
	return w
}

// setupHilltop is the owner's Heltec v4 running repeater v1.17.1, named “Hilltop 2”, plugged in.
func setupHilltop(w *fakeworld.World) *fakeworld.Board {
	b := fakeworld.HeltecV4(fakeworld.Firmware{Role: nodecli.RoleRepeater, Version: "v1.17.1", Name: "Hilltop 2"})
	w.Plug(b)
	return b
}

// setupFromBoard presses s on the Board, whose probe of the one port plugged in answered as a repeater: the Setup
// form opens once the node's settings are read.
func setupFromBoard(d *driver) {
	d.t.Helper()
	d.keys("s")
	if d.m.screen != scrSetup || d.m.setup.mode != setupForm {
		d.t.Fatalf("s on the Board didn't open the Setup form (screen %d, mode %d):\n%s", d.m.screen,
			d.m.setup.mode, d.view())
	}
}

// setupPreset picks preset i on the form's preset list.
func setupPreset(d *driver, i int) {
	d.t.Helper()
	d.keys("p")
	for d.m.setup.preset != i {
		if d.m.setup.preset < i {
			d.keys("j")
		} else {
			d.keys("k")
		}
	}
	d.keys("enter")
}

// setupWaitJob waits for a Job the test started itself to finish (guarded, never a sleep) and gives its Outcome.
func setupWaitJob(t *testing.T, j *flash.Job) flash.Outcome {
	t.Helper()
	select {
	case <-j.Done():
	case <-time.After(cmdGuard):
		j.Cancel()
		t.Fatalf("job %d didn't finish in %v", j.ID(), cmdGuard)
	}
	return j.Snapshot().Outcome
}

// Board s on a repeater → the form → a name, a preset, the admin password → y → every step ✓, the reboot → "Set
// up": the node keeps what was sent, config.json the radio, the draft is gone, and the password is nowhere.
func TestE2ESetupRepeater(t *testing.T) {
	world := setupWorld(t)
	board := setupHilltop(world)
	d := newWorldDriver(t, 45, 10, world, true)
	setupFromBoard(d)
	if v := d.m.setup.vals; v[setupName] != "Hilltop 2" || d.m.setup.node.Info.Version.String() != "v1.17.1" {
		t.Fatalf("the form opened with name %q, version %s", v[setupName], d.m.setup.node.Info.Version)
	}
	setupType(d, setupName, "Hilltop East")
	setupPreset(d, 1) // Australia (Narrow): 916.575 MHz, 62.5 kHz, SF7, CR7, a 2-byte path hash
	setupType(d, setupAdmin, setupTestAdmin)
	d.advance(time.Second)
	d.do("y")
	if d.m.setup.mode != setupDone || d.m.setup.outcome.Title != "Set up" {
		t.Fatalf("the setup ended %q (mode %d):\n%s\n%s", d.m.setup.outcome.Title, d.m.setup.mode, d.view(),
			d.logText())
	}
	rebooted := false
	for _, s := range d.m.setup.picture.Steps {
		if s.State != flash.StepDone && s.State != flash.StepSkipped {
			t.Errorf("step %q ended %d", s.Text, s.State)
		}
		rebooted = rebooted || strings.Contains(strings.ToLower(s.Text), "reboot")
	}
	if !rebooted {
		t.Error("no reboot step")
	}
	n := board.Node()
	if n == nil {
		t.Fatal("the board runs no console after the setup")
	}
	want := nodecli.Radio{FreqMHz: 916.575, BWkHz: 62.5, SF: 7, CR: 7}
	if n.Name != "Hilltop East" || !n.Radio.Near(want) || !n.ActiveRadio.Near(want) || n.PathHashMode != 1 ||
		n.Reboots() == 0 {
		t.Errorf("the node has name %q, radio %s (running %s), path hash mode %d, %d reboots", n.Name,
			n.Radio.Display(), n.ActiveRadio.Display(), n.PathHashMode, n.Reboots())
	}
	if n.AdminPassword != setupTestAdmin {
		t.Error("the node doesn't have the admin password typed")
	}
	if r := d.m.cfg.Radio; r == nil || r.FreqMHz != 916.575 || r.SF != 7 || r.CR != 7 || r.PathHashBytes != 2 {
		t.Errorf("the radio remembered: %v", r)
	}
	if setupStoreBuilt(d) {
		cfg, err := d.st.LoadConfig()
		if err != nil || cfg.Radio == nil || cfg.Radio.FreqMHz != 916.575 {
			t.Errorf("config.json's radio: %v (%v)", cfg.Radio, err)
		}
		if _, ok := d.st.LoadDraft(d.m.setup.port.DraftKey()); ok {
			t.Error("the draft stayed after the setup worked")
		}
	}
	d.noSecret(setupTestAdmin)
}

// Result's s right after a flash (firstBoot): the form reads the node that just came back, and its setup works.
// The flash goes through the backend as Confirm's y would, and Setup opens as Result's s opens it (openSetup with
// firstBoot), so the test needs no other screen's keys.
func TestE2ESetupAfterFlash(t *testing.T) {
	world := setupWorld(t)
	board := fakeworld.RAK4631(fakeworld.Firmware{Role: "companion", Version: "v1.17.1"})
	world.Plug(board)
	d := newWorldDriver(t, 45, 10, world, true)
	be := d.m.backend
	cat, err := be.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	dev := cat.Find("RAK WisBlock / WisMesh (RAK 4631)")
	if dev == nil {
		t.Fatal("no RAK 4631 in the catalog")
	}
	var fw *catalog.Firmware
	for i := range dev.Firmware {
		if f := &dev.Firmware[i]; f.Source == catalog.SourceMeshCore && f.Role == "repeater" {
			fw = f
		}
	}
	if fw == nil {
		t.Fatal("no MeshCore repeater for the RAK 4631")
	}
	ver := fw.Versions[0]
	var files []catalog.File
	for _, f := range ver.Files {
		if f.Type == "flash" {
			files = append(files, f)
		}
	}
	if o := setupWaitJob(t, be.Download(context.Background(), files)); len(o.Entries) == 0 {
		t.Fatalf("the download: %q %q", o.Title, o.Summary)
	}
	scan, err := be.Scan()
	if err != nil || len(scan.Ports) != 1 {
		t.Fatalf("the scan: %v (%v)", scan.Ports, err)
	}
	o := setupWaitJob(t, be.Flash(context.Background(), flash.Request{Port: scan.Ports[0], Device: *dev,
		Firmware: *fw, Version: ver, Kind: catalog.Update}))
	if !o.Setup || o.Board == nil {
		t.Fatalf("the flash didn't offer a setup: %q %q %q", o.Title, o.Summary, o.State)
	}
	d.run(d.m.openSetup(*o.Board, dev.Name, true))
	if d.m.setup.mode != setupForm || d.m.setup.node.Info.Version.String() != ver.Name {
		t.Fatalf("after the flash the form opened in mode %d on %s:\n%s", d.m.setup.mode,
			d.m.setup.node.Info.Version, d.view())
	}
	setupType(d, setupAdmin, setupTestAdmin)
	d.advance(time.Second)
	d.do("y")
	if d.m.setup.mode != setupDone {
		t.Fatalf("the setup after the flash ended %q:\n%s", d.m.setup.outcome.Title, d.view())
	}
	if n := board.Node(); n == nil || n.AdminPassword != setupTestAdmin {
		t.Error("the flashed node didn't take the admin password")
	}
	d.noSecret(setupTestAdmin)
}

// The board unplugged after three of the setup's commands: the StepError's State says what the node is left with,
// and the screen shows it, the board's mark ○.
func TestE2ESetupUnplugged(t *testing.T) {
	world := setupWorld(t)
	board := setupHilltop(world)
	d := newWorldDriver(t, 45, 10, world, true)
	setupFromBoard(d)
	setupPreset(d, 1)
	setupType(d, setupAdmin, setupTestAdmin)
	board.UnplugAfterCommands(3)
	d.advance(time.Second)
	d.do("y")
	o := d.m.setup.outcome
	if d.m.setup.mode != setupFailed || o.State == "" || !o.Retry {
		t.Fatalf("unplugged mid-setup: mode %d, %+v", d.m.setup.mode, o)
	}
	// The backend is Outcome.Log's one writer: the line is in screen.log once, Setup's end adding no copy.
	if n := strings.Count(d.logText(), o.Log); o.Log == "" || n != 1 {
		t.Errorf("screen.log has the Outcome's Log %q %d times:\n%s", o.Log, n, d.logText())
	}
	for _, l := range lines(d.view()) {
		if width(l) > 45 {
			t.Errorf("a line is too wide: %q", l)
		}
	}
	shown := ""
	for top := 0; top < 20; top++ { // what scrolls, and what is pinned
		shown += " " + setupScreenText(d)
		d.keys("j")
	}
	if want := strings.Join(strings.Fields(o.State), " "); !strings.Contains(shown, want) {
		t.Errorf("the State %q isn't on the screen:\n%s", o.State, d.view())
	}
	if first := lines(d.view())[0]; !strings.HasSuffix(first, "○") {
		t.Errorf("the mark doesn't say the board is gone: %q", first)
	}
	d.noSecret(setupTestAdmin)
}

// What was half-typed survives Ctrl+C, and a text box cancelled by an Esc hold's first tap survives the SIGTERM's
// Shutdown: each comes back when Setup reopens on the same board.
func TestE2ESetupDraftThroughQuit(t *testing.T) {
	for _, how := range []string{"ctrl+c in a text box", "esc, then Shutdown"} {
		t.Run(how, func(t *testing.T) {
			world := setupWorld(t)
			setupHilltop(world)
			d := newWorldDriver(t, 45, 10, world, true)
			setupNeedsStore(t, d)
			setupFromBoard(d)
			setupType(d, setupFreq, "869.525")
			setupFocus(d, setupName)
			d.keys("enter", "ctrl+u")
			d.typeText("Hilltop Ea")
			if how == "ctrl+c in a text box" {
				d.keys("ctrl+c")
				if !d.quit {
					t.Fatal("Ctrl+C didn't quit")
				}
			} else {
				d.keys("esc")
				d.m.Shutdown()
			}
			again := newWorldDriver(t, 45, 10, world, true, func(o *Options) { o.Store = d.st })
			setupFromBoard(again)
			if v := again.m.setup.vals; v[setupName] != "Hilltop Ea" || v[setupFreq] != "869.525" ||
				again.m.note != "Restored what was typed for this board." {
				t.Errorf("reopened: name %q, frequency %q, note %q", v[setupName], v[setupFreq], again.m.note)
			}
		})
	}
}

// Ctrl+C after set radio was answered (owner question 16): meshflash quits with no question, the setup stops
// between two commands, the node keeps its new radio unrebooted, screen.log has the frame's line and then, as
// internal/app writes it after Run, the Outcome; config.json has the radio once that shutdown has run; no password
// anywhere.
func TestE2ESetupCtrlCQuits(t *testing.T) {
	world := setupWorld(t)
	board := setupHilltop(world)
	d := newWorldDriver(t, 45, 10, world, true)
	setupFromBoard(d)
	old := board.Node().ActiveRadio
	setupPreset(d, 1) // Australia (Narrow), not the radio the node runs
	setupType(d, setupAdmin, setupTestAdmin)

	// The setup is held just after the node has answered set radio, in the backend's gap before its next command,
	// where Ctrl+C meets it: an event of the fake world, in virtual time (cmdGuard only guards against a deadlock).
	paused, resume := board.PauseAfter("set radio ")
	defer resume()
	d.advance(time.Second)
	d.keys("y")
	select {
	case <-paused:
	case <-time.After(cmdGuard):
		t.Fatal("the setup never reached set radio")
	}
	handled := len(board.Node().Commands())
	d.keys("ctrl+c")
	if !d.quit || d.m.confirm != noConfirm {
		t.Fatalf("Ctrl+C while setting up asked (%d) instead of quitting", d.m.confirm)
	}
	// internal/app after Run: Shutdown, then the Job's end, its Outcome in screen.log and its radio saved.
	j := d.m.Shutdown()
	resume()
	if j == nil {
		t.Fatal("Shutdown gave no Job to wait for")
	}
	o := setupWaitJob(t, j)
	fmt.Fprintf(d.log, "%s\n%s\n%s\n%s\n", o.Title, strings.Join(o.Summary, " "), o.State, o.Recovery)
	if o.Sent != nil && setupStoreBuilt(d) {
		if err := d.st.SaveRadio(SavedRadio(*o.Sent)); err != nil {
			t.Fatal(err)
		}
	}

	if !o.Stopped || o.Title != "Setup stopped" || o.State == "" || o.Recovery == "" || len(o.Summary) == 0 {
		t.Errorf("the stopped setup's Outcome: %+v", o)
	}
	if o.Sent == nil {
		t.Fatal("the Outcome doesn't say the node took the radio")
	}
	log := d.logText()
	for _, want := range []string{"meshflash: quit while setting up (step ",
		"Its new radio is saved but it hasn't restarted", o.State} {
		if !strings.Contains(log, want) {
			t.Errorf("screen.log lacks %q:\n%s", want, log)
		}
	}
	n := board.Node()
	if n == nil {
		t.Fatal("the board runs no console after the stopped setup")
	}
	// Stopped between two commands: nothing reached the node after the Ctrl+C (not set tx, the next one).
	if cmds := n.Commands(); len(cmds) != handled {
		t.Errorf("%d commands reached the node after Ctrl+C", len(cmds)-handled)
	}
	if o.Sent.TxDBm != n.TxPower {
		t.Errorf("TX power recorded %d, the node has %d", o.Sent.TxDBm, n.TxPower)
	}
	want := nodecli.Radio{FreqMHz: 916.575, BWkHz: 62.5, SF: 7, CR: 7}
	if !n.Radio.Near(want) || !n.ActiveRadio.Near(old) || n.Reboots() != 0 {
		t.Errorf("the node: radio %s, running %s, %d reboots", n.Radio.Display(), n.ActiveRadio.Display(),
			n.Reboots())
	}
	if setupStoreBuilt(d) {
		cfg, err := d.st.LoadConfig()
		if err != nil || cfg.Radio == nil || cfg.Radio.FreqMHz != 916.575 {
			t.Errorf("config.json's radio after the shutdown: %v (%v)", cfg.Radio, err)
		}
	}
	d.noSecret(setupTestAdmin)
}
