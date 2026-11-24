package nodecli

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Secret is a password. It goes to the node and nowhere else: fmt prints it as •••• (whatever the verb, inside
// structs too), and JSON encodes it as "", so a log line, an error or a saved draft can't carry it by mistake
// (CLAUDE.md: never store or log a password). string(s) is the password itself, for the wire only.
type Secret string

// Mask is how every password is shown.
const Mask = "••••"

// String is Mask, or "" for no password.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return Mask
}

// Format prints the mask for every verb, so %v, %s, %q, %x and %#v never show the password.
func (s Secret) Format(f fmt.State, _ rune) { io.WriteString(f, s.String()) }

// MarshalJSON writes "": a password is never saved.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`""`), nil }

// Radio is a LoRa radio setting as `set radio` takes it.
type Radio struct {
	// FreqMHz is the frequency in MHz, 150–2500 (300–2500 before v1.15.0).
	FreqMHz float64
	// BWkHz is the bandwidth in kHz, one of Bandwidths.
	BWkHz float64
	SF    int // spreading factor, 5–12
	CR    int // coding rate 5–8, meaning 4/5–4/8
}

// DefaultRadio is the firmware's own setting when nothing has been saved: 869.618 MHz, 62.5 kHz, SF8, CR5
// (MeshCore platformio.ini:29-31, simple_repeater/MyMesh.cpp:15-16, docs/cli_commands.md:214).
var DefaultRadio = Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 5}

// Bandwidths are the only bandwidths (kHz) the radios take. The CLI accepts any value from 7 to 500, but at boot
// RadioLib maps it to these and rejects the rest, and MeshCore ignores the error, so the radio never comes up on the
// new setting (RadioLib SX126x_config.cpp:44-87; docs/meshcore-flashing.md 7.6). The strings are how setup sends
// them.
var Bandwidths = []struct {
	KHz  float64
	Text string
}{
	{7.8, "7.8"}, {10.4, "10.4"}, {15.6, "15.6"}, {20.8, "20.8"}, {31.25, "31.25"},
	{41.7, "41.7"}, {62.5, "62.5"}, {125, "125"}, {250, "250"}, {500, "500"},
}

// bandwidth finds bw in Bandwidths.
func bandwidth(bw float64) (string, bool) {
	for _, b := range Bandwidths {
		if math.Abs(bw-b.KHz) < 0.001 {
			return b.Text, true
		}
	}
	return "", false
}

// freqText is the shortest decimal that is the same float32 as f: the node keeps the frequency as a float32, so this
// is f exactly as the node will store it, written cleanly ("869.618", not "869.6179809" read back from a node).
func freqText(f float64) string { return strconv.FormatFloat(f, 'f', -1, 32) }

// Arg is the argument of `set radio`: "869.618,62.5,8,5". The bandwidth is written as the list writes it (the config
// tool sends "62.5.0", harmless only because strtof stops at the second dot, 7.9).
func (r Radio) Arg() string {
	bw, ok := bandwidth(r.BWkHz)
	if !ok {
		bw = strconv.FormatFloat(r.BWkHz, 'f', -1, 32)
	}
	return fmt.Sprintf("%s,%s,%d,%d", freqText(r.FreqMHz), bw, r.SF, r.CR)
}

// Display is the radio for the screen: "869.618 MHz, 62.5 kHz, SF8, CR5".
func (r Radio) Display() string {
	bw, ok := bandwidth(r.BWkHz)
	if !ok {
		bw = strconv.FormatFloat(r.BWkHz, 'f', -1, 32)
	}
	return fmt.Sprintf("%s MHz, %s kHz, SF%d, CR%d", freqText(r.FreqMHz), bw, r.SF, r.CR)
}

// Near says whether two settings are the same radio, comparing numbers as the node's float32 printing allows: the
// node prints 869.618 as 869.6179809 (docs/meshcore-flashing.md 7.4).
func (r Radio) Near(o Radio) bool {
	return math.Abs(r.FreqMHz-o.FreqMHz) < 0.0005 && math.Abs(r.BWkHz-o.BWkHz) < 0.01 && r.SF == o.SF && r.CR == o.CR
}

// Clean is r with the frequency written as the node stores it (869.6179809 becomes 869.618) and the bandwidth
// snapped to the list when it is within 0.01 kHz of a value there.
func (r Radio) Clean() Radio {
	if f, err := strconv.ParseFloat(freqText(r.FreqMHz), 64); err == nil {
		r.FreqMHz = f
	}
	for _, b := range Bandwidths {
		if math.Abs(r.BWkHz-b.KHz) < 0.01 {
			r.BWkHz = b.KHz
		}
	}
	return r
}

// parseRadioReply reads `get radio`'s reply: "> 869.6179809,62.5,8,5".
func parseRadioReply(reply string) (Radio, error) {
	v, ok := strings.CutPrefix(reply, "> ")
	parts := strings.Split(strings.TrimSpace(v), ",")
	if !ok || len(parts) != 4 {
		return Radio{}, fmt.Errorf("nodecli: %q isn't a radio setting", reply)
	}
	var r Radio
	var errs [4]error
	r.FreqMHz, errs[0] = strconv.ParseFloat(parts[0], 64)
	r.BWkHz, errs[1] = strconv.ParseFloat(parts[1], 64)
	r.SF, errs[2] = strconv.Atoi(parts[2])
	r.CR, errs[3] = strconv.Atoi(parts[3])
	for _, err := range errs {
		if err != nil {
			return Radio{}, fmt.Errorf("nodecli: %q isn't a radio setting", reply)
		}
	}
	return r, nil
}

// Settings are what setup sends to a repeater or room server (SPEC.md 4, the Setup form).
type Settings struct {
	// Name is the node's name in adverts: 1–23 bytes, none of [ ] / \ : , ? *, no space at either end.
	Name string
	// Radio takes effect at the reboot that ends setup.
	Radio Radio
	// TxDBm is the radio chip's TX power setting, at most the board's limit (TxLimit).
	TxDBm int
	// PathHashBytes is the size of the path hash in the node's adverts, 1–3 bytes (sent as path.hash.mode 0–2), or
	// 0 to leave it. Firmware before v1.14.0 has no such setting, and then it isn't sent.
	PathHashBytes int
	// Lat and Lon are the node's location in degrees; nil sends nothing.
	Lat, Lon *float64
	// AdvertInterval is the local (zero-hop) advert interval in minutes: 0 for off, or 60–240 in even minutes (the
	// node keeps it in 2-minute steps). nil sends nothing, but the first setting saved on a new node turns its
	// 2-minute local advert off anyway (7.3).
	AdvertInterval *int
	// AdminPassword is required: 1–15 bytes, no space at either end. An empty one would let anyone log in as admin
	// over LoRa (7.6).
	AdminPassword Secret
	// GuestPassword is required on a room server, whose build may default it to "hello" or blank (7.6), and sent on
	// a repeater only when given: 1–15 bytes, no space at either end.
	GuestPassword Secret
	// ClockSynced says the Zero's clock is synchronised (NTP), so setup may set the node's clock from it. Without it
	// the clock isn't touched: on a board with an RTC a wrong time in the future survives reboots (7.6).
	ClockSynced bool
	// Device is the catalog's name for the board ("UnitEng Station G2"), when the owner picked one; with Info.Board
	// it sets the TX power limit. It isn't sent.
	Device string
}

// FieldError is a problem with one field of the form, in plain English, with the fix.
type FieldError struct {
	// Field is the form row: "Name", "Radio", "TX power", "Path hash", "Latitude", "Longitude", "Local advert",
	// "Admin password" or "Guest password".
	Field string
	// Problem says what's wrong and what to do; it never holds a password.
	Problem string
}

// Error is the field and the problem: "Name: can't contain "/": …".
func (e *FieldError) Error() string { return e.Field + ": " + e.Problem }

// Problems is every field with a problem, in form order. Plan returns it as its error.
type Problems []*FieldError

// Error is every problem, joined with "; ".
func (p Problems) Error() string {
	var b strings.Builder
	for i, e := range p {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

// Limits from the firmware and the research (docs/meshcore-flashing.md 7.3, 7.6, 7.7).
const (
	// MaxName is the longest name, in bytes: an advert has room for 23 with the location, which repeaters and room
	// servers always send (docs/cli_commands.md:324; 7.6).
	MaxName = 23
	// MaxPassword is the longest password, in bytes: the node keeps 15 and silently drops the rest.
	MaxPassword = 15
	// MinTxDBm is the lowest TX power an SX1262 takes (RadioLib SX1262.cpp:208-214).
	MinTxDBm = -9
	// DefaultMaxTxDBm is the TX power limit of a board without an external amplifier: the most an SX1262 takes.
	DefaultMaxTxDBm = 22
	// minAdvertInterval and maxAdvertInterval are the local advert interval's range in minutes, besides 0 for off
	// (MIN_LOCAL_ADVERT_INTERVAL and the 240 of CommonCLI.cpp:503-512).
	minAdvertInterval, maxAdvertInterval = 60, 240
)

// badNameChars are refused in names: v1.12.0 refuses all of them and answers "Error, bad chars", later versions all
// but "/" (CommonCLI.cpp:22-28); setup refuses them on every version (7.5).
const badNameChars = `[]/\:,?*`

// CheckName checks a node name.
func CheckName(name string) error {
	switch {
	case name == "":
		return &FieldError{"Name", fmt.Sprintf("type a name, 1 to %d bytes.", MaxName)}
	case !utf8.ValidString(name):
		return &FieldError{"Name", "has characters that aren't valid text; type it again."}
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return &FieldError{"Name", "has a control character (a line break or a tab); remove it."}
	case strings.TrimSpace(name) != name:
		return &FieldError{"Name", "starts or ends with a space, which the node would keep; remove it."}
	}
	if i := strings.IndexAny(name, badNameChars); i >= 0 {
		return &FieldError{"Name", fmt.Sprintf("can't contain %q: MeshCore refuses [ ] / \\ : , ? * in names. Remove it.", name[i:i+1])}
	}
	if len(name) > MaxName {
		return &FieldError{"Name", fmt.Sprintf("is %d bytes; an advert has room for %d. Shorten it.", len(name), MaxName)}
	}
	return nil
}

// checkPassword checks a password; field names it, and required says whether it may be empty. The problems never
// quote the password, nor its length.
func checkPassword(field string, pw Secret, required bool, whyRequired string) error {
	s := string(pw)
	switch {
	case s == "" && required:
		return &FieldError{field, fmt.Sprintf("type one, 1 to %d bytes. %s", MaxPassword, whyRequired)}
	case s == "":
		return nil
	case !utf8.ValidString(s):
		return &FieldError{field, "has characters that aren't valid text; type it again."}
	case strings.IndexFunc(s, unicode.IsControl) >= 0:
		return &FieldError{field, "has a control character (a line break or a tab); remove it."}
	case strings.TrimSpace(s) != s:
		return &FieldError{field, "starts or ends with a space, which the node would keep; remove it."}
	case len(s) > MaxPassword:
		return &FieldError{field, fmt.Sprintf("is too long: the node keeps only %d bytes. Shorten it.", MaxPassword)}
	}
	return nil
}

// CheckRadio checks a radio setting against what the firmware version and the radios accept.
func CheckRadio(r Radio, v Version) error {
	floor := 150.0
	if !v.AtLeast(1, 15, 0) {
		floor = 300 // set radio's floor before v1.15.0 (7.5)
	}
	f := r.FreqMHz
	switch {
	case math.IsNaN(f) || math.IsInf(f, 0) || f < floor || f > 2500:
		why := ""
		if floor == 300 {
			why = fmt.Sprintf(" (firmware %s; 150 MHz needs v1.15.0)", v)
		}
		return &FieldError{"Radio", fmt.Sprintf("the frequency must be %g to 2500 MHz%s.", floor, why)}
	}
	if _, ok := bandwidth(r.BWkHz); !ok {
		return &FieldError{"Radio", "the bandwidth must be one the radio takes: 7.8, 10.4, 15.6, 20.8, 31.25, 41.7, 62.5, 125, 250 or 500 kHz."}
	}
	if r.SF < 5 || r.SF > 12 {
		return &FieldError{"Radio", fmt.Sprintf("the spreading factor must be 5 to 12, not %d.", r.SF)}
	}
	if r.CR < 5 || r.CR > 8 {
		return &FieldError{"Radio", fmt.Sprintf("the coding rate must be 5 to 8 (4/5 to 4/8), not %d.", r.CR)}
	}
	return nil
}

// dBmRE finds the module's output named in an Ikoka board string ("Ikoka Stick-E22-30dBm (Xiao_nrf52)"), and
// e22RE the E22 module names the FAQ uses ("E22-900M30S").
var (
	dBmRE = regexp.MustCompile(`(?i)(\d+)\s*dBm`)
	e22RE = regexp.MustCompile(`(?i)E22-\d+M(\d\d)S`)
)

// TxLimit is the highest TX power setting (dBm) setup sends to a board, and why, from the catalog's name for the
// board (device, may be empty) and what the firmware's `board` command said (board, may be empty). Boards with an
// external amplifier need a low chip setting, and "incorrect power settings can permanently damage your radio
// hardware" (MeshCore FAQ 7.7, docs/meshcore-flashing.md 7.7):
//
//   - an E22-900M33S (2 W) module, which the Ikoka board string names as "33dBm": 9;
//   - an E22-900M30S (1 W) module ("30dBm"), or a Station G2 (its PA burns out above 19:
//     variants/station_g2/platformio.ini:21): 19;
//   - an Ikoka board whose module the strings don't name: 9, the lowest of its modules' limits;
//   - a Heltec V4: 22, its high-output setting;
//   - anything else: 22, the most an SX1262 takes.
func TxLimit(device, board string) (int, string) {
	lower := strings.ToLower(device + " " + board)
	module := ""
	if m := e22RE.FindStringSubmatch(device + " " + board); m != nil {
		module = m[1]
	} else if m := dBmRE.FindStringSubmatch(board); m != nil && strings.Contains(strings.ToLower(board), "e22") {
		module = m[1]
	}
	switch module {
	case "33":
		return 9, "its E22 2 W (33 dBm) amplifier module must not be set above 9 (MeshCore FAQ 7.7)"
	case "30":
		return 19, "its E22 1 W (30 dBm) amplifier module must not be set above 19 (MeshCore FAQ 7.7)"
	}
	switch {
	case strings.Contains(lower, "station g2"):
		return 19, "the Station G2's amplifier burns out above 19 (MeshCore FAQ 7.7)"
	case strings.Contains(lower, "ikoka") && module == "":
		return 9, "an Ikoka board's amplifier module isn't known, so the 2 W module's limit applies (MeshCore FAQ 7.7)"
	case strings.Contains(lower, "heltec v4"):
		return 22, "22 is the Heltec V4's high-output setting (MeshCore FAQ 7.7)"
	}
	return DefaultMaxTxDBm, "22 is the most its radio chip takes"
}

// CheckTxPower checks a TX power setting against the board's limit (TxLimit).
func CheckTxPower(dbm int, device, board string) error {
	limit, why := TxLimit(device, board)
	switch {
	case dbm > limit:
		return &FieldError{"TX power", fmt.Sprintf("%d dBm is above this board's limit: %s. Choose %d or less.", dbm, why, limit)}
	case dbm < MinTxDBm:
		return &FieldError{"TX power", fmt.Sprintf("%d dBm is below %d, the lowest the radio takes.", dbm, MinTxDBm)}
	}
	return nil
}

// Check returns every problem with s for the node described by info, one per field in form order, or nil.
func Check(s Settings, info Info) Problems {
	var p Problems
	add := func(err error) {
		if fe, ok := err.(*FieldError); ok {
			p = append(p, fe)
		}
	}
	add(CheckName(s.Name))
	add(CheckRadio(s.Radio, info.Version))
	add(CheckTxPower(s.TxDBm, s.Device, info.Board))
	if s.PathHashBytes != 0 && (s.PathHashBytes < 1 || s.PathHashBytes > 3) {
		add(&FieldError{"Path hash", "choose 1, 2 or 3 bytes."})
	}
	checkDeg := func(field string, x *float64, lim float64) {
		if x != nil && (math.IsNaN(*x) || math.IsInf(*x, 0) || *x < -lim || *x > lim) {
			add(&FieldError{field, fmt.Sprintf("must be −%g to %g degrees.", lim, lim)})
		}
	}
	checkDeg("Latitude", s.Lat, 90)
	checkDeg("Longitude", s.Lon, 180)
	if a := s.AdvertInterval; a != nil {
		switch {
		case *a == 0:
		case *a < minAdvertInterval || *a > maxAdvertInterval:
			add(&FieldError{"Local advert", "choose off, or every 60 to 240 minutes."})
		case *a%2 != 0:
			add(&FieldError{"Local advert", fmt.Sprintf("the node counts in 2-minute steps: choose %d or %d minutes.", *a-1, *a+1)})
		}
	}
	add(checkPassword("Admin password", s.AdminPassword, true, "Without one anyone can log in as admin over LoRa."))
	add(checkPassword("Guest password", s.GuestPassword, info.Role == RoleRoomServer, "A blank guest password lets anyone in."))
	return p
}
