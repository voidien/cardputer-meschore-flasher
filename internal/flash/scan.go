package flash

import (
	"context"
	"fmt"

	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// Why a bootloader session is stale (docs/phase2-design.md 4.4).
const (
	staleStarted = iota // START was written into it: a flash stopped part-way, or a DFU that was cancelled
	staleAtStart        // it was already on USB at the backend's first scan: its session may be one a stopped flash left
)

// staleDFU is a bootloader session that may already have had START: DFU is never started in it.
type staleDFU struct {
	why int
	// gone says its bootloader's port has been seen gone since the record: when it is seen again, the owner has
	// pressed reset twice and the session is fresh.
	gone bool
}

// scanPorts is Scan: the ports as the screens see them, and whether ports can be opened at all.
func (b *Backend) scanPorts() (Scan, error) {
	ports, err := b.scan()
	if err != nil {
		return Scan{}, err
	}
	s := Scan{Dialout: true}
	if b.d.Dialout != nil {
		s.Dialout, s.Hint = b.d.Dialout()
		if s.Dialout {
			s.Hint = ""
		}
	}
	for _, p := range ports {
		s.Ports = append(s.Ports, PortOf(p))
	}
	return s, nil
}

// scan lists the ports and keeps the stale sessions up to date. Every scan goes through it, the screens' and the
// backend's own (waiting for a board, a double press), so a session is cleared as soon as anything sees it end.
func (b *Backend) scan() ([]usbid.Port, error) {
	ports, err := b.d.Scan()
	if err != nil {
		return nil, err
	}
	b.note(ports)
	return ports, nil
}

// note updates the stale sessions from one scan (4.4): a bootloader at the first scan is stale; a record is cleared
// when the board's app is on USB again, or when its bootloader's port is seen gone and then back.
func (b *Backend) note(ports []usbid.Port) {
	apps, boots := map[string]bool{}, map[string]bool{}
	for _, p := range ports {
		fp := PortOf(p)
		key := fp.DraftKey()
		switch {
		case key == "":
		case fp.Kind == usbid.NRFBootloader:
			boots[key] = true
		case fp.Kind == usbid.NRFApp:
			apps[key] = true
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.scanned {
		b.scanned = true
		for key := range boots {
			b.stale[key] = staleDFU{why: staleAtStart}
		}
		return
	}
	for key, rec := range b.stale {
		switch {
		case apps[key]:
			delete(b.stale, key)
		case !boots[key]:
			rec.gone = true
			b.stale[key] = rec
		case rec.gone:
			delete(b.stale, key) // gone and back: a fresh session
		}
	}
}

// ensureScanned makes the first scan if none has been made, so a bootloader already on USB when the backend started
// is known to be stale even when the first thing asked is a probe or a flash.
func (b *Backend) ensureScanned() {
	b.mu.Lock()
	done := b.scanned
	b.mu.Unlock()
	if !done {
		_, _ = b.scan()
	}
}

// markStarted records that START is being written into p's bootloader session.
func (b *Backend) markStarted(p Port) {
	key := p.DraftKey()
	if key == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stale[key] = staleDFU{why: staleStarted}
}

// staleFor says whether p's bootloader session may already have had START, and why.
func (b *Backend) staleFor(p Port) (staleDFU, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, ok := b.stale[p.DraftKey()]
	return rec, ok && p.DraftKey() != ""
}

// lock takes the board lock, or gives up when ctx ends first.
func (b *Backend) lock(ctx context.Context) error {
	select {
	case b.board <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Backend) unlock() { <-b.board }

// What a port is opened for, told to a port that asks (purposer).
const (
	purposeFlash = "flash"
	purposeSetup = "setup"
	purposeRead  = "read"
)

// purposer is a port that wants to know what it was opened for. Only the fake world's are (they hold a port the
// backend says nothing about to a probe's rules: internal/flash/fakeworld); a real port is told nothing.
type purposer interface{ OpenedFor(purpose string) }

// open opens a tty at baud, exclusively (SPEC.md 6.1), telling a port that asks what it is for ("" for a probe).
func (b *Backend) open(path string, baud int, purpose string) (serialport.Port, error) {
	p, err := b.d.Open(path, serialport.Options{Baud: baud, Exclusive: true})
	if err != nil {
		return nil, err
	}
	if t, ok := p.(purposer); ok && purpose != "" {
		t.OpenedFor(purpose)
	}
	return p, nil
}

// touchOpener is Deps.Open for nrfdfu.Touch, which opens the port itself (at 1200 baud, exclusively): its port is a
// flash's.
func (b *Backend) touchOpener(path string, o serialport.Options) (serialport.Port, error) {
	p, err := b.d.Open(path, o)
	if err != nil {
		return nil, err
	}
	if t, ok := p.(purposer); ok {
		t.OpenedFor(purposeFlash)
	}
	return p, nil
}

// sameBoard is usbid.SameBoard, and for a port it can't follow (no useful serial, not a bridge) the same tty with
// the same identity in the same socket: the screens' rule for the status mark (internal/ui's listed).
func sameBoard(a, b usbid.Port) bool {
	if usbid.SameBoard(a, b) {
		return true
	}
	if usefulSerial(usbid.NormalSerial(a.Serial)) || usefulSerial(usbid.NormalSerial(b.Serial)) {
		return false
	}
	return a.Name == b.Name && a.VID == b.VID && a.PID == b.PID && a.USBPath != "" && a.USBPath == b.USBPath
}

// findBoard scans and finds the board that was on was, as it shows now (its tty may have changed): the start of
// every Flash, Setup and ReadNode, since the port the screens give is what Board showed, maybe minutes ago.
func (b *Backend) findBoard(was Port) (Port, bool, error) {
	ports, err := b.scan()
	if err != nil {
		return Port{}, false, fmt.Errorf("reading the USB ports: %w", err)
	}
	var found []usbid.Port
	for _, p := range ports {
		if sameBoard(was.Port, p) {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return Port{}, false, nil
	}
	for _, p := range found {
		if p.Name == was.Name {
			return PortOf(p), true, nil
		}
	}
	return PortOf(found[0]), true, nil
}

// find is a WaitForPort search: the first scanned port of the board was that match says is the one wanted.
func (b *Backend) find(was Port, match func(usbid.Port) bool) func() (string, bool, error) {
	return func() (string, bool, error) {
		ports, err := b.scan()
		if err != nil {
			return "", false, err
		}
		for _, q := range ports {
			if sameBoard(was.Port, q) && match(q) {
				return q.Dev, true, nil
			}
		}
		return "", false, nil
	}
}

// portAt is the scanned port on path now, as the screens see it.
func (b *Backend) portAt(path string) (Port, bool) {
	ports, err := b.scan()
	if err != nil {
		return Port{}, false
	}
	for _, q := range ports {
		if q.Dev == path {
			return PortOf(q), true
		}
	}
	return Port{}, false
}
