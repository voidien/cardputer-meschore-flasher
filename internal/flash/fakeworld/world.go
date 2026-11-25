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
// comments are fixed, and B may add exported names (a new board, a new event) but never change these. The
// placeholders below plug nothing in and serve nothing.
package fakeworld

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/esp"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/serialport"
)

var errNotBuilt = errors.New("fakeworld: not built yet (Phase 2 contract placeholder)")

// World is the fake Zero's surroundings. Its methods are safe for concurrent use: the backend's goroutine and the
// test's call them at once.
type World struct {
	clock *serialport.VirtualClock
	wire  *serialport.Log
}

// New is a world with nothing plugged in, a battery at 87%, a synchronised clock, the owner in the dialout group,
// and the network up.
func New() *World {
	w := &World{wire: &serialport.Log{}}
	w.clock = serialport.NewVirtualClock(w.wire)
	return w
}

// Deps are a flash.Backend's dependencies over this world, with the catalog client's files under cacheDir and the
// flasher sites served in memory (no socket at all). With withCatalog the catalog is saved there first, as a u would
// have saved it; those requests are not in Requests. MessengerPath is "" (no messenger file) unless SetMessenger
// was called.
func (w *World) Deps(cacheDir, version string, withCatalog bool) (flash.Deps, error) {
	return flash.Deps{}, errNotBuilt
}

// DepsOver is Deps with the sites at srv, an httptest.Server running Handler on the loopback interface: for the
// tests of the HTTP path itself (a catalog update through httptest).
func (w *World) DepsOver(srv *httptest.Server, cacheDir, version string, withCatalog bool) (flash.Deps, error) {
	return flash.Deps{}, errNotBuilt
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
func (w *World) Handler() http.Handler { return http.NotFoundHandler() }

// Requests is every path (with its query) the sites were asked for, in order: a test that nothing was fetched
// unless the owner asked.
func (w *World) Requests() []string { return nil }

// SetOffline makes every request fail as no network does (catalog.ErrUnreachable), or not.
func (w *World) SetOffline(off bool) {}

// FailPath makes the sites answer status for every request whose path starts with prefix (a 503 for config.json, an
// HTML page for a file: status 200 with ctype "text/html").
func (w *World) FailPath(prefix string, status int, ctype string) {}

// SetBattery sets the Zero's charge; a negative percent is no battery (a PC).
func (w *World) SetBattery(percent int) {}

// SetBatteryUnreadable makes the battery there but unreadable (the gauge didn't answer): Deps.Battery gives (-1,
// true) until the next SetBattery.
func (w *World) SetBatteryUnreadable() {}

// At runs f once, on the world's ticker, when the virtual clock has moved d past now: how a test acts "during" a
// wait (a double press of reset while a retry waits for a fresh bootloader) without racing the backend's goroutine.
// f may call the world's and the boards' methods.
func (w *World) At(d time.Duration, f func()) {}

// SetClockSynced says whether the Zero's clock is synchronised.
func (w *World) SetClockSynced(ok bool) {}

// SetDialout puts the owner in the dialout group or not: without it every open fails with serialport.ErrPermission.
func (w *World) SetDialout(ok bool) {}

// SetMessenger writes a messenger settings file (SPEC.md 8.4) into dir and makes Deps point at it.
func (w *World) SetMessenger(dir string, r nodecli.MessengerRadio) error { return errNotBuilt }

// Plug plugs a board in: it shows on USB at once, in the mode it is in, under the lowest free tty name of its kind.
func (w *World) Plug(b *Board) {}

// Unplug pulls a board out: its port goes away at once and every call on an open one fails with
// serialport.ErrGone.
func (w *World) Unplug(b *Board) {}

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
func (w *World) Problems() []string { return nil }

// Board is one fake board. Make it with one of the constructors, change it before (or while) it is plugged in.
type Board struct{}

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
func HeltecV4(fw Firmware) *Board { return &Board{} }

// HeltecV3 is a Heltec v3 behind a CP2102 bridge (10c4:ea60, serial "0001": not a useful one; socket 1-1.2), reset
// into its ROM through the bridge's auto-reset circuit.
func HeltecV3(fw Firmware) *Board { return &Board{} }

// RAK4631 is the owner's RAK WisBlock 4631: an nRF52840, app 239a:8029 "TinyUSB Serial", bootloader 239a:002a
// "nRF Serial", serial D2A1F3C4B5E69788, socket 1-1.3, SoftDevice S140 v6. The 1200-baud touch drops it into the
// bootloader, which shows on USB about a second later; after STOP with a good image it leaves for the app.
func RAK4631(fw Firmware) *Board { return &Board{} }

// XiaoNRF52 is one of the owner's Seeed Xiao nRF52840 boards (2886:8044 "XIAO nRF52840", bootloader 2886:0045,
// serial 8A4C2E1F0B3D5A69), whose USB identity matches three catalog boards.
func XiaoNRF52(fw Firmware) *Board { return &Board{} }

// Tty is the board's tty name now ("ttyACM0"), or "" while it isn't on USB (unplugged, or between modes).
func (b *Board) Tty() string { return "" }

// Running is what the board runs now: what was last written to it, once it has started it.
func (b *Board) Running() Firmware { return Firmware{} }

// Node is the console the board runs now (nil when it runs none), to check what setup sent and stored. Its
// Commands hold the password: never print them.
func (b *Board) Node() *nodecli.FakeNode { return nil }

// ESP is an ESP32 board's chip (nil on an nRF52), to check what was written and erased.
func (b *Board) ESP() *esp.FakeESP { return nil }

// Sessions are an nRF52's bootloader sessions so far, oldest first.
func (b *Board) Sessions() []nrfdfu.FakeResult { return nil }

// NextBootloader makes the nRF52's next bootloader session act out an nrfdfu.NewFakeBootloader scenario
// ("drop-ack:5", "unplug-at:12", "bad-crc", …); "" is a good bootloader. A SoftDevice other than the board's is
// refused as the real bootloader refuses it.
func (b *Board) NextBootloader(scenario string) {}

// UnplugWhileWriting unplugs the board as the n-th block of a flash arrives (an ESP32 FLASH_DEFL_DATA, an nRF52 DATA
// frame), counting from 1.
func (b *Board) UnplugWhileWriting(n int) {}

// UnplugAfterCommands unplugs the board once its console has handled n commands of a setup.
func (b *Board) UnplugAfterCommands(n int) {}

// PressReset presses the reset button once: the app restarts (and an nRF52 re-enumerates).
func (b *Board) PressReset() {}

// DoublePressReset presses reset twice quickly: an nRF52 starts its bootloader, fresh, whatever it was doing.
func (b *Board) DoublePressReset() {}

// Demo is the demo build's world (cmd/meshflash with -tags demo): a Heltec v4 running repeater v1.17.1 named
// “Hilltop 2” and a RAK 4631 running companion firmware, the committed catalog already saved under cacheDir,
// the battery at 87%, and time paced so a flash takes about half a minute on the screen. log is the demo's
// screen.log.
func Demo(cacheDir, version string, log io.Writer) (flash.Deps, error) {
	return flash.Deps{}, errNotBuilt
}

// The sites a world's in-memory Deps use; Handler serves the same paths under any host.
var Sites = catalog.Sites{
	MeshCore: "http://meshcore.fake.invalid/mc",
	ZephCore: "http://zephcore.fake.invalid/zc",
	GitHub:   "http://github.fake.invalid/gh",
}
