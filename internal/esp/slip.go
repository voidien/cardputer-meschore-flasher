package esp

import (
	"bytes"
	"context"
	"errors"
	"time"

	"meshflash/internal/serialport"
)

// SLIP framing as the ROM bootloader and the stub speak it: a packet is sent as C0, the packet with C0 escaped as
// DB DC and DB as DB DD, then C0. Ported from esptool-js 0.5.4 src/webserial.ts (slipWriter, read, newRead),
// Apache-2.0, Copyright 2019 Espressif Systems (Shanghai) CO LTD; modified: the reading side is rewritten so that
// a read never loses bytes and skips what isn't a frame instead of throwing.
const (
	slipEnd    = 0xC0
	slipEsc    = 0xDB
	slipEscEnd = 0xDC
	slipEscEsc = 0xDD
)

// slipEncode frames one packet. esptool-js writes one frame per write, and so do we.
func slipEncode(pkt []byte) []byte {
	out := make([]byte, 0, len(pkt)+len(pkt)/32+2)
	out = append(out, slipEnd)
	for _, b := range pkt {
		switch b {
		case slipEnd:
			out = append(out, slipEsc, slipEscEnd)
		case slipEsc:
			out = append(out, slipEsc, slipEscEsc)
		default:
			out = append(out, b)
		}
	}
	return append(out, slipEnd)
}

// slipDecode undoes the escapes of one frame's contents (without its C0s). A DB followed by anything but DC or DD
// is line noise: the frame is reported as corrupt.
func slipDecode(raw []byte) ([]byte, bool) {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if b != slipEsc {
			out = append(out, b)
			continue
		}
		i++
		if i == len(raw) {
			return nil, false
		}
		switch raw[i] {
		case slipEscEnd:
			out = append(out, slipEnd)
		case slipEscEsc:
			out = append(out, slipEsc)
		default:
			return nil, false
		}
	}
	return out, true
}

// readSlice is the longest single Read, so a cancelled context is noticed while waiting for a slow reply (a chip
// erase can take a minute).
const readSlice = 250 * time.Millisecond

// errDeadline is a link-level timeout; commands turn it into ErrNoReply with what they were waiting for.
var errDeadline = errors.New("esp: deadline passed")

// link is the byte stream from the board. Every byte read stays in buf until a whole frame is taken out or the
// boot banner is set aside, so a deadline passing in the middle of a reply loses nothing: the next read carries
// on where this one stopped (the fix for esptool-js 0.5.4's bug 4.10b).
type link struct {
	ctx   context.Context
	port  serialport.Port
	clock serialport.Clock
	buf   []byte
	// noise keeps the last bytes seen outside any frame (an app's log, a panic), for error messages.
	noise []byte
}

// fill waits until deadline at most for more bytes and appends what came. It returns errDeadline when the deadline
// has passed, the context's error when it's done, and the port's error (ErrGone when unplugged).
func (k *link) fill(deadline time.Time) error {
	if err := k.ctx.Err(); err != nil {
		return err
	}
	wait := deadline.Sub(k.clock.Now())
	if wait <= 0 {
		return errDeadline
	}
	if wait > readSlice {
		wait = readSlice
	}
	var tmp [4096]byte
	n, err := k.port.Read(tmp[:], wait)
	k.buf = append(k.buf, tmp[:n]...)
	if err != nil && !errors.Is(err, serialport.ErrTimeout) {
		return err
	}
	return nil
}

// frame returns the next whole SLIP frame, unescaped, waiting until deadline at most.
func (k *link) frame(deadline time.Time) ([]byte, error) {
	for {
		if f, ok := k.nextFrame(); ok {
			return f, nil
		}
		if !k.clock.Now().Before(deadline) {
			return nil, errDeadline
		}
		if err := k.fill(deadline); err != nil {
			return nil, err
		}
	}
}

// nextFrame takes the first whole frame out of buf. Bytes before its opening C0 aren't part of any frame and are
// set aside as noise; an empty frame (C0 C0) means the second C0 opens the frame, which is how a reader that came in
// between two frames gets back in step.
func (k *link) nextFrame() ([]byte, bool) {
	for {
		i := bytes.IndexByte(k.buf, slipEnd)
		if i < 0 {
			k.skip(len(k.buf))
			return nil, false
		}
		k.skip(i)
		j := bytes.IndexByte(k.buf[1:], slipEnd)
		if j < 0 {
			return nil, false // the frame isn't all here yet
		}
		if j == 0 {
			k.buf = k.buf[1:]
			continue
		}
		raw := k.buf[1 : 1+j]
		k.buf = k.buf[2+j:]
		if f, ok := slipDecode(raw); ok {
			return f, true
		}
		k.addNoise(raw)
	}
}

func (k *link) skip(n int) {
	if n > 0 {
		k.addNoise(k.buf[:n])
		k.buf = k.buf[n:]
	}
}

func (k *link) addNoise(b []byte) {
	const keep = 512
	k.noise = append(k.noise, b...)
	if len(k.noise) > keep {
		k.noise = append([]byte(nil), k.noise[len(k.noise)-keep:]...)
	}
}

// drainBanner sets aside what the board prints after a reset (the ROM's boot banner), so the first SYNC's reply
// isn't stuck behind it. Like esptool-js it waits up to first for the board to say anything; then it reads until
// the board has been quiet for quiet, or for most cap after the first byte, so an app that never stops printing
// can't hold it up. It returns the bytes it set aside.
func (k *link) drainBanner(first, quiet, cap time.Duration) ([]byte, error) {
	start := k.clock.Now()
	deadline := start.Add(first)
	for len(k.buf) == 0 {
		err := k.fill(deadline)
		if errors.Is(err, errDeadline) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	hard := k.clock.Now().Add(cap)
	for {
		n := len(k.buf)
		until := k.clock.Now().Add(quiet)
		if until.After(hard) {
			until = hard
		}
		for len(k.buf) == n {
			err := k.fill(until)
			if errors.Is(err, errDeadline) {
				banner := k.buf
				k.buf = nil
				return banner, nil
			}
			if err != nil {
				return nil, err
			}
		}
	}
}
