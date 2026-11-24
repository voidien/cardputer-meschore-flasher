package nodecli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

func TestFindReply(t *testing.T) {
	for _, c := range []struct {
		name, in, cmd, reply string
		ok, echoed           bool
		rest                 string
	}{
		{"repeater", "get name\r\n  -> > Hilltop\r\n", "get name", "> Hilltop", true, true, ""},
		{"room server", "get name\r  -> > Hilltop\r\n", "get name", "> Hilltop", true, true, ""},
		{"banner first", "Repeater ID: ABCD\r\nver\r\n  -> v1.18.0\r\n", "ver", "v1.18.0", true, true, ""},
		// A stale reply before this command's echo is not its reply.
		{"stale reply", "  -> OK\r\nget tx\r\n  -> > 22\r\nmore", "get tx", "> 22", true, true, "more"},
		{"no echo yet", "  -> OK\r\n", "get tx", "", false, false, ""},
		{"no reply yet", "get tx\r\n", "get tx", "", false, true, ""},
		{"reply cut short", "get tx\r\n  -> > 2", "get tx", "", false, true, ""},
		// Blank guest password: "> " with its space kept (the config tool trims it to ">").
		{"blank value", "get guest.password\r\n  -> > \r\n", "get guest.password", "> ", true, true, ""},
		{"escape", "\r\n  -> OK - loaded 0 regions\r\n", "", "OK - loaded 0 regions", true, true, ""},
	} {
		reply, used, ok, echoed := findReply([]byte(c.in), c.cmd)
		if reply != c.reply || ok != c.ok || echoed != c.echoed || (ok && c.in[used:] != c.rest) {
			t.Errorf("%s: got %q ok=%v echoed=%v rest=%q", c.name, reply, ok, echoed, c.in[used:])
		}
	}
}

// Commands longer than 128 bytes, or with a line break in them, are refused before anything is written.
func TestCommandCap(t *testing.T) {
	n := NewFakeNode(RoleRepeater, "v1.18.0")
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	p := serialport.NewFake(n, clock, log, serialport.Options{})
	c := newConn(context.Background(), p, clock)
	if _, err := c.exchange("set name "+strings.Repeat("x", MaxCommand-9+1), time.Second); !errors.Is(err, ErrTooLong) {
		t.Errorf("129 bytes: %v", err)
	}
	for _, bad := range []string{"set name a\rb", "set name a\nb", "set name a\x00b"} {
		if _, err := c.exchange(bad, time.Second); !errors.Is(err, ErrBadCommand) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if tx := log.TX(); len(tx) != 0 {
		t.Fatalf("refused commands were written: %q", tx)
	}
	// Exactly 128 is sent.
	cmd := "set name " + strings.Repeat("x", MaxCommand-9)
	if reply, err := c.exchange(cmd, time.Second); err != nil || reply != "OK" {
		t.Errorf("128 bytes: %q, %v", reply, err)
	}
	if string(log.TX()) != cmd+"\r" {
		t.Errorf("wrote %q", log.TX())
	}

	// And through Run, the step that would overflow fails by name with nothing written.
	log2 := &serialport.Log{}
	p2 := serialport.NewFake(n, clock, log2, serialport.Options{})
	steps := []Step{{Command: "set name " + strings.Repeat("y", 200), Expect: exact("OK"), Display: "Name: yyy…"}}
	err := Run(context.Background(), p2, clock, steps, nil)
	var se *StepError
	if !errors.As(err, &se) || !errors.Is(err, ErrTooLong) || se.Display != "Name: yyy…" {
		t.Fatalf("Run: %v", err)
	}
	if len(log2.TX()) != 0 {
		t.Errorf("Run wrote %q", log2.TX())
	}
}

// A command times out; the region-load escape (one bare CR) is tried, and when that gets no reply either the node
// isn't listening, so the step fails by name with ErrNoReply without sending the command again.
func TestTimeout(t *testing.T) {
	n := NewFakeNode(RoleRepeater, "v1.18.0")
	n.Hang = "set tx"
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	p := serialport.NewFake(n, clock, log, serialport.Options{})
	info := Info{Version: n.Version, Role: RoleRepeater, Board: "Heltec V3"}
	steps, err := Plan(goodSettings(), info)
	if err != nil {
		t.Fatal(err)
	}
	start := clock.Now()
	var last Progress
	err = Run(context.Background(), p, clock, steps, func(pr Progress) { last = pr })
	var se *StepError
	if !errors.As(err, &se) || !errors.Is(err, ErrNoReply) {
		t.Fatalf("Run: %v", err)
	}
	if se.Display != "TX power: 20 dBm" || !strings.Contains(se.Error(), "didn't answer within 5 seconds") {
		t.Errorf("error %q at step %q", se.Error(), se.Display)
	}
	if last.Status != Failed || last.Display != se.Display || last.Note != se.Reason {
		t.Errorf("last progress %+v", last)
	}
	// set tx reached the node and got no answer, so it may have been saved.
	if se.State != "What was sent before this step is saved on the node, and this step's setting may be too; run setup again to finish. It keeps its old radio until it restarts." {
		t.Errorf("state %q", se.State)
	}
	// set tx, then the escape: two 5 s waits.
	if el := clock.Now().Sub(start); el < 10*time.Second || el > 11*time.Second {
		t.Errorf("took %v of virtual time", el)
	}
	if tx := string(log.TX()); !strings.HasSuffix(tx, "set tx 20\r\r") {
		t.Errorf("wire ends %q", tx[max(0, len(tx)-40):])
	}
}

// A node left in region-load mode swallows every line. Probe, which must not change the node, reports it
// (ErrStuck) without sending the bare CR that would end the mode and swap the regions table the node routes with;
// Run, which ends in a reboot, sends it and goes on.
func TestRegionLoadEscape(t *testing.T) {
	n := NewFakeNode(RoleRoomServer, "v1.17.1")
	n.RegionLoad = true
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	p := serialport.NewFake(n, clock, log, serialport.Options{})
	start := clock.Now()
	if _, err := Probe(context.Background(), p, clock); !errors.Is(err, ErrStuck) || errors.Is(err, ErrNotCLI) {
		t.Fatalf("Probe: %v", err)
	}
	if el := clock.Now().Sub(start); el > 2500*time.Millisecond {
		t.Errorf("Probe took %v", el)
	}
	if tx := string(log.TX()); tx != "ver\rver\r" || !n.RegionLoad {
		t.Errorf("Probe sent %q; region load still on: %v", tx, n.RegionLoad)
	}
	// ReadSettings and Confirm don't end it either: they time out.
	info := Info{Version: n.Version, Role: RoleRoomServer}
	if _, err := ReadSettings(context.Background(), p, clock, info); !errors.Is(err, ErrNoReply) || !n.RegionLoad {
		t.Errorf("ReadSettings: %v; region load still on: %v", err, n.RegionLoad)
	}
	if err := Confirm(context.Background(), p, clock, Want{Version: n.Version, Radio: DefaultRadio}); !errors.Is(err, ErrStuck) || !n.RegionLoad {
		t.Errorf("Confirm: %v; region load still on: %v", err, n.RegionLoad)
	}
	if strings.Contains(string(log.TX()), "\r\r") {
		t.Errorf("a bare CR was sent: %q", log.TX())
	}
	// In the middle of a setup, the escape ends it and the command is sent again.
	s := goodSettings()
	s.GuestPassword = "guest-42"
	steps, err := Plan(s, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), p, clock, steps, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(log.TX()), "set name Hilltop 2\r\rset name Hilltop 2\r") {
		t.Error("no escape on the wire")
	}
	if n.Name != "Hilltop 2" {
		t.Errorf("name %q", n.Name)
	}
}

// A cancelled context stops at once.
func TestCancel(t *testing.T) {
	n := NewFakeNode(RoleRepeater, "v1.18.0")
	clock := serialport.NewVirtualClock(nil)
	p := serialport.NewFake(n, clock, nil, serialport.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Probe(ctx, p, clock); !errors.Is(err, context.Canceled) {
		t.Errorf("Probe: %v", err)
	}
	steps, _ := Plan(goodSettings(), Info{Version: n.Version, Role: RoleRepeater})
	err := Run(ctx, p, clock, steps, nil)
	var se *StepError
	if !errors.As(err, &se) || !errors.Is(err, context.Canceled) || se.Reason != "stopped." {
		t.Errorf("Run: %v", err)
	}
}

// A board pulled out mid-setup fails the step with serialport.ErrGone.
func TestUnplugged(t *testing.T) {
	n := NewFakeNode(RoleRepeater, "v1.18.0")
	clock := serialport.NewVirtualClock(nil)
	p := serialport.NewFake(n, clock, nil, serialport.Options{})
	steps, _ := Plan(goodSettings(), Info{Version: n.Version, Role: RoleRepeater})
	p.Unplug()
	err := Run(context.Background(), p, clock, steps, nil)
	if !errors.Is(err, serialport.ErrGone) || !strings.Contains(err.Error(), "went away") {
		t.Errorf("Run: %v", err)
	}
}

// cutShort echoes each line and starts a reply it never finishes: a node that is answering, not one swallowing
// lines in region-load mode.
type cutShort struct{}

func (cutShort) Receive(b []byte) []byte              { return append(append([]byte(nil), b...), "\n  -> O"...) }
func (cutShort) Lines(dtr, rts bool, baud int) []byte { return nil }

// The bare CR goes only after a timeout with no "  -> " after the echo (7.10): a reply that started and never
// ended means the node is answering, and the step fails after one wait with nothing more sent.
func TestEscapeOnlyWithoutReply(t *testing.T) {
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	p := serialport.NewFake(cutShort{}, clock, log, serialport.Options{})
	start := clock.Now()
	err := Run(context.Background(), p, clock, []Step{{Command: "set tx 20", Expect: exact("OK"), Display: "TX power: 20 dBm"}}, nil)
	if !errors.Is(err, ErrNoReply) {
		t.Fatalf("Run: %v", err)
	}
	if tx := string(log.TX()); tx != "set tx 20\r" {
		t.Errorf("wire %q", tx)
	}
	if el := clock.Now().Sub(start); el > ReplyTimeout+time.Second {
		t.Errorf("took %v", el)
	}
}

// pullOut is a FakeNode whose board is pulled out of the USB port as soon as it has handled a given command, before
// its reply leaves: the setting may be saved, and the host never hears.
type pullOut struct {
	*FakeNode
	after string
	port  *serialport.Fake
}

func (d *pullOut) Receive(b []byte) []byte {
	out := d.FakeNode.Receive(b)
	if c := d.Commands(); len(c) > 0 && strings.HasPrefix(c[len(c)-1], d.after) {
		d.port.Unplug()
	}
	return out
}

// A board pulled out mid-setup fails the step with ErrGone, and the error says what the node is left with: nothing
// changed, the settings sent so far, perhaps the one in flight, and the old radio until it restarts.
func TestUnpluggedMidSetup(t *testing.T) {
	const (
		before   = "What was sent before this step is saved on the node; run setup again to finish."
		oldRadio = " It keeps its old radio until it restarts."
	)
	for _, c := range []struct {
		after       string
		inFlight    bool // pulled out before the reply to after leaves, not between commands
		step, state string
	}{
		{"", false, "Name: Hilltop 2", "Nothing on the node was changed."},
		{"set name", false, "Radio: 910.525 MHz, 62.5 kHz, SF7, CR5", before},
		{"set radio", false, "TX power: 20 dBm", before + oldRadio},
		{"password", false, "Check name", "Every setting was sent to the node; run setup again to check them." + oldRadio},
		{"get tx", false, "Reboot to start the new radio", "Every setting was sent, saved and checked, but the node didn't get the reboot." + oldRadio},
		{"set name", true, "Name: Hilltop 2", "This step's setting may have been saved on the node; nothing else was changed. Run setup again to finish."},
		{"set radio", true, "Radio: 910.525 MHz, 62.5 kHz, SF7, CR5", "What was sent before this step is saved on the node, and this step's setting may be too; run setup again to finish." + oldRadio},
	} {
		n := NewFakeNode(RoleRepeater, "v1.18.0")
		clock := serialport.NewVirtualClock(nil)
		var port *serialport.Fake
		if c.inFlight {
			d := &pullOut{FakeNode: n, after: c.after}
			port = serialport.NewFake(d, clock, nil, serialport.Options{})
			d.port = port
		} else {
			port = serialport.NewFake(n, clock, nil, serialport.Options{})
			// Pulled out at the next tick after the node handled after: the 100 ms gap before the next command.
			clock.OnTick(func(time.Time) {
				if cmds := n.Commands(); c.after == "" || len(cmds) > 0 && strings.HasPrefix(cmds[len(cmds)-1], c.after) {
					port.Unplug()
				}
			})
		}
		if c.after == "" {
			port.Unplug()
		}
		steps, err := Plan(goodSettings(), Info{Version: n.Version, Role: RoleRepeater})
		if err != nil {
			t.Fatal(err)
		}
		err = Run(context.Background(), port, clock, steps, nil)
		var se *StepError
		if !errors.As(err, &se) || !errors.Is(err, serialport.ErrGone) {
			t.Errorf("after %q (in flight %v): %v", c.after, c.inFlight, err)
			continue
		}
		if se.Display != c.step || se.State != c.state {
			t.Errorf("after %q (in flight %v): stopped at %q, state %q", c.after, c.inFlight, se.Display, se.State)
		}
		if strings.Contains(se.State+se.Reason, adminPW) {
			t.Error("a password in the error")
		}
	}
	// A refusal is not "may have been saved": the node said no.
	r := newRig(RoleRepeater, "v1.12.0")
	err := Run(context.Background(), r.port, r.clock, []Step{{Command: "set name a/b", Expect: exact("OK"), Display: "Name: a/b"}}, nil)
	var se *StepError
	if !errors.As(err, &se) || se.State != "Nothing on the node was changed." {
		t.Errorf("refused: %v, state %q", err, se.State)
	}
}
