package nrfdfu

import (
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestGoldenFrames checks the frames docs/meshcore-flashing.md 5.5 lists, from the RAK4631 repeater v1.18.0 package
// (recorded from dfu.js and nrfutil alike) and the tiny-1028 fixture.
func TestGoldenFrames(t *testing.T) {
	rakDat := mustHex(t, "5200ffffffffffff0100b600df49")
	tinyDat := mustHex(t, "5200ffffffffffff0100b6008600")
	tiny := synthImage(1028)
	for _, c := range []struct {
		name string
		got  []byte
		want string
	}{
		{"RAK START", encodeFrame(1, startPayload(440924)), "c0d14e01e0030000000400000000000000000000005cba0600fa06c0"},
		{"RAK INIT", encodeFrame(2, initPayload(rakDat)), "c0da4e01d7010000005200ffffffffffff0100b600df490000bbd2c0"},
		{"RAK STOP", encodeFrame(1, stopPayload()), "c0d14e00e1050000007482c0"},
		{"tiny START", encodeFrame(1, startPayload(1028)), "c0d14e01e003000000040000000000000000000000040400005ab9c0"},
		{"tiny INIT", encodeFrame(2, initPayload(tinyDat)), "c0da4e01d7010000005200ffffffffffff0100b60086000000cbd7c0"},
		{"tiny DATA#3", encodeFrame(5, dataPayload(tiny[1024:])), "c0f58e007d04000000dbdcc7ced55c3ac0"},
		{"tiny STOP", encodeFrame(6, stopPayload()), "c0fe4e00b405000000bb16c0"},
	} {
		if got := hex.EncodeToString(c.got); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	// DATA#1 of the RAK image starts c0e34e20af04000000; of tiny-1028 it is 528 bytes, ending b2b959fdc0.
	d1 := hex.EncodeToString(encodeFrame(3, dataPayload(tiny[:512])))
	if !strings.HasPrefix(d1, "c0e34e20af04000000dbdcc7ced5dce3") || !strings.HasSuffix(d1, "b2b959fdc0") || len(d1) != 2*528 {
		t.Errorf("tiny DATA#1: %d bytes, %.40s…%s", len(d1)/2, d1, d1[len(d1)-10:])
	}
}

// TestHeaderBytes: the first header byte for each sequence number (5.3); none needs escaping.
func TestHeaderBytes(t *testing.T) {
	want := map[int]byte{1: 0xD1, 2: 0xDA, 3: 0xE3, 4: 0xEC, 5: 0xF5, 6: 0xFE, 7: 0xC7, 0: 0xC8}
	for seq, h0 := range want {
		f := encodeFrame(seq, stopPayload())
		if f[1] != h0 {
			t.Errorf("seq %d: H0 %#02x, want %#02x", seq, f[1], h0)
		}
		d, err := slipDecode(f[1 : len(f)-1])
		if err != nil {
			t.Fatal(err)
		}
		if byte(d[0]+d[1]+d[2]+d[3]) != 0 {
			t.Errorf("seq %d: header doesn't sum to 0", seq)
		}
		if l := int(d[1]>>4) | int(d[2])<<4; l != 4 {
			t.Errorf("seq %d: length field %d", seq, l)
		}
	}
	// The 12-bit length of a full DATA frame: 516 = 0x204, H1 = 0x4E, H2 = 0x20.
	f := encodeFrame(3, dataPayload(make([]byte, 512)))
	if f[2] != 0x4E || f[3] != 0x20 {
		t.Errorf("DATA length bytes %#02x %#02x", f[2], f[3])
	}
}

// TestAckFrames: what the bootloader sends for each expected number (5.3's table).
func TestAckFrames(t *testing.T) {
	want := []string{
		"c0000000" + "00c0", "c0080000f8c0", "c0100000f0c0", "c0180000e8c0",
		"c0200000e0c0", "c0280000d8c0", "c0300000d0c0", "c0380000c8c0",
	}
	for n, w := range want {
		if got := hex.EncodeToString(ackFrame(n)); got != w {
			t.Errorf("ACK %d: %s, want %s", n, got, w)
		}
	}
}

// TestTimings: dfu.js's sleeps, compared with what Node computes for them.
func TestTimings(t *testing.T) {
	for _, c := range []struct {
		size int
		ms   float64
	}{
		{440924, 9745.67724609375}, // RAK4631 (5.4)
		{360768, 7990.3078125},     // Xiao nRF52 (proto-nrf52/out/xiao-drop-ack:1.jsonl)
		{40000, 965.6765625},
		{1028, 500}, // the floor
		{4096, 500},
	} {
		got := float64(EraseWait(c.size)) / float64(time.Millisecond)
		if math.Abs(got-c.ms) > 1e-6 {
			t.Errorf("EraseWait(%d) = %.9f ms, want %.9f", c.size, got, c.ms)
		}
	}
	if got := PageWriteWait(); got != 102400*time.Microsecond {
		t.Errorf("PageWriteWait = %v", got)
	}
}

// ackPort is a port the test feeds bytes into by hand.
func ackPort(t *testing.T, chunks ...[]byte) (*serialport.Fake, *serialport.VirtualClock) {
	t.Helper()
	clock := serialport.NewVirtualClock(nil)
	f := serialport.NewFake(nil, clock, nil, serialport.Options{})
	for _, c := range chunks {
		f.Send(c)
	}
	return f, clock
}

func TestAcks(t *testing.T) {
	t.Run("stale bytes before the frame are skipped", func(t *testing.T) {
		p, _ := ackPort(t, []byte{0x12, 0x34}, ackFrame(2))
		a := newAcks(p)
		if n, err := a.next(); n != 2 || err != nil {
			t.Fatalf("%d, %v", n, err)
		}
	})
	t.Run("the first is taken whatever its number, then each must follow", func(t *testing.T) {
		p, _ := ackPort(t, ackFrame(5), ackFrame(6), ackFrame(0))
		a := newAcks(p)
		if n, err := a.next(); n != 5 || err != nil {
			t.Fatalf("%d, %v", n, err)
		}
		if n, err := a.next(); n != 6 || err != nil {
			t.Fatalf("%d, %v", n, err)
		}
		if _, err := a.next(); !errors.Is(err, ErrBadAck) || !strings.Contains(err.Error(), "expected 7, got 0") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("two in one read: the second is kept for the next", func(t *testing.T) {
		p, _ := ackPort(t, append(ackFrame(2), ackFrame(3)...))
		a := newAcks(p)
		for _, want := range []int{2, 3} {
			if n, err := a.next(); n != want || err != nil {
				t.Fatalf("%d, %v; want %d", n, err, want)
			}
		}
	})
	t.Run("split across reads", func(t *testing.T) {
		f := ackFrame(2)
		p, clock := ackPort(t, f[:2])
		sent := false
		// The rest arrives while the second read waits.
		clock.OnTick(func(time.Time) {
			if !sent {
				sent = true
				p.Send(f[2:])
			}
		})
		if n, err := newAcks(p).next(); n != 2 || err != nil || !sent {
			t.Fatalf("%d, %v (rest sent: %v)", n, err, sent)
		}
	})
	t.Run("a duplicate is skipped only after a resend", func(t *testing.T) {
		p, _ := ackPort(t, ackFrame(2), ackFrame(2), ackFrame(3))
		a := newAcks(p)
		a.next()
		a.strays = 1 // the frame was sent twice
		if n, err := a.next(); n != 3 || err != nil {
			t.Fatalf("%d, %v", n, err)
		}
		p2, _ := ackPort(t, ackFrame(2), ackFrame(2))
		b := newAcks(p2)
		b.next()
		if _, err := b.next(); !errors.Is(err, ErrBadAck) {
			t.Fatalf("without a resend a repeated ACK is wrong: %v", err)
		}
	})
	t.Run("incomplete", func(t *testing.T) {
		p, _ := ackPort(t, []byte{0xC0, 0x10, 0xC0})
		if _, err := newAcks(p).next(); !errors.Is(err, ErrBadAck) || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad escape", func(t *testing.T) {
		p, _ := ackPort(t, []byte{0xC0, 0xDB, 0x01, 0x00, 0xC0})
		if _, err := newAcks(p).next(); !errors.Is(err, ErrBadAck) || !strings.Contains(err.Error(), "escape") {
			t.Fatalf("got %v", err)
		}
		p, _ = ackPort(t, []byte{0xC0, 0x10, 0xDB, 0xC0})
		if _, err := newAcks(p).next(); !errors.Is(err, ErrBadAck) || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("only the acknowledgement's exact form counts", func(t *testing.T) {
		for n := 0; n < 8; n++ {
			p, _ := ackPort(t, ackFrame(n))
			if got, err := newAcks(p).next(); got != n || err != nil {
				t.Errorf("ACK %d: %d, %v", n, got, err)
			}
		}
		for name, f := range map[string][]byte{
			"an echoed START frame":   encodeFrame(1, startPayload(1028)),
			"an echoed STOP frame":    encodeFrame(6, stopPayload()),
			"a reliable-packet bit":   {0xC0, 0x90, 0, 0, 0x70, 0xC0},
			"a sequence number":       {0xC0, 0x11, 0, 0, 0xEF, 0xC0},
			"a length":                {0xC0, 0x10, 0x10, 0, 0xE0, 0xC0},
			"a bad checksum":          {0xC0, 0x10, 0, 0, 0xF1, 0xC0},
			"three bytes":             {0xC0, 0x10, 0, 0xF0, 0xC0},
			"five bytes":              {0xC0, 0x10, 0, 0, 0xF0, 0, 0xC0},
			"console text with 0xC0s": append(append([]byte{0xC0}, "> ok\r\n"...), 0xC0),
		} {
			p, _ := ackPort(t, f)
			if _, err := newAcks(p).next(); !errors.Is(err, ErrBadAck) || !strings.Contains(err.Error(), "is this the bootloader") {
				t.Errorf("%s: got %v", name, err)
			}
		}
	})
	t.Run("escaped bytes decode", func(t *testing.T) {
		d, err := slipDecode([]byte{0xDB, 0xDC, 0xDB, 0xDD, 0x01})
		if err != nil || hex.EncodeToString(d) != "c0db01" {
			t.Fatalf("%x, %v", d, err)
		}
	})
	t.Run("a port that chatters without acknowledging", func(t *testing.T) {
		p, _ := ackPort(t, []byte(strings.Repeat("MeshCore repeater console text\r\n", 200)))
		if _, err := newAcks(p).next(); !errors.Is(err, ErrBadAck) || !strings.Contains(err.Error(), "is this the bootloader") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("nothing: a timeout after 5 s", func(t *testing.T) {
		p, clock := ackPort(t)
		start := clock.Now()
		if _, err := newAcks(p).next(); !errors.Is(err, serialport.ErrTimeout) {
			t.Fatalf("got %v", err)
		}
		if d := clock.Now().Sub(start); d != AckTimeout {
			t.Errorf("waited %v", d)
		}
	})
}
