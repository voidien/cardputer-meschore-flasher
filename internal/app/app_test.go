package app

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"meshflash/internal/flash"
	"meshflash/internal/screen"
	"meshflash/internal/serialport"
	"meshflash/internal/store"
	"meshflash/internal/ui"
)

// After the screens have quit (and stopped listening), a SIGTERM still mustn't kill the program before it saves. The
// check runs in a child process (this test binary again), which would die of the signal without absorbSIGTERM. Its
// 200 ms sleep, the kit's, is the one real sleep in meshflash's own tests (SPEC.md 10; internal/screen's tests copied
// from the kit keep theirs): a signal has no other way to say it has been and gone.
func TestASIGTERMAfterQuittingIsAbsorbed(t *testing.T) {
	if os.Getenv("MESHFLASH_SIGTERM_CHILD") == "1" {
		absorbSIGTERM()
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond) // time for the signal to arrive (and, without the fix, kill)
		os.Stdout.WriteString("saved\n")
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestASIGTERMAfterQuittingIsAbsorbed$")
	cmd.Env = append(os.Environ(), "MESHFLASH_SIGTERM_CHILD=1")
	out, err := cmd.Output()
	if err != nil || string(out) != "saved\n" {
		t.Errorf("the program didn't live through a late SIGTERM: %v, output %q", err, out)
	}
}

// In a terminal the screens run under the app's own signal handling (inTerminal), not Bubble Tea's. In a child
// process (this test binary again) the screens read a pipe that never sends a key, and their first command sends the
// process a signal: a SIGTERM quits them as a quit does, a SIGHUP (the CLI app's terminal closing) ends them as a
// lost terminal, so a flash under way is left to finish; neither kills the program. No sleep: the child waits for the
// screens to end.
func TestTerminalSignals(t *testing.T) {
	if sig := os.Getenv("MESHFLASH_SIGNAL_CHILD"); sig != "" {
		absorbSIGTERM()
		hups, _ := absorbSIGHUP()
		in, keep, err := os.Pipe()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer keep.Close() // its input never ends
		send := map[string]syscall.Signal{"term": syscall.SIGTERM, "hup": syscall.SIGHUP}[sig]
		hungUp, err := inTerminal(signalModel{send}, in, io.Discard, hups)
		fmt.Printf("hung up %v, error %v\n", hungUp, err)
		os.Exit(0)
	}
	for sig, want := range map[string]string{
		"term": "hung up false, error <nil>\n",
		"hup":  "hung up true, error <nil>\n",
	} {
		t.Run(sig, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestTerminalSignals$")
			cmd.Env = append(os.Environ(), "MESHFLASH_SIGNAL_CHILD="+sig)
			out, err := cmd.Output()
			if err != nil || string(out) != want {
				t.Errorf("SIG%s: %v, output %q, want %q", strings.ToUpper(sig), err, out, want)
			}
		})
	}
}

// signalModel sends the process a signal as soon as the screens run.
type signalModel struct{ sig syscall.Signal }

func (m signalModel) Init() tea.Cmd {
	return func() tea.Msg {
		syscall.Kill(os.Getpid(), m.sig)
		return nil
	}
}

func (m signalModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m signalModel) View() string                        { return "" }

// The text box's cursor and the headings are reverse video, so meshflash keeps the 16 colors whatever TERM says: the
// home screen's terminal says vt100, the CLI app claims true color it can't show.
func TestColorProfile(t *testing.T) {
	for _, term := range []string{"vt100", "", "st-256color", "xterm-256color"} {
		t.Run(term, func(t *testing.T) {
			t.Setenv("TERM", term)
			t.Setenv("COLORTERM", "truecolor")
			t.Setenv("NO_COLOR", "")
			t.Setenv("CLICOLOR", "")
			if got := colorProfile(); got != termenv.ANSI {
				t.Errorf("TERM=%q: profile %v, want ANSI", term, got)
			}
			t.Setenv("NO_COLOR", "1")
			if got := colorProfile(); got != termenv.Ascii {
				t.Errorf("TERM=%q with NO_COLOR: profile %v, want none", term, got)
			}
		})
	}
}

// --version prints "<Name> <Version>" (S3) and opens nothing: no folder, no screen.log, so a meshflash already
// running keeps its log.
func TestVersionFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	for _, c := range []struct{ name, arg string }{
		{"meshflash", "--version"},
		{"meshflash", "-version"},
		{"meshflash-demo", "--version"},
	} {
		p := Program{Name: c.name, Command: c.name, Version: "0.1.0", Fake: refusingFake(t)}
		var out, errs bytes.Buffer
		code := p.run([]string{c.arg}, strings.NewReader(""), &out, &errs)
		if want := c.name + " 0.1.0\n"; code != 0 || out.String() != want || errs.Len() != 0 {
			t.Errorf("%s %s: exit %d, stdout %q, stderr %q; want exit 0 and %q", c.name, c.arg, code, out.String(),
				errs.String(), want)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("--version made %v in the home folder", entries)
	}
}

// The flags are the kit's, without --server, and --backlight-sys; -h lists them all, and a flag or an argument it
// doesn't know is an error the flag package explains.
func TestFlags(t *testing.T) {
	p := stubProgram(t)
	f, err := p.parse(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := flags{font: screen.DefaultFont, framebuffer: screen.DeviceFramebuffer, keyboard: screen.DefaultKeyboard,
		sys: "/sys/class/graphics/fb0"}
	if f != want {
		t.Errorf("defaults %+v, want %+v", f, want)
	}

	var help bytes.Buffer
	if _, err := p.parse([]string{"--help"}, &help); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("--help: %v, want flag.ErrHelp", err)
	}
	for _, name := range []string{"version", "screen", "screen-test", "font", "framebuffer", "keyboard",
		"framebuffer-sys", "backlight-sys"} {
		if !strings.Contains(help.String(), "-"+name+" ") && !strings.Contains(help.String(), "-"+name+"\n") {
			t.Errorf("--help doesn't list -%s:\n%s", name, help.String())
		}
	}
	if strings.Contains(help.String(), "-server") {
		t.Errorf("--help lists the kit's --server, which meshflash hasn't:\n%s", help.String())
	}

	for _, args := range [][]string{{"--server", "x"}, {"screen"}, {"--screen", "extra"}} {
		var errs bytes.Buffer
		if _, err := p.parse(args, &errs); err == nil || errs.Len() == 0 {
			t.Errorf("%q: %v, stderr %q; want an error, explained", args, err, errs.String())
		}
	}
	got, err := p.parse([]string{"--screen", "--framebuffer", "/tmp/fb", "--backlight-sys", "/tmp/bl"}, io.Discard)
	if err != nil || !got.screen || got.framebuffer != "/tmp/fb" || got.backlight != "/tmp/bl" {
		t.Errorf("--screen --framebuffer /tmp/fb --backlight-sys /tmp/bl: %+v, %v", got, err)
	}
}

// The demo build draws only into a regular file, never on the LCD or any other device, so its fake boards never show
// on the device's screen and it never switches the real backlight; the release build draws wherever it is told.
func TestDemoDrawsOnlyIntoAFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fb")
	if err := os.WriteFile(file, make([]byte, 320*170*2), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "fb-link")
	if err := os.Symlink("/dev/null", link); err != nil {
		t.Fatal(err)
	}
	demo := stubProgram(t)
	release := demo
	release.Fake = nil
	for _, c := range []struct {
		p    Program
		args []string
		ok   bool
	}{
		{demo, nil, true}, // the terminal
		{demo, []string{"--screen"}, false},
		{demo, []string{"--screen-test"}, false},
		{demo, []string{"--screen", "--framebuffer", "/dev/fb0"}, false},
		{demo, []string{"--screen", "--framebuffer", "/dev/null"}, false},
		{demo, []string{"--screen", "--framebuffer", link}, false},
		{demo, []string{"--screen", "--framebuffer", filepath.Join(dir, "missing")}, false},
		{demo, []string{"--screen", "--framebuffer", dir}, false},
		{demo, []string{"--screen", "--framebuffer", file}, true},
		{demo, []string{"--screen-test", "--framebuffer", file}, true},
		{release, []string{"--screen"}, true},
	} {
		f, err := c.p.parse(c.args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		err = c.p.check(f)
		if (err == nil) != c.ok {
			t.Errorf("fake %v, %q: %v; want allowed %v", c.p.Fake != nil, c.args, err, c.ok)
		}
	}
}

// The screen lock's wiring (docs/phase2-design.md 7.1, 18): on the LCD screen.Run gets the Tab hold's message
// (ui.LockMsg{}, after its default 3 s) and the backlight folder exactly as --backlight-sys gave it, "" without one
// (internal/screen decides which folder it may switch: one under /sys only when drawing on /dev/fb0, its own tests'
// business); the model it is given is one it can ask to go dark. In a terminal the frame finds the Tab hold itself
// (TerminalKeys) and the status line leaves the battery to the CLI app's bar.
func TestLockWiring(t *testing.T) {
	dir := t.TempDir()
	fb := filepath.Join(dir, "fb")
	if err := os.WriteFile(fb, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	light := filepath.Join(dir, "backlight")
	for _, c := range []struct {
		args      []string
		backlight string
	}{
		{[]string{"--screen", "--framebuffer", fb, "--backlight-sys", light}, light},
		{[]string{"--screen", "--framebuffer", fb}, ""},
		{[]string{"--screen", "--framebuffer", fb, "--backlight-sys", "/sys/class/backlight/x"}, "/sys/class/backlight/x"},
	} {
		s, _, err := startWith(t, stubProgram(t), c.args...)
		if err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if s.lcd == nil || s.terminal {
			t.Fatalf("%q: drawn on the LCD %v, in the terminal %v", c.args, s.lcd != nil, s.terminal)
		}
		o := *s.lcd
		if o.TabHeld != (ui.LockMsg{}) || o.HoldToLock != 0 {
			t.Errorf("%q: TabHeld %#v after %v, want ui.LockMsg{} after the default 3 s", c.args, o.TabHeld,
				o.HoldToLock)
		}
		if o.Backlight != c.backlight {
			t.Errorf("%q: Backlight %q, want %q", c.args, o.Backlight, c.backlight)
		}
		if o.Framebuffer != fb || o.Log == nil {
			t.Errorf("%q: Framebuffer %q, Log %v", c.args, o.Framebuffer, o.Log)
		}
		if _, ok := s.model.(*ui.Model); !ok {
			t.Errorf("%q: the screens are %T, not the *ui.Model the shutdown ends", c.args, s.model)
		}
		if _, ok := s.model.(screen.Dimmable); !ok {
			t.Errorf("%q: the model %T can't ask internal/screen for a dark screen", c.args, s.model)
		}
	}

	s, _, err := startWith(t, stubProgram(t))
	if err != nil {
		t.Fatal(err)
	}
	if !s.terminal || s.lcd != nil {
		t.Errorf("no --screen: drawn on the LCD %v, in the terminal %v", s.lcd != nil, s.terminal)
	}

	p := stubProgram(t)
	log, _ := newLog()
	for _, onLCD := range []bool{false, true} {
		o := p.uiOptions(nil, nil, store.Config{}, log, onLCD)
		if o.TerminalKeys == onLCD || o.ShowBattery != onLCD {
			t.Errorf("on the LCD %v: TerminalKeys %v, ShowBattery %v", onLCD, o.TerminalKeys, o.ShowBattery)
		}
		if o.Name != p.Name || o.Version != p.Version || o.Log != io.Writer(log) {
			t.Errorf("on the LCD %v: Name %q, Version %q, Log %v", onLCD, o.Name, o.Version, o.Log)
		}
		if o.Now != nil || o.After != nil || o.Wait != nil {
			t.Errorf("on the LCD %v: the screens' clock or waits replaced outside a test", onLCD)
		}
	}
}

// A Fake given is used: it gets the Program's own folders, its version and screen.log; the backend is built over
// its world; a Logf it leaves unset writes to screen.log, and one it sets is kept. A world it can't make, or one
// missing what the backend must have, stops the program before the screens start.
func TestDemoDepsWired(t *testing.T) {
	var got []Env
	p := stubProgram(t)
	p.Fake = func(e Env) (flash.Deps, error) {
		got = append(got, e)
		return stubDeps(t), nil
	}
	f, _ := p.parse(nil, io.Discard)
	st := tempStore(t)
	log, mem := newLog()
	s := &shown{}
	if err := p.start(f, st, log, strings.NewReader(""), io.Discard, io.Discard, s.runners()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the Fake was asked %d times, want once", len(got))
	}
	want := Env{ConfigDir: st.ConfigDir(), CacheDir: st.CacheDir(), Version: "0.1.0", Log: log}
	if got[0] != want {
		t.Errorf("the Fake got %+v, want %+v", got[0], want)
	}
	if s.model == nil {
		t.Error("the screens never started")
	}

	errOwn := errors.New("the Fake's own Open")
	p.Fake = func(Env) (flash.Deps, error) {
		d := stubDeps(t)
		d.Open = func(string, serialport.Options) (serialport.Port, error) { return nil, errOwn }
		return d, nil
	}
	d, err := deps(p, Env{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Open("/dev/ttyACM0", serialport.Options{}); !errors.Is(err, errOwn) {
		t.Errorf("Open isn't the Fake's: %v", err)
	}
	d.Logf("probe: %s answered %d", "ttyACM0", 3)
	if !strings.Contains(mem.String(), "probe: ttyACM0 answered 3\n") {
		t.Errorf("Deps.Logf doesn't write a line to screen.log: %q", mem.String())
	}
	var own []string
	p.Fake = func(Env) (flash.Deps, error) {
		d := stubDeps(t)
		d.Logf = func(format string, args ...any) { own = append(own, fmt.Sprintf(format, args...)) }
		return d, nil
	}
	d, _ = deps(p, Env{Log: log})
	if d.Logf("x"); len(own) != 1 {
		t.Error("a Fake's own Logf was replaced")
	}

	p.Fake = func(Env) (flash.Deps, error) { return flash.Deps{}, errStub }
	s = &shown{}
	if err := p.start(f, st, log, strings.NewReader(""), io.Discard, io.Discard, s.runners()); !errors.Is(err,
		errStub) || s.model != nil {
		t.Errorf("a Fake that fails: %v, screens started %v", err, s.model != nil)
	}
	p.Fake = func(Env) (flash.Deps, error) {
		d := stubDeps(t)
		d.Catalog = nil
		return d, nil
	}
	if _, err := deps(p, Env{}); err == nil {
		t.Error("a world without a catalog was taken")
	}
}

// A config.json that can't be read at all (not a damaged one, which reads as none) doesn't stop meshflash: it starts
// without the boards and the radio it remembers, and screen.log says why (docs/phase2-design.md 13).
func TestUnreadableConfigIsALine(t *testing.T) {
	p := stubProgram(t)
	f, _ := p.parse(nil, io.Discard)
	log, mem := newLog()
	s := &shown{}
	st := unreadableConfig{tempStore(t)}
	if err := p.start(f, st, log, strings.NewReader(""), io.Discard, io.Discard, s.runners()); err != nil {
		t.Fatal(err)
	}
	if s.model == nil {
		t.Error("the screens never started")
	}
	want := "meshflash: can't read config.json, so it starts without the boards and the radio it remembers: " +
		"permission denied\n"
	if mem.String() != want {
		t.Errorf("screen.log %q, want %q", mem.String(), want)
	}
}

// unreadableConfig is a store whose config.json can't be read.
type unreadableConfig struct{ *store.Store }

func (unreadableConfig) LoadConfig() (store.Config, error) {
	return store.Config{}, os.ErrPermission
}
