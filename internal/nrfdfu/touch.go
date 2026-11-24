package nrfdfu

// The 1200-baud touch is ported from the web flasher's lib/dfu.js forceDfuMode (flasher.meshcore.dev@45a5257, MIT,
// Copyright (c) 2025 Rastislav Vysoky), whose constants come from adafruit-nrfutil's dfu_transport_serial.py
// (BSD-3-Clause, Copyright (c) 2015 Nordic Semiconductor ASA), modified as Touch says.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"meshflash/internal/serialport"
)

// The 1200-baud touch's numbers, from dfu.js (DFU_TOUCH_BAUD, SERIAL_PORT_OPEN_WAIT_TIME, TOUCH_RESET_WAIT_TIME,
// dfu.js:4-6), which has them from nrfutil.
const (
	// TouchBaud is the line speed that, with DTR dropped, tells MeshCore's app to reboot into the serial DFU
	// bootloader (TinyUSB's tud_cdc_line_state_cb, docs/meshcore-flashing.md 5.2).
	TouchBaud = 1200
	// TouchOpenWait is how long the port stays open at 1200 baud before DTR drops.
	TouchOpenWait = 100 * time.Millisecond
	// TouchResetWait is how long dfu.js waits after the close for the board to reset into its bootloader.
	TouchResetWait = 1500 * time.Millisecond
)

// Touch is the 1200-baud touch that sends a board running MeshCore into its serial DFU bootloader: open the app's
// port exclusively at 1200 baud, wait 100 ms, drop DTR, close, wait 1.5 s (dfu.js's forceDfuMode, dfu.js:270-284,
// done the Linux way of docs/meshcore-flashing.md 5.2).
//
// The departure: dfu.js never touches DTR and relies on the close dropping it. On Linux the close drops DTR only
// when it is the port's last close (tty_port_close_start counts the openers); if ModemManager, a console or anything
// else still has the port open, DTR stays up and the touch silently does nothing. So Touch opens with TIOCEXCL,
// which keeps anyone else from opening the port meanwhile, and drops DTR itself while the line is at 1200 baud,
// which reaches the board whoever else holds the port. (Source analysis only: UNVERIFIED on hardware.)
//
// The board resets as DTR drops, so the drop or the close may find the device gone: that is the touch working, not
// an error. Touch fails only when the port can't be opened, or DTR can't be dropped for another reason (the port is
// still closed and the full wait made, so the board may have reset all the same: look for the bootloader anyway).
//
// After a touch the port's saved speed is 1200 baud (Linux keeps termios per tty, and cdc-acm doesn't reset it).
// Closing a port at 1200 baud is what reboots MeshCore into DFU, so every later open must set its speed:
// serialport.Open always does, and the DFU session opens at DFUBaud. A board already in its bootloader ignores
// the touch (the bootloader has no line-state handler): use it as it is.
func Touch(open serialport.Opener, path string, clock serialport.Clock) error {
	port, err := open(path, serialport.Options{Baud: TouchBaud, Exclusive: true})
	if err != nil {
		return fmt.Errorf("nrfdfu: the 1200-baud touch couldn't open %s: %w", path, err)
	}
	clock.Sleep(TouchOpenWait)
	dtrErr := port.SetDTR(false)
	// Close whatever happened: the close at 1200 baud is itself the touch's fallback (HUPCL drops DTR on the last
	// close), and a port must never be left open at 1200 baud. Its error is of no use: the board resetting under
	// it is the expected outcome.
	_ = port.Close()
	clock.Sleep(TouchResetWait)
	if dtrErr != nil && !errors.Is(dtrErr, serialport.ErrGone) {
		return fmt.Errorf("nrfdfu: the 1200-baud touch couldn't drop DTR on %s: %w", path, dtrErr)
	}
	return nil
}

// How long to wait for a port, and how often to look (SPEC.md 6.3; docs/meshcore-flashing.md 5.2 and 5.7).
const (
	// PollInterval is how often WaitForPort looks (5.2: every 100-250 ms).
	PollInterval = 250 * time.Millisecond
	// BootloaderWait is how long to wait for the bootloader's port after a touch (PlatformIO waits 5 s).
	BootloaderWait = 10 * time.Second
	// AppWait is how long to wait for the app's port after STOP: the bootloader checks the image, then starts it.
	AppWait = 15 * time.Second
)

// ErrPortTimeout means the port waited for didn't appear in time.
var ErrPortTimeout = errors.New("nrfdfu: the board's port didn't appear in time")

// WaitForPort looks for a port every PollInterval until find says it is there, or within has passed
// (BootloaderWait after a touch, AppWait after STOP). find is the caller's: it matches the
// board by its USB serial number and what it is running (internal/usbid), never by tty name alone, because the port
// can come back under another name. find returns ok false while the port isn't there; an error from it ends the
// wait at once. WaitForPort returns the path find gave, or ErrPortTimeout. ctx ends the wait early.
func WaitForPort(ctx context.Context, find func() (path string, ok bool, err error), clock serialport.Clock, within time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	start := clock.Now()
	for {
		path, ok, err := find()
		if err != nil {
			return "", err
		}
		if ok {
			return path, nil
		}
		left := within - clock.Now().Sub(start)
		if left <= 0 {
			return "", fmt.Errorf("%w (waited %v)", ErrPortTimeout, within)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		clock.Sleep(min(PollInterval, left))
	}
}
