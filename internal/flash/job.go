package flash

// Phase 2 contract (docs/phase2-design.md): this file is fixed and complete. Builder B adds job_test.go.

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	"meshflash/internal/serialport"
)

// jobIDs numbers jobs for the whole program, so a screen can tell its job's pictures from an earlier one's.
var jobIDs atomic.Uint64

// Job is long work running on its own goroutine (a download, a flash, a setup), seen through whole pictures of it
// (Progress) rather than a stream of events: the worker changes the picture with Set and ends it with Finish, which
// never block, so a flash's progress callbacks (which esp and nrfdfu call on the flashing goroutine and which must not
// block) can call Set directly; the screen asks for the next picture with Next, and a picture it missed is simply
// replaced by a newer one. Nothing is queued, so a slow screen never slows a flash.
//
// Every Job is finished exactly once: the backend's worker finishes it however it ends (an error, a cancel, a panic
// recovered), so Next always returns in the end.
type Job struct {
	id     uint64
	kind   JobKind
	clock  serialport.Clock
	cancel context.CancelFunc

	mu   sync.Mutex
	p    Progress
	wake chan struct{} // closed, and replaced, on every change
	done chan struct{} // closed by Finish
}

// NewJob makes a job of a kind. clock stamps each picture's Now (nil leaves it zero, as the screens' test fakes do);
// cancel, if not nil, is what Cancel calls: the cancel of the context the work runs under.
func NewJob(kind JobKind, clock serialport.Clock, cancel context.CancelFunc) *Job {
	j := &Job{id: jobIDs.Add(1), kind: kind, clock: clock, cancel: cancel, wake: make(chan struct{}),
		done: make(chan struct{})}
	j.p = Progress{Job: j.id, Kind: kind}
	if clock != nil {
		j.p.Now = clock.Now()
	}
	return j
}

// ID tells this job from every other in the program.
func (j *Job) ID() uint64 { return j.id }

// Kind is what the job does.
func (j *Job) Kind() JobKind { return j.kind }

// Set changes the picture: f gets the current one to change (append a step, move the bytes on, mark a step done) and
// must not keep its slices. It never blocks for long and does nothing once the job is finished. f may not change Job,
// Kind, Seq, Done or Outcome; Set puts them back.
func (j *Job) Set(f func(p *Progress)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.p.Done {
		return
	}
	id, kind, seq := j.p.Job, j.p.Kind, j.p.Seq
	f(&j.p)
	j.p.Job, j.p.Kind, j.p.Seq, j.p.Done, j.p.Outcome = id, kind, seq+1, false, Outcome{}
	if j.clock != nil {
		j.p.Now = j.clock.Now()
	}
	j.wakeLocked()
}

// Finish ends the job with its outcome. Only the first Finish counts. It also releases the job's context.
func (j *Job) Finish(o Outcome) {
	j.mu.Lock()
	if j.p.Done {
		j.mu.Unlock()
		return
	}
	j.p.Seq++
	j.p.Done, j.p.Outcome = true, o
	if j.clock != nil {
		j.p.Now = j.clock.Now()
	}
	j.wakeLocked()
	close(j.done)
	j.mu.Unlock()
	if j.cancel != nil {
		j.cancel()
	}
}

func (j *Job) wakeLocked() {
	close(j.wake)
	j.wake = make(chan struct{})
}

// Snapshot is the picture now, a copy the caller may keep.
func (j *Job) Snapshot() Progress {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.p.clone()
}

// Next waits until the picture is newer than seen (a Progress.Seq; 0 for the first) and returns it. A finished job's
// picture is returned at once whatever seen is, so a wait for a picture after the last never hangs (the screens drop
// a picture of a job they no longer watch, so this can't loop). When ctx ends first it returns the picture as it is
// and ctx's error.
func (j *Job) Next(ctx context.Context, seen uint64) (Progress, error) {
	for {
		j.mu.Lock()
		if j.p.Seq > seen || j.p.Done {
			p := j.p.clone()
			j.mu.Unlock()
			return p, nil
		}
		wake := j.wake
		j.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return j.Snapshot(), ctx.Err()
		}
	}
}

// Cancel asks the work to stop. The job still finishes, with Outcome.Stopped and what the board is left with.
func (j *Job) Cancel() {
	if j.cancel != nil {
		j.cancel()
	}
}

// Done is closed when the job has finished.
func (j *Job) Done() <-chan struct{} { return j.done }

// clone copies p deeply enough that neither copy can change the other.
func (p Progress) clone() Progress {
	p.Steps = slices.Clone(p.Steps)
	o := &p.Outcome
	o.Summary = slices.Clone(o.Summary)
	o.Entries = slices.Clone(o.Entries)
	if o.Board != nil {
		b := *o.Board
		b.Candidates = slices.Clone(b.Candidates)
		o.Board = &b
	}
	if o.Sent != nil {
		s := *o.Sent
		o.Sent = &s
	}
	return p
}
