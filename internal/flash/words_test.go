package flash

// Internal tests of what can't be reached in step with the fake world: the note a cancel during the nRF52 erase
// gives (the erase is one jump of the virtual clock), and the words.

import (
	"context"
	"errors"
	"testing"
	"time"

	"meshflash/internal/esp"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/serialport"
)

// A cancel during the nRF52 erase says the stop waits for the erase to end: nrfdfu checks between frames. The note
// gives no count of seconds, which would be stale at once: the step's own clock and Expect say how long it runs.
func TestCancelDuringEraseSaysSo(t *testing.T) {
	clock := serialport.NewVirtualClock(nil)
	ctx, cancel := context.WithCancel(context.Background())
	j := NewJob(JobFlash, clock, cancel)
	r := &flashRun{j: j, ctx: ctx, clock: clock, cur: -1}
	stop := context.AfterFunc(ctx, r.cancelNote)
	defer stop()
	i := r.start("Erasing")
	r.mu.Lock()
	r.eraseEnds = clock.Now().Add(8 * time.Second)
	r.mu.Unlock()
	clock.Advance(200 * time.Millisecond)
	seen := j.Snapshot().Seq
	j.Cancel()
	// AfterFunc writes the note on its own goroutine, through Job.Set: wait for that change as an event.
	wait, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	p, err := j.Next(wait, seen)
	if err != nil {
		t.Fatalf("no note came: %v", err)
	}
	if got := p.Steps[i].Note; got != "Stopping once the erase ends" {
		t.Errorf("note %q", got)
	}
}

// esp's and nrfdfu's Recovery split into State, one sentence, and Recovery, never repeating it.
func TestSplitRecovery(t *testing.T) {
	for _, fe := range []*esp.FlashError{
		{Stage: esp.StageConnecting, Err: esp.ErrNoBootloader},
		{Stage: esp.StageStub, Err: esp.ErrWrongChip},
		{Stage: esp.StageConnecting, Err: serialport.ErrGone},
		{Stage: esp.StageResetting, Touched: true, Err: serialport.ErrGone},
		{Stage: esp.StageVerifying, Touched: true, Err: esp.ErrVerify},
		{Stage: esp.StageVerifying, Touched: true, Err: esp.ErrNoReply},
		{Stage: esp.StageWriting, Touched: true, EraseChip: true, Err: serialport.ErrGone},
		{Stage: esp.StageWriting, Touched: true, Address: esp.AppAddress, Err: serialport.ErrGone},
	} {
		state, rec := splitRecovery(fe.Recovery())
		if state == "" || rec == "" || state[len(state)-1] != '.' || len(state)+len(rec) < len(fe.Recovery())-3 {
			t.Errorf("%v: %q | %q", fe.Err, state, rec)
		}
	}
	mh := &nrfdfu.FlashError{Stage: nrfdfu.StageFinishing, StopSent: true, Err: serialport.ErrGone}
	if _, rec := splitRecovery(mh.Recovery()); rec == "" {
		t.Errorf("MayHaveWorked: no recovery")
	}
}

func TestWords(t *testing.T) {
	for in, want := range map[string]string{
		"it runs v1.10.0; flash v1.11.0 or newer first": "It runs v1.10.0; flash v1.11.0 or newer first.",
		"Done.": "Done.",
	} {
		if got := sentence(in); got != want {
			t.Errorf("sentence(%q) = %q", in, got)
		}
	}
	if got := clause("The board went away."); got != "the board went away" {
		t.Errorf("clause %q", got)
	}
	if got := clause("SHA-256 changed."); got != "SHA-256 changed" {
		t.Errorf("clause %q", got)
	}
	if got := plain(errors.New("nodecli: reading name: nodecli: no reply")); got != "reading name: no reply" {
		t.Errorf("plain %q", got)
	}
	for n, want := range map[int]string{999: "999 bytes", 361_000: "361 KB", 1_300_000: "1.3 MB", 57_344: "57 KB"} {
		if got := sizeText(n); got != want {
			t.Errorf("sizeText(%d) = %q", n, got)
		}
	}
}
