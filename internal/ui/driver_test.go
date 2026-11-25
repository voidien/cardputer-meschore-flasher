package ui

// Phase 2 contract (docs/phase2-design.md): this file is fixed and complete; builders C and D both use it. It is the
// CardputerZero app kit's test driver (code/internal/ui/ui_test.go: TestMain, sizes, eachSize, send, keys, check),
// changed so that nothing waits on real time: the screens' clock, their timers (Options.After) and their waits for a
// Job's pictures (Options.Wait) are the driver's, and a test moves them on itself (advance, settle). A Cmd the driver
// runs must return: one that hasn't in cmdGuard (a hung test, never a slow one) fails the test rather than being
// dropped, as the kit's 150 ms wait dropped it. To see a state while an answer is still out (a probe under way,
// Prepare not back), a test holds the Cmds instead of running them and releases them when it chooses (hold,
// release, releaseAll). Over the fake backend, a Cmd that takes real time (slowCmd) fails the test: it waits on a
// real timer (tea.Tick) where it should use m.after.

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	lcd "meshflash/internal/screen"
	"meshflash/internal/store"
)

// Goldens are plain text, so they read as the screen does: colors and reverse video are checked separately
// (TestStyles). Times print in UTC, and widths never follow a CJK locale (where go-runewidth would make ▸ ● … two
// cells while the LCD draws one).
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	time.Local = time.UTC
	runewidth.DefaultCondition.EastAsianWidth = false
	os.Exit(m.Run())
}

// The CLI app's grid, the narrowest the screens must still work at, and the whole LCD in Terminus 6×12 (S10).
var sizes = []struct{ w, h int }{{45, 10}, {40, 10}, {53, 14}}

// eachSize runs f as a subtest at each size, named "45x10", "40x10" and "53x14": a test calling check in it writes
// testdata/<TestName>/<W>x<H>.golden.
func eachSize(t *testing.T, f func(t *testing.T, w, h int)) {
	for _, s := range sizes {
		t.Run(fmt.Sprintf("%dx%d", s.w, s.h), func(t *testing.T) { f(t, s.w, s.h) })
	}
}

// testNow is the screens' clock when every test starts: 24 Nov 2026, 14:32 UTC.
var testNow = time.Date(2026, 11, 24, 14, 32, 0, 0, time.UTC)

// cmdGuard is how long a Cmd, or a fake world's Job, may take before the test fails as hung. It is never waited out
// by a passing test.
const cmdGuard = 30 * time.Second

// slowCmd is how much real time a Cmd over the fake backend may take: every fake answer is instant, so a Cmd that
// takes longer is waiting on a real timer. Generous, so a loaded machine never trips it; a tea.Tick of a second does.
const slowCmd = 500 * time.Millisecond

// driver runs the model without a terminal. Each Cmd runs at once and its message goes back in; tea.Batch's Cmds run
// one after another, in order (P10 in docs/phase2-design.md: never tea.Sequence, which the driver can't see into).
type driver struct {
	t         *testing.T
	m         *Model
	st        *store.Store
	configDir string
	log       *syncBuffer
	world     *fakeworld.World // for newWorldDriver

	mu     sync.Mutex
	now    time.Time
	timers []timer
	waits  []jobWait
	jobs   map[*flash.Job]bool // every Job the screens ever watched, for stopJobs
	// follow makes settle wait for each Job to finish (a fake world's Jobs finish by themselves); without it, settle
	// delivers what a test's scripted Job has published and leaves it waiting.
	follow bool
	quit   bool // a tea.Quit came back

	// holding keeps each Cmd in held instead of running it (hold); the test goroutine alone uses these.
	holding bool
	held    []tea.Cmd
}

type timer struct {
	due time.Time
	msg tea.Msg
}

type jobWait struct {
	j    *flash.Job
	seen uint64
}

// newDriver starts the model at w×h over a fake backend, a real store in temporary folders and the driver's clock.
// opts change the Options first (ShowBattery, Config, …).
func newDriver(t *testing.T, w, h int, fb *fakeBackend, opts ...func(*Options)) *driver {
	t.Helper()
	d := newBareDriver(t)
	d.start(w, h, fb, opts)
	return d
}

// newWorldDriver starts the model at w×h over the real orchestration (flash.New) on a fake world, with the catalog
// already saved when withCatalog, and the flasher sites served in memory. The backend's screen.log lines go to the
// same log as the screens'. settle waits for the world's Jobs to finish. When the test ends, every Job is stopped
// and waited for before the world's rules are checked and the temporary folders go.
func newWorldDriver(t *testing.T, w, h int, world *fakeworld.World, withCatalog bool, opts ...func(*Options)) *driver {
	t.Helper()
	return newWorldDriverWith(t, w, h, world, func(cacheDir string) (flash.Deps, error) {
		return world.Deps(cacheDir, "test", withCatalog)
	}, opts)
}

// newWorldDriverOver is newWorldDriver with the flasher sites at srv, an httptest.Server running world.Handler on
// the loopback interface (World.DepsOver): for the tests of the HTTP path itself.
func newWorldDriverOver(t *testing.T, w, h int, world *fakeworld.World, srv *httptest.Server, withCatalog bool,
	opts ...func(*Options)) *driver {
	t.Helper()
	return newWorldDriverWith(t, w, h, world, func(cacheDir string) (flash.Deps, error) {
		return world.DepsOver(srv, cacheDir, "test", withCatalog)
	}, opts)
}

func newWorldDriverWith(t *testing.T, w, h int, world *fakeworld.World, deps func(string) (flash.Deps, error),
	opts []func(*Options)) *driver {
	t.Helper()
	d := newBareDriver(t)
	d.world, d.follow = world, true
	dp, err := deps(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dp.Logf = func(format string, args ...any) { fmt.Fprintf(d.log, format+"\n", args...) }
	d.start(w, h, flash.New(dp), opts)
	// Cleanups run last first: this one before newBareDriver's and before the temporary folders are removed.
	t.Cleanup(func() {
		d.stopJobs()
		if p := world.Problems(); len(p) > 0 {
			t.Errorf("the backend broke the world's rules:\n%s", strings.Join(p, "\n"))
		}
	})
	return d
}

func newBareDriver(t *testing.T) *driver {
	configDir := t.TempDir()
	d := &driver{t: t, st: store.New(configDir, t.TempDir()), configDir: configDir, log: &syncBuffer{}, now: testNow,
		jobs: map[*flash.Job]bool{}}
	t.Cleanup(d.stopJobs)
	return d
}

// stopJobs cancels every Job the screens watched and, over a fake world, waits for each to finish (a test's
// scripted Jobs over the fake backend have nothing to stop), so no Job still writes when the folders are removed.
func (d *driver) stopJobs() {
	d.mu.Lock()
	jobs := make([]*flash.Job, 0, len(d.jobs))
	for j := range d.jobs {
		jobs = append(jobs, j)
	}
	d.mu.Unlock()
	for _, j := range jobs {
		j.Cancel()
	}
	if !d.follow {
		return
	}
	for _, j := range jobs {
		select {
		case <-j.Done():
		case <-time.After(cmdGuard):
			d.t.Errorf("job %d didn't finish in %v after it was cancelled", j.ID(), cmdGuard)
		}
	}
}

func (d *driver) start(w, h int, be Backend, opts []func(*Options)) {
	d.t.Helper()
	o := Options{Backend: be, Store: d.st, Version: "0.1.0", Log: d.log, Now: d.clock, After: d.after, Wait: d.wait}
	for _, f := range opts {
		f(&o)
	}
	d.m = New(o)
	d.run(d.m.Init())
	d.send(tea.WindowSizeMsg{Width: w, Height: h})
}

// The screens' clock, timers and Job waits (Options.Now, After, Wait): called from Update, on the test's goroutine,
// but locked all the same in case a Cmd calls one.
func (d *driver) clock() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.now
}

func (d *driver) after(dur time.Duration, msg tea.Msg) tea.Cmd {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.timers = append(d.timers, timer{due: d.now.Add(dur), msg: msg})
	return nil
}

func (d *driver) wait(j *flash.Job, seen uint64) tea.Cmd {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.waits = append(d.waits, jobWait{j, seen})
	d.jobs[j] = true
	return nil
}

// send gives the model a message, then runs every Cmd that follows from it, breadth first, drawing after each Update
// as Bubble Tea does (View clamps scroll offsets).
func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	queue := []tea.Msg{msg}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		_, cmd := d.m.Update(next)
		d.m.View()
		queue = append(queue, d.exec(cmd)...)
	}
}

// run runs a Cmd (Init's) and sends what comes back.
func (d *driver) run(cmd tea.Cmd) {
	d.t.Helper()
	for _, msg := range d.exec(cmd) {
		d.send(msg)
	}
}

// exec runs a Cmd and returns the messages it gives (a tea.Batch's children run in order), or, while holding, keeps
// it for release instead.
func (d *driver) exec(cmd tea.Cmd) []tea.Msg {
	d.t.Helper()
	if cmd == nil {
		return nil
	}
	if d.holding {
		d.held = append(d.held, cmd)
		return nil
	}
	return d.runCmd(cmd)
}

func (d *driver) runCmd(cmd tea.Cmd) []tea.Msg {
	d.t.Helper()
	ch := make(chan tea.Msg, 1)
	began := time.Now()
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(cmdGuard):
		d.t.Fatalf("a command didn't return in %v: it waits on something the test never ends", cmdGuard)
	}
	if took := time.Since(began); !d.follow && took > slowCmd {
		d.t.Errorf("a command took %v of real time over the fake backend: it waits on a real timer (use m.after) "+
			"or blocks", took)
	}
	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.QuitMsg:
		d.quit = true
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, d.exec(c)...)
		}
		return out
	}
	if fmt.Sprintf("%T", msg) == "tea.sequenceMsg" {
		d.t.Fatalf("a command used tea.Sequence: use tea.Batch, and don't depend on its order")
	}
	return []tea.Msg{msg}
}

// hold makes the driver keep every Cmd that follows (a tea.Batch's children too, once the batch is released) instead
// of running it, until release or releaseAll: so a test can look at the screen while a probe, a Prepare or a ReadNode
// is still out, or deliver answers in an order it chooses. Off again, Cmds run at once as before; those held stay
// held. A test knows its screens' order of Cmds: hold, press r, release(0) (the scan), check "Asking…".
func (d *driver) hold(on bool) { d.holding = on }

// heldCmds is how many Cmds are held.
func (d *driver) heldCmds() int { return len(d.held) }

// release runs the i-th held Cmd (from 0, in the order they were held) and sends what it gives. A tea.Batch's
// children, and the Cmds that follow from its messages, are held again while the driver still holds.
func (d *driver) release(i int) {
	d.t.Helper()
	if i < 0 || i >= len(d.held) {
		d.t.Fatalf("release(%d) with %d commands held", i, len(d.held))
	}
	cmd := d.held[i]
	d.held = append(d.held[:i:i], d.held[i+1:]...)
	for _, msg := range d.runCmd(cmd) {
		d.send(msg)
	}
}

// releaseAll stops holding and runs every held Cmd, in order, and everything that follows from them.
func (d *driver) releaseAll() {
	d.t.Helper()
	d.holding = false
	for len(d.held) > 0 {
		d.release(0)
	}
}

// advance moves the screens' clock on by dur and delivers every timer due by then, earliest first (a timer set while
// delivering, and due by then, too).
func (d *driver) advance(dur time.Duration) {
	d.t.Helper()
	d.mu.Lock()
	d.now = d.now.Add(dur)
	d.mu.Unlock()
	for {
		msg, ok := d.dueTimer()
		if !ok {
			return
		}
		d.send(msg)
	}
}

func (d *driver) dueTimer() (tea.Msg, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	best := -1
	for i, tm := range d.timers {
		if !tm.due.After(d.now) && (best < 0 || tm.due.Before(d.timers[best].due)) {
			best = i
		}
	}
	if best < 0 {
		return nil, false
	}
	msg := d.timers[best].msg
	d.timers = append(d.timers[:best], d.timers[best+1:]...)
	return msg, true
}

// timersSet is every message a timer will bring back, in the order they were set: a test that the Board screen
// stops rescanning once it is left, say.
func (d *driver) timersSet() []tea.Msg {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []tea.Msg
	for _, tm := range d.timers {
		out = append(out, tm.msg)
	}
	return out
}

// settle delivers the Jobs' pictures: each one newer than the screens last asked for. With follow (a fake world) it
// waits for every Job to finish, delivering pictures on the way; otherwise it delivers what each scripted Job has
// published and leaves it waiting for more (the test publishes with Job.Set or Job.Finish, then settles again).
func (d *driver) settle() {
	d.t.Helper()
	for {
		p, ok := d.readyPicture()
		if !ok {
			return
		}
		d.send(p)
	}
}

func (d *driver) readyPicture() (flash.Progress, bool) {
	d.t.Helper()
	d.mu.Lock()
	waits := append([]jobWait(nil), d.waits...)
	d.mu.Unlock()
	for i, w := range waits {
		var p flash.Progress
		if d.follow {
			ctx, cancel := context.WithTimeout(context.Background(), cmdGuard)
			var err error
			p, err = w.j.Next(ctx, w.seen)
			cancel()
			if err != nil {
				d.t.Fatalf("job %d didn't move past picture %d in %v", w.j.ID(), w.seen, cmdGuard)
			}
		} else {
			p = w.j.Snapshot()
			if p.Seq <= w.seen {
				continue
			}
		}
		d.mu.Lock()
		d.waits = append(d.waits[:i:i], d.waits[i+1:]...)
		d.mu.Unlock()
		return p, true
	}
	return flash.Progress{}, false
}

// do types keys, then settles: the usual step of an end-to-end test.
func (d *driver) do(keys ...string) {
	d.t.Helper()
	d.keys(keys...)
	d.settle()
}

// namedKeys are the keys keys knows by name; anything else is typed as runes.
var namedKeys = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
	"backspace": tea.KeyBackspace, "ctrl+c": tea.KeyCtrlC, "ctrl+u": tea.KeyCtrlU, "up": tea.KeyUp,
	"down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "home": tea.KeyHome, "end": tea.KeyEnd,
}

// keys presses each key: a name from namedKeys, " " for Space, or text (a letter, "?", "y").
func (d *driver) keys(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		if t, ok := namedKeys[k]; ok {
			msg = tea.KeyMsg{Type: t}
		} else if k == " " {
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		d.send(msg)
	}
}

// typeText types text a character at a time, as the keyboard does.
func (d *driver) typeText(s string) {
	d.t.Helper()
	for _, r := range s {
		d.keys(string(r))
	}
}

func (d *driver) view() string { return d.m.View() }

// logText is screen.log so far.
func (d *driver) logText() string { return d.log.String() }

// check compares the screen with its golden, after checking it fills the grid exactly, never runs past its edge, its
// key hints aren't cut (S12), and it comes out the same on the LCD (--screen), where internal/screen lays it out cell
// by cell (S10, S28). Verbatim from the kit.
func (d *driver) check(w, h int) {
	d.t.Helper()
	v := d.view()
	lines := strings.Split(v, "\n")
	if len(lines) != h {
		d.t.Errorf("%d lines, want %d:\n%s", len(lines), h, v)
	}
	for i, l := range lines {
		if width(l) > w {
			d.t.Errorf("line %d is %d cells wide, more than %d: %q", i+1, width(l), w, l)
		}
	}
	// the last line is the key hints: a hint cut short hides a key, so each screen picks one that fits (fitHint)
	if last := ansi.Strip(lines[len(lines)-1]); width(last) == w && strings.HasSuffix(last, "…") {
		d.t.Errorf("the key hints are cut at %d columns: %q", w, last)
	}
	onLCD := strings.Split(lcd.Parse(v, w, h).Text(), "\n")
	for i, l := range lines {
		if want := strings.TrimRight(ansi.Strip(l), " "); i < len(onLCD) && onLCD[i] != want {
			d.t.Errorf("line %d on the LCD is %q, want %q", i+1, onLCD[i], want)
		}
	}
	golden.RequireEqual(d.t, v)
}

// noSecret fails the test if secret shows on the screen, in screen.log, or in any file in the config folder
// (config.json, drafts.json). The failure never prints the secret.
func (d *driver) noSecret(secret string) {
	d.t.Helper()
	if strings.Contains(d.view(), secret) {
		d.t.Error("the screen shows the password")
	}
	if strings.Contains(d.logText(), secret) {
		d.t.Error("screen.log holds the password")
	}
	err := filepath.WalkDir(d.configDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte(secret)) {
			d.t.Errorf("%s holds the password", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		d.t.Error(err)
	}
}

// syncBuffer is screen.log in memory: the screens write it from Update and the backend from its goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testCatalog is the committed live catalog (internal/catalog/testdata: MeshCore's with its two release lists, and
// ZephCore's), resolved once for the whole test binary, saved "24 Nov 2026, 09:12". It is shared: never change it.
// Its file URLs are the real sites', which no test fetches.
func testCatalog(t testing.TB) *catalog.Catalog {
	t.Helper()
	testCatOnce.Do(func() { testCat, testCatErr = loadTestCatalog() })
	if testCatErr != nil {
		t.Fatal(testCatErr)
	}
	return testCat
}

var (
	testCatOnce sync.Once
	testCat     *catalog.Catalog
	testCatErr  error
)

func loadTestCatalog() (*catalog.Catalog, error) {
	var size int64
	read := func(name string) ([]byte, error) {
		f, err := os.Open(filepath.Join("..", "catalog", "testdata", name))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		z, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(z)
		size += int64(len(b))
		return b, err
	}
	raws := map[string]*catalog.Raw{}
	for _, in := range []struct{ source, key, file string }{
		{catalog.SourceMeshCore, "", "meshcore-config.json.gz"},
		{catalog.SourceMeshCore, "github", "meshcore-releases-github.json.gz"},
		{catalog.SourceMeshCore, "github-ripple", "meshcore-releases-github-ripple.json.gz"},
		{catalog.SourceZephCore, "", "zephcore-config.json.gz"},
		{catalog.SourceZephCore, "github-zephcore", "zephcore-releases-github-zephcore.json.gz"},
	} {
		raw := raws[in.source]
		if raw == nil {
			raw = &catalog.Raw{Releases: map[string][]byte{}}
			raws[in.source] = raw
		}
		b, err := read(in.file)
		if err != nil {
			return nil, err
		}
		if in.key == "" {
			raw.Config = b
		} else {
			raw.Releases[in.key] = b
		}
	}
	c, err := catalog.Resolve(*raws[catalog.SourceMeshCore], *raws[catalog.SourceZephCore], catalog.DefaultSites)
	if err != nil {
		return nil, err
	}
	c.Fetched = time.Date(2026, 11, 24, 9, 12, 0, 0, time.UTC)
	c.Size, c.Downloaded = size, size
	return c, nil
}
