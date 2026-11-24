package nodecli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"meshflash/internal/serialport"
)

// The roles `get role` reports (examples/simple_repeater/MyMesh.h:77, simple_room_server/MyMesh.h:80).
const (
	RoleRepeater   = "repeater"
	RoleRoomServer = "room_server"
)

// Info is what a node says about itself.
type Info struct {
	// Version is what `ver` said.
	Version Version
	// Role is what `get role` said: RoleRepeater or RoleRoomServer for the firmware setup knows.
	Role string
	// Board is what `board` said: the firmware's own name for the board ("Heltec V3", "Ikoka Stick-E22-30dBm
	// (Xiao_nrf52)"), not the catalog's. It is for showing, and for the TX power limit (TxLimit); empty on firmware
	// too old to have the command.
	Board string
}

// Settable says whether setup can configure this node: a repeater or room server on v1.11.0 or newer, the versions
// the live catalog offers and the research covers (docs/meshcore-flashing.md 7.5). Newer versions are taken to
// behave as v1.18.0.
func (i Info) Settable() error {
	// The version first: firmware before v1.4.2 has no `get role`, so its role is empty, and the fix is the same.
	if !i.Version.AtLeast(1, 11, 0) {
		return fmt.Errorf("%w: it runs %s; flash v1.11.0 or newer first", ErrUnsupported, i.Version)
	}
	if i.Role != RoleRepeater && i.Role != RoleRoomServer {
		return fmt.Errorf("%w: it says its role is %q; only repeater and room-server firmware can be set up here", ErrUnsupported, i.Role)
	}
	return nil
}

var (
	// ErrNotCLI means nothing answered `ver` the way a MeshCore repeater or room server does: the board runs
	// companion, KISS or other firmware, sits in a bootloader, or isn't running at all.
	ErrNotCLI = errors.New("nodecli: no MeshCore repeater or room-server console answered")
	// ErrUnsupported means the node answered but setup can't configure it (another role, or firmware older than
	// v1.11.0).
	ErrUnsupported = errors.New("nodecli: this node can't be set up here")
	// ErrChanged means that after the reboot the node runs other firmware, or another radio, than was set.
	ErrChanged = errors.New("nodecli: the node came back different")
	// ErrStuck means the node echoed `ver` but never answered it. A repeater or room server left in `region load`
	// mode does that: it swallows every line until a blank one (docs/meshcore-flashing.md 7.10). Probe doesn't end
	// the mode itself, because ending it changes the node (the regions table it routes with becomes the one being
	// loaded, often empty, until it restarts: see conn.command). Restarting the node ends the mode and keeps its
	// saved regions, so the screen says: press the board's reset button (or unplug it if it has no battery) and try
	// again. Run does end the mode, with a bare CR, since a setup ends with a reboot anyway.
	ErrStuck = errors.New("nodecli: the node echoes what it is sent but answers nothing")
)

const (
	// QuickProbe is how long Probe keeps asking: firmware that isn't the CLI never answers, and the Board screen
	// shouldn't wait on it long (SPEC.md 4: "others stay silent within 2 s").
	QuickProbe = 2 * time.Second
	// ConfirmWait is how long Confirm keeps asking `ver` while the node reboots. The firmware waits 1 s after
	// starting its serial port and then starts the radio and file system (simple_repeater/main.cpp:39-40); a node's
	// first boot after a full ESP32 erase formats its file system and takes longer (UNVERIFIED how long:
	// docs/meshcore-flashing.md 7.10), so after a full wipe the caller uses ProbeFor with a longer wait first.
	ConfirmWait = 20 * time.Second
	// settle is how long the probe first drops whatever arrives: an ESP32 ROM banner or "Repeater ID: …" after the
	// port opened. Anything later is passed over by findReply anyway.
	settle = 250 * time.Millisecond
	// verEvery is how often ver is sent again while nothing answers (7.10).
	verEvery = time.Second
)

// Probe asks the node on an open port what it is: `ver`, then `get role` and `board`. It sends nothing else, and
// nothing that changes the node: not even the region-load escape (ErrStuck). A board that isn't running a repeater
// or room server gives ErrNotCLI after about 2 s.
//
// The caller opens the port raw at 115200 with DTR and RTS asserted, as Linux does on open: nRF52 (TinyUSB)
// firmware prints nothing while DTR is low (7.2).
func Probe(ctx context.Context, port serialport.Port, clock serialport.Clock) (Info, error) {
	return ProbeFor(ctx, port, clock, QuickProbe)
}

// ProbeFor is Probe that keeps asking for up to wait, for a node that is still starting (after a flash or a
// reboot).
func ProbeFor(ctx context.Context, port serialport.Port, clock serialport.Clock, wait time.Duration) (Info, error) {
	c := newConn(ctx, port, clock)
	if err := c.discard(settle); err != nil {
		return Info{}, err
	}
	v, err := c.askVersion(wait)
	if err != nil {
		return Info{}, err
	}
	info := Info{Version: v}
	// get role exists from v1.4.2 (CommonCLI.cpp:753-754).
	if !v.AtLeast(1, 4, 2) {
		return info, nil
	}
	reply, err := c.ask("get role")
	if err != nil {
		return info, fmt.Errorf("nodecli: asking the node its role: %w", err)
	}
	role, ok := strings.CutPrefix(reply, "> ")
	if !ok {
		return info, fmt.Errorf("nodecli: the node answered %q when asked its role", reply)
	}
	info.Role = role
	// board exists from repeater v1.9.1 and room server v1.10.0 (7.3); older firmware answers "Unknown command".
	if (role == RoleRepeater && v.AtLeast(1, 9, 1)) || (role == RoleRoomServer && v.AtLeast(1, 10, 0)) {
		board, err := c.ask("board")
		if err != nil {
			return info, fmt.Errorf("nodecli: asking the node its board: %w", err)
		}
		info.Board = board
	}
	return info, nil
}

// askVersion sends ver every second until a version comes back or wait runs out (7.10: "until a '  -> v' line
// arrives"). A reply that isn't a version doesn't end it at once: a byte left in the node's line buffer (noise on
// the line at power-up, half a line typed in another program) turns the first `ver` into "xver", which the node
// answers "Unknown command", and the next `ver` goes into an empty buffer. When wait runs out, a node that echoed
// but never answered is ErrStuck; anything else is ErrNotCLI.
func (c *conn) askVersion(wait time.Duration) (Version, error) {
	deadline := c.clock.Now().Add(wait)
	echoed, other := false, ""
	for {
		if err := c.send("ver"); err != nil {
			return Version{}, err
		}
		next := c.clock.Now().Add(verEvery)
		if next.After(deadline) {
			next = deadline
		}
		for {
			reply, used, ok, e := findReply(c.buf, "ver")
			echoed = echoed || e
			if ok {
				c.buf = c.buf[used:]
				v, err := ParseVersion(reply)
				if err == nil {
					return v, nil
				}
				other = reply
				continue // a second echo and reply may be in the buffer already
			}
			if !c.clock.Now().Before(next) {
				break
			}
			if err := c.fill(next); err != nil {
				return Version{}, err
			}
		}
		if !c.clock.Now().Before(deadline) {
			switch {
			case other != "":
				return Version{}, fmt.Errorf("%w (it answered ver with %q)", ErrNotCLI, other)
			case echoed:
				return Version{}, ErrStuck
			}
			return Version{}, ErrNotCLI
		}
	}
}

// Want is what Confirm expects of the node after the reboot.
type Want struct {
	// Version is the firmware the node ran before the reboot (Info.Version).
	Version Version
	// Radio is what setup sent (Settings.Radio); it takes effect only at the reboot.
	Radio Radio
}

// Confirm checks the node after setup's reboot, on the port the caller reopened: it asks `ver` until the node
// answers (up to ConfirmWait), then `get radio`, and compares both with want. It sends nothing that changes the
// node.
//
// `get radio` prints the saved setting, not the radio the chip runs (CommonRadioPrefs.cpp:32-38 reads the prefs),
// so this proves the firmware is the same and the radio setting survived a restart; that the node did restart is
// shown by the caller seeing the port go and come back (nRF52 and native-USB ESP32 re-enumerate), not by Confirm.
// A board behind a USB-serial chip keeps its port through a reboot, and there Confirm can't tell a node that
// rebooted from one that didn't (docs/meshcore-flashing.md 10.3).
func Confirm(ctx context.Context, port serialport.Port, clock serialport.Clock, want Want) error {
	c := newConn(ctx, port, clock)
	if err := c.discard(settle); err != nil {
		return err
	}
	v, err := c.askVersion(ConfirmWait)
	if err != nil {
		return fmt.Errorf("nodecli: after the reboot: %w", err)
	}
	if !v.Same(want.Version) {
		return fmt.Errorf("%w: it runs %s, not %s", ErrChanged, v.Build(), want.Version.Build())
	}
	reply, err := c.ask("get radio")
	if err != nil {
		return fmt.Errorf("nodecli: after the reboot, asking the radio: %w", err)
	}
	got, err := parseRadioReply(reply)
	if err != nil {
		return fmt.Errorf("nodecli: after the reboot: %w", err)
	}
	if !got.Near(want.Radio) {
		return fmt.Errorf("%w: its radio is %s, not %s", ErrChanged, got.Display(), want.Radio.Display())
	}
	return nil
}

// ReadSettings reads the node's current settings, for the setup form's defaults: name, radio, TX power, path hash
// size (v1.14.0+), location and local advert interval, and on a room server the guest password. It sends only get
// commands, without the region-load escape. The admin password can't be read back (7.3), so it stays empty. A new
// node's 2-minute local advert reads as off (0): saving any setting turns it off (7.3).
func ReadSettings(ctx context.Context, port serialport.Port, clock serialport.Clock, info Info) (Settings, error) {
	if err := info.Settable(); err != nil {
		return Settings{}, err
	}
	c := newConn(ctx, port, clock)
	var s Settings
	get := func(key string) (string, error) {
		reply, err := c.ask("get " + key)
		if err != nil {
			return "", fmt.Errorf("nodecli: reading %s: %w", key, err)
		}
		v, ok := strings.CutPrefix(reply, "> ")
		if !ok {
			if key == "guest.password" {
				return "", fmt.Errorf("nodecli: reading %s: unexpected reply", key)
			}
			return "", fmt.Errorf("nodecli: reading %s: the node answered %q", key, reply)
		}
		return v, nil
	}
	var err error
	if s.Name, err = get("name"); err != nil {
		return s, err
	}
	reply, err := c.ask("get radio")
	if err != nil {
		return s, fmt.Errorf("nodecli: reading radio: %w", err)
	}
	if s.Radio, err = parseRadioReply(reply); err != nil {
		return s, err
	}
	s.Radio = s.Radio.Clean()
	tx, err := get("tx")
	if err != nil {
		return s, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(tx))
	if err != nil {
		return s, fmt.Errorf("nodecli: reading tx: %q isn't a number", tx)
	}
	// v1.11.0, v1.12.0 and v1.18.0 print a negative setting as 256+n (7.4); no real setting is above 127.
	if n > 127 && n < 256 {
		n -= 256
	}
	s.TxDBm = n
	if info.Version.AtLeast(1, 14, 0) {
		mode, err := get("path.hash.mode")
		if err != nil {
			return s, err
		}
		m, err := strconv.Atoi(strings.TrimSpace(mode))
		if err != nil || m < 0 || m > 2 {
			return s, fmt.Errorf("nodecli: reading path.hash.mode: %q isn't 0, 1 or 2", mode)
		}
		s.PathHashBytes = m + 1
	}
	for _, f := range []struct {
		key string
		to  **float64
	}{{"lat", &s.Lat}, {"lon", &s.Lon}} {
		v, err := get(f.key)
		if err != nil {
			return s, err
		}
		x, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
			return s, fmt.Errorf("nodecli: reading %s: %q isn't a number", f.key, v)
		}
		x = shortestPrinted(x, strings.TrimSpace(v))
		*f.to = &x
	}
	adv, err := get("advert.interval")
	if err != nil {
		return s, err
	}
	mins, err := strconv.Atoi(strings.TrimSpace(adv))
	if err != nil {
		return s, fmt.Errorf("nodecli: reading advert.interval: %q isn't a number", adv)
	}
	if mins > 0 && mins < minAdvertInterval {
		// A new node's 2-minute local advert: the first setting saved turns it off (CommonCLI.cpp:160-168), so off
		// is what the node will have, and the form's default must pass Check (60 to 240 or off).
		mins = 0
	}
	s.AdvertInterval = &mins
	if info.Role == RoleRoomServer {
		g, err := get("guest.password")
		if err != nil {
			return s, err
		}
		s.GuestPassword = Secret(g)
	}
	return s, nil
}
