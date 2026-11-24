package nrfdfu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

// The frames of synth-40000: START 0, INIT 1, DATA#1..#79 are 2..80, STOP 81.
const (
	synthFrames = 79
	synthStop   = synthFrames + 2
)

func synthPackage(t *testing.T) *Package { return readPackage(t, testdata("synth-40000.zip")) }

func synthOK(t *testing.T) []step {
	return nodeRaw(loadRecording(t, testdata("synth-40000.jsonl.gz")))
}

// TestResendCases: the acknowledgement is lost, or comes late, rather than the frame: the bootloader has the frame,
// so it drops the copy and acknowledges it again. Either way the bytes are a good run's with one frame sent twice,
// and the image arrives once.
func TestResendCases(t *testing.T) {
	pkg := synthPackage(t)
	ok := synthOK(t)
	// (A lost acknowledgement of STOP is TestStopAckLost: the bootloader has left by the time the copy goes out.)
	for _, scenario := range []string{"lose-ack:0", "lose-ack:1", "lose-ack:6", "late-ack:0", "late-ack:1", "late-ack:9", "drop-ack:" + fmt.Sprint(synthStop)} {
		t.Run(scenario, func(t *testing.T) {
			var n int
			fmt.Sscanf(scenario[strings.IndexByte(scenario, ':')+1:], "%d", &n)
			r := flashFake(t, pkg, scenario, nil)
			if r.err != nil {
				t.Fatal(r.err)
			}
			compareSteps(t, goSteps(r.log), withResend(ok, n))
			res := r.boot.Result()
			wantDups := 1
			if strings.HasPrefix(scenario, "drop-ack") {
				wantDups = 0
			}
			if !res.Valid || res.Dups != wantDups || !bytes.Equal(r.boot.Image(), pkg.Bin) {
				t.Errorf("fake bootloader: %+v", res)
			}
		})
	}
}

// TestNeverAnswers: a bootloader that stops answering is given the frame three times, 5 s apart, and the flash
// fails with the stage it got to.
func TestNeverAnswers(t *testing.T) {
	pkg := synthPackage(t)
	for _, c := range []struct {
		from     int
		stage    Stage
		sent     int
		recovery string
	}{
		{0, StageStarting, 0, recoverNothingWritten},
		{1, StageErasing, 0, recoverErased},
		{2, StageWriting, 0, "refused this firmware"}, // DATA#1: what a refused init packet looks like
		{6, StageWriting, 4 * chunkSize, recoverErased},
		{synthStop, StageFinishing, len(pkg.Bin), recoverErased},
	} {
		t.Run(fmt.Sprintf("mute-from:%d", c.from), func(t *testing.T) {
			r := flashFake(t, pkg, fmt.Sprintf("mute-from:%d", c.from), nil)
			var fe *FlashError
			if !errors.As(r.err, &fe) || !errors.Is(r.err, ErrNoAck) {
				t.Fatalf("got %v, want a *FlashError wrapping ErrNoAck", r.err)
			}
			if fe.Stage != c.stage || fe.Sent != c.sent || fe.Total != len(pkg.Bin) {
				t.Errorf("stage %v, sent %d of %d; want %v, %d", fe.Stage, fe.Sent, fe.Total, c.stage, c.sent)
			}
			if !strings.Contains(fe.Recovery(), c.recovery) {
				t.Errorf("recovery %q doesn't say %q", fe.Recovery(), c.recovery)
			}
			// The frame went out three times, 5 s apart, and nothing after it.
			w := r.port.writes
			if len(w) != c.from+Tries {
				t.Fatalf("%d writes, want %d", len(w), c.from+Tries)
			}
			for i := c.from + 1; i < len(w); i++ {
				if gap := w[i].Sub(w[i-1]); gap != AckTimeout {
					t.Errorf("write %d came %v after the one before", i, gap)
				}
			}
			if got := bytes.Count(r.log.TX(), lastFrame(t, r.log)); got != Tries {
				t.Errorf("the last frame was sent %d times", got)
			}
		})
	}
}

// lastFrame is the last frame the host wrote.
func lastFrame(t *testing.T, log *serialport.Log) []byte {
	t.Helper()
	tx := log.TX()
	end := len(tx) - 1
	start := bytes.LastIndexByte(tx[:end], slipEnd)
	if tx[end] != slipEnd || start < 0 {
		t.Fatalf("the host's last bytes aren't a frame: %x", tx[max(0, len(tx)-16):])
	}
	return tx[start:]
}

// TestUnplugMidData: the board vanishes while the firmware is being written.
func TestUnplugMidData(t *testing.T) {
	pkg := synthPackage(t)
	r := flashFake(t, pkg, "unplug-at:12", nil) // DATA#11
	var fe *FlashError
	if !errors.As(r.err, &fe) || !errors.Is(r.err, serialport.ErrGone) {
		t.Fatalf("got %v, want a *FlashError wrapping serialport.ErrGone", r.err)
	}
	if fe.Stage != StageWriting || fe.Sent != 10*chunkSize {
		t.Errorf("stage %v, sent %d; want writing, %d", fe.Stage, fe.Sent, 10*chunkSize)
	}
	rec := fe.Recovery()
	for _, want := range []string{"press reset twice quickly to get the bootloader back, then flash again", "never retry in the same bootloader session", "Plug it back in"} {
		if !strings.Contains(rec, want) {
			t.Errorf("recovery %q doesn't say %q", rec, want)
		}
	}
	if !strings.Contains(fe.Error(), "writing (5120 of 40000 bytes)") {
		t.Errorf("error %q", fe.Error())
	}
	// Nothing was resent to a port that has gone.
	if len(r.port.writes) != 13 {
		t.Errorf("%d writes", len(r.port.writes))
	}
}

// TestUnplugBeforeStart: the port is gone before START's acknowledgement: nothing was written.
func TestUnplugBeforeStart(t *testing.T) {
	r := flashFake(t, synthPackage(t), "unplug-at:0", nil)
	var fe *FlashError
	if !errors.As(r.err, &fe) || fe.Stage != StageStarting || !errors.Is(r.err, serialport.ErrGone) {
		t.Fatalf("got %v", r.err)
	}
	if !strings.HasPrefix(fe.Recovery(), "Nothing was written") {
		t.Errorf("recovery %q", fe.Recovery())
	}
}

// TestInitRefused: a package for another SoftDevice that got past the checks (CheckSoftDevice would have stopped
// it). The bootloader acknowledges INIT, refuses it, resets, and vanishes: the host sees the port gone at DATA#1.
func TestInitRefused(t *testing.T) {
	pkg := synthPackage(t) // needs S140 6.1.1
	if _, err := pkg.CheckSoftDevice(SoftDeviceS140v7); !errors.Is(err, ErrWrongSoftDevice) {
		t.Fatalf("CheckSoftDevice: %v", err)
	}
	// How such a package gets this far: the catalog didn't say which SoftDevice the board has.
	if note, err := pkg.CheckSoftDevice(0); err != nil || note == "" {
		t.Fatalf("CheckSoftDevice(0): %q, %v", note, err)
	}
	r := flashFake(t, pkg, "ok", func(b *FakeBootloader) { b.SoftDevice = SoftDeviceS140v7 })
	var fe *FlashError
	if !errors.As(r.err, &fe) || !errors.Is(r.err, serialport.ErrGone) {
		t.Fatalf("got %v", r.err)
	}
	if fe.Stage != StageWriting || fe.Sent != 0 || !r.boot.Result().Reset {
		t.Errorf("stage %v, sent %d, reset %v", fe.Stage, fe.Sent, r.boot.Result().Reset)
	}
	if !strings.Contains(fe.Recovery(), "refused this firmware") || !strings.Contains(strings.ToLower(fe.Recovery()), "press reset twice quickly") {
		t.Errorf("recovery %q", fe.Recovery())
	}

	// The same bootloader takes the package when its SoftDevice is the one listed.
	r = flashFake(t, pkg, "ok", func(b *FakeBootloader) { b.SoftDevice = SoftDeviceS140v6 })
	if r.err != nil || !r.boot.Result().Valid {
		t.Fatalf("%v, %+v", r.err, r.boot.Result())
	}
}

func TestProgress(t *testing.T) {
	pkg := readPackage(t, testdata("tiny-4096.zip"))
	r := flashFake(t, pkg, "ok", nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	var got []string
	for _, p := range r.prog {
		if p.Total != 4096 || p.Frames != 8 {
			t.Errorf("progress %+v", p)
		}
		s := fmt.Sprintf("%v %d/%d %d", p.Stage, p.Frame, p.Frames, p.Sent)
		if p.Stage == StageErasing {
			s += fmt.Sprintf(" %v", p.Wait)
		}
		got = append(got, s)
	}
	want := []string{
		"starting 0/8 0", "erasing 0/8 0 500ms", "writing 0/8 0",
		"writing 1/8 512", "writing 2/8 1024", "writing 3/8 1536", "writing 4/8 2048",
		"writing 5/8 2560", "writing 6/8 3072", "writing 7/8 3584", "writing 8/8 4096",
		"finishing 8/8 4096",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("progress:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestStop: the owner stops the flash; it ends between frames with the context's error.
func TestStop(t *testing.T) {
	pkg := synthPackage(t)
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	boot, _ := NewFakeBootloader("ok")
	port := serialport.NewFake(boot, clock, log, serialport.Options{Baud: DFUBaud})
	ctx, cancel := context.WithCancel(context.Background())
	err := Flash(ctx, port, clock, pkg, func(p Progress) {
		if p.Frame == 3 {
			cancel()
		}
	})
	var fe *FlashError
	if !errors.As(err, &fe) || !errors.Is(err, context.Canceled) || fe.Stage != StageWriting || fe.Sent != 3*chunkSize {
		t.Fatalf("got %v", err)
	}
	if boot.Result().Received != 3*chunkSize {
		t.Errorf("the bootloader got %d bytes", boot.Result().Received)
	}

	// Stopped before anything was sent.
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	log = &serialport.Log{}
	boot, _ = NewFakeBootloader("ok")
	port = serialport.NewFake(boot, clock, log, serialport.Options{Baud: DFUBaud})
	err = Flash(ctx, port, clock, pkg, nil)
	if !errors.As(err, &fe) || fe.Stage != StageStarting || len(log.TX()) != 0 {
		t.Fatalf("got %v, wrote %d bytes", err, len(log.TX()))
	}
}

// TestFlashRefusesBadPackage: a Package built by hand is checked again; nothing is written.
func TestFlashRefusesBadPackage(t *testing.T) {
	good := readPackage(t, testdata("tiny-1028.zip"))
	for _, c := range []struct {
		name string
		pkg  *Package
		want error
	}{
		{"nil", nil, nil},
		{"odd size", &Package{Bin: good.Bin[:1026], Dat: good.Dat}, ErrBadImage},
		{"wrong CRC", &Package{Bin: append([]byte{1, 2, 3, 4}, good.Bin[4:]...), Dat: good.Dat}, ErrCRCMismatch},
		{"no init packet", &Package{Bin: good.Bin}, ErrBadInitPacket},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := &serialport.Log{}
			clock := serialport.NewVirtualClock(log)
			boot, _ := NewFakeBootloader("ok")
			port := serialport.NewFake(boot, clock, log, serialport.Options{Baud: DFUBaud})
			err := Flash(context.Background(), port, clock, c.pkg, nil)
			var fe *FlashError
			if !errors.As(err, &fe) || fe.Stage != StageStarting || (c.want != nil && !errors.Is(err, c.want)) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if len(log.TX()) != 0 || fe.Recovery() != recoverNothingWritten {
				t.Errorf("wrote %d bytes; recovery %q", len(log.TX()), fe.Recovery())
			}
		})
	}
}

// TestFlashClosedPort: a port that's closed fails at once, with nothing written.
func TestFlashClosedPort(t *testing.T) {
	clock := serialport.NewVirtualClock(nil)
	port := serialport.NewFake(nil, clock, nil, serialport.Options{Baud: DFUBaud})
	port.Close()
	err := Flash(nil, port, clock, readPackage(t, testdata("tiny-1028.zip")), nil) // a nil context is taken as Background
	var fe *FlashError
	if !errors.As(err, &fe) || fe.Stage != StageStarting || !errors.Is(err, serialport.ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

func TestFakeBootloaderScenarios(t *testing.T) {
	for _, bad := range []string{"drop-ack", "drop-ack:x", "drop-ack:-1", "nonsense:1", "bad"} {
		if _, err := NewFakeBootloader(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// bad-crc drops INIT as the harness does: dfu.js would time out there; meshflash resends.
	r := flashFake(t, readPackage(t, testdata("tiny-1028.zip")), "bad-crc", nil)
	if r.err != nil || r.boot.Result().Dropped != 1 || !r.boot.Result().Valid {
		t.Fatalf("%v, %+v", r.err, r.boot.Result())
	}
	// A corrupt frame (bad CRC on the wire) is dropped without a word.
	boot, _ := NewFakeBootloader("ok")
	f := encodeFrame(1, startPayload(1028))
	f[len(f)-2] ^= 0x01
	if out := boot.Receive(f); out != nil || boot.Result().Invalid != 1 {
		t.Errorf("corrupt frame answered %x, result %+v", out, boot.Result())
	}
	// Bytes before the first 0xC0 are ignored, and so is an empty frame.
	boot, _ = NewFakeBootloader("ok")
	out := boot.Receive(append([]byte{1, 2, slipEnd, slipEnd}, encodeFrame(1, startPayload(1028))[1:]...))
	if !bytes.Equal(out, ackFrame(2)) {
		t.Errorf("answered %x", out)
	}
}

// TestStopAckLost: STOP's acknowledgement is lost (or late). The real bootloader has by then checked the image and
// left for the app, so the copy of STOP finds the port gone. That is most likely a good flash, and the error says
// so (MayHaveWorked), rather than the "app is erased" of a board pulled out before STOP.
func TestStopAckLost(t *testing.T) {
	pkg := synthPackage(t)
	ok := synthOK(t)
	for _, scenario := range []string{"lose-ack:" + fmt.Sprint(synthStop), "late-ack:" + fmt.Sprint(synthStop)} {
		t.Run(scenario, func(t *testing.T) {
			r := flashFake(t, pkg, scenario, nil)
			var fe *FlashError
			if !errors.As(r.err, &fe) || !errors.Is(r.err, serialport.ErrGone) {
				t.Fatalf("got %v, want a *FlashError wrapping serialport.ErrGone", r.err)
			}
			if fe.Stage != StageFinishing || !fe.StopSent || !fe.MayHaveWorked() || fe.Sent != len(pkg.Bin) {
				t.Errorf("stage %v, stop sent %v, may have worked %v, sent %d", fe.Stage, fe.StopSent, fe.MayHaveWorked(), fe.Sent)
			}
			if rec := fe.Recovery(); !strings.Contains(rec, "most likely worked") || !strings.Contains(rec, "press reset twice quickly") {
				t.Errorf("recovery %q", rec)
			}
			if !strings.Contains(fe.Error(), "after sending STOP") {
				t.Errorf("error %q", fe.Error())
			}
			if res := r.boot.Result(); !res.Valid || !res.AppStarted {
				t.Errorf("fake bootloader: %+v", res)
			}
			// A good run with STOP sent a second time, one read timeout later, and nothing after it.
			compareSteps(t, goSteps(r.log), withResend(ok, synthStop))
			if n := len(r.port.writes); n != synthStop+2 || r.port.writes[n-1].Sub(r.port.writes[n-2]) != AckTimeout {
				t.Errorf("%d writes", n)
			}
		})
	}
}

// unplugAt is a port whose board is pulled out just before the host's write number at (from 0).
type unplugAt struct {
	*timedPort
	fake *serialport.Fake
	at   int
}

func (p *unplugAt) Write(b []byte) (int, error) {
	if len(p.writes) == p.at {
		p.fake.Unplug()
	}
	return p.timedPort.Write(b)
}

// TestUnplugBeforeStop: the board goes away during the last page's wait, so STOP never leaves: the app is erased and
// nothing suggests the flash worked.
func TestUnplugBeforeStop(t *testing.T) {
	pkg := synthPackage(t)
	clock := serialport.NewVirtualClock(nil)
	boot, _ := NewFakeBootloader("ok")
	fake := serialport.NewFake(boot, clock, nil, serialport.Options{Baud: DFUBaud})
	port := &unplugAt{timedPort: &timedPort{Port: fake, clock: clock}, fake: fake, at: synthStop}
	err := Flash(context.Background(), port, clock, pkg, nil)
	var fe *FlashError
	if !errors.As(err, &fe) || !errors.Is(err, serialport.ErrGone) {
		t.Fatalf("got %v", err)
	}
	if fe.Stage != StageFinishing || fe.StopSent || fe.MayHaveWorked() {
		t.Errorf("stage %v, stop sent %v", fe.Stage, fe.StopSent)
	}
	if rec := fe.Recovery(); strings.Contains(rec, "most likely worked") || !strings.Contains(rec, "app is erased") {
		t.Errorf("recovery %q", rec)
	}
	if boot.Result().Stopped {
		t.Error("the bootloader got STOP")
	}
}

// flightPort fails the test when the host writes a frame before it has read the last one's acknowledgement (or
// waited out a read): the bootloader acknowledges a frame before it has room for it, so a second frame in flight
// can be acknowledged and then dropped (docs/meshcore-flashing.md 5.3). serialport.Log can't show this: the fake
// answers inside Write, so its rx events come right after each tx whatever order the host reads in.
type flightPort struct {
	serialport.Port
	t       *testing.T
	pending bool
	writes  int
}

func (p *flightPort) Write(b []byte) (int, error) {
	if p.pending {
		p.t.Errorf("write %d went out before write %d's acknowledgement was read", p.writes, p.writes-1)
	}
	p.pending = true
	p.writes++
	return p.Port.Write(b)
}

func (p *flightPort) Read(b []byte, timeout time.Duration) (int, error) {
	n, err := p.Port.Read(b, timeout)
	if n > 0 || errors.Is(err, serialport.ErrTimeout) {
		p.pending = false
	}
	return n, err
}

// TestOneFrameInFlight: every frame waits for its acknowledgement (or a read timeout, before a resend) before the
// next write, as dfu.js's sendPacket does.
func TestOneFrameInFlight(t *testing.T) {
	pkg := synthPackage(t)
	for _, scenario := range []string{"ok", "lose-ack:6", "late-ack:9", "drop-ack:1"} {
		t.Run(scenario, func(t *testing.T) {
			clock := serialport.NewVirtualClock(nil)
			boot, _ := NewFakeBootloader(scenario)
			fake := serialport.NewFake(boot, clock, nil, serialport.Options{Baud: DFUBaud})
			boot.Port = fake
			port := &flightPort{Port: fake, t: t}
			if err := Flash(context.Background(), port, clock, pkg, nil); err != nil {
				t.Fatal(err)
			}
			want := synthStop + 1 // START, INIT, the DATA frames, STOP
			if scenario != "ok" {
				want++ // and the one resend
			}
			if port.writes != want {
				t.Errorf("%d writes, want %d", port.writes, want)
			}
		})
	}
}

// TestFlashRequiresSoftDeviceCheck: Flash refuses, before writing anything, a package CheckSoftDevice hasn't passed
// for the board, one it refused, and one whose init packet was changed after the check.
func TestFlashRequiresSoftDeviceCheck(t *testing.T) {
	raw, err := os.ReadFile(testdata("tiny-1028.zip"))
	if err != nil {
		t.Fatal(err)
	}
	open := func() *Package {
		p, err := OpenPackage(raw)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	flash := func(p *Package) (tx []byte, err error) {
		log := &serialport.Log{}
		clock := serialport.NewVirtualClock(log)
		boot, _ := NewFakeBootloader("ok")
		port := serialport.NewFake(boot, clock, log, serialport.Options{Baud: DFUBaud})
		err = Flash(context.Background(), port, clock, p, nil)
		return log.TX(), err
	}
	refused := func(name string, p *Package) {
		t.Helper()
		tx, err := flash(p)
		var fe *FlashError
		if !errors.As(err, &fe) || !errors.Is(err, ErrSoftDeviceUnchecked) || fe.Stage != StageStarting || len(tx) != 0 {
			t.Errorf("%s: got %v, wrote %d bytes", name, err, len(tx))
		} else if fe.Recovery() != recoverNothingWritten {
			t.Errorf("%s: recovery %q", name, fe.Recovery())
		}
	}

	refused("never checked", open())

	p := open()
	if _, err := p.CheckSoftDevice(SoftDeviceS140v6); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CheckSoftDevice(SoftDeviceS140v7); !errors.Is(err, ErrWrongSoftDevice) {
		t.Fatal(err)
	}
	refused("passed, then refused for another board", p)

	p = open()
	if _, err := p.CheckSoftDevice(SoftDeviceS140v6); err != nil {
		t.Fatal(err)
	}
	p.Dat = mkDat(0x52, []uint16{SoftDeviceS140v7}, p.Init.CRC16) // a .dat for S140 7.3.0 only, swapped in afterwards
	refused("init packet changed after the check", p)

	for _, fwid := range []uint16{SoftDeviceS140v6, 0} {
		p = open()
		if _, err := p.CheckSoftDevice(fwid); err != nil {
			t.Fatal(err)
		}
		if _, err := flash(p); err != nil {
			t.Errorf("checked for %#04x: %v", fwid, err)
		}
	}
}

// TestFakeBootloaderEdges: a valid frame too short to hold a packet type is counted, not a panic; after STOP with a
// good image the fake leaves for the app (its port vanishes before the host's next write); with a bad one it stays
// and keeps answering, as the real bootloader does (docs/meshcore-flashing.md 5.6).
func TestFakeBootloaderEdges(t *testing.T) {
	for n := 0; n < 4; n++ {
		boot, _ := NewFakeBootloader("ok")
		if out := boot.Receive(encodeFrame(1, make([]byte, n))); !bytes.Equal(out, ackFrame(2)) || boot.Result().Unknown != 1 {
			t.Errorf("%d-byte payload: answered %x, %+v", n, out, boot.Result())
		}
	}

	// session runs START, INIT, one DATA and STOP into a fake, then sends STOP again (as after a lost
	// acknowledgement) and returns what the fake answered to that copy.
	session := func(bin []byte, crc uint16) (*FakeBootloader, *serialport.Fake, []byte) {
		clock := serialport.NewVirtualClock(nil)
		boot, _ := NewFakeBootloader("ok")
		fake := serialport.NewFake(boot, clock, nil, serialport.Options{Baud: DFUBaud})
		boot.Port = fake
		seq := 0
		for _, pl := range [][]byte{startPayload(len(bin)), initPayload(mkDat(0x52, []uint16{SoftDeviceS140v6}, crc)), dataPayload(bin), stopPayload()} {
			seq = (seq + 1) % 8
			if out := boot.Receive(encodeFrame(seq, pl)); !bytes.Equal(out, ackFrame((seq+1)%8)) {
				t.Fatalf("frame %d answered %x", seq, out)
			}
		}
		again := boot.Receive(encodeFrame(seq, stopPayload()))
		return boot, fake, again
	}
	bin := synthImage(512)
	boot, fake, again := session(bin, CRC16(bin))
	if res := boot.Result(); !res.Valid || !res.AppStarted || again != nil {
		t.Errorf("good image: %+v, answered the copy with %x", res, again)
	}
	if _, err := fake.Read(make([]byte, 16), time.Second); !errors.Is(err, serialport.ErrGone) {
		t.Errorf("after a good STOP the port is still there: %v", err)
	}
	boot, fake, again = session(bin, CRC16(bin)^1)
	if res := boot.Result(); res.Valid || res.AppStarted || res.Dups != 1 || !bytes.Equal(again, ackFrame(5)) {
		t.Errorf("bad image: %+v, answered the copy with %x", res, again)
	}
	if _, _, _, closed := fake.State(); closed {
		t.Error("closed")
	}
	if _, err := fake.Read(make([]byte, 16), time.Second); !errors.Is(err, serialport.ErrTimeout) {
		t.Errorf("after a bad STOP the port went: %v", err)
	}
}

// TestCheckSoftDeviceReadsDat: the check is made on the init packet INIT will send, not on Init, so a Package whose
// two disagree is judged by what the bootloader would see.
func TestCheckSoftDeviceReadsDat(t *testing.T) {
	bin := synthImage(1028)
	p := &Package{Bin: bin, Dat: mkDat(0x52, []uint16{SoftDeviceS140v6}, CRC16(bin)), Init: InitPacket{SDReq: []uint16{SoftDeviceS140v7}}}
	if _, err := p.CheckSoftDevice(SoftDeviceS140v7); !errors.Is(err, ErrWrongSoftDevice) {
		t.Errorf("v6 .dat on a v7 board: %v", err)
	}
	if _, err := (&Package{Bin: bin, Dat: []byte{1, 2}}).CheckSoftDevice(0); !errors.Is(err, ErrBadInitPacket) {
		t.Errorf("broken .dat: %v", err)
	}
}

// echoConsole is MeshCore's serial console as a port mistaken for the bootloader's would show it: every byte the host
// writes comes back, except '\n' (examples/simple_repeater/main.cpp, the loop around Serial.print(c)).
type echoConsole struct{ got []byte }

func (e *echoConsole) Receive(p []byte) []byte {
	e.got = append(e.got, p...)
	return bytes.ReplaceAll(p, []byte{'\n'}, nil)
}
func (e *echoConsole) Lines(dtr, rts bool, baud int) []byte { return nil }

// TestEchoingPortRefused: every frame carries seq+1 where an acknowledgement carries its number, so to dfu.js an
// echoed frame is a perfect acknowledgement and an app's console would take the whole image as "success". Only
// the exact acknowledgement form counts, so Flash stops at START with nothing more sent.
func TestEchoingPortRefused(t *testing.T) {
	pkg := synthPackage(t)
	clock := serialport.NewVirtualClock(nil)
	app := &echoConsole{}
	fake := serialport.NewFake(app, clock, nil, serialport.Options{Baud: DFUBaud})
	port := &timedPort{Port: fake, clock: clock}
	err := Flash(context.Background(), port, clock, pkg, nil)
	var fe *FlashError
	if !errors.As(err, &fe) || !errors.Is(err, ErrBadAck) || fe.Stage != StageStarting {
		t.Fatalf("got %v, want a *FlashError at StageStarting wrapping ErrBadAck", err)
	}
	if !strings.Contains(err.Error(), "is this the bootloader") || fe.Recovery() != recoverNothingWritten {
		t.Errorf("error %q, recovery %q", err, fe.Recovery())
	}
	if len(port.writes) != 1 || !bytes.Equal(app.got, encodeFrame(1, startPayload(len(pkg.Bin)))) {
		t.Errorf("%d writes, %d bytes reached the console", len(port.writes), len(app.got))
	}
}
