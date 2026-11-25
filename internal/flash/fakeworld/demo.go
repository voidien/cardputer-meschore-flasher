package fakeworld

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
)

// demoPace is how much real time the demo spends per second of the world's time. The demo's files are real-sized
// (demoAppSize), so a flash takes the world about as long as a real board (an ESP32 update about 35 s at 115200
// baud, an nRF52 about 25 s), and most of that on the screen is about half a minute.
const demoPace = 0.9

// demoAppSize is a real MeshCore app's size (361 KB for v1.18.0's repeater, SPEC.md 4).
const demoAppSize = 360 << 10

// demoStep caps one real sleep, so a long virtual wait (an nRF52's 20 s confirm) never freezes the demo for long.
const demoStep = 2 * time.Second

func demo(cacheDir, version string, log io.Writer) (flash.Deps, error) {
	w := New()
	w.appSize = demoAppSize
	w.Plug(HeltecV4(Firmware{Role: nodecli.RoleRepeater, Version: "v1.17.1", Name: "Hilltop 2"}))
	w.Plug(RAK4631(Firmware{Role: "companion"}))
	d, err := w.Deps(cacheDir, version, false)
	if err != nil {
		return flash.Deps{}, err
	}
	if _, err := d.Catalog.Load(); errors.Is(err, catalog.ErrNoCatalog) {
		if err := w.saveCatalog(cacheDir, version); err != nil {
			return flash.Deps{}, err
		}
	}
	if log != nil {
		var mu sync.Mutex
		d.Logf = func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			fmt.Fprintf(log, format+"\n", args...)
		}
	}
	// Pacing: the world's time moves on in jumps (a 10 s erase is one Advance); the demo sleeps a fraction of each
	// jump in real time, on the goroutine that moved it, so the screens see a flash take a real board's time.
	var mu sync.Mutex
	last := w.clock.Now()
	w.clock.OnTick(func(now time.Time) {
		mu.Lock()
		d := now.Sub(last)
		last = now
		mu.Unlock()
		if d <= 0 {
			return
		}
		time.Sleep(min(time.Duration(float64(d)*demoPace), demoStep))
	})
	return d, nil
}
