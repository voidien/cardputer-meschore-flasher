package nodecli

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FakeNode is a MeshCore repeater or room server at the far end of a serialport.Fake, behaving as the firmware's
// serial console does in the versions the live catalog offers, v1.11.0 to v1.18.0, from the MeshCore source at
// those tags (docs/meshcore-flashing.md 7.2-7.6): the echo of each role, "  -> " replies, the commands setup uses
// with their replies and checks, prefix or exact matching of the get keys, the per-version differences (no
// path.hash.mode before v1.14.0, the 300 MHz floor before v1.15.0, the name checks of v1.12.0 and v1.13.0+, how
// get tx prints a negative setting), the stored settings, region-load mode, reboot (no reply, then silent until the
// port is opened again), a boot banner and noise.
//
// Set the exported fields before the port is opened; read them after the port is closed.
type FakeNode struct {
	// Role is RoleRepeater or RoleRoomServer.
	Role string
	// Version is what ver answers; its number picks the behaviour.
	Version Version
	// Board is what `board` answers.
	Board string
	// PublicKey is the 64 hex digits in the banner and get public.key.
	PublicKey string

	// The node's stored settings (its prefs file).
	Name  string
	Radio Radio // the frequency and bandwidth are kept as float32, as the firmware keeps them
	// TxPower is the TX power setting as stored: v1.11.0 and v1.12.0 keep a uint8, v1.13.0+ an int8.
	TxPower int
	// PathHashMode is path.hash.mode, 0-2 (bytes − 1); firmware before v1.14.0 has none.
	PathHashMode int
	// Lat and Lon are the location as stored (doubles; printed through float32).
	Lat, Lon float64
	// AdvertInterval is the local advert interval in the node's own unit, 2 minutes; 1 on a new node.
	AdvertInterval int
	// AdminPassword and GuestPassword are the passwords as stored, at most 15 bytes each.
	AdminPassword, GuestPassword string
	// Clock is the node's clock, Unix seconds.
	Clock int64

	// ActiveRadio is the radio the node runs: what Radio was at its last boot. set radio takes effect at reboot.
	ActiveRadio Radio

	// Banner makes it print "Repeater ID: …" or "Room ID: …" when the port opens after a boot.
	Banner bool
	// Noise is sent once, just ahead of the first echo: a log line in the way of the first reply.
	Noise []byte
	// Silent makes it a board that never answers: companion or KISS firmware, or a bootloader.
	Silent bool
	// RegionLoad puts it in region-load mode: every line is swallowed, echoed with no reply, until a blank one.
	RegionLoad bool
	// Hang makes it stop answering from the first command that starts with this: it echoes but never replies.
	Hang string
	// Store makes it keep something else than it is told: set key ("name", "radio", "tx", "path.hash.mode", "lat",
	// "lon", "advert.interval", "guest.password", or "password") → the value it stores instead.
	Store map[string]string

	mu        sync.Mutex
	pending   []byte // received, not yet read by the firmware's loop
	line      []byte
	open      bool
	booted    bool // the banner of this boot has been printed
	rebooting bool
	hung      bool
	wedged    bool
	noiseSent bool
	commands  []string
	reboots   int
}

// fakeBuilds are the commits and dates of the releases the live catalog offers (`git log -1 <tag>` in MeshCore).
// The Build date is CI's run date; v1.18.0's is the one in the real release images (TestSamples), the others are
// the tag's date standing in (UNVERIFIED per release).
var fakeBuilds = map[string]string{
	"v1.11.0": "6d32193 (Build: 30-Nov-2025)",
	"v1.12.0": "e738a74 (Build: 29-Jan-2026)",
	"v1.13.0": "295f67d (Build: 15-Feb-2026)",
	"v1.14.0": "9f1a3ea (Build: 06-Mar-2026)",
	"v1.14.1": "467959c (Build: 20-Mar-2026)",
	"v1.15.0": "dee3e26 (Build: 19-Apr-2026)",
	"v1.16.0": "07a3ca9 (Build: 06-Jun-2026)",
	"v1.17.0": "727fc05 (Build: 09-Aug-2026)",
	"v1.17.1": "d929643 (Build: 14-Aug-2026)",
	"v1.18.0": "81dbae5 (Build: 15-Oct-2026)",
}

// NewFakeNode makes a fake node with a new node's settings: role RoleRepeater or RoleRoomServer, version as
// "v1.18.0" (a release the catalog offers gets its real commit and date) or a whole ver reply. A room server's
// guest password is "hello", as builds that define ROOM_PASSWORD have it (7.6).
func NewFakeNode(role, version string) *FakeNode {
	raw := version
	if b, ok := fakeBuilds[version]; ok {
		raw = version + "-" + b
	}
	v, err := ParseVersion(raw)
	if err != nil {
		panic(err)
	}
	n := &FakeNode{
		Role:           role,
		Version:        v,
		Board:          "Heltec V3",
		PublicKey:      strings.Repeat("A1B2C3D4", 8),
		Name:           "Heltec Repeater",
		Radio:          DefaultRadio,
		TxPower:        22,
		AdvertInterval: 1,
		AdminPassword:  "password",
		Clock:          1715770351, // 15 May 2024, where a board without an RTC starts (ArduinoHelpers.h:11)
	}
	if role == RoleRoomServer {
		n.Name = "Heltec Room"
		n.GuestPassword = "hello"
	}
	n.Radio = n.radio(n.Radio)
	n.ActiveRadio = n.Radio
	return n
}

// Commands is every line the node has handled, in order (passwords included: this is the wire).
func (n *FakeNode) Commands() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.commands...)
}

// Reboots is how many times the node has rebooted.
func (n *FakeNode) Reboots() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.reboots
}

// Lines is the port opening (DTR up) or closing. Opening after a reboot boots the node: its stored radio becomes
// the one it runs, and with Banner set it prints its ID.
func (n *FakeNode) Lines(dtr, rts bool, baud int) []byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !dtr {
		n.open = false
		return nil
	}
	if n.open {
		return nil
	}
	n.open = true
	if n.rebooting {
		n.rebooting, n.booted, n.hung, n.wedged, n.RegionLoad = false, false, false, false, false
		n.pending, n.line = nil, nil
		n.ActiveRadio = n.Radio
	}
	if n.Banner && !n.booted && !n.Silent {
		n.booted = true
		id := "Repeater ID: "
		if n.Role == RoleRoomServer {
			id = "Room ID: "
		}
		return []byte(id + n.PublicKey + "\r\n")
	}
	n.booted = true
	return nil
}

// Receive is the firmware's loop reading what the host wrote: echo and line handling per role
// (simple_repeater/main.cpp and simple_room_server/main.cpp).
func (n *FakeNode) Receive(b []byte) []byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Silent || n.rebooting || n.wedged {
		return nil
	}
	n.pending = append(n.pending, b...)
	var out []byte
	if !n.noiseSent && len(n.Noise) > 0 {
		n.noiseSent = true
		out = append(out, n.Noise...)
	}
	for len(n.pending) > 0 && !n.rebooting && !n.wedged {
		if n.Role == RoleRoomServer {
			out = n.roomPass(out)
		} else {
			out = n.repeaterPass(out)
		}
	}
	if n.rebooting || n.wedged {
		n.pending = nil
	}
	return out
}

// repeaterPass is one pass of the repeater's loop (v1.10.0+): every byte but LF is kept and echoed, reading stops
// at CR, and a whole line gets "\n" and then its reply. The buffer holds 159 bytes; when it fills, v1.18.0 cuts the
// line to 158 and handles it, and older versions never handle it and read past the buffer (undefined behaviour,
// modelled as a node that stops answering).
func (n *FakeNode) repeaterPass(out []byte) []byte {
	const size = 160
	for len(n.pending) > 0 && len(n.line) < size-1 {
		c := n.pending[0]
		n.pending = n.pending[1:]
		if c != '\n' {
			n.line = append(n.line, c)
			out = append(out, c)
		}
		if c == '\r' {
			break
		}
	}
	if len(n.line) == size-1 {
		if !n.Version.AtLeast(1, 18, 0) {
			n.wedged = true
			return out
		}
		n.line[size-2] = '\r'
		n.line = n.line[:size-1]
	}
	if k := len(n.line); k > 0 && n.line[k-1] == '\r' {
		out = append(out, '\n')
		cmd := string(n.line[:k-1])
		n.line = nil
		out = n.reply(out, cmd)
	}
	return out
}

// roomPass is one pass of the room server's loop: every byte is echoed, LF included, every byte but LF is kept, and
// reading doesn't stop at CR, so "a\rb\r" written at once is one command "a\rb". The buffer holds 152 bytes.
func (n *FakeNode) roomPass(out []byte) []byte {
	const size = 152 // MAX_POST_TEXT_LEN+1 (simple_room_server/MyMesh.h:84)
	for len(n.pending) > 0 && len(n.line) < size-1 {
		c := n.pending[0]
		n.pending = n.pending[1:]
		if c != '\n' {
			n.line = append(n.line, c)
		}
		out = append(out, c)
	}
	if len(n.line) == size-1 {
		if !n.Version.AtLeast(1, 18, 0) {
			n.wedged = true
			return out
		}
		n.line[size-2] = '\r'
	}
	if k := len(n.line); k > 0 && n.line[k-1] == '\r' {
		cmd := string(n.line[:k-1])
		n.line = nil
		out = n.reply(out, cmd)
	}
	return out
}

// reply handles one command line and appends its reply line, if it has one.
func (n *FakeNode) reply(out []byte, cmd string) []byte {
	n.commands = append(n.commands, cmd)
	r := n.handle(cmd)
	if r == "" {
		return out
	}
	return append(out, replyMark+r+"\r\n"...)
}

// handle is MyMesh::handleCommand: region-load mode, then the optional "xx|" tag, then the CLI.
func (n *FakeNode) handle(cmd string) string {
	if n.RegionLoad {
		// simple_repeater/MyMesh.cpp:1199-1231: a blank line ends the mode; any other line is a region (or
		// ignored without an indent) and gets no reply.
		if strings.Trim(cmd, " ") == "" {
			n.RegionLoad = false
			return "OK - loaded 0 regions"
		}
		return ""
	}
	cmd = strings.TrimLeft(cmd, " ")
	if n.hung || (n.Hang != "" && strings.HasPrefix(cmd, n.Hang)) {
		n.hung = true
		return ""
	}
	tag := ""
	if len(cmd) > 4 && cmd[2] == '|' {
		tag, cmd = cmd[:3], cmd[3:]
	}
	r := n.cli(cmd)
	if r == "" && tag == "" {
		return ""
	}
	return tag + r
}

// cli is CommonCLI::handleCommand, for the commands setup and probing use, in its order (prefix matches by memcmp).
func (n *FakeNode) cli(cmd string) string {
	v := n.Version
	if v.AtLeast(1, 18, 0) {
		if r, ok := n.radioPrefs(cmd); ok {
			return r
		}
	}
	has := func(p string) bool { return strings.HasPrefix(cmd, p) }
	switch {
	case has("poweroff"), has("shutdown"), has("reboot"):
		n.reboot()
		return ""
	case has("clkreboot"):
		n.Clock = 1715770351
		n.reboot()
		return ""
	case has("advert.zerohop") && (len(cmd) == 14 || cmd[14] == ' ') && v.AtLeast(1, 14, 0):
		return "OK - zerohop advert sent"
	case has("advert"):
		return "OK - Advert sent"
	case has("clock sync"):
		return "ERR: clock cannot go backwards" // the sender timestamp is 0 on serial
	case has("clock"):
		return clockText(n.Clock)
	case has("time "):
		secs := int64(cAtoiDigits(cmd[5:]))
		if secs > n.Clock {
			n.Clock = secs
			return "OK - clock set: " + clockText(n.Clock)
		}
		return "(ERR: clock cannot go backwards)"
	case has("password "):
		n.AdminPassword = truncate(n.stored("password", cmd[9:]), 16)
		n.save()
		return "password now: " + n.AdminPassword
	case has("get "):
		return n.get(cmd[4:])
	case has("set "):
		return n.set(cmd[4:])
	case has("ver"):
		return v.Raw
	case has("board"):
		if (n.Role == RoleRepeater && v.AtLeast(1, 9, 1)) || (n.Role == RoleRoomServer && v.AtLeast(1, 10, 0)) {
			return n.Board
		}
	case has("region"):
		if f := strings.Fields(cmd); len(f) >= 2 && f[1] == "load" {
			n.RegionLoad = true
			return ""
		}
		return "*"
	}
	return "Unknown command"
}

// radioPrefs is v1.18.0's CommonRadioPrefs::handleCommand, which comes first and matches its get keys exactly
// (CommonRadioPrefs.cpp:32-189): "get radio " with a trailing space falls through to "??: radio ".
func (n *FakeNode) radioPrefs(cmd string) (string, bool) {
	switch {
	case cmd == "get radio":
		return n.radioText(), true
	case strings.HasPrefix(cmd, "set radio "):
		return n.setRadio(cmd[10:]), true
	case cmd == "get freq":
		return "> " + ftoa(float32(n.Radio.FreqMHz)), true
	case strings.HasPrefix(cmd, "get tx") && (len(cmd) == 6 || cmd[6] == ' '):
		return "> " + strconv.Itoa(n.txText()), true
	case strings.HasPrefix(cmd, "set tx "):
		n.TxPower = cAtoi(n.stored("tx", cmd[7:]))
		n.save()
		return "OK", true
	case cmd == "get path.hash.mode":
		return "> " + strconv.Itoa(n.PathHashMode), true
	case strings.HasPrefix(cmd, "set path.hash.mode "):
		return n.setPathHash(cmd[19:]), true
	}
	return "", false
}

// get is CommonCLI::handleGetCmd for the keys setup reads, in its order, by prefix.
func (n *FakeNode) get(key string) string {
	v := n.Version
	has := func(p string) bool { return strings.HasPrefix(key, p) }
	switch {
	case has("advert.interval"):
		return "> " + strconv.Itoa(n.AdvertInterval*2)
	case has("guest.password"):
		return "> " + n.GuestPassword
	case has("name"):
		return "> " + n.Name
	case has("lat"):
		return "> " + ftoa(float32(n.Lat))
	case has("lon"):
		return "> " + ftoa(float32(n.Lon))
	}
	if !v.AtLeast(1, 18, 0) { // from v1.18.0 these are CommonRadioPrefs's, matched exactly
		switch {
		case has("radio.rxgain") && v.AtLeast(1, 14, 1):
			// Its own key ahead of "radio" from v1.14.1 (git show repeater-v1.17.1:src/helpers/CommonCLI.cpp:845);
			// v1.14.0 answers it with the radio, by prefix (docs/meshcore-flashing.md 7.9).
			return "> off"
		case has("radio"):
			return n.radioText()
		case has("path.hash.mode") && v.AtLeast(1, 14, 0):
			return "> " + strconv.Itoa(n.PathHashMode)
		case has("tx") && (len(key) == 2 || key[2] == ' '):
			return "> " + strconv.Itoa(n.txText())
		case has("freq"):
			return "> " + ftoa(float32(n.Radio.FreqMHz))
		}
	}
	switch {
	case has("public.key"):
		return "> " + n.PublicKey
	case has("role"):
		return "> " + n.Role
	}
	return "??: " + key
}

// set is CommonCLI::handleSetCmd for the keys setup sends, in its order, by prefix.
func (n *FakeNode) set(cfg string) string {
	v := n.Version
	has := func(p string) bool { return strings.HasPrefix(cfg, p) }
	switch {
	case has("advert.interval "):
		mins := cAtoiDigits(n.stored("advert.interval", cfg[16:]))
		if (mins > 0 && mins < 60) || mins > 240 {
			return "Error: interval range is 60-240 minutes"
		}
		n.AdvertInterval = mins / 2
		n.save()
		return "OK"
	case has("guest.password "):
		n.GuestPassword = truncate(n.stored("guest.password", cfg[15:]), 16)
		n.save()
		return "OK"
	case has("name "):
		name := n.stored("name", cfg[5:])
		bad := ""
		switch {
		case v.AtLeast(1, 13, 0):
			bad = `[]\:,?*`
		case v.AtLeast(1, 12, 0):
			bad = `[]/\:,?*`
		}
		if bad != "" && strings.ContainsAny(name, bad) {
			return "Error, bad chars"
		}
		n.Name = truncate(name, 32)
		n.save()
		return "OK"
	case has("lat "):
		n.Lat = cAtof(n.stored("lat", cfg[4:]))
		n.save()
		return "OK"
	case has("lon "):
		n.Lon = cAtof(n.stored("lon", cfg[4:]))
		n.save()
		return "OK"
	}
	if !v.AtLeast(1, 18, 0) {
		switch {
		case has("radio "):
			return n.setRadio(cfg[6:])
		case has("path.hash.mode ") && v.AtLeast(1, 14, 0):
			return n.setPathHash(cfg[15:])
		case has("tx "):
			n.TxPower = cAtoi(n.stored("tx", cfg[3:]))
			n.save()
			return "OK"
		}
	}
	return "unknown config: " + cfg
}

// setRadio is set radio: four comma-separated parts, a missing one counting as 0, checked against the version's
// ranges (CommonRadioPrefs.cpp:39-58; before v1.15.0 the floor was 300 MHz, before v1.11.0 SF 7).
func (n *FakeNode) setRadio(arg string) string {
	arg = n.stored("radio", arg)
	parts := strings.SplitN(arg, ",", 4)
	if len(parts) == 4 {
		if i := strings.IndexByte(parts[3], ','); i >= 0 {
			parts[3] = parts[3][:i]
		}
	}
	part := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	freq, bw := cStrtof(part(0)), cStrtof(part(1))
	sf, cr := cAtoi(part(2))&0xff, cAtoi(part(3))&0xff
	floor, minSF := float32(150), 5
	if !n.Version.AtLeast(1, 15, 0) {
		floor = 300
	}
	if !n.Version.AtLeast(1, 11, 0) {
		minSF = 7
	}
	if freq >= floor && freq <= 2500 && sf >= minSF && sf <= 12 && cr >= 5 && cr <= 8 && bw >= 7 && bw <= 500 {
		n.Radio = Radio{FreqMHz: float64(freq), BWkHz: float64(bw), SF: sf, CR: cr}
		if n.Version.AtLeast(1, 18, 0) {
			n.save() // before v1.18.0 set radio saves without the advert-interval rule
		}
		return "OK - reboot to apply"
	}
	return "Error, invalid radio params"
}

func (n *FakeNode) setPathHash(arg string) string {
	mode := cAtoi(n.stored("path.hash.mode", arg)) & 0xff // uint8_t mode = atoi(config)
	if mode < 3 {
		n.PathHashMode = mode
		n.save()
		return "OK"
	}
	return "Error, must be 0,1, or 2"
}

// radioText is get radio's reply; the bandwidth through ftoa3 from v1.11.0 ("250"), ftoa before ("250.0").
func (n *FakeNode) radioText() string {
	bw := ftoa3(float32(n.Radio.BWkHz))
	if !n.Version.AtLeast(1, 11, 0) {
		bw = ftoa(float32(n.Radio.BWkHz))
	}
	return fmt.Sprintf("> %s,%s,%d,%d", ftoa(float32(n.Radio.FreqMHz)), bw, n.Radio.SF, n.Radio.CR)
}

// txText is how get tx prints the setting (7.4): v1.11.0 and v1.12.0 keep a uint8, v1.13.0 to v1.17.1 an int8
// printed as a signed number, v1.18.0 an int8 read back through a uint8 getter.
func (n *FakeNode) txText() int {
	if n.Version.AtLeast(1, 13, 0) && !n.Version.AtLeast(1, 18, 0) {
		return int(int8(n.TxPower))
	}
	return int(uint8(n.TxPower))
}

// radio is r as the node keeps it: float32 frequency and bandwidth.
func (n *FakeNode) radio(r Radio) Radio {
	r.FreqMHz, r.BWkHz = float64(float32(r.FreqMHz)), float64(float32(r.BWkHz))
	return r
}

// stored is the value the node keeps for key: what it was told, unless Store says otherwise.
func (n *FakeNode) stored(key, value string) string {
	if s, ok := n.Store[key]; ok {
		return s
	}
	return value
}

// save is CommonCLI::savePrefs: the first save turns off a new node's 2-minute local advert (CommonCLI.cpp:160-168).
func (n *FakeNode) save() {
	if n.AdvertInterval*2 < 60 {
		n.AdvertInterval = 0
	}
}

// reboot restarts the node: no reply, and nothing more until the port is opened again.
func (n *FakeNode) reboot() {
	n.rebooting = true
	n.reboots++
}

// clockText is how the CLI prints a time: "HH:MM - D/M/YYYY UTC".
func clockText(unix int64) string {
	t := time.Unix(unix, 0).UTC()
	return fmt.Sprintf("%02d:%02d - %d/%d/%d UTC", t.Hour(), t.Minute(), t.Day(), int(t.Month()), t.Year())
}

// truncate is StrHelper::strncpy into a buffer of size bytes: at most size-1 bytes kept.
func truncate(s string, size int) string {
	if len(s) > size-1 {
		return s[:size-1]
	}
	return s
}

// cAtoi is C's atoi: leading spaces, a sign, digits, then anything; 0 when there are no digits.
func cAtoi(s string) int {
	s = strings.TrimLeft(s, " \t")
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	v := cAtoiDigits(s)
	if neg {
		return -v
	}
	return v
}

// cAtoiDigits is CommonCLI's _atoi: digits only, from the start.
func cAtoiDigits(s string) int {
	v := 0
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9' && v < 1<<40; i++ {
		v = v*10 + int(s[i]-'0')
	}
	return v
}

// cStrtof is strtof: the number at the start, rounded once, straight to a float32.
func cStrtof(s string) float32 {
	f, err := strconv.ParseFloat(cNumber(s), 32)
	if err != nil {
		return 0
	}
	return float32(f)
}

// cAtof is C's atof (strtod) for plain decimals: the longest number at the start, 0 when there is none.
func cAtof(s string) float64 {
	f, err := strconv.ParseFloat(cNumber(s), 64)
	if err != nil {
		return 0
	}
	return f
}

// cNumber is the plain decimal at the start of s, as strtod reads it ("62.5.0" gives "62.5").
func cNumber(s string) string {
	s = strings.TrimLeft(s, " \t")
	end := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' || ((c == '-' || c == '+') && i == 0) {
			if c == '.' && strings.Contains(s[:i], ".") {
				break
			}
			end = i + 1
			continue
		}
		break
	}
	return strings.TrimRight(s[:end], ".")
}
