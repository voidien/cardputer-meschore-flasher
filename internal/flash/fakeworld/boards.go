package fakeworld

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"meshflash/internal/esp"
	"meshflash/internal/nodecli"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/usbid"
)

// boardKind is how a board shows on USB and gets into its bootloader.
type boardKind int

const (
	kindESPJTAG    boardKind = iota // an ESP32-S3 on its own USB-Serial-JTAG: one identity in the app and the ROM
	kindESPBridge                   // an ESP32 behind a USB-to-UART bridge: the bridge's identity, always
	kindESPTinyUSB                  // an ESP32-S3 app on TinyUSB: its ROM comes back as the USB-Serial-JTAG
	kindNRF                         // an nRF52840: the app, or the Adafruit bootloader after the touch or a double press
)

// The modes a board shows on USB.
const (
	modeApp        = "app"
	modeBootloader = "bootloader" // an nRF52's serial DFU bootloader
	modeROM        = "rom"        // a TinyUSB board's ROM, on its USB-Serial-JTAG
	modeBTDFU      = "btdfu"      // an OTAFIX nRF52 that refused an init packet: Bluetooth DFU, no USB port
)

// How long a board takes to come back on USB after it leaves it (a reset, the touch, STOP): about a second, as
// research 5.2 has it for the bootloader; long enough that a backend polling every 100 ms sees it gone first.
const (
	backAfter   = time.Second
	romAfter    = 500 * time.Millisecond // a TinyUSB board's ROM port, after the classic reset
	blockRate   = 11520                  // bytes a second over 115200 baud, for a FLASH_DEFL_DATA block
	chipErasing = 10 * time.Second       // an ESP32 chip erase
	appAddress  = esp.AppAddress
	opDeflData  = 0x11 // FLASH_DEFL_DATA
	opErase     = 0xD0 // ERASE_FLASH
)

// boardSpec is what a board is: its name in problems, how it works, and its USB identities (Name and Dev blank).
type boardSpec struct {
	name         string
	kind         boardKind
	chip         esp.Chip
	softDevice   uint16 // an nRF52's SoftDevice, which its bootloader checks the init packet against
	nodeBoard    string // what a MeshCore console answers to `board`
	appID        usbid.Port
	bootID       usbid.Port // the bootloader after the touch
	bootDoubleID usbid.Port // the bootloader after a double press of reset
	romID        usbid.Port // a TinyUSB board's ROM
}

// The owner's boards, as internal/usbid's tests have them (fakesys_test.go: serials made up in each chip's format).
var (
	heltecV4Spec = boardSpec{name: "Heltec v4", kind: kindESPJTAG, chip: esp.ESP32S3, nodeBoard: "Heltec V4",
		appID: usbid.Port{VID: 0x303a, PID: 0x1001, BCD: 0x0101, Manufacturer: "Espressif",
			Product: "USB JTAG/serial debug unit", Serial: "60:55:F9:C0:27:18", Driver: "cdc_acm", USBPath: "1-1.3"}}
	heltecV3Spec = boardSpec{name: "Heltec v3", kind: kindESPBridge, chip: esp.ESP32S3, nodeBoard: "Heltec V3",
		appID: usbid.Port{VID: 0x10c4, PID: 0xea60, BCD: 0x0100, Manufacturer: "Silicon Labs",
			Product: "CP2102 USB to UART Bridge Controller", Serial: "0001", Driver: "cp210x", USBPath: "1-1.2"}}
	trackerV2Spec = boardSpec{name: "Heltec Wireless Tracker v2", kind: kindESPTinyUSB, chip: esp.ESP32S3,
		nodeBoard: "Heltec Tracker V2",
		appID: usbid.Port{VID: 0x303a, PID: 0x0002, BCD: 0x0100, Manufacturer: "Heltec", Product: "heltec_tracker v2",
			Serial: "6055F9C02719", Interface: "TinyUSB CDC", Driver: "cdc_acm", USBPath: "1-1.4"},
		romID: usbid.Port{VID: 0x303a, PID: 0x1001, BCD: 0x0101, Manufacturer: "Espressif",
			Product: "USB JTAG/serial debug unit", Serial: "60:55:F9:C0:27:19", Driver: "cdc_acm", USBPath: "1-1.4"}}
	rak4631Spec = boardSpec{name: "RAK 4631", kind: kindNRF, softDevice: nrfdfu.SoftDeviceS140v6, nodeBoard: "RAK 4631",
		appID: usbid.Port{VID: 0x239a, PID: 0x8029, BCD: 0x0100, Manufacturer: "RAKwireless",
			Product: "WisCore RAK4631 Board", Serial: "D2A1F3C4B5E69788", Interface: usbid.InterfaceNRFApp,
			Driver: "cdc_acm", USBPath: "1-1.3"},
		bootID: usbid.Port{VID: 0x239a, PID: 0x002a, BCD: 0x0100, Manufacturer: "RAKWireless", Product: "WisBlock RAK4631",
			Serial: "D2A1F3C4B5E69788", Interface: usbid.InterfaceNRFBootloader, Driver: "cdc_acm", USBPath: "1-1.3"},
		bootDoubleID: usbid.Port{VID: 0x239a, PID: 0x0029, BCD: 0x0100, Manufacturer: "RAKWireless",
			Product: "WisBlock RAK4631", Serial: "D2A1F3C4B5E69788", Interface: usbid.InterfaceNRFBootloader,
			Driver: "cdc_acm", USBPath: "1-1.3"}}
	xiaoSpec = boardSpec{name: "Xiao nRF52840", kind: kindNRF, softDevice: nrfdfu.SoftDeviceS140v7,
		nodeBoard: "Xiao_nrf52",
		appID: usbid.Port{VID: 0x2886, PID: 0x8044, BCD: 0x0100, Manufacturer: "Seeed Studio", Product: "XIAO nRF52840",
			Serial: "8A4C2E1F0B3D5A69", Interface: usbid.InterfaceNRFApp, Driver: "cdc_acm", USBPath: "1-1.2"},
		bootID: usbid.Port{VID: 0x2886, PID: 0x0045, BCD: 0x0100, Manufacturer: "Seeed Studio",
			Product: "XIAO nRF52840 Sense", Serial: "8A4C2E1F0B3D5A69", Interface: usbid.InterfaceNRFBootloader,
			Driver: "cdc_acm", USBPath: "1-1.2"}}
)

func newBoard(spec boardSpec, fw Firmware) *Board {
	if spec.bootDoubleID.VID == 0 {
		spec.bootDoubleID = spec.bootID
	}
	b := &Board{boardSpec: spec, mode: modeApp, fw: fw}
	if spec.kind != kindNRF {
		b.esp = esp.NewFakeESP(spec.chip)
		b.esp.USBJTAG = spec.kind != kindESPBridge // the TinyUSB board's ROM is on the USB-Serial-JTAG
		b.esp.FlashBytes = 1 << 20                 // enough for any made-up image, and quick to copy
	}
	b.node = newNode(fw, nil, spec.nodeBoard)
	return b
}

// newNode is the console firmware fw runs (nil for none), with old's stored settings kept when old is given (an
// update keeps the node's prefs; a full wipe or a new board doesn't).
func newNode(fw Firmware, old *nodecli.FakeNode, board string) *nodecli.FakeNode {
	if fw.Role != nodecli.RoleRepeater && fw.Role != nodecli.RoleRoomServer {
		return nil
	}
	version := fw.Version
	if version == "" {
		version = "v1.18.0"
	}
	if _, err := nodecli.ParseVersion(version); err != nil {
		return nil // nothing a console could print: as good as none
	}
	n := nodecli.NewFakeNode(fw.Role, version)
	n.Board = board
	n.Banner = true
	if old != nil {
		n.Name, n.Radio, n.TxPower, n.PathHashMode = old.Name, old.Radio, old.TxPower, old.PathHashMode
		n.Lat, n.Lon, n.AdvertInterval, n.Clock = old.Lat, old.Lon, old.AdvertInterval, old.Clock
		n.AdminPassword = old.AdminPassword
		if fw.Role == nodecli.RoleRoomServer && old.Role == nodecli.RoleRoomServer {
			n.GuestPassword = old.GuestPassword
		}
		n.ActiveRadio = n.Radio
	}
	if fw.Name != "" {
		n.Name = fw.Name
	}
	n.RegionLoad = fw.Stuck
	return n
}

// identity is the USB identity the board shows in its mode now.
func (b *Board) identity() usbid.Port {
	switch {
	case b.mode == modeBootloader && b.double:
		return b.bootDoubleID
	case b.mode == modeBootloader:
		return b.bootID
	case b.mode == modeROM:
		return b.romID
	}
	return b.appID
}

// ttyPrefix is the tty its driver gives it: cdc-acm's ttyACM, or a bridge driver's ttyUSB.
func (b *Board) ttyPrefix() string {
	if b.kind == kindESPBridge {
		return "ttyUSB"
	}
	return "ttyACM"
}

func (b *Board) currentNode() *nodecli.FakeNode {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.node
}

// powerUp starts the board as powering it does, in its mode: an nRF52's app or a fresh bootloader session, an ESP32
// running its app. The first time it is plugged in it is as it was made (a stuck console stays stuck); after that a
// replug is a power cycle, which restarts the console. Called with b.mu held, before arriveLocked.
func (b *Board) powerUp() {
	first := !b.powered
	b.powered = true
	switch {
	case first && b.kind == kindNRF && b.mode == modeBootloader:
		b.newSession()
	case first && b.kind == kindNRF:
		b.newApp()
	case first:
		// An ESP32 as made: running its app (FakeESP's "off" is taken for that).
	case b.kind == kindNRF && b.mode == modeBootloader:
		b.newSession()
	case b.kind == kindNRF && b.mode == modeBTDFU:
		// Bluetooth DFU stays until a double press of reset: no port.
	case b.kind == kindNRF:
		b.mode = modeApp
		b.newApp()
		b.restartNode()
	default:
		b.mode = modeApp
		espToApp(b.esp)
		b.restartNode()
	}
}

// newApp is a fresh nRF52 app boot (FakeApp latches its reset once).
func (b *Board) newApp() {
	b.app = nil
	if !b.ignoreTouch {
		b.app = &nrfdfu.FakeApp{}
	}
}

// newSession is a fresh bootloader session, acting out the scenario NextBootloader set.
func (b *Board) newSession() {
	boot, _ := nrfdfu.NewFakeBootloader(b.scenario) // NextBootloader checked it
	b.unplugFrame = -1
	if n, ok := strings.CutPrefix(b.scenario, "unplug-at:"); ok {
		// The fake bootloader pulls its port out at that frame; the board is then off USB altogether.
		b.unplugFrame, _ = strconv.Atoi(n)
	}
	b.scenario = ""
	boot.SoftDevice = b.softDevice
	b.boot = boot
	b.boots = append(b.boots, boot)
}

// restartNode restarts the console as a reset does: its stored settings kept, region-load mode and a hang ended.
func (b *Board) restartNode() {
	if b.node == nil {
		return
	}
	b.node = newNode(Firmware{Role: b.node.Role, Version: b.node.Version.Raw}, b.node, b.nodeBoard)
	b.reboots = 0
}

// setFirmware is the board running fw from now on, its node's settings kept unless erased (an nRF52's prefs live
// outside the app's pages, so they outlast an app erased by a DFU session that stopped).
func (b *Board) setFirmware(fw Firmware, erased bool) {
	old := b.node
	if old == nil {
		old = b.prefs
	}
	if erased {
		old, b.prefs = nil, nil
	}
	if fw.Name == "" && old != nil {
		fw.Name = old.Name
	}
	b.fw = fw
	if old != nil {
		b.prefs = old
	}
	b.node = newNode(fw, old, b.nodeBoard)
	b.reboots = 0
}

// leaveLocked takes the board off USB: its open port, if any, goes, and its console sees DTR go with it. Called with
// b.mu held.
func (b *Board) leaveLocked() {
	b.tty = ""
	if s := b.open; s != nil {
		if s.fake != nil {
			s.fake.Unplug()
		}
		b.open = nil
	}
	if b.node != nil {
		b.node.Lines(false, false, 0)
	}
}

// comeBack puts the board back on USB in mode after d, the time a reset or a re-enumeration takes.
func (b *Board) comeBack(now time.Time, d time.Duration, mode string, double bool) {
	b.pending, b.at, b.arrive, b.double = true, now.Add(d), mode, double
}

// arriveLocked puts the board on USB in its mode, under the lowest free tty name of its kind. Called with w.mu and
// b.mu held.
func (w *World) arriveLocked(b *Board) {
	if b.mode == modeBTDFU {
		b.tty = ""
		return
	}
	b.tty = w.freeTty(b)
}

// freeTty is the lowest tty name the board's driver would give it: not another board's, not held by HoldTty, and
// not held by a port still open on it (cdc-acm frees a minor only at the last close). Called with w.mu held.
func (w *World) freeTty(b *Board) string {
	used := map[string]bool{}
	for name := range w.held {
		used[name] = true
	}
	for _, o := range w.boards {
		if o != b {
			if o.tryTty() != "" {
				used[o.tryTty()] = true
			}
		}
	}
	for _, s := range w.sessions {
		s.mu.Lock()
		if !s.closed {
			used[s.tty] = true
		}
		s.mu.Unlock()
	}
	for n := 0; ; n++ {
		name := b.ttyPrefix() + strconv.Itoa(n)
		if !used[name] {
			return name
		}
	}
}

// tryTty reads another board's tty while w.mu is held. Boards are locked one at a time under w.mu, so taking its
// lock here can't deadlock with a device, which takes only its own board's.
func (b *Board) tryTty() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tty
}

// tick is the world's one ticker: every move of the clock updates the boards and runs the At events due.
func (w *World) tick(now time.Time) {
	w.update()
	for {
		w.mu.Lock()
		if len(w.events) == 0 || w.events[0].due.After(now) {
			w.mu.Unlock()
			break
		}
		e := w.events[0]
		w.events = w.events[1:]
		w.mu.Unlock()
		e.f()
		w.update()
	}
}

// update moves every board on from what its fakes have done: a touch, STOP, a reboot, a reset into the ROM, a hard
// reset after a flash, a board coming back after a while.
func (w *World) update() {
	now := w.clock.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range w.boards {
		b.mu.Lock()
		w.updateLocked(b, now)
		b.mu.Unlock()
	}
}

func (w *World) updateLocked(b *Board, now time.Time) {
	if !b.plugged {
		return
	}
	if b.unplugReq {
		b.unplugReq, b.plugged, b.pending = false, false, false
		b.leaveLocked()
		return
	}
	if b.pending {
		if now.Before(b.at) {
			return
		}
		b.pending, b.mode = false, b.arrive
		switch b.mode {
		case modeApp:
			if b.kind == kindNRF {
				b.newApp()
			}
		case modeBootloader:
			b.newSession()
		}
		w.arriveLocked(b)
		return
	}
	if b.tty == "" {
		return
	}
	switch b.kind {
	case kindNRF:
		switch b.mode {
		case modeApp:
			if b.app != nil && b.app.Reset() {
				// The 1200-baud touch: MeshCore reboots into its serial DFU bootloader.
				b.leaveLocked()
				b.app = nil
				b.comeBack(now, backAfter, modeBootloader, false)
			} else if b.nodeRebooted() {
				b.leaveLocked()
				b.comeBack(now, backAfter, modeApp, false)
			}
		case modeBootloader:
			r := b.boot.Result()
			if b.unplugFrame >= 0 && r.Frames > b.unplugFrame {
				b.unplugFrame = -1
				b.plugged, b.pending = false, false
				if r.Start != nil {
					b.setFirmware(Firmware{}, false)
				}
				b.leaveLocked()
				return
			}
			switch {
			case r.Start != nil && !r.AppStarted && b.fw != (Firmware{}):
				// START erased the app: until a good image is written it runs nothing.
				b.setFirmware(Firmware{}, false)
			}
			switch {
			case r.AppStarted:
				fw, ok := parseMarker(b.boot.Image())
				if !ok {
					fw = Firmware{}
				}
				b.setFirmware(fw, false)
				b.leaveLocked()
				b.comeBack(now, backAfter, modeApp, false)
			case r.Reset:
				// It refused the init packet after erasing the app: OTAFIX drops into Bluetooth DFU (5.6).
				b.setFirmware(Firmware{}, false)
				b.leaveLocked()
				b.mode = modeBTDFU
			}
		}
	case kindESPTinyUSB:
		switch b.mode {
		case modeApp:
			if b.tinyReset {
				b.tinyReset = false
				b.leaveLocked()
				espToROM(b.esp)
				b.comeBack(now, romAfter, modeROM, false)
			} else if b.nodeRebooted() {
				b.leaveLocked()
				b.comeBack(now, backAfter, modeApp, false)
			}
		case modeROM:
			if m := b.esp.Mode(); m == "app" || m == "off" {
				b.espCheck()
				b.leaveLocked()
				b.comeBack(now, backAfter, modeApp, false)
			}
		}
	default:
		b.espCheck()
		b.nodeRebooted() // its port stays through a reboot: the console waits for the next open
	}
}

// nodeRebooted says the console has rebooted since last seen (setup's reboot).
func (b *Board) nodeRebooted() bool {
	if b.node == nil {
		return false
	}
	if n := b.node.Reboots(); n > b.reboots {
		b.reboots = n
		return true
	}
	return false
}

// espCheck follows an ESP32 back to its app after a flash: once it runs its app again and has been written to
// since last seen, it runs what is at 0x10000 (nothing, if that isn't a whole image), and a chip erase wiped its
// node's settings.
func (b *Board) espCheck() {
	if m := b.esp.Mode(); m != "app" && m != "off" {
		return
	}
	blocks, erased := b.esp.Count(opDeflData), b.esp.Erased()
	if blocks == b.espBlocks && erased == b.espErased {
		return
	}
	wiped := erased && !b.espErased
	b.espBlocks, b.espErased = blocks, erased
	fw, ok := parseMarker(b.esp.Flash()[appAddress:])
	if !ok {
		fw = Firmware{}
	}
	b.setFirmware(fw, wiped)
}

// espToROM resets a fake ESP32 on its USB-Serial-JTAG into its ROM download mode (DTR asserted as the reset
// begins), as a TinyUSB app's classic reset does: the world drives its lines while no port is open on it.
func espToROM(e *esp.FakeESP) {
	e.Lines(false, false, 115200)
	e.Lines(true, false, 115200)
	e.Lines(false, true, 115200)
	e.Lines(false, false, 115200)
}

// espToApp resets a fake ESP32 into its app (DTR released as the reset begins), as powering it on does.
func espToApp(e *esp.FakeESP) {
	e.Lines(false, false, 115200)
	e.Lines(false, true, 115200)
	e.Lines(false, false, 115200)
}

// reset presses the reset button once, or twice quickly.
func (b *Board) reset(double bool) {
	b.mu.Lock()
	w := b.w
	b.mu.Unlock()
	if w == nil {
		// Never plugged in: it starts in the mode the press leaves it in.
		b.mu.Lock()
		if double && b.kind == kindNRF {
			b.mode = modeBootloader
		}
		b.mu.Unlock()
		return
	}
	now := w.clock.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.plugged {
		if double && b.kind == kindNRF {
			b.mode = modeBootloader
		}
		return
	}
	switch b.kind {
	case kindNRF:
		switch {
		case double:
			b.leaveLocked()
			b.app = nil
			b.comeBack(now, backAfter, modeBootloader, true)
		case b.mode == modeBTDFU && !b.pending:
			// OTAFIX keeps Bluetooth DFU across a single reset: only a double press brings the port back.
		case b.fw == (Firmware{}):
			// No app to start: the bootloader comes up again, a fresh session.
			b.leaveLocked()
			b.app = nil
			b.comeBack(now, backAfter, modeBootloader, b.double)
		default:
			b.leaveLocked()
			b.app = nil
			b.restartNode()
			b.comeBack(now, backAfter, modeApp, false)
		}
	case kindESPTinyUSB:
		b.leaveLocked()
		espToApp(b.esp)
		b.restartNode()
		b.comeBack(now, backAfter, modeApp, false)
	default:
		// The bridge's and the USB-Serial-JTAG's port stay: only the chip restarts.
		if b.open == nil {
			espToApp(b.esp)
		}
		b.restartNode()
	}
}

// cmdHook is an UnplugAfterCommands or AfterCommands: when the setup commands handled reach at; or a PauseAfter:
// when the console has handled a setup command beginning with match.
type cmdHook struct {
	at     int
	match  string
	unplug bool
	f      func()
}

// due says the hook fires now that the setup commands handled number count, the latest being handled.
func (h cmdHook) due(count int, handled []string) bool {
	if h.match == "" {
		return count >= h.at
	}
	return slices.ContainsFunc(handled, func(c string) bool { return strings.HasPrefix(c, h.match) })
}

func (b *Board) addHook(n int, unplug bool, f func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hooks = append(b.hooks, cmdHook{at: b.setupCmds + n, unplug: unplug, f: f})
}

// console passes what the host wrote to the board's console, and keeps count of a setup's commands for the hooks.
func (b *Board) console(s *session, p []byte) []byte {
	n := b.currentNode()
	if n == nil {
		return nil
	}
	before := len(n.Commands())
	out := n.Receive(p)
	if s.purposeNow() != purposeSetup {
		return out
	}
	handled := n.Commands()[before:]
	if len(handled) == 0 {
		return out
	}
	b.mu.Lock()
	b.setupCmds += len(handled)
	var due []cmdHook
	kept := b.hooks[:0]
	for _, h := range b.hooks {
		if h.due(b.setupCmds, handled) {
			due = append(due, h)
		} else {
			kept = append(kept, h)
		}
	}
	b.hooks = kept
	unplug := false
	for _, h := range due {
		unplug = unplug || h.unplug
	}
	var fake = s.fake
	if unplug {
		b.unplugReq = true
	}
	b.mu.Unlock()
	if unplug && fake != nil {
		fake.Unplug()
	}
	for _, h := range due {
		if h.f != nil {
			h.f()
		}
	}
	if unplug {
		return nil // the board went with the reply
	}
	return out
}

// command is one console line the host sent, kept for Problems' password rule.
type command struct {
	session int    // the port's number on the board
	purpose string // what it was opened for
	index   int    // its place in that session, from 1
	text    string // the line itself: it may hold a password, so it never leaves this package
}

// passwordProblems is the rule that a password goes only in its own commands: every password the host sent (in
// `password …` and `set guest.password …`) must appear in no other command (the name excepted: the owner may put the
// admin password in a public name, SPEC.md 11). Passwords under 4 bytes are left out: they turn up anywhere.
func (b *Board) passwordProblems() []string {
	b.mu.Lock()
	cmds := append([]command(nil), b.commands...)
	name := b.name
	b.mu.Unlock()
	var secrets []string
	for _, c := range cmds {
		for _, p := range []string{"password ", "set guest.password "} {
			if pw, ok := strings.CutPrefix(c.text, p); ok && len(pw) >= 4 {
				secrets = append(secrets, pw)
			}
		}
	}
	var out []string
	for _, c := range cmds {
		if strings.HasPrefix(c.text, "password ") || strings.HasPrefix(c.text, "set guest.password ") ||
			strings.HasPrefix(c.text, "set name ") {
			continue
		}
		for _, s := range secrets {
			if strings.Contains(c.text, s) {
				out = append(out, fmt.Sprintf("%s: command %d of port %d (%s) holds a password outside the password "+
					"commands", name, c.index, c.session, sessionWord(c.purpose)))
				break
			}
		}
	}
	return out
}

func sessionWord(purpose string) string {
	if purpose == "" {
		return "a probe"
	}
	return "the " + purpose
}

// sortPorts is usbid.Scan's order: ttyACM before ttyUSB, by number.
func sortPorts(ports []usbid.Port) {
	num := func(name string) (string, int) {
		i := strings.IndexAny(name, "0123456789")
		if i < 0 {
			return name, -1
		}
		n, _ := strconv.Atoi(name[i:])
		return name[:i], n
	}
	sort.Slice(ports, func(i, j int) bool {
		pi, ni := num(ports[i].Name)
		pj, nj := num(ports[j].Name)
		if pi != pj {
			return pi < pj
		}
		return ni < nj
	})
}
