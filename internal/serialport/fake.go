package serialport

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// VirtualClock is a Clock for tests: Sleep moves it forward at once, so a 10 s erase wait takes no real time, and
// every sleep is recorded in the Log (when one is set).
type VirtualClock struct {
	mu  sync.Mutex
	now time.Time
	log *Log
	// tickers are told whenever time moves (a fake board rebooting, a bootloader timing out).
	tickers []func(now time.Time)
}

// NewVirtualClock starts at a fixed instant, so recordings are the same on every run.
func NewVirtualClock(log *Log) *VirtualClock {
	return &VirtualClock{now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), log: log}
}

func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep records the sleep and moves time forward.
func (c *VirtualClock) Sleep(d time.Duration) {
	if c.log != nil {
		c.log.add(Event{Ev: "sleep", Ms: float64(d) / float64(time.Millisecond)})
	}
	c.Advance(d)
}

// Advance moves time forward without recording a sleep (time spent waiting in a Read).
func (c *VirtualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	tickers := append([]func(time.Time){}, c.tickers...)
	c.mu.Unlock()
	for _, t := range tickers {
		t(now)
	}
}

// OnTick calls f with the new time whenever the clock moves.
func (c *VirtualClock) OnTick(f func(now time.Time)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tickers = append(c.tickers, f)
}

// Event is one thing that happened on a fake port, in the same spirit as the Node recordings of the web flasher
// (reference/): open, close, baud, dtr, rts, tx (host to board), rx (board to host), sleep, note.
type Event struct {
	Ev   string  `json:"ev"`
	Baud int     `json:"baud,omitempty"`
	On   *bool   `json:"on,omitempty"`
	Hex  string  `json:"hex,omitempty"`
	Ms   float64 `json:"ms,omitempty"`
	Text string  `json:"text,omitempty"`
}

// Log records events in order. It's safe for concurrent use.
type Log struct {
	mu     sync.Mutex
	events []Event
}

func (l *Log) add(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Merge consecutive tx (or rx) chunks: how writes are split is not part of the protocol.
	if n := len(l.events); n > 0 && (e.Ev == "tx" || e.Ev == "rx") && l.events[n-1].Ev == e.Ev {
		l.events[n-1].Hex += e.Hex
		return
	}
	l.events = append(l.events, e)
}

// Note adds a free-text marker (for reading a recording, not for comparing).
func (l *Log) Note(s string) { l.add(Event{Ev: "note", Text: s}) }

// Events is a copy of everything recorded so far.
func (l *Log) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event(nil), l.events...)
}

// TX is every byte the host wrote, in order.
func (l *Log) TX() []byte { return l.bytes("tx") }

// RX is every byte the board sent, in order.
func (l *Log) RX() []byte { return l.bytes("rx") }

func (l *Log) bytes(kind string) []byte {
	var out []byte
	for _, e := range l.Events() {
		if e.Ev == kind {
			b, _ := hex.DecodeString(e.Hex)
			out = append(out, b...)
		}
	}
	return out
}

// JSONL is the log as one JSON object per line, for testdata and for reading.
func (l *Log) JSONL() string {
	var b strings.Builder
	for _, e := range l.Events() {
		j, _ := json.Marshal(e)
		b.Write(j)
		b.WriteByte('\n')
	}
	return b.String()
}

// Device is the board at the other end of a fake port. A fake ROM bootloader, nRF52 bootloader or MeshCore console
// implements it.
type Device interface {
	// Receive takes bytes the host wrote and returns what the board sends back (nil for nothing).
	Receive(b []byte) []byte
	// Lines is told the line state after any change of DTR, RTS or baud (and on open), and returns what the board
	// sends back (a boot banner after a reset into the bootloader, say).
	Lines(dtr, rts bool, baud int) []byte
}

// Fake is a Port connected to a fake Device, in virtual time.
type Fake struct {
	mu     sync.Mutex
	dev    Device
	clock  *VirtualClock
	log    *Log
	in     []byte
	dtr    bool
	rts    bool
	baud   int
	closed bool
	gone   bool
}

// NewFake opens a fake port to dev. Like Linux, opening raises DTR and RTS. log may be nil.
func NewFake(dev Device, clock *VirtualClock, log *Log, o Options) *Fake {
	if o.Baud == 0 {
		o.Baud = 115200
	}
	if log == nil {
		log = &Log{}
	}
	f := &Fake{dev: dev, clock: clock, log: log, dtr: true, rts: true, baud: o.Baud}
	log.add(Event{Ev: "open", Baud: o.Baud})
	f.lines()
	return f
}

// Log is where this port records.
func (f *Fake) Log() *Log { return f.log }

// Send queues bytes from the board to the host, as if the board sent them by itself.
func (f *Fake) Send(b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue(b)
}

func (f *Fake) queue(b []byte) {
	if len(b) == 0 || f.gone || f.closed {
		return
	}
	f.in = append(f.in, b...)
	f.log.add(Event{Ev: "rx", Hex: hex.EncodeToString(b)})
}

// Unplug makes every later call fail with ErrGone, as a board pulled out of the USB port does.
func (f *Fake) Unplug() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.gone {
		f.gone = true
		f.log.add(Event{Ev: "note", Text: "unplugged"})
	}
}

func (f *Fake) check() error {
	if f.closed {
		return ErrClosed
	}
	if f.gone {
		return ErrGone
	}
	return nil
}

func (f *Fake) Write(p []byte) (int, error) {
	f.mu.Lock()
	if err := f.check(); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	f.log.add(Event{Ev: "tx", Hex: hex.EncodeToString(p)})
	dev := f.dev
	f.mu.Unlock()
	if dev != nil {
		reply := dev.Receive(append([]byte(nil), p...))
		f.mu.Lock()
		f.queue(reply)
		f.mu.Unlock()
	}
	return len(p), nil
}

// Read returns queued bytes at once; with none, it moves the virtual clock forward by timeout (giving the device's
// tickers a chance to send something) and returns ErrTimeout if still nothing came.
func (f *Fake) Read(p []byte, timeout time.Duration) (int, error) {
	f.mu.Lock()
	if err := f.check(); err != nil {
		f.mu.Unlock()
		return 0, err
	}
	if len(f.in) == 0 {
		f.mu.Unlock()
		f.clock.Advance(timeout)
		f.mu.Lock()
		if err := f.check(); err != nil {
			f.mu.Unlock()
			return 0, err
		}
		if len(f.in) == 0 {
			f.mu.Unlock()
			return 0, ErrTimeout
		}
	}
	n := copy(p, f.in)
	f.in = f.in[n:]
	f.mu.Unlock()
	return n, nil
}

func (f *Fake) SetBaud(baud int) error {
	f.mu.Lock()
	if err := f.check(); err != nil {
		f.mu.Unlock()
		return err
	}
	f.baud = baud
	f.log.add(Event{Ev: "baud", Baud: baud})
	f.mu.Unlock()
	f.lines()
	return nil
}

func (f *Fake) SetDTR(on bool) error { return f.setLine("dtr", on) }
func (f *Fake) SetRTS(on bool) error { return f.setLine("rts", on) }

func (f *Fake) setLine(name string, on bool) error {
	f.mu.Lock()
	if err := f.check(); err != nil {
		f.mu.Unlock()
		return err
	}
	if name == "dtr" {
		f.dtr = on
	} else {
		f.rts = on
	}
	v := on
	f.log.add(Event{Ev: name, On: &v})
	f.mu.Unlock()
	f.lines()
	return nil
}

func (f *Fake) lines() {
	f.mu.Lock()
	dev, dtr, rts, baud := f.dev, f.dtr, f.rts, f.baud
	f.mu.Unlock()
	if dev == nil {
		return
	}
	reply := dev.Lines(dtr, rts, baud)
	f.mu.Lock()
	f.queue(reply)
	f.mu.Unlock()
}

func (f *Fake) FlushInput() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return err
	}
	f.in = nil
	return nil
}

// Close drops DTR and RTS (HUPCL), tells the device, and records the close.
func (f *Fake) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return ErrClosed
	}
	wasGone := f.gone
	f.closed = true
	f.log.add(Event{Ev: "close"})
	dev, baud := f.dev, f.baud
	f.mu.Unlock()
	if dev != nil && !wasGone {
		dev.Lines(false, false, baud)
	}
	return nil
}

// State is the port's line state, for tests.
func (f *Fake) State() (dtr, rts bool, baud int, closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dtr, f.rts, f.baud, f.closed
}

// String describes the fake, for test messages.
func (f *Fake) String() string {
	dtr, rts, baud, closed := f.State()
	return fmt.Sprintf("fake port (dtr=%v rts=%v baud=%d closed=%v)", dtr, rts, baud, closed)
}
