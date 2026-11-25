package screen

// meshflash's tests (not the kit's) of the screen lock's dark screen: the backlight in a fake sysfs folder, the
// drawing loop's dark frames, and the light turned on again before every run ends. No real backlight, no real
// framebuffer, no sleeps.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeLight is a Backlight that notes each call and, at each, the pixel at the screen's first cell (so a test can
// tell whether the picture was drawn before or after it) and how many rows had been written.
type fakeLight struct {
	mu    sync.Mutex
	calls []string
	at    func() uint16
}

func (l *fakeLight) note(what string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at != nil {
		what += map[bool]string{true: " (drawn)", false: " (black)"}[l.at() != palette[DefaultBG]]
	}
	l.calls = append(l.calls, what)
}

func (l *fakeLight) Off() { l.note("off") }
func (l *fakeLight) On()  { l.note("on") }

func (l *fakeLight) got() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.calls, ", ")
}

// backlightDir is a fake class device holding brightness.
func backlightDir(t *testing.T, brightness string) string {
	t.Helper()
	dir := t.TempDir()
	if brightness != "" {
		if err := os.WriteFile(filepath.Join(dir, "brightness"), []byte(brightness), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "max_brightness"), []byte("255\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func brightnessIn(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "brightness"))
	if err != nil {
		return "(none)"
	}
	return strings.TrimSpace(string(b))
}

func TestSysBacklightOffAndOn(t *testing.T) {
	dir := backlightDir(t, "180\n")
	var log bytes.Buffer
	b := NewSysBacklight(dir, &log)
	b.Off()
	if got := brightnessIn(t, dir); got != "0" {
		t.Fatalf("off: brightness %q, want 0", got)
	}
	b.Off()
	b.On()
	if got := brightnessIn(t, dir); got != "180" {
		t.Errorf("on: brightness %q, want the 180 it had", got)
	}
	b.On()
	if got := brightnessIn(t, dir); got != "180" || log.Len() > 0 {
		t.Errorf("a second On: brightness %q, log %q", got, log.String())
	}
}

// A light already off (the screen asleep) is left as it is, and On puts back nothing.
func TestSysBacklightAlreadyDark(t *testing.T) {
	dir := backlightDir(t, "0\n")
	b := NewSysBacklight(dir, nil)
	b.Off()
	b.On()
	if got := brightnessIn(t, dir); got != "0" {
		t.Errorf("brightness %q, want the 0 it had", got)
	}
}

// A light that can't be switched (here: no brightness file; on the device, one void may not write) is logged once
// and left alone, and no file is ever created.
func TestSysBacklightRefusedIsLoggedOnce(t *testing.T) {
	dir := backlightDir(t, "")
	var log bytes.Buffer
	b := NewSysBacklight(dir, &log)
	b.Off()
	b.Off()
	b.On()
	if n := strings.Count(log.String(), "backlight:"); n != 1 {
		t.Errorf("%d log lines, want 1:\n%s", n, log.String())
	}
	if !strings.Contains(log.String(), "draws the screen black") {
		t.Errorf("the log doesn't say what happens instead: %q", log.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "brightness")); err == nil {
		t.Error("a brightness file was created")
	}
}

// On that fails keeps the brightness to put back, so the On every run ends with tries again.
func TestSysBacklightOnRetries(t *testing.T) {
	dir := backlightDir(t, "200\n")
	var log bytes.Buffer
	b := NewSysBacklight(dir, &log)
	b.Off()
	path := filepath.Join(dir, "brightness")
	os.Remove(path)
	b.On()
	if !strings.Contains(log.String(), "can't turn it back on") {
		t.Errorf("a failed On wasn't logged: %q", log.String())
	}
	os.WriteFile(path, []byte("0\n"), 0o600)
	b.On()
	if got := brightnessIn(t, dir); got != "200" {
		t.Errorf("after the retry: brightness %q, want 200", got)
	}
}

func TestFindBacklight(t *testing.T) {
	class := t.TempDir()
	os.Mkdir(filepath.Join(class, "a-no-brightness"), 0o700)
	os.Mkdir(filepath.Join(class, "b-lcd"), 0o700)
	os.WriteFile(filepath.Join(class, "b-lcd", "brightness"), []byte("1\n"), 0o600)
	if got := FindBacklight(class); got != filepath.Join(class, "b-lcd") {
		t.Errorf("found %q", got)
	}
	if got := FindBacklight(t.TempDir()); got != "" {
		t.Errorf("an empty class found %q", got)
	}
}

// Only a run drawing on the LCD itself switches the real backlight: drawing into a file switches only a folder it
// is given, and otherwise nothing.
func TestBacklightOnlyOnTheRealScreen(t *testing.T) {
	var log bytes.Buffer
	o := Options{Framebuffer: filepath.Join(t.TempDir(), "fb0"), Log: &log}
	if _, ok := o.backlight().(*noBacklight); !ok {
		t.Errorf("drawing into a file got a %T, want none", o.backlight())
	}
	nb := o.backlight()
	nb.Off()
	nb.Off()
	nb.On()
	if n := strings.Count(log.String(), "backlight: none"); n != 1 {
		t.Errorf("no backlight logged %d times, want once: %q", n, log.String())
	}
	o.Backlight = backlightDir(t, "9\n")
	if _, ok := o.backlight().(*sysBacklight); !ok {
		t.Errorf("a folder given got a %T", o.backlight())
	}
	// A real backlight named while drawing into a file is left alone, written as /sys or reached through a link.
	link := filepath.Join(t.TempDir(), "light")
	if err := os.Symlink("/sys", link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"/sys/class/backlight/x", "/sys/../sys/class/backlight/x", link} {
		o.Backlight = dir
		if _, ok := o.backlight().(*noBacklight); !ok {
			t.Errorf("--backlight-sys %s while drawing into a file got a %T, want none", dir, o.backlight())
		}
	}
	// Only which kind is checked: the light is never switched (Off or On), so nothing under /sys is touched.
	o.Framebuffer = DeviceFramebuffer
	o.Backlight = "/sys/class/backlight/x"
	if _, ok := o.backlight().(*sysBacklight); !ok {
		t.Errorf("a /sys folder named while drawing on %s got a %T", DeviceFramebuffer, o.backlight())
	}
	fake := &fakeLight{}
	o.light = fake
	if o.backlight() != Backlight(fake) {
		t.Error("a test's light wasn't used")
	}
}

// A dark frame draws the screen black before the light goes off; the next frame draws its picture before the light
// comes back on; dark frames in a row switch nothing.
func TestDrawDarkThenLight(t *testing.T) {
	fb := &memFB{b: make([]byte, 640*170)}
	s, _ := NewScreen(fb, device, testFont(t))
	light := &fakeLight{at: func() uint16 {
		fb.mu.Lock()
		defer fb.mu.Unlock()
		return fb.pixel(device, s.left, s.top)
	}}
	frames := make(chan frame)
	done := make(chan struct{})
	go func() {
		defer close(done)
		draw(s, light, frames, nil)
	}()
	for _, f := range []frame{{view: "A"}, {dark: true}, {dark: true}, {view: "A"}, {view: "A again"}} {
		frames <- f
	}
	close(frames)
	<-done
	if got := light.got(); got != "off (black), on (drawn)" {
		t.Errorf("the light was switched: %s", got)
	}
}

// However a run ends, the light is on before the screen is blanked (S8), so the home screen is never left dark.
func TestLeaveTurnsTheLightOnFirst(t *testing.T) {
	fb := &memFB{b: make([]byte, 640*170)}
	s, _ := NewScreen(fb, device, testFont(t))
	s.Draw(Parse("A", s.Cols, s.Rows))
	light := &fakeLight{at: func() uint16 { return fb.pixel(device, s.left, s.top) }}
	leave(s, light, nil)
	if got := light.got(); got != "on (drawn)" {
		t.Errorf("the light at the end: %s, want on before the blank", got)
	}
	if bytes.Count(fb.b, []byte{0}) != len(fb.b) {
		t.Error("the screen wasn't left blank")
	}
}

// tap asks a Dimmable model whether it is dark, with each View.
func TestTapAsksTheModelIfDark(t *testing.T) {
	frames := make(chan frame, 1)
	tap{Model: darkModel{}, frames: frames}.View()
	if f := <-frames; !f.dark || f.view != "" {
		t.Errorf("frame %+v, want a dark one", f)
	}
	tap{Model: echo{}, frames: frames}.View()
	if f := <-frames; f.dark {
		t.Error("a model that isn't Dimmable gave a dark frame")
	}
}

// darkModel asks for a dark screen from the start and quits on the first key.
type darkModel struct{}

func (darkModel) Init() tea.Cmd { return nil }

func (d darkModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return d, tea.Quit
	}
	return d, nil
}

func (darkModel) View() string { return "" }
func (darkModel) Dark() bool   { return true }

// A whole run on a fake device with a model that is dark throughout: the light goes off, and is on again when Run
// returns, with the screen blank. The key that ends it is written as soon as Run opens the keyboard.
func TestRunTurnsTheLightBackOn(t *testing.T) {
	o := fakeDevice(t)
	light := &fakeLight{}
	o.light = light
	go func() {
		w, err := os.OpenFile(o.Keyboard, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		w.Write(append(event(30, 1), event(30, 0)...))
		w.Close()
	}()
	if _, err := Run(darkModel{}, o); err != nil {
		t.Fatal(err)
	}
	if got := light.got(); got != "off, on" {
		t.Errorf("the light was switched: %s, want off, then on at the end", got)
	}
	if fb, _ := os.ReadFile(o.Framebuffer); bytes.Count(fb, []byte{0}) != len(fb) {
		t.Error("the screen wasn't left clear")
	}
}
