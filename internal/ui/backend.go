package ui

// Phase 2 contract (docs/phase2-design.md): this file is fixed. *flash.Backend satisfies Backend and *store.Store
// satisfies Store; internal/app checks both at build time.

import (
	"context"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/store"
)

// Backend is everything the screens ask of the boards, the network and the device. *flash.Backend is the real one;
// the tests' fake and flash over a fakeworld stand in for it. Two rules say where each call is made:
//
//   - Download, Flash and Setup start a Job and return it at once, doing no I/O before they return: the screens
//     call them in Update, only when no Job runs (m.canStart), and watch the Job in the same Update, so a second y
//     can never start a second one. Their ctx is context.Background(); a Job stops only through Job.Cancel.
//   - Every other call (Scan, Probe, LoadCatalog, UpdateCatalog, SavedFiles, DeleteFile, Prepare, ReadNode,
//     Battery, ClockSynced, MessengerRadio) is made only inside a tea.Cmd, never in Update or View, its arguments
//     captured before the Cmd is returned (the kit's rule, which keeps -race clean). View reads only what a Cmd's
//     message stored (the battery, for one: frame.go reads it every 30 s).
type Backend interface {
	// Scan lists the plugged-in ports from sysfs, opening none.
	Scan() (flash.Scan, error)
	// Probe asks a running node what it is (read-only, about 2 s for a silent one).
	Probe(ctx context.Context, p flash.Port) flash.Node
	// LoadCatalog reads the saved catalog (no network).
	LoadCatalog() (*catalog.Catalog, error)
	// UpdateCatalog fetches the catalog (only after u).
	UpdateCatalog(ctx context.Context) (*catalog.Catalog, error)
	// SavedFiles lists the downloaded files, newest first; DeleteFile removes one (only after y).
	SavedFiles() ([]catalog.Entry, error)
	DeleteFile(name string) error
	// Download fetches files (only after d, or y on a file not saved yet).
	Download(ctx context.Context, files []catalog.File) *flash.Job
	// Prepare checks a flash without touching the board, for the Confirm screen.
	Prepare(req flash.Request) flash.Prepared
	// Flash runs one flash (only after y on Confirm).
	Flash(ctx context.Context, req flash.Request) *flash.Job
	// ReadNode probes a node and reads its settings, for the Setup form.
	ReadNode(ctx context.Context, p flash.Port, firstBoot bool) flash.Node
	// Setup sends a setup (only after y on Setup).
	Setup(ctx context.Context, req flash.SetupRequest) *flash.Job
	// Battery is the Zero's charge and whether it has a battery; (-1, true) is a battery that can't be read now.
	Battery() (percent int, ok bool)
	// ClockSynced says the Zero's clock is synchronised, so setup sets the node's.
	ClockSynced() bool
	// MessengerRadio is the messenger's radio settings, to be used when they are Found, and a note for the form when
	// its file was skipped ("" when it was used, and when no file is read at all).
	MessengerRadio() (nodecli.MessengerRadio, string)
}

// Store is what the screens remember: *store.Store. Its calls are small local file writes, made from Update.
// RememberBoard takes a board's flash.Port.Key (its USB serial in one form, never ""); the drafts take its
// flash.Port.DraftKey (that serial, or its USB socket and identity for a board without a useful one).
type Store interface {
	RememberBoard(serial, device string) error
	SaveRadio(r store.Radio) error
	LoadDraft(key string) (store.Draft, bool)
	SaveDraft(key string, d store.Draft) error
	DeleteDraft(key string) error
}

// Options start the program.
type Options struct {
	// Backend does the work; Store remembers; Config is config.json as read at start (the model keeps its copy up to
	// date as it remembers boards and radios).
	Backend Backend
	Store   Store
	Config  store.Config
	// Name is the program's own name ("meshflash", or the demo build's "meshflash-demo": internal/app passes
	// Program.Name): the Board screen's title, and Help's "<Name> <Version> for CardputerZero" and folders
	// (~/.config/<Name>, ~/.cache/<Name>). "" is "meshflash".
	Name string
	// Version is meshflash's, shown in Help.
	Version string
	// Log is screen.log (S5): what went wrong, one line each, never a password. nil writes nothing. internal/app
	// gives the same locked writer here, to the backend's Logf and to the demo's world, so lines never interleave.
	Log io.Writer
	// ShowBattery puts the battery's charge on the status line ("87% ●"): --screen sets it, since the launcher's
	// bar is frozen while meshflash draws the LCD. The low-battery question on Confirm works either way.
	ShowBattery bool
	// TerminalKeys says the keys come from a terminal (the CLI app: internal/app sets it when not --screen), where a
	// held key arrives as the same key again and again and its release is never seen: the frame then finds the
	// screen lock's Tab hold from Tab's repeats, and drops them (lock.go). false under --screen, where
	// internal/screen reads each press and release, never sends a held Tab's repeats, and sends LockMsg once Tab has
	// been held 3 s (screen.Options.TabHeld); and in tests, unless a test of the terminal's rule sets it.
	TerminalKeys bool
	// Now is the screens' clock (nil: time.Now). It is never the backend's clock: tests drive it themselves.
	Now func() time.Time
	// After is how the screens wait before a message comes back (ticks for elapsed times, the Board screen's
	// rescans): nil is tea.Tick. Tests record the message and deliver it when they move their clock.
	After func(d time.Duration, msg tea.Msg) tea.Cmd
	// Wait is how the screens wait for a Job's next picture (a flash.Progress newer than seen): nil is a Cmd that
	// blocks in Job.Next. Tests record it and deliver pictures when they choose.
	Wait func(j *flash.Job, seen uint64) tea.Cmd
}
