package esp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// The command layer is ported from esptool-js 0.5.4 src/esploader.ts (command, readPacket, checkCommand, checksum,
// timeoutPerMb and the opcodes), Apache-2.0, Copyright 2019 Espressif Systems (Shanghai) CO LTD; modified: per-command
// deadlines, and every reply's status checked.

// Command opcodes.
const (
	opFlashBegin     = 0x02
	opMemBegin       = 0x05
	opMemEnd         = 0x06
	opMemData        = 0x07
	opSync           = 0x08
	opWriteReg       = 0x09
	opReadReg        = 0x0A
	opFlashDeflBegin = 0x10
	opFlashDeflData  = 0x11
	opFlashDeflEnd   = 0x12
	opSPIFlashMD5    = 0x13
	opEraseFlash     = 0xD0
)

var opNames = map[byte]string{
	opFlashBegin: "FLASH_BEGIN", 0x03: "FLASH_DATA", 0x04: "FLASH_END", opMemBegin: "MEM_BEGIN", opMemEnd: "MEM_END",
	opMemData: "MEM_DATA", opSync: "SYNC", opWriteReg: "WRITE_REG", opReadReg: "READ_REG", 0x0B: "SPI_SET_PARAMS",
	0x0D: "SPI_ATTACH", 0x0F: "CHANGE_BAUDRATE", opFlashDeflBegin: "FLASH_DEFL_BEGIN", opFlashDeflData: "FLASH_DEFL_DATA",
	opFlashDeflEnd: "FLASH_DEFL_END", opSPIFlashMD5: "SPI_FLASH_MD5", 0x14: "GET_SECURITY_INFO", opEraseFlash: "ERASE_FLASH",
	0xD1: "ERASE_REGION", 0xD2: "READ_FLASH", 0xD3: "RUN_USER_CODE",
}

func opName(op byte) string {
	if n, ok := opNames[op]; ok {
		return n
	}
	return fmt.Sprintf("command 0x%02x", op)
}

// Timeouts, the values esptool-js 0.5.4 uses (src/esploader.ts:110-116, docs/meshcore-flashing.md 4.8). Here each
// is the deadline of one whole command, not of each chunk read.
const (
	defaultTimeout    = 3000 * time.Millisecond
	syncTimeout       = 100 * time.Millisecond // the first reply to a SYNC
	bootLogTimeout    = 3000 * time.Millisecond
	memEndTimeout     = 200 * time.Millisecond
	chipEraseTimeout  = 120000 * time.Millisecond
	firstBlockTimeout = 5000 * time.Millisecond
	eraseWritePerMB   = 40000 // ms per MB
	md5PerMB          = 8000  // ms per MB
)

// timeoutPerMB scales a per-megabyte timeout to size bytes, never under the default 3 s (esptool-js timeoutPerMb).
func timeoutPerMB(msPerMB float64, size int) time.Duration {
	ms := msPerMB * (float64(size) / 1e6)
	if ms < 3000 {
		return defaultTimeout
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// checksum is the XOR of the data seeded with 0xEF, sent with MEM_DATA and FLASH_DEFL_DATA.
func checksum(data []byte) uint32 {
	s := byte(0xEF)
	for _, b := range data {
		s ^= b
	}
	return uint32(s)
}

// packet builds a command: direction 0x00, op, data length (LE16), checksum (LE32), data.
func packet(op byte, data []byte, chk uint32) []byte {
	p := make([]byte, 8+len(data))
	p[1] = op
	binary.LittleEndian.PutUint16(p[2:], uint16(len(data)))
	binary.LittleEndian.PutUint32(p[4:], chk)
	copy(p[8:], data)
	return p
}

// words packs 32-bit little-endian words, the shape of nearly every command's data.
func words(w ...uint32) []byte {
	b := make([]byte, 4*len(w))
	for i, v := range w {
		binary.LittleEndian.PutUint32(b[4*i:], v)
	}
	return b
}

var (
	// ErrNoReply means the board didn't answer a command in time. Mid-write it usually means the board was reset
	// or lost power.
	ErrNoReply = errors.New("esp: the board didn't answer")
	// ErrUnsupportedCommand is the ROM saying "invalid message" to a command it doesn't know.
	ErrUnsupportedCommand = errors.New("esp: the bootloader doesn't support that command")
)

// StatusError is a reply whose status bytes say the command failed. The ROM sends 4 status bytes, [status, error,
// 0, 0] with status 1 for a failure; the legacy stub sends 2, [error, 0] (stub_flasher.c:228-229,
// docs/meshcore-flashing.md 4.4). Either way a non-zero first byte is a failure.
type StatusError struct {
	// Op is the command refused.
	Op   byte
	Stub bool // the flasher stub answered, not the ROM
	// Code is the error code: the ROM's second status byte, or the stub's first.
	Code byte
	// Status is the raw status bytes.
	Status []byte
}

// Error names the command, who refused it and the error code.
func (e *StatusError) Error() string {
	who := "ROM bootloader"
	if e.Stub {
		who = "flasher stub"
	}
	return fmt.Sprintf("esp: the %s refused %s: %s (0x%02x)", who, opName(e.Op), errorCodeText(e.Code, e.Stub), e.Code)
}

// errorCodeText names the error codes of the ROM (esptool's serial-protocol docs) and the legacy stub
// (include/stub_flasher.h:98-109).
func errorCodeText(code byte, stub bool) string {
	if stub {
		switch code {
		case 0x01:
			return "wrong data length or handler failure"
		case 0xC0:
			return "bad data length"
		case 0xC1:
			return "bad data checksum"
		case 0xC2:
			return "bad block size"
		case 0xC3:
			return "invalid command"
		case 0xC4:
			return "SPI operation failed"
		case 0xC5:
			return "SPI unlock failed"
		case 0xC6:
			return "not in flash mode"
		case 0xC7:
			return "inflate error"
		case 0xC8:
			return "not enough data"
		case 0xC9:
			return "too much data"
		case 0xFF:
			return "command not implemented"
		}
		return "error"
	}
	switch code {
	case 0x05:
		return "invalid message"
	case 0x06:
		return "failed to act on the message"
	case 0x07:
		return "invalid CRC"
	case 0x08:
		return "flash write error"
	case 0x09:
		return "flash read error"
	case 0x0A:
		return "flash read length error"
	case 0x0B:
		return "deflate error"
	case 0x0C:
		return "Adler-32 error"
	}
	return "error"
}

// reply is one response frame from the board: 01, op, length (LE16), value (LE32), data and status bytes.
type reply struct {
	op   byte
	val  uint32
	body []byte // data and status bytes
}

func parseReply(f []byte) (reply, bool) {
	if len(f) < 8 || f[0] != 0x01 {
		return reply{}, false
	}
	return reply{op: f[1], val: binary.LittleEndian.Uint32(f[4:8]), body: f[8:]}, true
}

// command sends one command and waits for its reply (Loader.readReply).
func (l *Loader) command(op byte, data []byte, chk uint32, timeout time.Duration) (uint32, []byte, error) {
	if err := l.ctx.Err(); err != nil {
		return 0, nil, err
	}
	if _, err := l.port.Write(slipEncode(packet(op, data, chk))); err != nil {
		return 0, nil, fmt.Errorf("esp: sending %s: %w", opName(op), err)
	}
	return l.readReply(op, timeout)
}

// readReply waits for the reply to op and checks its status. As esptool-js's readPacket does, it skips up to 100
// frames that aren't replies or answer another command (leftover SYNC replies, say), except a ROM "invalid message"
// reply, which ends the wait at once. It returns the reply's value and its data without the status bytes.
func (l *Loader) readReply(op byte, timeout time.Duration) (uint32, []byte, error) {
	deadline := l.clock.Now().Add(timeout)
	for skipped := 0; ; skipped++ {
		if skipped >= 100 {
			return 0, nil, fmt.Errorf("esp: no reply to %s among 100 frames: %w", opName(op), ErrNoReply)
		}
		f, err := l.link.frame(deadline)
		if errors.Is(err, errDeadline) {
			return 0, nil, fmt.Errorf("esp: no reply to %s in %v: %w", opName(op), timeout, ErrNoReply)
		}
		if err != nil {
			return 0, nil, fmt.Errorf("esp: waiting for the reply to %s: %w", opName(op), err)
		}
		r, ok := parseReply(f)
		if !ok {
			continue
		}
		if r.op != op {
			if len(r.body) >= 2 && r.body[0] != 0 && r.body[1] == 0x05 {
				return 0, nil, fmt.Errorf("esp: %s: %w", opName(op), ErrUnsupportedCommand)
			}
			continue
		}
		statusLen := 4
		if l.stub {
			statusLen = 2
		}
		if op == opSync && len(r.body) == 2 {
			statusLen = 2 // a stub that's already running answers SYNC too, with its 2 status bytes
		}
		if len(r.body) < statusLen {
			return 0, nil, fmt.Errorf("esp: the reply to %s is %d bytes, too short for its status: %w", opName(op), len(r.body), ErrNoReply)
		}
		data, status := r.body[:len(r.body)-statusLen], r.body[len(r.body)-statusLen:]
		if status[0] != 0 {
			e := &StatusError{Op: op, Stub: statusLen == 2, Status: append([]byte(nil), status...)}
			if e.Stub {
				e.Code = status[0]
			} else {
				e.Code = status[1]
			}
			return 0, nil, e
		}
		return r.val, data, nil
	}
}
