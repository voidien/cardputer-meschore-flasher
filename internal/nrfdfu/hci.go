package nrfdfu

// The HCI/SLIP framing of Nordic's legacy serial DFU, ported from the web flasher's lib/dfu.js
// (flasher.meshcore.dev@45a5257, MIT, Copyright (c) 2025 Rastislav Vysoky), which adapts adafruit-nrfutil's
// dfu_transport_serial.py and util.py (BSD-3-Clause, Copyright (c) 2015 Nordic Semiconductor). See
// THIRD_PARTY_NOTICES.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"meshflash/internal/serialport"
)

// HCI and DFU constants, named as in dfu.js.
const (
	slipEnd    = 0xC0
	slipEsc    = 0xDB
	slipEscEnd = 0xDC // 0xDB 0xDC stands for 0xC0
	slipEscEsc = 0xDD // 0xDB 0xDD stands for 0xDB

	hciPacketType      = 14 // HCI_PACKET_TYPE: vendor specific
	dataIntegrityCheck = 1  // DATA_INTEGRITY_CHECK_PRESENT: the frame ends in a CRC-16
	reliablePacket     = 1  // RELIABLE_PACKET: the bootloader acknowledges it

	dfuInitPacket = 1 // DFU_INIT_PACKET
	dfuStart      = 3 // DFU_START_PACKET
	dfuData       = 4 // DFU_DATA_PACKET
	dfuStop       = 5 // DFU_STOP_DATA_PACKET
	// DFU_ERASE_PAGE (6) is never sent: dfu.js uses it only with eraseBeforeUpdate, which the web flasher never sets.

	dfuModeApplication = 4 // DFU_UPDATE_MODE_APP

	chunkSize = 512 // DFU_PACKET_MAX_SIZE: firmware bytes per DATA frame
)

// encodeFrame builds one HCI frame as dfu.js's HciPacket does (dfu.js:43-64,88-114): a 4-byte header (sequence
// number, the acknowledgement number seq+1, the data-integrity and reliable bits; packet type 14 and the 12-bit
// payload length; the header checksum), the payload, the CRC-16 of header and payload low byte first, all SLIP
// escaped between two 0xC0.
func encodeFrame(seq int, payload []byte) []byte {
	n := len(payload)
	h0 := byte(seq | ((seq+1)%8)<<3 | dataIntegrityCheck<<6 | reliablePacket<<7)
	h1 := byte(hciPacketType | (n&0x000F)<<4)
	h2 := byte((n & 0x0FF0) >> 4)
	h3 := byte(-(int(h0) + int(h1) + int(h2))) // (~(h0+h1+h2)+1) & 0xFF: the four bytes sum to 0
	raw := make([]byte, 0, 4+n+2)
	raw = append(raw, h0, h1, h2, h3)
	raw = append(raw, payload...)
	crc := CRC16(raw)
	raw = append(raw, byte(crc), byte(crc>>8))

	out := make([]byte, 0, len(raw)+len(raw)/16+2)
	out = append(out, slipEnd)
	for _, b := range raw {
		switch b {
		case slipEnd:
			out = append(out, slipEsc, slipEscEnd)
		case slipEsc:
			out = append(out, slipEsc, slipEscEsc)
		default:
			out = append(out, b)
		}
	}
	return append(out, slipEnd)
}

// le32 is int32ToBytes (dfu.js:29-34).
func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// startPayload is sendStartDfu's frame for an application of appSize bytes (dfu.js:286-296): type, mode, SoftDevice
// size, bootloader size, application size.
func startPayload(appSize int) []byte {
	p := le32(dfuStart)
	p = append(p, le32(dfuModeApplication)...)
	p = append(p, le32(0)...)
	p = append(p, le32(0)...)
	return append(p, le32(uint32(appSize))...)
}

// initPayload is sendInitPacket's frame (dfu.js:259-267): the type, the .dat verbatim, and two zero bytes. The
// zeros matter: the bootloader takes floor(length/4)-1 words after the type (OTAFIX dfu_transport_serial.c:271), so
// a 14-byte .dat alone would lose its CRC; with them it arrives whole (docs/meshcore-flashing.md 5.4).
func initPayload(dat []byte) []byte {
	p := le32(dfuInitPacket)
	p = append(p, dat...)
	return append(p, 0, 0)
}

// initArrivesWhole reports whether the bootloader keeps all of the .dat from initPayload: it keeps
// 4*(floor(len/4)-1) bytes after the 4-byte type. True for every even .dat length; ParseInitPacket only accepts
// even ones, and Flash checks again so the rule can't be lost.
func initArrivesWhole(dat []byte) bool {
	n := len(initPayload(dat))
	return 4*(n/4-1) >= len(dat)
}

// dataPayload is one DATA frame's payload (dfu.js:337-345).
func dataPayload(chunk []byte) []byte {
	p := le32(dfuData)
	return append(p, chunk...)
}

// stopPayload is the STOP frame's payload (dfu.js:371-372).
func stopPayload() []byte { return le32(dfuStop) }

// Errors reading the bootloader's acknowledgements.
var (
	// ErrNoAck means the bootloader didn't acknowledge a frame, though it was sent three times, 5 s apart (dfu.js's
	// "Read timeout", after one try).
	ErrNoAck = errors.New("nrfdfu: the bootloader didn't acknowledge")
	// ErrBadAck means what came back wasn't the acknowledgement expected: a wrong number (dfu.js's "Invalid ACK
	// sequence. Expected X, got Y"), a broken frame ("Received incomplete ACK.", "Invalid SLIP escape
	// sequence"), or a frame that isn't an acknowledgement at all (the port isn't the bootloader's). Nothing is
	// resent: the bootloader is in some other session, or it isn't a bootloader, and only a reset helps.
	ErrBadAck = errors.New("nrfdfu: wrong acknowledgement from the bootloader")
)

// AckTimeout is how long one read waits for an acknowledgement: DEFAULT_SERIAL_PORT_TIMEOUT * 1000 * 5 ms, per read
// call, with no overall deadline (dfu.js:131-155).
const AckTimeout = 5 * time.Second

// acks reads the bootloader's acknowledgements the way dfu.js's getAck does (dfu.js:173-230): bytes up to an
// opening 0xC0 are skipped, the frame runs to the next 0xC0, it is SLIP-decoded, needs at least 2 bytes, and its
// number is bits 3-5 of the first byte. The first acknowledgement of a session is taken whatever its number; each
// later one must be the previous one plus 1 (mod 8). Four departures, which change nothing when every frame is
// acknowledged once: bytes after the closing 0xC0 are kept for the next frame (dfu.js drops the rest of that read);
// after a frame was resent, its extra acknowledgements (the bootloader answers each copy) are skipped; a frame
// that isn't exactly an acknowledgement (isAck) is an error, where dfu.js would read a number out of it; and more
// than maxAckNoise bytes without an acknowledgement is an error (dfu.js would read on).
type acks struct {
	port serialport.Port
	buf  []byte // read from the port, not yet used
	last int    // the last acknowledgement number taken; -1 before the first
	// strays is how many more acknowledgements carrying last may still arrive: one per resend of the frame last
	// acknowledged.
	strays int
	chunk  [256]byte
}

func newAcks(port serialport.Port) *acks { return &acks{port: port, last: -1} }

// next waits for the next acknowledgement and returns its number. A serialport.ErrTimeout from a read is passed on
// as it is (the caller resends); every other read error too.
func (a *acks) next() (int, error) {
	for {
		frame, err := a.frame()
		if err != nil {
			return 0, err
		}
		d, err := slipDecode(frame)
		if err != nil {
			return 0, err
		}
		if len(d) < 2 {
			return 0, fmt.Errorf("%w: received incomplete ACK", ErrBadAck)
		}
		if !isAck(d) {
			return 0, fmt.Errorf("%w: % x is not an acknowledgement: is this the bootloader's port?", ErrBadAck, d[:min(len(d), 8)])
		}
		ack := int(d[0]>>3) & 7
		if a.last != -1 && ack != (a.last+1)%8 {
			if ack == a.last && a.strays > 0 {
				a.strays-- // the bootloader's answer to a copy of the frame already acknowledged
				continue
			}
			return 0, fmt.Errorf("%w: invalid ACK sequence, expected %d, got %d", ErrBadAck, (a.last+1)%8, ack)
		}
		a.last = ack
		a.strays = 0
		return ack, nil
	}
}

// isAck says a decoded frame has the form of the bootloader's acknowledgement, the only thing it ever sends in
// serial DFU: [expected<<3, 0, 0, checksum], 4 bytes summing to 0 (hci_transport.c ack_transmit, the same in OTAFIX
// and upstream Adafruit). dfu.js takes any frame of 2 bytes or more and reads only bits 3-5 of its first byte. But
// every frame the host sends has seq+1 in exactly those bits, so a port that echoes what it is sent (MeshCore's
// serial console does, examples/simple_repeater/main.cpp) would acknowledge every frame perfectly, and the whole
// image would go into the node's command line as a "successful" flash. With this check it stops at START.
func isAck(d []byte) bool {
	return len(d) == 4 && d[0]&^0x38 == 0 && d[1] == 0 && d[2] == 0 && d[0]+d[1]+d[2]+d[3] == 0
}

// maxAckNoise is how many bytes may arrive while no acknowledgement does. dfu.js reads on for as long as bytes keep
// coming; a port that chatters without one (an app's console, not a bootloader) would hold the Flashing screen
// for ever.
const maxAckNoise = 4096

// frame returns the bytes between the next two 0xC0, reading (AckTimeout per read) until they are there. Bytes
// before the opening 0xC0 are dropped, as dfu.js drops them.
func (a *acks) frame() ([]byte, error) {
	noise := 0
	for {
		if i := indexByte(a.buf, 0, slipEnd); i >= 0 {
			if j := indexByte(a.buf, i+1, slipEnd); j >= 0 {
				f := append([]byte(nil), a.buf[i+1:j]...)
				a.buf = a.buf[j+1:]
				return f, nil
			}
			a.buf = a.buf[i:]
		} else {
			a.buf = a.buf[:0]
		}
		if noise > maxAckNoise {
			return nil, fmt.Errorf("%w: %d bytes came and no acknowledgement: is this the bootloader?", ErrBadAck, noise)
		}
		n, err := a.port.Read(a.chunk[:], AckTimeout)
		if err != nil {
			return nil, err
		}
		noise += n
		a.buf = append(a.buf, a.chunk[:n]...)
	}
}

func indexByte(b []byte, from int, c byte) int {
	for i := from; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// slipDecode is dfu.js's decodeSlip (dfu.js:232-257) for the bytes between two 0xC0.
func slipDecode(b []byte) ([]byte, error) {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != slipEsc {
			out = append(out, b[i])
			continue
		}
		i++
		if i >= len(b) {
			return nil, fmt.Errorf("%w: invalid SLIP escape sequence: incomplete", ErrBadAck)
		}
		switch b[i] {
		case slipEscEnd:
			out = append(out, slipEnd)
		case slipEscEsc:
			out = append(out, slipEsc)
		default:
			return nil, fmt.Errorf("%w: invalid SLIP escape sequence: DB followed by %x", ErrBadAck, b[i])
		}
	}
	return out, nil
}

// ackFrame is what the bootloader sends to acknowledge: [expected<<3, 0, 0, checksum], SLIP framed, never escaped
// in practice (hci_transport.c:190-212). The fake bootloader uses it; tests check it against the research's table.
func ackFrame(expected int) []byte {
	h := [4]byte{byte(expected << 3), 0, 0, 0}
	h[3] = byte(-(int(h[0]) + int(h[1]) + int(h[2])))
	out := []byte{slipEnd}
	for _, b := range h {
		switch b {
		case slipEnd:
			out = append(out, slipEsc, slipEscEnd)
		case slipEsc:
			out = append(out, slipEsc, slipEscEsc)
		default:
			out = append(out, b)
		}
	}
	return append(out, slipEnd)
}
