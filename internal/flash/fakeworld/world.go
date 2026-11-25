// Package fakeworld is a whole fake world for meshflash: the boards plugged into the Zero (each a composite of the
// Phase 1 fakes: esp.FakeESP, nrfdfu.FakeBootloader and FakeApp, nodecli.FakeNode, behind serialport.Fake ports),
// re-enumerating on USB as the real ones do; the flasher sites serving the committed catalog and firmware files it
// makes up; the battery and the clock. A flash.Backend built over World.Deps runs unchanged against it, so the
// screens' end-to-end tests (internal/ui) and the demo build (cmd/meshflash, -tags demo) drive the real
// orchestration with no board, no network and no real time: everything runs on one serialport.VirtualClock.
//
// Only tests and the demo build import it; the release binary never does (make test checks), so it carries no
// fakes. It is not a _test package because internal/ui's tests and cmd/meshflash's demo.go need it.
//
// The firmware it serves is made up, deterministic and at least catalog.MinFileSize: an ESP32 image header naming
// the board's chip, or a DFU .zip with a good init packet for the board's SoftDevice, each holding a marker
// ("MESHFLASH-FAKE <role> <version>") from which a fake board, once flashed, runs that role and version, so "came
// back running the new version" is tested for real and a wrong image gives a wrong version.
//
// Time: the world registers exactly one VirtualClock.OnTick in New, which runs every transition, scheduled event
// (At) and pending re-enumeration from the world's own state (OnTick has no way to remove a ticker, so one per port
// or session would only grow); Demo adds one more, for pacing. The fake ESP32 moves the clock on as a real one takes
// time: each FLASH_DEFL_DATA block by its bytes at about 11.5 KB/s (115200 baud), a chip erase by about 10 s, so a
// step's time and the demo's progress are those of a real board.
//
// Phase 2 contract (docs/phase2-design.md): builder B writes the bodies; the exported names, signatures and doc
// comments are fixed, and B may add exported names (a new board, a new event) but never change these.
//
// How it is built (builder B): boards.go holds the boards and their transitions, ports.go the ports a backend opens
// (each open a session, watched for the rules Problems lists), sites.go the flasher sites, firmware.go the files they
// make up, catalog.go the committed catalog, demo.go the demo build's world. Locks: World.mu, then a Board's mu; the
// fake devices take only their board's, and nothing holds a lock while the clock moves, so the ticker can always run.
package fakeworld

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/esp"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// World is the fake Zero's surroundings. Its methods are safe for concurrent use: the backend's goroutine and the
// test's call them at once.
type World struct {
	clock *serialport.VirtualClock
	wire  *serialport.Log

	mu       sync.Mutex
	boards   []*Board
	sessions []*session     // every port ever opened, in order
	speeds   map[string]int // each tty name's line speed, kept as Linux keeps it across closes
	held     map[string]bool
	events   []event
	problems []string
	seen     map[string]bool // problems already listed

	requests []string
	fails    []failure
	offline  bool
	serveAs  map[string]Firmware
	serveSD  map[string]uint16
	appSize  int // the made-up apps' size: 0 for appSize (tests), a real image's in the demo

	battery     int // percent; negative: no battery
	unreadable  bool
	clockSynced bool
	dialout     bool
	messenger   string
}

// event is an At: f runs once the clock reaches due.
type event struct {
	due time.Time
	f   func()
}

// New is a world with nothing plugged in, a battery at 87%, a synchronised clock, the owner in the dialout group,
// and the network up.
func New() *World {
	w := &World{wire: &serialport.Log{}, speeds: map[string]int{}, held: map[string]bool{}, seen: map[string]bool{},
		serveAs: map[string]Firmware{}, serveSD: map[string]uint16{}, battery: 87, clockSynced: true, dialout: true}
	w.clock = serialport.NewVirtualClock(w.wire)
	w.clock.OnTick(w.tick)
	return w
}

// Deps are a flash.Backend's dependencies over this world, with the catalog client's files under cacheDir and the
// flasher sites served in memory (no socket at all). With withCatalog the catalog is saved there first, as a u would
// have saved it; those requests are not in Requests. MessengerPath is "" (no messenger file) unless SetMessenger
// was called.
func (w *World) Deps(cacheDir, version string, withCatalog bool) (flash.Deps, error) {
	client := catalog.NewClient(cacheDir, version, catalog.Options{Sites: Sites, Clock: w.clock,
		HTTP: &http.Client{Transport: memTransport{w}}})
	return w.deps(client, cacheDir, version, withCatalog)
}

// DepsOver is Deps with the sites at srv, an httptest.Server running Handler on the loopback interface: for the
// tests of the HTTP path itself (a catalog update through httptest).
func (w *World) DepsOver(srv *httptest.Server, cacheDir, version string, withCatalog bool) (flash.Deps, error) {
	sites := catalog.Sites{MeshCore: srv.URL + "/mc", ZephCore: srv.URL + "/zc", GitHub: srv.URL + "/gh"}
	client := catalog.NewClient(cacheDir, version, catalog.Options{Sites: sites, Clock: w.clock,
		HTTP: &http.Client{Transport: offlineTransport{w, srv.Client().Transport}}})
	return w.deps(client, cacheDir, version, withCatalog)
}

func (w *World) deps(client *catalog.Client, cacheDir, version string, withCatalog bool) (flash.Deps, error) {
	if err := loadCommitted(); err != nil {
		return flash.Deps{}, err
	}
	if withCatalog {
		if err := w.saveCatalog(cacheDir, version); err != nil {
			return flash.Deps{}, err
		}
	}
	w.mu.Lock()
	messenger := w.messenger
	w.mu.Unlock()
	return flash.Deps{
		Open:          w.open,
		Scan:          w.Scan,
		Dialout:       w.dialoutStatus,
		Clock:         w.clock,
		Catalog:       client,
		Battery:       w.batteryLevel,
		ClockSynced:   w.synced,
		MessengerPath: messenger,
	}, nil
}

// saved is the catalog folder an update saves, kept from the first world that saved one: every world's update gives
// the same files (the sites serve the committed catalog), and resolving it costs about a second under -race, so
// later worlds get a copy of those files rather than another update.
var saved struct {
	mu    sync.Mutex
	files map[string][]byte // name in <cache>/catalog → contents
}

// saveCatalog saves the committed catalog under cacheDir, as an update would (through the in-memory sites, quietly),
// or copies the folder the first such update saved.
func (w *World) saveCatalog(cacheDir, version string) error {
	saved.mu.Lock()
	defer saved.mu.Unlock()
	dir := filepath.Join(cacheDir, "catalog")
	if saved.files != nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		for name, b := range saved.files {
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
				return err
			}
		}
		return nil
	}
	c := catalog.NewClient(cacheDir, version, catalog.Options{Sites: Sites, Clock: w.clock, HTTP: w.quietClient()})
	if _, err := c.Update(context.Background()); err != nil {
		return fmt.Errorf("fakeworld: saving the catalog: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		files[e.Name()] = b
	}
	saved.files = files
	return nil
}

// Clock is the world's virtual clock, shared by every fake port, the backend and the catalog client. Tests never
// give it to the screens: the screens' clock is the test driver's own.
func (w *World) Clock() *serialport.VirtualClock { return w.clock }

// Wire records every byte to and from every board. It holds the admin password (setup sends it): assert on it,
// never write it anywhere.
func (w *World) Wire() *serialport.Log { return w.wire }

// Handler serves the flasher sites: /mc (flasher.meshcore.io: config.json and /releases?repo=github and
// github-ripple, and the firmware), /zc (zephcore.meshcore.dev) and /gh (GitHub's release downloads), as
// internal/catalog's own test server lays them out.
func (w *World) Handler() http.Handler { return http.HandlerFunc(w.serveHTTP) }

// Requests is every path (with its query) the sites were asked for, in order: a test that nothing was fetched
// unless the owner asked.
func (w *World) Requests() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.requests...)
}

// SetOffline makes every request fail as no network does (catalog.ErrUnreachable), or not.
func (w *World) SetOffline(off bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.offline = off
}

// FailPath makes the sites answer status for every request whose path starts with prefix (a 503 for config.json, an
// HTML page for a file: status 200 with ctype "text/html").
func (w *World) FailPath(prefix string, status int, ctype string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.fails[:0]
	for _, f := range w.fails {
		if f.prefix != prefix {
			kept = append(kept, f)
		}
	}
	w.fails = kept
	if status != 0 {
		w.fails = append(w.fails, failure{prefix: prefix, status: status, ctype: ctype})
	}
}

// SetBattery sets the Zero's charge; a negative percent is no battery (a PC).
func (w *World) SetBattery(percent int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.battery, w.unreadable = percent, false
}

// SetBatteryUnreadable makes the battery there but unreadable (the gauge didn't answer): Deps.Battery gives (-1,
// true) until the next SetBattery.
func (w *World) SetBatteryUnreadable() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.unreadable = true
}

func (w *World) batteryLevel() (int, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case w.unreadable:
		return -1, true
	case w.battery < 0:
		return 0, false
	}
	return w.battery, true
}

// At runs f once, on the world's ticker, when the virtual clock has moved d past now: how a test acts "during" a
// wait (a double press of reset while a retry waits for a fresh bootloader) without racing the backend's goroutine.
// f may call the world's and the boards' methods.
func (w *World) At(d time.Duration, f func()) {
	due := w.clock.Now().Add(d)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event{due: due, f: f})
	sort.SliceStable(w.events, func(i, j int) bool { return w.events[i].due.Before(w.events[j].due) })
}

// SetClockSynced says whether the Zero's clock is synchronised.
func (w *World) SetClockSynced(ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clockSynced = ok
}

func (w *World) synced() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clockSynced
}

// SetDialout puts the owner in the dialout group or not: without it every open fails with serialport.ErrPermission.
func (w *World) SetDialout(ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dialout = ok
}

func (w *World) dialoutStatus() (bool, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dialout {
		return true, ""
	}
	return false, usbid.DialoutHint
}

// SetMessenger writes a messenger settings file (SPEC.md 8.4) into dir and makes Deps point at it.
func (w *World) SetMessenger(dir string, r nodecli.MessengerRadio) error {
	// Deps made before the call keep the path they were given (Deps.MessengerPath is a string), so call it first.
	radio := map[string]any{"freq_mhz": r.Radio.FreqMHz, "bw_khz": r.Radio.BWkHz, "sf": r.Radio.SF, "cr": r.Radio.CR}
	if r.HasTx {
		radio["tx_dbm"] = r.TxDBm
	}
	b, err := json.MarshalIndent(map[string]any{"radio": radio}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messenger = path
	return nil
}

// Plug plugs a board in: it shows on USB at once, in the mode it is in, under the lowest free tty name of its kind.
func (w *World) Plug(b *Board) {
	w.mu.Lock()
	defer w.mu.Unlock()
	known := false
	for _, o := range w.boards {
		known = known || o == b
	}
	if !known {
		w.boards = append(w.boards, b)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.plugged {
		return
	}
	b.w, b.plugged = w, true
	b.powerUp()
	w.arriveLocked(b)
}

// Unplug pulls a board out: its port goes away at once and every call on an open one fails with
// serialport.ErrGone.
func (w *World) Unplug(b *Board) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.plugged {
		return
	}
	b.plugged, b.pending = false, false
	b.leaveLocked()
}

// HoldTty makes something else hold a tty's minor (another program with it open, say) or let it go: while it is
// held, a board coming onto USB gets the next free name, as cdc-acm gives it, so a bootloader can come up on a new
// tty name after the touch.
func (w *World) HoldTty(name string, held bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if held {
		w.held[name] = true
	} else {
		delete(w.held, name)
	}
}

// Speed is a tty name's line speed as Linux keeps it (the last speed it was opened or set at, kept across closes),
// 0 for a name never opened: the touch leaves 1200, and the backend must leave 115200.
func (w *World) Speed(tty string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.speeds[tty]
}

// Opens is every port the backend opened so far, in order, as "ttyACM0 at 115200" ("… exclusive" when it was),
// for a test that something was never opened at all.
func (w *World) Opens() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, s := range w.sessions {
		o := fmt.Sprintf("%s at %d", s.tty, s.baud)
		if s.exclusive {
			o += " exclusive"
		}
		out = append(out, o)
	}
	return out
}

// Problems is every rule a backend broke so far, in plain words, for a test to fail on:
//
//   - a port left open after a job, a probe or a read finished;
//   - a port opened without a baud, or not exclusively;
//   - a probe (a session that isn't a flash or a setup) opening a bootloader port, or sending anything but ver,
//     get <name> and board, or asking get guest.password (owner question 1: a probe reads the name, never the
//     guest password);
//   - a 1200-baud touch on a port that isn't an nRF52 app; SLIP sent to an nRF52; a DFU frame sent to an app;
//   - START sent to anything but the bootloader, or into a bootloader session that already had START;
//   - FLASH_BEGIN or START sent with no flash job running;
//   - a tty left at 1200 baud when a job finished (the world keeps each tty name's speed as Linux does: a close
//     leaves it, and the next open's close at 1200 reboots an nRF52 app into DFU);
//   - a port closed at 1200 baud other than by the touch;
//   - the admin or guest password in any command other than the password commands.
//
// Each problem names the rule, the board and the command's place ("command 7 of the setup"), never the command's
// bytes or a password: tests print these lines.
func (w *World) Problems() []string {
	// The world tells a probe from a flash or a setup by what the backend says it opened a port for (ports.go:
	// OpenedFor); a session it isn't told about is held to a probe's rules. A port still open and a tty at 1200 baud
	// are judged now, so call it once every job has finished; a tty at 1200 counts only while a board is on it
	// (nothing can open it again until one is).
	w.update()
	w.mu.Lock()
	out := append([]string(nil), w.problems...)
	open := map[string]bool{}
	for _, s := range w.sessions {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if !closed {
			out = append(out, fmt.Sprintf("%s: port %d (%s) is still open: every port must be closed before a job, "+
				"a probe or a read finishes", s.b.name, s.n, s.tty))
			open[s.tty] = true
		}
	}
	for _, b := range w.boards {
		b.mu.Lock()
		tty, name := b.tty, b.name
		b.mu.Unlock()
		if tty != "" && w.speeds[tty] == nrfdfu.TouchBaud && !open[tty] {
			out = append(out, fmt.Sprintf("%s: %s was left at 1200 baud: the next program to open and close it "+
				"reboots the board into its bootloader", name, tty))
		}
	}
	boards := append([]*Board(nil), w.boards...)
	w.mu.Unlock()
	for _, b := range boards {
		out = append(out, b.passwordProblems()...)
	}
	return out
}

// problem records a rule broken, once.
func (w *World) problem(format string, args ...any) {
	p := fmt.Sprintf(format, args...)
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.seen[p] {
		w.seen[p] = true
		w.problems = append(w.problems, p)
	}
}

// update brings the world the board is in up to date, so what a test reads of it is what it would see now.
func (b *Board) update() {
	b.mu.Lock()
	w := b.w
	b.mu.Unlock()
	if w != nil {
		w.update()
	}
}

// Board is one fake board. Make it with one of the constructors, change it before (or while) it is plugged in.
type Board struct {
	boardSpec

	mu sync.Mutex
	w  *World

	plugged bool
	mode    string    // modeApp, modeBootloader, modeROM, modeBTDFU
	tty     string    // its tty now, "" while it isn't on USB
	pending bool      // between modes: it comes on USB in arrive's mode at arriveAt
	arrive  string    // the mode it comes back in
	double  bool      // the bootloader it comes back in was started by a double press (RAK: 239a:0029)
	at      time.Time // when it comes back
	open    *session  // the port open on it now, if any
	fw      Firmware  // what it runs
	node    *nodecli.FakeNode
	prefs   *nodecli.FakeNode // the last console's stored settings, kept while no console runs
	powered bool              // it has been plugged in before: a replug is a power cycle
	esp     *esp.FakeESP
	app     *nrfdfu.FakeApp
	boot    *nrfdfu.FakeBootloader
	boots   []*nrfdfu.FakeBootloader
	opens   int // ports opened on it so far, for naming them in problems

	scenario    string // the next bootloader session's
	unplugFrame int    // the session's unplug-at frame, -1 for none
	ignoreTouch bool
	busy        bool
	reboots     int // the node's reboots already seen
	espBlocks   int // FLASH_DEFL_DATA blocks already seen
	espErased   bool
	unplugAt    int // UnplugWhileWriting: the block, 0 for none
	blocks      int // blocks of a flash seen since UnplugWhileWriting
	hooks       []cmdHook
	setupCmds   int // console commands of setups handled since the first hook
	commands    []command
	unplugReq   bool // a device pulled it out (the next update unplugs it)
	tinyReset   bool // a TinyUSB app was reset into its ROM
}

// Firmware is what a fake board runs.
type Firmware struct {
	// Role is nodecli.RoleRepeater or nodecli.RoleRoomServer for a MeshCore console; "companion" for firmware
	// with no console (companion, KISS, ZephCore, Ripple: silent to a probe); "" for none (erased, or a bad image).
	Role string
	// Version is the MeshCore release ("v1.17.1"), for a console; "1.18.2-zephcore" makes a ZephCore build whose
	// console answers ver, get role and board as MeshCore's does (UNVERIFIED: recorded on hardware in Phase 3), so
	// the backend's ZephCore rule is tested.
	Version string
	// Name is the node's name ("" keeps the fake node's default).
	Name string
	// Stuck leaves the console in region-load mode: it echoes, never answers (nodecli.ErrStuck).
	Stuck bool
}

// HeltecV4 is the owner's Heltec v4: an ESP32-S3 on its own USB-Serial-JTAG (303a:1001, "USB JTAG/serial debug
// unit", serial 60:55:F9:C0:27:18, socket 1-1.3), whose identity is the same in the app and the ROM.
func HeltecV4(fw Firmware) *Board { return newBoard(heltecV4Spec, fw) }

// HeltecV3 is a Heltec v3 behind a CP2102 bridge (10c4:ea60, serial "0001": not a useful one; socket 1-1.2), reset
// into its ROM through the bridge's auto-reset circuit.
func HeltecV3(fw Firmware) *Board { return newBoard(heltecV3Spec, fw) }

// RAK4631 is the owner's RAK WisBlock 4631: an nRF52840, app 239a:8029 "TinyUSB Serial", bootloader 239a:002a
// "nRF Serial", serial D2A1F3C4B5E69788, socket 1-1.3, SoftDevice S140 v6. The 1200-baud touch drops it into the
// bootloader, which shows on USB about a second later; after STOP with a good image it leaves for the app.
func RAK4631(fw Firmware) *Board { return newBoard(rak4631Spec, fw) }

// XiaoNRF52 is one of the owner's Seeed Xiao nRF52840 boards (2886:8044 "XIAO nRF52840", bootloader 2886:0045,
// serial 8A4C2E1F0B3D5A69), whose USB identity matches three catalog boards.
func XiaoNRF52(fw Firmware) *Board { return newBoard(xiaoSpec, fw) }

// HeltecTrackerV2 is a Heltec Wireless Tracker v2: an ESP32-S3 whose app is on arduino-esp32's TinyUSB serial
// (303a:0002, "heltec_tracker v2", serial 6055F9C02719, socket 1-1.4). Reset into its ROM it comes back as a new
// USB device, the chip's USB-Serial-JTAG (303a:1001, serial 60:55:F9:C0:27:19), and after the flash's hard reset
// its app's port returns (docs/meshcore-flashing.md 6.2: source inference).
func HeltecTrackerV2(fw Firmware) *Board { return newBoard(trackerV2Spec, fw) }

// Tty is the board's tty name now ("ttyACM0"), or "" while it isn't on USB (unplugged, or between modes).
func (b *Board) Tty() string {
	b.update()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tty
}

// Running is what the board runs now: what was last written to it, once it has started it.
func (b *Board) Running() Firmware {
	b.update()
	b.mu.Lock()
	defer b.mu.Unlock()
	fw := b.fw
	if b.node != nil {
		fw.Name = b.node.Name
	}
	return fw
}

// Node is the console the board runs now (nil when it runs none), to check what setup sent and stored. Its
// Commands hold the password: never print them.
func (b *Board) Node() *nodecli.FakeNode {
	b.update()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.node
}

// ESP is an ESP32 board's chip (nil on an nRF52), to check what was written and erased.
func (b *Board) ESP() *esp.FakeESP {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.esp
}

// Sessions are an nRF52's bootloader sessions so far, oldest first.
func (b *Board) Sessions() []nrfdfu.FakeResult {
	b.mu.Lock()
	boots := append([]*nrfdfu.FakeBootloader(nil), b.boots...)
	b.mu.Unlock()
	out := make([]nrfdfu.FakeResult, len(boots))
	for i, s := range boots {
		out[i] = s.Result()
	}
	return out
}

// NextBootloader makes the nRF52's next bootloader session act out an nrfdfu.NewFakeBootloader scenario
// ("drop-ack:5", "unplug-at:12", "bad-crc", …); "" is a good bootloader. A SoftDevice other than the board's is
// refused as the real bootloader refuses it.
func (b *Board) NextBootloader(scenario string) {
	if _, err := nrfdfu.NewFakeBootloader(scenario); err != nil {
		panic(err) // a test's typo
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scenario = scenario
}

// UnplugWhileWriting unplugs the board as the n-th block of a flash arrives (an ESP32 FLASH_DEFL_DATA, an nRF52 DATA
// frame), counting from 1.
func (b *Board) UnplugWhileWriting(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.unplugAt, b.blocks = n, 0
}

// UnplugAfterCommands unplugs the board once its console has handled n commands of a setup.
func (b *Board) UnplugAfterCommands(n int) {
	b.addHook(n, true, nil)
}

// AfterCommands runs f once the board's console has handled n commands of a setup (counted as UnplugAfterCommands
// counts them), on the goroutine sending them and before the n-th command's reply reaches it: how a test stops a
// setup at a chosen step (f calls the Job's Cancel) without racing it. f may call the world's and the boards'
// methods.
func (b *Board) AfterCommands(n int, f func()) {
	b.addHook(n, false, f)
}

// IgnoreTouch makes the nRF52's app ignore the 1200-baud touch (as one that something else holds DTR high for):
// the touch then does nothing, and the board stays in its app.
func (b *Board) IgnoreTouch(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ignoreTouch = on
}

// SetBusy makes another program hold the board's port, or let it go: while it is held every open fails with
// serialport.ErrBusy.
func (b *Board) SetBusy(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.busy = on
}

// PressReset presses the reset button once: the app restarts (and an nRF52 re-enumerates).
func (b *Board) PressReset() { b.reset(false) }

// DoublePressReset presses reset twice quickly: an nRF52 starts its bootloader, fresh, whatever it was doing.
func (b *Board) DoublePressReset() { b.reset(true) }

// Demo is the demo build's world (cmd/meshflash with -tags demo): a Heltec v4 running repeater v1.17.1 named
// “Hilltop 2” and a RAK 4631 running companion firmware, the committed catalog already saved under cacheDir,
// the battery at 87%, and time paced so a flash takes about half a minute on the screen. log is the demo's
// screen.log.
func Demo(cacheDir, version string, log io.Writer) (flash.Deps, error) {
	return demo(cacheDir, version, log)
}

// The sites a world's in-memory Deps use; Handler serves the same paths under any host.
var Sites = catalog.Sites{
	MeshCore: "http://meshcore.fake.invalid/mc",
	ZephCore: "http://zephcore.fake.invalid/zc",
	GitHub:   "http://github.fake.invalid/gh",
}

// errNoPort is what opening a tty no board is on gives.
var errNoPort = errors.New("fakeworld: no such port")
