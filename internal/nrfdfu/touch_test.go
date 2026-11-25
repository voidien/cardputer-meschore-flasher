package nrfdfu

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

// failDTR is a port whose DTR can't be dropped.
type failDTR struct {
	serialport.Port
	err error
}

func (p failDTR) SetDTR(bool) error { return p.err }

func TestTouchOpenFails(t *testing.T) {
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	open := func(string, serialport.Options) (serialport.Port, error) {
		return nil, fmt.Errorf("%w: /dev/ttyACM0", serialport.ErrPermission)
	}
	err := Touch(open, "/dev/ttyACM0", clock)
	if !errors.Is(err, serialport.ErrPermission) {
		t.Fatalf("got %v", err)
	}
	if len(log.Events()) != 0 {
		t.Errorf("did something anyway: %v", log.Events())
	}
}

// TestTouchDTRFails: when DTR can't be dropped the port is still closed at 1200 baud (the reference's own touch,
// which works when nothing else holds the port) and the full wait made, and the error is returned.
func TestTouchDTRFails(t *testing.T) {
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	app := &FakeApp{}
	var fake *serialport.Fake
	open := func(path string, o serialport.Options) (serialport.Port, error) {
		fake = serialport.NewFake(app, clock, log, o)
		app.Port = fake
		return failDTR{Port: fake, err: errors.New("inappropriate ioctl")}, nil
	}
	err := Touch(open, "/dev/ttyACM0", clock)
	if err == nil || !strings.Contains(err.Error(), "couldn't drop DTR") {
		t.Fatalf("got %v", err)
	}
	if _, _, _, closed := fake.State(); !closed {
		t.Error("left open")
	}
	compareSteps(t, goSteps(log), nodeSteps(loadRecording(t, testdata("touch.jsonl"))))
	if !app.Reset() {
		t.Error("the close at 1200 baud didn't reset the app")
	}
}

// TestTouchBoardGoneAtDTR: the board resetting under the DTR drop is the touch working.
func TestTouchBoardGoneAtDTR(t *testing.T) {
	clock := serialport.NewVirtualClock(nil)
	open := func(path string, o serialport.Options) (serialport.Port, error) {
		f := serialport.NewFake(nil, clock, nil, o)
		return failDTR{Port: f, err: fmt.Errorf("%w: set a modem line", serialport.ErrGone)}, nil
	}
	if err := Touch(open, "/dev/ttyACM0", clock); err != nil {
		t.Fatal(err)
	}
}

// TestTouchBootloader: a board already in its bootloader ignores the touch (no line-state handler).
func TestTouchBootloader(t *testing.T) {
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	boot, _ := NewFakeBootloader("ok")
	var fake *serialport.Fake
	open := func(path string, o serialport.Options) (serialport.Port, error) {
		fake = serialport.NewFake(boot, clock, log, o)
		return fake, nil
	}
	if err := Touch(open, "/dev/ttyACM0", clock); err != nil {
		t.Fatal(err)
	}
	if _, _, _, closed := fake.State(); !closed {
		t.Error("left open")
	}
}

func TestWaitForPort(t *testing.T) {
	t.Run("found on the third look", func(t *testing.T) {
		log := &serialport.Log{}
		clock := serialport.NewVirtualClock(log)
		looks := 0
		path, err := WaitForPort(context.Background(), func() (string, bool, error) {
			looks++
			return "/dev/ttyACM1", looks == 3, nil
		}, clock, 10*time.Second)
		if path != "/dev/ttyACM1" || err != nil || looks != 3 {
			t.Fatalf("%q, %v after %d looks", path, err, looks)
		}
		if got := sleeps(log); got != "250 250" {
			t.Errorf("sleeps %s", got)
		}
	})
	t.Run("never: a look every 250 ms, the last at the deadline", func(t *testing.T) {
		log := &serialport.Log{}
		clock := serialport.NewVirtualClock(log)
		start := clock.Now()
		var at []time.Duration
		_, err := WaitForPort(context.Background(), func() (string, bool, error) {
			at = append(at, clock.Now().Sub(start))
			return "", false, nil
		}, clock, 600*time.Millisecond)
		if !errors.Is(err, ErrPortTimeout) {
			t.Fatalf("got %v", err)
		}
		if fmt.Sprint(at) != "[0s 250ms 500ms 600ms]" || sleeps(log) != "250 250 100" {
			t.Errorf("looked at %v, slept %s", at, sleeps(log))
		}
	})
	t.Run("the bootloader's 10 s", func(t *testing.T) {
		clock := serialport.NewVirtualClock(nil)
		start := clock.Now()
		looks := 0
		_, err := WaitForPort(context.Background(), func() (string, bool, error) { looks++; return "", false, nil }, clock, 10*time.Second)
		if !errors.Is(err, ErrPortTimeout) || looks != 41 || clock.Now().Sub(start) != 10*time.Second {
			t.Errorf("%v after %d looks and %v", err, looks, clock.Now().Sub(start))
		}
	})
	t.Run("find's error ends it", func(t *testing.T) {
		boom := errors.New("two boards with the same serial")
		_, err := WaitForPort(context.Background(), func() (string, bool, error) { return "", false, boom }, serialport.NewVirtualClock(nil), time.Second)
		if !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("stopped", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		looks := 0
		_, err := WaitForPort(ctx, func() (string, bool, error) {
			looks++
			if looks == 2 {
				cancel()
			}
			return "", false, nil
		}, serialport.NewVirtualClock(nil), 10*time.Second)
		if !errors.Is(err, context.Canceled) || looks != 2 {
			t.Fatalf("%v after %d looks", err, looks)
		}
	})
}

// sleeps lists the recorded sleeps in milliseconds.
func sleeps(log *serialport.Log) string {
	var s []string
	for _, e := range log.Events() {
		if e.Ev == "sleep" {
			s = append(s, fmt.Sprint(e.Ms))
		}
	}
	return strings.Join(s, " ")
}

// TestTouchThenFlash is the whole sequence against fakes: touch the app, wait for the bootloader, flash, wait for
// the app to come back.
func TestTouchThenFlash(t *testing.T) {
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	app := &FakeApp{}
	open := func(path string, o serialport.Options) (serialport.Port, error) {
		f := serialport.NewFake(app, clock, log, o)
		app.Port = f
		return f, nil
	}
	if err := Touch(open, "/dev/ttyACM0", clock); err != nil {
		t.Fatal(err)
	}
	// The bootloader enumerates 2 s after the touch began (the 1.6 s of the touch, then 0.4 s more).
	start := clock.Now()
	bootAt := start.Add(400 * time.Millisecond)
	path, err := WaitForPort(context.Background(), func() (string, bool, error) {
		return "/dev/ttyACM0", app.Reset() && !clock.Now().Before(bootAt), nil
	}, clock, 10*time.Second)
	if err != nil || path != "/dev/ttyACM0" {
		t.Fatalf("%q, %v", path, err)
	}
	boot, _ := NewFakeBootloader("ok")
	port := serialport.NewFake(boot, clock, log, serialport.Options{Baud: DFUBaud})
	pkg := readPackage(t, testdata("tiny-1028.zip"))
	if err := Flash(context.Background(), port, clock, pkg, nil); err != nil {
		t.Fatal(err)
	}
	port.Close()
	if !boot.Result().Valid {
		t.Errorf("%+v", boot.Result())
	}
	if got := sleeps(log); got != "100 1500 250 250 500 102.4 102.4" {
		t.Errorf("sleeps %s", got)
	}
}

// stopAtSleep is a clock that stops a context at its at-th Sleep, as an owner's stop landing in that wait.
type stopAtSleep struct {
	serialport.Clock
	stop   func()
	at, n  int
	sleeps []time.Duration
}

func (c *stopAtSleep) Sleep(d time.Duration) {
	c.n++
	c.sleeps = append(c.sleeps, d)
	if c.n == c.at {
		c.stop()
	}
	c.Clock.Sleep(d)
}

// TouchContext stopped before DTR drops sets the line back to 115200 first, so the close resets nothing and the tty
// isn't left at 1200; stopped in the wait after, it returns at once, the board touched. Its waits look at the context
// every PollInterval at most.
func TestTouchContextStops(t *testing.T) {
	for _, tc := range []struct {
		name    string
		at      int
		touched bool
	}{{"before DTR drops", 1, false}, {"in the wait after", 2, true}} {
		t.Run(tc.name, func(t *testing.T) {
			log := &serialport.Log{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			vclock := serialport.NewVirtualClock(log)
			clock := &stopAtSleep{Clock: vclock, stop: cancel, at: tc.at}
			app := &FakeApp{}
			var fake *serialport.Fake
			open := func(path string, o serialport.Options) (serialport.Port, error) {
				fake = serialport.NewFake(app, vclock, log, o)
				app.Port = fake
				return fake, nil
			}
			err := TouchContext(ctx, open, "/dev/ttyACM0", clock)
			if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTouchStopped) == tc.touched {
				t.Fatalf("got %v", err)
			}
			if app.Reset() != tc.touched {
				t.Errorf("the app reset: %v", app.Reset())
			}
			if _, _, baud, closed := fake.State(); !closed || !tc.touched && baud != 115200 {
				t.Errorf("closed %v at %d baud", closed, baud)
			}
			for _, d := range clock.sleeps {
				if d > PollInterval {
					t.Errorf("a wait of %v doesn't look at the context in time", d)
				}
			}
			if len(clock.sleeps) != tc.at {
				t.Errorf("waited %v after the stop", clock.sleeps[tc.at:])
			}
		})
	}
}
