package screen

// meshflash's, not the kit's (see the package doc): the LCD's backlight, for meshflash's screen lock.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// DeviceFramebuffer is the LCD itself. Only a run drawing on it ever touches the real backlight.
	DeviceFramebuffer = "/dev/fb0"
	// BacklightClass is where Linux lists backlights. Which one the CardputerZero's LCD has, and whether its user may
	// write it, is checked on the device (UNVERIFIED: Phase 3, with the owner watching).
	BacklightClass = "/sys/class/backlight"
)

// Dimmable is a model that can ask for the screen to be dark: after each View, Run asks Dark, and while it is true
// it draws the screen black and turns the backlight off; the first frame after it draws the picture and then turns
// the backlight on again, so the old picture never flashes up. meshflash's screen lock (internal/ui) is one.
type Dimmable interface {
	Dark() bool
}

// Backlight is the LCD's light. Off and On never fail the caller: a light that can't be switched is logged once and
// left as it is (the dark screen is then drawn black, which is dark enough), and On puts back only what Off changed.
type Backlight interface {
	Off()
	On()
}

// FindBacklight is the first backlight in class (BacklightClass) with a brightness file, or "" when there is none.
func FindBacklight(class string) string {
	entries, err := os.ReadDir(class)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		dir := filepath.Join(class, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "brightness")); err == nil {
			return dir
		}
	}
	return ""
}

// NewSysBacklight is the backlight in a sysfs folder (a class device: brightness, max_brightness), or a fake folder
// of the same shape in a test or a run drawing into a file. Off writes 0 to brightness, keeping what was there; On
// writes it back. It never creates a file. Problems go to log.
func NewSysBacklight(dir string, log io.Writer) Backlight {
	if log == nil {
		log = io.Discard
	}
	return &sysBacklight{path: filepath.Join(dir, "brightness"), log: log}
}

type sysBacklight struct {
	path string
	log  io.Writer

	mu     sync.Mutex
	saved  []byte // the brightness before Off, while off; nil when Off changed nothing
	failed bool   // the light couldn't be switched: never tried again
}

func (b *sysBacklight) Off() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed || b.saved != nil {
		return
	}
	cur, err := os.ReadFile(b.path)
	if err != nil {
		b.fail(err)
		return
	}
	cur = bytes.TrimSpace(cur)
	if len(cur) == 0 || string(cur) == "0" {
		return // already dark: there is nothing to put back
	}
	if err := writeSys(b.path, "0"); err != nil {
		b.fail(err)
		return
	}
	b.saved = cur
}

func (b *sysBacklight) On() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.saved == nil {
		return
	}
	if err := writeSys(b.path, string(b.saved)); err != nil {
		// Kept, so the next On (Run always calls one before it returns) tries again.
		fmt.Fprintln(b.log, "backlight: can't turn it back on:", err)
		return
	}
	b.saved = nil
}

func (b *sysBacklight) fail(err error) {
	b.failed = true
	fmt.Fprintf(b.log, "backlight: %v; the screen lock draws the screen black and leaves the light on\n", err)
}

// writeSys writes a sysfs attribute, which must already exist.
func writeSys(path, v string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(v + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// noBacklight is a run with no backlight to switch: drawing into a file, a PC, a device whose light can't be found.
// The dark screen is drawn black; Off says so in the log, once.
type noBacklight struct {
	why  string
	log  io.Writer
	once sync.Once
}

func (b *noBacklight) Off() {
	b.once.Do(func() {
		fmt.Fprintf(b.log, "backlight: none (%s); the screen lock draws the screen black\n", b.why)
	})
}

func (b *noBacklight) On() {}

// backlight is what Run switches while the model is dark: a test's; the folder Options.Backlight names, but one under
// /sys only when drawing on DeviceFramebuffer; the device's own (the first in BacklightClass) when drawing on
// DeviceFramebuffer; else none. So a run into a file, a test and a PC never touch a real backlight, whatever folder
// they are given: one outside /sys is a fake (CLAUDE.md: never draw on the real LCD unless the owner is watching).
func (o *Options) backlight() Backlight {
	onLCD := o.Framebuffer == DeviceFramebuffer
	switch {
	case o.light != nil:
		return o.light
	case o.Backlight != "" && !onLCD && underSys(o.Backlight):
		return &noBacklight{why: o.Backlight + " is a real backlight and this run isn't drawing on " +
			DeviceFramebuffer, log: o.Log}
	case o.Backlight != "":
		return NewSysBacklight(o.Backlight, o.Log)
	case !onLCD:
		return &noBacklight{why: "not drawing on " + DeviceFramebuffer, log: o.Log}
	}
	if dir := FindBacklight(BacklightClass); dir != "" {
		return NewSysBacklight(dir, o.Log)
	}
	return &noBacklight{why: "nothing in " + BacklightClass, log: o.Log}
}

// underSys says dir is in /sys, as written or once its links are followed (a link to a real backlight is one).
func underSys(dir string) bool {
	in := func(p string) bool {
		p = filepath.Clean(p)
		return p == "/sys" || strings.HasPrefix(p, "/sys/")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return true // can't tell: treat it as real, which only leaves the light alone
	}
	if in(abs) {
		return true
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil && in(real) {
		return true
	}
	return false
}
