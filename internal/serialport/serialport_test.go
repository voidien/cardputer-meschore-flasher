package serialport

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// echo is a fake board that sends back what it gets, and says "boot" when DTR drops with RTS up.
type echo struct{ lines [][3]any }

func (e *echo) Receive(b []byte) []byte { return b }
func (e *echo) Lines(dtr, rts bool, baud int) []byte {
	e.lines = append(e.lines, [3]any{dtr, rts, baud})
	if !dtr && rts {
		return []byte("boot")
	}
	return nil
}

func TestFakeRecordsEverything(t *testing.T) {
	log := &Log{}
	clock := NewVirtualClock(log)
	dev := &echo{}
	p := NewFake(dev, clock, log, Options{Baud: 115200})
	p.Write([]byte{1, 2})
	p.Write([]byte{3})
	buf := make([]byte, 8)
	n, err := p.Read(buf, time.Second)
	if err != nil || !bytes.Equal(buf[:n], []byte{1, 2, 3}) {
		t.Fatalf("read %x, %v", buf[:n], err)
	}
	p.SetDTR(false)
	clock.Sleep(100 * time.Millisecond)
	n, _ = p.Read(buf, time.Second)
	if string(buf[:n]) != "boot" {
		t.Fatalf("after DTR dropped: %q", buf[:n])
	}
	start := clock.Now()
	if _, err := p.Read(buf, 5*time.Second); !errors.Is(err, ErrTimeout) {
		t.Fatalf("empty read: %v", err)
	}
	if got := clock.Now().Sub(start); got != 5*time.Second {
		t.Fatalf("a timed-out read took %v of virtual time, want 5s", got)
	}
	p.Close()
	if _, err := p.Write([]byte{1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	// The echo answers each write at once, so sends and replies interleave.
	want := `{"ev":"open","baud":115200}
{"ev":"tx","hex":"0102"}
{"ev":"rx","hex":"0102"}
{"ev":"tx","hex":"03"}
{"ev":"rx","hex":"03"}
{"ev":"dtr","on":false}
{"ev":"rx","hex":"626f6f74"}
{"ev":"sleep","ms":100}
{"ev":"close"}
`
	if got := log.JSONL(); got != want {
		t.Fatalf("log:\n%s\nwant:\n%s", got, want)
	}
	if !bytes.Equal(log.TX(), []byte{1, 2, 3}) {
		t.Fatalf("TX %x", log.TX())
	}
	if last := dev.lines[len(dev.lines)-1]; last != [3]any{false, false, 115200} {
		t.Fatalf("close should drop DTR and RTS: %v", last)
	}
}

func TestFakeUnplug(t *testing.T) {
	clock := NewVirtualClock(nil)
	p := NewFake(&echo{}, clock, nil, Options{})
	p.Unplug()
	if _, err := p.Write([]byte{1}); !errors.Is(err, ErrGone) {
		t.Fatalf("write after unplug: %v", err)
	}
	if _, err := p.Read(make([]byte, 1), time.Second); !errors.Is(err, ErrGone) {
		t.Fatalf("read after unplug: %v", err)
	}
}

// The Linux port, on a pseudo-terminal (not a serial port): raw mode, reads with a timeout, baud changes and the
// exclusive open. Modem lines need a real UART, so they're checked on the device.
func TestLinuxOnAPty(t *testing.T) {
	master, slave := openPty(t)
	defer master.Close()
	p, err := Open(slave, Options{Baud: 115200, Exclusive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if _, err := Open(slave, Options{}); err == nil && os.Geteuid() != 0 {
		t.Error("a second open of an exclusive port should fail")
	}

	buf := make([]byte, 16)
	start := time.Now()
	if _, err := p.Read(buf, 50*time.Millisecond); !errors.Is(err, ErrTimeout) {
		t.Fatalf("read with nothing sent: %v", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("the read didn't wait")
	}
	// Raw mode: CR and LF pass unchanged both ways.
	master.Write([]byte("ver\r\n"))
	n, err := p.Read(buf, time.Second)
	if err != nil || string(buf[:n]) != "ver\r\n" {
		t.Fatalf("read %q, %v", buf[:n], err)
	}
	if _, err := p.Write([]byte("  -> OK\r\n")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 16)
	m, _ := master.Read(got)
	if string(got[:m]) != "  -> OK\r\n" {
		t.Fatalf("the other end got %q", got[:m])
	}
	if err := p.SetBaud(1200); err != nil {
		t.Fatal(err)
	}
	if err := p.SetBaud(12345); err == nil {
		t.Fatal("an unsupported baud rate should fail")
	}
	if err := p.FlushInput(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(buf, time.Millisecond); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open("/dev/does-not-exist-meshflash", Options{}); !errors.Is(err, ErrGone) {
		t.Fatalf("missing device: %v", err)
	}
	if _, err := Open("/dev/null", Options{Baud: 7}); err == nil {
		t.Fatal("bad baud rate accepted")
	}
}

func openPty(t *testing.T) (*os.File, string) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pseudo-terminals here:", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		m.Close()
		t.Skip("can't unlock the pty:", err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		m.Close()
		t.Skip("no pty number:", err)
	}
	// The master's own termios would echo; make it raw too so the test sees exact bytes.
	if tio, err := unix.IoctlGetTermios(int(m.Fd()), unix.TCGETS); err == nil {
		tio.Lflag &^= unix.ECHO | unix.ICANON
		tio.Oflag &^= unix.OPOST
		unix.IoctlSetTermios(int(m.Fd()), unix.TCSETS, tio)
	}
	return m, "/dev/pts/" + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
