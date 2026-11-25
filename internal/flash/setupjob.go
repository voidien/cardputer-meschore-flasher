package flash

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"meshflash/internal/nodecli"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// stepKind is what a planned setup step does to the node, worked out once before Run from its command (which may
// hold a password and is never kept past Run).
type stepKind int

const (
	kindSkip     stepKind = iota // nothing sent
	kindSet                      // changes a setting (the node saves it before it answers)
	kindRadio                    // set radio: saved, applied at the reboot
	kindPathHash                 // set path.hash.mode
	kindTx                       // set tx
	kindRead                     // a read-back
	kindReboot                   // reboot
)

func kindOf(st nodecli.Step) stepKind {
	switch c := st.Command; {
	case c == "":
		return kindSkip
	case strings.HasPrefix(c, "set radio "):
		return kindRadio
	case strings.HasPrefix(c, "set path.hash.mode "):
		return kindPathHash
	case strings.HasPrefix(c, "set tx "):
		return kindTx
	case strings.HasPrefix(c, "get "):
		return kindRead
	case c == "reboot":
		return kindReboot
	}
	return kindSet
}

// setup is Setup: the Job, at once; the work on its own goroutine.
func (b *Backend) setup(ctx context.Context, req SetupRequest) *Job {
	ctx, cancel := context.WithCancel(ctx)
	j := NewJob(JobSetup, b.d.Clock, cancel)
	// The request is a copy; its passwords live only in this goroutine's setupRun until Run returns.
	r := &setupRun{flashRun: flashRun{b: b, j: j, ctx: ctx, clock: b.d.Clock, cur: -1}, node: req.Node,
		settings: req.Settings}
	go r.main()
	return j
}

// setupRun is one setup under way. It shares flashRun's steps.
type setupRun struct {
	flashRun
	node     Node
	settings nodecli.Settings

	kinds   []stepKind
	labels  []string // each step's Display, for saying what was sent
	took    []bool   // the steps the node answered rightly
	sent    *Sent
	rebootd bool
}

func (r *setupRun) main() {
	var o Outcome
	defer func() {
		if p := recover(); p != nil {
			o = Outcome{Title: titleSetupStopped, Summary: []string{"meshflash went wrong while setting it up."},
				State: "meshflash can't tell what the node took.", Recovery: "Set it up again.", Retry: true,
				Log: fmt.Sprintf("setup: internal error: %v", p)}
		}
		// The passwords go as soon as the node has them (and whatever happened).
		r.settings.AdminPassword, r.settings.GuestPassword = "", ""
		if !o.OK && o.Log == "" {
			o.Log = r.setupLog(o)
		}
		if !o.OK {
			r.b.logf("%s", o.Log)
		}
		if r.sent != nil {
			s := *r.sent
			o.Sent = &s
		}
		r.j.Finish(o)
	}()
	o = r.run()
}

func (r *setupRun) run() Outcome {
	s := r.settings
	s.ClockSynced = r.b.ClockSynced()
	info := r.node.Info
	if isZephCore(info) {
		// ZephCore is never set up (4.1), whatever the screens asked: its console isn't MeshCore's.
		return Outcome{Title: titleSetupStopped, Summary: []string{zephCoreSetup}, State: nothingChanged,
			Board: &r.node.Port}
	}
	steps, err := nodecli.Plan(s, info)
	s.AdminPassword, s.GuestPassword = "", ""
	if err != nil {
		// Problems never quote a password (nodecli).
		return Outcome{Title: titleSetupStopped, Summary: []string{"The settings can't be sent: " +
			clause(plain(err)) + "."}, State: nothingChanged, Retry: true, Board: &r.node.Port}
	}
	r.kinds, r.labels, r.took = make([]stepKind, len(steps)), make([]string, len(steps)), make([]bool, len(steps))
	for i, st := range steps {
		r.kinds[i], r.labels[i] = kindOf(st), st.Display
	}
	planned := len(steps) + 2 // the plan (its read-backs and reboot), the board back on USB, the check
	r.j.Set(func(p *Progress) { p.Planned = planned })

	if err := r.b.lock(r.ctx); err != nil {
		dropSteps(steps)
		return r.cancelled(nil)
	}
	defer r.b.unlock()
	cur, ok, err := r.b.findBoard(r.node.Port)
	switch {
	case err != nil || !ok:
		dropSteps(steps)
		return Outcome{Title: titleSetupStopped, Summary: []string{"The board isn't plugged in."},
			State: nothingChanged, Recovery: "Plug it in and set it up again.", Retry: true}
	case cur.Kind == usbid.NRFBootloader:
		dropSteps(steps)
		return Outcome{Title: titleSetupStopped, Board: &cur, Summary: []string{"It is in its bootloader now."},
			State: nothingChanged, Recovery: "Press its reset button once, then set it up again.", Retry: true}
	}
	port, err := r.b.open(cur.Dev, 115200, purposeSetup)
	if err != nil {
		dropSteps(steps)
		why := "meshflash couldn't open its port: " + clause(plain(err)) + "."
		if errors.Is(err, serialport.ErrBusy) {
			why = "Another program has its port open."
		}
		return Outcome{Title: titleSetupStopped, Board: r.boardNow(cur), Summary: []string{why},
			State: nothingChanged, Retry: true}
	}
	// Closed however Run ends, a panic in a progress report included: an exclusive port left open would refuse
	// every later open of this board until meshflash ends. The close after Run is the usual one; a second does no
	// harm.
	defer port.Close()
	err = nodecli.Run(r.ctx, port, r.clock, steps, r.progress)
	// The node has the passwords now, or never will from this run: drop every copy (4.1, "Passwords").
	dropSteps(steps)
	r.settings.AdminPassword, r.settings.GuestPassword = "", ""
	port.Close()
	if err != nil {
		var se *nodecli.StepError
		if errors.As(err, &se) && se.Maybe {
			r.mayHaveTaken(se.Index)
		}
		switch {
		case stopped(err):
			return r.cancelled(se)
		case se != nil:
			return Outcome{Title: titleSetupStopped, Board: r.boardNow(cur), Retry: true,
				Summary: []string{se.Display + ": " + se.Reason}, State: se.State}
		}
		return Outcome{Title: titleSetupStopped, Board: r.boardNow(cur), Retry: true,
			Summary: []string{sentence(plain(err))}, State: "meshflash can't tell what the node took."}
	}
	r.rebootd = true
	r.setRisk(RiskRestarted) // the reboot applies the radio: a stop from here skips only the check

	// The board back after its reboot (a native-USB board re-enumerates; one behind a bridge keeps its port), then
	// the check: the same firmware, the radio that was sent.
	back := r.start(waitingOnUSB)
	want := s.Want(info)
	for attempt := 0; ; attempt++ {
		path, err := nrfdfu.WaitForPort(r.ctx, r.b.find(cur, func(q usbid.Port) bool {
			return PortOf(q).Kind != usbid.NRFBootloader
		}), r.clock, BoardBackWait)
		if err != nil {
			r.failed(back, "not back")
			if stopped(err) {
				return r.cancelled(nil)
			}
			return Outcome{Title: titleSetupStopped, Retry: true, Summary: []string{r.sentSummary(),
				"It restarted but hasn't come back on USB."},
				State:    "Every setting was sent, saved and checked, and it restarted, but meshflash couldn't check it after.",
				Recovery: "Unplug it and plug it back in, then set it up again to check it."}
		}
		now, _ := r.b.portAt(path)
		r.done(back, "Back on USB ("+now.Name+")")
		check := r.start(fmt.Sprintf("Checking %s on %s", info.Version.String(), want.Radio.Display()))
		err = r.confirm(path, want)
		if errors.Is(err, serialport.ErrGone) && attempt == 0 {
			// The old port was still listed for a moment after the reboot: wait for it to come back once more.
			r.failed(check, "it went away: waiting again")
			back = r.start(waitingOnUSB)
			continue
		}
		board := r.boardNow(cur)
		switch {
		case err == nil:
			r.done(check, fmt.Sprintf("Checked: %s on %s", info.Version.String(), want.Radio.Display()))
			return Outcome{OK: true, Title: titleSetUp, Board: board, Summary: []string{r.sentSummary(),
				fmt.Sprintf("It restarted and runs %s on %s.", info.Version.String(), want.Radio.Display())}}
		case stopped(err):
			r.failed(check, "")
			return r.cancelled(nil)
		case errors.Is(err, nodecli.ErrChanged):
			r.failed(check, "came back different")
			why := plain(err)
			if i := strings.Index(why, "came back different: "); i >= 0 {
				why = why[i+len("came back different: "):]
			}
			return Outcome{Title: titleSetDifferent, Board: board, Retry: true,
				Summary: []string{r.sentSummary(), "After the restart " + clause(why) + "."},
				State:   "Every setting was sent and saved, but after the restart it isn't what was sent."}
		}
		r.failed(check, "no answer")
		return Outcome{Title: titleSetupStopped, Board: board, Retry: true, Summary: []string{r.sentSummary(),
			"It didn't answer after the restart."},
			State:    "Every setting was sent, saved and checked, and it restarted, but it didn't answer after.",
			Recovery: "Press its reset button once; if it still doesn't answer, set it up again."}
	}
}

// confirm opens the node after its reboot and checks it (nodecli.Confirm).
func (r *setupRun) confirm(path string, want nodecli.Want) error {
	p, err := r.b.open(path, 115200, purposeSetup)
	if err != nil {
		return err
	}
	defer p.Close()
	return nodecli.Confirm(r.ctx, p, r.clock, want)
}

// progress turns Run's reports into steps (their Text is the step's Display: a password shows as ••••) and keeps
// the Risk and what was sent. It runs on the setup's goroutine.
func (r *setupRun) progress(p nodecli.Progress) {
	switch p.Status {
	case nodecli.Sending:
		r.start(p.Display)
	case nodecli.Skipped:
		r.skip(p.Display, "")
	case nodecli.Failed:
		r.mu.Lock()
		i := r.cur
		r.mu.Unlock()
		note := clause(p.Note)
		if note == "stopped" {
			note = "" // the step already reads "…: stopped"
		}
		r.failed(i, note)
	case nodecli.Done:
		r.mu.Lock()
		i := r.cur
		r.mu.Unlock()
		r.end(i, StepDone, "", clause(p.Note))
		if p.Index >= len(r.kinds) {
			return
		}
		r.took[p.Index] = true
		s := r.settings
		switch k := r.kinds[p.Index]; k {
		case kindRadio:
			r.sentRadio()
		case kindTx, kindPathHash, kindSet:
			switch {
			case r.sent == nil:
			case k == kindTx:
				r.sent.TxDBm = s.TxDBm
			case k == kindPathHash:
				r.sent.PathHashBytes = s.PathHashBytes
			}
			if r.snapshotRisk() == RiskNone {
				r.setRisk(RiskSettings)
			}
		}
	}
}

// sentRadio records that the node took (or may have taken) the radio sent, for config.json (Outcome.Sent): the TX
// power is the node's own until `set tx`, the next step, is answered too, so a setup that ends between the two never
// records a TX power the node didn't get.
func (r *setupRun) sentRadio() {
	tx := r.settings.TxDBm
	if r.node.HasCurrent {
		tx = r.node.Current.TxDBm
	}
	r.sent = &Sent{Radio: r.settings.Radio, TxDBm: tx}
	r.setRisk(RiskRadioSent)
}

// mayHaveTaken notes the step i that reached the node with no answer back (nodecli.StepError.Maybe): the node may have
// saved it, so a radio is recorded as sent and stopping is worded as for a radio taken.
func (r *setupRun) mayHaveTaken(i int) {
	if i < 0 || i >= len(r.kinds) {
		return
	}
	switch r.kinds[i] {
	case kindRadio:
		r.sentRadio()
	case kindSet, kindTx, kindPathHash:
		if r.snapshotRisk() == RiskNone {
			r.setRisk(RiskSettings)
		}
	}
}

func (r *setupRun) snapshotRisk() Risk { return r.j.Snapshot().Risk }

// cancelled is a setup the owner (or a quit) stopped between commands: what was sent and read back, what the node is
// left with and how to finish, by the step it reached (docs/phase2-design.md 4.2). se is Run's account of the stop,
// nil when it came outside Run: when the step under way reached the node and its answer never came (StopGrace
// passed), the node may have saved it, and nodecli's State says so.
func (r *setupRun) cancelled(se *nodecli.StepError) Outcome {
	o := Outcome{Stopped: true, Title: titleSetupStopped, Retry: true, Board: r.boardNow(r.node.Port)}
	risk := r.snapshotRisk()
	switch {
	case se != nil && se.Maybe:
		label, _, _ := strings.Cut(se.Display, ":")
		o.Summary = []string{r.sentSummary(), label + " was sent just as it stopped, and may have been saved."}
		o.State = se.State
	case r.rebootd:
		o.Summary = []string{r.sentSummary(), "It restarted; meshflash stopped before checking it."}
		o.State = "Every setting is saved and it restarted with them."
	case risk == RiskNone:
		o.Summary = []string{"Stopped before the node saved anything."}
		o.State = nothingChanged
		o.Recovery = "Set it up again when you're ready."
	case risk == RiskRadioSent:
		o.Summary = []string{r.sentSummary()}
		o.State = "Its radio is saved but it hasn't restarted: it stays on its old radio until it restarts."
		o.Recovery = "Set it up again to finish, or restart it to switch to the new radio."
	default:
		o.Summary = []string{r.sentSummary()}
		o.State = "The settings sent before it stopped are saved on the node, still on its old radio."
		o.Recovery = "Set it up again to finish."
	}
	return o
}

// sentSummary says what the node took and what was read back: "Sent and saved: name, TX power. Read back and
// checked: name." (the steps' own words, which never hold a password).
func (r *setupRun) sentSummary() string {
	var sent, read []string
	for i, ok := range r.took {
		if !ok {
			continue
		}
		label, _, _ := strings.Cut(r.labels[i], ":")
		switch r.kinds[i] {
		case kindSet, kindRadio, kindTx, kindPathHash:
			sent = append(sent, lowerFirst(label))
		case kindRead:
			read = append(read, lowerFirst(strings.TrimPrefix(label, "Check ")))
		}
	}
	if len(sent) == 0 {
		return "Nothing was saved on the node."
	}
	out := "Sent and saved: " + strings.Join(sent, ", ") + "."
	if len(read) > 0 {
		out += " Read back and checked: " + strings.Join(read, ", ") + "."
	}
	return out
}

// lowerFirst lowers a label's first letter, but not an acronym's ("TX power" stays).
func lowerFirst(s string) string {
	if len(s) > 1 && s[1] >= 'A' && s[1] <= 'Z' {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// setupLog is the one screen.log line for a setup that didn't work, in the steps' and the outcome's words.
func (r *setupRun) setupLog(o Outcome) string {
	p := r.j.Snapshot()
	at := ""
	for i := len(p.Steps) - 1; i >= 0; i-- {
		if s := p.Steps[i]; s.State == StepFailed || s.State == StepNow {
			at = fmt.Sprintf(" at step %d of %d (%s", i+1, p.Planned, s.Text)
			if s.Note != "" {
				at += ": " + s.Note
			}
			at += ")"
			break
		}
	}
	line := fmt.Sprintf("setup of %s%s: %s.", r.node.Port.Name, at, o.Title)
	for _, s := range append(append([]string(nil), o.Summary...), o.State, o.Recovery) {
		if s != "" {
			line += " " + s
		}
	}
	return line
}

// dropSteps forgets the plan's commands and checks, which hold the passwords, once nothing more will be sent.
func dropSteps(steps []nodecli.Step) {
	for i := range steps {
		steps[i] = nodecli.Step{Display: steps[i].Display}
	}
}
