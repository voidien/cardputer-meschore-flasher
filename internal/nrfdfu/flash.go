package nrfdfu

// The DFU session, ported from the web flasher's lib/dfu.js (flasher.meshcore.dev@45a5257, MIT, Copyright (c)
// 2025 Rastislav Vysoky; dfuUpdate, sendStartDfu, sendInitPacket, sendFirmware, sendPacket), which adapts
// adafruit-nrfutil's dfu_transport_serial.py (BSD-3-Clause, Copyright (c) 2015 Nordic Semiconductor). Changed as
// the package comment lists.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"meshflash/internal/serialport"
)

// DFUBaud is the speed of the DFU session. The bootloader ignores line coding, but it is what dfu.js opens with,
// and opening at it explicitly is what keeps a port from being left at 1200 baud (Touch).
const DFUBaud = 115200

// Tries is how often one frame is sent before Flash gives up: once, and twice more when no acknowledgement came
// within AckTimeout. dfu.js sends once.
const Tries = 3

// The timing constants of dfu.js (dfu.js:9-12), in seconds. They are variables, not constants, so Go computes the
// sleeps in float64 step by step as JavaScript does, and the durations match the reference's to the nanosecond.
var (
	flashPageSize      = 4096.0
	flashPageEraseTime = 0.0897   // nRF52840 max erase time per page
	flashWordWriteTime = 0.000100 // nRF52840 max write time per word
)

// msDuration turns milliseconds, as dfu.js passes them to setTimeout, into a Duration (to the nearest nanosecond).
func msDuration(ms float64) time.Duration { return time.Duration(math.Round(ms * 1e6)) }

// EraseWait is how long dfu.js waits after START's acknowledgement while the bootloader erases the app:
// max(0.5, (size/4096 + 1) × 0.0897) s, with a floating-point division (dfu.js:298-301). 9745.677 ms for a 440,924
// byte image. Browsers round the setTimeout down to whole milliseconds; this is the exact value.
func EraseWait(size int) time.Duration {
	pages := float64(float64(size)/flashPageSize) + 1
	s := math.Max(0.5, float64(pages*flashPageEraseTime))
	return msDuration(float64(s * 1000))
}

// PageWriteWait is FLASH_PAGE_WRITE_TIME: (4096/4) × 0.0001 s = 102.4 ms, the time to write one 4 KiB page. dfu.js
// sleeps it before the first DATA frame, after every 8th, and once more before STOP (dfu.js:350,362-369).
func PageWriteWait() time.Duration {
	s := float64((flashPageSize / 4) * flashWordWriteTime)
	return msDuration(float64(s * 1000))
}

// Stage is where a flash got to, for the Flashing screen and for saying what state a failure left the board in.
type Stage int

// The stages, in order.
const (
	// StageStarting: START is being sent. Until its acknowledgement nothing has been erased.
	StageStarting Stage = iota
	// StageErasing: START was acknowledged, so the bootloader is erasing the app; then the init packet is sent.
	StageErasing
	// StageWriting: the firmware is being sent, 512 bytes a frame.
	StageWriting
	// StageFinishing: the last page's write and STOP.
	StageFinishing
)

// String names the stage in plain words ("writing"), for messages.
func (s Stage) String() string {
	switch s {
	case StageStarting:
		return "starting"
	case StageErasing:
		return "erasing"
	case StageWriting:
		return "writing"
	case StageFinishing:
		return "finishing"
	}
	return fmt.Sprintf("stage %d", int(s))
}

// Progress is reported as a flash goes: at each stage, and after each DATA frame is acknowledged.
type Progress struct {
	Stage Stage
	// Wait is how long the erase is given (EraseWait), at StageErasing.
	Wait time.Duration
	// Frame and Frames are DATA frames acknowledged, and in all.
	Frame, Frames int
	// Sent and Total are firmware bytes acknowledged, and the firmware's size. (The web flasher counts wire bytes
	// and reaches 100% about 2% early, docs/meshcore-flashing.md 5.7.)
	Sent, Total int
}

// FlashError is a flash that stopped: at which stage, how much of the firmware the bootloader had acknowledged, and
// why. Recovery says what to do about it.
type FlashError struct {
	Stage Stage
	// Sent and Total are firmware bytes acknowledged, and in all.
	Sent, Total int
	// StopSent says STOP was written at least once (at StageFinishing). See MayHaveWorked.
	StopSent bool
	Err      error
}

// Error says where the flash stopped and why.
func (e *FlashError) Error() string {
	switch {
	case e.Stage == StageWriting:
		return fmt.Sprintf("nrfdfu: stopped while writing (%d of %d bytes): %v", e.Sent, e.Total, e.Err)
	case e.StopSent:
		return fmt.Sprintf("nrfdfu: stopped after sending STOP: %v", e.Err)
	}
	return fmt.Sprintf("nrfdfu: stopped while %s: %v", e.Stage, e.Err)
}

// Unwrap gives the cause, so errors.Is finds serialport.ErrGone, ErrNoAck, ErrBadAck or a context error.
func (e *FlashError) Unwrap() error { return e.Err }

// MayHaveWorked says the board vanished after STOP was written but before its acknowledgement was read. That is
// most likely a good flash: the bootloader acknowledges STOP, checks the image and, when it is good, tears down USB
// and starts the app at once (OTAFIX main.c after bootloader_dfu_start), so a resend of STOP whose first
// acknowledgement was lost finds the port gone. The caller treats it as it treats a nil error: wait for the app to
// come back (WaitForPort, AppWait) and ask its version; only if it doesn't come back is the flash a failure.
func (e *FlashError) MayHaveWorked() bool {
	return e.StopSent && errors.Is(e.Err, serialport.ErrGone)
}

// The recovery advice (docs/meshcore-flashing.md 10.2). Once START is acknowledged the bootloader has erased the app,
// and the bootloader's session state is stale, so the only way on is a fresh bootloader session: a double press of
// reset gives one (the OTAFIX bootloader then shows a serial port and a UF2 drive, with no timeout), even when the
// board fell into Bluetooth DFU mode.
const (
	recoverNothingWritten = "Nothing was written: reset the board and try again."
	recoverErased         = "The app is erased: press reset twice quickly to get the bootloader back, then flash again; never retry in the same bootloader session."
)

// Recovery says, in plain words, what state the board is in and how to get it working again.
func (e *FlashError) Recovery() string {
	gone := errors.Is(e.Err, serialport.ErrGone)
	switch {
	case e.Stage == StageStarting && gone:
		return "Nothing was written, but the board went away: plug it back in, reset it and try again."
	case e.Stage == StageStarting:
		return recoverNothingWritten
	case e.MayHaveWorked():
		return "The board left its bootloader right after STOP, as it does when the firmware is good, so the flash most likely worked: " +
			"wait for it to come back running MeshCore. If it doesn't, press reset twice quickly to get the bootloader back, then flash again."
	case e.Stage == StageWriting && e.Sent == 0 && (gone || errors.Is(e.Err, ErrNoAck)):
		// The bootloader checks the init packet after acknowledging it and resets when it refuses it, so the
		// refusal shows up as the board vanishing (or going quiet) at the first DATA frame (5.6).
		return "The board most likely refused this firmware after erasing its app (a file for another board or another SoftDevice). " +
			"Press reset twice quickly to get the bootloader back, then flash the right file; never retry in the same bootloader session."
	case gone:
		return "The board went away (unplugged, or it reset) after START, so its app is erased. Plug it back in, press reset twice quickly to get the bootloader back, then flash again; never retry in the same bootloader session."
	}
	return recoverErased
}

// Flash sends pkg to the bootloader on port: START, the erase wait, INIT, the firmware in 512-byte DATA frames with
// the page-write waits, and STOP, byte for byte and sleep for sleep as dfu.js's dfuUpdate does, with this package's
// departures (the package comment). The caller has opened port at DFUBaud, only after identifying it as the
// bootloader's, and closes it afterwards (dfu.js closes right after STOP's acknowledgement); Flash doesn't touch
// DTR or RTS, as dfu.js doesn't.
//
// pkg must have passed OpenPackage's checks, which Flash runs again, and CheckSoftDevice for this board, which Flash
// requires (ErrSoftDeviceUnchecked): both before anything is written.
//
// ctx stops the flash between frames (the owner's "stop"). progress, if not nil, is told each step. Every error is
// a *FlashError. A nil error means STOP was acknowledged, not that the image is good: the bootloader checks the
// CRC at STOP but never says (5.6), so the caller waits for the app to come back (WaitForPort) and asks it its
// version. It does the same for a FlashError whose MayHaveWorked is true (the port gone after STOP).
func Flash(ctx context.Context, port serialport.Port, clock serialport.Clock, pkg *Package, progress func(Progress)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if pkg == nil {
		return &FlashError{Stage: StageStarting, Err: errors.New("nrfdfu: no package")}
	}
	total := len(pkg.Bin)
	fe := &FlashError{Stage: StageStarting, Total: total}
	fail := func(err error) error {
		fe.Err = err
		return fe
	}
	report := func(p Progress) {
		if progress != nil {
			p.Total = total
			progress(p)
		}
	}
	ip, err := pkg.check()
	if err != nil {
		return fail(err)
	}
	if !pkg.sdChecked || !sdAccepts(ip.SDReq, pkg.sdFWID) {
		return fail(ErrSoftDeviceUnchecked)
	}
	if !initArrivesWhole(pkg.Dat) {
		return fail(fmt.Errorf("%w: the bootloader would cut it short", ErrBadInitPacket))
	}
	s := &session{port: port, acks: newAcks(port)}
	frames := (total + chunkSize - 1) / chunkSize

	report(Progress{Stage: StageStarting, Frames: frames})
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := s.send(startPayload(total)); err != nil {
		return fail(err)
	}

	fe.Stage = StageErasing
	wait := EraseWait(total)
	report(Progress{Stage: StageErasing, Wait: wait, Frames: frames})
	clock.Sleep(wait)
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := s.send(initPayload(pkg.Dat)); err != nil {
		return fail(err)
	}

	fe.Stage = StageWriting
	report(Progress{Stage: StageWriting, Frames: frames})
	// dfu.js's "brief stabilization pause" before the first DATA frame (dfu.js:347-350).
	clock.Sleep(PageWriteWait())
	for i := 0; i < frames; i++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		chunk := pkg.Bin[i*chunkSize : min((i+1)*chunkSize, total)]
		if err := s.send(dataPayload(chunk)); err != nil {
			return fail(err)
		}
		fe.Sent += len(chunk)
		report(Progress{Stage: StageWriting, Frame: i + 1, Frames: frames, Sent: fe.Sent})
		if (i+1)%8 == 0 {
			clock.Sleep(PageWriteWait()) // one 4 KiB page written (dfu.js:362-365)
		}
	}

	fe.Stage = StageFinishing
	report(Progress{Stage: StageFinishing, Frame: frames, Frames: frames, Sent: total})
	clock.Sleep(PageWriteWait()) // the last page (dfu.js:368-369)
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := s.send(stopPayload()); err != nil {
		fe.StopSent = s.copies > 0
		return fail(err)
	}
	return nil
}

// session is one DFU session's sequence numbers and acknowledgements. dfu.js keeps the sequence number in a static
// that dfuUpdate resets to 0 and every new frame pre-increments, so the first frame is 1 (dfu.js:89-92,382).
type session struct {
	port serialport.Port
	seq  int
	acks *acks
	// copies is how many times the frame being sent has been written so far.
	copies int
}

// send frames payload with the next sequence number, writes it in one write, and waits for its acknowledgement,
// keeping exactly one frame in flight as dfu.js does (sendPacket, dfu.js:157-171): the bootloader acknowledges a
// frame before it has room for it, so a second frame in flight could be dropped after its ACK
// (docs/meshcore-flashing.md 5.3). When no acknowledgement comes within AckTimeout the same frame, with the same
// sequence number, is sent again, up to Tries in all: the bootloader re-acknowledges a copy of a frame it has
// already taken and drops it (hci_transport.c:304-314), so a resend can't write anything twice.
func (s *session) send(payload []byte) error {
	s.seq = (s.seq + 1) % 8
	frame := encodeFrame(s.seq, payload)
	s.copies = 0
	for try := 1; ; try++ {
		if _, err := s.port.Write(frame); err != nil {
			return err
		}
		s.copies++
		_, err := s.acks.next()
		if err == nil {
			s.acks.strays = try - 1
			return nil
		}
		if !errors.Is(err, serialport.ErrTimeout) {
			return err
		}
		if try == Tries {
			return fmt.Errorf("%w (sent %d times, waiting %v each time)", ErrNoAck, Tries, AckTimeout)
		}
	}
}
