package flash_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

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
				p.Risk != flash.RiskSettings {
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

// TestSetupCancelledSaysWhatWasSent: a setup stopped between commands (Esc and y, or a Ctrl+C) says what was sent
// and read back, what the node is left with, and how to finish, by the step it reached (4.2).
func TestSetupCancelledSaysWhatWasSent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		after    int // the commands the node had handled when the stop came
		risk     flash.Risk
		summary  string
		state    string
		recovery string
		sent     bool
	}{
		{"before anything", 1, flash.RiskNone, "Stopped before the node saved anything.",
			"Nothing on the node was changed.", "Set it up again when you're ready.", false},
		{"after the name", 3, flash.RiskSettings, "Sent and saved: clock, name.",
			"The settings sent before it stopped are saved on the node, still on its old radio.",
			"Set it up again to finish.", false},
		{"after the radio", 4, flash.RiskRadioSent, "Sent and saved: clock, name, radio.",
			"Its radio is saved but it hasn't restarted: it stays on its old radio until it restarts.",
			"Set it up again to finish, or restart it to switch to the new radio.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b, n := setupRig(t, repeater("v1.18.0", ""), fakeworld.HeltecV4)
			var mu sync.Mutex
			var j *flash.Job
			b.AfterCommands(tc.after, func() {
				mu.Lock()
				defer mu.Unlock()
				j.Cancel()
			})
			mu.Lock()
			j = r.b.Setup(context.Background(), flash.SetupRequest{Node: n, Settings: goodSettings()})
			mu.Unlock()
			p := wait(t, j)
			o := p.Outcome
			if !o.Stopped || o.Title != "Setup stopped" || !strings.HasPrefix(o.Summary[0], tc.summary) ||
				o.State != tc.state || o.Recovery != tc.recovery || p.Risk != tc.risk || (o.Sent != nil) != tc.sent {
				t.Fatalf("%s\nrisk %v sent %+v\n%s", outcome(o), p.Risk, o.Sent, steps(p))
			}
			if b.Node().Reboots() != 0 {
				t.Error("rebooted")
			}
			noSecret(t, r, p, adminPW)
		})
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
