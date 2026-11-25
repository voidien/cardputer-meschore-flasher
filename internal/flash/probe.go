package flash

import (
	"context"
	"errors"

	"meshflash/internal/nodecli"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// probe is Probe: ver, get role and board; a repeater's or room server's name with read-only gets; a bootloader
// answered without opening it.
func (b *Backend) probe(ctx context.Context, p Port) Node {
	if p.Kind == usbid.NRFBootloader {
		return b.bootloaderNode(p)
	}
	if err := b.lock(ctx); err != nil {
		return Node{Port: p, Problem: Failed, Reason: "stopped"}
	}
	defer b.unlock()
	port, err := b.open(p.Dev, 115200, "")
	if err != nil {
		return openFailed(p, err)
	}
	defer port.Close()
	info, err := nodecli.Probe(ctx, port, b.d.Clock)
	if err != nil {
		return probeFailed(p, err)
	}
	n := classify(p, info)
	if n.Problem != NoProblem {
		return n
	}
	// The name, for the Board screen (owner question 1): read as a repeater's settings whatever the role, so a room
	// server is never asked get guest.password. A failure here costs only the name.
	as := info
	as.Role = nodecli.RoleRepeater
	if cur, err := nodecli.ReadSettings(ctx, port, b.d.Clock, as); err == nil {
		cur.GuestPassword = ""
		n.Current, n.HasCurrent = cur, true
	}
	return n
}

// readNode is ReadNode: the board found again, probed (longer on its first boot), and every setting read, the
// room server's guest password included, for the setup form.
func (b *Backend) readNode(ctx context.Context, p Port, firstBoot bool) Node {
	if err := b.lock(ctx); err != nil {
		return Node{Port: p, Problem: Failed, Reason: "stopped"}
	}
	defer b.unlock()
	cur, ok, err := b.findBoard(p)
	switch {
	case err != nil:
		return Node{Port: p, Problem: Failed, Reason: clause(err.Error())}
	case !ok:
		return Node{Port: p, Problem: Gone}
	case cur.Kind == usbid.NRFBootloader:
		return b.bootloaderNode(cur)
	}
	port, err := b.open(cur.Dev, 115200, purposeRead)
	if err != nil {
		return openFailed(cur, err)
	}
	defer port.Close()
	wait := nodecli.QuickProbe
	if firstBoot {
		wait = FirstBootWait
	}
	info, err := nodecli.ProbeFor(ctx, port, b.d.Clock, wait)
	if err != nil {
		return probeFailed(cur, err)
	}
	n := classify(cur, info)
	if n.Problem != NoProblem {
		return n
	}
	settings, err := nodecli.ReadSettings(ctx, port, b.d.Clock, info)
	if err != nil {
		n := probeFailed(cur, err)
		if n.Problem == Failed {
			// ReadSettings quotes the node's replies; none of them is shown or logged (Deps.Logf's rules).
			n.Reason = "it didn't answer every read of its settings"
		}
		n.Info = info
		return n
	}
	n.Current, n.HasCurrent = settings, true
	return n
}

// classify says whether a node that answered can be set up: MeshCore's repeater or room server, not ZephCore.
func classify(p Port, info nodecli.Info) Node {
	n := Node{Port: p, Info: info}
	switch {
	case isZephCore(info):
		n.Problem, n.Reason = NotSettable, zephCoreReason
	case info.Settable() != nil:
		n.Problem, n.Reason = NotSettable, settableReason(info.Settable())
	}
	return n
}

// bootloaderNode is a bootloader, never opened: ready to flash, or what to do first (4.4).
func (b *Backend) bootloaderNode(p Port) Node {
	b.ensureScanned()
	n := Node{Port: p, Problem: InBootloader}
	if !p.InBootloader() {
		n.Reason = unconfirmed
		return n
	}
	if rec, ok := b.staleFor(p); ok {
		n.Reason = staleReason
		if rec.why == staleAtStart {
			n.Reason = atStartReason
		}
	}
	return n
}

// openFailed says why a port couldn't be opened.
func openFailed(p Port, err error) Node {
	n := Node{Port: p}
	switch {
	case errors.Is(err, serialport.ErrPermission):
		n.Problem, n.Reason = NoPermission, "no permission to open it"
	case errors.Is(err, serialport.ErrBusy):
		n.Problem, n.Reason = Busy, "another program has it open"
	case errors.Is(err, serialport.ErrGone):
		n.Problem = Gone
	default:
		n.Problem, n.Reason = Failed, clause(plain(err))
	}
	return n
}

// probeFailed says why a probe got no repeater or room server.
func probeFailed(p Port, err error) Node {
	n := Node{Port: p}
	switch {
	case errors.Is(err, nodecli.ErrNotCLI):
		n.Problem = Silent
	case errors.Is(err, nodecli.ErrStuck):
		n.Problem = Stuck
		n.Reason = "it echoes but doesn't answer: press its reset button (or unplug it if it has no battery) and press r"
	case errors.Is(err, serialport.ErrGone):
		n.Problem = Gone
	case stopped(err):
		n.Problem, n.Reason = Failed, "stopped"
	default:
		n.Problem, n.Reason = Failed, clause(plain(err))
	}
	return n
}
