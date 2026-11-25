package flash

// Phase 2 contract (docs/phase2-design.md): builder B writes the bodies and may add unexported fields to Backend.
// The method set is exactly ui.Backend (the build checks it in internal/app) and the signatures and doc comments are
// fixed. The placeholders below do no I/O: they report "not built yet", and every Job they make is already finished,
// so a caller never waits on one.
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
	"errors"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/nodecli"
)

var errNotBuilt = errors.New("flash: not built yet (Phase 2 contract placeholder)")

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
type Backend struct {
	d Deps
}

// New makes a Backend over its dependencies.
func New(d Deps) *Backend { return &Backend{d: d} }

// Scan lists the plugged-in ports, from sysfs only: it never opens a port. An error means sysfs couldn't be read.
func (b *Backend) Scan() (Scan, error) { return Scan{}, errNotBuilt }

// Probe asks a running node what it is, read-only (nodecli.Probe: ver, get role, board, within 2 s; never anything
// that could change the node, not even the blank line that would unstick it). The screens call it for every port
// that appears on the Board screen; an nRF52 bootloader is never opened and comes back InBootloader with the Reason
// its kind calls for (a stale session, a bootloader not positively identified). A ZephCore build answering like
// MeshCore is NotSettable. The port is opened at 115200, exclusively, and closed before Probe returns. Cancelling ctx
// stops it within 100 ms.
func (b *Backend) Probe(ctx context.Context, p Port) Node {
	return Node{Port: p, Problem: Failed, Reason: errNotBuilt.Error()}
}

// LoadCatalog reads the saved catalog, with no network: catalog.ErrNoCatalog when there is none, catalog.ErrDamaged
// when a saved file has changed.
func (b *Backend) LoadCatalog() (*catalog.Catalog, error) { return nil, errNotBuilt }

// UpdateCatalog fetches both catalogs and their release lists and saves them, or changes nothing (the saved catalog
// stays) and says why: errors.Is catalog.ErrUnreachable for no network, a *catalog.FetchError with the HTTP status.
// Only ever called when the owner pressed u.
func (b *Backend) UpdateCatalog(ctx context.Context) (*catalog.Catalog, error) {
	return nil, errNotBuilt
}

// SavedFiles lists the downloaded files still on the SD card, newest first.
func (b *Backend) SavedFiles() ([]catalog.Entry, error) { return nil, errNotBuilt }

// DeleteFile removes a saved file and its record. Only after the owner said y (S20).
func (b *Backend) DeleteFile(name string) error { return errNotBuilt }

// Download fetches files one after another (catalog.Client.Download, with the GitHub fallback for MeshCore's own
// release files), one Step each with its bytes, and finishes with the Entries saved, or with why not. Only ever
// started when the owner pressed d, or y on a file not yet saved. It returns at once.
func (b *Backend) Download(ctx context.Context, files []catalog.File) *Job {
	return notBuilt(JobDownload)
}

// Prepare checks a Request without touching the board (Fits, catalog.Choose, the saved file's SHA-256, the image's
// chip or the DFU package and its SoftDevice), for the Confirm screen. It reads and hashes the saved file, so it is
// called from a tea.Cmd. It never downloads.
func (b *Backend) Prepare(req Request) Prepared {
	return Prepared{Problem: errNotBuilt.Error()}
}

// Flash runs one flash from start to end: every check of Prepare again on the bytes it then writes (Fits first; a
// file not saved, or changed since, is "Not flashed": Flash never downloads), finding the board by its USB serial
// number (or USB path), the ESP32 ROM bootloader or the nRF52 1200-baud touch and serial DFU, the MD5 check (ESP32)
// or the board coming back running the version just flashed (nRF52), and where the board is afterwards. A board
// whose bootloader session is stale (see Backend) gets a first step, "Press the board's reset button twice quickly",
// that waits up to FreshBootloaderWait for its bootloader port to go and come back, and never uses the port as it
// is. Every flash that made a 1200-baud touch ends, however it ends, by opening the tty now on the touched name (if
// any) at 115200 and closing it, so no later program's close reboots the board into DFU. Every failure finishes the
// Job with what the board is left with and how to recover it. Job.Cancel stops it the safe way (on an ESP32 the RTS
// pulse that takes the board out of its bootloader). It returns at once.
func (b *Backend) Flash(ctx context.Context, req Request) *Job {
	return notBuilt(JobFlash)
}

// ReadNode opens a node for setup: probes it (waiting FirstBootWait rather than 2 s when firstBoot, right after a
// flash) and reads its current settings, read-only. The Node's Problem says why it can't be set up when it can't.
func (b *Backend) ReadNode(ctx context.Context, p Port, firstBoot bool) Node {
	return Node{Port: p, Problem: Failed, Reason: errNotBuilt.Error()}
}

// Setup sends a setup end to end: nodecli.Plan and Run (one command at a time, each checked, then every value read
// back), the reboot, waiting for the board to come back (by serial number), and nodecli.Confirm. Steps are the
// plan's (their Text is the nodecli.Step's Display: a password shows as ••••). A failure finishes with the
// nodecli.StepError's State. The admin password is dropped as soon as it has been sent. It returns at once.
func (b *Backend) Setup(ctx context.Context, req SetupRequest) *Job {
	return notBuilt(JobSetup)
}

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
	return nodecli.MessengerRadio{}, ""
}

// notBuilt is a placeholder Job, already finished.
func notBuilt(kind JobKind) *Job {
	j := NewJob(kind, nil, nil)
	j.Finish(Outcome{Title: "Not built yet", Summary: []string{errNotBuilt.Error()}, Log: errNotBuilt.Error()})
	return j
}
