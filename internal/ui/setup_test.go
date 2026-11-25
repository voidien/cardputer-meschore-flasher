package ui

// Builder D's tests of the Setup screen (docs/phase2-design.md 10.7, 15): goldens of every state at 45×10, 40×10
// and 53×14 over the fake backend, the drafts in a real store, the defaults, sending once, and the passwords never
// shown, kept or logged. Setup is opened the way builder C's Board and Result screens open it, through openSetup.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/store"
)

// The passwords the tests type and the fake room server holds. TestNoGoldenHoldsThePassword looks for each in every
// golden; tests compare them, never print them.
const (
	setupTestAdmin = "Tr0ub4dor&3"
	setupTestGuest = "gu3st-Pa55"
)

// setupTestPasswords are every password a test types or a fake node holds: the fake world's room server keeps
// nodecli.NewFakeNode's guest password, "hello".
var setupTestPasswords = []string{setupTestAdmin, setupTestGuest, "hello"}

// setupNode is what ReadNode gives for a node of role on version: “Hilltop 2” (a room server “Hilltop Room”, its
// guest password setupTestGuest) on EU/UK (Narrow), 22 dBm, a 1-byte path hash (v1.14.0 and newer), in London,
// local advert off, saying its board is a "Heltec V4".
func setupNode(p flash.Port, role, version string) flash.Node {
	v, err := nodecli.ParseVersion(version)
	if err != nil {
		panic(err)
	}
	lat, lon, advert := 51.5074, -0.1278, 0
	n := flash.Node{Port: p, Problem: flash.NoProblem, HasCurrent: true,
		Info: nodecli.Info{Version: v, Role: role, Board: "Heltec V4"},
		Current: nodecli.Settings{Name: "Hilltop 2", Radio: nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8},
			TxDBm: 22, PathHashBytes: 1, Lat: &lat, Lon: &lon, AdvertInterval: &advert}}
	if role == nodecli.RoleRoomServer {
		n.Current.Name, n.Current.GuestPassword = "Hilltop Room", nodecli.Secret(setupTestGuest)
	}
	if !v.AtLeast(1, 14, 0) {
		n.Current.PathHashBytes = 0
	}
	return n
}

// setupBackend is the fake backend with p plugged in and ReadNode answering n, the Zero's clock synchronised and no
// messenger file.
func setupBackend(p flash.Port, n flash.Node) *fakeBackend {
	fb := newFakeBackend()
	fb.synced = true
	fb.scan = flash.Scan{Ports: []flash.Port{p}, Dialout: true}
	fb.readNodes[p.Name] = n
	return fb
}

// setupOpen opens Setup on p as the Board's s does (a catalog board "Heltec v4" remembered for it).
func setupOpen(d *driver, p flash.Port) {
	d.t.Helper()
	d.run(d.m.openSetup(p, "Heltec v4", false))
}

// setupDriver is the form for a repeater v1.18.0 on a Heltec v4 at w×h.
func setupDriver(t *testing.T, w, h int, opts ...func(*Options)) (*driver, *fakeBackend) {
	t.Helper()
	p := heltecV4Port("ttyACM0")
	fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0-81dbae5 (Build: 15-Oct-2026)"))
	d := newDriver(t, w, h, fb, opts...)
	setupOpen(d, p)
	if d.m.setup.mode != setupForm {
		t.Fatalf("the form didn't open: mode %d", d.m.setup.mode)
	}
	return d, fb
}

// setupFocus moves the form's focus to row r with j and k, as the owner would.
func setupFocus(d *driver, r setupRow) {
	d.t.Helper()
	for i := 0; d.m.setup.focus != r; i++ {
		if i > int(setupRowCount) {
			d.t.Fatalf("row %d isn't on the form", r)
		}
		if d.m.setup.focus < r {
			d.keys("j")
		} else {
			d.keys("k")
		}
	}
}

// setupType replaces row r's text: Enter opens its box, Ctrl+U empties it, the text is typed and Enter keeps it.
func setupType(d *driver, r setupRow, text string) {
	d.t.Helper()
	setupFocus(d, r)
	d.keys("enter", "ctrl+u")
	d.typeText(text)
	d.keys("enter")
}

// setupNeedsStore skips a test that reads back drafts.json or config.json while builder A's store is still the
// contract's placeholder, whose writes report "not built yet".
func setupNeedsStore(t *testing.T, d *driver) {
	t.Helper()
	if !setupStoreBuilt(d) {
		t.Skip("internal/store is still the Phase 2 placeholder (builder A's): drafts and config.json aren't saved")
	}
}

func setupStoreBuilt(d *driver) bool {
	err := d.st.DeleteDraft("meshflash-test-probe")
	return err == nil || !strings.Contains(err.Error(), "not built yet")
}

// setupTestSteps are a repeater setup's steps as the backend names them (nodecli.Step.Display, then the reboot
// and the check).
var setupTestSteps = []string{
	"Clock: set to the Zero's time", "Name: Hilltop 2", "Radio: 869.618 MHz, 62.5 kHz, SF8, CR8",
	"TX power: 22 dBm", "Path hash: 1 bytes", "Latitude: 51.5074", "Longitude: -0.1278", "Local advert: off",
	"Admin password: ••••", "Check name", "Check radio", "Check TX power", "Check path hash", "Check latitude",
	"Check longitude", "Check local advert", "Reboot to start the new radio", "Back on USB (ttyACM0)",
	"Check: v1.18.0 on 869.618 MHz",
}

// setupAt is a setup's picture with the steps before n done and step n under way for 2 s.
func setupAt(n int, risk flash.Risk) func(p *flash.Progress) {
	return func(p *flash.Progress) {
		p.Now, p.Planned, p.Risk = testNow, len(setupTestSteps), risk
		p.Steps = nil
		for i := 0; i <= n && i < len(setupTestSteps); i++ {
			st := flash.Step{Text: setupTestSteps[i], State: flash.StepDone}
			if i == n {
				st.State, st.Started = flash.StepNow, testNow.Add(-2*time.Second)
			}
			p.Steps = append(p.Steps, st)
		}
	}
}

// setupSendingDriver is the form with the admin password typed and y pressed: the setup Job the test moves on itself, and
// its context, which Job.Cancel cancels.
func setupSendingDriver(t *testing.T, w, h int) (*driver, *fakeBackend, *flash.Job, context.Context) {
	t.Helper()
	d, fb := setupDriver(t, w, h)
	ctx, cancel := context.WithCancel(context.Background())
	j := flash.NewJob(flash.JobSetup, nil, cancel)
	fb.set(func(f *fakeBackend) { f.setup = func(flash.SetupRequest) *flash.Job { return j } })
	setupType(d, setupAdmin, setupTestAdmin)
	d.keys("y")
	if d.m.setup.mode != setupSending {
		t.Fatalf("y didn't send: mode %d, note %q", d.m.setup.mode, d.m.note)
	}
	return d, fb, j, ctx
}

// setupCalls counts the backend's Setup calls.
func setupCalls(fb *fakeBackend) int {
	n := 0
	for _, c := range fb.called() {
		if c == "Setup" {
			n++
		}
	}
	return n
}

// setupScreenText is the screen as one line of words, for text that wraps differently at each size.
func setupScreenText(d *driver) string { return strings.Join(strings.Fields(d.view()), " ") }

// setupOK is a setup that worked, sending EU/UK (Narrow) at 22 dBm with a 1-byte path hash.
func setupOK(p flash.Port) flash.Outcome {
	return flash.Outcome{OK: true, Title: "Set up", Board: &p,
		Summary: []string{"Name, radio, TX power, path hash, location, local advert and admin password sent and " +
			"read back.", "It restarted and runs v1.18.0 on 869.618 MHz."},
		Sent: &flash.Sent{Radio: nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8}, TxDBm: 22,
			PathHashBytes: 1}}
}

// --- Goldens ---

func TestSetupReading(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		d := newDriver(t, w, h, setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0")))
		d.hold(true)
		setupOpen(d, p)
		d.advance(2 * time.Second)
		d.check(w, h)
		d.releaseAll()
	})
}

func TestSetupReadFailed(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		d := newDriver(t, w, h, setupBackend(p, flash.Node{Port: p, Problem: flash.Silent}))
		setupOpen(d, p)
		d.check(w, h)
	})
}

// A ZephCore build answers like MeshCore's console, but setup sets up only MeshCore's own firmware.
func TestSetupNotARepeater(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		n := setupNode(p, nodecli.RoleRepeater, "v1.18.2-zephcore")
		n.Problem = flash.NotSettable
		n.Reason = "ZephCore: meshflash sets up only MeshCore's own repeater and room-server firmware."
		d := newDriver(t, w, h, setupBackend(p, n))
		setupOpen(d, p)
		d.check(w, h)
	})
}

func TestSetupForm(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := setupDriver(t, w, h)
		d.check(w, h)
	})
}

func TestSetupFormRoomServer(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		d := newDriver(t, w, h, setupBackend(p, setupNode(p, nodecli.RoleRoomServer, "v1.18.0")))
		setupOpen(d, p)
		setupFocus(d, setupGuest)
		d.check(w, h)
		d.noSecret(setupTestGuest)
	})
}

// A name the node can't take, a frequency below the radio's, and the admin password not typed yet: each problem
// under its row.
func TestSetupFormProblems(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, fb := setupDriver(t, w, h)
		setupType(d, setupName, "Hill/top")
		setupType(d, setupFreq, "100")
		setupFocus(d, setupName)
		d.check(w, h)
		d.keys("y")
		if setupCalls(fb) != 0 {
			t.Error("y sent a form with problems")
		}
	})
}

func TestSetupFormScrolled(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := setupDriver(t, w, h)
		setupFocus(d, setupAdmin)
		d.check(w, h)
	})
}

func TestSetupEditName(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := setupDriver(t, w, h)
		d.keys("enter")
		d.typeText(" East")
		d.check(w, h)
	})
}

func TestSetupEditPassword(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := setupDriver(t, w, h)
		setupFocus(d, setupAdmin)
		d.keys("enter")
		d.typeText(setupTestAdmin)
		d.check(w, h)
		d.noSecret(setupTestAdmin)
	})
}

func TestSetupPickBandwidth(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := setupDriver(t, w, h)
		setupFocus(d, setupBW)
		d.keys("enter", "j")
		d.check(w, h)
	})
}

func TestSetupPresets(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := setupDriver(t, w, h)
		d.keys("p")
		d.check(w, h)
	})
}

func TestSetupMessengerNote(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0"))
		fb.messengerNote = "Your messenger's radio settings weren't used: /home/void/.config/mesh/config.json " +
			"isn't valid JSON, or its radio values aren't numbers."
		d := newDriver(t, w, h, fb)
		setupOpen(d, p)
		d.check(w, h)
	})
}

func TestSetupClockNotSynced(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0"))
		fb.synced = false
		d := newDriver(t, w, h, fb)
		setupOpen(d, p)
		d.check(w, h)
	})
}

// v1.13.0 has no path hash setting: the row says so, and the setup leaves it out.
func TestSetupOldFirmware(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.13.0-295f67d (Build: 15-Feb-2026)"))
		d := newDriver(t, w, h, fb)
		setupOpen(d, p)
		setupFocus(d, setupPathHash)
		d.check(w, h)
		d.keys("enter")
		if d.m.setup.mode != setupForm || !strings.Contains(d.m.note, "v1.14.0") {
			t.Errorf("Enter on the path hash of v1.13.0: mode %d, note %q", d.m.setup.mode, d.m.note)
		}
		if set, _ := d.m.setupSettings(); set.PathHashBytes != 0 {
			t.Errorf("v1.13.0 would be sent a path hash of %d", set.PathHashBytes)
		}
	})
}

func TestSetupDraftRestored(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		d := newDriver(t, w, h, setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0")))
		setupNeedsStore(t, d)
		if err := d.st.SaveDraft(p.DraftKey(), store.Draft{Role: nodecli.RoleRepeater, Name: "Hilltop East",
			Freq: "869.52", Cleared: []string{"lat"}}); err != nil {
			t.Fatal(err)
		}
		setupOpen(d, p)
		d.check(w, h)
	})
}

func TestSetupDraftRoleChanged(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		p := heltecV4Port("ttyACM0")
		d := newDriver(t, w, h, setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0")))
		setupNeedsStore(t, d)
		if err := d.st.SaveDraft(p.DraftKey(), store.Draft{Role: nodecli.RoleRoomServer, Name: "Hilltop Room",
			TX: "20"}); err != nil {
			t.Fatal(err)
		}
		setupOpen(d, p)
		d.check(w, h)
	})
}

func TestSetupSending(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j, _ := setupSendingDriver(t, w, h)
		j.Set(setupAt(4, flash.RiskRadioSent))
		d.settle()
		d.check(w, h)
	})
}

// Late in a setup of nineteen steps, at 40×10, the oldest done steps fold into one line and the one under way stays
// in view.
func TestSetupSendingScrolled(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j, _ := setupSendingDriver(t, w, h)
		j.Set(setupAt(13, flash.RiskRadioSent))
		d.settle()
		d.check(w, h)
	})
}

func TestSetupStopAsks(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j, _ := setupSendingDriver(t, w, h)
		j.Set(setupAt(4, flash.RiskRadioSent))
		d.settle()
		d.keys("esc")
		d.check(w, h)
	})
}

func TestSetupDone(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j, _ := setupSendingDriver(t, w, h)
		j.Finish(setupOK(heltecV4Port("ttyACM0")))
		d.settle()
		d.check(w, h)
	})
}

// A read-back that found something else: the StepError's words, and what the node is left with.
func TestSetupFailed(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j, _ := setupSendingDriver(t, w, h)
		p := heltecV4Port("ttyACM0")
		j.Finish(flash.Outcome{Title: "Setup stopped", Board: &p, Retry: true,
			Summary: []string{"Check name: the node has \"Hill\" stored, not \"Hilltop 2\"."},
			State:   "Every setting was sent to the node; run setup again to check them. It keeps its old radio until it restarts.",
			Sent:    &flash.Sent{Radio: nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8}, TxDBm: 22}})
		d.settle()
		d.check(w, h)
	})
}

// A long account of a setup cut short after its reboot: the Summary scrolls, what the node is left with stays at the
// bottom.
func TestSetupFailedLate(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _, j, _ := setupSendingDriver(t, w, h)
		j.Finish(flash.Outcome{Title: "Setup stopped", Retry: true,
			Summary: []string{
				"Name, radio, TX power, path hash, location, local advert and admin password sent and read back.",
				"Reboot to start the new radio: sent.",
				"The board didn't come back on USB within 15 s.",
			},
			State:    "Every setting was sent, saved and checked; the node may not have restarted.",
			Recovery: "Press its reset button once, then press s on the Board to check it.",
			Sent:     &flash.Sent{Radio: nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8}, TxDBm: 22}})
		d.settle()
		d.keys("j")
		d.check(w, h)
	})
}

func TestHelpSetup(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d, _ := setupDriver(t, w, h)
		d.keys("?")
		d.check(w, h)
	})
}

// --- The rules ---

// S16: the answer replaces "Reading…" with no key pressed, and the board mark stops saying it's busy.
func TestSetupReadingGoes(t *testing.T) {
	p := heltecV4Port("ttyACM0")
	d := newDriver(t, 45, 10, setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0")))
	d.hold(true)
	setupOpen(d, p)
	if !strings.Contains(d.view(), "Reading the node's settings") || d.m.busy != 1 {
		t.Fatalf("not reading (busy %d):\n%s", d.m.busy, d.view())
	}
	d.releaseAll()
	if v := d.view(); strings.Contains(v, "Reading") || !strings.Contains(v, "Hilltop 2") || d.m.busy != 0 {
		t.Errorf("the answer didn't replace Reading (busy %d):\n%s", d.m.busy, v)
	}
	// Right after a flash (Result's s) the node may take a while to start: the screen says it is waiting for that.
	d.hold(true)
	d.run(d.m.openSetup(p, "Heltec v4", true))
	if !strings.Contains(setupScreenText(d), "Waiting for it to start, then reading its settings… (0:00)") {
		t.Errorf("first boot:\n%s", d.view())
	}
	d.releaseAll()
}

// Esc while reading goes back to the Board; the answer, when it comes, is dropped and the busy mark let go.
func TestSetupEscWhileReading(t *testing.T) {
	p := heltecV4Port("ttyACM0")
	d := newDriver(t, 45, 10, setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0")))
	d.hold(true)
	setupOpen(d, p)
	d.keys("esc")
	if d.m.screen != scrBoard {
		t.Fatalf("Esc while reading left the screen on %d", d.m.screen)
	}
	d.releaseAll()
	if d.m.screen != scrBoard || d.m.setup.mode != setupClosed || d.m.busy != 0 {
		t.Errorf("a late answer moved the screens: screen %d, mode %d, busy %d", d.m.screen, d.m.setup.mode, d.m.busy)
	}
}

// y sends the form as typed, once, with the password; then the password is gone from the form.
func TestSetupSendsWhatWasTyped(t *testing.T) {
	d, fb, _, _ := setupSendingDriver(t, 45, 10)
	if n := len(fb.setups); n != 1 {
		t.Fatalf("%d setups sent", n)
	}
	req := fb.setups[0]
	s := req.Settings
	if s.Name != "Hilltop 2" || !s.Radio.Near(nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8}) ||
		s.TxDBm != 22 || s.PathHashBytes != 1 || s.Lat == nil || *s.Lat != 51.5074 || s.Lon == nil ||
		*s.Lon != -0.1278 || s.AdvertInterval == nil || *s.AdvertInterval != 0 || !s.ClockSynced ||
		s.Device != "Heltec v4" || s.GuestPassword != "" {
		t.Errorf("the settings sent: %v", s) // Settings prints its passwords as ••••
	}
	if string(s.AdminPassword) != setupTestAdmin {
		t.Error("the admin password sent isn't the one typed")
	}
	if req.Node.Info.Role != nodecli.RoleRepeater || req.Node.Port.Name != "ttyACM0" {
		t.Errorf("the node sent: %v", req.Node.Info)
	}
	if len(d.m.setup.admin.text) != 0 || len(d.m.setup.box.text) != 0 {
		t.Error("the form still holds the admin password after sending")
	}
	d.noSecret(setupTestAdmin)
}

// y with a problem sends nothing: the note names the rows, and the first is focused.
func TestSetupProblemsRefuseSend(t *testing.T) {
	d, fb := setupDriver(t, 45, 10)
	d.keys("y")
	if setupCalls(fb) != 0 || d.m.setup.focus != setupAdmin || !strings.Contains(d.m.note, "Admin password") {
		t.Errorf("y with no admin password: %d calls, focus %d, note %q", setupCalls(fb), d.m.setup.focus, d.m.note)
	}
}

// S20: Esc while sending asks; any key but y says no and does nothing else; y stops it once.
func TestSetupStopNeedsYes(t *testing.T) {
	d, _, j, ctx := setupSendingDriver(t, 45, 10)
	j.Set(setupAt(4, flash.RiskSettings))
	d.settle()
	for _, k := range []string{"n", "?", "esc"} {
		d.keys("esc")
		if d.m.confirm != confirmStopSetup {
			t.Fatalf("Esc while sending didn't ask: %d", d.m.confirm)
		}
		if !strings.Contains(setupScreenText(d), "The node keeps what it was sent so far.") {
			t.Errorf("the question doesn't say what the node keeps:\n%s", d.view())
		}
		d.keys(k)
		if ctx.Err() != nil || d.m.help || d.m.confirm != noConfirm {
			t.Fatalf("%q to the question: stopped %v, help %v, confirm %d", k, ctx.Err() != nil, d.m.help,
				d.m.confirm)
		}
	}
	d.advance(time.Second) // a deliberate y, not the one that sent it held down
	d.keys("esc", "y")
	if ctx.Err() == nil || !d.m.setup.stopping {
		t.Fatal("y didn't stop the setup")
	}
	if d.keys("esc"); d.m.confirm != noConfirm {
		t.Error("Esc asked again while stopping")
	}
	j.Finish(flash.Outcome{Title: "Setup stopped", Stopped: true, Retry: true, State: "The node keeps the " +
		"settings it took so far, still on its old radio."})
	d.settle()
	if d.m.setup.mode != setupFailed || !strings.Contains(setupScreenText(d), "still on its old radio") {
		t.Errorf("the stopped setup's end:\n%s", d.view())
	}
}

// The question says what stopping leaves, by the picture's Risk.
func TestSetupStopWords(t *testing.T) {
	for risk, want := range map[flash.Risk]string{
		flash.RiskNone:      "Stop now? Nothing has been sent yet.",
		flash.RiskSettings:  "Stop now? The node keeps what it was sent so far.",
		flash.RiskRadioSent: "Stop now? The node keeps what it was sent so far, and switches to the new radio when it restarts.",
	} {
		d, _, j, _ := setupSendingDriver(t, 45, 10)
		j.Set(setupAt(1, risk))
		d.settle()
		d.keys("esc")
		if got := d.m.setupConfirmText(confirmStopSetup); got != want {
			t.Errorf("Risk %d: %q, want %q", risk, got, want)
		}
		if d.m.setupConfirmText(confirmQuit) != "" || d.m.setupConfirmText(confirmStop) != "" {
			t.Error("setupConfirmText words another screen's question")
		}
	}
}

// Two y presses, the second while the first's Job is held, send one setup.
func TestSetupSendsOnce(t *testing.T) {
	d, fb := setupDriver(t, 45, 10)
	j := newJob(flash.JobSetup)
	fb.set(func(f *fakeBackend) { f.setup = func(flash.SetupRequest) *flash.Job { return j } })
	setupType(d, setupAdmin, setupTestAdmin)
	d.hold(true)
	d.keys("y")
	d.advance(600 * time.Millisecond) // not a held y: a second, deliberate press
	d.keys("y")
	d.releaseAll()
	if n := setupCalls(fb); n != 1 {
		t.Errorf("%d setups sent", n)
	}
}

// A held y (owner question 5): its first press sends, its repeats send nothing more; and a y less than 0.5 s after
// a y typed in a text box sends nothing, until a deliberate press.
func TestSetupHeldYSendsNothing(t *testing.T) {
	d, fb := setupDriver(t, 45, 10)
	jobs := 0
	fb.set(func(f *fakeBackend) {
		f.setup = func(flash.SetupRequest) *flash.Job {
			jobs++
			return finishedJob(flash.JobSetup, flash.Outcome{Title: "Setup stopped", Retry: true})
		}
	})
	setupType(d, setupName, "Hilly")
	setupType(d, setupAdmin, setupTestAdmin)
	setupFocus(d, setupName)
	d.keys("enter", "y", "enter") // a y typed, then Enter, then y at once
	d.keys("y")
	if n := setupCalls(fb); n != 0 {
		t.Fatalf("a y 0 s after a typed y sent %d setups", n)
	}
	d.advance(600 * time.Millisecond)
	d.keys("y")
	for at := 33 * time.Millisecond; at <= 2*time.Second; at += 33 * time.Millisecond {
		d.advance(33 * time.Millisecond)
		d.keys("y")
	}
	if n := setupCalls(fb); n != 1 {
		t.Errorf("a held y sent %d setups", n)
	}
}

// The form shows the guest password as •••• from the start, and its box one • per character; it never shows it.
func TestSetupGuestMasked(t *testing.T) {
	p := heltecV4Port("ttyACM0")
	d := newDriver(t, 45, 10, setupBackend(p, setupNode(p, nodecli.RoleRoomServer, "v1.18.0")))
	setupOpen(d, p)
	setupFocus(d, setupGuest)
	if v := d.view(); !strings.Contains(v, "Guest password   ••••") {
		t.Errorf("the guest password row isn't masked:\n%s", v)
	}
	d.keys("enter")
	if v := d.view(); !strings.Contains(v, strings.Repeat("•", len(setupTestGuest))) {
		t.Errorf("the guest password box doesn't show a • per character:\n%s", v)
	}
	if string(d.m.setup.node.Current.GuestPassword) != "" {
		t.Error("the node's guest password is kept outside the form's box")
	}
	d.keys("esc")
	d.noSecret(setupTestGuest)
}

// Both passwords are empty again after sending (r back to the form) and after leaving the form; Esc on a password
// box wipes the box.
func TestSetupPasswordCleared(t *testing.T) {
	d, _, j, _ := setupSendingDriver(t, 45, 10)
	j.Finish(flash.Outcome{Title: "Setup stopped", Retry: true, State: "Nothing on the node was changed."})
	d.settle()
	d.keys("r")
	if d.m.setup.mode != setupForm || d.m.setup.focus != setupAdmin {
		t.Fatalf("r didn't bring back the form on the admin password: mode %d, focus %d", d.m.setup.mode,
			d.m.setup.focus)
	}
	d.keys("enter")
	if d.m.setup.box.Value() != "" || len(d.m.setup.admin.text) != 0 {
		t.Error("the password box isn't empty after sending")
	}
	d.typeText("abc")
	d.keys("esc")
	if len(d.m.setup.box.text) != 0 || len(d.m.setup.admin.text) != 0 {
		t.Error("Esc kept the cancelled password")
	}
	setupType(d, setupAdmin, setupTestAdmin)
	held := d.m.setup.admin.text
	d.keys("esc") // leaves the form
	for _, r := range held {
		if r != 0 {
			t.Fatal("leaving the form didn't wipe the admin password")
		}
	}
	setupOpen(d, heltecV4Port("ttyACM0"))
	if len(d.m.setup.admin.text) != 0 {
		t.Error("the admin password came back when the form reopened")
	}
	d.noSecret(setupTestAdmin)
}

// r goes back to the form only after a failure that can be retried, with the values kept.
func TestSetupRetryBackToForm(t *testing.T) {
	d, _, j, _ := setupSendingDriver(t, 45, 10)
	moved := heltecV4Port("ttyACM1") // the board came back on another tty after the reboot
	j.Finish(flash.Outcome{Title: "Setup stopped", Retry: true, Board: &moved})
	d.settle()
	if !strings.Contains(d.view(), "r retry") {
		t.Errorf("no r on a failure that can be retried:\n%s", d.view())
	}
	d.keys("r")
	if d.m.setup.mode != setupForm || d.m.setup.vals[setupName] != "Hilltop 2" ||
		d.m.setup.port.Name != "ttyACM1" || d.m.setup.node.Port.Name != "ttyACM1" {
		t.Errorf("r: mode %d, name %q, port %q", d.m.setup.mode, d.m.setup.vals[setupName], d.m.setup.port.Name)
	}

	d2, _, j2, _ := setupSendingDriver(t, 45, 10)
	j2.Finish(flash.Outcome{Title: "Setup stopped", State: "Nothing on the node was changed."})
	d2.settle()
	d2.keys("r")
	if d2.m.setup.mode != setupFailed || strings.Contains(d2.view(), "r retry") {
		t.Errorf("r on a failure that can't be retried: mode %d\n%s", d2.m.setup.mode, d2.view())
	}
	d2.keys("enter")
	if d2.m.screen != scrBoard {
		t.Error("Enter at the end didn't go back to the Board")
	}
}

// The radio a setup sent is saved whenever the Outcome says the node took it: after a setup that worked, and after
// one that failed past set radio; not when it didn't get that far.
func TestSetupSavesRadio(t *testing.T) {
	sent := &flash.Sent{Radio: nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8}, TxDBm: 20, PathHashBytes: 2}
	for _, c := range []struct {
		name string
		o    flash.Outcome
		want bool
	}{
		{"worked", flash.Outcome{OK: true, Title: "Set up", Sent: sent}, true},
		{"failed past set radio", flash.Outcome{Title: "Setup stopped", Retry: true, Sent: sent}, true},
		{"failed before it", flash.Outcome{Title: "Setup stopped", Retry: true}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, _, j, _ := setupSendingDriver(t, 45, 10)
			j.Finish(c.o)
			d.settle()
			want := store.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8, TxDBm: 20, PathHashBytes: 2}
			switch got := d.m.cfg.Radio; {
			case c.want && (got == nil || *got != want):
				t.Errorf("m.cfg.Radio is %v, want %v", got, want)
			case !c.want && got != nil:
				t.Errorf("m.cfg.Radio is %v, want none", got)
			}
			if !setupStoreBuilt(d) {
				return
			}
			cfg, err := d.st.LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if (cfg.Radio != nil) != c.want || c.want && *cfg.Radio != want {
				t.Errorf("config.json's radio is %v", cfg.Radio)
			}
		})
	}
}

// A picture of a setup Job Setup didn't start (the lock's tests watch one from the Board) changes nothing.
func TestSetupIgnoresOtherJobs(t *testing.T) {
	d, _ := setupDriver(t, 45, 10)
	j := newJob(flash.JobSetup)
	d.run(d.m.watch(j))
	j.Set(setupAt(2, flash.RiskSettings))
	d.settle()
	j.Finish(flash.Outcome{OK: true, Title: "Set up", Sent: &flash.Sent{TxDBm: 5}})
	d.settle()
	if d.m.setup.mode != setupForm || d.m.cfg.Radio != nil {
		t.Errorf("another Job's pictures moved Setup: mode %d, radio %v", d.m.setup.mode, d.m.cfg.Radio)
	}
}

// The radio's defaults: the messenger's when its file was used, else the last radio sent when the firmware takes
// it, else the node's own.
func TestSetupDefaultsOrder(t *testing.T) {
	p := heltecV4Port("ttyACM0")
	last := func(r store.Radio) func(*Options) { return func(o *Options) { o.Config.Radio = &r } }
	for _, c := range []struct {
		name      string
		version   string
		messenger nodecli.MessengerRadio
		note      string
		opts      []func(*Options)
		want      [4]string
	}{
		{name: "messenger", version: "v1.18.0",
			messenger: nodecli.MessengerRadio{Radio: nodecli.Radio{FreqMHz: 915.8, BWkHz: 250, SF: 10, CR: 5}},
			opts:      []func(*Options){last(store.Radio{FreqMHz: 433.65, BWkHz: 62.5, SF: 8, CR: 8, TxDBm: 10})},
			want:      [4]string{"915.8", "250", "10", "5"}},
		{name: "messenger skipped, last sent", version: "v1.18.0", note: "Your messenger's radio settings weren't used: it lacks one of freq_mhz, bw_khz, sf and cr.",
			opts: []func(*Options){last(store.Radio{FreqMHz: 433.65, BWkHz: 62.5, SF: 8, CR: 8, TxDBm: 10})},
			want: [4]string{"433.65", "62.5", "8", "8"}},
		{name: "last sent below this firmware's floor, node", version: "v1.14.0",
			opts: []func(*Options){last(store.Radio{FreqMHz: 169.4, BWkHz: 62.5, SF: 8, CR: 8, TxDBm: 10})},
			want: [4]string{"869.618", "62.5", "8", "8"}},
		{name: "node", version: "v1.18.0", want: [4]string{"869.618", "62.5", "8", "8"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, c.version))
			fb.messenger, fb.messengerNote = c.messenger, c.note
			d := newDriver(t, 45, 10, fb, c.opts...)
			setupOpen(d, p)
			v := d.m.setup.vals
			if got := [4]string{v[setupFreq], v[setupBW], v[setupSF], v[setupCR]}; got != c.want {
				t.Errorf("radio rows %q, want %q", got, c.want)
			}
		})
	}
}

// TX power (owner question 6): the messenger's, else the last sent, else the node's, capped at the board's limit
// with a note saying so.
func TestSetupTxDefaults(t *testing.T) {
	p := heltecV4Port("ttyACM0")
	last := func(o *Options) {
		o.Config.Radio = &store.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8, TxDBm: 17}
	}
	for _, c := range []struct {
		name, device string
		messenger    nodecli.MessengerRadio
		opts         []func(*Options)
		want, note   string
	}{
		{name: "messenger", device: "Heltec v4", messenger: nodecli.MessengerRadio{
			Radio: nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8}, TxDBm: 20, HasTx: true},
			opts: []func(*Options){last}, want: "20"},
		{name: "last sent", device: "Heltec v4", opts: []func(*Options){last}, want: "17"},
		{name: "node", device: "Heltec v4", want: "22"},
		{name: "capped", device: "UnitEng Station G2", messenger: nodecli.MessengerRadio{
			Radio: nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8}, TxDBm: 22, HasTx: true},
			want: "19", note: "22 dBm is above this board's limit: 19 dBm."},
	} {
		t.Run(c.name, func(t *testing.T) {
			fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0"))
			fb.messenger = c.messenger
			d := newDriver(t, 45, 10, fb, c.opts...)
			d.run(d.m.openSetup(p, c.device, false))
			setupFocus(d, setupTx)
			if got := d.m.setup.vals[setupTx]; got != c.want {
				t.Errorf("TX power %q, want %q", got, c.want)
			}
			text := setupScreenText(d)
			if c.note != "" && !strings.Contains(text, c.note) || c.note == "" && strings.Contains(text, "above") {
				t.Errorf("the cap's note (%q):\n%s", c.note, d.view())
			}
			if c.note != "" {
				setupType(d, setupTx, "18")
				if strings.Contains(setupScreenText(d), c.note) {
					t.Error("the cap's note stayed once the TX power was changed")
				}
			}
		})
	}
}

// The path hash (owner question 6): the preset's whose radio the form starts with, when the presets with that radio
// agree on one; else the last sent; else the node's.
func TestSetupPathHashDefaults(t *testing.T) {
	p := heltecV4Port("ttyACM0")
	australia := nodecli.MessengerRadio{Radio: nodecli.Radio{FreqMHz: 915.8, BWkHz: 250, SF: 10, CR: 5}}
	last := func(o *Options) {
		o.Config.Radio = &store.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8, TxDBm: 22, PathHashBytes: 3}
	}
	for _, c := range []struct {
		name      string
		messenger nodecli.MessengerRadio
		opts      []func(*Options)
		want      string
	}{
		{"preset", australia, []func(*Options){last}, "2"},
		// EU/UK (Narrow), Netherlands (Limburg) and Switzerland share the radio but not the path hash.
		{"presets disagree, last sent", nodecli.MessengerRadio{}, []func(*Options){last}, "3"},
		{"node", nodecli.MessengerRadio{}, nil, "1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0"))
			fb.messenger = c.messenger
			d := newDriver(t, 45, 10, fb, c.opts...)
			setupOpen(d, p)
			if got := d.m.setup.vals[setupPathHash]; got != c.want {
				t.Errorf("path hash %q, want %q", got, c.want)
			}
		})
	}
}

// TX power is refused above the board's limit and below the radio's floor, saying the limit.
func TestSetupTxLimit(t *testing.T) {
	d, fb := setupDriver(t, 45, 10)
	setupType(d, setupAdmin, setupTestAdmin)
	setupType(d, setupTx, "23")
	if text := setupScreenText(d); !strings.Contains(text, "23 dBm is above this board's limit") ||
		!strings.Contains(text, "Choose 22 or less.") {
		t.Errorf("23 dBm on a 22 dBm board:\n%s", d.view())
	}
	d.keys("y")
	if setupCalls(fb) != 0 {
		t.Fatal("23 dBm was sent")
	}
	setupType(d, setupTx, "-10")
	if !strings.Contains(setupScreenText(d), "-10 dBm is below -9") {
		t.Errorf("-10 dBm:\n%s", d.view())
	}
	setupType(d, setupTx, "x")
	if !strings.Contains(setupScreenText(d), "Type a whole number of dBm, -9 to 22.") {
		t.Errorf("x dBm:\n%s", d.view())
	}
	setupType(d, setupTx, "-9")
	d.advance(time.Second)
	d.keys("y")
	if setupCalls(fb) != 1 || fb.setups[0].Settings.TxDBm != -9 {
		t.Errorf("-9 dBm wasn't sent: %d calls", setupCalls(fb))
	}
}

// The presets fill the four radio rows, and the path hash when the preset gives one; Esc leaves the form as it was.
func TestSetupPresetApplies(t *testing.T) {
	d, _ := setupDriver(t, 45, 10)
	d.keys("p")
	if d.m.setup.preset != 8 { // EU/UK (Narrow): the node's radio
		t.Errorf("the list opened on preset %d", d.m.setup.preset)
	}
	d.keys("k", "esc")
	if d.m.setup.vals[setupFreq] != "869.618" || d.m.setup.mode != setupForm {
		t.Error("Esc on the presets changed the form")
	}
	d.keys("p")
	for d.m.setup.preset != 0 {
		d.keys("k")
	}
	d.keys("enter") // Australia: 915.8 MHz, 250 kHz, SF10, CR5, a 2-byte path hash
	v := d.m.setup.vals
	if got := [5]string{v[setupFreq], v[setupBW], v[setupSF], v[setupCR], v[setupPathHash]}; got !=
		[5]string{"915.8", "250", "10", "5", "2"} {
		t.Errorf("Australia's preset gave %q", got)
	}
	if d.m.setup.focus != setupFreq || !strings.Contains(d.m.note, "Australia") {
		t.Errorf("after the preset: focus %d, note %q", d.m.setup.focus, d.m.note)
	}
	d.keys("p")
	for d.m.setup.preset != 9 { // EU/UK (Deprecated): no path hash of its own
		d.keys("j")
	}
	d.keys("enter")
	if d.m.setup.vals[setupPathHash] != "2" || d.m.setup.vals[setupFreq] != "869.525" {
		t.Errorf("a preset without a path hash: %q, %q", d.m.setup.vals[setupFreq], d.m.setup.vals[setupPathHash])
	}
}

// Esc in a text box restores the old value; Tab keeps it and moves on; pick lists keep with Enter and cancel with
// Esc.
func TestSetupEditKeys(t *testing.T) {
	d, _ := setupDriver(t, 45, 10)
	d.keys("enter", "ctrl+u")
	d.typeText("Gone")
	d.keys("esc")
	if d.m.setup.vals[setupName] != "Hilltop 2" || !strings.Contains(d.view(), "▸ Name             Hilltop 2") {
		t.Errorf("Esc didn't restore the name:\n%s", d.view())
	}
	d.keys("enter")
	d.typeText(" East")
	d.keys("tab")
	if d.m.setup.vals[setupName] != "Hilltop 2 East" || d.m.setup.focus != setupFreq || d.m.setup.mode != setupForm {
		t.Errorf("Tab in the box: name %q, focus %d", d.m.setup.vals[setupName], d.m.setup.focus)
	}
	d.keys("j", "enter", "j", "esc")
	if d.m.setup.vals[setupBW] != "62.5" {
		t.Errorf("Esc on the pick list changed the bandwidth to %q", d.m.setup.vals[setupBW])
	}
	d.keys("enter", "j", "enter")
	if d.m.setup.vals[setupBW] != "125" {
		t.Errorf("Enter on the pick list kept %q", d.m.setup.vals[setupBW])
	}
	d.keys("enter", "?")
	if !d.m.help || d.m.setup.mode != setupPicking {
		t.Error("? in a pick list didn't open Help")
	}
	d.keys("x", "esc")
	setupFocus(d, setupName)
	d.keys("enter", "?", "j", "y")
	if d.m.help || d.m.setup.box.Value() != "Hilltop 2 East?jy" {
		t.Errorf("? j y in a text box aren't letters: %q", d.m.setup.box.Value())
	}
	d.keys("esc", "shift+tab")
	if d.m.setup.focus != setupAdmin {
		t.Errorf("Shift+Tab from the first row went to %d", d.m.setup.focus)
	}
	d.keys("tab")
	if d.m.setup.focus != setupName {
		t.Errorf("Tab from the last row went to %d", d.m.setup.focus)
	}
}

// A blank latitude, longitude or local advert sends nothing; "off" or 0 turns the local advert off.
func TestSetupBlankRowsSendNothing(t *testing.T) {
	d, fb := setupDriver(t, 45, 10)
	fb.set(func(f *fakeBackend) { f.setup = func(flash.SetupRequest) *flash.Job { return newJob(flash.JobSetup) } })
	setupType(d, setupLat, "")
	setupType(d, setupLon, "")
	setupType(d, setupAdvert, "OFF")
	if text := setupScreenText(d); !strings.Contains(text, "Latitude not sent") ||
		!strings.Contains(text, "Local advert off") {
		t.Errorf("a blank latitude, the local advert off:\n%s", d.view())
	}
	setupType(d, setupAdmin, setupTestAdmin)
	d.keys("y")
	if setupCalls(fb) != 1 {
		t.Fatalf("not sent: %q", d.m.note)
	}
	s := fb.setups[0].Settings
	if s.Lat != nil || s.Lon != nil || s.AdvertInterval == nil || *s.AdvertInterval != 0 {
		t.Errorf("blank rows sent: lat %v lon %v advert %v", s.Lat, s.Lon, s.AdvertInterval)
	}
}

// The status line: the port while reading, the node's role and version on the form, how it ended at the end; the
// mark follows the node's port, then the Outcome's Board.
func TestSetupTitleAndMark(t *testing.T) {
	d, _, j, _ := setupSendingDriver(t, 45, 10)
	if first := lines(d.view())[0]; !strings.HasPrefix(first, "Setting up repeater v1.18.0") ||
		!strings.HasSuffix(first, "…") {
		t.Errorf("while sending: %q", first)
	}
	j.Finish(flash.Outcome{OK: true, Title: "Set up"}) // no Board: it's gone
	d.settle()
	if first := lines(d.view())[0]; !strings.HasPrefix(first, "Set up") || !strings.HasSuffix(first, "○") {
		t.Errorf("at the end with the board gone: %q", first)
	}
}

// Every Setup state's hint fits uncut at each size, with "? help" outside a text box (S12).
func TestSetupHintsFit(t *testing.T) {
	long := []string{"One.", "Two.", "Three.", "Four.", "Five.", "Six.", "Seven.", "Eight.", "Nine.", "Ten."}
	states := map[string]func(t *testing.T, w, h int) *driver{
		"reading": func(t *testing.T, w, h int) *driver {
			p := heltecV4Port("ttyACM0")
			d := newDriver(t, w, h, setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0")))
			d.hold(true)
			setupOpen(d, p)
			return d
		},
		"read failed": func(t *testing.T, w, h int) *driver {
			p := heltecV4Port("ttyACM0")
			d := newDriver(t, w, h, setupBackend(p, flash.Node{Port: p, Problem: flash.NoPermission}))
			setupOpen(d, p)
			return d
		},
		"form": func(t *testing.T, w, h int) *driver { d, _ := setupDriver(t, w, h); return d },
		"editing": func(t *testing.T, w, h int) *driver {
			d, _ := setupDriver(t, w, h)
			d.keys("enter")
			return d
		},
		"picking": func(t *testing.T, w, h int) *driver {
			d, _ := setupDriver(t, w, h)
			setupFocus(d, setupCR)
			d.keys("enter")
			return d
		},
		"presets": func(t *testing.T, w, h int) *driver {
			d, _ := setupDriver(t, w, h)
			d.keys("p")
			return d
		},
		"sending": func(t *testing.T, w, h int) *driver {
			d, _, j, _ := setupSendingDriver(t, w, h)
			j.Set(setupAt(3, flash.RiskSettings))
			d.settle()
			return d
		},
		"stopping": func(t *testing.T, w, h int) *driver {
			d, _, _, _ := setupSendingDriver(t, w, h)
			d.advance(time.Second)
			d.keys("esc", "y")
			return d
		},
		"done": func(t *testing.T, w, h int) *driver {
			d, _, j, _ := setupSendingDriver(t, w, h)
			j.Finish(setupOK(heltecV4Port("ttyACM0")))
			d.settle()
			return d
		},
		"done, scrolling": func(t *testing.T, w, h int) *driver {
			d, _, j, _ := setupSendingDriver(t, w, h)
			j.Finish(flash.Outcome{OK: true, Title: "Set up", Summary: long})
			d.settle()
			return d
		},
		"failed": func(t *testing.T, w, h int) *driver {
			d, _, j, _ := setupSendingDriver(t, w, h)
			j.Finish(flash.Outcome{Title: "Setup stopped", Retry: true, State: "Nothing on the node was changed."})
			d.settle()
			return d
		},
		"failed, scrolling": func(t *testing.T, w, h int) *driver {
			d, _, j, _ := setupSendingDriver(t, w, h)
			j.Finish(flash.Outcome{Title: "Setup stopped", Retry: true, Summary: long, State: "Nothing changed."})
			d.settle()
			return d
		},
		"failed, no retry": func(t *testing.T, w, h int) *driver {
			d, _, j, _ := setupSendingDriver(t, w, h)
			j.Finish(flash.Outcome{Title: "Setup stopped", Summary: long})
			d.settle()
			return d
		},
	}
	for name, open := range states {
		t.Run(name, func(t *testing.T) {
			eachSize(t, func(t *testing.T, w, h int) {
				d := open(t, w, h)
				defer d.releaseAll()
				ls := lines(d.view())
				if len(ls) != h {
					t.Fatalf("%d lines, want %d", len(ls), h)
				}
				hint := ls[len(ls)-1]
				if width(hint) > w || width(hint) == w && strings.HasSuffix(hint, "…") {
					t.Errorf("the hint is cut: %q", hint)
				}
				if !d.m.setupEditing() && !strings.Contains(hint, "? help") {
					t.Errorf("no ? help in %q", hint)
				}
				for i, l := range ls {
					if width(l) > w {
						t.Errorf("line %d is %d cells wide: %q", i+1, width(l), l)
					}
				}
			})
		})
	}
}

// Much smaller than the three sizes (a terminal made tiny), every Setup state still draws exactly h lines, none too
// wide, and nothing panics.
func TestSetupSmallScreens(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{30, 6}, {12, 4}} {
		t.Run(fmt.Sprintf("%dx%d", sz.w, sz.h), func(t *testing.T) {
			w, h := sz.w, sz.h
			p := heltecV4Port("ttyACM0")
			fb := setupBackend(p, setupNode(p, nodecli.RoleRoomServer, "v1.18.0"))
			d := newDriver(t, w, h, fb)
			j := newJob(flash.JobSetup)
			fb.set(func(f *fakeBackend) { f.setup = func(flash.SetupRequest) *flash.Job { return j } })
			look := func(what string) {
				t.Helper()
				ls := lines(d.view())
				if len(ls) != h {
					t.Errorf("%s: %d lines, want %d", what, len(ls), h)
				}
				for i, l := range ls {
					if width(l) > w {
						t.Errorf("%s: line %d is %d cells wide: %q", what, i+1, width(l), l)
					}
				}
			}
			d.hold(true)
			setupOpen(d, p)
			look("reading")
			d.releaseAll()
			look("form")
			d.keys("enter")
			d.typeText("a very long name, past the box")
			look("editing")
			d.keys("esc", "j", "j", "enter")
			look("picking")
			d.keys("esc", "p")
			look("presets")
			d.keys("esc")
			setupType(d, setupAdmin, setupTestAdmin)
			d.keys("y")
			j.Set(setupAt(12, flash.RiskSettings))
			d.settle()
			look("sending")
			j.Finish(flash.Outcome{Title: "Setup stopped", Retry: true, Summary: []string{"One.", "Two."},
				State: "Nothing on the node was changed.", Recovery: "Set it up again."})
			d.settle()
			look("failed")
		})
	}
}

// --- Drafts (S7; owner question 13) ---

// What was typed survives Esc, Ctrl+C and a SIGTERM's SaveUnsent, and comes back when the form reopens.
func TestSetupDraftKept(t *testing.T) {
	p := heltecV4Port("ttyACM0")
	for _, how := range []string{"esc", "ctrl+c", "SaveUnsent"} {
		t.Run(how, func(t *testing.T) {
			fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0"))
			d := newDriver(t, 45, 10, fb)
			setupNeedsStore(t, d)
			setupOpen(d, p)
			setupType(d, setupName, "Hilltop East")
			setupType(d, setupLat, "")
			switch how {
			case "SaveUnsent":
				d.m.SaveUnsent()
			default:
				d.keys(how)
			}
			draft, ok := d.st.LoadDraft(p.DraftKey())
			if !ok || draft.Name != "Hilltop East" || draft.Freq != "" || draft.Role != nodecli.RoleRepeater ||
				len(draft.Cleared) != 1 || draft.Cleared[0] != "lat" || !draft.Saved.Equal(testNow) {
				t.Fatalf("the draft after %s: %+v (%v)", how, draft, ok)
			}
			again := newDriver(t, 45, 10, fb, func(o *Options) { o.Store = d.st })
			setupOpen(again, p)
			v := again.m.setup.vals
			if v[setupName] != "Hilltop East" || v[setupLat] != "" || v[setupLon] != "-0.1278" ||
				again.m.note != "Restored what was typed for this board." {
				t.Errorf("reopened: name %q lat %q lon %q, note %q", v[setupName], v[setupLat], v[setupLon],
					again.m.note)
			}
		})
	}
}

// A form put back to its defaults deletes the board's draft.
func TestSetupDraftBackToDefaults(t *testing.T) {
	d, _ := setupDriver(t, 45, 10)
	setupNeedsStore(t, d)
	setupType(d, setupName, "Hilltop East")
	d.m.SaveUnsent()
	if _, ok := d.st.LoadDraft(heltecV4Port("").DraftKey()); !ok {
		t.Fatal("no draft")
	}
	setupType(d, setupName, "Hilltop 2")
	d.m.SaveUnsent()
	d.m.SaveUnsent() // twice in a row, as a quit and then Shutdown
	if _, ok := d.st.LoadDraft(heltecV4Port("").DraftKey()); ok {
		t.Error("a form back at its defaults kept its draft")
	}
}

// An Esc hold reaches the model as one Esc, then Shutdown: the line the Esc cancelled is what the draft keeps.
func TestSetupDraftKeptThroughEscHold(t *testing.T) {
	d, _ := setupDriver(t, 45, 10)
	setupNeedsStore(t, d)
	d.keys("enter", "ctrl+u")
	d.typeText("Hill")
	d.keys("esc")
	if !strings.Contains(d.view(), "▸ Name             Hilltop 2") {
		t.Errorf("the screen doesn't show the old name after Esc:\n%s", d.view())
	}
	d.m.Shutdown()
	if draft, ok := d.st.LoadDraft(heltecV4Port("").DraftKey()); !ok || draft.Name != "Hill" {
		t.Errorf("the draft after an Esc hold: %+v (%v)", draft, ok)
	}
}

// A key, or 3.5 s, after the Esc drops the cancelled line: only an Esc hold keeps it.
func TestSetupCancelledTextDropped(t *testing.T) {
	for _, between := range []string{"key", "3.5 s"} {
		t.Run(between, func(t *testing.T) {
			d, _ := setupDriver(t, 45, 10)
			setupNeedsStore(t, d)
			d.keys("enter", "ctrl+u")
			d.typeText("Hill")
			d.keys("esc")
			if between == "key" {
				d.keys("j")
			} else {
				d.advance(setupCancelKeep)
			}
			d.m.Shutdown()
			if draft, ok := d.st.LoadDraft(heltecV4Port("").DraftKey()); ok {
				t.Errorf("the cancelled line was kept: %+v", draft)
			}
		})
	}
}

// After a setup that worked, its draft is gone and a quit brings none back.
func TestSetupDraftNotAfterSend(t *testing.T) {
	d, fb := setupDriver(t, 45, 10)
	setupNeedsStore(t, d)
	j := newJob(flash.JobSetup)
	fb.set(func(f *fakeBackend) { f.setup = func(flash.SetupRequest) *flash.Job { return j } })
	setupType(d, setupName, "Hilltop East")
	setupType(d, setupAdmin, setupTestAdmin)
	d.keys("y")
	if _, ok := d.st.LoadDraft(heltecV4Port("").DraftKey()); !ok {
		t.Fatal("y didn't save the draft before sending")
	}
	j.Finish(setupOK(heltecV4Port("ttyACM0")))
	d.settle()
	if _, ok := d.st.LoadDraft(heltecV4Port("").DraftKey()); ok {
		t.Error("the draft stayed after the setup worked")
	}
	d.keys("ctrl+c")
	d.m.Shutdown()
	if _, ok := d.st.LoadDraft(heltecV4Port("").DraftKey()); ok {
		t.Error("a quit after the setup brought the draft back")
	}
}

// A board with no useful serial (a Heltec v3's CP2102 says "0001") keeps its draft by USB socket, and says so.
func TestSetupDraftBySocket(t *testing.T) {
	p := cp2102Port("ttyUSB0")
	if p.Key != "" || p.DraftKey() != "usb 1-1.2 10c4:ea60" {
		t.Fatalf("the CP2102's keys: %q, %q", p.Key, p.DraftKey())
	}
	fb := setupBackend(p, setupNode(p, nodecli.RoleRepeater, "v1.18.0"))
	d := newDriver(t, 45, 10, fb)
	setupNeedsStore(t, d)
	setupOpen(d, p)
	setupType(d, setupName, "Valley")
	d.keys("esc")
	again := newDriver(t, 45, 10, fb, func(o *Options) { o.Store = d.st })
	setupOpen(again, p)
	if again.m.setup.vals[setupName] != "Valley" ||
		again.m.note != "Restored what was typed for the board on this USB socket." {
		t.Errorf("reopened: name %q, note %q", again.m.setup.vals[setupName], again.m.note)
	}
}

// drafts.json never holds a password: not typed in the row, not half-typed in its box at a quit.
func TestSetupDraftHasNoPassword(t *testing.T) {
	p := heltecV4Port("ttyACM0")
	d := newDriver(t, 45, 10, setupBackend(p, setupNode(p, nodecli.RoleRoomServer, "v1.18.0")))
	setupNeedsStore(t, d)
	setupOpen(d, p)
	setupType(d, setupName, "Hilltop East")
	setupType(d, setupAdmin, setupTestAdmin)
	setupFocus(d, setupGuest)
	d.keys("enter")
	d.typeText("half")
	d.keys("ctrl+c")
	b, err := os.ReadFile(filepath.Join(d.configDir, store.DraftsFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Hilltop East") {
		t.Fatalf("no draft saved: %s", b)
	}
	for _, pw := range append(setupTestPasswords, "half") {
		if strings.Contains(string(b), pw) {
			t.Error("drafts.json holds a password")
		}
	}
	if strings.Contains(strings.ToLower(string(b)), "pass") {
		t.Error("drafts.json has a password field")
	}
	d.noSecret(setupTestAdmin)
	d.noSecret(setupTestGuest)
}

// No golden of any test, Setup's or another screen's, holds a password a test typed or a fake node holds.
func TestNoGoldenHoldsThePassword(t *testing.T) {
	n := 0
	err := filepath.WalkDir("testdata", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		for _, pw := range setupTestPasswords {
			if strings.Contains(string(b), pw) {
				t.Errorf("%s holds a test password", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("no goldens found")
	}
}
