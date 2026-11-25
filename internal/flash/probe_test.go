package flash_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/nodecli"
	"meshflash/internal/usbid"
)

func TestProbeRepeater(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", "Hilltop 2"))
	r := newRig(t, v4)
	n := r.b.Probe(context.Background(), r.port(v4))
	if n.Problem != flash.NoProblem || n.Info.Role != nodecli.RoleRepeater || n.Info.Version.String() != "v1.17.1" ||
		n.Info.Board != "Heltec V4" || !n.HasCurrent || n.Current.Name != "Hilltop 2" {
		t.Fatalf("%+v", n)
	}
	for _, c := range v4.Node().Commands() {
		if c != "ver" && c != "board" && !strings.HasPrefix(c, "get ") {
			t.Errorf("a probe sent %q", c)
		}
	}
}

// A room server's name is read as a repeater's settings, so it is never asked its guest password (owner question 1).
func TestProbeNeverAsksGuestPassword(t *testing.T) {
	v4 := fakeworld.HeltecV4(fakeworld.Firmware{Role: nodecli.RoleRoomServer, Version: "v1.18.0", Name: "Hall"})
	r := newRig(t, v4)
	n := r.b.Probe(context.Background(), r.port(v4))
	if n.Problem != flash.NoProblem || n.Info.Role != nodecli.RoleRoomServer || !n.HasCurrent ||
		n.Current.Name != "Hall" || n.Current.GuestPassword != "" {
		t.Fatalf("%+v", n)
	}
	cmds := v4.Node().Commands()
	if slices.Contains(cmds, "get guest.password") || !slices.Contains(cmds, "get name") {
		t.Errorf("commands %d, the guest password asked or the name not", len(cmds))
	}
}

func TestProbeProblems(t *testing.T) {
	for _, tc := range []struct {
		name  string
		board func() *fakeworld.Board
		set   func(*fakeworld.World, *fakeworld.Board)
		want  flash.Problem
		why   string
	}{
		{"companion silent", func() *fakeworld.Board { return fakeworld.RAK4631(fakeworld.Firmware{Role: "companion"}) },
			nil, flash.Silent, ""},
		{"stuck", func() *fakeworld.Board {
			return fakeworld.HeltecV4(fakeworld.Firmware{Role: nodecli.RoleRepeater, Version: "v1.17.1", Stuck: true})
		}, nil, flash.Stuck, "press its reset button"},
		{"no permission", func() *fakeworld.Board { return fakeworld.HeltecV4(repeater("v1.17.1", "")) },
			func(w *fakeworld.World, _ *fakeworld.Board) { w.SetDialout(false) }, flash.NoPermission, ""},
		{"busy", func() *fakeworld.Board { return fakeworld.HeltecV4(repeater("v1.17.1", "")) },
			func(_ *fakeworld.World, b *fakeworld.Board) { b.SetBusy(true) }, flash.Busy, ""},
		{"gone", func() *fakeworld.Board { return fakeworld.HeltecV4(repeater("v1.17.1", "")) },
			func(w *fakeworld.World, b *fakeworld.Board) { w.Unplug(b) }, flash.Gone, ""},
		{"ZephCore", func() *fakeworld.Board { return fakeworld.RAK4631(repeater("1.18.2-zephcore", "")) }, nil,
			flash.NotSettable, "meshflash sets up only MeshCore's own repeater and room-server firmware."},
		// Worded to follow the Board's "Running repeater v1.10.0: ", so it doesn't say the version again.
		{"too old", func() *fakeworld.Board { return fakeworld.HeltecV4(repeater("v1.10.0", "")) }, nil,
			flash.NotSettable, "flash v1.11.0 or newer to set it up here."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.board()
			r := newRig(t, b)
			port := r.port(b)
			if tc.set != nil {
				tc.set(r.w, b)
			}
			n := r.b.Probe(context.Background(), port)
			if n.Problem != tc.want || !strings.Contains(n.Reason, tc.why) {
				t.Fatalf("got %+v", n)
			}
			if tc.want == flash.NotSettable && n.Info.Version.Raw == "" {
				t.Errorf("no Info: %+v", n)
			}
		})
	}
}

// TestProbeZephCoreNotSettable is the ZephCore rule on its own, in ReadNode too: a ver naming ZephCore is never set
// up (4.1).
func TestProbeZephCoreNotSettable(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("1.18.2-zephcore", ""))
	r := newRig(t, v4)
	port := r.port(v4)
	for name, n := range map[string]flash.Node{
		"probe":    r.b.Probe(context.Background(), port),
		"readnode": r.b.ReadNode(context.Background(), port, false),
	} {
		if n.Problem != flash.NotSettable || !strings.HasPrefix(n.Reason, "meshflash sets up only MeshCore's own") ||
			n.HasCurrent {
			t.Errorf("%s: %+v", name, n)
		}
	}
	// And Setup refuses it whatever it is given.
	p := wait(t, r.b.Setup(context.Background(), flash.SetupRequest{Node: r.b.ReadNode(context.Background(), port,
		false), Settings: goodSettings()}))
	if p.Outcome.OK || p.Outcome.State != "Nothing on the node was changed." {
		t.Errorf("%s", outcome(p.Outcome))
	}
	for _, c := range v4.Node().Commands() {
		if strings.HasPrefix(c, "set ") || strings.HasPrefix(c, "password") {
			t.Errorf("sent a setting to ZephCore")
		}
	}
}

// A bootloader is answered without opening it: ready, a stale session, or one not positively identified.
func TestProbeBootloaders(t *testing.T) {
	t.Run("seen to appear", func(t *testing.T) {
		w := fakeworld.New()
		r := newRigOver(t, w, false)
		r.b.Scan()
		rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
		rak.DoublePressReset()
		w.Plug(rak)
		if n := r.b.Probe(context.Background(), r.port(rak)); n.Problem != flash.InBootloader || n.Reason != "" {
			t.Errorf("%+v", n)
		}
		if o := w.Opens(); len(o) != 0 {
			t.Errorf("opened %q", o)
		}
	})
	t.Run("there at the first scan", func(t *testing.T) {
		rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
		rak.DoublePressReset()
		r := newRig(t, rak)
		n := r.b.Probe(context.Background(), r.port(rak))
		if n.Problem != flash.InBootloader ||
			n.Reason != "It was already in its bootloader when meshflash started: meshflash asks you to press reset twice quickly when you flash it." {
			t.Errorf("%+v", n)
		}
		if o := r.w.Opens(); len(o) != 0 {
			t.Errorf("opened %q", o)
		}
	})
	t.Run("unconfirmed", func(t *testing.T) {
		r := newRig(t)
		// A RAK bootloader's identity with no "nRF Serial" interface string: usbid infers it from the VID:PID.
		p := flash.PortOf(usbid.Port{Name: "ttyACM0", Dev: "/dev/ttyACM0", VID: 0x239a, PID: 0x0029,
			Product: "WisBlock RAK4631", Serial: "D2A1F3C4B5E69788", Driver: "cdc_acm", USBPath: "1-1.3"})
		if p.Kind != usbid.NRFBootloader || p.InBootloader() {
			t.Fatalf("port %+v", p)
		}
		n := r.b.Probe(context.Background(), p)
		if n.Problem != flash.InBootloader ||
			n.Reason != "Looks like its bootloader, but meshflash can't confirm it: press reset twice quickly." {
			t.Errorf("%+v", n)
		}
	})
}

// A cancelled probe gives up within 100 ms of the world's time and leaves the port closed.
func TestProbeCancelled(t *testing.T) {
	rak := fakeworld.RAK4631(fakeworld.Firmware{Role: "companion"})
	r := newRig(t, rak)
	port := r.port(rak)
	ctx, cancel := context.WithCancel(context.Background())
	start := r.w.Clock().Now()
	r.w.At(500*time.Millisecond, cancel)
	n := r.b.Probe(ctx, port)
	if n.Problem != flash.Failed || n.Reason != "stopped" {
		t.Errorf("%+v", n)
	}
	if took := r.w.Clock().Now().Sub(start); took > 700*time.Millisecond {
		t.Errorf("took %v", took)
	}
}

// ReadNode reads every setting, the room server's guest password included (the form's default), and waits longer
// on a first boot.
func TestReadNode(t *testing.T) {
	v4 := fakeworld.HeltecV4(fakeworld.Firmware{Role: nodecli.RoleRoomServer, Version: "v1.18.0", Name: "Hall"})
	r := newRig(t, v4)
	n := r.b.ReadNode(context.Background(), r.port(v4), false)
	if n.Problem != flash.NoProblem || !n.HasCurrent || n.Current.Name != "Hall" || string(n.Current.GuestPassword) != "hello" ||
		n.Current.Radio != nodecli.DefaultRadio.Clean() {
		t.Fatalf("%+v", n)
	}

	rak := fakeworld.RAK4631(fakeworld.Firmware{Role: "companion"})
	r = newRig(t, rak)
	for _, tc := range []struct {
		first bool
		at    time.Duration
	}{{false, nodecli.QuickProbe}, {true, flash.FirstBootWait}} {
		start := r.w.Clock().Now()
		n := r.b.ReadNode(context.Background(), r.port(rak), tc.first)
		took := r.w.Clock().Now().Sub(start)
		if n.Problem != flash.Silent || took < tc.at || took > tc.at+time.Second {
			t.Errorf("first boot %v: %+v after %v", tc.first, n.Problem, took)
		}
	}
}

// The ports as Scan gives them, and the dialout hint.
func TestScan(t *testing.T) {
	v4, v3, rak := fakeworld.HeltecV4(repeater("v1.17.1", "")), fakeworld.HeltecV3(repeater("v1.17.1", "")),
		fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, v3, v4, rak)
	s, err := r.b.Scan()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range s.Ports {
		names = append(names, p.Name+" "+p.Kind.String()+" "+p.Label+" "+p.Key)
	}
	want := []string{"ttyACM0 esp-usb-jtag USB JTAG/serial debug unit 6055F9C02718",
		"ttyACM1 nrf-app WisCore RAK4631 Board D2A1F3C4B5E69788", "ttyUSB0 esp-bridge CP210x "}
	if !slices.Equal(names, want) || !s.Dialout || s.Hint != "" {
		t.Errorf("ports %q, dialout %v %q", names, s.Dialout, s.Hint)
	}
	r.w.SetDialout(false)
	if s, _ := r.b.Scan(); s.Dialout || s.Hint != usbid.DialoutHint {
		t.Errorf("dialout %v %q", s.Dialout, s.Hint)
	}
}
