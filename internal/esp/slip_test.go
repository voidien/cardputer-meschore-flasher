package esp

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

func TestSLIPRoundTrip(t *testing.T) {
	pkt := []byte{0x00, 0xC0, 0xDB, 0x01, 0xDB, 0xDC, 0xC0, 0xC0, 0xDD}
	wire := slipEncode(pkt)
	want := []byte{0xC0, 0x00, 0xDB, 0xDC, 0xDB, 0xDD, 0x01, 0xDB, 0xDD, 0xDC, 0xDB, 0xDC, 0xDB, 0xDC, 0xDD, 0xC0}
	if !bytes.Equal(wire, want) {
		t.Fatalf("encode: %x\nwant    %x", wire, want)
	}
	got, ok := slipDecode(wire[1 : len(wire)-1])
	if !ok || !bytes.Equal(got, pkt) {
		t.Fatalf("decode: %x %v", got, ok)
	}
	if _, ok := slipDecode([]byte{0x01, 0xDB, 0x02}); ok {
		t.Error("a bad escape decoded")
	}
	if _, ok := slipDecode([]byte{0x01, 0xDB}); ok {
		t.Error("a trailing escape decoded")
	}
}

// The wire bytes of the commands the research quotes (docs/meshcore-flashing.md 4.5).
func TestPacketBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
		want string
	}{
		{"READ_REG magic", slipEncode(packet(opReadReg, words(chipDetectMagicReg), 0)), "c0000a04000000000000100040c0"},
		{"MEM_END ESP32", slipEncode(packet(opMemEnd, words(0, 0x400be5ec), 0)), "c0000608000000000000000000ece50b40c0"},
		{"ERASE_FLASH", slipEncode(packet(opEraseFlash, nil, 0)), "c000d0000000000000c0"},
		{"FLASH_BEGIN(0,0)", slipEncode(packet(opFlashBegin, words(0, 0, 0x4000, 0), 0)), "c0000210000000000000000000000000000040000000000000c0"},
		{"FLASH_DEFL_END(1)", slipEncode(packet(opFlashDeflEnd, words(1), 0)), "c0001204000000000001000000c0"},
		{"FLASH_DEFL_BEGIN S3", slipEncode(packet(opFlashDeflBegin, words(0x11ecb0, 0x30, 0x4000, 0x10000), 0)), "c00010100000000000b0ec110030000000004000000000010" + "0c0"},
	} {
		if got := hexString(tc.wire); got != tc.want {
			t.Errorf("%s: %s\nwant %s", tc.name, got, tc.want)
		}
	}
	if c := checksum([]byte{1, 2, 3}); c != 0xEF^1^2^3 {
		t.Errorf("checksum %x", c)
	}
}

func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

// silent is a device that never answers; the test sends the board's bytes itself with Fake.Send.
type silent struct{}

func (silent) Receive([]byte) []byte        { return nil }
func (silent) Lines(bool, bool, int) []byte { return nil }

func newLink(t *testing.T) (*link, *serialport.Fake, *serialport.VirtualClock) {
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	port := serialport.NewFake(silent{}, clock, log, serialport.Options{})
	return &link{ctx: context.Background(), port: port, clock: clock}, port, clock
}

// A deadline passing in the middle of a reply loses nothing: the next read finishes the frame (esptool-js 0.5.4
// threw the partial bytes away, bug 4.10b).
func TestLinkKeepsBytesAcrossDeadlines(t *testing.T) {
	k, port, clock := newLink(t)
	frame := slipEncode([]byte{0x01, 0x0A, 0x02, 0x00, 0x83, 0x1D, 0xF0, 0x00, 0x00, 0x00})
	port.Send(frame[:5])
	if _, err := k.frame(clock.Now().Add(300 * time.Millisecond)); !errors.Is(err, errDeadline) {
		t.Fatalf("half a frame: %v", err)
	}
	port.Send(frame[5:])
	f, err := k.frame(clock.Now().Add(300 * time.Millisecond))
	if err != nil || !bytes.Equal(f, []byte{0x01, 0x0A, 0x02, 0x00, 0x83, 0x1D, 0xF0, 0x00, 0x00, 0x00}) {
		t.Fatalf("got %x, %v", f, err)
	}
}

// Bytes outside frames (an app's log, the end of a banner) are skipped, and C0 C0 puts the reader back in step.
func TestLinkSkipsNoise(t *testing.T) {
	k, port, clock := newLink(t)
	port.Send([]byte("waiting for download\r\n"))
	port.Send([]byte{0xC0, 0xC0, 0x01, 0x02, 0xC0})
	port.Send([]byte{0xC0, 0x01, 0xDB, 0x07, 0xC0}) // a bad escape: noise
	port.Send([]byte{0xC0, 0x03, 0xC0})
	deadline := clock.Now().Add(time.Second)
	for _, want := range [][]byte{{0x01, 0x02}, {0x03}} {
		f, err := k.frame(deadline)
		if err != nil || !bytes.Equal(f, want) {
			t.Fatalf("got %x, %v; want %x", f, err, want)
		}
	}
	if !bytes.Contains(k.noise, []byte("waiting for download")) {
		t.Errorf("noise %q", k.noise)
	}
}

func TestDrainBanner(t *testing.T) {
	k, port, clock := newLink(t)
	// Nothing at all: waits the boot-log timeout and returns nothing.
	start := clock.Now()
	b, err := k.drainBanner(bootLogTimeout, bannerQuiet, bootLogTimeout)
	if err != nil || b != nil || clock.Now().Sub(start) != bootLogTimeout {
		t.Fatalf("silent board: %q, %v after %v", b, err, clock.Now().Sub(start))
	}
	// A banner in two pieces close together is taken whole.
	port.Send([]byte("ets Jun  8 2016\r\n"))
	sent := false
	clock.OnTick(func(time.Time) {
		if !sent { // the rest comes on the next read's wait
			sent = true
			port.Send([]byte("waiting for download\r\n"))
		}
	})
	b, err = k.drainBanner(bootLogTimeout, bannerQuiet, bootLogTimeout)
	if err != nil || string(b) != "ets Jun  8 2016\r\nwaiting for download\r\n" {
		t.Fatalf("banner %q, %v", b, err)
	}
	if len(k.buf) != 0 {
		t.Errorf("left %q", k.buf)
	}
}

func TestReadReplyRules(t *testing.T) {
	k, port, clock := newLink(t)
	l := &Loader{ctx: context.Background(), port: port, clock: clock, link: *k}
	reply := func(op byte, val uint32, body ...byte) []byte {
		p := append([]byte{1, op, byte(len(body)), 0}, words(val)...)
		return slipEncode(append(p, body...))
	}
	// Leftover SYNC replies and a stray frame are skipped; the READ_REG reply's value comes back.
	port.Send(reply(opSync, 0x20120707, 0, 0, 0, 0))
	port.Send([]byte{0xC0, 0x02, 0xC0})
	port.Send(reply(opReadReg, 0x00f01d83, 0, 0, 0, 0))
	v, data, err := l.readReply(opReadReg, time.Second)
	if err != nil || v != 0x00f01d83 || len(data) != 0 {
		t.Fatalf("got %#x %x %v", v, data, err)
	}
	// A ROM "invalid message" to something else ends the wait at once.
	port.Send(reply(opMemBegin, 0, 1, 5, 0, 0))
	if _, _, err := l.readReply(opReadReg, time.Second); !errors.Is(err, ErrUnsupportedCommand) {
		t.Fatalf("invalid message: %v", err)
	}
	// A failed status is a StatusError: ROM layout here.
	port.Send(reply(opReadReg, 0, 1, 0x07, 0, 0))
	var se *StatusError
	if _, _, err := l.readReply(opReadReg, time.Second); !errors.As(err, &se) || se.Code != 7 || se.Stub {
		t.Fatalf("ROM failure: %v", err)
	}
	// The stub's layout: the error code first.
	l.stub = true
	port.Send(reply(opFlashDeflData, 0, 0xC7, 0))
	if _, _, err := l.readReply(opFlashDeflData, time.Second); !errors.As(err, &se) || se.Code != 0xC7 || !se.Stub {
		t.Fatalf("stub failure: %v", err)
	}
	// Silence is ErrNoReply after the command's own deadline.
	start := clock.Now()
	if _, _, err := l.readReply(opReadReg, 3*time.Second); !errors.Is(err, ErrNoReply) || clock.Now().Sub(start) != 3*time.Second {
		t.Fatalf("silence: %v after %v", err, clock.Now().Sub(start))
	}
}
