package nodecli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"meshflash/internal/serialport"
)

// The line protocol of the MeshCore serial console (docs/meshcore-flashing.md 7.2), as MeshCore's own setup tool
// speaks it: config.meshcore.io lib/serial-cli.js (aff42ad8, MIT, Copyright (c) 2026 Rastislav Vysoky), modified as
// the package comment lists. A command is written with a CR; the node echoes it (the repeater every byte but LF,
// then "\n" after the CR; the room server every byte as it comes) and answers with "  -> " + reply + "\r\n", or
// prints nothing for a command with an empty reply (reboot).

const (
	// ReplyTimeout is how long a command waits for its reply, as the config tool (serial-cli.js:27).
	ReplyTimeout = 5 * time.Second
	// MaxCommand is the most bytes a command may have, without its CR. The repeater takes 158 and the room server
	// 150, but `set radio` copies its argument into a 132-byte stack buffer with strcpy (CommonRadioPrefs.cpp:40-41),
	// so a cap well below both keeps any command from overflowing it (7.10).
	MaxCommand = 128
	// commandGap is the pause between one reply and the next command, as the config tool (serial-cli.js:28).
	commandGap = 100 * time.Millisecond
	// pollEvery is the longest single wait on the port, so a deadline or a cancelled context is noticed in time.
	pollEvery = 100 * time.Millisecond
	// maxBuffer bounds what is kept while looking for a reply, against a node that never stops printing.
	maxBuffer = 64 << 10
)

// replyMark starts every reply line.
const replyMark = "  -> "

var (
	// ErrNoReply means the node didn't answer a command in time.
	ErrNoReply = errors.New("nodecli: no reply from the node")
	// ErrTooLong means a command was refused before sending because it is longer than MaxCommand bytes.
	ErrTooLong = errors.New("nodecli: command too long to send")
	// ErrBadCommand means a command was refused before sending because it holds a CR, LF or NUL, which would end
	// or cut the line early.
	ErrBadCommand = errors.New("nodecli: command holds a line break")
)

// conn is one conversation with a node over an open port. It keeps what has been received and not yet used.
type conn struct {
	ctx    context.Context
	port   serialport.Port
	clock  serialport.Clock
	buf    []byte
	sent   bool // a command has been sent, so the next one waits commandGap first
	writes int  // commands written, so Run knows whether a failed step reached the node
}

func newConn(ctx context.Context, port serialport.Port, clock serialport.Clock) *conn {
	if ctx == nil {
		ctx = context.Background()
	}
	if clock == nil {
		clock = serialport.RealClock
	}
	return &conn{ctx: ctx, port: port, clock: clock}
}

// checkCommand refuses a command the node would read wrongly: too long, or holding a line break or NUL.
func checkCommand(cmd string) error {
	if len(cmd) > MaxCommand {
		return ErrTooLong
	}
	if strings.ContainsAny(cmd, "\r\n\x00") {
		return ErrBadCommand
	}
	return nil
}

// send writes cmd and its CR in one write: the room server reads whatever has arrived as one line, so two commands
// in one write would merge, and one command split across writes is harmless but pointless.
func (c *conn) send(cmd string) error {
	if err := checkCommand(cmd); err != nil {
		return err
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if c.sent {
		c.clock.Sleep(commandGap)
	}
	c.sent = true
	if _, err := c.port.Write([]byte(cmd + "\r")); err != nil {
		return fmt.Errorf("nodecli: writing to the node: %w", err)
	}
	c.writes++
	return nil
}

// fill waits for more bytes, at most until deadline and at most pollEvery. A quiet port is not an error.
func (c *conn) fill(deadline time.Time) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	wait := deadline.Sub(c.clock.Now())
	if wait > pollEvery {
		wait = pollEvery
	}
	if wait <= 0 {
		return nil
	}
	var tmp [512]byte
	n, err := c.port.Read(tmp[:], wait)
	if n > 0 {
		c.buf = append(c.buf, tmp[:n]...)
		if len(c.buf) > maxBuffer {
			// Keep the tail: an echo and its reply are short, and they come last.
			c.buf = append([]byte(nil), c.buf[len(c.buf)-maxBuffer/4:]...)
		}
	}
	if err != nil && !errors.Is(err, serialport.ErrTimeout) {
		return fmt.Errorf("nodecli: reading from the node: %w", err)
	}
	return nil
}

// discard drops whatever the node sends for d: a boot banner, the ESP32 ROM's chatter after the port opened.
func (c *conn) discard(d time.Duration) error {
	_ = c.port.FlushInput()
	deadline := c.clock.Now().Add(d)
	for c.clock.Now().Before(deadline) {
		if err := c.fill(deadline); err != nil {
			return err
		}
	}
	c.buf = c.buf[:0]
	return nil
}

// findReply looks in b for the reply to cmd: the first "  -> " after cmd's echo (cmd and its CR), up to the next CR
// LF. It reports the reply, how many bytes of b it used, whether it is complete, and whether the echo was seen.
// Anything before the echo (a boot banner, a log line, the leftovers of an earlier command) is passed over, so a
// stale reply is never taken for this command's. For the bare CR of the region-load escape, cmd is "".
//
// It follows serial-cli.js's checkForResponse (:228-303), except that the echo is required: the config tool falls
// back to the start of its buffer when it sees no echo.
func findReply(b []byte, cmd string) (reply string, used int, ok, echoed bool) {
	echo := bytes.Index(b, []byte(cmd+"\r"))
	if echo < 0 {
		return "", 0, false, false
	}
	start := echo + len(cmd) + 1
	mark := bytes.Index(b[start:], []byte(replyMark))
	if mark < 0 {
		return "", 0, false, true
	}
	from := start + mark + len(replyMark)
	end := bytes.Index(b[from:], []byte("\r\n"))
	if end < 0 {
		return "", 0, false, true
	}
	return string(b[from : from+end]), from + end + 2, true, true
}

// await waits up to timeout for the reply to cmd, which has been sent.
func (c *conn) await(cmd string, timeout time.Duration) (string, error) {
	deadline := c.clock.Now().Add(timeout)
	for {
		if reply, used, ok, _ := findReply(c.buf, cmd); ok {
			c.buf = c.buf[used:]
			return reply, nil
		}
		if !c.clock.Now().Before(deadline) {
			return "", ErrNoReply
		}
		if err := c.fill(deadline); err != nil {
			return "", err
		}
	}
}

// exchange sends cmd and waits for its reply. What was received before is dropped first: one command is in flight
// at a time, so nothing earlier can be this command's answer.
func (c *conn) exchange(cmd string, timeout time.Duration) (string, error) {
	c.buf = c.buf[:0]
	if err := c.send(cmd); err != nil {
		return "", err
	}
	return c.await(cmd, timeout)
}

// ask is exchange with the usual 5 s wait and no escape: what Probe, ReadSettings and Confirm use, since they must
// not change the node.
func (c *conn) ask(cmd string) (string, error) { return c.exchange(cmd, ReplyTimeout) }

// command is exchange with the region-load escape (7.10), for Run only. After `region load` a node swallows every
// line, echo and all but with no reply, until a blank one; setup never sends `region load`, but a node left in that
// mode looks like one that times out on everything. So when a command times out with no "  -> " after its echo,
// one bare CR is sent, and cmd once more if the CR was answered. Outside load mode the node answers the CR with
// "Unknown command" (its empty line matches no command). In load mode the CR ends the mode with "OK - loaded N
// regions", and that is a change: `region load` starts an empty table (RegionMap::resetFrom sets num_regions = 0,
// src/helpers/RegionMap.h:52) and the blank line makes it the table the node runs on (region_map = temp_map,
// simple_repeater/MyMesh.cpp:1199-1205; room server :899-905), so until the node restarts it routes with only the
// regions loaded since (none, after setup's lines, which have no indent). Nothing is saved (only `region save`
// writes the table), and the reboot that ends setup loads the saved table again. docs/meshcore-flashing.md 7.10
// says the map is unchanged; the source says otherwise.
func (c *conn) command(cmd string) (string, error) {
	reply, err := c.exchange(cmd, ReplyTimeout)
	if !errors.Is(err, ErrNoReply) || answering(c.buf, cmd) {
		// Answered, or failed some other way, or a reply mark came and its line never ended: that node is
		// answering, not swallowing lines, and a bare CR can't help.
		return reply, err
	}
	if err := c.escape(ReplyTimeout); err != nil {
		return "", err
	}
	return c.exchange(cmd, ReplyTimeout)
}

// answering says whether b holds cmd's echo followed by the start of a reply.
func answering(b []byte, cmd string) bool {
	echo := bytes.Index(b, []byte(cmd+"\r"))
	return echo >= 0 && bytes.Contains(b[echo+len(cmd)+1:], []byte(replyMark))
}

// escape sends one bare CR and waits for whatever reply it gets (see command for what it does to a node in
// region-load mode).
func (c *conn) escape(timeout time.Duration) error {
	_, err := c.exchange("", timeout)
	return err
}
