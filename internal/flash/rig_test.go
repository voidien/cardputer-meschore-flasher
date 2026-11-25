package flash_test

// The backend's tests run it against the fake world (internal/flash/fakeworld), which imports flash, so they are in
// package flash_test. Everything runs on the world's VirtualClock: no real sleeps, no network, no serial port.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/nodecli"
)

// jobGuard is how long a test waits, in real time, for a Job running in virtual time: far longer than any takes.
const jobGuard = 20 * time.Second

// rig is a backend over a fake world, with its screen.log lines kept.
type rig struct {
	t *testing.T
	w *fakeworld.World
	b *flash.Backend
	d flash.Deps

	clientDir string        // the catalog client's folder
	sites     catalog.Sites // the client's sites when they aren't fakeworld.Sites (an httptest server)

	mu   sync.Mutex
	logs []string
}

// newRig is a backend over a world with these boards plugged in, the catalog not saved (none of the backend's
// tests but the catalog's needs it loaded: the committed one is resolved once, fakeworld.Catalog). When the test
// ends the world's rules are checked.
func newRig(t *testing.T, boards ...*fakeworld.Board) *rig {
	t.Helper()
	w := fakeworld.New()
	for _, b := range boards {
		w.Plug(b)
	}
	return newRigOver(t, w, false)
}

func newRigOver(t *testing.T, w *fakeworld.World, withCatalog bool) *rig {
	t.Helper()
	dir := t.TempDir()
	d, err := w.Deps(dir, "test", withCatalog)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, w: w, clientDir: dir}
	d.Logf = func(format string, args ...any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.logs = append(r.logs, fmt.Sprintf(format, args...))
	}
	r.d, r.b = d, flash.New(d)
	t.Cleanup(func() {
		if p := w.Problems(); len(p) > 0 {
			t.Errorf("the backend broke the world's rules:\n%s", strings.Join(p, "\n"))
		}
	})
	return r
}

// log is screen.log so far.
func (r *rig) log() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.logs, "\n")
}

// port is the board's port as the Board screen would show it now.
func (r *rig) port(b *fakeworld.Board) flash.Port {
	r.t.Helper()
	s, err := r.b.Scan()
	if err != nil {
		r.t.Fatal(err)
	}
	for _, p := range s.Ports {
		if p.Name == b.Tty() {
			return p
		}
	}
	r.t.Fatalf("no port for the board (tty %q) in %d ports", b.Tty(), len(s.Ports))
	return flash.Port{}
}

// wait waits for a Job to finish and gives its last picture.
func wait(t *testing.T, j *flash.Job) flash.Progress {
	t.Helper()
	select {
	case <-j.Done():
	case <-time.After(jobGuard):
		t.Fatalf("job %d didn't finish in %v: %+v", j.ID(), jobGuard, j.Snapshot().Steps)
	}
	return j.Snapshot()
}

// committed is the committed catalog, resolved once (fakeworld.Catalog).
func committed(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := fakeworld.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// request is a flash of the newest version of a role (a catalog key and source) for a catalog board.
func request(t *testing.T, port flash.Port, device, source, role string, kind catalog.Kind) flash.Request {
	t.Helper()
	d := committed(t).Find(device)
	if d == nil {
		t.Fatalf("no board %q in the catalog", device)
	}
	for _, fw := range d.Firmware {
		if fw.Source == source && fw.Role == role {
			return flash.Request{Port: port, Device: *d, Firmware: fw, Version: fw.Versions[0], Kind: kind}
		}
	}
	t.Fatalf("%s has no %s %s", device, source, role)
	return flash.Request{}
}

// versioned is request with another version of the role, by name.
func versioned(t *testing.T, req flash.Request, name string) flash.Request {
	t.Helper()
	for _, v := range req.Firmware.Versions {
		if v.Name == name {
			req.Version = v
			return req
		}
	}
	t.Fatalf("%s has no version %s", roleOf(req), name)
	return req
}

func roleOf(req flash.Request) string { return req.Device.Name + " " + req.Firmware.Role }

// save downloads the file a request would flash, as d or y on Confirm does.
func (r *rig) save(req flash.Request) catalog.Flash {
	r.t.Helper()
	fl, err := catalog.Choose(req.Device, req.Version, req.Kind)
	if err != nil {
		r.t.Fatal(err)
	}
	p := wait(r.t, r.b.Download(context.Background(), []catalog.File{fl.File}))
	if !p.Outcome.OK {
		r.t.Fatalf("download: %+v", p.Outcome)
	}
	return fl
}

// flash runs a flash to its end.
func (r *rig) flash(req flash.Request) flash.Progress {
	r.t.Helper()
	return wait(r.t, r.b.Flash(context.Background(), req))
}

// steps is a picture's steps, one per line, as "✓ Text (note)", for failure messages.
func steps(p flash.Progress) string {
	var b strings.Builder
	marks := map[flash.StepState]string{flash.StepNow: "▸", flash.StepDone: "✓", flash.StepSkipped: "·",
		flash.StepFailed: "✗"}
	for _, s := range p.Steps {
		fmt.Fprintf(&b, "  %s %s", marks[s.State], s.Text)
		if s.Note != "" {
			fmt.Fprintf(&b, " (%s)", s.Note)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// texts is a picture's step texts.
func texts(p flash.Progress) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.Text)
	}
	return out
}

// outcome is an Outcome in a few lines, for failure messages.
func outcome(o flash.Outcome) string {
	return fmt.Sprintf("Title %q OK %v Stopped %v Retry %v Setup %v\n  Summary %q\n  State %q\n  Recovery %q\n  Log %q",
		o.Title, o.OK, o.Stopped, o.Retry, o.Setup, o.Summary, o.State, o.Recovery, o.Log)
}

// repeater is MeshCore repeater firmware of a version.
func repeater(version, name string) fakeworld.Firmware {
	return fakeworld.Firmware{Role: nodecli.RoleRepeater, Version: version, Name: name}
}

// flashSeeing runs a flash and takes its picture as the world's clock passes each of at, counted from the start: on
// the backend's own goroutine (World.At), so in step with it, where a watcher on another goroutine would see only
// the pictures it happened to wake for.
func (r *rig) flashSeeing(req flash.Request, at ...time.Duration) (flash.Progress, []flash.Progress) {
	r.t.Helper()
	var mu sync.Mutex
	var j *flash.Job
	seen := make([]flash.Progress, len(at))
	for i, d := range at {
		r.w.At(d, func() {
			mu.Lock()
			defer mu.Unlock()
			seen[i] = j.Snapshot()
		})
	}
	mu.Lock()
	j = r.b.Flash(context.Background(), req)
	mu.Unlock()
	p := wait(r.t, j)
	mu.Lock()
	defer mu.Unlock()
	return p, seen
}
