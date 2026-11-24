package esp

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"time"

	"meshflash/internal/serialport"
)

// Stage is a step of a flash, for the Flashing screen and for saying what state a failure left the board in.
type Stage int

// The stages, in order.
const (
	StageCheck      Stage = iota // checking the image against the board chosen; nothing sent yet
	StageConnecting              // resetting into the ROM bootloader and syncing
	StageStub                    // reading the chip, uploading the flasher stub, reading the flash ID
	StageErasing                 // erasing the whole chip (full wipe only)
	StageWriting                 // writing the image
	StageVerifying               // comparing the flash's MD5 with the image's
	StageResetting               // leaving the bootloader and restarting the board
)

// String names the stage in words for the screen ("writing").
func (s Stage) String() string {
	switch s {
	case StageCheck:
		return "checking the image"
	case StageConnecting:
		return "connecting"
	case StageStub:
		return "loading the flasher stub"
	case StageErasing:
		return "erasing"
	case StageWriting:
		return "writing"
	case StageVerifying:
		return "verifying"
	case StageResetting:
		return "resetting"
	}
	return fmt.Sprintf("stage %d", int(s))
}

// Progress is reported as a flash goes.
type Progress struct {
	// Stage is the step under way.
	Stage Stage
	// Attempt is the reset being tried while connecting (1, 2, …).
	Attempt int
	// Done and Total are bytes of the image (padded to a multiple of 4) the board has acknowledged, and in all,
	// while writing.
	Done, Total int
	// Chip is the chip once it's been read.
	Chip Chip
}

// Options say how to flash. Address and EraseChip must name one of the two flashes SPEC.md 6.2 allows, an update
// (Address AppAddress, no erase) or a full wipe (Address 0x0 with EraseChip); the zero value names neither and is
// refused, so a forgotten Address can't write an app over the bootloader at 0x0.
type Options struct {
	// Clock is the time the protocol sees: serialport.RealClock (the default) on the device, a
	// serialport.VirtualClock in tests.
	Clock serialport.Clock
	// USBVendor and USBProduct are the port's USB IDs from internal/usbid's scan (0 when unknown). Product 0x1001,
	// the chip's own USB-Serial-JTAG, selects the USB-JTAG reset; like esptool-js, the vendor isn't checked.
	USBVendor, USBProduct uint16
	// ExpectChip is the chip of the board the owner chose ("" to take the image's word for it), in any form
	// ParseChip accepts. A board whose chip is different is refused with ErrWrongChip before anything is uploaded.
	// It must be the board's actual chip: the catalog's device type is "esp32" for every ESP32-family board, so
	// passing that for an ESP32-S3 board refuses it.
	ExpectChip Chip
	// Address is where the image goes: AppAddress (0x10000) for an app update, 0x0 for a full wipe's merged image.
	// No other address is accepted.
	Address uint32
	// EraseChip erases the whole chip before writing (the full wipe). It goes with Address 0x0 and only with it:
	// an image at 0x0 is the full wipe, and erasing under an app at 0x10000 would leave no bootloader.
	EraseChip bool
	// Compress makes the zlib stream; nil means ZlibCompress.
	Compress Compressor
	// Progress is told each step, on Flash's goroutine, so it must not block; nil for none.
	Progress func(Progress)
	// ConnectAttempts caps the resets tried while connecting (0 for DefaultConnectAttempts).
	ConnectAttempts int
	// ConnectTimeout caps the time spent connecting: no new reset starts after it (0 for DefaultConnectTimeout).
	ConnectTimeout time.Duration
	// Logf receives esptool-js's console messages ("Chip is …", "Writing at 0x…"), for screen.log; nil for none.
	Logf func(format string, args ...any)
}

// Result describes a flash that worked.
type Result struct {
	// Chip is what the reference's informational reads found.
	Chip ChipInfo
	// FlashID is the SPI flash's JEDEC ID as the chip reads it.
	FlashID uint32
	// FlashSize is the size FlashID gives (0 if unknown).
	FlashSize int
	// Size is the bytes written (the image padded to a multiple of 4), Compressed the bytes sent for them.
	Size, Compressed int
	// MD5 is the image's MD5, which the board's flash matched.
	MD5 [16]byte
}

// FlashError is a flash that stopped: at which stage, how much of the image the board had acknowledged, and
// whether the flash may have changed. Recovery says what to do about it.
type FlashError struct {
	// Stage is the step that failed.
	Stage Stage
	// Written and Total are bytes of the (padded) image acknowledged by the board, and in all.
	Written, Total int
	// Touched says an erase or a data block was sent: the old firmware may be gone.
	Touched bool
	// Address is where the image was going, which decides what a partial write broke.
	Address uint32
	// EraseChip says the chip was to be erased first (the full wipe).
	EraseChip bool
	// Err is what went wrong.
	Err error
}

// Error says at which stage the flash stopped, and why.
func (e *FlashError) Error() string {
	if e.Stage == StageWriting && e.Total > 0 {
		return fmt.Sprintf("esp: stopped while writing (%d of %d bytes): %v", e.Written, e.Total, e.Err)
	}
	return fmt.Sprintf("esp: stopped while %s: %v", e.Stage, e.Err)
}

// Unwrap gives the cause, for errors.Is (ErrNoBootloader, ErrVerify, serialport.ErrGone, …).
func (e *FlashError) Unwrap() error { return e.Err }

// Recovery says, in plain words, what state the board is in and how to get it working again
// (docs/meshcore-flashing.md 10.1). The ROM bootloader is in mask ROM, so an ESP32 can always be flashed again.
func (e *FlashError) Recovery() string {
	const bootDance = "hold BOOT, press and release RESET, release BOOT"
	switch {
	case e.Stage == StageCheck:
		return "Nothing was sent to the board: it still has its old firmware."
	case errors.Is(e.Err, ErrNoBootloader):
		return "Flash untouched. The board didn't start its bootloader: " + bootDance + ", then try again."
	case !e.Touched && (errors.Is(e.Err, ErrWrongChip) || errors.Is(e.Err, ErrUnsupportedChip) || errors.Is(e.Err, ErrTooBig)):
		// Trying the same file on the same board again would fail the same way.
		return "Flash untouched: the old firmware is still there. This file can't go on this board, so trying again won't help: choose the board and the file that match it."
	case !e.Touched:
		return "Flash untouched: the old firmware is still there. Reset the board (or unplug it and plug it back in) and try again."
	case e.Stage == StageResetting:
		return "Written and checked, but the board didn't restart: press its RESET button, or unplug it and plug it back in."
	case e.Stage == StageVerifying && errors.Is(e.Err, ErrVerify):
		return "Written, but the flash doesn't match the file, so the board may not start. Flash it again; if it doesn't connect, " + bootDance + " first."
	case e.Stage == StageVerifying:
		return "Written, but not checked: the board stopped answering. Flash it again; if it doesn't connect, " + bootDance + " first."
	case e.EraseChip || e.Address < AppAddress:
		return "Partially written: the chip was erased or its bootloader overwritten, so the board can't start until it's flashed again. " +
			"To recover, " + bootDance + ", then flash the full wipe again."
	default:
		return "Partially written: the bootloader and the board's settings are intact, but the app is incomplete, so the board won't start it. " +
			"To recover, " + bootDance + ", then flash the update again. Nothing else is lost."
	}
}

// Flash writes image to the board on port at o.Address, as the web flasher's flashEsp32 does, with this package's
// departures (the package comment): it checks the image first, guards the chip, verifies with MD5, and ends with
// the reference's hard reset. The caller opened port at 115200 baud and closes it afterwards. Every error is a
// *FlashError.
//
// It ports js/flash.js flashEsp32 (MIT, Copyright (c) 2025 Rastislav Vysoky) and the esptool-js 0.5.4 calls that
// makes (ESPLoader.main, flashId, writeFlash, after; src/esploader.ts, Apache-2.0, Copyright 2019 Espressif Systems
// (Shanghai) CO LTD; modified as the package comment lists).
func Flash(ctx context.Context, port serialport.Port, image []byte, o Options) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fe := &FlashError{Stage: StageCheck, Address: o.Address, EraseChip: o.EraseChip}
	fail := func(err error) (*Result, error) {
		fe.Err = err
		return nil, fe
	}

	// The file, before anything touches the board. First the two flashes SPEC.md 6.2 allows: the image header
	// alone can't stop an app going to 0x0, because an ESP32-S3 or ESP32-C3 app starts with a header just as the
	// merged image's bootloader does (ImageChip), so the address and the erase must ask for the full wipe together.
	switch {
	case o.Address == AppAddress && !o.EraseChip, o.Address == 0 && o.EraseChip:
	case o.EraseChip:
		return fail(fmt.Errorf("%w: a full erase goes only with a merged image at 0x0, not at 0x%x", ErrImage, o.Address))
	case o.Address == 0:
		return fail(fmt.Errorf("%w: an image at 0x0 is a full wipe and goes with a chip erase; an update goes at 0x%x", ErrImage, AppAddress))
	default:
		return fail(fmt.Errorf("%w: images go at 0x%x (update) or 0x0 (full wipe), not at 0x%x", ErrImage, AppAddress, o.Address))
	}
	imgChip, err := ImageChip(image, o.Address)
	if err != nil {
		return fail(err)
	}
	if c, ok := ParseChip(string(o.ExpectChip)); ok {
		o.ExpectChip = c // "ESP32-S3" and "esp32s3" name the same chip
	}
	if o.ExpectChip != "" && imgChip != o.ExpectChip {
		return fail(fmt.Errorf("%w: the file is for the %s, the board chosen has an %s", ErrWrongChip, imgChip, o.ExpectChip))
	}
	if !imgChip.Supported() {
		return fail(fmt.Errorf("%w: %s", ErrUnsupportedChip, imgChip))
	}
	padded := padImage(image)
	fe.Total = len(padded)
	sum := md5.Sum(padded)
	compress := o.Compress
	if compress == nil {
		compress = ZlibCompress
	}
	z, err := compress(padded)
	if err != nil {
		return fail(fmt.Errorf("%w: %v", ErrCompress, err))
	}
	marks, err := inflateMarks(z, padded)
	if err != nil {
		return fail(err)
	}

	l := NewLoader(ctx, port, o)
	if o.USBVendor != 0 || o.USBProduct != 0 {
		l.logf("Serial port USB VendorID 0x%x ProductID 0x%x", o.USBVendor, o.USBProduct)
	}
	failBoard := func(err error) (*Result, error) {
		fe.Touched = l.touched
		// flash.js pulses RTS whenever writing fails, so the board leaves the bootloader; here that's done for any
		// failure once the bootloader has answered, unless the board is gone.
		if l.synced && !errors.Is(err, serialport.ErrGone) && !errors.Is(err, serialport.ErrClosed) {
			_ = l.pulseRTS()
		}
		return fail(err)
	}

	fe.Stage = StageConnecting
	if err := l.Connect(); err != nil {
		return failBoard(err)
	}

	fe.Stage = StageStub
	l.progress(Progress{Stage: StageStub})
	chip, err := l.DetectChip()
	if err != nil {
		return failBoard(err)
	}
	if chip != imgChip {
		return failBoard(fmt.Errorf("%w: the board has an %s, the file is for the %s", ErrWrongChip, chip, imgChip))
	}
	l.progress(Progress{Stage: StageStub})
	info, err := l.ReadChipInfo()
	if err != nil {
		return failBoard(err)
	}
	if err := l.RunStub(); err != nil {
		return failBoard(err)
	}
	id, err := l.FlashID()
	if err != nil {
		return failBoard(err)
	}
	size := FlashSize(id)
	if size > 0 && int64(o.Address)+int64(len(padded)) > int64(size) {
		return failBoard(fmt.Errorf("%w: %d bytes at 0x%x, but the flash is %d MB", ErrTooBig, len(padded), o.Address, size>>20))
	}

	if o.EraseChip {
		fe.Stage = StageErasing
		l.progress(Progress{Stage: StageErasing})
		if err := l.EraseFlash(); err != nil {
			return failBoard(err)
		}
	}

	fe.Stage = StageWriting
	l.progress(Progress{Stage: StageWriting, Total: len(padded)})
	err = l.WriteDeflated(o.Address, len(padded), z, marks, func(n int) {
		fe.Written = n
		l.progress(Progress{Stage: StageWriting, Done: n, Total: len(padded)})
	})
	if err != nil {
		return failBoard(err)
	}

	fe.Stage = StageVerifying
	l.progress(Progress{Stage: StageVerifying})
	got, err := l.FlashMD5(o.Address, len(padded))
	if err != nil {
		return failBoard(err)
	}
	if got != sum {
		l.logf("File  md5: %x", sum)
		l.logf("Flash md5: %x", got)
		return failBoard(fmt.Errorf("%w: the flash's MD5 is %x, the file's %x", ErrVerify, got, sum))
	}
	l.logf("Hash of data verified.")

	fe.Stage = StageResetting
	l.progress(Progress{Stage: StageResetting})
	if err := l.LeaveFlashMode(); err != nil {
		return failBoard(err)
	}
	if err := l.HardReset(); err != nil {
		return failBoard(err)
	}
	return &Result{Chip: info, FlashID: id, FlashSize: size, Size: len(padded), Compressed: len(z), MD5: sum}, nil
}
