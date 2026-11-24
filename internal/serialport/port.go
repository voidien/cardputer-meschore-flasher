// Package serialport is the USB serial link to a LoRa board: a Port interface the flashing and setup code uses, the
// Linux implementation (raw termios, modem lines, exclusive open), and a fake with a virtual clock for tests.
//
// Everything that talks to a board takes a Port and a Clock, so tests run the same code against a fake device in
// virtual time and record every byte, line change and sleep (Log) to compare with the web flasher's own recordings.
package serialport

import (
	"errors"
	"time"
)

// Port is an open serial port.
type Port interface {
	// Write sends all of p, or returns an error.
	Write(p []byte) (int, error)
	// Read waits up to timeout for at least one byte and returns what has arrived (at most len(p)). It returns
	// ErrTimeout when nothing arrived in time. Bytes are never dropped: what isn't read stays for the next Read.
	Read(p []byte, timeout time.Duration) (int, error)
	// SetBaud changes the line speed.
	SetBaud(baud int) error
	// SetDTR and SetRTS raise (true) or drop (false) the modem control lines.
	SetDTR(on bool) error
	SetRTS(on bool) error
	// FlushInput discards bytes received and not yet read.
	FlushInput() error
	// Close releases the port. With HUPCL set (the Linux default) the last close drops DTR and RTS.
	Close() error
}

// Options say how to open a port.
type Options struct {
	Baud int // default 115200
	// Exclusive sets TIOCEXCL, so no other (non-root) process can open the port while it's held. The nRF52
	// 1200-baud touch needs it: DTR drops only on the last close.
	Exclusive bool
}

// Opener opens a port by its device path (/dev/ttyACM0). Open is the real one; tests pass a fake.
type Opener func(path string, o Options) (Port, error)

var (
	// ErrTimeout is what Read returns when nothing arrived in time.
	ErrTimeout = errors.New("serial: nothing received in time")
	// ErrGone means the device went away (unplugged, or it reset and re-enumerated).
	ErrGone = errors.New("serial: the device is gone")
	// ErrPermission means the user may not open the port: on the CardputerZero, void isn't in the dialout group.
	ErrPermission = errors.New("serial: no permission to open the port")
	// ErrBusy means another process has the port open exclusively.
	ErrBusy = errors.New("serial: the port is in use")
	// ErrClosed is returned by a Port after Close.
	ErrClosed = errors.New("serial: the port is closed")
)

// Clock is time as the protocols see it: real on the device, virtual in tests.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// RealClock is the wall clock.
var RealClock Clock = realClock{}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }
