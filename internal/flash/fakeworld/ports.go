package fakeworld

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"meshflash/internal/esp"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// What a backend says it opened a port for (OpenedFor). A port it says nothing about is a probe's, held to the
// probe's rules: ver, get <name> (never get guest.password) and board, and never a bootloader port.
const (
	purposeFlash = "flash" // a flash job: the ESP32 ROM, the DFU session, the checks after it, the tty's speed
	purposeSetup = "setup" // a setup job: its commands, and the check after the reboot
	purposeRead  = "read"  // ReadNode: reads only, a room server's guest password included (the form's default)
)

// Scan lists the ports on USB now, as usbid.Scan would from sysfs (Deps.Scan): ttyACM before ttyUSB, by number.
func (w *World) Scan() ([]usbid.Port, error) {
	w.update()
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []usbid.Port
	for _, b := range w.boards {
		b.mu.Lock()
		if b.plugged && b.tty != "" {
			p := b.identity()
			p.Name, p.Dev = b.tty, "/dev/"+b.tty
			out = append(out, p)
		}
		b.mu.Unlock()
	}
	sortPorts(out)
	return out, nil
}

// session is one port a backend opened, from its open to its close.
type session struct {
	w         *World
	b         *Board
	n         int    // its number among the board's opens, from 1
	tty       string // the tty it was opened on
	mode      string // the board's mode then
	baud      int
	exclusive bool
	touch     bool // opened at 1200 baud exclusively: the 1200-baud touch
	boot      *nrfdfu.FakeBootloader
	hadStart  bool // the bootloader session already had START when this port was opened

	fake *serialport.Fake // set once made; read under b.mu

	mu       sync.Mutex
	purpose  string
	closed   bool
	line     []byte // a console line not yet ended
	cmds     int    // console commands sent on it
	frames   int    // DFU frames sent on it
	flagged  map[string]bool
	lastBaud int
}

func (s *session) purposeNow() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.purpose
}

// what names the session in a problem: "Heltec v4: port 3 (ttyACM0, a probe)".
func (s *session) what() string {
	s.mu.Lock()
	p := s.purpose
	s.mu.Unlock()
	return fmt.Sprintf("%s: port %d (%s, %s)", s.b.name, s.n, s.tty, sessionWord(p))
}

// open is the world's serialport.Opener (Deps.Open).
func (w *World) open(path string, o serialport.Options) (serialport.Port, error) {
	w.update()
	tty := strings.TrimPrefix(path, "/dev/")
	w.mu.Lock()
	var b *Board
	for _, c := range w.boards {
		c.mu.Lock()
		if c.plugged && c.tty != "" && c.tty == tty {
			b = c
		}
		c.mu.Unlock()
	}
	if b == nil {
		w.mu.Unlock()
		return nil, fmt.Errorf("fakeworld: opening %s: %w", path, serialport.ErrGone)
	}
	if !w.dialout {
		w.mu.Unlock()
		return nil, fmt.Errorf("fakeworld: opening %s: %w", path, serialport.ErrPermission)
	}
	b.mu.Lock()
	if b.busy {
		b.mu.Unlock()
		w.mu.Unlock()
		return nil, fmt.Errorf("fakeworld: opening %s: %w", path, serialport.ErrBusy)
	}
	if b.open != nil {
		name := b.name
		b.mu.Unlock()
		w.mu.Unlock()
		w.problem("%s: %s was opened while the backend still had it open: one board operation at a time", name, tty)
		return nil, fmt.Errorf("fakeworld: opening %s: %w", path, serialport.ErrBusy)
	}
	b.opens++
	s := &session{w: w, b: b, n: b.opens, tty: tty, mode: b.mode, baud: o.Baud, exclusive: o.Exclusive,
		flagged: map[string]bool{}}
	if s.baud == 0 {
		s.baud = 115200
	}
	s.lastBaud = s.baud
	s.touch = o.Baud == nrfdfu.TouchBaud && o.Exclusive
	var dev serialport.Device
	var app *nrfdfu.FakeApp
	appKind := b.kind == kindNRF && b.mode == modeApp
	switch {
	case b.kind == kindNRF && b.mode == modeBootloader:
		s.boot = b.boot
		s.hadStart = b.boot.Result().Start != nil
		dev = b.boot
	case appKind:
		app = b.app
		dev = &nrfAppDevice{b: b, app: app, s: s}
	case b.kind == kindESPTinyUSB && b.mode == modeApp:
		dev = &tinyDevice{b: b, s: s}
	default:
		dev = &espDevice{b: b, e: b.esp, s: s}
	}
	b.open = s
	w.sessions = append(w.sessions, s)
	w.speeds[tty] = s.baud
	name := b.name
	b.mu.Unlock()
	w.mu.Unlock()

	if o.Baud == 0 {
		w.problem("%s: port %d (%s) was opened without a baud: every open sets its speed", name, s.n, tty)
	}
	if !o.Exclusive {
		w.problem("%s: port %d (%s) was opened without Exclusive: every open is exclusive", name, s.n, tty)
	}
	if o.Baud == nrfdfu.TouchBaud && !appKind {
		w.problem("%s: port %d (%s) was opened at 1200 baud, a touch, but it isn't an nRF52 app's port", name, s.n, tty)
	}

	// The fake is made with no lock held: making it tells the device the lines are up, and the device takes its
	// board's lock.
	fake := serialport.NewFake(dev, w.clock, w.wire, serialport.Options{Baud: s.baud})
	b.mu.Lock()
	s.fake = fake
	switch {
	case s.boot != nil:
		s.boot.Port = fake
	case app != nil:
		app.Port = fake
	}
	gone := b.open != s // the board left USB while the fake was made
	b.mu.Unlock()
	if gone {
		fake.Unplug()
	}
	return &worldPort{s: s, fake: fake}, nil
}

// worldPort is a port the backend opened: the fake, watched for the world's rules.
type worldPort struct {
	s    *session
	fake *serialport.Fake
}

// OpenedFor is how a backend tells the world what it opened a port for: "flash", "setup" or "read". internal/flash
// says so through an interface of its own, which no real port has; a port it says nothing about is a probe's.
func (p *worldPort) OpenedFor(purpose string) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	p.s.purpose = purpose
}

func (p *worldPort) Read(b []byte, timeout time.Duration) (int, error) {
	return p.fake.Read(b, timeout)
}
func (p *worldPort) SetDTR(on bool) error { return p.fake.SetDTR(on) }
func (p *worldPort) SetRTS(on bool) error { return p.fake.SetRTS(on) }
func (p *worldPort) FlushInput() error    { return p.fake.FlushInput() }

func (p *worldPort) SetBaud(baud int) error {
	if err := p.fake.SetBaud(baud); err != nil {
		return err
	}
	p.s.mu.Lock()
	p.s.lastBaud = baud
	p.s.mu.Unlock()
	w := p.s.w
	w.mu.Lock()
	w.speeds[p.s.tty] = baud
	w.mu.Unlock()
	return nil
}

func (p *worldPort) Close() error {
	s := p.s
	s.mu.Lock()
	already, baud, touch := s.closed, s.lastBaud, s.touch
	s.mu.Unlock()
	err := p.fake.Close()
	if already {
		return err
	}
	if s.mode == modeBootloader {
		s.checkBootloaderOpen()
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	w := s.w
	w.mu.Lock()
	b := s.b
	b.mu.Lock()
	if b.open == s {
		b.open = nil
	}
	b.mu.Unlock()
	w.mu.Unlock()
	if baud == nrfdfu.TouchBaud && !touch {
		w.problem("%s was closed at 1200 baud, which reboots an nRF52 app into DFU: only the touch may", s.what())
	}
	return err
}

// Write checks what is sent against the world's rules, then sends it.
func (p *worldPort) Write(b []byte) (int, error) {
	s := p.s
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return p.fake.Write(b)
	}
	if s.mode == modeBootloader {
		s.checkBootloaderOpen()
	}
	espSession := s.b.kind != kindNRF
	var defl, erase int
	if espSession {
		defl, erase = s.b.esp.Count(opDeflData), s.b.esp.Count(opErase)
	}
	if frames := slipFrames(b); len(frames) > 0 {
		for _, f := range frames {
			s.checkFrame(f)
		}
	} else if len(b) > 0 {
		s.consoleText(b)
	}
	if s.blockArrives(b) {
		p.fake.Unplug()
	}
	n, err := p.fake.Write(b)
	if espSession && err == nil {
		// A real ESP32 takes the time a block takes to cross 115200 baud, and about 10 s to erase its chip.
		if s.b.esp.Count(opDeflData) > defl {
			s.w.clock.Advance(time.Duration(len(b)) * time.Second / blockRate)
		}
		if s.b.esp.Count(opErase) > erase {
			s.w.clock.Advance(chipErasing)
		}
	}
	return n, err
}

// checkBootloaderOpen holds a session that isn't a flash's to the rule that only a flash opens a bootloader's port.
func (s *session) checkBootloaderOpen() {
	if s.purposeNow() != purposeFlash {
		s.flag("boot", "%s opened the bootloader's port: only a flash may (a probe answers a bootloader without "+
			"opening it)", s.what())
	}
}

// flag records a problem once per session and kind.
func (s *session) flag(kind, format string, args ...any) {
	s.mu.Lock()
	seen := s.flagged[kind]
	s.flagged[kind] = true
	s.mu.Unlock()
	if !seen {
		s.w.problem(format, args...)
	}
}

// checkFrame holds one SLIP frame to the rules: no SLIP to an nRF52 app, only DFU to its bootloader, START only in a
// flash and never into a used session, and FLASH_BEGIN or an erase only in a flash.
func (s *session) checkFrame(f []byte) {
	hci := len(f) >= 6 && f[0]&0x80 != 0 && f[1]&0x0F == 14
	switch {
	case s.b.kind == kindNRF && s.mode == modeApp:
		s.flag("slip-app", "%s sent a SLIP frame to the nRF52's app: DFU goes only to its bootloader", s.what())
	case s.b.kind == kindNRF && !hci:
		s.flag("slip-boot", "%s sent an ESP32's SLIP to the nRF52 bootloader", s.what())
	case s.b.kind == kindNRF:
		s.mu.Lock()
		s.frames++
		first := s.frames == 1
		s.mu.Unlock()
		if f[4] == 3 { // DFU_START_PACKET
			if s.purposeNow() != purposeFlash {
				s.flag("start-nojob", "%s sent DFU START with no flash job running", s.what())
			}
			if first && s.hadStart {
				s.flag("start-stale", "%s: START sent into a bootloader session that already had START", s.what())
			}
		}
	case hci:
		s.flag("dfu-esp", "%s sent an nRF52 DFU frame (START) to an ESP32", s.what())
	case len(f) >= 2 && f[0] == 0x00:
		switch f[1] {
		case 0x02, 0x10, opErase: // FLASH_BEGIN, FLASH_DEFL_BEGIN, ERASE_FLASH
			if s.purposeNow() != purposeFlash {
				s.flag("flashbegin-nojob", "%s sent FLASH_BEGIN or an erase with no flash job running", s.what())
			}
		}
	}
}

// consoleText splits what was written into console commands, keeps them for the password rule, and holds a probe
// to its commands.
func (s *session) consoleText(b []byte) {
	s.mu.Lock()
	s.line = append(s.line, b...)
	var lines []string
	for {
		i := strings.IndexAny(string(s.line), "\r")
		if i < 0 {
			break
		}
		lines = append(lines, strings.TrimRight(string(s.line[:i]), "\n"))
		s.line = s.line[i+1:]
	}
	purpose := s.purpose
	first := s.cmds
	s.cmds += len(lines)
	s.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	s.b.mu.Lock()
	for i, l := range lines {
		s.b.commands = append(s.b.commands, command{session: s.n, purpose: purpose, index: first + i + 1, text: l})
	}
	s.b.mu.Unlock()
	if purpose != "" {
		return
	}
	for i, l := range lines {
		key, isGet := strings.CutPrefix(l, "get ")
		switch {
		case l == "ver", l == "board":
		case isGet && strings.TrimSpace(key) == "guest.password":
			s.flag(fmt.Sprintf("guest-%d", first+i), "%s asked get guest.password (command %d): a probe reads the "+
				"name, never the guest password", s.what(), first+i+1)
		case isGet && strings.TrimSpace(key) != "":
		default:
			s.flag(fmt.Sprintf("cmd-%d", first+i), "%s sent command %d, which isn't ver, get <name> or board: a "+
				"probe never changes the node", s.what(), first+i+1)
		}
	}
}

// blockArrives counts the blocks of a flash for UnplugWhileWriting (an ESP32's FLASH_DEFL_DATA, an nRF52's DFU DATA
// frame) and says whether this write's block is the one that pulls the board out.
func (s *session) blockArrives(b []byte) bool {
	frames := slipFrames(b)
	blocks := 0
	for _, f := range frames {
		switch {
		case s.b.kind == kindNRF && len(f) >= 6 && f[4] == 4: // DFU_DATA_PACKET
			blocks++
		case s.b.kind != kindNRF && len(f) >= 2 && f[0] == 0x00 && f[1] == opDeflData:
			blocks++
		}
	}
	if blocks == 0 {
		return false
	}
	bd := s.b
	bd.mu.Lock()
	defer bd.mu.Unlock()
	if bd.unplugAt == 0 {
		return false
	}
	bd.blocks += blocks
	if bd.blocks < bd.unplugAt {
		return false
	}
	bd.unplugAt = 0
	bd.unplugReq = true
	return true
}

// slipFrames decodes the SLIP frames in one write (0xC0 … 0xC0, 0xDB escapes); none for console text.
func slipFrames(b []byte) [][]byte {
	if len(b) == 0 || b[0] != 0xC0 {
		return nil
	}
	var out [][]byte
	var cur []byte
	in, esc := false, false
	for _, c := range b {
		switch {
		case c == 0xC0:
			if in && len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			in = true
		case !in:
		case esc:
			esc = false
			switch c {
			case 0xDC:
				cur = append(cur, 0xC0)
			case 0xDD:
				cur = append(cur, 0xDB)
			default:
				cur = append(cur, c)
			}
		case c == 0xDB:
			esc = true
		default:
			cur = append(cur, c)
		}
	}
	return out
}

// nrfAppDevice is an nRF52 running its app: MeshCore's 1200-baud reset (FakeApp; nil when the board ignores the
// touch) and its console, if the firmware has one.
type nrfAppDevice struct {
	b   *Board
	app *nrfdfu.FakeApp
	s   *session
}

func (d *nrfAppDevice) Lines(dtr, rts bool, baud int) []byte {
	if d.app != nil {
		d.app.Lines(dtr, rts, baud)
	}
	if n := d.b.currentNode(); n != nil {
		return n.Lines(dtr, rts, baud)
	}
	return nil
}

func (d *nrfAppDevice) Receive(p []byte) []byte { return d.b.console(d.s, p) }

// espDevice is an ESP32 on its USB-Serial-JTAG or behind a bridge: the ROM and the stub while FakeESP runs them,
// its app's console otherwise.
type espDevice struct {
	b *Board
	e *esp.FakeESP
	s *session
}

func (d *espDevice) Lines(dtr, rts bool, baud int) []byte {
	out := d.e.Lines(dtr, rts, baud)
	if m := d.e.Mode(); m == "rom" || m == "stub" {
		return out
	}
	if n := d.b.currentNode(); n != nil {
		out = append(out, n.Lines(dtr, rts, baud)...)
	}
	return out
}

func (d *espDevice) Receive(p []byte) []byte {
	if m := d.e.Mode(); m == "rom" || m == "stub" {
		return d.e.Receive(p)
	}
	return d.b.console(d.s, p)
}

// tinyDevice is an ESP32-S3 app on TinyUSB: the classic reset's end, (DTR, RTS) (1,0) then (0,0), reboots it into
// the ROM, which comes back as another USB device, so this port goes (as internal/esp's TestTinyUSBReenumerates
// models it).
type tinyDevice struct {
	b        *Board
	s        *session
	mu       sync.Mutex
	dtr, rts bool
}

func (d *tinyDevice) Lines(dtr, rts bool, baud int) []byte {
	d.mu.Lock()
	reset := d.dtr && !d.rts && !dtr && !rts
	d.dtr, d.rts = dtr, rts
	d.mu.Unlock()
	if reset {
		d.b.mu.Lock()
		d.b.tinyReset = true
		fake := d.s.fake
		d.b.mu.Unlock()
		if fake != nil {
			fake.Unplug()
		}
		return nil
	}
	if n := d.b.currentNode(); n != nil {
		return n.Lines(dtr, rts, baud)
	}
	return nil
}

func (d *tinyDevice) Receive(p []byte) []byte { return d.b.console(d.s, p) }
