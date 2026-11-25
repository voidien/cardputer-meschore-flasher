// Command meshflash is a MeshCore flasher for the M5Stack CardputerZero: plug a LoRa board into the USB-A port, pick
// the board, the role and the version from MeshCore's flasher catalog, and meshflash flashes it, checks it, and sets
// up repeaters and room servers over their serial console. Installed as ~/.local/bin/meshflash; run it in the CLI
// app, or with --screen on the LCD itself (the home-screen entry "MeshFlash").
//
// Built with -tags demo it is meshflash-demo instead: the same screens against a fake world (demo.go), with folders
// of its own, for trying the screens on a PC with no board and no network.
package main

// Phase 2 contract (docs/phase2-design.md): builder E owns this file. Version is the one constant S3 asks for.

import "meshflash/internal/app"

// Version is meshflash's own version (CHANGELOG.md): --version prints it, Help shows it, the User-Agent sends it.
const Version = "0.1.0"

// name and fake are the release binary's; demo.go (-tags demo) changes both before main runs.
var (
	name = "meshflash"
	fake app.Fake
)

func main() { app.Main(app.Program{Name: name, Command: name, Version: Version, Fake: fake}) }
