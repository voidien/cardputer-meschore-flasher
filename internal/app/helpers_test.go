package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/screen"
	"meshflash/internal/serialport"
	"meshflash/internal/store"
	"meshflash/internal/usbid"
)

// errStub is what the stub world's ports and network answer.
var errStub = errors.New("the stub world has no ports and no network")

// stubDeps are a backend's dependencies with nothing behind them: no port is listed, opening one fails the test, the
// catalog's network fails the test, and the clock is virtual. Every Program these tests build uses them (or a Fake
// that fails the test if called), so no test reaches realDeps.
func stubDeps(t *testing.T) flash.Deps {
	t.Helper()
	return flash.Deps{
		Open: func(path string, _ serialport.Options) (serialport.Port, error) {
			t.Errorf("a test opened the port %s", path)
			return nil, errStub
		},
		Scan:    func() ([]usbid.Port, error) { return nil, nil },
		Dialout: func() (bool, string) { return true, "" },
		Clock:   serialport.NewVirtualClock(nil),
		Catalog: catalog.NewClient(t.TempDir(), "0.1.0", catalog.Options{
			Sites: catalog.Sites{MeshCore: "http://meshcore.stub.invalid", ZephCore: "http://zephcore.stub.invalid",
				GitHub: "http://github.stub.invalid"},
			HTTP: &http.Client{Transport: noNetwork{t}},
		}),
	}
}

// noNetwork fails the test on any request.
type noNetwork struct{ t *testing.T }

func (n noNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("a test reached the network: %s", r.URL)
	return nil, errStub
}

// stubProgram is meshflash with the stub world as its Fake.
func stubProgram(t *testing.T) Program {
	t.Helper()
	return Program{Name: "meshflash", Command: "meshflash", Version: "0.1.0",
		Fake: func(Env) (flash.Deps, error) { return stubDeps(t), nil }}
}

// refusingFake is a Fake that fails the test if it is asked for a world at all.
func refusingFake(t *testing.T) Fake {
	return func(Env) (flash.Deps, error) {
		t.Error("the program built its world, which this test never needs")
		return flash.Deps{}, errStub
	}
}

// tempStore is a store in the test's own folders.
func tempStore(t *testing.T) *store.Store {
	dir := t.TempDir()
	return store.New(filepath.Join(dir, "config", "meshflash"), filepath.Join(dir, "cache", "meshflash"))
}

// memLog is a screen.log in memory, safe to read while the program's goroutines write it.
type memLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (m *memLog) Write(b []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(b)
}

func (m *memLog) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

// newLog is a logWriter over a memLog.
func newLog() (*logWriter, *memLog) {
	m := &memLog{}
	return &logWriter{w: m}, m
}

// shown is what the screens were started with, by recording runners that never draw.
type shown struct {
	lcd      *screen.Options // set when they ran on the LCD
	terminal bool            // set when they ran in the terminal
	model    tea.Model
}

// recorders are runners that only record what they were given and end at once, as a quit would.
func (s *shown) runners() runners {
	return runners{
		lcd: func(m tea.Model, o screen.Options) (tea.Model, error) {
			s.lcd, s.model = &o, m
			return m, nil
		},
		terminal: func(m tea.Model, _ io.Reader, _ io.Writer) (bool, error) {
			s.terminal, s.model = true, m
			return false, nil
		},
	}
}

// startWith runs start for p with args over a temporary store, the recording runners and a log in memory.
func startWith(t *testing.T, p Program, args ...string) (*shown, *memLog, error) {
	t.Helper()
	f, err := p.parse(args, io.Discard)
	if err != nil {
		t.Fatalf("parse(%q): %v", args, err)
	}
	log, mem := newLog()
	s := &shown{}
	err = p.start(f, tempStore(t), log, strings.NewReader(""), io.Discard, io.Discard, s.runners())
	return s, mem, err
}

// fakeModel stands in for the screens' model in finish's tests, with a Job of the test's own.
type fakeModel struct {
	job         *flash.Job
	shutdowns   int
	unsents     int
	brokenSaves bool // SaveUnsent panics, as a model a crash left half-changed might
}

// Shutdown is the frame's: it saves what's unsent and stops the Job.
func (m *fakeModel) Shutdown() *flash.Job {
	m.shutdowns++
	m.unsents++
	if m.job != nil {
		m.job.Cancel()
	}
	return m.job
}

func (m *fakeModel) SaveUnsent() {
	m.unsents++
	if m.brokenSaves {
		panic("the model was left half-changed")
	}
}

func (m *fakeModel) Running() *flash.Job { return m.job }

// backendJob is a Job as the backend runs one: its worker finishes it with stopped once it is cancelled. cancels says
// how often it was cancelled while it ran (Finish's own release of the Job's context isn't one).
func backendJob(kind flash.JobKind, stopped flash.Outcome) (j *flash.Job, cancels func() int) {
	ctx, cancel := context.WithCancel(context.Background())
	var (
		mu sync.Mutex
		n  int
	)
	j = flash.NewJob(kind, nil, func() {
		select {
		case <-j.Done():
		default:
			mu.Lock()
			n++
			mu.Unlock()
		}
		cancel()
	})
	go func() {
		<-ctx.Done()
		j.Finish(stopped)
	}()
	return j, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// stuckJob is a Job whose worker never finishes it, however it is cancelled; its last picture is a step under way
// with risk.
func stuckJob(kind flash.JobKind, risk flash.Risk) (j *flash.Job, cancels func() int) {
	n := 0
	j = flash.NewJob(kind, nil, func() { n++ })
	j.Set(func(p *flash.Progress) {
		p.Steps = []flash.Step{{Text: "Writing", State: flash.StepNow}}
		p.Risk = risk
	})
	return j, func() int { return n }
}

// fakeWaits replaces finish's clock for the test: it records every wait asked, in order, and the i-th one is over at
// once when fire[i] is true, else never (the Job decides). then, if not nil, is called at each wait with its number.
func fakeWaits(t *testing.T, then func(i int), fire ...bool) *[]time.Duration {
	t.Helper()
	var asked []time.Duration
	old := timeAfter
	t.Cleanup(func() { timeAfter = old })
	timeAfter = func(d time.Duration) <-chan time.Time {
		i := len(asked)
		asked = append(asked, d)
		c := make(chan time.Time, 1)
		if i < len(fire) && fire[i] {
			c <- time.Time{}
		}
		if then != nil {
			then(i)
		}
		return c
	}
	return &asked
}

// radioBook records the radios saved.
type radioBook struct {
	saved []store.Radio
	err   error
}

func (b *radioBook) SaveRadio(r store.Radio) error {
	b.saved = append(b.saved, r)
	return b.err
}
