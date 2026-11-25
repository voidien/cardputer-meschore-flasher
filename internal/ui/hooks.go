package ui

// Phase 2 contract (docs/phase2-design.md): this file is fixed.
//
// The frame (frame.go, steps.go, editor.go, text.go: fixed and complete), the screens (model.go and the screen
// files: builder C) and Setup (setup.go, presets.go and the other setup*.go files: builder D) meet only through the
// methods named below. The assignments make the build fail if anyone changes one's signature, so nobody waits for
// anybody: each builder tests their screens through the real frame, with the other's methods still placeholders.
//
// What the frame does (frame.go has it all):
//
//   - Update: the size; a key (while locked, the lock alone: lock.go; a held Tab's repeats dropped, its 3 s hold
//     locking; Ctrl+C: quit at once, or ask confirmQuit while a flash Job runs, a setup being stopped with no
//     question; then Help; then an open question, where any key but y says no and a held y does nothing; then ?
//     unless a text box is open; then setupKey on scrSetup, screenKey elsewhere); a Job's flash.Progress (stale ones
//     dropped; the next waited for; at Done m.job and m.busy let go, Help closed, a stop or quit question dropped;
//     then setupProgress for a JobSetup picture, jobProgress for the others); its own scans and battery; the lock's
//     LockMsg and timer; then screenMsg; then setupResult.
//   - It scans sysfs (Backend.Scan) every second on every screen into m.scan and calls afterScan; it reads the
//     battery every 30 s into m.battery while ShowBattery.
//   - View: the status line (the title from setupTitle or screenTitle, the battery, the mark from m.busy and
//     setupSubject or screenSubject against m.scan), the body and hint from setupView or screenView (or Help), the
//     question (setupConfirmText or screenConfirmText) or m.note, and the hint line. While locked, the lock page
//     instead (or nothing, dark), and no hook's View is called.
//   - Help: "Keys", setupHelp or screenHelp, the lock ("hold tab"), Ctrl+C, and the common footer.
//   - quitNow and Shutdown: SaveUnsent (saveSetupDraft on scrSetup), closeSetup (Shutdown), cancelProbes, and the Job
//     cancelled. toBoard: closeSetup when leaving scrSetup, then Board, a rescan, and boardOpened.
//
// Rules for both builders:
//
//   - Download, Flash and Setup are called in Update, only when canStart(), with context.Background(), and the Job
//     is given to watch in the same Update. Every other Backend call is made inside a tea.Cmd that captures its
//     arguments before it is returned.
//   - Ask a question only with ask(k); builder D asks only confirmStopSetup, and only on scrSetup.
//   - A y is ignored, starting nothing, while m.yHeld (a y less than heldYGap after the previous y: a held y
//     repeating) on Confirm and Setup.
//   - A Tab reaching screenKey or setupKey is one press: the frame drops a held Tab's repeats (a terminal's) and
//     internal/screen never sends them (the LCD's), so Tab keeps its own meaning (S21's next field) once per press.
//     Neither builder handles a held key or the lock, and no key reaches either while locked.
//   - jobProgress and setupProgress do nothing, and never panic, for a picture of a Job their screens didn't start
//     (the frame's own tests, lock_test.go and quit_test.go, watch Jobs from the Board screen); likewise
//     screenConfirmText and setupConfirmText only ever word their own questions.
//   - m.note is one short sentence; longer words go in the screen's body.
//   - Builder D reads m.backend, m.store, m.cat, m.cfg, m.job, m.jobSeenAt, m.scan, m.name, and may write m.note,
//     m.busy (incremented while ReadNode runs, decremented when its answer comes) and m.cfg.Radio (after a setup
//     sent a radio). Builder D never changes m.screen except through toBoard, and never reads or writes builder C's
//     screen fields or m.flow. Builder C never reads or writes m.setup.
//   - Every top-level name builder D declares, in code or tests (types, message types, funcs, vars, consts, test
//     helpers), starts with setup, preset or pick (or Setup, Preset, Pick in test names, plus TestEditor… and
//     TestNoGoldenHoldsThePassword); builder C never uses those prefixes. Nobody declares a name starting with frame
//     or lock (or Lock), no test named TestLock…, TestCtrlC…, TestShutdownLogs… or TestNothingLocks… (lock_test.go
//     and quit_test.go are the integrator's; builder C's TestQuitWhileFlashingAsks is C's own), and no name already
//     declared in a fixed file or test file (lines, writing, watchJob, runningJob, radioSaved, lockDriver among them).

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
)

var (
	// Builder D's, in setup.go and the files it adds:

	// openSetup starts Setup for the board on p, whose catalog board is device ("" when not known: then only the
	// board's own `board` reply sets the TX power limit), reading the node first (firstBoot right after a flash, for
	// the longer wait). It sets m.screen = scrSetup. Builder C cancels the Board's probes before calling it.
	_ func(*Model, flash.Port, string, bool) tea.Cmd = (*Model).openSetup
	// setupKey handles a key on scrSetup (after the frame's lock, held Tab, Ctrl+C, Help, question and ? handling).
	_ func(*Model, tea.KeyMsg) tea.Cmd = (*Model).setupKey
	// setupView draws scrSetup's body in w×h cells (scrolling or fitting its own: fitSteps, window) and gives its
	// key hint, one that fits w (S12: fitHint), with "? help" outside a text box.
	_ func(*Model, int, int) ([]string, string) = (*Model).setupView
	// setupTitle is the status line's title on scrSetup.
	_ func(*Model) string = (*Model).setupTitle
	// setupSubject is the board scrSetup is about, for the status mark: the node's port while reading and on the
	// form and while sending, and the Outcome's Board once the setup has ended (nil when it's gone). named is true.
	_ func(*Model) (*flash.Port, bool) = (*Model).setupSubject
	// setupEditing says a text box is open on scrSetup: ? (and y, j, k) are then letters.
	_ func(*Model) bool = (*Model).setupEditing
	// setupResult takes a message nobody else knows; ok says it was Setup's (ReadNode's answer, its ticks).
	_ func(*Model, tea.Msg) (tea.Cmd, bool) = (*Model).setupResult
	// setupProgress takes a picture of the running setup Job, after the frame's bookkeeping. At Done it saves
	// Outcome.Sent (store.SaveRadio(SavedRadio(*Sent)) and m.cfg.Radio) whether or not the setup worked, deletes the
	// draft when it did, and keeps it when it didn't. It may come while the screen is locked: Setup moves on
	// underneath and shows its end once unlocked.
	_ func(*Model, flash.Progress) tea.Cmd = (*Model).setupProgress
	// setupHelp is Help's list of scrSetup's keys, for the state Setup is in ("key" padded to 15 runes, then what it
	// does).
	_ func(*Model) []string = (*Model).setupHelp
	// setupConfirmText words a question on scrSetup: confirmStopSetup, by the picture's flash.Risk. (Ctrl+C while
	// sending asks nothing: the frame quits at once and logs what the node was left with: owner question 16.)
	_ func(*Model, confirmKind) string = (*Model).setupConfirmText
	// setupConfirm does one of Setup's questions after y (the frame does confirmQuit itself).
	_ func(*Model, confirmKind) tea.Cmd = (*Model).setupConfirm
	// saveSetupDraft keeps the open form as a draft for its board (S7), under flash.Port.DraftKey (owner question
	// 13): no passwords; nothing for a board without a key; the draft removed when the form is back at its
	// defaults. It acts only while the form shows (the form, a text box, a pick list, the presets), never while
	// reading, sending (y already saved it) or done, so after a setup that worked it can't bring back the draft just
	// deleted; and it may run twice in a row (a quit, then Shutdown). When Esc has just cancelled a text box, the
	// text it held stays that row's draft value for 3.5 s on the screens' clock (an Esc hold reaches the model as one
	// Esc first, then the SIGTERM's Shutdown: S7); any other key drops it, and the screen shows the old value
	// throughout.
	_ func(*Model) = (*Model).saveSetupDraft
	// closeSetup ends Setup: clears both passwords (the form's, the open text box's and any value kept to restore on
	// Esc, wiped with editor.Clear) and cancels a ReadNode still running. Safe to call when Setup never opened, and
	// twice.
	_ func(*Model) = (*Model).closeSetup

	// Builder C's, in model.go and its files (placeholders in model.go until then):

	// start is what the screens do when the program starts (Init batches it after the first scan): the catalog.
	_ func(*Model) tea.Cmd = (*Model).start
	// screenKey handles a key on every screen but scrSetup (after the same frame handling as setupKey).
	_ func(*Model, tea.KeyMsg) tea.Cmd = (*Model).screenKey
	// screenView draws every screen but scrSetup in w×h cells, and gives its hint (as setupView).
	_ func(*Model, int, int) ([]string, string) = (*Model).screenView
	// screenTitle is the status line's title on every screen but scrSetup (Board's is m.name).
	_ func(*Model) string = (*Model).screenTitle
	// screenSubject is the board the screen is about, for the status mark: named false on a screen about no board
	// (Update catalog, Saved files; Board with no port selected), else the port (Board's selected one; flow.port from
	// Choose board to Flashing; the Outcome's Board on Result, nil when it's gone).
	_ func(*Model) (*flash.Port, bool) = (*Model).screenSubject
	// screenHelp is Help's list of the screen's keys.
	_ func(*Model) []string = (*Model).screenHelp
	// screenTextBox says a text box is open (Choose board's find box).
	_ func(*Model) bool = (*Model).screenTextBox
	// screenConfirmText words the questions of every screen but scrSetup; screenConfirm does one after y.
	_ func(*Model, confirmKind) string  = (*Model).screenConfirmText
	_ func(*Model, confirmKind) tea.Cmd = (*Model).screenConfirm
	// screenMsg takes builder C's own messages; ok says it was one.
	_ func(*Model, tea.Msg) (tea.Cmd, bool) = (*Model).screenMsg
	// jobProgress takes a picture of a download or flash Job, after the frame's bookkeeping (at Done: to Result or
	// back where the download came from, in the same Update: S16, locked or not: Result shows once unlocked).
	_ func(*Model, flash.Progress) tea.Cmd = (*Model).jobProgress
	// afterScan is told of every scan the frame keeps (m.scan; m.scanErr when it failed), with the ports before it:
	// the Board's probes for new ports and dropped lines for gone ones, a Confirm whose board went.
	_ func(*Model, []flash.Port) tea.Cmd = (*Model).afterScan
	// boardOpened is called by toBoard once the Board shows again.
	_ func(*Model) tea.Cmd = (*Model).boardOpened
	// cancelProbes stops every probe under way (before a flash or a setup, and at a quit).
	_ func(*Model) = (*Model).cancelProbes

	// The frame's, for both (frame.go, steps.go):

	// toBoard goes back to the Board screen, rescanning, with note shown once ("" for none).
	_ func(*Model, string) tea.Cmd = (*Model).toBoard
	// canStart says no Job runs: call Download, Flash or Setup only then.
	_ func(*Model) bool = (*Model).canStart
	// watch makes j the running Job (m.job; m.busy++ for a flash or setup) and returns the Cmd that waits for its
	// first picture.
	_ func(*Model, *flash.Job) tea.Cmd = (*Model).watch
	// ask opens a question.
	_ func(*Model, confirmKind) = (*Model).ask
	// rescan scans now (and every second after).
	_ func(*Model) tea.Cmd = (*Model).rescan
	// listed says a port is plugged in now (the last scan has the same board).
	_ func(*Model, flash.Port) bool = (*Model).listed
	// after returns a Cmd that brings msg back after d (Options.After). Call it only in Update, never in a Cmd.
	_ func(*Model, time.Duration, tea.Msg) tea.Cmd = (*Model).after
	// now is the screens' clock (Options.Now).
	_ func(*Model) time.Time = (*Model).now
	// size is the grid, 45×10 until the first tea.WindowSizeMsg.
	_ func(*Model) (int, int) = (*Model).size
	// logf writes one line to screen.log (Options.Log). Never give it a password.
	_ func(*Model, string, ...any) = (*Model).logf
	// stepLines lists a Job's steps in w cells (✓ done, ▸ under way or stopped, · skipped); fitSteps fits them in h
	// lines, folding the oldest into "✓ 3 earlier steps" so the running or failed one stays in view.
	_ func(flash.Progress, int, time.Duration) []string      = stepLines
	_ func(flash.Progress, int, int, time.Duration) []string = fitSteps
)
