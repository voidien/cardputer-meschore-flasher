package flash_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/usbid"
)

// TestPortOfAgreesWithSameBoard: rules.go keeps its own copy of usbid's "useful serial" rule (unexported there), so
// a Key is set exactly when SameBoard would follow the board by its serial: two ports with Keys are the same board
// when their Keys are equal, and a port without one is followed by SameBoard only as a bridge in its socket.
func TestPortOfAgreesWithSameBoard(t *testing.T) {
	ports := []usbid.Port{
		{VID: 0x303a, PID: 0x1001, Product: "USB JTAG/serial debug unit", Serial: "60:55:F9:C0:27:18", USBPath: "1-1.3"},
		{VID: 0x303a, PID: 0x0002, Product: "heltec_tracker v2", Serial: "6055F9C02718", Interface: "TinyUSB CDC", USBPath: "1-1.4"},
		{VID: 0x239a, PID: 0x8029, Product: "WisCore RAK4631 Board", Serial: "D2A1F3C4B5E69788", Interface: "TinyUSB Serial", USBPath: "1-1.3"},
		{VID: 0x239a, PID: 0x002a, Product: "WisBlock RAK4631", Serial: "d2a1f3c4b5e69788", Interface: "nRF Serial", USBPath: "1-1.2"},
		{VID: 0x2886, PID: 0x8044, Product: "XIAO nRF52840", Serial: "8A4C2E1F0B3D5A69", Interface: "TinyUSB Serial", USBPath: "1-1.2"},
		{VID: 0x10c4, PID: 0xea60, Product: "CP2102", Serial: "0001", Driver: "cp210x", USBPath: "1-1.2"},
		{VID: 0x10c4, PID: 0xea60, Product: "CP2102", Serial: "0001", Driver: "cp210x", USBPath: "1-1.4"},
		{VID: 0x1a86, PID: 0x7523, Product: "USB2.0-Serial", Driver: "ch341", USBPath: "1-1.4"},
		{VID: 0x1a86, PID: 0x7523, Product: "USB2.0-Serial", Serial: "0000", Driver: "ch341", USBPath: "1-1.4"},
		{VID: 0x10c4, PID: 0xea60, Product: "CP2104", Serial: "01A2B3C4", Driver: "cp210x", USBPath: "1-1.2"},
	}
	for i, a := range ports {
		for j, b := range ports {
			pa, pb := flash.PortOf(a), flash.PortOf(b)
			same := usbid.SameBoard(a, b)
			switch {
			case pa.Key != "" && pb.Key != "":
				if (pa.Key == pb.Key) != same {
					t.Errorf("ports %d and %d: Keys %q and %q, SameBoard %v", i, j, pa.Key, pb.Key, same)
				}
			case pa.Key != "" || pb.Key != "":
				if same {
					t.Errorf("ports %d and %d: one Key, yet SameBoard", i, j)
				}
			default:
				bridge := pa.Kind == usbid.EspBridge && pb.Kind == usbid.EspBridge
				want := bridge && a.VID == b.VID && a.PID == b.PID && a.USBPath == b.USBPath
				if same != want {
					t.Errorf("ports %d and %d: no Keys, SameBoard %v", i, j, same)
				}
			}
		}
	}
}

// One board operation at a time: a probe made while a flash runs waits for it, and the world never sees a second
// port opened on the board while one is.
func TestOneBoardOperationAtATime(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	probed := make(chan flash.Node, 1)
	// A second into the flash (during the touch, on the flash's own goroutine) the screens ask for a probe.
	r.w.At(time.Second, func() {
		go func() { probed <- r.b.Probe(context.Background(), req.Port) }()
	})
	j := r.b.Flash(context.Background(), req)
	var n flash.Node
	select {
	case n = <-probed:
	case <-time.After(jobGuard):
		t.Fatal("the probe never returned")
	}
	select {
	case <-j.Done():
	default:
		t.Fatal("the probe returned while the flash still ran")
	}
	if p := j.Snapshot(); !p.Outcome.OK {
		t.Fatalf("%s\n%s", outcome(p.Outcome), steps(p))
	}
	if n.Problem != flash.NoProblem || n.Info.Version.String() != "v1.18.0" {
		t.Errorf("probe %+v", n)
	}
	// A probe waiting for the lock gives up when its context ends.
	j = r.b.Flash(context.Background(), req)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n := r.b.Probe(ctx, req.Port); n.Problem != flash.Failed || n.Reason != "stopped" {
		// It may have got the lock before the flash took it; then it answered as a probe does.
		if n.Problem != flash.NoProblem && n.Problem != flash.Gone {
			t.Errorf("cancelled probe %+v", n)
		}
	}
	wait(t, j)
}

// A board not plugged in when the flash starts: nothing sent, and how to go on.
func TestFlashBoardNotFound(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
	r.save(req)
	r.w.Unplug(v4)
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Not flashed" || o.Summary[0] != "The board isn't plugged in." ||
		o.State != "Nothing was sent to the board: it still has its old firmware." || o.Recovery != "Plug it in and press r." ||
		!o.Retry || o.Board != nil {
		t.Fatalf("%s", outcome(o))
	}
	// Plugged into another socket under another tty, it is found by its serial number.
	r.w.HoldTty("ttyACM0", true)
	r.w.Plug(v4)
	if p := r.flash(req); !p.Outcome.OK || p.Outcome.Board.Name != "ttyACM1" {
		t.Errorf("%s", outcome(p.Outcome))
	}
}

// No word the backend writes to screen.log names a Go package or holds a step's command.
func TestLogWords(t *testing.T) {
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, rak)
	req := rakRepeater(t, r, rak)
	r.save(req)
	rak.NextBootloader("mute-from:20")
	p := r.flash(req)
	if p.Outcome.Log == "" || !strings.Contains(r.log(), p.Outcome.Log) {
		t.Fatalf("log %q, screen.log %q", p.Outcome.Log, r.log())
	}
	for _, w := range []string{"nrfdfu:", "esp:", "nodecli:", "serial:"} {
		if strings.Contains(r.log(), w) {
			t.Errorf("screen.log has %q: %q", w, r.log())
		}
	}
}
