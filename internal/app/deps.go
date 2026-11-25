package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"golang.org/x/sys/unix"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/screen"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// deps are the backend's dependencies: the Program's fake world when it has one (the demo build, and every test of
// this package), else the real boards, network, battery and clock (realDeps). A Logf left unset writes to Env.Log,
// screen.log.
func deps(p Program, env Env) (flash.Deps, error) {
	var d flash.Deps
	if p.Fake != nil {
		var err error
		if d, err = p.Fake(env); err != nil {
			return flash.Deps{}, fmt.Errorf("making the fake world: %w", err)
		}
	} else {
		d = realDeps(env)
	}
	// What flash.Deps says must be set: a world missing one would fail only when the screens first ask.
	if d.Open == nil || d.Scan == nil || d.Dialout == nil || d.Clock == nil || d.Catalog == nil {
		return flash.Deps{}, errors.New("the backend's dependencies are incomplete: Open, Scan, Dialout, Clock " +
			"and Catalog must be set")
	}
	if d.Logf == nil && env.Log != nil {
		log := env.Log
		d.Logf = func(format string, args ...any) { writeLine(log, format, args...) }
	}
	return d, nil
}

// realDeps are the real things, on the device: the serial ports (serialport, the only code that opens a tty), the
// USB list from sysfs (which opens nothing), the dialout check, the wall clock, the catalog and its files over the
// network (fetched only when a key asks, S23), the battery, the clock's NTP state, and the messenger's file (read
// only). It refuses to run in a test: a test that got here would open real ports and reach the real servers
// (CLAUDE.md), so every test of this package gives its Program a Fake.
func realDeps(env Env) flash.Deps {
	if inTest() {
		panic("app: the real boards and network in a test: give the Program a Fake")
	}
	return flash.Deps{
		Open:          serialport.Open,
		Scan:          func() ([]usbid.Port, error) { return usbid.Scan("") },
		Dialout:       usbid.DialoutStatus,
		Clock:         serialport.RealClock,
		Catalog:       catalog.NewClient(env.CacheDir, env.Version, catalog.Options{}),
		Battery:       battery(screen.PowerSupplies, onZero(deviceModel)),
		ClockSynced:   clockSynced,
		MessengerPath: nodecli.MessengerPath(),
	}
}

// inTest says this process is a go test binary, of any package: by the time a test runs, its main has called
// testing.Init, which puts test.v on flag.CommandLine. It is testing.Testing() without importing package testing,
// which would link testing and its flags into dist/meshflash (make test checks that nothing does).
func inTest() bool { return flag.Lookup("test.v") != nil }

// battery reads the Zero's charge from the power supplies in dir (screen.PowerSupplies), afresh each time it is asked
// (every 30 s for the status line, and before a flash), so a reading that failed is tried again next time:
//
//   - (percent, true) from the first power supply of type "Battery" (the Zero's bq27220-0);
//   - (-1, true) when there is a battery, or was one earlier in this run, or this is the CardputerZero (zero, from
//     onZero), but its charge can't be read: the screens then ask before flashing, as under 30% (a battery that
//     can't be read is not "no battery", and on the Zero a fuel gauge whose driver didn't load is one that can't);
//   - (0, false) only when no battery has been seen at all and this isn't the Zero: a PC.
//
// screen.Battery isn't used: its reading is kept for a minute, and its "" means both no battery and one it can't read.
func battery(dir string, zero bool) func() (percent int, ok bool) {
	var seen atomic.Bool
	seen.Store(zero)
	return func() (int, bool) {
		path := findBattery(dir)
		if path == "" {
			if seen.Load() {
				return -1, true
			}
			return 0, false
		}
		seen.Store(true)
		b, err := os.ReadFile(filepath.Join(path, "capacity"))
		if err != nil {
			return -1, true
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || n < 0 || n > 100 {
			return -1, true
		}
		return n, true
	}
}

// deviceModel is where Linux names the board it runs on, from its device tree.
const deviceModel = "/proc/device-tree/model"

// onZero says the program runs on the CardputerZero, which always has a battery: the device tree at path names its
// Raspberry Pi Compute Module 0 ("Raspberry Pi Compute Module 0 Rev 1.0", docs/device.md in the kit). It is told by
// the board, not by whether the fuel gauge is listed, so a gauge that didn't load reads as a battery that can't be
// read. A PC has no such file, or names another board. One function, so how the Zero is told is easy to change.
func onZero(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(strings.TrimRight(string(b), "\x00\n"), "Raspberry Pi Compute Module 0")
}

// findBattery is the folder of the first power supply in dir whose type is "Battery", or "".
func findBattery(dir string) string {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		kind, err := os.ReadFile(filepath.Join(dir, e.Name(), "type"))
		if err == nil && strings.TrimSpace(string(kind)) == "Battery" {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// clockSynced says the Zero's clock is synchronised (NTP), so a setup may set the node's clock (SPEC.md 7): the
// kernel's own view, from adjtimex(2) asking only (Modes 0, which needs no privilege).
func clockSynced() bool {
	var tx unix.Timex
	state, err := unix.Adjtimex(&tx)
	return err == nil && synced(state, tx.Status)
}

// synced reads adjtimex's answer: the clock state isn't TIME_ERROR and the status hasn't STA_UNSYNC, which the kernel
// sets until a time daemon has disciplined the clock.
func synced(state int, status int32) bool {
	return state != unix.TIME_ERROR && status&unix.STA_UNSYNC == 0
}
