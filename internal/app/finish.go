package app

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"meshflash/internal/flash"
	"meshflash/internal/store"
	"meshflash/internal/ui"
)

// The waits of the end of the program. They are the whole of it: once SIGTERM and SIGHUP are absorbed only SIGKILL
// can end a hang, and the launcher sends that 3 s after its SIGTERM (S6).
var (
	// shutdownWait is how long a Job stopped by the end of the program has to finish stopping (an ESP32 let out of
	// its bootloader, the port closed, the setup's last command answered) before screen.log is told how it ended.
	shutdownWait = 1500 * time.Millisecond
	// lostWait is how long a flash or setup is left to finish with no screen after a crash or a closed terminal
	// (owner question 19: "up to about 3 minutes"); then it is stopped the safe way, as a quit would.
	lostWait = 3 * time.Minute
	// timeAfter is the waits' clock: tests give channels of their own, so none waits in real time.
	timeAfter = time.After
)

// ending is how the screens ended, which decides what happens to a Job still running.
type ending struct {
	// lost says they ended without being told to: a panic Bubble Tea caught, or the terminal gone. why says which,
	// for screen.log.
	lost bool
	why  string
}

// model is what finish needs of the screens: *ui.Model, or a test's stand-in with a Job of its own.
type model interface {
	Shutdown() *flash.Job
	SaveUnsent()
	Running() *flash.Job
}

// radios is where a setup's radio is remembered: *store.Store.
type radios interface {
	SaveRadio(store.Radio) error
}

var (
	_ model  = (*ui.Model)(nil)
	_ radios = (*store.Store)(nil)
)

// finish ends the program once the screens have ended, on the model the app made (docs/phase2-design.md 7.1):
//
//   - After a quit (Ctrl+C, Ctrl+C and y while flashing, Esc on Board, an Esc hold, SIGTERM; locked or not),
//     Shutdown saves what's unsent and stops the running Job the safe way; the frame has already written to
//     screen.log where it was and what stopping there leaves (logStop), so the log says it even if the Job outlives
//     this. finish waits for the Job at most shutdownWait and writes how it ended. Nothing about the stopped flash
//     goes in config.json (owner question 9).
//   - After a crash or a closed terminal, SaveUnsent, and a flash or a setup under way is left to finish with no
//     screen, for up to lostWait (owner question 19); on the LCD screen.Run has already turned the backlight on and
//     blanked the screen. A download is stopped at once: no board depends on it, and waiting on it with no screen
//     helps nobody.
//
// Either way a setup that ended with its radio taken by the node (Outcome.Sent) has the radio saved, as the screens
// would have, had they seen the end: the node keeps it.
//
// stops are the SIGTERMs and SIGINTs that arrive meanwhile (absorbSIGTERM; nil for none): with no screen left to
// turn them into a quit, they stop a Job left to finish alone the safe way, at once.
func finish(m model, end ending, st radios, log *logWriter, name string, stderr io.Writer, stops <-chan os.Signal) {
	var j *flash.Job
	if !end.lost {
		guard(log, name, "stopping", func() { j = m.Shutdown() })
		if j != nil {
			waitFor(j, shutdownWait)
		}
	} else {
		guard(log, name, "saving what was typed", m.SaveUnsent)
		if j = m.Running(); j != nil {
			finishAlone(j, end, log, name, stderr, stops)
		}
	}
	if j == nil {
		return
	}
	logEnd(j, log, name)
	saveSent(j, st, log, name)
}

// finishAlone lets a Job the screens can no longer show finish by itself, bounded: lostWait for a flash or a setup,
// then stopped the safe way, or at once when a SIGTERM (the launcher's, at the end of an Esc hold) or a SIGINT
// (Ctrl+C in the CLI app's cooked terminal) arrives on stops; a download is stopped at once.
func finishAlone(j *flash.Job, end ending, log *logWriter, name string, stderr io.Writer, stops <-chan os.Signal) {
	what, ok := jobWords[j.Kind()]
	if !ok {
		j.Cancel()
		waitFor(j, shutdownWait)
		return
	}
	log.linef("%s: the screens ended (%s) while the %s went on: it is left to finish with no screen, for up to %s",
		name, end.why, what, minutes(lostWait))
	// On the LCD this goes to the launcher's journal; after a crash in the CLI app the owner sees it under Bubble
	// Tea's account of the panic, and knows why meshflash hasn't ended yet.
	fmt.Fprintf(stderr, "%s: the %s goes on with no screen; waiting up to %s for it to end (screen.log says how)\n",
		name, what, minutes(lostWait))
	select {
	case <-j.Done():
		return
	case sig := <-stops:
		log.linef("%s: told to end (%s) while the %s went on with no screen: stopping it the safe way", name,
			signalName(sig), what)
	case <-timeAfter(lostWait):
		log.linef("%s: the %s hadn't ended after %s: stopping it the safe way", name, what, minutes(lostWait))
	}
	j.Cancel()
	waitFor(j, shutdownWait)
}

// signalName names a signal as screen.log does: "SIGTERM", "SIGINT".
func signalName(sig os.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	}
	return fmt.Sprint(sig)
}

// jobWords name the Jobs whose end screen.log is told: a flash and a setup, which change a board. A download changes
// nothing on one, and a quit stops it quietly, as the frame does (logStop).
var jobWords = map[flash.JobKind]string{flash.JobFlash: "flash", flash.JobSetup: "setup"}

// logEnd writes to screen.log how a flash or a setup cut off by the end of the program ended: its Outcome's Title,
// Summary, State and Recovery (what the board or node was sent, what it is left with, how to recover or finish:
// owner questions 9 and 16), or that it was still stopping. These are the backend's plain words, which never hold a
// password (flash.Outcome, Deps.Logf).
func logEnd(j *flash.Job, log *logWriter, name string) {
	what, ok := jobWords[j.Kind()]
	if !ok {
		return
	}
	p := j.Snapshot()
	if !p.Done {
		line := fmt.Sprintf("%s: the %s was still stopping when %s ended", name, what, name)
		switch p.Risk {
		case flash.RiskErased:
			line += ": its app is erased, so the board has no working firmware until it is flashed again"
		case flash.RiskBootloader:
			line += ": nothing was written, but it may be in its bootloader: press its reset button once to start it"
		}
		log.linef("%s.", line)
		return
	}
	o := p.Outcome
	log.linef("%s: how the %s ended: %s", name, what, cmp.Or(o.Title, "ended"))
	for _, s := range o.Summary {
		log.linef("%s:   %s", name, s)
	}
	if o.State != "" {
		log.linef("%s:   Left with: %s", name, o.State)
	}
	if o.Recovery != "" {
		log.linef("%s:   What to do: %s", name, o.Recovery)
	}
	// Not o.Log: the backend wrote that line to screen.log itself as the Job finished (flash.Outcome.Log has one
	// writer), so writing it here would put it there twice.
}

// saveSent remembers the radio a setup's node took (config.json, SPEC.md 8), when the Job has ended with one.
func saveSent(j *flash.Job, st radios, log *logWriter, name string) {
	if j.Kind() != flash.JobSetup {
		return
	}
	p := j.Snapshot()
	if !p.Done || p.Outcome.Sent == nil {
		return
	}
	if err := st.SaveRadio(ui.SavedRadio(*p.Outcome.Sent)); err != nil {
		log.linef("%s: the node took its new radio, but it can't be remembered in %s: %v", name, store.ConfigFile, err)
	}
}

// waitFor waits for j to finish, at most d, and says whether it did.
func waitFor(j *flash.Job, d time.Duration) bool {
	select {
	case <-j.Done():
		return true
	default:
	}
	select {
	case <-j.Done():
		return true
	case <-timeAfter(d):
		return false
	}
}

// guard calls f, one of the model's, and writes a panic in it to screen.log rather than end the program with it:
// after a crash the model may be in any state, and the flash still under way matters more than the draft.
func guard(log *logWriter, name, doing string, f func()) {
	defer func() {
		if r := recover(); r != nil {
			log.linef("%s: %s failed: %v", name, doing, r)
		}
	}()
	f()
}

// minutes says d as screen.log does: "3 min", "1.5 s".
func minutes(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d min", d/time.Minute)
	}
	return fmt.Sprintf("%g s", d.Seconds())
}
