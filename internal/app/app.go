// Package app starts meshflash: the flags, the store and config, the backend over the real boards and network (or
// the demo build's fake world), the screens in a terminal or on the LCD, and the end of the program: the draft saved,
// the running job stopped within a bounded time, and how it ended written to screen.log.
//
// Its shape is the CardputerZero app kit's internal/app (code/internal/app/app.go, the owner's own code): Main,
// screenLog, run, absorbSIGTERM and colorProfile, with meshflash's dependencies in place of Loom's.
//
// Phase 2 contract (docs/phase2-design.md): builder E writes the bodies; Program, Env, Fake and Main's signature are
// fixed. The placeholder Main answers --version and otherwise says it isn't built yet.
package app

import (
	"fmt"
	"io"
	"os"

	"meshflash/internal/flash"
	"meshflash/internal/store"
	"meshflash/internal/ui"
)

// The real backend and store are what the screens take; the build stops here if either drifts from the screens'
// interfaces.
var (
	_ ui.Backend = (*flash.Backend)(nil)
	_ ui.Store   = (*store.Store)(nil)
)

// Program says which program this is: meshflash, or the demo build's meshflash-demo.
type Program struct {
	// Name is the program's own name: its folders (~/.config/<Name>, ~/.cache/<Name>), what --version prints
	// ("<Name> <Version>", S3), and screen.log's first line.
	Name string
	// Command is what it is installed as (~/.local/bin/<Command>); it starts its error messages.
	Command string
	// Version is meshflash's own version (CHANGELOG.md): Help shows it and the User-Agent sends it.
	Version string
	// Fake, when set, replaces the boards, the network, the battery and the clock with a fake world: the demo build
	// sets it (cmd/meshflash/demo.go, -tags demo). The release binary leaves it nil and never links a fake. Every
	// test of this package sets it: the real dependencies (serialport.Open, usbid.Scan, the real catalog client) are
	// built in one function that refuses to run under testing.Testing(), so no test opens a real port.
	Fake Fake
}

// Fake makes the backend's dependencies for the demo build, given the folders the app uses.
type Fake func(Env) (flash.Deps, error)

// Env is what a Fake is given.
type Env struct {
	ConfigDir, CacheDir string // the Program's own folders
	Version             string // Program.Version
	// Log is screen.log: the one locked writer that the screens (ui.Options.Log) and the backend (Deps.Logf) write
	// too, so lines never interleave.
	Log io.Writer
}

// Main reads the flags (--version, --screen, --screen-test, --font, --framebuffer, --keyboard, --framebuffer-sys, as
// the kit's) and runs the program; it exits with 1 on an error, which it also writes to screen.log.
func Main(p Program) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(p.Name, p.Version)
		return
	}
	fmt.Fprintln(os.Stderr, p.Command+": not built yet (Phase 2 contract placeholder)")
	os.Exit(1)
}
