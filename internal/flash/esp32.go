package flash

import (
	"errors"
	"fmt"

	"meshflash/internal/esp"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// esp32 flashes an ESP32-family board found on cur through its ROM bootloader (docs/meshcore-flashing.md 4; SPEC.md
// 6.2): esp.Flash writes exactly the bytes check verified, at the address and with the erase catalog.Choose gave,
// guarded by the board's chip, and checks the MD5. An ESP32-S3 on TinyUSB comes back from the reset as its ROM's
// USB-Serial-JTAG, a new port, found by its serial number and flashed there (map-backend.md 1.4 step 6).
func (r *flashRun) esp32(cur Port, c checked) Outcome {
	expect := esp.Chip("")
	if ch, ok := usbid.DeviceChip(r.req.Device.Name); ok {
		expect, _ = esp.ParseChip(string(ch))
	}
	ep := &espSteps{r: r, into: -1, cur: -1}
	port := cur
	var err error
	for attempt := 0; ; attempt++ {
		sp, oerr := r.b.open(port.Dev, 115200, purposeFlash)
		if oerr != nil {
			return r.espOpenFailed(cur, oerr, attempt > 0)
		}
		_, err = esp.Flash(r.ctx, sp, c.data, esp.Options{Clock: r.clock, USBVendor: port.VID,
			USBProduct: port.PID, ExpectChip: expect, Address: c.fl.Address, EraseChip: c.fl.EraseChip,
			Progress: ep.progress})
		sp.Close()
		var fe *esp.FlashError
		if attempt == 0 && errors.As(err, &fe) && fe.Stage == esp.StageConnecting && !fe.Touched &&
			errors.Is(err, serialport.ErrGone) && port.Kind == usbid.EspTinyUSB && r.ctx.Err() == nil {
			// The classic reset rebooted the TinyUSB app into its ROM, which is a new USB device.
			r.note(ep.into, "it restarted into its ROM: finding its new port")
			rom, werr := nrfdfu.WaitForPort(r.ctx, r.b.find(cur, func(q usbid.Port) bool {
				return usbid.Classify(q) == usbid.EspUSBJTAG
			}), r.clock, nrfdfu.BootloaderWait)
			if werr != nil {
				if stopped(werr) {
					return r.stoppedUntouched()
				}
				return r.espFailed(cur, fe, err, ep)
			}
			port, _ = r.b.portAt(rom)
			r.note(ep.into, "on its ROM's port, "+port.Name)
			continue
		}
		break
	}
	if err != nil {
		var fe *esp.FlashError
		errors.As(err, &fe)
		return r.espFailed(cur, fe, err, ep)
	}
	ep.finish()
	r.setRisk(RiskWritten)

	// Where the board is now: a TinyUSB board's app comes back as its own port after the hard reset.
	board := r.boardNow(cur)
	if port.Kind != cur.Kind || board == nil {
		if p, err := nrfdfu.WaitForPort(r.ctx, r.b.find(cur, func(q usbid.Port) bool {
			return PortOf(q).Kind == cur.Kind
		}), r.clock, BoardBackWait); err == nil {
			if q, ok := r.b.portAt(p); ok {
				board = &q
			}
		}
	}
	how := fmt.Sprintf("Wrote %s (%s) as an update at 0x%x: its identity, contacts and pairing are kept.",
		what(r.req), sizeText(len(c.data)), c.fl.Address)
	if c.fl.EraseChip {
		how = fmt.Sprintf("Erased the whole chip and wrote %s (%s) at 0x0.", what(r.req), sizeText(len(c.data)))
	}
	o := Outcome{OK: true, Title: titleFlashed, Board: board, Summary: []string{how,
		"Checked: the MD5 of the flash matches the file.", "It restarted."}}
	if board == nil {
		o.Summary[2] = "It restarted, but hasn't come back on USB yet."
	}
	o.Setup = meshCoreConsole(r.req.Firmware) && board != nil
	return o
}

// espSteps turns esp's progress into steps: into the bootloader (with the attempt), the stub, the erase, the write
// with its bytes, the MD5, the restart. It runs on the flashing goroutine and never blocks (Job.Set).
type espSteps struct {
	r     *flashRun
	stage esp.Stage
	into  int // the "Into the bootloader" step
	cur   int // the step under way
	chip  esp.Chip
	begun bool
}

func (e *espSteps) progress(p esp.Progress) {
	r := e.r
	if p.Chip != "" {
		e.chip = p.Chip
	}
	if e.begun && p.Stage == e.stage {
		switch p.Stage {
		case esp.StageConnecting:
			if p.Attempt > 1 {
				r.note(e.into, fmt.Sprintf("attempt %d of %d", p.Attempt, esp.DefaultConnectAttempts))
			}
		case esp.StageWriting:
			done := int64(p.Done)
			i := e.cur
			r.j.Set(func(pr *Progress) {
				if i >= 0 && i < len(pr.Steps) {
					pr.Steps[i].Done = done
				}
			})
		}
		return
	}
	if p.Stage == esp.StageConnecting && e.into >= 0 {
		return // the TinyUSB board's second connect: the same step
	}
	e.close()
	e.stage, e.begun = p.Stage, true
	switch p.Stage {
	case esp.StageConnecting:
		e.into = r.start("Into the bootloader")
		e.cur = e.into
		if p.Attempt > 1 {
			r.note(e.into, fmt.Sprintf("attempt %d of %d", p.Attempt, esp.DefaultConnectAttempts))
		}
	case esp.StageStub:
		e.cur = r.start("Loading the flasher stub")
	case esp.StageErasing:
		r.setRisk(RiskErased)
		e.cur = r.start("Erasing the whole chip")
	case esp.StageWriting:
		r.setRisk(RiskErased) // FLASH_DEFL_BEGIN erases the region it writes
		total := int64(p.Total)
		e.cur = r.start("Writing", func(s *Step) { s.Total = total })
	case esp.StageVerifying:
		e.cur = r.start("Checking the MD5")
	case esp.StageResetting:
		r.setRisk(RiskWritten)
		e.cur = r.start("Restarting")
	}
}

// close ends the step under way, done, in its finished words.
func (e *espSteps) close() {
	if !e.begun || e.cur < 0 {
		return
	}
	text := ""
	switch e.stage {
	case esp.StageStub:
		text = "Flasher stub loaded"
		if e.chip != "" {
			text += " (" + e.chip.String() + ")"
		}
	case esp.StageErasing:
		text = "Erased the whole chip"
	case esp.StageWriting:
		text = "Written"
	case esp.StageVerifying:
		text = "Checked: MD5 matches"
	case esp.StageResetting:
		text = "Restarted"
	}
	e.r.done(e.cur, text)
	e.cur = -1
}

// finish ends the last step once esp.Flash has returned with no error.
func (e *espSteps) finish() { e.close() }

// espOpenFailed is the board's port not opening: nothing was sent.
func (r *flashRun) espOpenFailed(cur Port, err error, rom bool) Outcome {
	why := "meshflash couldn't open the board's port: " + clause(plain(err)) + "."
	switch {
	case errors.Is(err, serialport.ErrBusy):
		why = "Another program has the board's port open."
	case errors.Is(err, serialport.ErrPermission):
		why = "meshflash may not open serial ports yet: it needs the dialout group."
	case errors.Is(err, serialport.ErrGone):
		why = "The board went away before meshflash could open it."
	}
	if rom {
		why = "Its ROM's port came up but couldn't be opened: " + clause(why) + "."
	}
	return Outcome{Title: titleNotFlashed, Board: r.boardNow(cur), Retry: true, Summary: []string{why},
		State: nothingSent, Recovery: plugItIn}
}

// espFailed is esp.Flash stopping: what its FlashError says the board is left with, State its first sentence and
// Recovery the rest (4.1, "Words").
func (r *flashRun) espFailed(cur Port, fe *esp.FlashError, err error, ep *espSteps) Outcome {
	if ep.cur >= 0 {
		note := clause(plain(err))
		if fe != nil {
			note = clause(espSummary(fe, err))
		}
		r.failed(ep.cur, note)
	}
	board := r.boardNow(cur)
	if fe == nil {
		return Outcome{Title: titleStopped, Board: board, Retry: true, Summary: []string{sentence(plain(err))},
			State: "meshflash can't tell what the board is left with.", Recovery: plugItIn}
	}
	state, recovery := splitRecovery(fe.Recovery())
	o := Outcome{Title: titleStopped, Board: board, Retry: true, State: state, Recovery: recovery,
		Stopped: stopped(err), Summary: []string{espSummary(fe, err)}}
	switch {
	case fe.Stage == esp.StageCheck:
		// esp's own check of the file, which Prepare made too: refused before the board was touched.
		o = Outcome{Title: titleNotFlashed, State: nothingSent, Recovery: sentence(plain(fe.Err))}
	case o.Stopped:
		o.Summary = []string{"Stopped while " + fe.Stage.String() + "."}
	case !fe.Touched:
		o.Title = titleNotFlashed
	}
	return o
}

// espSummary says what went wrong, in words: "The board stopped answering while writing (212 of 361 KB)."
func espSummary(fe *esp.FlashError, err error) string {
	switch {
	case errors.Is(err, esp.ErrNoBootloader):
		return "The board didn't answer from its bootloader."
	case errors.Is(err, esp.ErrVerify):
		return "The flash's MD5 doesn't match the file's."
	case errors.Is(err, esp.ErrWrongChip), errors.Is(err, esp.ErrUnsupportedChip), errors.Is(err, esp.ErrTooBig):
		return sentence(plain(fe.Err))
	case fe.Stage == esp.StageWriting && errors.Is(err, serialport.ErrGone):
		return fmt.Sprintf("The board went away while writing (%s of %s).", sizeText(fe.Written), sizeText(fe.Total))
	case fe.Stage == esp.StageWriting:
		return fmt.Sprintf("The board stopped answering while writing (%s of %s).", sizeText(fe.Written),
			sizeText(fe.Total))
	case errors.Is(err, serialport.ErrGone):
		return "The board went away while " + fe.Stage.String() + "."
	}
	return "It stopped while " + fe.Stage.String() + ": " + clause(plain(fe.Err)) + "."
}
