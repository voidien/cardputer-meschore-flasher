package nrfdfu

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

// The recordings in testdata were made by reference/nrf52 (make record): the web flasher's own lib/dfu.js run in
// Node against harness.mjs's fake bootloader. These tests run the Go code against FakeBootloader, its Go port, and
// compare: every byte written, every sleep in order (to 1e-6 ms), and the open at 115200 and the close around them.

// nodeEvent is one line of a recording.
type nodeEvent struct {
	T    string `json:"t"`
	Opts *struct {
		BaudRate int `json:"baudRate"`
	} `json:"opts"`
	Len     int     `json:"len"`
	Hex     string  `json:"hex"`
	Ms      float64 `json:"ms"`
	Message string  `json:"message"`
	// The fake bootloader's view ("bootloader" events).
	Start *struct {
		Mode, SD, BL, App int
	} `json:"start"`
	InitHex  string `json:"init_hex"`
	Stop     bool   `json:"stop"`
	Received int    `json:"received"`
	CRC      int    `json:"crc"`
	Valid    bool   `json:"valid"`
	Warn     string `json:"warn"`
}

func loadRecording(t *testing.T, path string) []nodeEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		r = gz
	}
	var out []nodeEvent
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e nodeEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// step is one thing the host did, in a form both recordings give.
type step struct {
	Kind string // open, tx, sleep, dtr, close
	Baud int
	Hex  string
	Ms   float64
	On   bool
}

func (s step) String() string {
	switch s.Kind {
	case "open":
		return fmt.Sprintf("open %d", s.Baud)
	case "tx":
		return "tx " + describeFrames(s.Hex)
	case "sleep":
		return fmt.Sprintf("sleep %.7f ms", s.Ms)
	case "dtr":
		return fmt.Sprintf("dtr %v", s.On)
	}
	return s.Kind
}

// describeFrames names the HCI frames in hex wire bytes, for failure messages.
func describeFrames(h string) string {
	b, _ := hex.DecodeString(h)
	var names []string
	for _, f := range bytes.Split(b, []byte{slipEnd}) {
		d, err := slipDecode(f)
		if err != nil || len(d) < 6 {
			continue
		}
		kind := map[byte]string{dfuStart: "START", dfuInitPacket: "INIT", dfuData: "DATA", dfuStop: "STOP"}[d[4]]
		names = append(names, fmt.Sprintf("%s(seq %d, %d B)", kind, d[0]&7, len(d)-6))
	}
	return fmt.Sprintf("%d bytes %s", len(b), strings.Join(names, " "))
}

// mergeTX joins consecutive writes: how writes are split is not part of the protocol (serialport.Log merges them
// too).
func mergeTX(in []step) []step {
	var out []step
	for _, s := range in {
		if s.Kind == "tx" && len(out) > 0 && out[len(out)-1].Kind == "tx" {
			out[len(out)-1].Hex += s.Hex
			continue
		}
		out = append(out, s)
	}
	return out
}

// nodeSteps is a recording's open, writes, sleeps and close, consecutive writes joined.
func nodeSteps(evs []nodeEvent) []step { return mergeTX(nodeRaw(evs)) }

// nodeRaw is the same with one write per frame.
func nodeRaw(evs []nodeEvent) []step {
	var out []step
	for _, e := range evs {
		switch e.T {
		case "open":
			out = append(out, step{Kind: "open", Baud: e.Opts.BaudRate})
		case "write":
			out = append(out, step{Kind: "tx", Hex: e.Hex})
		case "sleep":
			out = append(out, step{Kind: "sleep", Ms: e.Ms})
		case "close":
			out = append(out, step{Kind: "close"})
		}
	}
	return out
}

// goSteps is the same from a serialport.Log (what the board sent and notes left out).
func goSteps(log *serialport.Log) []step {
	var out []step
	for _, e := range log.Events() {
		switch e.Ev {
		case "open":
			out = append(out, step{Kind: "open", Baud: e.Baud})
		case "tx":
			out = append(out, step{Kind: "tx", Hex: e.Hex})
		case "sleep":
			out = append(out, step{Kind: "sleep", Ms: e.Ms})
		case "dtr", "rts":
			out = append(out, step{Kind: e.Ev, On: *e.On})
		case "baud":
			out = append(out, step{Kind: "baud", Baud: e.Baud})
		case "close":
			out = append(out, step{Kind: "close"})
		}
	}
	return mergeTX(out)
}

// compareSteps fails the test at the first difference.
func compareSteps(t *testing.T, got, want []step) {
	t.Helper()
	for i := 0; i < len(got) || i < len(want); i++ {
		if i >= len(got) {
			t.Fatalf("step %d: Go stopped; the reference goes on with %v (and %d more)", i, want[i], len(want)-i-1)
		}
		if i >= len(want) {
			t.Fatalf("step %d: Go goes on with %v; the reference had stopped", i, got[i])
		}
		g, w := got[i], want[i]
		same := g.Kind == w.Kind
		switch {
		case !same:
		case g.Kind == "open" || g.Kind == "baud":
			same = g.Baud == w.Baud
		case g.Kind == "tx":
			same = g.Hex == w.Hex
		case g.Kind == "sleep":
			same = math.Abs(g.Ms-w.Ms) <= 1e-6
		case g.Kind == "dtr" || g.Kind == "rts":
			same = g.On == w.On
		}
		if !same {
			msg := fmt.Sprintf("step %d: Go %v, reference %v", i, g, w)
			if g.Kind == "tx" && w.Kind == "tx" {
				msg += fmt.Sprintf("\n  Go  %.120s\n  ref %.120s", g.Hex, w.Hex)
			}
			t.Fatal(msg)
		}
	}
}

func testdata(name string) string { return filepath.Join("testdata", name) }

func readPackage(t *testing.T, path string) *Package {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := OpenPackage(b)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	// As the caller does before flashing: the board has the SoftDevice the package asks for.
	if _, err := p.CheckSoftDevice(p.Init.SDReq[0]); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return p
}

// timedPort notes the virtual time of every write.
type timedPort struct {
	serialport.Port
	clock  serialport.Clock
	writes []time.Time
}

func (p *timedPort) Write(b []byte) (int, error) {
	p.writes = append(p.writes, p.clock.Now())
	return p.Port.Write(b)
}

// run is one flash, as dfu.js's dfuUpdate does it: open at 115200, flash, close.
type run struct {
	log   *serialport.Log
	clock *serialport.VirtualClock
	boot  *FakeBootloader
	port  *timedPort
	err   error
	prog  []Progress
}

func flashFake(t *testing.T, pkg *Package, scenario string, setup func(*FakeBootloader)) *run {
	t.Helper()
	r := &run{log: &serialport.Log{}}
	r.clock = serialport.NewVirtualClock(r.log)
	boot, err := NewFakeBootloader(scenario)
	if err != nil {
		t.Fatal(err)
	}
	r.boot = boot
	if setup != nil {
		setup(boot)
	}
	fake := serialport.NewFake(boot, r.clock, r.log, serialport.Options{Baud: DFUBaud})
	boot.Port = fake
	r.port = &timedPort{Port: fake, clock: r.clock}
	r.err = Flash(context.Background(), r.port, r.clock, pkg, func(p Progress) { r.prog = append(r.prog, p) })
	if err := fake.Close(); err != nil {
		t.Fatal(err)
	}
	return r
}

// checkVerdict compares the Go fake's view of the session with the Node fake's.
func checkVerdict(t *testing.T, rec []nodeEvent, res FakeResult) {
	t.Helper()
	seen := map[string]bool{}
	for _, e := range rec {
		switch {
		case e.T != "bootloader":
		case e.Warn != "":
			t.Errorf("the reference's fake warned: %s", e.Warn)
		case e.Start != nil:
			seen["start"] = true
			if res.Start == nil || int(res.Start.App) != e.Start.App || int(res.Start.Mode) != e.Start.Mode ||
				int(res.Start.SoftDevice) != e.Start.SD || int(res.Start.Bootloader) != e.Start.BL {
				t.Errorf("START: Go fake %+v, reference %+v", res.Start, *e.Start)
			}
		case e.InitHex != "":
			seen["init"] = true
			if got := hex.EncodeToString(res.Init); got != e.InitHex {
				t.Errorf("init packet: Go fake %s, reference %s", got, e.InitHex)
			}
		case e.Stop:
			seen["stop"] = true
			if !res.Stopped || res.Received != e.Received || int(res.ImageCRC) != e.CRC || res.Valid != e.Valid {
				t.Errorf("STOP: Go fake stopped=%v received=%d crc=%#04x valid=%v; reference received=%d crc=%#04x valid=%v",
					res.Stopped, res.Received, res.ImageCRC, res.Valid, e.Received, e.CRC, e.Valid)
			}
		}
	}
	if !seen["start"] || !seen["init"] || !seen["stop"] {
		t.Errorf("the recording lacks the reference fake's view of START, INIT or STOP: %v", seen)
	}
	if res.LenMismatch != 0 || res.Invalid != 0 || res.Unknown != 0 {
		t.Errorf("Go fake: %d length-field mismatches, %d invalid frames, %d unknown packets", res.LenMismatch, res.Invalid, res.Unknown)
	}
}

// writes is every write of a recording, one per frame (dfu.js writes each frame in one go).
func writes(rec []nodeEvent) [][]byte {
	var out [][]byte
	for _, e := range rec {
		if e.T == "write" {
			b, _ := hex.DecodeString(e.Hex)
			out = append(out, b)
		}
	}
	return out
}

func lastEvent(rec []nodeEvent, kind string) *nodeEvent {
	for i := len(rec) - 1; i >= 0; i-- {
		if rec[i].T == kind {
			return &rec[i]
		}
	}
	return nil
}

// TestMatchesReference flashes each synthetic package and compares everything with dfu.js's recording.
func TestMatchesReference(t *testing.T) {
	for _, c := range []struct {
		name      string
		recording string
		frames    int // DATA frames
	}{
		{"tiny-1028", "tiny-1028.jsonl", 3},
		{"tiny-4096", "tiny-4096.jsonl", 8},
		{"synth-40000", "synth-40000.jsonl.gz", 79},
	} {
		t.Run(c.name, func(t *testing.T) {
			pkg := readPackage(t, testdata(c.name+".zip"))
			rec := loadRecording(t, testdata(c.recording))
			if e := lastEvent(rec, "error"); e != nil {
				t.Fatalf("the reference failed: %s", e.Message)
			}
			r := flashFake(t, pkg, "ok", nil)
			if r.err != nil {
				t.Fatal(r.err)
			}
			want := nodeSteps(rec)
			compareSteps(t, goSteps(r.log), want)
			checkVerdict(t, rec, r.boot.Result())
			if res := r.boot.Result(); !res.Valid || !bytes.Equal(r.boot.Image(), pkg.Bin) {
				t.Errorf("the fake bootloader didn't get the image: %+v", res)
			}

			// What the recording shows: open at 115200, START, INIT, the DATA frames, STOP, close.
			tx := len(writes(rec))
			if want[0] != (step{Kind: "open", Baud: 115200}) || want[len(want)-1].Kind != "close" || tx != c.frames+3 {
				t.Errorf("recording: %d writes, first %v, last %v", tx, want[0], want[len(want)-1])
			}
		})
	}
}

// TestReferenceShape checks the properties the synthetic packages were made to exercise, in the recordings
// themselves: escapes, the double sleep at 8 frames, and the sequence number wrapping.
func TestReferenceShape(t *testing.T) {
	steps := nodeSteps(loadRecording(t, testdata("tiny-1028.jsonl")))
	var escapes int
	for _, s := range steps {
		if s.Kind == "tx" {
			escapes += strings.Count(s.Hex, "dbdc") + strings.Count(s.Hex, "dbdd")
		}
	}
	if escapes == 0 {
		t.Error("tiny-1028 escapes no 0xC0 or 0xDB")
	}

	steps = nodeSteps(loadRecording(t, testdata("tiny-4096.jsonl")))
	// ... DATA#8, sleep (page), sleep (last page), STOP, close
	n := len(steps)
	if steps[n-4].Kind != "sleep" || steps[n-3].Kind != "sleep" || steps[n-2].Kind != "tx" {
		t.Errorf("tiny-4096 doesn't end in two page-write sleeps and STOP: %v", steps[n-5:])
	}

	var seqs []int
	for _, w := range writes(loadRecording(t, testdata("synth-40000.jsonl.gz"))) {
		seqs = append(seqs, int(w[1]&7))
	}
	for i, s := range seqs {
		if s != (i+1)%8 {
			t.Fatalf("frame %d has seq %d", i, s)
		}
	}
	if len(seqs) < 80 {
		t.Errorf("synth-40000 has %d frames, too few to wrap the sequence ten times", len(seqs))
	}
}

// TestDropAck: dfu.js gives up when an acknowledgement doesn't come within 5 s; meshflash sends the same frame again
// 5 s later and carries on. The bootloader dropped the first copy, so the resend is taken as new.
func TestDropAck(t *testing.T) {
	pkg := readPackage(t, testdata("synth-40000.zip"))
	ok := nodeRaw(loadRecording(t, testdata("synth-40000.jsonl.gz")))
	for _, n := range []int{0, 1} {
		t.Run(fmt.Sprintf("drop-ack:%d", n), func(t *testing.T) {
			rec := loadRecording(t, testdata(fmt.Sprintf("synth-40000-drop-ack-%d.jsonl", n)))
			// The reference: the same steps as a good run up to frame n, a 5 s read timeout, "Read timeout", close.
			if e := lastEvent(rec, "error"); e == nil || e.Message != "Read timeout" {
				t.Fatalf("the reference didn't fail with a read timeout: %+v", e)
			}
			if e := lastEvent(rec, "read-timeout"); e == nil || e.Ms != 5000 {
				t.Fatalf("the reference's read timeout isn't 5000 ms: %+v", e)
			}
			ref := nodeSteps(rec)
			k := txIndex(ok, n)
			compareSteps(t, ref, mergeTX(append(append([]step(nil), ok[:k+1]...), step{Kind: "close"})))

			r := flashFake(t, pkg, fmt.Sprintf("drop-ack:%d", n), nil)
			if r.err != nil {
				t.Fatalf("meshflash didn't recover: %v", r.err)
			}
			compareSteps(t, goSteps(r.log), withResend(ok, n))
			if res := r.boot.Result(); !res.Valid || res.Dropped != 1 || res.Dups != 0 {
				t.Errorf("fake bootloader: %+v", res)
			}
			// The resend went out 5 s (one read timeout) after the first copy.
			if gap := r.port.writes[n+1].Sub(r.port.writes[n]); gap != AckTimeout {
				t.Errorf("resent after %v, want %v", gap, AckTimeout)
			}
		})
	}
}

// txIndex is the index in steps of the n-th write (from 0).
func txIndex(steps []step, n int) int {
	for i, s := range steps {
		if s.Kind == "tx" {
			if n == 0 {
				return i
			}
			n--
		}
	}
	panic("no such write")
}

// withResend is a good run's steps (one write per frame) with frame n sent twice in a row, consecutive writes
// joined.
func withResend(raw []step, n int) []step {
	k := txIndex(raw, n)
	out := append([]step(nil), raw[:k+1]...)
	out = append(out, raw[k:]...)
	return mergeTX(out)
}

// TestBadAck: a wrong acknowledgement number fails at once, as in dfu.js, with nothing resent.
func TestBadAck(t *testing.T) {
	pkg := readPackage(t, testdata("synth-40000.zip"))
	rec := loadRecording(t, testdata("synth-40000-bad-ack-2.jsonl"))
	r := flashFake(t, pkg, "bad-ack:2", nil)
	compareSteps(t, goSteps(r.log), nodeSteps(rec))

	var fe *FlashError
	if !errors.As(r.err, &fe) || !errors.Is(r.err, ErrBadAck) {
		t.Fatalf("got %v, want a *FlashError wrapping ErrBadAck", r.err)
	}
	if fe.Stage != StageWriting || fe.Sent != 0 {
		t.Errorf("stage %v, sent %d; want writing, 0", fe.Stage, fe.Sent)
	}
	numbers := regexp.MustCompile(`(?i)expected (\d+), got (\d+)`)
	ref := lastEvent(rec, "error")
	if ref == nil {
		t.Fatal("the reference didn't fail")
	}
	w, g := numbers.FindStringSubmatch(ref.Message), numbers.FindStringSubmatch(r.err.Error())
	if w == nil || g == nil || w[1] != g[1] || w[2] != g[2] {
		t.Errorf("Go says %q, the reference %q", r.err, ref.Message)
	}
	if !strings.Contains(fe.Recovery(), recoverErased) {
		t.Errorf("recovery %q", fe.Recovery())
	}
}

// TestRefusesBootloaderPackage: dfu.js opens the port and then throws on an OTAFIX-style package; meshflash
// refuses it before any port is opened.
func TestRefusesBootloaderPackage(t *testing.T) {
	rec := loadRecording(t, testdata("sd-bl.jsonl"))
	if rec[0].T != "open" || lastEvent(rec, "error") == nil || lastEvent(rec, "write") != nil {
		t.Fatalf("the reference recording isn't open-then-fail: %+v", rec)
	}
	b, err := os.ReadFile(testdata("sd-bl.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPackage(b); !errors.Is(err, ErrNotApplication) {
		t.Fatalf("got %v, want ErrNotApplication", err)
	}
}

// TestTouchMatchesReference: the reference's open at 1200, 100 ms, close, 1500 ms, plus the DTR drop before the
// close, on a port opened exclusively.
func TestTouchMatchesReference(t *testing.T) {
	want := nodeSteps(loadRecording(t, testdata("touch.jsonl")))
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	app := &FakeApp{}
	var opened serialport.Options
	var fake *serialport.Fake
	open := func(path string, o serialport.Options) (serialport.Port, error) {
		if path != "/dev/ttyACM0" {
			t.Errorf("opened %s", path)
		}
		opened = o
		fake = serialport.NewFake(app, clock, log, o)
		app.Port = fake
		return fake, nil
	}
	if err := Touch(open, "/dev/ttyACM0", clock); err != nil {
		t.Fatal(err)
	}
	if !opened.Exclusive || opened.Baud != TouchBaud {
		t.Errorf("opened with %+v, want exclusive at 1200", opened)
	}
	got := goSteps(log)
	// Our one addition: DTR dropped between the 100 ms wait and the close.
	if len(got) != 5 || got[2] != (step{Kind: "dtr", On: false}) {
		t.Fatalf("Go touch: %v", got)
	}
	compareSteps(t, append(append([]step(nil), got[:2]...), got[3:]...), want)
	if !app.Reset() {
		t.Error("the app didn't reset into its bootloader")
	}
	// It reset at the DTR drop, before the close.
	var notes []string
	for _, e := range log.Events() {
		if e.Ev == "note" || e.Ev == "close" || e.Ev == "dtr" {
			notes = append(notes, e.Ev+e.Text)
		}
	}
	if strings.Join(notes, ",") != "dtr,noteunplugged,close" {
		t.Errorf("events %v", notes)
	}
	if _, _, _, closed := fake.State(); !closed {
		t.Error("the port was left open")
	}
}
