package nodecli

import (
	"strings"
	"testing"

	"meshflash/internal/serialport"
)

// The read-back numbers of docs/meshcore-flashing.md 7.3-7.4, which the research computed from the same C code.
func TestFtoa(t *testing.T) {
	for in, want := range map[float32]string{
		869.618: "869.6179809", 910.525: "910.5250244", 915.8: "915.7999877", 433.65: "433.6499938",
		917.375: "917.375", 921: "921.0", 0: "0.0", 51.5074: "51.5074005", -0.1278: "-0.1277999", 0.5: "0.5",
	} {
		if got := ftoa(in); got != want {
			t.Errorf("ftoa(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[float32]string{
		62.5: "62.5", 250: "250", 7.8: "7.8", 10.4: "10.4", 15.6: "15.6", 20.8: "20.8", 31.25: "31.25", 41.7: "41.7",
		125: "125", 500: "500", 0: "0",
	} {
		if got := ftoa3(in); got != want {
			t.Errorf("ftoa3(%v) = %q, want %q", in, got, want)
		}
	}
}

// talk writes each line to a fresh fake port on n and returns everything the node sent back.
func talk(n *FakeNode, writes ...string) string {
	clock := serialport.NewVirtualClock(nil)
	p := serialport.NewFake(n, clock, nil, serialport.Options{})
	for _, w := range writes {
		p.Write([]byte(w))
	}
	p.Close()
	return string(p.Log().RX())
}

// The echo rules of docs/meshcore-flashing.md 7.2.
func TestFakeEcho(t *testing.T) {
	rep := NewFakeNode(RoleRepeater, "v1.18.0")
	if got, want := talk(rep, "ver\r"), "ver\r\n  -> v1.18.0-81dbae5 (Build: 15-Oct-2026)\r\n"; got != want {
		t.Errorf("repeater: %q, want %q", got, want)
	}
	// The repeater drops LF and doesn't echo it.
	if got, want := talk(rep, "ver\r\n"), "ver\r\n  -> v1.18.0-81dbae5 (Build: 15-Oct-2026)\r\n"; got != want {
		t.Errorf("repeater, CR LF: %q, want %q", got, want)
	}
	// Two commands in one write are two lines on the repeater: it stops reading at CR.
	if got := talk(rep, "get role\rboard\r"); got != "get role\r\n  -> > repeater\r\nboard\r\n  -> Heltec V3\r\n" {
		t.Errorf("repeater, two lines: %q", got)
	}
	room := NewFakeNode(RoleRoomServer, "v1.18.0")
	if got, want := talk(room, "ver\r"), "ver\r  -> v1.18.0-81dbae5 (Build: 15-Oct-2026)\r\n"; got != want {
		t.Errorf("room server: %q, want %q", got, want)
	}
	if got, want := talk(room, "ver\r\n"), "ver\r\n  -> v1.18.0-81dbae5 (Build: 15-Oct-2026)\r\n"; got != want {
		t.Errorf("room server, CR LF: %q, want %q", got, want)
	}
	// The room server doesn't stop at CR: "a\rb\r" in one write is one command "a\rb", here "get role\rboard",
	// which memcmp matches as get role.
	room.commands = nil
	if got := talk(room, "get role\rboard\r"); got != "get role\rboard\r  -> > room_server\r\n" {
		t.Errorf("room server, two lines at once: %q", got)
	}
	if c := room.Commands(); len(c) != 1 || c[0] != "get role\rboard" {
		t.Errorf("room server handled %q", c)
	}
	// Leading spaces are skipped, and the "xx|" tag is reflected (MyMesh.cpp:1233-1239).
	if got := talk(rep, "  01|get role\r"); !strings.HasSuffix(got, "  -> 01|> repeater\r\n") {
		t.Errorf("tagged command: %q", got)
	}
	// A line ending only in LF is never handled.
	if got := talk(NewFakeNode(RoleRepeater, "v1.18.0"), "ver\n"); got != "ver" {
		t.Errorf("LF only: %q", got)
	}
}

// What each version answers differently (docs/meshcore-flashing.md 7.5).
func TestFakeVersions(t *testing.T) {
	ask := func(ver, role string, setup func(*FakeNode), line string) string {
		n := NewFakeNode(role, ver)
		if setup != nil {
			setup(n)
		}
		out := talk(n, line+"\r")
		i := strings.Index(out, replyMark)
		if i < 0 {
			return "(no reply)"
		}
		return strings.TrimSuffix(out[i+len(replyMark):], "\r\n")
	}
	minus5 := func(n *FakeNode) { n.TxPower = -5 }
	for _, c := range []struct {
		ver, line string
		setup     func(*FakeNode)
		want      string
	}{
		{"v1.11.0", "get tx", minus5, "> 251"},
		{"v1.12.0", "get tx", minus5, "> 251"},
		{"v1.13.0", "get tx", minus5, "> -5"},
		{"v1.17.1", "get tx", minus5, "> -5"},
		{"v1.18.0", "get tx", minus5, "> 251"},
		{"v1.13.0", "get path.hash.mode", nil, "??: path.hash.mode"},
		{"v1.13.0", "set path.hash.mode 1", nil, "unknown config: path.hash.mode 1"},
		{"v1.14.0", "get path.hash.mode", nil, "> 0"},
		{"v1.14.0", "set path.hash.mode 3", nil, "Error, must be 0,1, or 2"},
		{"v1.14.0", "set radio 200,62.5,8,5", nil, "Error, invalid radio params"},
		{"v1.15.0", "set radio 200,62.5,8,5", nil, "OK - reboot to apply"},
		{"v1.11.0", "set name a/b:c", nil, "OK"},
		{"v1.12.0", "set name a/b", nil, "Error, bad chars"},
		{"v1.13.0", "set name a/b", nil, "OK"},
		{"v1.13.0", "set name a:b", nil, "Error, bad chars"},
		{"v1.18.0", "set name ", nil, "OK"},                            // an empty name is accepted on every version
		{"v1.17.1", "get radio ", nil, "> 869.6179809,62.5,8,5"},       // prefix match
		{"v1.18.0", "get radio ", nil, "??: radio "},                   // exact match
		{"v1.14.0", "get radio.rxgain", nil, "> 869.6179809,62.5,8,5"}, // prefix match: no such key yet
		{"v1.17.1", "get radio.rxgain", nil, "> off"},                  // its own key from v1.14.1
		{"v1.18.0", "get radio", nil, "> 869.6179809,62.5,8,5"},
		{"v1.18.0", "set radio 910.525,62.5.0,7,5", nil, "OK - reboot to apply"}, // the config tool's "62.5.0"
		{"v1.18.0", "set advert.interval 30", nil, "Error: interval range is 60-240 minutes"},
		{"v1.18.0", "get advert.interval", nil, "> 2"},
		{"v1.18.0", "password abc", nil, "password now: abc"},
		{"v1.18.0", "password 0123456789abcdefXYZ", nil, "password now: 0123456789abcde"},
		{"v1.18.0", "time 1000", nil, "(ERR: clock cannot go backwards)"},
		{"v1.18.0", "time 1795953600", nil, "OK - clock set: 12:00 - 29/11/2026 UTC"},
		{"v1.18.0", "frobnicate", nil, "Unknown command"},
		{"v1.18.0", "get nothing", nil, "??: nothing"},
		{"v1.18.0", "reboot", nil, "(no reply)"},
	} {
		if got := ask(c.ver, RoleRepeater, c.setup, c.line); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.ver, c.line, got, c.want)
		}
	}
	// The first save turns a new node's 2-minute local advert off (CommonCLI.cpp:160-168).
	n := NewFakeNode(RoleRepeater, "v1.18.0")
	talk(n, "set lat 1\r")
	if n.AdvertInterval != 0 {
		t.Errorf("advert interval after the first save: %d", n.AdvertInterval)
	}
}

// Reboot: no reply, nothing until the port is opened again; then the stored radio is the one the node runs, and
// with Banner it prints its ID.
func TestFakeReboot(t *testing.T) {
	n := NewFakeNode(RoleRoomServer, "v1.18.0")
	n.Banner = true
	clock := serialport.NewVirtualClock(nil)
	p := serialport.NewFake(n, clock, nil, serialport.Options{})
	p.Write([]byte("set radio 915.8,250,10,5\r"))
	p.Write([]byte("reboot\r"))
	before := len(p.Log().RX())
	p.Write([]byte("ver\r"))
	if len(p.Log().RX()) != before {
		t.Error("the node answered while rebooting")
	}
	if n.ActiveRadio.FreqMHz != float64(float32(869.618)) || n.Reboots() != 1 {
		t.Errorf("before the reopen: running %+v, %d reboots", n.ActiveRadio, n.Reboots())
	}
	p.Close()
	p = serialport.NewFake(n, clock, nil, serialport.Options{})
	p.Write([]byte("ver\r"))
	got := string(p.Log().RX())
	if !strings.HasPrefix(got, "Room ID: "+n.PublicKey+"\r\nver\r  -> v1.18.0") {
		t.Errorf("after the reopen: %q", got)
	}
	if want := (Radio{float64(float32(915.8)), 250, 10, 5}); n.ActiveRadio != want {
		t.Errorf("running %+v after the reboot, want %+v", n.ActiveRadio, want)
	}
}

// A full line buffer: v1.18.0 cuts the line to 158 characters (repeater) and handles it; older versions never
// handle it (undefined behaviour in the firmware, a node that stops answering here).
func TestFakeFullBuffer(t *testing.T) {
	long := "set name " + strings.Repeat("x", 200) + "\r"
	n := NewFakeNode(RoleRepeater, "v1.18.0")
	out := talk(n, long)
	if c := n.Commands(); len(c) < 1 || len(c[0]) != 158 {
		t.Errorf("v1.18.0 handled %d commands, the first %d long", len(c), len(c[0]))
	}
	if !strings.Contains(out, "  -> OK\r\n") {
		t.Errorf("v1.18.0: %q", out)
	}
	old := NewFakeNode(RoleRepeater, "v1.17.1")
	talk(old, long)
	if got := talk(old, "ver\r"); got != "" {
		t.Errorf("v1.17.1 after a full buffer answered %q", got)
	}
}
