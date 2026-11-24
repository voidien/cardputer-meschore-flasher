//go:build linux

package serialport

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// bauds are the speeds the protocols use, as termios constants.
var bauds = map[int]uint32{
	1200:   unix.B1200,
	9600:   unix.B9600,
	115200: unix.B115200,
	230400: unix.B230400,
	460800: unix.B460800,
	921600: unix.B921600,
}

// tty is a serial device opened non-blocking, in raw mode.
type tty struct {
	mu     sync.Mutex
	fd     int
	path   string
	closed bool
}

// Open opens a serial device in raw mode (8N1, no flow control, no echo, no CR/LF translation), at o.Baud
// (default 115200). Linux raises DTR and RTS on open; HUPCL stays set, so the last close drops them.
func Open(path string, o Options) (Port, error) {
	if o.Baud == 0 {
		o.Baud = 115200
	}
	speed, ok := bauds[o.Baud]
	if !ok {
		return nil, fmt.Errorf("serial: unsupported baud rate %d", o.Baud)
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, openError(path, err)
	}
	t := &tty{fd: fd, path: path}
	if o.Exclusive {
		if err := unix.IoctlSetInt(fd, unix.TIOCEXCL, 0); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("serial: %s: exclusive open: %w", path, err)
		}
	}
	if err := t.configure(speed); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return t, nil
}

// openError maps the errors a person can act on.
func openError(path string, err error) error {
	switch {
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return fmt.Errorf("%w: %s", ErrPermission, path)
	case errors.Is(err, unix.EBUSY):
		return fmt.Errorf("%w: %s", ErrBusy, path)
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENODEV), errors.Is(err, unix.ENXIO), errors.Is(err, unix.EIO):
		return fmt.Errorf("%w: %s", ErrGone, path)
	}
	return &os.PathError{Op: "open", Path: path, Err: err}
}

// configure is cfmakeraw plus CLOCAL, CREAD, HUPCL and the speed.
func (t *tty) configure(speed uint32) error {
	tio, err := unix.IoctlGetTermios(t.fd, unix.TCGETS)
	if err != nil {
		return t.ioError("read the line settings", err)
	}
	tio.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL |
		unix.IXON | unix.IXOFF | unix.IXANY
	tio.Oflag &^= unix.OPOST
	tio.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	tio.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB | unix.CRTSCTS | unix.CBAUD
	tio.Cflag |= unix.CS8 | unix.CLOCAL | unix.CREAD | unix.HUPCL | speed
	tio.Ispeed = speed
	tio.Ospeed = speed
	tio.Cc[unix.VMIN] = 0
	tio.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(t.fd, unix.TCSETS, tio); err != nil {
		return t.ioError("set the line settings", err)
	}
	return nil
}

func (t *tty) ioError(what string, err error) error {
	if errors.Is(err, unix.EIO) || errors.Is(err, unix.ENODEV) || errors.Is(err, unix.ENXIO) {
		return fmt.Errorf("%w: %s: %s", ErrGone, t.path, what)
	}
	return fmt.Errorf("serial: %s: %s: %w", t.path, what, err)
}

func (t *tty) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, ErrClosed
	}
	written := 0
	for written < len(p) {
		n, err := unix.Write(t.fd, p[written:])
		if n > 0 {
			written += n
		}
		switch {
		case err == nil:
		case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
			// The output buffer is full: wait until it drains (5 s is far longer than any packet takes).
			if perr := t.poll(unix.POLLOUT, 5*time.Second); perr != nil {
				return written, perr
			}
		default:
			return written, t.ioError("write", err)
		}
	}
	return written, nil
}

func (t *tty) Read(p []byte, timeout time.Duration) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, ErrClosed
	}
	deadline := time.Now().Add(timeout)
	for {
		n, err := unix.Read(t.fd, p)
		switch {
		case n > 0:
			return n, nil
		case err == nil:
			// 0 bytes with no error: nothing waiting in non-blocking mode on some drivers; wait as for EAGAIN.
		case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
		default:
			return 0, t.ioError("read", err)
		}
		left := time.Until(deadline)
		if left <= 0 {
			return 0, ErrTimeout
		}
		if err := t.poll(unix.POLLIN, left); err != nil {
			return 0, err
		}
	}
}

// poll waits for the event, returning ErrTimeout when it doesn't come and ErrGone when the device has gone.
func (t *tty) poll(events int16, d time.Duration) error {
	ms := int(d / time.Millisecond)
	if ms <= 0 {
		ms = 1
	}
	fds := []unix.PollFd{{Fd: int32(t.fd), Events: events}}
	for {
		n, err := unix.Poll(fds, ms)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return t.ioError("wait", err)
		}
		if n == 0 {
			return ErrTimeout
		}
		if fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 && fds[0].Revents&events == 0 {
			return fmt.Errorf("%w: %s", ErrGone, t.path)
		}
		return nil
	}
}

func (t *tty) SetBaud(baud int) error {
	speed, ok := bauds[baud]
	if !ok {
		return fmt.Errorf("serial: unsupported baud rate %d", baud)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	return t.configure(speed)
}

func (t *tty) SetDTR(on bool) error { return t.modem(unix.TIOCM_DTR, on) }
func (t *tty) SetRTS(on bool) error { return t.modem(unix.TIOCM_RTS, on) }

func (t *tty) modem(bit int, on bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	req := uint(unix.TIOCMBIC)
	if on {
		req = unix.TIOCMBIS
	}
	if err := unix.IoctlSetPointerInt(t.fd, req, bit); err != nil {
		return t.ioError("set a modem line", err)
	}
	return nil
}

func (t *tty) FlushInput() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if err := unix.IoctlSetInt(t.fd, unix.TCFLSH, unix.TCIFLUSH); err != nil {
		return t.ioError("flush", err)
	}
	return nil
}

func (t *tty) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	t.closed = true
	return unix.Close(t.fd)
}
