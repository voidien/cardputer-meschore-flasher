package flash_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"meshflash/internal/flash"
	"meshflash/internal/serialport"
)

// Many goroutines changing the picture at once (as esp's and nrfdfu's progress callbacks and the backend's own
// steps do) while another follows it: every Seq it sees is newer than the last, and it sees Done (run with -race).
func TestJobManySets(t *testing.T) {
	clock := serialport.NewVirtualClock(nil)
	j := flash.NewJob(flash.JobFlash, clock, nil)
	got := make(chan []uint64, 1)
	go func() {
		var seqs []uint64
		var seen uint64
		for {
			p, err := j.Next(context.Background(), seen)
			if err != nil {
				t.Error(err)
				return
			}
			if p.Seq <= seen && !p.Done {
				t.Errorf("picture %d after %d", p.Seq, seen)
			}
			seqs = append(seqs, p.Seq)
			seen = p.Seq
			if p.Done {
				got <- seqs
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				j.Set(func(p *flash.Progress) {
					p.Steps = append(p.Steps, flash.Step{Text: "step"})
					p.Seq = 0 // Set puts back what f may not change
				})
				clock.Advance(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	j.Finish(flash.Outcome{OK: true, Title: "Flashed"})
	select {
	case seqs := <-got:
		if last := seqs[len(seqs)-1]; last != 8*200+1 {
			t.Errorf("last Seq %d", last)
		}
	case <-time.After(jobGuard):
		t.Fatal("the follower never saw Done")
	}
	p := j.Snapshot()
	if len(p.Steps) != 8*200 || !p.Done || p.Outcome.Title != "Flashed" {
		t.Errorf("%d steps, done %v", len(p.Steps), p.Done)
	}
}

// Next never misses Done, however Finish races it; and after Done it returns at once, whatever seen is.
func TestJobNextNeverMissesDone(t *testing.T) {
	for i := 0; i < 200; i++ {
		j := flash.NewJob(flash.JobDownload, nil, nil)
		done := make(chan flash.Progress, 1)
		go func() {
			p, _ := j.Next(context.Background(), 0)
			for !p.Done {
				p, _ = j.Next(context.Background(), p.Seq)
			}
			done <- p
		}()
		go j.Set(func(p *flash.Progress) {})
		go j.Finish(flash.Outcome{Title: "Downloaded"})
		select {
		case p := <-done:
			if p.Outcome.Title != "Downloaded" {
				t.Fatalf("%+v", p.Outcome)
			}
		case <-time.After(jobGuard):
			t.Fatal("Next missed Done")
		}
	}
	j := flash.NewJob(flash.JobSetup, nil, nil)
	j.Finish(flash.Outcome{Title: "Set up"})
	for _, seen := range []uint64{0, 1, 99} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		p, err := j.Next(ctx, seen)
		cancel()
		if err != nil || !p.Done {
			t.Errorf("Next(%d) after Done: %v %+v", seen, err, p)
		}
	}
}

// Finish counts once and releases the job's context; Set after it changes nothing; Cancel cancels.
func TestJobFinishTwice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	j := flash.NewJob(flash.JobFlash, nil, cancel)
	j.Finish(flash.Outcome{Title: "first"})
	j.Finish(flash.Outcome{Title: "second"})
	j.Set(func(p *flash.Progress) { p.Steps = append(p.Steps, flash.Step{Text: "late"}) })
	p := j.Snapshot()
	if p.Outcome.Title != "first" || len(p.Steps) != 0 || p.Seq != 1 {
		t.Errorf("%+v", p)
	}
	if ctx.Err() == nil {
		t.Error("Finish didn't release the context")
	}
	select {
	case <-j.Done():
	default:
		t.Error("Done not closed")
	}

	ctx, cancel = context.WithCancel(context.Background())
	j = flash.NewJob(flash.JobFlash, nil, cancel)
	j.Cancel()
	if ctx.Err() == nil {
		t.Error("Cancel didn't cancel")
	}
	// A wait whose context ends gives the picture as it is.
	wctx, wcancel := context.WithCancel(context.Background())
	wcancel()
	if p, err := j.Next(wctx, 5); err == nil || p.Job != j.ID() {
		t.Errorf("%v %+v", err, p)
	}
}

// A Snapshot is the caller's: changing it changes nothing in the job.
func TestJobSnapshotIsACopy(t *testing.T) {
	j := flash.NewJob(flash.JobFlash, nil, nil)
	j.Set(func(p *flash.Progress) { p.Steps = append(p.Steps, flash.Step{Text: "a"}) })
	board := flash.Port{Label: "x"}
	j.Finish(flash.Outcome{Summary: []string{"s"}, Board: &board})
	p := j.Snapshot()
	p.Steps[0].Text, p.Outcome.Summary[0], p.Outcome.Board.Label = "b", "t", "y"
	q := j.Snapshot()
	if q.Steps[0].Text != "a" || q.Outcome.Summary[0] != "s" || q.Outcome.Board.Label != "x" {
		t.Errorf("%+v", q)
	}
}
