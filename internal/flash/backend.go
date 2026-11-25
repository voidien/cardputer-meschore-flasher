package flash

// Phase 2 contract (docs/phase2-design.md): builder B writes the bodies and may add unexported fields to Backend.
// The method set is exactly ui.Backend (the build checks it in internal/app) and the signatures and doc comments are
// fixed. The bodies are in this package's other files: scan.go (Scan and the stale bootloader sessions), probe.go
// (Probe, ReadNode), files.go (Prepare, Download), flashjob.go and its nrf52.go and esp32.go (Flash), setupjob.go
// (Setup), words.go (what the screens and screen.log are told).
//
// Rules for every body:
//   - Download, Flash and Setup return their Job at once and never block: the board lock, every wait and every
//     open happen on the Job's own goroutine, so the screens can call them inside Update and watch the Job in the
//     same Update (two y presses can then never start two flashes). They stop only through Job.Cancel; their ctx is
//     context.Background() from the screens.
//   - Every port a Job, Probe or ReadNode opened is closed, and the board lock released, before Finish (or before
//     Probe or ReadNode returns), so the screens' next step never waits on the lock and the fake world never sees a
//     port open at the end.
//   - Logf and Outcome.Log by Deps.Logf's rules: never a raw reply, a command, a serialport.Log or a password.

import (
	"context"
	"sync"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/nodecli"
)

// Timeouts the backend adds to the Phase 1 packages' own.
const (
	// BoardBackWait is how long to wait for a board to come back on USB with the same serial number after a flash or
	// a setup's reboot (an ESP32-S3 TinyUSB app, an nRF52's app after STOP, a re-enumerating board after reboot).
	BoardBackWait = 15 * time.Second
	// FreshBootloaderWait is how long a retry after an nRF52 failure past START waits for the owner to press reset
	// twice, so the bootloader's port goes away and comes back (a fresh session: never retry in the old one).
	FreshBootloaderWait = 60 * time.Second
	// FirstBootWait is how long ReadNode waits for a node's console when firstBoot is set: the first boot after a
	// full wipe formats the file system before the console answers (UNVERIFIED how long; nodecli.ConfirmWait).
	FirstBootWait = nodecli.ConfirmWait
)

// Backend does what the screens ask of the boards and the network. It is safe for concurrent use; board operations
// (Probe, ReadNode, Flash, Setup) run one at a time, a later one waiting for the one before to finish, so the caller
// cancels a probe before it flashes.
//
// It remembers, for this run, every nRF52 board (by Port.DraftKey) whose bootloader session has had START sent
// into it, from the moment START is written until that board's app is seen back on USB, or its bootloader port is
// seen gone and then back (docs/phase2-design.md 4.4): such a session is stale, and DFU is never started in it again.
// A bootloader port already there at the first scan is stale too: only one seen to appear in this run is fresh
// (owner question 18). Nothing of this is saved across runs (owner questions 9 and 18: config.json holds nothing new).
type Backend struct {
	d Deps

	// board is the board lock: one board operation at a time. A channel, so a probe waiting for it can give up when
	// its context ends.
	board chan struct{}

	mu sync.Mutex
	// scanned says the first scan has been made: a bootloader listed then is stale (owner question 18).
	scanned bool
	// stale is every nRF52 bootloader session START may already have been sent into, by Port.DraftKey (4.4).
	stale map[string]staleDFU
}

// New makes a Backend over its dependencies.
func New(d Deps) *Backend {
	return &Backend{d: d, board: make(chan struct{}, 1), stale: map[string]staleDFU{}}
}

// Scan lists the plugged-in ports, from sysfs only: it never opens a port. An error means sysfs couldn't be read.
func (b *Backend) Scan() (Scan, error) { return b.scanPorts() }

// Probe asks a running node what it is, read-only (nodecli.Probe: ver, get role, board, within 2 s; never anything
// that could change the node, not even the blank line that would unstick it); once a repeater or room server has
// answered, it reads its settings for the Board screen's name with nodecli.ReadSettings given the Info with Role
// nodecli.RoleRepeater, so a room server is never asked its guest password (owner question 1: Node.Current, about
// half a second more). The screens call it for every port that appears on the Board screen; an nRF52 bootloader is
// never opened and comes back InBootloader with the Reason its kind calls for (a stale session, a bootloader not
// positively identified, or one already there at the first scan: owner question 18). A ZephCore build answering like
// MeshCore is NotSettable. The port is opened at 115200, exclusively, and closed before Probe returns. Cancelling ctx
// stops it within 100 ms.
func (b *Backend) Probe(ctx context.Context, p Port) Node { return b.probe(ctx, p) }

// LoadCatalog reads the saved catalog, with no network: catalog.ErrNoCatalog when there is none, catalog.ErrDamaged
// when a saved file has changed.
func (b *Backend) LoadCatalog() (*catalog.Catalog, error) { return b.d.Catalog.Load() }

// UpdateCatalog fetches both catalogs and their release lists and saves them, or changes nothing (the saved catalog
// stays) and says why: errors.Is catalog.ErrUnreachable for no network, a *catalog.FetchError with the HTTP status.
// Only ever called when the owner pressed u.
func (b *Backend) UpdateCatalog(ctx context.Context) (*catalog.Catalog, error) {
	cat, err := b.d.Catalog.Update(ctx)
	if err != nil {
		b.logf("catalog update failed, the saved catalog stays: %v", err)
	}
	return cat, err
}

// SavedFiles lists the downloaded files still on the SD card, newest first.
func (b *Backend) SavedFiles() ([]catalog.Entry, error) { return b.d.Catalog.List() }

// DeleteFile removes a saved file and its record. Only after the owner said y (S20).
func (b *Backend) DeleteFile(name string) error { return b.d.Catalog.Delete(name) }

// Download fetches files one after another (catalog.Client.Download, with the GitHub fallback for MeshCore's own
// release files), one Step each with its bytes, and finishes with the Entries saved, or with why not. Only ever
// started when the owner pressed d, or y on a file not yet saved. It returns at once.
func (b *Backend) Download(ctx context.Context, files []catalog.File) *Job {
	return b.download(ctx, files)
}

// Prepare checks a Request without touching the board (Fits, catalog.Choose, the saved file's SHA-256, the image's
// chip or the DFU package and its SoftDevice), for the Confirm screen. It reads and hashes the saved file, so it is
// called from a tea.Cmd. It never downloads.
func (b *Backend) Prepare(req Request) Prepared { return b.prepare(req) }

// Flash runs one flash from start to end: every check of Prepare again on the bytes it then writes (Fits first; a
// file not saved, or changed since, is "Not flashed": Flash never downloads), finding the board by its USB serial
// number (or USB path), the ESP32 ROM bootloader or the nRF52 1200-baud touch and serial DFU, the MD5 check (ESP32)
// or the board coming back running the version just flashed (nRF52), and where the board is afterwards. A board
// whose bootloader session is stale (see Backend) gets a first step, "Press the board's reset button twice quickly",
// that waits up to FreshBootloaderWait for its bootloader port to go and come back, and never uses the port as it
// is (owner question 17: no Phase 1 change; a double press the scans missed is asked for again). Every flash that
// made a 1200-baud touch ends, however it ends, by opening the tty now on the touched name (if any) at 115200 and
// closing it, so no later program's close reboots the board into DFU. Every failure finishes the Job with what the
// board is left with and how to recover it. Job.Cancel stops it the safe way (on an ESP32 the RTS pulse that takes
// the board out of its bootloader). It returns at once.
func (b *Backend) Flash(ctx context.Context, req Request) *Job { return b.flash(ctx, req) }

// ReadNode opens a node for setup: probes it (waiting FirstBootWait rather than 2 s when firstBoot, right after a
// flash) and reads its current settings, read-only. The Node's Problem says why it can't be set up when it can't.
func (b *Backend) ReadNode(ctx context.Context, p Port, firstBoot bool) Node {
	return b.readNode(ctx, p, firstBoot)
}

// Setup sends a setup end to end: nodecli.Plan and Run (one command at a time, each checked, then every value read
// back), the reboot, waiting for the board to come back (by serial number), and nodecli.Confirm. Steps are the
// plan's (their Text is the nodecli.Step's Display: a password shows as ••••), and every picture's Planned is how
// many steps there are in all, for the screen lock's "step 4 of 20". A failure finishes with the nodecli.StepError's
// State. Job.Cancel (Esc and y, or a Ctrl+C, which quits at once: owner question 16) stops it between commands, and
// the Outcome then says what was sent and read back (Summary), what the node is left with (State: "Its radio is
// saved but it hasn't restarted: it stays on its old radio until it restarts.") and how to finish (Recovery), which
// internal/app writes to screen.log. The admin password is dropped as soon as it has been sent. It returns at once.
func (b *Backend) Setup(ctx context.Context, req SetupRequest) *Job { return b.setup(ctx, req) }

// Battery is the Zero's charge in percent, and whether it has a battery (Deps.Battery): (-1, true) is a battery that
// can't be read now.
func (b *Backend) Battery() (percent int, ok bool) {
	if b.d.Battery == nil {
		return 0, false
	}
	return b.d.Battery()
}

// ClockSynced says the Zero's clock is synchronised (Deps.ClockSynced).
func (b *Backend) ClockSynced() bool { return b.d.ClockSynced != nil && b.d.ClockSynced() }

// MessengerRadio is the messenger's radio settings (nodecli.ReadMessengerRadio on Deps.MessengerPath), read only,
// and a note for the setup form when the file was skipped ("" when it was used, or when there is no path).
func (b *Backend) MessengerRadio() (nodecli.MessengerRadio, string) {
	if b.d.MessengerPath == "" {
		return nodecli.MessengerRadio{}, ""
	}
	return nodecli.ReadMessengerRadio(b.d.MessengerPath)
}

// logf writes one line to screen.log (Deps.Logf), by its rules: never a raw reply, a command or a password.
func (b *Backend) logf(format string, args ...any) {
	if b.d.Logf != nil {
		b.d.Logf(format, args...)
	}
}
