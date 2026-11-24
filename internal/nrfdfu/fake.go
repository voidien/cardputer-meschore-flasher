package nrfdfu

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"meshflash/internal/serialport"
)

// FakeBootloader is a fake Adafruit nRF52 serial-DFU bootloader for serialport.Fake. It is the Go port of
// FakeBootloader in reference/nrf52/harness.mjs, the fake the reference recordings were made against, which mirrors
// the bootloader's hci_slip.c, hci_transport.c, dfu_transport_serial.c and dfu_single_bank.c: it waits for the first
// 0xC0, drops a frame that fails the HCI checks without a word, re-acknowledges a frame with the wrong sequence
// number and drops it, acknowledges a good frame before processing it, keeps only whole words of a payload, and
// checks the image's size and CRC-16 at STOP. Beyond the Node fake it can check the init packet against the board's
// SoftDevice as the real bootloader does (and reset, vanishing from USB, when it doesn't match); after STOP with a
// good image it leaves for the app, vanishing from USB too (the Node recordings can't show this: dfu.js closes the
// port as soon as STOP is acknowledged); and it has the Go-only scenarios listed at NewFakeBootloader.
//
// Set the exported fields before the port is opened.
type FakeBootloader struct {
	// SoftDevice is the board's SoftDevice ID. When set, an init packet whose sd_req doesn't list it (or
	// SoftDeviceAny), or whose device type isn't 0x0052, is refused as dfu_init.c refuses it: after its
	// acknowledgement the bootloader resets, so its port vanishes before the next frame. 0 accepts any, as the Node
	// fake does.
	SoftDevice uint16
	// Port is the fake port it is plugged into, for the scenarios that unplug it (and the SoftDevice refusal).
	Port *serialport.Fake

	sc fakeScenario

	mu       sync.Mutex
	started  bool // seen the first 0xC0
	esc      bool
	buf      []byte
	expected int
	n        int    // frames decoded so far (the harness's rxCount)
	held     []byte // an acknowledgement held back (late-ack), sent ahead of the next reply
	resetNow bool   // refused the init packet, or started the app: vanish when the host next writes
	data     []byte
	res      FakeResult
}

// FakeResult is what a FakeBootloader made of a session.
type FakeResult struct {
	// Frames is every frame decoded; Invalid those dropped for failing the HCI checks; Dups the copies
	// re-acknowledged and dropped for a wrong sequence number; Dropped those the scenario dropped.
	Frames, Invalid, Dups, Dropped int
	// LenMismatch counts frames whose 12-bit length field isn't their payload's length (the bootloader doesn't
	// check it; the harness warns).
	LenMismatch int
	// Start is START's sizes (nil until one arrives).
	Start *FakeStart
	// Init is the init packet as the bootloader keeps it: the .dat and dfu.js's two zero bytes.
	Init []byte
	// SDReq and CRC are read from Init as the bootloader reads them.
	SDReq []uint16
	CRC   uint16
	// Received is the firmware bytes taken from DATA frames.
	Received int
	// Stopped says STOP arrived; Valid that the image then had START's size and the init packet's CRC-16 (the
	// image's own CRC is ImageCRC).
	Stopped  bool
	ImageCRC uint16
	Valid    bool
	// Reset says it reset after refusing the init packet.
	Reset bool
	// AppStarted says it took a good image at STOP and left for the app, so its port vanishes before the host's next
	// write (a resend of STOP whose acknowledgement was lost finds it gone).
	AppStarted bool
	// Unknown counts payloads of a type it doesn't know.
	Unknown int
}

// FakeStart is a START packet's fields.
type FakeStart struct {
	Mode, SoftDevice, Bootloader, App uint32
}

type fakeScenario struct {
	kind string
	n    int
}

// NewFakeBootloader makes a fake bootloader acting out a scenario. Frames are counted from 0 in the order they
// arrive (START is 0, INIT 1, DATA#1 2, …; a resent frame counts again), as in harness.mjs:
//
//	ok, ""        a good bootloader
//	drop-ack:N    frame N is dropped: not processed, not acknowledged (as harness.mjs)
//	bad-ack:N     frame N is processed, but its acknowledgement carries expected+3 (as harness.mjs)
//	bad-crc       frame 1 is dropped as if its CRC were wrong (as harness.mjs)
//	lose-ack:N    frame N is processed, but its acknowledgement is lost
//	late-ack:N    frame N is processed; its acknowledgement comes only after the host writes again
//	mute-from:N   frame N and every later one is dropped: the bootloader has stopped answering
//	unplug-at:N   the port is unplugged as frame N arrives (needs Port)
func NewFakeBootloader(scenario string) (*FakeBootloader, error) {
	b := &FakeBootloader{expected: 1} // INITIAL_ACK_NUMBER_EXPECTED (hci_transport.c:56)
	switch {
	case scenario == "" || scenario == "ok":
		b.sc.kind = "ok"
	case scenario == "bad-crc":
		b.sc = fakeScenario{kind: "drop-ack", n: 1}
	default:
		kind, num, ok := strings.Cut(scenario, ":")
		n, err := strconv.Atoi(num)
		if !ok || err != nil || n < 0 {
			return nil, fmt.Errorf("nrfdfu: fake bootloader: bad scenario %q", scenario)
		}
		switch kind {
		case "drop-ack", "bad-ack", "lose-ack", "late-ack", "mute-from", "unplug-at":
		default:
			return nil, fmt.Errorf("nrfdfu: fake bootloader: unknown scenario %q", scenario)
		}
		b.sc = fakeScenario{kind: kind, n: n}
	}
	return b, nil
}

// Result is what the bootloader made of the session so far.
func (b *FakeBootloader) Result() FakeResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.res
	r.Init = append([]byte(nil), r.Init...)
	r.SDReq = append([]uint16(nil), r.SDReq...)
	if r.Start != nil {
		s := *r.Start
		r.Start = &s
	}
	return r
}

// Image is the firmware it has received.
func (b *FakeBootloader) Image() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

// Lines ignores line coding and control lines, as the bootloader does (it has no handlers for them).
func (b *FakeBootloader) Lines(dtr, rts bool, baud int) []byte { return nil }

// Receive takes what the host wrote and returns the acknowledgements.
func (b *FakeBootloader) Receive(p []byte) []byte {
	b.mu.Lock()
	if b.resetNow {
		b.resetNow = false
		b.mu.Unlock()
		b.unplug()
		return nil
	}
	out := b.held
	b.held = nil
	unplug := false
	for _, c := range p {
		if !b.started { // hci_slip.c ignores everything before the first 0xC0
			b.started = c == slipEnd
			continue
		}
		if b.esc {
			b.esc = false
			switch c {
			case slipEscEnd:
				c = slipEnd
			case slipEscEsc:
				c = slipEsc
			}
			b.buf = append(b.buf, c)
			continue
		}
		switch c {
		case slipEsc:
			b.esc = true
		case slipEnd:
			if len(b.buf) > 0 { // an empty frame is ignored
				reply, gone := b.frame(b.buf)
				out = append(out, reply...)
				unplug = unplug || gone
			}
			b.buf = nil
		default:
			b.buf = append(b.buf, c)
		}
	}
	b.mu.Unlock()
	if unplug {
		b.unplug()
		return nil
	}
	return out
}

func (b *FakeBootloader) unplug() {
	if b.Port != nil {
		b.Port.Unplug()
	}
}

// frame handles one decoded frame (hci_transport.c's is_rx_pkt_valid and the seq/ACK rule) and returns the
// acknowledgement to send, and whether the port should vanish now.
func (b *FakeBootloader) frame(p []byte) (reply []byte, unplug bool) {
	n := b.n
	b.n++
	b.res.Frames++
	sc := b.sc
	if sc.kind == "unplug-at" && n == sc.n {
		return nil, true
	}
	valid := len(p) > 4 && p[0]&0x40 != 0 && p[0]&0x80 != 0 && p[1]&0x0F == hciPacketType &&
		byte(p[0]+p[1]+p[2]+p[3]) == 0 &&
		CRC16(p[:len(p)-2]) == binary.LittleEndian.Uint16(p[len(p)-2:])
	if !valid {
		b.res.Invalid++
		return nil, false
	}
	if (sc.kind == "drop-ack" && n == sc.n) || (sc.kind == "mute-from" && n >= sc.n) {
		b.res.Dropped++
		return nil, false
	}
	if seq := int(p[0] & 7); seq != b.expected {
		b.res.Dups++
		return ackFrame(b.expected), false
	}
	b.expected = (b.expected + 1) & 7
	if sc.kind == "bad-ack" && n == sc.n {
		b.expected = (b.expected + 3) & 7
	}
	ack := ackFrame(b.expected)
	lenField := int(p[1]>>4) | int(p[2])<<4
	b.payload(p[4:len(p)-2], lenField)
	switch {
	case sc.kind == "lose-ack" && n == sc.n:
		return nil, false
	case sc.kind == "late-ack" && n == sc.n:
		b.held = ack
		return nil, false
	}
	return ack, false
}

// payload processes a DFU packet as dfu_transport_serial.c and dfu_single_bank.c do: the type is byte 0 only, and
// the body is floor(len/4)-1 whole words after the 4-byte type (dfu_transport_serial.c:267-271).
func (b *FakeBootloader) payload(pl []byte, lenField int) {
	if lenField != len(pl) {
		b.res.LenMismatch++
	}
	if len(pl) < 4 {
		// Too short to hold a packet type (the real bootloader would read past the frame): nothing to process.
		b.res.Unknown++
		return
	}
	words := len(pl)/4 - 1
	body := pl[4 : 4+words*4]
	le := binary.LittleEndian
	switch pl[0] {
	case dfuStart:
		if len(pl) < 20 {
			b.res.Unknown++
			return
		}
		b.res.Start = &FakeStart{Mode: le.Uint32(pl[4:]), SoftDevice: le.Uint32(pl[8:]), Bootloader: le.Uint32(pl[12:]), App: le.Uint32(pl[16:])}
	case dfuInitPacket:
		b.res.Init = append(b.res.Init, body...)
		ip := b.res.Init
		if len(ip) < 12 {
			return
		}
		sdn := int(le.Uint16(ip[8:]))
		if len(ip) < 12+2*sdn {
			return
		}
		b.res.SDReq = b.res.SDReq[:0]
		for i := 0; i < sdn; i++ {
			b.res.SDReq = append(b.res.SDReq, le.Uint16(ip[10+2*i:]))
		}
		b.res.CRC = le.Uint16(ip[10+2*sdn:])
		if b.SoftDevice != 0 && !b.initAccepted(le.Uint16(ip[0:])) {
			// dfu_init_prevalidate fails → APP_ERROR_CHECK → NVIC_SystemReset (5.6): the host already has the ACK.
			b.res.Reset = true
			b.resetNow = true
		}
	case dfuData:
		b.data = append(b.data, body...)
		b.res.Received = len(b.data)
	case dfuStop:
		b.res.Stopped = true
		b.res.ImageCRC = CRC16(b.data)
		b.res.Valid = b.res.Start != nil && len(b.data) == int(b.res.Start.App) && b.res.ImageCRC == b.res.CRC
		if b.res.Valid {
			// A good image is activated and the app started: the bootloader tears down USB right after STOP's
			// acknowledgement (OTAFIX main.c, after bootloader_dfu_start). A bad one leaves it in serial DFU.
			b.res.AppStarted = true
			b.resetNow = true
		}
	default:
		b.res.Unknown++
	}
}

func (b *FakeBootloader) initAccepted(deviceType uint16) bool {
	if deviceType != DeviceTypeNRF52 {
		return false
	}
	for _, s := range b.res.SDReq {
		if s == b.SoftDevice || s == SoftDeviceAny {
			return true
		}
	}
	return false
}

// FakeApp is MeshCore's app on an nRF52 board as the 1200-baud touch sees it: when DTR drops while the line is at
// 1200 baud, it reboots into its serial DFU bootloader (TinyUSB's tud_cdc_line_state_cb → enterSerialDfu,
// docs/meshcore-flashing.md 5.2) and its USB port goes away. Its console isn't modelled.
type FakeApp struct {
	// Port is the fake port it is plugged into; it is unplugged when the app resets.
	Port *serialport.Fake

	mu    sync.Mutex
	reset bool
}

// Receive ignores what the host writes.
func (a *FakeApp) Receive(p []byte) []byte { return nil }

// Lines resets the app into its bootloader when DTR is low at 1200 baud.
func (a *FakeApp) Lines(dtr, rts bool, baud int) []byte {
	a.mu.Lock()
	fire := !dtr && baud == TouchBaud && !a.reset
	if fire {
		a.reset = true
	}
	port := a.Port
	a.mu.Unlock()
	if fire && port != nil {
		port.Unplug()
	}
	return nil
}

// Reset says the app has rebooted into its bootloader.
func (a *FakeApp) Reset() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reset
}
