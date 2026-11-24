package nodecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"meshflash/internal/serialport"
)

var (
	// ErrWrongReply means the node answered a command, but not with what it answers when the command works.
	ErrWrongReply = errors.New("nodecli: the node refused a setting")
	// ErrMismatch means a value read back isn't what was sent.
	ErrMismatch = errors.New("nodecli: the node kept something else")
)

// expectKind is how a step's reply is checked.
type expectKind int

const (
	expectSkip  expectKind = iota // nothing is sent: the step only tells the owner what was left out
	expectNone                    // no reply comes (reboot)
	expectExact                   // the reply is exactly text
	expectClock                   // "OK - clock set: …", or the harmless "(ERR: clock cannot go backwards)"
	expectRadio                   // "> 869.6179809,62.5,8,5", compared numerically with radio
	expectTx                      // "> n", compared modulo 256 with num (7.4)
	expectFloat                   // "> 51.5074005", within tol of num
)

// Expect is what a step's reply must be. Plan makes them; the zero Expect sends nothing.
type Expect struct {
	kind     expectKind
	text     string
	radio    Radio
	num, tol float64
	readBack bool   // a read-back: a wrong value is ErrMismatch, not ErrWrongReply
	secret   string // the password this step carries, never to be shown
}

// Format describes the check without the password it may hold.
func (e Expect) Format(f fmt.State, _ rune) {
	switch e.kind {
	case expectSkip:
		io.WriteString(f, "nothing sent")
	case expectNone:
		io.WriteString(f, "no reply")
	case expectExact:
		if e.secret != "" {
			io.WriteString(f, "a reply with the password")
		} else {
			fmt.Fprintf(f, "%q", e.text)
		}
	case expectClock:
		io.WriteString(f, "the clock set")
	case expectRadio:
		fmt.Fprintf(f, "radio %s", e.radio.Display())
	default:
		fmt.Fprintf(f, "%g", e.num)
	}
}

// Step is one command of a setup, and how the screen shows it.
type Step struct {
	// Command is what is sent, before its CR. For the clock step it is "time", and Run adds the Unix time when it
	// sends it, so the node gets the time of sending. It is empty for a step that only says what was left out.
	// For a Secret step it holds a password: show Display, never Command.
	Command string
	// Expect is the reply that means it worked.
	Expect Expect
	// Display is the step for the screen, with every password as ••••.
	Display string
	// Secret says Command and Expect hold a password.
	Secret bool
}

// Format prints Display, so a step logged by mistake never shows a password.
func (s Step) Format(f fmt.State, _ rune) { io.WriteString(f, s.Display) }

// MarshalJSON writes the step without its password. A Secret step's command keeps its command words and shows ••••
// for the password ("password ••••"), which may hold spaces of its own, so it is never cut at a space; a command of
// any other shape is shown as •••• whole.
func (s Step) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Command, Display string }{s.maskedCommand(), s.Display})
}

// secretCommands are the commands Plan puts a password after.
var secretCommands = []string{"password ", "set guest.password "}

// maskedCommand is Command with any password in it shown as Mask.
func (s Step) maskedCommand() string {
	if !s.Secret {
		return s.Command
	}
	pw := s.Expect.secret
	for _, p := range secretCommands {
		if pw != "" && s.Command == p+pw {
			return p + Mask
		}
	}
	if pw != "" && !strings.Contains(s.Command, pw) {
		return s.Command // a read-back ("get guest.password"): no password in it
	}
	return Mask
}

func exact(text string) Expect { return Expect{kind: expectExact, text: text} }

func readBack(text string) Expect { return Expect{kind: expectExact, text: text, readBack: true} }

// degText writes a latitude or longitude to 6 decimals (0.1 m) without trailing zeros.
func degText(x float64) string {
	x = math.Round(x*1e6) / 1e6
	if x == 0 {
		x = 0 // no "-0"
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}

// Plan is the setup of a node, in the order of docs/meshcore-flashing.md 7.10: the clock (only when the Zero's is
// synchronised), name, radio, TX power, path hash mode (v1.14.0+), location, local advert interval, guest password,
// admin password; then each value read back and compared; then reboot, which applies the radio. Every setting is
// saved by the node before it replies, so a setup cut short leaves what was sent so far, and running it again is
// safe (10.3). It returns Problems when the settings don't pass Check, and ErrUnsupported for a node setup can't
// configure. It sends only commands the CLI has in the node's version (7.5).
func Plan(s Settings, info Info) ([]Step, error) {
	if err := info.Settable(); err != nil {
		return nil, err
	}
	if p := Check(s, info); p != nil {
		return nil, p
	}
	var steps []Step
	add := func(st Step) { steps = append(steps, st) }
	skip := func(display string) { add(Step{Display: display}) }

	if s.ClockSynced {
		add(Step{Command: "time", Expect: Expect{kind: expectClock}, Display: "Clock: set to the Zero's time"})
	} else {
		skip("Clock: not set (the Zero's clock isn't synchronised)")
	}
	add(Step{Command: "set name " + s.Name, Expect: exact("OK"), Display: "Name: " + s.Name})
	add(Step{Command: "set radio " + s.Radio.Arg(), Expect: exact("OK - reboot to apply"), Display: "Radio: " + s.Radio.Display()})
	tx := strconv.Itoa(s.TxDBm)
	add(Step{Command: "set tx " + tx, Expect: exact("OK"), Display: "TX power: " + tx + " dBm"})
	pathHash := s.PathHashBytes != 0 && info.Version.AtLeast(1, 14, 0)
	mode := strconv.Itoa(s.PathHashBytes - 1)
	switch {
	case pathHash:
		add(Step{Command: "set path.hash.mode " + mode, Expect: exact("OK"), Display: fmt.Sprintf("Path hash: %d bytes", s.PathHashBytes)})
	case s.PathHashBytes != 0:
		// Older firmware would answer "unknown config: …" (7.5).
		skip("Path hash: not set (needs v1.14.0 or newer)")
	}
	if s.Lat != nil {
		add(Step{Command: "set lat " + degText(*s.Lat), Expect: exact("OK"), Display: "Latitude: " + degText(*s.Lat)})
	}
	if s.Lon != nil {
		add(Step{Command: "set lon " + degText(*s.Lon), Expect: exact("OK"), Display: "Longitude: " + degText(*s.Lon)})
	}
	if s.AdvertInterval != nil {
		d := "Local advert: off"
		if *s.AdvertInterval != 0 {
			d = fmt.Sprintf("Local advert: every %d minutes", *s.AdvertInterval)
		}
		add(Step{Command: "set advert.interval " + strconv.Itoa(*s.AdvertInterval), Expect: exact("OK"), Display: d})
	}
	guest := string(s.GuestPassword)
	if guest != "" {
		e := exact("OK")
		e.secret = guest
		add(Step{Command: "set guest.password " + guest, Expect: e, Display: "Guest password: " + Mask, Secret: true})
	}
	admin := string(s.AdminPassword)
	e := exact("password now: " + admin) // the node echoes what it stored; there is no read-back (7.3)
	e.secret = admin
	add(Step{Command: "password " + admin, Expect: e, Display: "Admin password: " + Mask, Secret: true})

	add(Step{Command: "get name", Expect: readBack("> " + s.Name), Display: "Check name"})
	add(Step{Command: "get radio", Expect: Expect{kind: expectRadio, radio: s.Radio, readBack: true}, Display: "Check radio"})
	add(Step{Command: "get tx", Expect: Expect{kind: expectTx, num: float64(s.TxDBm), readBack: true}, Display: "Check TX power"})
	if pathHash {
		add(Step{Command: "get path.hash.mode", Expect: readBack("> " + mode), Display: "Check path hash"})
	}
	if guest != "" {
		e := readBack("> " + guest)
		e.secret = guest
		add(Step{Command: "get guest.password", Expect: e, Display: "Check guest password", Secret: true})
	}
	for _, d := range []struct {
		key, name string
		x         *float64
	}{{"lat", "latitude", s.Lat}, {"lon", "longitude", s.Lon}} {
		if d.x != nil {
			x, _ := strconv.ParseFloat(degText(*d.x), 64)
			// The node prints its value through float32 with 7 decimals: within 2e-5 degrees at 180.
			add(Step{Command: "get " + d.key, Expect: Expect{kind: expectFloat, num: x, tol: 1e-4, readBack: true}, Display: "Check " + d.name})
		}
	}
	if s.AdvertInterval != nil {
		add(Step{Command: "get advert.interval", Expect: readBack("> " + strconv.Itoa(*s.AdvertInterval)), Display: "Check local advert"})
	}
	add(Step{Command: "reboot", Expect: Expect{kind: expectNone}, Display: "Reboot to start the new radio"})
	return steps, nil
}

// Want is what Confirm should find after the reboot that ends the plan for s and info.
func (s Settings) Want(info Info) Want { return Want{Version: info.Version, Radio: s.Radio} }

// Status is where a step is.
type Status int

const (
	Sending Status = iota // sent, waiting for the reply
	Done                  // the reply was right
	Skipped               // nothing was sent (see Display)
	Failed                // see Note; Run returns a *StepError
)

// String is the status as a word, for logs and tests.
func (s Status) String() string {
	switch s {
	case Sending:
		return "sending"
	case Done:
		return "ok"
	case Skipped:
		return "skipped"
	}
	return "failed"
}

// Progress is one report from Run.
type Progress struct {
	// Index is the step's place in the plan, from 0, of Total.
	Index, Total int
	// Display is the step's Display.
	Display string
	// Status is where the step is.
	Status Status
	// Note says why a step failed, or something worth knowing about one that worked. It never holds a password.
	Note string
}

// StepError is a setup stopped at a step.
type StepError struct {
	// Index is the step's place in the plan, from 0.
	Index int
	// Display is the step's Display.
	Display string
	// Reason is what went wrong, in plain English. It never holds a password.
	Reason string
	// State is what the node is left with, in plain English, for the screen: nothing changed, or what was sent
	// before this step kept (the node saves each setting before it answers: docs/meshcore-flashing.md 10.3), and
	// whether the new radio still waits for a restart. It never holds a password.
	State string
	// Err is ErrNoReply, ErrWrongReply, ErrMismatch, ErrTooLong, ErrBadCommand, a serialport error (ErrGone when
	// the board went away) or the context's.
	Err error
}

// Error is the step and the reason: "Check name: the node has "Hill" stored, not "Hilltop 2"."
func (e *StepError) Error() string { return e.Display + ": " + e.Reason }

// Unwrap gives Err, for errors.Is.
func (e *StepError) Unwrap() error { return e.Err }

// Run sends the plan's steps one at a time on an open port, each waiting for its reply (5 s, with the region-load
// escape when no reply has started), and stops at the first wrong reply with a *StepError naming the step and
// saying what the node is left with (StepError.State). progress, if not nil,
// hears each step as it is sent and when it is done. After the last step (reboot) the caller closes the port, waits
// for the board to come back, reopens it, and calls Confirm.
func Run(ctx context.Context, port serialport.Port, clock serialport.Clock, steps []Step, progress func(Progress)) error {
	c := newConn(ctx, port, clock)
	var secrets []string
	for _, st := range steps {
		if st.Expect.secret != "" {
			secrets = append(secrets, st.Expect.secret)
		}
	}
	report := func(i int, st Status, note string) {
		if progress != nil {
			progress(Progress{Index: i, Total: len(steps), Display: steps[i].Display, Status: st, Note: note})
		}
	}
	var changed, radio bool // a step that changes the node has worked; set radio has
	for i, st := range steps {
		if st.Expect.kind == expectSkip {
			report(i, Skipped, "")
			continue
		}
		report(i, Sending, "")
		writes := c.writes
		note, reason, err := c.run(st, secrets)
		if err != nil {
			report(i, Failed, reason)
			// A setting that reached the node and wasn't refused may have been saved (the node saves before it
			// answers), even when no answer came back.
			maybe := c.writes > writes && st.changes() && !errors.Is(err, ErrWrongReply)
			return &StepError{Index: i, Display: st.Display, Reason: reason, State: leftState(st, changed, radio, maybe), Err: err}
		}
		report(i, Done, note)
		if st.changes() {
			changed = true
			radio = radio || strings.HasPrefix(st.Command, "set radio ")
		}
	}
	return nil
}

// changes says whether a step changes the node: anything sent that isn't a read-back or the reboot.
func (s Step) changes() bool {
	k := s.Expect.kind
	return k != expectSkip && k != expectNone && !s.Expect.readBack
}

// leftState says what a setup stopped at st leaves on the node (StepError.State). changed and radio say whether an
// earlier step changed the node and whether one was set radio; maybe says st itself reached the node and may have
// been saved.
func leftState(st Step, changed, radio, maybe bool) string {
	radio = radio || maybe && strings.HasPrefix(st.Command, "set radio ")
	var s string
	switch {
	case changed && st.Expect.kind == expectNone:
		s = "Every setting was sent, saved and checked, but the node didn't get the reboot."
	case changed && st.Expect.readBack:
		s = "Every setting was sent to the node; run setup again to check them."
	case changed && maybe:
		s = "What was sent before this step is saved on the node, and this step's setting may be too; run setup again to finish."
	case changed:
		s = "What was sent before this step is saved on the node; run setup again to finish."
	case maybe:
		s = "This step's setting may have been saved on the node; nothing else was changed. Run setup again to finish."
	default:
		return "Nothing on the node was changed."
	}
	if radio {
		s += " It keeps its old radio until it restarts."
	}
	return s
}

// run sends one step and checks its reply. It returns a note for the owner, or the reason it failed and the error.
func (c *conn) run(st Step, secrets []string) (note, reason string, err error) {
	if st.Expect.kind == expectNone {
		c.buf = c.buf[:0]
		if err := c.send(st.Command); err != nil {
			return "", sendReason(err), err
		}
		return "", "", nil
	}
	cmd := st.Command
	if st.Expect.kind == expectClock {
		cmd = "time " + strconv.FormatInt(c.clock.Now().Unix(), 10)
	}
	reply, err := c.command(cmd)
	if err != nil {
		return "", sendReason(err), err
	}
	return st.Expect.check(reply, secrets)
}

// sendReason says in plain English why a command got no reply.
func sendReason(err error) string {
	switch {
	case errors.Is(err, ErrNoReply):
		return fmt.Sprintf("the node didn't answer within %d seconds.", int(ReplyTimeout.Seconds()))
	case errors.Is(err, ErrTooLong):
		return fmt.Sprintf("the command is longer than %d bytes, too long to send safely.", MaxCommand)
	case errors.Is(err, ErrBadCommand):
		return "the command holds a line break."
	case errors.Is(err, serialport.ErrGone):
		return "the board went away (unplugged, or it reset)."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "stopped."
	}
	return "the port failed: " + err.Error()
}

// quote shows a reply in a message, unless it holds a password.
func quote(reply string, secrets []string) (string, bool) {
	for _, s := range secrets {
		if strings.Contains(reply, s) {
			return "", false
		}
	}
	return strconv.Quote(reply), true
}

// check compares a reply with e. It returns a note when the reply is right, else the reason and the error.
func (e Expect) check(reply string, secrets []string) (note, reason string, err error) {
	bad := func(want string) (string, string, error) {
		q, ok := quote(reply, secrets)
		switch {
		case e.secret != "" && e.readBack:
			return "", "the node has a different password stored.", ErrMismatch
		case e.secret != "":
			return "", "the node didn't confirm the password.", ErrWrongReply
		case !ok:
			if e.readBack {
				return "", "the node has something else stored.", ErrMismatch
			}
			return "", "the node gave an unexpected reply.", ErrWrongReply
		case e.readBack && strings.HasPrefix(reply, "> "):
			got, _ := quote(strings.TrimPrefix(reply, "> "), nil)
			if _, ok := quote(want, secrets); !ok {
				return "", "the node has " + got + " stored, not what was sent.", ErrMismatch
			}
			return "", fmt.Sprintf("the node has %s stored, not %s.", got, want), ErrMismatch
		}
		return "", "the node answered " + q + ".", ErrWrongReply
	}
	value, isValue := strings.CutPrefix(reply, "> ")
	switch e.kind {
	case expectExact:
		if reply == e.text {
			return "", "", nil
		}
		want, _ := quote(strings.TrimPrefix(e.text, "> "), nil)
		return bad(want)
	case expectClock:
		switch {
		case strings.HasPrefix(reply, "OK - clock set"), strings.HasPrefix(reply, "(OK - clock set"):
			return "", "", nil
		case strings.Contains(reply, "clock cannot go backwards"):
			// The node's clock is already later than the Zero's; it never goes back (CommonCLI.cpp:228-238).
			return "the node's clock was already later, so it was left as it was.", "", nil
		}
		return bad("the clock set")
	case expectRadio:
		if r, err := parseRadioReply(reply); err == nil && r.Near(e.radio) {
			return "", "", nil
		} else if err == nil {
			return "", fmt.Sprintf("the node has %s stored, not %s.", r.Display(), e.radio.Display()), ErrMismatch
		}
		return bad(e.radio.Display())
	case expectTx:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		want := int(e.num)
		if isValue && err == nil {
			// v1.11.0, v1.12.0 and v1.18.0 print a negative setting as 256+n, v1.13.0–v1.17.1 as n (7.4).
			if (n%256+256)%256 == (want%256+256)%256 {
				return "", "", nil
			}
			if n > 127 && n < 256 {
				n -= 256
			}
			return "", fmt.Sprintf("the node has %d dBm stored, not %d.", n, want), ErrMismatch
		}
		return bad(strconv.Itoa(want))
	case expectFloat:
		x, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if isValue && err == nil && math.Abs(x-e.num) < e.tol {
			return "", "", nil
		}
		return bad(strconv.FormatFloat(e.num, 'f', -1, 64))
	}
	return "", "", nil
}
