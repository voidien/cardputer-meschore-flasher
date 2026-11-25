package flash_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/nodecli"
)

// The passwords the setup tests send: long enough that the world's password rule looks for them everywhere.
const (
	adminPW = "Tr0ub4dor&3"
	guestPW = "letmein42"
)

// goodSettings is a whole setup: a name, the EU narrow preset, TX 14 dBm, the path hash, a location, the local advert
// off, and the admin password.
func goodSettings() nodecli.Settings {
	lat, lon, adv := 51.5074, -0.1278, 0
	s := nodecli.Settings{Name: "Hilltop 2", TxDBm: 14, Lat: &lat, Lon: &lon, AdvertInterval: &adv,
		AdminPassword: adminPW}
	for _, p := range nodecli.Presets {
		if strings.Contains(p.Title, "EU") || strings.Contains(p.Title, "UK") {
			p.Apply(&s)
			break
		}
	}
	if s.Radio.FreqMHz == 0 {
		nodecli.Presets[0].Apply(&s)
	}
	return s
}

// setupRig is a rig with a board running a repeater (or room server), its node read as the Setup form would.
func setupRig(t *testing.T, fw fakeworld.Firmware, board func(fakeworld.Firmware) *fakeworld.Board) (*rig, *fakeworld.Board, flash.Node) {
	t.Helper()
	b := board(fw)
	r := newRig(t, b)
	n := r.b.ReadNode(context.Background(), r.port(b), false)
	if n.Problem != flash.NoProblem {
		t.Fatalf("read %+v", n)
	}
	return r, b, n
}

// noSecret fails if any of the setup's words hold a password: the steps, the outcome, screen.log.
func noSecret(t *testing.T, r *rig, p flash.Progress, secrets ...string) {
	t.Helper()
	words := []string{r.log(), fmt.Sprint(p.Outcome), fmt.Sprintf("%+v", p.Outcome), steps(p)}
	for _, s := range p.Steps {
		words = append(words, s.Text, s.Note)
	}
	for _, w := range words {
		for _, s := range secrets {
			if strings.Contains(w, s) {
				t.Errorf("a password is in what the backend said: %q", w)
			}
		}
	}
}

func TestSetupRepeater(t *testing.T) {
	for _, tc := range []struct {
		name  string
		board func(fakeworld.Firmware) *fakeworld.Board
	}{{"Heltec v4", fakeworld.HeltecV4}, {"RAK 4631", fakeworld.RAK4631}, {"Heltec v3", fakeworld.HeltecV3}} {
		t.Run(tc.name, func(t *testing.T) {
			r, b, n := setupRig(t, repeater("v1.18.0", ""), tc.board)
			s := goodSettings()
			p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: s}))
			o := p.Outcome
			if !o.OK || o.Title != "Set up" || o.Sent == nil || o.Sent.Radio != s.Radio || o.Sent.TxDBm != 14 ||
				o.Sent.PathHashBytes != s.PathHashBytes || o.Board == nil {
				t.Fatalf("%s\n%s", outcome(o), steps(p))
			}
			if !strings.Contains(o.Summary[0], "Sent and saved: clock, name, radio, TX power") ||
				!strings.Contains(o.Summary[1], "It restarted and runs v1.18.0 on") {
				t.Errorf("summary %q", o.Summary)
			}
			node := b.Node()
			if node.Name != "Hilltop 2" || node.AdminPassword != adminPW || node.TxPower != 14 ||
				node.Reboots() != 1 || !node.ActiveRadio.Near(s.Radio) {
				t.Errorf("the node has %q, TX %d, %d reboots", node.Name, node.TxPower, node.Reboots())
			}
			last := p.Steps[len(p.Steps)-1]
			if !strings.HasPrefix(last.Text, "Checked: v1.18.0 on ") || last.State != flash.StepDone ||
				p.Risk != flash.RiskRestarted {
				t.Errorf("steps:\n%s", steps(p))
			}
			noSecret(t, r, p, adminPW)
		})
	}
}

// TestSetupPlanned: every picture of a setup says how many steps it has in all (the lock page's "step 4 of 20"),
// and the last one has exactly that many. The pictures are taken as the node handles each command (AfterCommands,
// on the setup's own goroutine), so none is missed.
func TestSetupPlanned(t *testing.T) {
	r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
	s := goodSettings()
	plan, err := nodecli.Plan(func() nodecli.Settings { c := s; c.ClockSynced = true; return c }(), n.Info)
	if err != nil {
		t.Fatal(err)
	}
	want := len(plan) + 2
	var mu sync.Mutex
	var j *flash.Job
	var seen []flash.Progress
	for k := 1; k <= len(plan)+6; k++ {
		b.AfterCommands(k, func() {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, j.Snapshot())
		})
	}
	mu.Lock()
	j = r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: s})
	mu.Unlock()
	p := wait(t, j)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < len(plan)-1 {
		t.Errorf("only %d pictures", len(seen))
	}
	for _, q := range append(seen, p) {
		if q.Planned != want {
			t.Errorf("picture %d (%d steps): Planned %d, want %d", q.Seq, len(q.Steps), q.Planned, want)
		}
	}
	if len(p.Steps) != want {
		t.Errorf("%d steps, Planned %d:\n%s", len(p.Steps), want, steps(p))
	}
}

// A step the node refuses or keeps wrongly stops the setup, with what the node is left with in nodecli's words.
func TestSetupFailsAtAStep(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store map[string]string
		step  string
		state string
	}{
		{"radio refused", map[string]string{"radio": "1,2,3,4"}, "Radio: ",
			"What was sent before this step is saved on the node; run setup again to finish."},
		{"name kept wrongly", map[string]string{"name": "Hill"}, "Check name",
			"Every setting was sent to the node; run setup again to check them. It keeps its old radio until it restarts."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
			b.Node().Store = tc.store
			p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()}))
			o := p.Outcome
			if o.OK || o.Stopped || o.Title != "Setup stopped" || o.State != tc.state || !o.Retry ||
				!strings.HasPrefix(o.Summary[0], tc.step) {
				t.Fatalf("%s\n%s", outcome(o), steps(p))
			}
			if !strings.Contains(r.log(), "setup of ttyACM0 at step") {
				t.Errorf("screen.log %q", r.log())
			}
			noSecret(t, r, p, adminPW)
		})
	}
}

// The password step answered with a reply holding a password, and a room server's guest password read back wrong:
// no step, outcome or screen.log line holds either password.
func TestSetupPasswordsNeverShown(t *testing.T) {
	t.Run("admin", func(t *testing.T) {
		r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
		b.Node().Store = map[string]string{"password": adminPW + "x"}
		p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()}))
		if p.Outcome.OK || !strings.Contains(p.Outcome.Summary[0], "the node didn't confirm the password") {
			t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
		}
		noSecret(t, r, p, adminPW)
	})
	t.Run("guest", func(t *testing.T) {
		r, b, n := setupRig(t, fakeworld.Firmware{Role: nodecli.RoleRoomServer, Version: "v1.18.0"}, fakeworld.HeltecV4)
		b.Node().Store = map[string]string{"guest.password": "other1234"}
		s := goodSettings()
		s.GuestPassword = guestPW
		p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: s}))
		if p.Outcome.OK || !strings.Contains(p.Outcome.Summary[0], "the node has a different password stored") {
			t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
		}
		noSecret(t, r, p, adminPW, guestPW, "other1234")
	})
}

// Unplugged mid-setup: stopped, what the node may have kept, and the board gone.
func TestSetupUnplugged(t *testing.T) {
	r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.RAK4631)
	b.UnplugAfterCommands(3) // the clock, the name, the radio
	p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()}))
	o := p.Outcome
	if o.Title != "Setup stopped" || o.Board != nil || !o.Retry ||
		!strings.Contains(o.Summary[0], "the board went away") ||
		o.State != "What was sent before this step is saved on the node, and this step's setting may be too; run setup again to finish. It keeps its old radio until it restarts." {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
	noSecret(t, r, p, adminPW)
}

// TestSetupCancelledSaysWhatWasSent: a setup stopped (Esc and y, or a Ctrl+C) stops between commands: the command
// already written has its answer read, nothing more is written, and the outcome says what the node took and read
// back, what it is left with, and how to finish, by the step it reached (4.2). The stop comes once the node has
// handled after commands and before their answer reaches the backend (fakeworld's AfterCommands).
func TestSetupCancelledSaysWhatWasSent(t *testing.T) {
	const settings = "The settings sent before it stopped are saved on the node, still on its old radio."
	const radioSaved = "Its radio is saved but it hasn't restarted: it stays on its old radio until it restarts."
	for _, tc := range []struct {
		name     string
		after    int // the setup commands the node had handled when the stop came
		risk     flash.Risk
		summary  string
		state    string
		recovery string
		tx       int // the TX power recorded with the radio, 0 for no radio recorded
	}{
		{"after the clock", 1, flash.RiskSettings, "Sent and saved: clock.", settings, "Set it up again to finish.", 0},
		{"after the name", 2, flash.RiskSettings, "Sent and saved: clock, name.", settings,
			"Set it up again to finish.", 0},
		// The radio taken, its TX power not yet sent: the TX recorded is the node's own (22), not the form's 14.
		{"after the radio", 3, flash.RiskRadioSent, "Sent and saved: clock, name, radio.", radioSaved,
			"Set it up again to finish, or restart it to switch to the new radio.", 22},
		{"after the TX power", 4, flash.RiskRadioSent, "Sent and saved: clock, name, radio, TX power.", radioSaved,
			"Set it up again to finish, or restart it to switch to the new radio.", 14},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
			var mu sync.Mutex
			var j *flash.Job
			handled := -1
			b.AfterCommands(tc.after, func() {
				mu.Lock()
				defer mu.Unlock()
				handled = len(b.Node().Commands())
				j.Cancel()
			})
			mu.Lock()
			j = r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()})
			mu.Unlock()
			p := wait(t, j)
			o := p.Outcome
			if !o.Stopped || o.Title != "Setup stopped" || !strings.HasPrefix(o.Summary[0], tc.summary) ||
				o.State != tc.state || o.Recovery != tc.recovery || p.Risk != tc.risk || (o.Sent != nil) != (tc.tx != 0) ||
				o.Sent != nil && o.Sent.TxDBm != tc.tx {
				t.Fatalf("%s\nrisk %v sent %+v\n%s", outcome(o), p.Risk, o.Sent, steps(p))
			}
			mu.Lock()
			defer mu.Unlock()
			if cmds := b.Node().Commands(); len(cmds) != handled {
				t.Errorf("%d commands reached the node after the stop", len(cmds)-handled)
			}
			if b.Node().Reboots() != 0 {
				t.Error("rebooted")
			}
			noSecret(t, r, p, adminPW)
		})
	}
	t.Run("before anything", func(t *testing.T) {
		r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
		before := len(b.Node().Commands())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p := wait(t, r.b.Setup(ctx, flash.SetupRequest{Node: n, Settings: goodSettings()}))
		o := p.Outcome
		if !o.Stopped || o.Summary[0] != "Stopped before the node saved anything." ||
			o.State != "Nothing on the node was changed." || p.Risk != flash.RiskNone || o.Sent != nil {
			t.Fatalf("%s", outcome(o))
		}
		if got := len(b.Node().Commands()); got != before {
			t.Errorf("%d commands sent", got-before)
		}
	})
}

// A stop in the gap before the next command (the backend has read set name's answer and waits 100 ms before set
// radio) writes nothing more: the node never gets the radio, and the outcome says so.
func TestSetupStopInTheGapSendsNothing(t *testing.T) {
	r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
	paused, resume := b.PauseAfter("set name ")
	defer resume()
	j := r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()})
	select {
	case <-paused:
	case <-time.After(jobGuard):
		t.Fatal("the setup never paused after set name")
	}
	handled := len(b.Node().Commands())
	j.Cancel()
	resume()
	p := wait(t, j)
	o := p.Outcome
	if got := b.Node().Commands(); len(got) != handled {
		t.Errorf("sent after the stop: %q", got[handled:])
	}
	if !o.Stopped || o.Summary[0] != "Sent and saved: clock, name." || o.Sent != nil || p.Risk != flash.RiskSettings ||
		o.State != "The settings sent before it stopped are saved on the node, still on its old radio." {
		t.Fatalf("%s\nrisk %v sent %+v\n%s", outcome(o), p.Risk, o.Sent, steps(p))
	}
	for _, st := range p.Steps {
		if strings.Contains(st.Note, "stopped") {
			t.Errorf("the step says stopped twice: %+v", st)
		}
	}
}

// A stop while the node never answers set radio: after StopGrace the setup ends, saying the radio may have been
// saved, and it is recorded as sent (the node may switch to it at its next restart).
func TestSetupStopWithNoAnswerMayHaveSaved(t *testing.T) {
	r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
	b.Node().Hang = "set radio "
	var mu sync.Mutex
	var j *flash.Job
	b.AfterCommands(3, func() {
		mu.Lock()
		defer mu.Unlock()
		j.Cancel()
	})
	mu.Lock()
	j = r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()})
	mu.Unlock()
	p := wait(t, j)
	o := p.Outcome
	if !o.Stopped || len(o.Summary) != 2 || o.Summary[0] != "Sent and saved: clock, name." ||
		o.Summary[1] != "Radio was sent just as it stopped, and may have been saved." || o.Sent == nil ||
		o.Sent.TxDBm != 22 || p.Risk != flash.RiskRadioSent ||
		o.State != "What was sent before this step is saved on the node, and this step's setting may be too; run "+
			"setup again to finish. It keeps its old radio until it restarts." {
		t.Fatalf("%s\nrisk %v sent %+v\n%s", outcome(o), p.Risk, o.Sent, steps(p))
	}
}

// Settings the node can't take are refused before anything is sent.
func TestSetupRefusesBadSettings(t *testing.T) {
	r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
	s := goodSettings()
	s.AdminPassword = ""
	before := len(b.Node().Commands())
	p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: s}))
	if p.Outcome.OK || p.Outcome.State != "Nothing on the node was changed." || !strings.Contains(p.Outcome.Summary[0],
		"can't be sent") {
		t.Fatalf("%s", outcome(p.Outcome))
	}
	if got := len(b.Node().Commands()); got != before {
		t.Errorf("%d commands sent", got-before)
	}
}

// After the reboot the node came back with another radio than was sent.
func TestSetupCameBackDifferent(t *testing.T) {
	r, b, n := setupRig(t, repeater("v1.17.1", ""), fakeworld.HeltecV4)
	s := goodSettings()
	b.AfterCommands(12, func() { // the reboot handled: the node keeps another radio from now on
		node := b.Node()
		node.Radio = nodecli.DefaultRadio
	})
	p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: s}))
	o := p.Outcome
	if o.OK || o.Title != "Set up, but it came back different" || !strings.Contains(o.Summary[1], "its radio is") {
		t.Fatalf("%s\n%s", outcome(o), steps(p))
	}
}

// TestPauseAfterHoldsBetweenCommands: the world's PauseAfter holds a setup once the node has answered the named
// command and before the next is sent (where a test's Ctrl+C meets it), and lets it go on resume.
func TestPauseAfterHoldsBetweenCommands(t *testing.T) {
	r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
	paused, resume := b.PauseAfter("set radio ")
	defer resume()
	j := r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()})
	select {
	case <-paused:
	case <-time.After(jobGuard):
		t.Fatal("the setup never paused after set radio")
	}
	cmds := b.Node().Commands()
	if len(cmds) == 0 || !strings.HasPrefix(cmds[len(cmds)-1], "set radio ") {
		t.Errorf("paused after %q, not set radio", cmds)
	}
	if p := j.Snapshot(); p.Done || p.Risk != flash.RiskRadioSent {
		t.Errorf("paused: done %v, risk %d (want the radio sent)", p.Done, p.Risk)
	}
	resume()
	if p := wait(t, j); !p.Outcome.OK {
		t.Errorf("after resume: %s\n%s", outcome(p.Outcome), steps(p))
	}
}
