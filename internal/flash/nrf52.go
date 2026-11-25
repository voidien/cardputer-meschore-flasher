package flash

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"meshflash/internal/nodecli"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// pollFresh is how often the wait for a fresh bootloader looks (4.4: every 100 ms on the clock).
const pollFresh = 100 * time.Millisecond

// nrf52 flashes an nRF52 found on cur (docs/meshcore-flashing.md 5; SPEC.md 6.3): into a fresh bootloader session
// (the 1200-baud touch, a bootloader seen to appear in this run, or a double press of reset waited for), serial DFU
// of the package check made from the very bytes it verified, then the board back on USB running what was written.
func (r *flashRun) nrf52(cur Port, c checked) (o Outcome) {
	was := cur
	appTty := ""
	defer func() { r.backTo115200(appTty) }()

	path, o, ok := r.bootloader(cur)
	if !ok {
		return o
	}
	boot, _ := r.b.portAt(path)
	if boot.Name == "" {
		boot = cur
	}

	// The DFU session, on the bootloader's port at DFUBaud (opening it sets the tty back from the touch's 1200).
	sp, err := r.b.open(path, nrfdfu.DFUBaud, purposeFlash)
	if err != nil {
		return r.dfuOpenFailed(boot, err)
	}
	// Closed however the session ends, a panic in a progress report included (an exclusive port left open refuses
	// every later open of the board); the close after Flash is the usual one, and a second does no harm.
	defer sp.Close()
	r.reopened[boot.Name] = true
	dp := &dfuPort{Port: sp, r: r, board: boot}
	ferr := nrfdfu.Flash(r.ctx, dp, r.clock, c.pkg, r.dfuProgress(len(c.pkg.Bin)))
	sp.Close()
	r.mu.Lock()
	r.eraseEnds = time.Time{}
	r.mu.Unlock()

	var fe *nrfdfu.FlashError
	errors.As(ferr, &fe)
	mayHave := fe != nil && fe.MayHaveWorked()
	if ferr != nil && !mayHave {
		return r.dfuFailed(was, fe, ferr)
	}
	r.crcStep(mayHave)
	r.setRisk(RiskWritten)

	// Back on USB: the bootloader starts the app once it took the image.
	back := r.start(waitingOnUSB)
	appPath, err := nrfdfu.WaitForPort(r.ctx, r.b.find(was, func(q usbid.Port) bool {
		return q.Interface != usbid.InterfaceNRFBootloader && usbid.Classify(q) == usbid.NRFApp
	}), r.clock, nrfdfu.AppWait)
	if err != nil {
		r.failed(back, "not back in "+fmt.Sprint(secs(nrfdfu.AppWait))+" s")
		return r.notBack(was, fe, mayHave, err)
	}
	app, _ := r.b.portAt(appPath)
	if app.Name == "" {
		app = was
	}
	r.done(back, "Back on USB ("+app.Name+")")
	written := fmt.Sprintf("Wrote %s (%s).", what(r.req), sizeText(len(c.pkg.Bin)))

	if !meshCoreConsole(r.req.Firmware) {
		// Owner question 7: companion, KISS, ZephCore and Ripple firmware have no console meshflash knows.
		appTty = app.Name
		return Outcome{OK: true, Title: titleFlashed, Board: &app, Summary: []string{written,
			"The board checked the CRC and its bootloader took the image.", "It came back on USB (" + app.Name + ").",
			"meshflash can't ask this firmware its version."}}
	}

	// A repeater or room server: ask it its version, since the bootloader never says whether the image was good.
	vs := r.start("Asking its version")
	info, err := r.askVersion(appPath)
	r.reopened[app.Name] = true
	switch {
	case err != nil && stopped(err):
		r.failed(vs, "stopped")
		return Outcome{Stopped: true, Title: titleStopped, Board: r.boardNow(was), Retry: true,
			Summary: []string{written, "It came back on USB; meshflash stopped before asking its version."},
			State:   "It was written, but not checked."}
	case err != nil:
		r.failed(vs, "no answer")
		return Outcome{Title: titleNotChecked, Board: r.boardNow(was), Retry: true,
			Summary:  []string{written, "The board checked the CRC and came back on USB (" + app.Name + ")."},
			State:    "It came back on USB but didn't answer as " + consoleName(r.req.Firmware) + ".",
			Recovery: "Press reset once; if it still doesn't answer, flash it again."}
	case !info.Version.NamedIn(c.fl.File.Base()):
		r.failed(vs, "runs "+info.Version.String())
		return Outcome{Title: titleStopped, Board: r.boardNow(was), Retry: true,
			Summary: []string{written, "It came back on USB (" + app.Name + ") running " + info.Version.String() + "."},
			State: "It runs " + info.Version.String() + ", not the version just flashed: the bootloader most likely " +
				"refused the image.",
			Recovery: "Press reset twice quickly, then flash again."}
	}
	r.done(vs, "Running "+info.Version.String())
	return Outcome{OK: true, Title: titleFlashed, Board: r.boardNow(was), Setup: true, Summary: []string{written,
		"The board checked the CRC and came back running " + info.Version.String() + "."}}
}

// bootloader gets the board into a fresh bootloader session and gives its port's path, or how the flash ended.
func (r *flashRun) bootloader(cur Port) (string, Outcome, bool) {
	rec, stale := r.b.staleFor(cur)
	switch {
	case cur.Kind == usbid.NRFApp:
		return r.touch(cur)
	case cur.InBootloader() && !stale:
		// Seen to appear in this run (owner question 18): a fresh session, no touch needed.
		r.setRisk(RiskBootloader)
		r.done(r.start("Already in its bootloader"), "")
		return cur.Dev, Outcome{}, true
	}
	return r.freshBootloader(cur, rec, stale)
}

// touch is the 1200-baud touch, then the wait for the bootloader's port, positively identified ("nRF Serial") and
// the same board (its USB serial number), never found by tty name.
func (r *flashRun) touch(cur Port) (string, Outcome, bool) {
	i := r.start("Into the bootloader (1200-baud touch)")
	err := nrfdfu.TouchContext(r.ctx, r.b.touchOpener, cur.Dev, r.clock)
	if err != nil {
		switch {
		case errors.Is(err, nrfdfu.ErrTouchStopped):
			// Stopped before DTR dropped: the line went back to 115200 first, so the board was never touched.
			r.failed(i, "")
			return "", r.stoppedUntouched(), false
		case stopped(err):
			// Stopped once the touch was made: the board resets into its bootloader all the same.
			r.touched = cur.Name
			r.setRisk(RiskBootloader)
			r.failed(i, "")
			return "", r.stoppedInBootloader(cur, "Stopped just after the 1200-baud touch.", touchedState), false
		case errors.Is(err, serialport.ErrBusy), errors.Is(err, serialport.ErrPermission),
			errors.Is(err, serialport.ErrGone):
			// The port didn't open, so nothing reached the board.
			r.failed(i, "couldn't open "+cur.Name)
			why := map[bool]string{true: "Another program has the board's port open."}[errors.Is(err, serialport.ErrBusy)]
			if why == "" {
				why = "meshflash couldn't open the board's port: " + clause(plain(err)) + "."
			}
			return "", Outcome{Title: titleNotFlashed, Board: r.boardNow(cur), Retry: true, Summary: []string{why},
				State: nothingSent, Recovery: plugItIn}, false
		}
		// The board may have reset all the same (nrfdfu.Touch): look for the bootloader anyway.
		r.b.logf("flash: the 1200-baud touch on %s: %v", cur.Name, plain(err))
	}
	r.touched = cur.Name // the port was opened at 1200 baud: from here the flash ends setting it back
	r.setRisk(RiskBootloader)
	path, err := nrfdfu.WaitForPort(r.ctx, r.b.find(cur, func(q usbid.Port) bool {
		return q.Interface == usbid.InterfaceNRFBootloader
	}), r.clock, nrfdfu.BootloaderWait)
	switch {
	case err != nil && stopped(err):
		r.failed(i, "")
		return "", r.stoppedInBootloader(cur, "Stopped while waiting for its bootloader after the 1200-baud touch.",
			touchedState), false
	case err != nil:
		r.failed(i, "no bootloader in "+fmt.Sprint(secs(nrfdfu.BootloaderWait))+" s")
		board := r.boardNow(cur)
		o := Outcome{Title: titleNotFlashed, Board: board, Retry: true,
			Summary:  []string{"The board didn't start its bootloader after the 1200-baud touch."},
			State:    "Nothing was sent: it still has its old firmware.",
			Recovery: pressTwice}
		if board == nil {
			o.Summary = []string{"The board went away after the 1200-baud touch and didn't come back as its bootloader."}
			o.Recovery = "Plug it in; if it doesn't start, press reset twice quickly. Then press r."
		}
		return "", o, false
	}
	r.done(i, "")
	return path, Outcome{}, true
}

// freshBootloader waits for a fresh bootloader session on a board whose session may already have had START (4.4),
// or whose bootloader isn't positively identified: the owner presses reset twice, its port goes and comes back.
// The port as it is is never used.
func (r *flashRun) freshBootloader(cur Port, rec staleDFU, stale bool) (string, Outcome, bool) {
	i := r.start(pressResetTwiceStep, func(s *Step) {
		s.Note = fmt.Sprintf("waiting up to %d s for its bootloader to restart", secs(FreshBootloaderWait))
	})
	// What stopping here leaves: an earlier START erased the app; otherwise it is in its bootloader with whatever it
	// had.
	erased := stale && rec.why == staleStarted
	if erased {
		r.setRisk(RiskErased)
	} else {
		r.setRisk(RiskBootloader)
	}
	start := r.clock.Now()
	gone := false
	for {
		ports, err := r.b.scan()
		if err == nil {
			here, fresh := false, ""
			for _, q := range ports {
				if !sameBoard(cur.Port, q) {
					continue
				}
				if usbid.Classify(q) == usbid.NRFBootloader {
					here = true
					if q.Interface == usbid.InterfaceNRFBootloader {
						fresh = q.Dev
					}
				}
			}
			switch {
			case !here && !gone:
				gone = true
				r.note(i, "it went away: waiting for its bootloader to come back")
			case gone && fresh != "":
				// The step keeps its words: what it asked for, now ✓ (the screens and their tests read it so). The
				// session is a new one: nothing stale is left of the record.
				r.b.clearStale(cur)
				r.done(i, "")
				return fresh, Outcome{}, true
			}
		}
		if err := r.ctx.Err(); err != nil {
			r.failed(i, "")
			o := Outcome{Stopped: true, Title: titleStopped, Board: r.boardNow(cur), Retry: true,
				Summary: []string{"Stopped while waiting for the double press of reset, before anything was sent."},
				State:   "Nothing was sent: it is still in its bootloader.", Recovery: pressResetOnce}
			if erased {
				o.State, o.Recovery = "Nothing more was sent: its app is still erased.", askDoublePress
			}
			return "", o, false
		}
		if r.clock.Now().Sub(start) >= FreshBootloaderWait {
			break
		}
		r.clock.Sleep(pollFresh)
	}
	r.failed(i, fmt.Sprintf("no fresh bootloader in %d s", secs(FreshBootloaderWait)))
	// Only a double press seen during this wait makes the session fresh (scan.go's note), so r asks for it again.
	o := Outcome{Title: titleNotFlashed, Board: r.boardNow(cur), Retry: true, Recovery: askDoublePress,
		Summary: []string{"meshflash didn't see the board's bootloader go and come back, so it sent nothing."}}
	switch {
	case erased:
		o.State = "Nothing more was sent: its app is still erased."
	default:
		o.State = "Nothing was sent: it is still in its bootloader."
	}
	return "", o, false
}

// dfuProgress turns nrfdfu's progress into steps: "Erasing" (with how long it should take), "Writing" with its
// bytes, then the CRC check at STOP. It runs on the flashing goroutine and never blocks (Job.Set).
func (r *flashRun) dfuProgress(size int) func(nrfdfu.Progress) {
	erase, write, finish := -1, -1, -1
	return func(p nrfdfu.Progress) {
		switch p.Stage {
		case nrfdfu.StageErasing:
			if erase < 0 {
				erase = r.start("Erasing", func(s *Step) { s.Expect = p.Wait })
				r.mu.Lock()
				r.eraseEnds = r.clock.Now().Add(p.Wait)
				r.mu.Unlock()
			}
		case nrfdfu.StageWriting:
			if write < 0 {
				r.mu.Lock()
				r.eraseEnds = time.Time{}
				r.mu.Unlock()
				r.done(erase, "Erased")
				total := int64(p.Total)
				write = r.start("Writing", func(s *Step) { s.Total = total })
			}
			sent := int64(p.Sent)
			r.j.Set(func(pr *Progress) {
				if write < len(pr.Steps) {
					pr.Steps[write].Done = sent
				}
			})
		case nrfdfu.StageFinishing:
			if finish < 0 {
				r.done(write, "Written")
				finish = r.start("Checking the CRC")
			}
		}
	}
}

// crcStep ends the CRC step once STOP was acknowledged, or most likely taken (MayHaveWorked).
func (r *flashRun) crcStep(mayHave bool) {
	r.mu.Lock()
	i := r.cur
	r.mu.Unlock()
	if mayHave {
		r.end(i, StepDone, "The board most likely checked the CRC", "STOP's answer was lost as it left its bootloader")
	} else {
		r.done(i, "The board checked the CRC")
	}
}

// askVersion opens the app's port at 115200 and asks its version, waiting as a reboot needs (nodecli.ConfirmWait).
func (r *flashRun) askVersion(path string) (nodecli.Info, error) {
	p, err := r.b.open(path, 115200, purposeFlash)
	if err != nil {
		return nodecli.Info{}, err
	}
	defer p.Close()
	return nodecli.ProbeFor(r.ctx, p, r.clock, nodecli.ConfirmWait)
}

// dfuOpenFailed is the bootloader's port not opening: nothing was sent to it.
func (r *flashRun) dfuOpenFailed(boot Port, err error) Outcome {
	why := "meshflash couldn't open the bootloader's port: " + clause(plain(err)) + "."
	if errors.Is(err, serialport.ErrBusy) {
		why = "Another program has the bootloader's port open."
	}
	return Outcome{Title: titleNotFlashed, Board: r.boardNow(boot), Retry: true, Summary: []string{why},
		State: "Nothing was sent: it is in its bootloader.", Recovery: pressTwice}
}

// dfuFailed is a DFU session that stopped: before START was acknowledged nothing was written; after it, the app is
// erased and only a fresh session helps (Recovery: owner question 17).
func (r *flashRun) dfuFailed(was Port, fe *nrfdfu.FlashError, err error) Outcome {
	r.mu.Lock()
	i := r.cur
	r.mu.Unlock()
	r.failed(i, clause(plain(err)))
	o := Outcome{Title: titleStopped, Board: r.boardNow(was), Retry: true, State: appErased,
		Recovery: askDoublePress, Stopped: stopped(err)}
	if fe == nil {
		o.Summary = []string{sentence(plain(err))}
		return o
	}
	gone := errors.Is(err, serialport.ErrGone)
	switch {
	case fe.Stage == nrfdfu.StageStarting:
		// START may have reached it (its acknowledgement didn't come back), so the session counts as used.
		o.Title, o.State = titleNotFlashed, "Nothing was written: it is in its bootloader."
		o.Summary = []string{"The bootloader didn't answer START."}
		if gone {
			o.Summary = []string{"The board went away as the flash started."}
		}
		if o.Stopped {
			o.Title, o.Summary = titleStopped, []string{"Stopped before the bootloader started erasing."}
		}
	case o.Stopped:
		o.Summary = []string{"Stopped while " + fe.Stage.String() + "."}
	case fe.Stage == nrfdfu.StageWriting && fe.Sent == 0 && (gone || errors.Is(err, nrfdfu.ErrNoAck)):
		// The bootloader checks the init packet after acknowledging it and resets when it refuses it (5.6).
		o.Summary = []string{"The board most likely refused this firmware after erasing its app (a file for another " +
			"board or another SoftDevice); it may now be in Bluetooth DFU mode, with no USB port."}
	case fe.Stage == nrfdfu.StageWriting && gone:
		o.Summary = []string{fmt.Sprintf("The board went away while writing (%s of %s).", sizeText(fe.Sent),
			sizeText(fe.Total))}
	case fe.Stage == nrfdfu.StageWriting:
		o.Summary = []string{fmt.Sprintf("The board stopped answering while writing (%s of %s).", sizeText(fe.Sent),
			sizeText(fe.Total))}
	case gone:
		o.Summary = []string{"The board went away while " + fe.Stage.String() + "."}
	default:
		o.Summary = []string{"The board stopped answering while " + fe.Stage.String() + "."}
	}
	return o
}

// notBack is a board that didn't come back on USB after STOP.
func (r *flashRun) notBack(was Port, fe *nrfdfu.FlashError, mayHave bool, err error) Outcome {
	written := fmt.Sprintf("Wrote %s.", what(r.req))
	if stopped(err) {
		return Outcome{Stopped: true, Title: titleStopped, Board: r.boardNow(was), Retry: true,
			Summary: []string{written, "Stopped while waiting for it to come back."},
			State:   "It was written, but not checked."}
	}
	cur := r.boardNow(was)
	switch {
	case cur != nil && cur.Kind == usbid.NRFBootloader:
		// STOP was taken but the bootloader stayed: it refused the image at its CRC check (5.6), after the erase.
		return Outcome{Title: titleStopped, Board: cur, Retry: true, State: appErased, Recovery: askDoublePress,
			Summary: []string{"The bootloader didn't take the image: it stayed in its bootloader after STOP."}}
	case mayHave:
		_, rec := splitRecovery(fe.Recovery())
		return Outcome{Title: titleStopped, Board: cur, Retry: true,
			Summary:  []string{written, "The board left its bootloader as STOP went out, but it hasn't come back on USB."},
			State:    "Most likely written, but the board didn't come back.",
			Recovery: rec}
	case !meshCoreConsole(r.req.Firmware):
		return Outcome{OK: true, Title: titleFlashed, Board: cur, Summary: []string{written,
			"The board checked the CRC and its bootloader took the image.",
			"It hasn't come back on USB: press its reset button once if it doesn't start.",
			"meshflash can't ask this firmware its version."}}
	}
	return Outcome{Title: titleStopped, Board: cur, Retry: true,
		Summary:  []string{written, "The board left its bootloader but hasn't come back on USB."},
		State:    "Most likely written, but the board didn't come back.",
		Recovery: "Press its reset button once; if it doesn't come back, press reset twice quickly and flash it again."}
}

// dfuPort is the bootloader's port as the DFU session writes to it: the first frame written is START, so the
// session is stale from then on (4.4) and stopping leaves no app; a frame written again is a resend, which the
// running step says.
type dfuPort struct {
	serialport.Port
	r     *flashRun
	board Port

	writes int
	last   []byte
	tries  int
}

func (p *dfuPort) Write(b []byte) (int, error) {
	p.writes++
	if p.writes == 1 {
		p.r.b.markStarted(p.board)
		p.r.setRisk(RiskErased)
	}
	switch {
	case bytes.Equal(b, p.last):
		p.tries++
		p.r.mu.Lock()
		i := p.r.cur
		p.r.mu.Unlock()
		p.r.note(i, fmt.Sprintf("No answer: sending again (%d of %d)", p.tries, nrfdfu.Tries))
	case p.tries > 1:
		p.tries = 1
		p.r.mu.Lock()
		i := p.r.cur
		p.r.mu.Unlock()
		p.r.clearResendNote(i)
	default:
		p.tries = 1
	}
	p.last = append(p.last[:0], b...)
	return p.Port.Write(b)
}

// clearResendNote drops a resend note once the frame got through.
func (r *flashRun) clearResendNote(i int) {
	if i < 0 {
		return
	}
	r.j.Set(func(p *Progress) {
		if i < len(p.Steps) && strings.HasPrefix(p.Steps[i].Note, "No answer: sending again") {
			p.Steps[i].Note = ""
		}
	})
}
