package nodecli

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

var updateStreams = flag.Bool("update-streams", false, "write testdata/configtool/streams.json for record.mjs")

// ctCase is one byte stream fed to config.meshcore.io's SerialCLI in Node (testdata/configtool/record.mjs) and to
// findReply here.
type ctCase struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
	RX   string `json:"rx"` // hex: what the node sent back
	// Departure says why findReply rightly differs from the config tool here (see the package comment).
	Departure string `json:"departure,omitempty"`
	// From the recording.
	TX       string  `json:"tx,omitempty"` // hex: what SerialCLI wrote
	Whole    *string `json:"whole"`        // its reply, the stream fed whole; null: it never resolved
	Bytewise *string `json:"bytewise"`     // its reply, the stream fed a byte at a time
}

// configToolStreams are the cases: FakeNode's answers to the commands setup sends, on both roles and on versions
// whose answers differ (a blank guest password, a negative TX power, no path.hash.mode before v1.14.0), with a boot
// banner and a log line in the way; the region-load escape; and a stale reply ahead of the echo.
func configToolStreams() []ctCase {
	var cases []ctCase
	reply := func(n *FakeNode, line string) string { return hex.EncodeToString([]byte(talk(n, line+"\r"))) }
	cmds := []string{
		"ver", "get role", "board", "get name", "set name Hilltop 2", "get name", "set radio 869.618,62.5,8,8",
		"get radio", "set tx -5", "get tx", "get path.hash.mode", "set path.hash.mode 1", "get guest.password",
		"set lat 51.5074", "get lat", "set advert.interval 120", "get advert.interval", "time 1795953600",
	}
	// No password command is recorded: a golden never holds one, even a made-up one (CLAUDE.md). Their replies are
	// parsed like any other ("password now: …", "OK").
	for _, node := range []struct{ role, ver string }{
		{RoleRepeater, "v1.18.0"}, {RoleRoomServer, "v1.18.0"}, {RoleRepeater, "v1.11.0"}, {RoleRoomServer, "v1.13.0"},
	} {
		n := NewFakeNode(node.role, node.ver)
		n.GuestPassword = "" // a blank value reads back as "> "
		for _, cmd := range cmds {
			cases = append(cases, ctCase{Name: node.role + " " + node.ver + ": " + cmd, Cmd: cmd, RX: reply(n, cmd)})
		}
	}
	for _, role := range []string{RoleRepeater, RoleRoomServer} {
		n := NewFakeNode(role, "v1.17.1")
		n.Banner = true
		n.Noise = []byte("DEBUG: radio started\r\n")
		cases = append(cases, ctCase{Name: role + ": banner and a log line", Cmd: "ver", RX: reply(n, "ver")})
		n = NewFakeNode(role, "v1.17.1")
		n.RegionLoad = true
		c := ctCase{Name: role + ": region-load escape", Cmd: "", RX: reply(n, "")}
		if role == RoleRoomServer {
			c.Departure = "the room server echoes a bare CR without LF, so the config tool's search for CR LF first " +
				"finds the reply's own line end and it never resolves; findReply looks for the command and its CR " +
				"(the config tool never sends a bare CR)"
		}
		cases = append(cases, c)
	}
	n := NewFakeNode(RoleRepeater, "v1.18.0")
	cases = append(cases, ctCase{
		Name: "a stale reply ahead of the echo", Cmd: "get tx",
		RX: hex.EncodeToString([]byte("  -> OK\r\n")) + reply(n, "get tx"),
		Departure: "fed a byte at a time, the config tool takes the first reply it sees when no echo has come yet; " +
			"findReply requires the echo, so it never takes an earlier command's reply",
	})
	return cases
}

// The Go parser against MeshCore's own setup tool, recorded in Node: the same bytes written, the same reply taken.
func TestConfigTool(t *testing.T) {
	dir := filepath.Join("testdata", "configtool")
	cases := configToolStreams()
	if *updateStreams {
		b, _ := json.MarshalIndent(cases, "", " ")
		if err := os.WriteFile(filepath.Join(dir, "streams.json"), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skip("wrote streams.json; now run record.mjs (testdata/configtool/README.md)")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden []ctCase
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden) != len(cases) {
		t.Fatalf("golden has %d cases, the test %d: re-record (testdata/configtool/README.md)", len(golden), len(cases))
	}
	for i, g := range golden {
		c := cases[i]
		if g.Name != c.Name || g.Cmd != c.Cmd || g.RX != c.RX || g.Departure != c.Departure {
			t.Errorf("%s: the fake's stream changed since the recording: re-record (testdata/configtool/README.md)", c.Name)
			continue
		}
		// The same bytes on the wire.
		if want := hex.EncodeToString([]byte(c.Cmd + "\r")); g.TX != want {
			t.Errorf("%s: the config tool wrote %s, nodecli writes %s", c.Name, g.TX, want)
		}
		rx, _ := hex.DecodeString(c.RX)
		ours, used, ok, _ := findReply(rx, c.Cmd)
		// findReply takes the same reply however the bytes arrive: the first prefix of the stream that completes a
		// reply gives the whole stream's answer.
		for k := 1; k <= len(rx); k++ {
			if r, u, ok2, _ := findReply(rx[:k], c.Cmd); ok2 {
				if r != ours || u != used {
					t.Errorf("%s: a byte at a time gives %q, whole %q", c.Name, r, ours)
				}
				break
			}
		}
		// The config tool trims its replies; nodecli keeps the text as sent (a blank value is "> ").
		got := strings.TrimSpace(ours)
		same := func(theirs *string) bool { return ok && theirs != nil && *theirs == got || !ok && theirs == nil }
		if c.Departure == "" {
			if !same(g.Whole) || !same(g.Bytewise) {
				t.Errorf("%s: nodecli takes %q (ok=%v), the config tool %s whole and %s a byte at a time", c.Name, got, ok, show(g.Whole), show(g.Bytewise))
			}
			continue
		}
		if same(g.Whole) && same(g.Bytewise) {
			t.Errorf("%s: no longer a departure: both take %q", c.Name, got)
		}
		if !ok {
			t.Errorf("%s: nodecli takes no reply", c.Name)
		}
	}
	// And through nodecli's own write-and-wait (conn.exchange), not only its parser: the bytes it writes are the
	// bytes the config tool wrote, and the reply it returns, fed the stream whole and a byte at a time, is the one
	// the config tool took (trimmed, as the tool trims).
	for i, g := range golden {
		c := cases[i]
		rx, _ := hex.DecodeString(c.RX)
		for _, bytewise := range []bool{false, true} {
			clock := serialport.NewVirtualClock(nil)
			p := &replayPort{rx: rx, bytewise: bytewise, clock: clock}
			reply, err := newConn(context.Background(), p, clock).exchange(c.Cmd, ReplyTimeout)
			if tx := hex.EncodeToString(p.tx); tx != g.TX {
				t.Errorf("%s: nodecli wrote %s, the config tool %s", c.Name, tx, g.TX)
			}
			theirs := g.Whole
			if bytewise {
				theirs = g.Bytewise
			}
			got := strings.TrimSpace(reply)
			same := err == nil && theirs != nil && *theirs == got || errors.Is(err, ErrNoReply) && theirs == nil
			if same == (c.Departure != "" && (bytewise || c.Cmd == "")) {
				t.Errorf("%s (byte-wise %v): nodecli %q (%v), the config tool %s", c.Name, bytewise, got, err, show(theirs))
			}
			if err != nil && c.Departure == "" {
				t.Errorf("%s: %v", c.Name, err)
			}
		}
	}

	// The stale-reply case: nodecli takes this command's reply, not the earlier "OK".
	last := cases[len(cases)-1]
	rx, _ := hex.DecodeString(last.RX)
	if r, _, _, _ := findReply(rx, last.Cmd); r != "> 22" {
		t.Errorf("stale reply: %q", r)
	}
}

// replayPort answers the first write with a recorded stream, whole or a byte per read, in virtual time.
type replayPort struct {
	rx, tx   []byte
	bytewise bool
	clock    *serialport.VirtualClock
}

func (p *replayPort) Write(b []byte) (int, error) { p.tx = append(p.tx, b...); return len(b), nil }

func (p *replayPort) Read(b []byte, timeout time.Duration) (int, error) {
	if len(p.tx) == 0 || len(p.rx) == 0 {
		p.clock.Advance(timeout)
		return 0, serialport.ErrTimeout
	}
	n := len(p.rx)
	if p.bytewise {
		n = 1
	}
	n = copy(b, p.rx[:n])
	p.rx = p.rx[n:]
	return n, nil
}

func (p *replayPort) SetBaud(int) error { return nil }
func (p *replayPort) SetDTR(bool) error { return nil }
func (p *replayPort) SetRTS(bool) error { return nil }
func (p *replayPort) FlushInput() error { return nil }
func (p *replayPort) Close() error      { return nil }

func show(s *string) string {
	if s == nil {
		return "nothing"
	}
	return `"` + *s + `"`
}
