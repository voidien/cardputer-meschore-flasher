// Package flash is meshflash's orchestration: everything the screens ask of a board or of the network, done with the
// Phase 1 packages. It lists the plugged-in ports and what their USB identity suggests (usbid), probes a running node
// (nodecli), loads and updates the catalog and downloads its files (catalog), runs one flash from start to end as
// steps (esp, nrfdfu), and sets up a repeater or room server end to end (nodecli). Each failure ends with what the
// board is left with and how to recover it, in plain words (docs/meshcore-flashing.md 10).
//
// It is the only Phase 2 package that does serial I/O, and only through the serialport.Opener and the port scanner
// it is given (Deps), so tests and the demo build run it unchanged against a fake world (internal/flash/fakeworld).
// internal/ui never does serial or network I/O itself: it calls a *Backend through the ui.Backend interface from
// tea.Cmds, and watches long work through a *Job.
//
// Rules it keeps (SPEC.md 6, CLAUDE.md): the board's chip family is matched to the board picked (Fits) and the file
// to the chip before the board is touched; the saved file's SHA-256 is checked before every flash and exactly the
// bytes checked are written; a flash never downloads; never the catalog's erase or bootloader files; a board is
// followed across resets by its USB serial number or USB path, never by tty name alone; one board operation at a
// time; every open sets its baud and is exclusive, and a tty touched at 1200 baud is left at 115200; DFU goes only
// to a port positively identified as the nRF52 bootloader (Port.InBootloader), and START never into a bootloader
// session that already had one; a password never reaches a Step, an Outcome, a log line or an error text.
//
// types.go, job.go and rules.go are part of the Phase 2 contract (docs/phase2-design.md) and are fixed; backend.go's
// signatures are fixed and its bodies are builder B's.
package flash

import (
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/nodecli"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// Deps are what a Backend works with. On the device internal/app fills them with the real things; tests and the demo
// build fill them from a fakeworld.World. Open, Scan, Dialout, Clock and Catalog must be set; a nil Battery means no
// battery, a nil ClockSynced a clock never synchronised, a nil Logf no log.
type Deps struct {
	// Open opens a serial port: serialport.Open on the device. Every open passes its baud and Exclusive.
	Open serialport.Opener
	// Scan lists the USB serial ports from sysfs, never opening one: usbid.Scan("") on the device.
	Scan func() ([]usbid.Port, error)
	// Dialout says whether this process is in the dialout group, and the command that adds it:
	// usbid.DialoutStatus on the device. It is a hint; an open failing with serialport.ErrPermission is the proof.
	Dialout func() (ok bool, hint string)
	// Clock is time as the protocols see it: serialport.RealClock on the device, the world's VirtualClock in tests.
	Clock serialport.Clock
	// Catalog is the catalog and the saved files, under ~/.cache/meshflash on the device.
	Catalog *catalog.Client
	// Battery is the Zero's charge in percent, and whether the Zero has a battery at all (none on a PC). A battery
	// that is there but can't be read right now is (-1, true): the screens then ask before flashing, as under 30%.
	Battery func() (percent int, ok bool)
	// ClockSynced says the Zero's clock is synchronised (NTP), so setup may set the node's clock (SPEC.md 7).
	ClockSynced func() bool
	// MessengerPath is the messenger's settings file (nodecli.MessengerPath()), read only; "" reads none.
	MessengerPath string
	// Logf, if set, takes one line for screen.log: which step went wrong and why, in the words of nodecli's
	// StepError.Reason or Progress.Note, or esp's and nrfdfu's error text. Never a raw reply from the node (the
	// password step's reply echoes the password, a read-back of guest.password the guest's), a Step.Command, a
	// serialport.Log, or a Settings or SetupRequest printed whole.
	Logf func(format string, args ...any)
}

// Port is one plugged-in USB serial port, as the Board screen shows it.
type Port struct {
	// Port is its identity from sysfs: tty name and path, VID:PID, strings, serial number, USB path.
	usbid.Port
	// Kind is usbid.Classify's: an nRF52 app or bootloader, an ESP32's USB-Serial-JTAG, TinyUSB or bridge.
	Kind usbid.Kind
	// Label is what the Board screen names the port by after its tty: the bridge chip ("CP210x") for a bridge,
	// else the USB product string ("XIAO nRF52840"), else "". PortOf sets it.
	Label string
	// Candidates are the catalog boards its USB identity could be (usbid.Candidates), in catalog order; the owner
	// always picks.
	Candidates []usbid.Candidate
	// Key is its USB serial number in one form (usbid.NormalSerial), or "" when the serial can't tell boards apart
	// (none, all zeros, or a CP210x's factory "0001"). Boards are remembered by Key only, never for an empty Key;
	// drafts are kept by DraftKey.
	Key string
}

// Scan is what Backend.Scan found.
type Scan struct {
	// Ports in usbid.Scan's order (ttyACM before ttyUSB, by number).
	Ports []Port
	// Dialout says this process is in the dialout group (Deps.Dialout); when it isn't, Hint is the command to run.
	Dialout bool
	Hint    string
}

// Problem is why a node didn't answer as a repeater or room server, or couldn't be asked.
type Problem int

// The problems, as Probe and ReadNode report them.
const (
	// NoProblem: a MeshCore repeater or room server answered; Node.Info is set.
	NoProblem Problem = iota
	// Silent: nothing answered as a MeshCore console within 2 s: companion, KISS, ZephCore or Ripple firmware, an
	// ESP32 in its ROM, or nothing running (nodecli.ErrNotCLI). Not an error: the Board screen shows the identity.
	Silent
	// Stuck: it echoes what it's sent but never answers (left in region-load mode, nodecli.ErrStuck). The screen says:
	// press its reset button (or unplug it if it has no battery) and press r.
	Stuck
	// NotSettable: a console answered but setup can't configure it (another role, firmware before v1.11.0, or a
	// ZephCore build, whose `ver` names it: setup knows only MeshCore's own firmware); Reason says why, worded to
	// follow "Running repeater v1.10.0: " ("flash v1.11.0 or newer to set it up here.", "meshflash sets up only
	// repeater and room-server firmware.", or for ZephCore "meshflash sets up only MeshCore's own repeater and
	// room-server firmware.").
	NotSettable
	// InBootloader: an nRF52 showing its bootloader. It is never opened by a probe. Reason is "" when the port is
	// positively the bootloader (Port.InBootloader) in a fresh session, ready to flash; otherwise it says what to do
	// first: "Its last flash stopped part-way: meshflash asks you to press reset twice quickly when you flash it."
	// for a session that already had START (docs/phase2-design.md 4.4); "It was already in its bootloader when
	// meshflash started: meshflash asks you to press reset twice quickly when you flash it." for one listed at the
	// first scan, which may hold a session a stopped flash left (owner question 18); or "Looks like its bootloader,
	// but meshflash can't confirm it: press reset twice quickly." The screens show Reason as it is.
	InBootloader
	// NoPermission: the port can't be opened (serialport.ErrPermission): not in the dialout group yet (SPEC.md 9.3).
	NoPermission
	// Busy: another program has the port open (serialport.ErrBusy).
	Busy
	// Gone: the port went away while it was asked (serialport.ErrGone).
	Gone
	// Failed: anything else; Reason says what.
	Failed
)

// Node is what a probe or a read found on a port.
type Node struct {
	// Port is the port it answered on, as it shows now.
	Port Port
	// Problem is NoProblem when a repeater or room server answered; Reason is a plain-words explanation otherwise
	// ("" for Silent, and for InBootloader when it is ready to flash: see InBootloader).
	Problem Problem
	Reason  string
	// Info is what the node said about itself (ver, get role, board), when Problem is NoProblem or NotSettable.
	Info nodecli.Info
	// Current is the node's current settings (nodecli.ReadSettings: name, radio, TX power, path hash, location,
	// local advert, and a room server's guest password; never the admin password), when they were read. ReadNode
	// always reads them, and keeps the guest password as the form's default. Probe reads them too once a repeater or
	// room server has answered, so the Board screen can show the node's name (owner question 1, answered 25 Nov
	// 2026), but never the guest password: it reads them as a repeater's (the Info's Role set to
	// nodecli.RoleRepeater for the read), so a room server is never even asked for it. HasCurrent says they were
	// read.
	Current    nodecli.Settings
	HasCurrent bool
}

// Request is one flash, as the owner confirmed it. Device, Firmware and Version are from the catalog the screens
// show (Version must be one of Device's own versions: catalog.Choose checks); Kind is what the owner picked, never a
// default (the zero Kind is refused). The backend picks the file, address and erase itself with catalog.Choose; a
// caller never builds a catalog.Flash.
type Request struct {
	Port     Port
	Device   catalog.Device
	Firmware catalog.Firmware
	Version  catalog.Version
	Kind     catalog.Kind
}

// Prepared is what a Request would write, checked without touching the board: catalog.Choose's pick, the saved
// file's SHA-256 checked now, the image header's chip (ESP32) or the DFU package and its SoftDevice (nRF52).
type Prepared struct {
	// Flash is Choose's pick: the kind, the file, the address and the erase. Zero when Choose refused.
	Flash catalog.Flash
	// Saved says the file is on the SD card and its SHA-256 still matches what was recorded at download; Entry is
	// its record then.
	Saved bool
	Entry catalog.Entry
	// Problem says, in plain words, why this can't be flashed: a Choose refusal ("choose update or full wipe
	// first" when Kind is zero), a saved file that has changed, an image for another chip, a package for another
	// SoftDevice, a chip meshflash can't flash. "" when it can (once saved).
	Problem string
	// Notes are things worth knowing before y, in plain words: the SoftDevice the catalog doesn't name, say.
	Notes []string
	// Ask is a question the screens ask with a second y before flashing, "" for none: for an nRF52 board whose
	// SoftDevice the catalog doesn't name (T-Echo, Wio Tracker L1 EINK, ThinkNode M1, Nano G2 Ultra), "The catalog
	// doesn't say which SoftDevice this board has: if the file needs another, the bootloader refuses it and its app
	// is erased. Flash anyway?" (owner question 20: Confirm says so and asks a second y, as for a low battery).
	Ask string
}

// SetupRequest is one setup, as the owner confirmed it on the form.
type SetupRequest struct {
	// Node is what ReadNode gave when the form opened (its Port and Info pick the commands sent).
	Node Node
	// Settings are the form's values, with the admin password (a Secret: it prints as ••••). Device is the catalog
	// board when known, for the TX power limit. ClockSynced is ignored: the backend asks Deps.ClockSynced itself.
	// The backend drops the password as soon as the node has it.
	Settings nodecli.Settings
}

// JobKind is what a Job does.
type JobKind int

// The jobs.
const (
	JobDownload JobKind = iota + 1 // downloading catalog files (no board involved)
	JobFlash                       // one flash, from finding the board to checking it afterwards
	JobSetup                       // one setup, from the first command to checking the node after its reboot
)

// StepState is where a Step is.
type StepState int

// The step states, as the screens mark them.
const (
	StepNow     StepState = iota // under way: ▸
	StepDone                     // done: ✓
	StepSkipped                  // not needed, or not possible (Note says why)
	StepFailed                   // where it stopped (Note says why)
)

// Step is one step of a Job, as the Flashing and Setup screens list it (internal/ui's stepLines: ✓ done, ▸ under
// way or where it stopped, · skipped).
type Step struct {
	// Text says what the step is, in plain words, as the screen shows it: "Into the bootloader (1200-baud touch)",
	// "Erasing", "Writing", "Set name to “Hilltop 2”", "Set admin password ••••". The worker may change it when the
	// step ends ("Erasing" → "Erased"). It never holds a password.
	Text  string
	State StepState
	// Done and Total are bytes, for the steps that move them (downloading, writing); 0 and 0 otherwise. Total is -1
	// when it isn't known yet (a download with no Content-Length).
	Done, Total int64
	// Started is when the step began and Took how long it took once it ended, both on the backend's clock (Deps.Clock).
	Started time.Time
	Took    time.Duration
	// Expect is how long the step is expected to take when nothing shows progress (an nRF52 erase), or 0.
	Expect time.Duration
	// Note is a detail: "attempt 2 of 4", "No answer: sending again (2 of 3)", "Stopping once the erase ends", why
	// it was skipped or failed. Never a password.
	Note string
	// From is the URL a download step's file is being fetched from: the catalog's, then its GitHub fallback once the
	// flasher site didn't give it (catalog.Client.DownloadFrom); "" for every other step. The Download screen says
	// where the file comes from by it, never by a Note's words.
	From string
}

// Risk is what stopping a Job now would leave the board with, so the screens' "Stop now?" says what is true.
type Risk int

// The risks, as a flash or setup moves through them.
const (
	// RiskNone: nothing on the board has changed yet (the touch, SYNC, the stub, the first commands' reads): it keeps
	// its old firmware and settings.
	RiskNone Risk = iota
	// RiskErased: a flash is erasing or writing (an ESP32 from its first erase, an nRF52 from START): stopping leaves
	// no working firmware until it is flashed again.
	RiskErased
	// RiskWritten: a flash is written and being checked or restarted: stopping only skips the check.
	RiskWritten
	// RiskSettings: a setup has sent settings the node saved, but not its radio.
	RiskSettings
	// RiskRadioSent: a setup's "set radio" is saved on the node: it switches to the new radio when it restarts.
	RiskRadioSent
	// RiskBootloader: a flash has sent the board into its bootloader (the 1200-baud touch, an ESP32 reset into its
	// ROM), or found it there, but written nothing: stopping leaves its old firmware, in the bootloader until its
	// reset button is pressed.
	RiskBootloader
	// RiskRestarted: a setup's reboot is sent: the node restarted with its new settings, and stopping skips only the
	// check after.
	RiskRestarted
)

// Progress is a whole picture of a Job at one moment. It is also the tea.Msg the screens receive for it.
type Progress struct {
	// Job is the job's ID (Job.ID); a screen ignores a Progress of a job it no longer watches.
	Job  uint64
	Kind JobKind
	// Seq grows by one with every change; Job.Next waits for a Seq above the one it was given.
	Seq uint64
	// Now is the backend's clock when the picture was taken: the running step has taken Now−Started so far.
	Now time.Time
	// Steps so far, in order: every step that has started, the last one the one under way until Done.
	Steps []Step
	// Planned is how many steps the Job will have in all when it knows (a setup: its plan's commands, the read-back,
	// the reboot and the check), else 0. The screen lock's page shows "Setting up · step 4 of 20" from it.
	Planned int
	// Risk is what stopping now would leave the board with (flash and setup Jobs; RiskNone for a download).
	Risk Risk
	// Done says the job has ended; Outcome is then set and the picture never changes again.
	Done    bool
	Outcome Outcome
}

// Outcome is how a Job ended, in words the Result and Setup screens show as they are.
type Outcome struct {
	// OK says it worked; Stopped that the owner (or a quit) stopped it.
	OK, Stopped bool
	// Title is the screen's title: "Flashed", "Flashing stopped", "Not flashed", "Set up", "Setup stopped",
	// "Downloaded", "Download failed".
	Title string
	// Summary is what happened, a paragraph each: what was written or sent and how it was checked ("Checked: the MD5
	// of the flash matches the file."), or what went wrong ("The board stopped answering while writing (212 of 361
	// KB).").
	Summary []string
	// State is what the board is left with, on a failure, in one short sentence: "Its old firmware is erased.",
	// "Nothing was sent to the board: it still has its old firmware.", the setup's nodecli.StepError.State. "" when
	// OK. Recovery never begins by repeating it: where esp's or nrfdfu's Recovery text starts with the state ("Flash
	// untouched.", "Partially written: …"), that sentence is State and the rest is Recovery.
	State string
	// Recovery is how to get the board working again, on a failure (esp and nrfdfu Recovery, docs/meshcore-flashing.md
	// 10, or the backend's own where those would mislead: an nRF52 retry says "Press r; meshflash then asks you to
	// press reset twice quickly." rather than "press reset twice, then flash again"); "" when OK or when nothing needs
	// doing. The Result screen adds "Press r to try again: the file is saved, so no Wi-Fi is needed." when Retry is
	// set, so Recovery doesn't say it.
	Recovery string
	// Retry says r makes sense: the file is saved and the board can be flashed again (after the Recovery's steps).
	Retry bool
	// Setup says s makes sense: the Request's firmware is MeshCore's own (catalog.SourceMeshCore) repeater or room
	// server, and the board answered as one after the flash. Never for ZephCore, Ripple or a companion.
	Setup bool
	// Board is the board as it shows now (its tty may have changed across the flash or the reboot); nil when it's
	// gone. The status line's mark follows it.
	Board *Port
	// Entries are the files a download saved (JobDownload).
	Entries []catalog.Entry
	// Sent is the radio part a setup sent, once the node took its "set radio" step (or may have: it reached the node
	// and no answer came), for config.json (JobSetup); nil when it didn't get that far.
	Sent *Sent
	// Log is one line for screen.log about how it ended ("" for nothing), by the rules of Deps.Logf: which step,
	// why, in nodecli's or esp's and nrfdfu's words; never a raw reply, a command or a password. The backend is its
	// one writer: it sets Log only for a Job that didn't end OK (never for a download the owner stopped) and gives the
	// same line to Deps.Logf just before Finish, whether or not a screen is watching (a crash, a quit). The screens
	// and internal/app never write it again (one line per failure, never twice).
	Log string
}

// Sent is the radio part of a setup the node took.
type Sent struct {
	Radio nodecli.Radio
	// TxDBm is the TX power sent once its `set tx` was answered; until then the node's own, as read when the form
	// opened (a setup that ends between `set radio` and `set tx` never records a TX power the node didn't get).
	TxDBm         int
	PathHashBytes int // 0 when none was sent (before v1.14.0, or left as it was)
}
