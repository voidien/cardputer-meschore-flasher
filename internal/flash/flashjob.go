package flash

import (
	"context"
	"fmt"
	"sync"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/serialport"
)

// flash is Flash: the Job, at once; the work on its own goroutine.
func (b *Backend) flash(ctx context.Context, req Request) *Job {
	ctx, cancel := context.WithCancel(ctx)
	j := NewJob(JobFlash, b.d.Clock, cancel)
	r := &flashRun{b: b, j: j, ctx: ctx, req: req, clock: b.d.Clock, cur: -1, reopened: map[string]bool{}}
	go r.main()
	return j
}

// flashRun is one flash under way.
type flashRun struct {
	b     *Backend
	j     *Job
	ctx   context.Context
	req   Request
	clock serialport.Clock

	mu  sync.Mutex // the step under way and the erase's end, which the cancel note reads from another goroutine
	cur int        // the step under way, -1 for none
	// eraseEnds is when the nRF52's erase wait ends, while it runs (zero otherwise): a cancel then waits for it.
	eraseEnds time.Time

	// touched is the tty the 1200-baud touch was made on ("" for none), and reopened the ttys opened at 115200
	// since, so "Back to 115200 baud" opens only what is still at 1200.
	touched  string
	reopened map[string]bool
}

func (r *flashRun) main() {
	var o Outcome
	defer func() {
		if p := recover(); p != nil {
			o = Outcome{Title: titleStopped, Summary: []string{"meshflash went wrong while flashing."},
				State:    "meshflash can't tell what the board is left with.",
				Recovery: "Unplug the board and plug it back in, then flash it again from the Board screen.",
				Log:      fmt.Sprintf("flash: internal error: %v", p)}
			r.b.logf("%s", o.Log)
		}
		r.j.Finish(o)
	}()
	// A cancel during the nRF52 erase waits for it to end (nrfdfu checks between frames): say so, from the
	// goroutine that cancelled, at once.
	stop := context.AfterFunc(r.ctx, r.cancelNote)
	defer stop()
	o = r.run()
	if !o.OK && o.Log == "" {
		o.Log = r.logLine(o)
	}
	if !o.OK {
		r.b.logf("%s", o.Log)
	}
}

// run does the flash and says how it ended. Every port is closed and the board lock released when it returns.
func (r *flashRun) run() Outcome {
	req := r.req
	// The family, then the file, before anything touches the board; a flash never downloads.
	c := r.b.check(req)
	switch {
	case c.problem != "":
		return r.refused(c.problem)
	case !c.saved:
		return r.refused(notSaved)
	}
	if err := r.b.lock(r.ctx); err != nil {
		return r.stoppedUntouched()
	}
	defer r.b.unlock()
	cur, ok, err := r.b.findBoard(req.Port)
	switch {
	case err != nil:
		return Outcome{Title: titleNotFlashed, Summary: []string{sentence(err.Error())}, State: nothingSent,
			Recovery: plugItIn, Retry: true}
	case !ok:
		return Outcome{Title: titleNotFlashed, Summary: []string{"The board isn't plugged in."}, State: nothingSent,
			Recovery: plugItIn, Retry: true}
	}
	if ok, why, _ := Fits(req.Device, cur); !ok {
		return r.refused(why)
	}
	if req.Device.Type == catalog.NRF52 {
		return r.nrf52(cur, c)
	}
	return r.esp32(cur, c)
}

// refused is a flash refused before anything touched the board.
func (r *flashRun) refused(why string) Outcome {
	return Outcome{Title: titleNotFlashed, State: nothingSent, Recovery: why}
}

// stoppedUntouched is a stop before anything was sent.
func (r *flashRun) stoppedUntouched() Outcome {
	return Outcome{Stopped: true, Title: titleStopped, Summary: []string{"Stopped before anything was sent."},
		State: nothingSent, Retry: true}
}

// stoppedInBootloader is a stop once the board was sent into its bootloader (the 1200-baud touch, an ESP32's reset
// into its ROM) and before anything was written: its old firmware is there, but it waits in its bootloader, off the
// mesh, until its reset button is pressed (docs/meshcore-flashing.md 10.2).
func (r *flashRun) stoppedInBootloader(was Port, summary, state string) Outcome {
	return Outcome{Stopped: true, Title: titleStopped, Board: r.boardNow(was), Summary: []string{summary},
		State: state, Recovery: pressResetOnce, Retry: true}
}

// logLine is the one screen.log line for a flash that didn't work: where it stopped and why, in the steps' and the
// outcome's own words (they never hold a password).
func (r *flashRun) logLine(o Outcome) string {
	p := r.j.Snapshot()
	at := ""
	for i := len(p.Steps) - 1; i >= 0; i-- {
		if s := p.Steps[i]; s.State == StepFailed || s.State == StepNow {
			at = fmt.Sprintf(" at %q", s.Text)
			if s.Note != "" {
				at += " (" + s.Note + ")"
			}
			break
		}
	}
	line := fmt.Sprintf("flash of %s for %s on %s%s: %s.", what(r.req), r.req.Device.Name, r.req.Port.Name, at, o.Title)
	for _, s := range append(append([]string(nil), o.Summary...), o.State, o.Recovery) {
		if s != "" {
			line += " " + s
		}
	}
	return line
}

// start begins a step and returns its index.
func (r *flashRun) start(text string, set ...func(*Step)) int {
	now := r.clock.Now()
	i := -1
	r.j.Set(func(p *Progress) {
		st := Step{Text: text, State: StepNow, Started: now}
		for _, f := range set {
			f(&st)
		}
		p.Steps = append(p.Steps, st)
		i = len(p.Steps) - 1
	})
	r.mu.Lock()
	r.cur = i
	r.mu.Unlock()
	return i
}

// done ends step i as done, with its final text when text isn't "".
func (r *flashRun) done(i int, text string) { r.end(i, StepDone, text, "") }

// failed ends step i where it stopped, with why.
func (r *flashRun) failed(i int, note string) { r.end(i, StepFailed, "", note) }

func (r *flashRun) end(i int, state StepState, text, note string) {
	if i < 0 {
		return
	}
	now := r.clock.Now()
	r.j.Set(func(p *Progress) {
		if i >= len(p.Steps) {
			return
		}
		s := &p.Steps[i]
		s.State, s.Took = state, now.Sub(s.Started)
		if text != "" {
			s.Text = text
		}
		if note != "" || state == StepDone {
			s.Note = note
		}
	})
	r.mu.Lock()
	if r.cur == i {
		r.cur = -1
	}
	r.mu.Unlock()
}

// note sets the running step's note.
func (r *flashRun) note(i int, note string) {
	if i < 0 {
		return
	}
	r.j.Set(func(p *Progress) {
		if i < len(p.Steps) {
			p.Steps[i].Note = note
		}
	})
}

// skip adds a step that wasn't needed or possible.
func (r *flashRun) skip(text, note string) {
	now := r.clock.Now()
	r.j.Set(func(p *Progress) {
		p.Steps = append(p.Steps, Step{Text: text, State: StepSkipped, Started: now, Note: note})
	})
}

// setRisk says what stopping now would leave the board with.
func (r *flashRun) setRisk(risk Risk) {
	r.j.Set(func(p *Progress) { p.Risk = risk })
}

// cancelNote runs when the job is cancelled: during the nRF52 erase, the stop waits for the erase to end.
func (r *flashRun) cancelNote() {
	r.mu.Lock()
	ends, i := r.eraseEnds, r.cur
	r.mu.Unlock()
	if ends.IsZero() || i < 0 {
		return
	}
	if ends.After(r.clock.Now()) {
		// No count of seconds: it would be stale at once, and the step's own clock shows how long the erase runs.
		r.note(i, stoppingErase)
	}
}

// boardNow is where the board is now (its port as it shows, or nil when it's gone), for the Outcome.
func (r *flashRun) boardNow(was Port) *Port {
	cur, ok, err := r.b.findBoard(was)
	if err != nil || !ok {
		return nil
	}
	return &cur
}

// backTo115200 ends every flash that made a 1200-baud touch, however it ended: Linux keeps a tty's speed after the
// last close, so a tty left at 1200 baud reboots a companion into DFU when the next program (the messenger, a
// terminal) opens and closes it without setting a speed. The touched tty, and the app's tty when nothing else opened
// it, are opened at 115200 with nothing sent (4.1).
func (r *flashRun) backTo115200(appTty string) {
	if r.touched == "" {
		return
	}
	i := r.start("Back to 115200 baud")
	names := []string{r.touched}
	if appTty != "" && appTty != r.touched {
		names = append(names, appTty)
	}
	opened, missing := 0, 0
	for _, name := range names {
		if r.reopened[name] {
			continue
		}
		if _, ok := r.b.portAt("/dev/" + name); !ok {
			missing++
			continue
		}
		p, err := r.b.open("/dev/"+name, 115200, purposeFlash)
		if err != nil {
			r.b.logf("flash: couldn't open %s to set it back to 115200 baud: %v", name, plain(err))
			r.failed(i, "couldn't open "+name+": "+clause(plain(err)))
			return
		}
		p.Close()
		r.reopened[name] = true
		opened++
	}
	note := ""
	if opened == 0 && missing > 0 {
		note = "nothing is on " + r.touched + " now"
	}
	r.end(i, StepDone, "", note)
}
