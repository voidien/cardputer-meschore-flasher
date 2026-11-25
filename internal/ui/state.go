package ui

// Phase 2 contract (docs/phase2-design.md): this file is fixed. It is the model's shape: the fields the frame
// (frame.go) keeps, and one field per screen whose type belongs to that screen's builder (declared in their files),
// so each can add state without touching this file.

import (
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/store"
)

// screen is which screen is showing. Help is not a screen: it is drawn over the one it was opened on (Model.help).
type screen int

// The screens (SPEC.md 3, 4).
const (
	scrBoard    screen = iota // the start: plugged-in ports and what they run
	scrChoose                 // Choose board
	scrRole                   // the board's roles
	scrVersion                // the role's versions
	scrNotes                  // a version's release notes
	scrConfirm                // Flash this?
	scrDownload               // downloading files (from Confirm's y on a file not saved, or Version's d)
	scrFlashing               // the flash under way
	scrResult                 // how the flash ended
	scrSetup                  // the Setup form, and the setup under way and its end (builder D's)
	scrUpdate                 // Update catalog
	scrFiles                  // Saved files
)

// confirmKind is a question waiting for y (S20): any other key says no, and a held y (Model.yHeld) neither answers
// nor dismisses it. While one is asked, the message line shows its text and the hint line "y yes  any other key no".
// Ask one with Model.ask. While the screen is locked no key reaches a question: it waits, unanswered, underneath.
type confirmKind int

// The questions. Their words are builder C's (screenConfirmText), on scrSetup builder D's (setupConfirmText).
const (
	noConfirm confirmKind = iota
	// confirmStop: Esc on Flashing, worded by the running picture's flash.Risk: "Stop now? Nothing has been written
	// yet: the board keeps its firmware." (RiskNone), SPEC's "Stop now? The board will have no working firmware
	// until it's flashed again." (RiskErased), "Stop now? It's already written; meshflash just won't check it."
	// (RiskWritten).
	confirmStop
	// confirmQuit: Ctrl+C while a flash Job runs: confirmStop's words, ending "…and meshflash quits." y quits (the
	// frame's quitNow stops the Job). Ctrl+C while a setup is being sent asks nothing: it quits at once, stopping
	// the setup where it is (owner question 16), so this is never asked on scrSetup.
	confirmQuit
	// confirmLowBattery: y on Confirm under 30%, or with a battery that can't be read: "Battery 24%. Flash anyway?",
	// "Battery level unknown. Flash anyway?".
	confirmLowBattery
	// confirmUnchecked: y on Confirm when Prepared.Ask is set (owner question 20): its text.
	confirmUnchecked
	// confirmDelete: x on Saved files: "Delete <file>? It downloads again when needed."
	confirmDelete
	// confirmStopSetup: Esc while a setup is being sent (builder D's), worded by the picture's flash.Risk: "Stop now?
	// Nothing has been sent yet." (RiskNone), "Stop now? The node keeps what it was sent so far." (RiskSettings),
	// "…so far, and switches to the new radio when it restarts." (RiskRadioSent). Only Esc asks it: Ctrl+C quits.
	confirmStopSetup
)

// Model is the whole program, as one Bubble Tea model with pointer receivers: Update returns the same *Model, and
// internal/app type-asserts the final model as *Model.
type Model struct {
	// From Options.
	backend     Backend
	store       Store
	cfg         store.Config // config.json, kept up to date as boards and radios are remembered
	name        string       // "meshflash", or "meshflash-demo"
	version     string
	log         io.Writer
	showBattery bool
	// terminalKeys: the keys come from a terminal, where a held Tab is found from its repeats (lock.go).
	terminalKeys bool
	nowFn        func() time.Time
	afterFn      func(time.Duration, tea.Msg) tea.Cmd
	waitFn       func(*flash.Job, uint64) tea.Cmd

	// The frame (frame.go draws it and keeps these).
	w, h      int
	screen    screen
	help      bool // Help is showing, over screen
	helpTop   int
	note      string // one short sentence (the kit's "flash"): cleared by the next key; longer text goes in a body
	confirm   confirmKind
	confirmAt time.Time // when the question was asked
	busy      int       // board operations in flight (probes, ReadNode, a flash or setup Job): "…" on the status line
	quit      bool      // the program is ending: View draws nothing
	// lastY is when y was last pressed (zero before the first); yHeld says the key being handled is a y less than
	// heldYGap after it: a held y repeating, which answers no question and starts nothing.
	lastY time.Time
	yHeld bool

	// The USB ports as last scanned: the frame reads sysfs (Backend.Scan, which opens no port) every second on
	// every screen. scanned says the first scan has come back; scanErr is the last scan's error, when it failed (the
	// ports then stay as they were); scanGen drops a superseded scan (rescan).
	scan    flash.Scan
	scanErr error
	scanned bool
	scanGen int
	// The battery as the frame last read it (every 30 s while the status line shows it, ShowBattery): percent -1
	// with hasBattery is a battery that can't be read now. Confirm reads its own, fresh, before it flashes.
	battery    int
	hasBattery bool

	// The catalog, loaded at start and after an update; catErr says why there is none (catalog.ErrNoCatalog,
	// catalog.ErrDamaged).
	cat    *catalog.Catalog
	catErr error

	// The running Job, if any: at most one at a time (a download, a flash or a setup). watch sets it; the frame's
	// Progress handling clears it when the Job finishes. jobSeenAt is when its last picture arrived, on the screens'
	// clock, for the running step's time between pictures.
	job       *flash.Job
	jobSeenAt time.Time

	// The screen lock (lock.go, the frame's): while lock.on every key goes to it and View draws its page.
	lock lockState

	// What has been picked so far on the way to a flash (builder C's type).
	flow flow

	// Each screen's own state, its type its builder's: builder C's in model.go and the screen files, builder D's
	// (setupScreen) in setup.go.
	board  boardScreen
	choose chooseScreen
	role   roleScreen
	ver    versionScreen
	notes  notesScreen
	conf   confirmScreen
	dl     downloadScreen
	fl     flashingScreen
	res    resultScreen
	upd    updateScreen
	files  filesScreen
	setup  setupScreen
	more   modelMore // anything else builder C's model needs (tick generations, the probe queue, …)
}
