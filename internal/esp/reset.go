package esp

import "time"

// Line control, ported from esptool-js 0.5.4 src/webserial.ts (setRTS, setDTR) and src/reset.ts (ClassicReset,
// UsbJtagSerialReset, HardReset), Apache-2.0, Copyright 2019 Espressif Systems (Shanghai) CO LTD; modified: Go,
// sleeps on the Loader's clock, the first failed line change ends the sequence. And from the web flasher's
// js/flash.js (pulseRts and the delays around after('hard_reset')), MIT, Copyright (c) 2025 Rastislav Vysoky.
//
// On the usual auto-reset circuit RTS asserted holds EN low (the chip in reset) and DTR asserted holds IO0 low (the
// download strap), so releasing EN while IO0 is low starts the ROM bootloader (docs/meshcore-flashing.md 4.3).

// usbJTAGSerialPID is the USB product ID of Espressif's USB-Serial-JTAG. esptool-js checks only the product ID,
// not the vendor, and so does this package.
const usbJTAGSerialPID = 0x1001

// lineSeq runs a sequence of line changes and sleeps, stopping at the first line change that fails (the port
// gone, say), so the sequences below read like the TypeScript.
type lineSeq struct {
	l   *Loader
	err error
}

// dtr sets DTR and remembers it, as esptool-js's Transport does.
func (s *lineSeq) dtr(on bool) {
	if s.err == nil {
		s.l.dtr = on
		s.err = s.l.port.SetDTR(on)
	}
}

// rts sets RTS, then sends DTR again at its last value: esptool-js copies esptool.py's work-around for Windows'
// usbser.sys, and the reference's line trace has that extra DTR after every RTS change.
func (s *lineSeq) rts(on bool) {
	if s.err == nil {
		s.err = s.l.port.SetRTS(on)
		s.dtr(s.l.dtr)
	}
}

func (s *lineSeq) sleep(ms int) {
	if s.err == nil {
		s.l.clock.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// classicReset is esptool-js's ClassicReset: EN low with IO0 high, then IO0 low and EN released, which boots the
// ROM bootloader; then IO0 released after delay ms (50, then 550 on the next attempt).
func (l *Loader) classicReset(delay int) error {
	s := &lineSeq{l: l}
	s.dtr(false)
	s.rts(true)
	s.sleep(100)
	s.dtr(true)
	s.rts(false)
	s.sleep(delay)
	s.dtr(false)
	return s.err
}

// usbJTAGSerialReset is esptool-js's UsbJtagSerialReset, for the chip's own USB-Serial-JTAG port, whose reset logic
// reads the same two lines in a different order.
func (l *Loader) usbJTAGSerialReset() error {
	s := &lineSeq{l: l}
	s.rts(false)
	s.dtr(false)
	s.sleep(100)
	s.dtr(true)
	s.rts(false)
	s.sleep(100)
	s.rts(true)
	s.dtr(false)
	s.rts(true)
	s.sleep(100)
	s.rts(false)
	s.dtr(false)
	return s.err
}

// hardReset is what flash.js does after writing: delay(100); after('hard_reset'), which is esptool-js's HardReset
// (sleep 100, RTS released; on its own that resets nothing, because esptool-js 0.5.4 never asserts RTS); delay(100);
// then its finally block's pulseRts: RTS asserted for 100 ms, which resets the chip with IO0 high, so the new
// firmware boots.
func (l *Loader) hardReset() error {
	s := &lineSeq{l: l}
	s.sleep(100)
	l.logf("Hard resetting via RTS pin...")
	s.sleep(100)
	s.rts(false)
	s.sleep(100)
	l.pulse(s)
	return s.err
}

// pulseRTS is flash.js's pulseRts on its own: reset the chip with IO0 high, so it starts its firmware.
func (l *Loader) pulseRTS() error {
	s := &lineSeq{l: l}
	l.pulse(s)
	return s.err
}

func (l *Loader) pulse(s *lineSeq) {
	s.rts(true)
	s.sleep(100)
	s.rts(false)
}
