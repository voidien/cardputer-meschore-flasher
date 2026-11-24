package esp

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"io"
	"sync"

	"meshflash/internal/serialport"
)

// FakeESP is a fake ESP32-family board for serialport.Fake: its ROM bootloader and the flasher stub, enough to
// flash it. It is the Go port of FakeEsp in reference/esp32/fake.mjs, the fake the reference recordings were made
// against, with the same defaults (one-chunk boot banner, eFuses all 0, flash ID 0x1740EF, no busy polls), so
// the same run gives the same bytes. Beyond the Node fake it checks what the real legacy stub checks (FLASH_BEGIN
// and FLASH_DEFL_BEGIN exactly 16 bytes, block size, packet size), answers failures with the real status layouts,
// and can be told to fail (NAK, CorruptWrite, UnplugAfterBlocks).
//
// Set the exported fields before the port is opened.
type FakeESP struct {
	// Chip is the chip it pretends to be: ESP32, ESP32S3 or ESP32C3.
	Chip Chip
	// USBJTAG makes it a board on the chip's own USB-Serial-JTAG (USB product 0x1001), whose reset logic reads
	// DTR and RTS differently (Lines).
	USBJTAG bool
	// IgnoreResets is how many resets into the bootloader it ignores, starting its app instead.
	IgnoreResets int
	// FlashID is what W0 holds after an RDID.
	FlashID uint32
	// FlashBytes is the size of the fake's flash.
	FlashBytes int
	// Regs are register values on top of the chip's defaults (eFuse words, UARTDEV_BUF_NO).
	Regs map[uint32]uint32
	// Polls is how many READ_REGs of SPI_CMD still see the busy bit after the RDID starts.
	Polls int
	// AppBanner is what the app prints after a normal reset (nothing by default, as the Node fake).
	AppBanner []byte
	// StubResident makes it a board whose resets don't take: a flasher stub left running by an earlier flash
	// keeps answering, SYNC included (with value 0).
	StubResident bool

	// NAK makes it refuse one command: the N-th (from 0) with opcode Op gets error Code.
	NAK *FakeNAK
	// CorruptWrite flips a bit of what it writes, so the flash no longer matches the image.
	CorruptWrite bool
	// UnplugAfterBlocks unplugs Port as the n-th FLASH_DEFL_DATA arrives (0 = never).
	UnplugAfterBlocks int
	// Port is the fake port it is plugged into, for UnplugAfterBlocks.
	Port *serialport.Fake

	mu       sync.Mutex
	init     bool
	mode     string // off, rom, stub, app
	dtr, rts bool
	// jtagDownload latches, as a USB-Serial-JTAG reset begins, whether the chip will start its bootloader.
	jtagDownload bool
	rx           []byte
	inPkt        bool
	esc          bool
	flash        []byte
	regs         map[uint32]uint32
	busyReads    int
	defl         *fakeDefl
	counts       map[byte]int
	resets       int
	erased       bool
}

// FakeNAK is a command FakeESP refuses.
type FakeNAK struct {
	Op   byte // the opcode refused
	N    int  // which of the commands with that opcode, from 0
	Code byte // the error code answered: a stub code (0xC1, …) or a ROM one (0x07, …), whichever is running
}

type fakeDefl struct {
	size, blocks, blockSize, off uint32
	seq                          uint32
	parts                        bytes.Buffer
}

type fakeChip struct {
	magic   uint32
	spiBase uint32
	w0      uint32
	banner  string
	regs    map[uint32]uint32
}

var fakeChips = map[Chip]fakeChip{
	ESP32: {magic: 0x00f01d83, spiBase: 0x3ff42000, w0: 0x80,
		banner: "ets Jun  8 2016 00:22:57\r\n\r\nrst:0x1 (POWERON_RESET),boot:0x3 (DOWNLOAD_BOOT(UART0/UART1/SDIO_REI_REO_V2))\r\nwaiting for download\r\n",
		regs:   map[uint32]uint32{0x3ff40014: 347}}, // UART_CLKDIV: a 40 MHz crystal at 115200
	ESP32S3: {magic: 0x00000009, spiBase: 0x60002000, w0: 0x58,
		banner: "ESP-ROM:esp32s3-20210327\r\nBuild:Mar 27 2021\r\nrst:0x1 (POWERON),boot:0x0 (DOWNLOAD(USB/UART0))\r\nwaiting for download\r\n"},
	ESP32C3: {magic: 0x1b31506f, spiBase: 0x60002000, w0: 0x58,
		banner: "ESP-ROM:esp32c3-api1-20210207\r\nBuild:Feb  7 2021\r\nrst:0x1 (POWERON),boot:0x0 (DOWNLOAD(USB/UART0/1))\r\nwaiting for download\r\n"},
}

// NewFakeESP makes a fake board of the given chip with the Node fake's defaults.
func NewFakeESP(chip Chip) *FakeESP {
	return &FakeESP{Chip: chip, FlashID: 0x1740ef, FlashBytes: 8 << 20}
}

func (f *FakeESP) setup() {
	if f.init {
		return
	}
	f.init = true
	f.mode = "off"
	f.flash = bytes.Repeat([]byte{0xFF}, f.FlashBytes)
	f.regs = map[uint32]uint32{}
	c := fakeChips[f.Chip]
	f.regs[chipDetectMagicReg] = c.magic
	for a, v := range c.regs {
		f.regs[a] = v
	}
	for a, v := range f.Regs { // last, so a test can make it claim to be another chip
		f.regs[a] = v
	}
	f.counts = map[byte]int{}
}

// Lines follows DTR and RTS. On a USB-serial bridge's auto-reset circuit, RTS released while DTR is asserted
// starts the ROM bootloader, which prints its banner, and RTS released with DTR released starts the app (as the
// Node fake). With USBJTAG it models the chip's USB-Serial-JTAG: RTS asserted with DTR released holds the chip in
// reset, and on release it starts the bootloader if DTR was asserted just before the reset began (esptool's
// UsbJtagSerialReset: "set IO0", then reset), else the app. The Node fake starts the bootloader on every reset
// over USB-JTAG; that changes no byte the host sends, only that the board here ends up in its app.
func (f *FakeESP) Lines(dtr, rts bool, baud int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setup()
	prevDTR, prevRTS := f.dtr, f.rts
	f.dtr, f.rts = dtr, rts
	var toROM bool
	if f.USBJTAG {
		inReset, wasInReset := rts && !dtr, prevRTS && !prevDTR
		if inReset && !wasInReset {
			f.jtagDownload = prevDTR
		}
		if !wasInReset || inReset {
			return nil
		}
		toROM = f.jtagDownload
	} else {
		if !prevRTS || rts {
			return nil
		}
		toROM = dtr
	}
	f.resets++
	if toROM && f.IgnoreResets > 0 {
		f.IgnoreResets--
		toROM = false
	}
	if !toROM {
		f.mode = "app"
		return append([]byte(nil), f.AppBanner...)
	}
	if f.StubResident {
		f.mode = "stub"
		return nil
	}
	f.mode = "rom"
	f.defl = nil
	return []byte(fakeChips[f.Chip].banner)
}

// Receive takes the host's bytes, SLIP-decodes them and answers each whole packet.
func (f *FakeESP) Receive(b []byte) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setup()
	var out []byte
	for _, c := range b {
		if c == slipEnd {
			if f.inPkt && len(f.rx) > 0 {
				out = append(out, f.handle(f.rx)...)
				f.rx = nil
				f.inPkt = false
			} else {
				f.inPkt = true
				f.rx = nil
			}
			continue
		}
		if !f.inPkt {
			continue
		}
		switch {
		case f.esc:
			if c == slipEscEnd {
				f.rx = append(f.rx, slipEnd)
			} else {
				f.rx = append(f.rx, slipEsc)
			}
			f.esc = false
		case c == slipEsc:
			f.esc = true
		default:
			f.rx = append(f.rx, c)
		}
	}
	return out
}

func (f *FakeESP) statusLen() int {
	if f.mode == "stub" {
		return 2
	}
	return 4
}

// reply builds a response; err 0 is success. The ROM's status is [1, err, 0, 0] on failure, the stub's [err, 0].
func (f *FakeESP) reply(op byte, val uint32, data []byte, err byte) []byte {
	st := make([]byte, f.statusLen())
	if err != 0 {
		if f.mode == "stub" {
			st[0] = err
		} else {
			st[0], st[1] = 1, err
		}
	}
	body := append(append([]byte(nil), data...), st...)
	p := make([]byte, 8+len(body))
	p[0], p[1] = 1, op
	binary.LittleEndian.PutUint16(p[2:], uint16(len(body)))
	binary.LittleEndian.PutUint32(p[4:], val)
	copy(p[8:], body)
	return slipEncode(p)
}

func (f *FakeESP) fail(op byte, rom, stub byte) []byte {
	if f.mode == "stub" {
		return f.reply(op, 0, nil, stub)
	}
	return f.reply(op, 0, nil, rom)
}

func (f *FakeESP) readReg(a uint32) uint32 {
	if a == fakeChips[f.Chip].spiBase && f.busyReads > 0 {
		f.busyReads--
		return 1 << 18
	}
	return f.regs[a]
}

func (f *FakeESP) handle(p []byte) []byte {
	if f.mode != "rom" && f.mode != "stub" {
		return nil // an app doesn't speak the protocol
	}
	if len(p) < 8 {
		return nil
	}
	op := p[1]
	size := int(binary.LittleEndian.Uint16(p[2:]))
	chk := binary.LittleEndian.Uint32(p[4:])
	data := p[8:]
	if size < len(data) {
		data = data[:size]
	}
	w := func(i int) uint32 {
		if 4*i+4 > len(data) {
			return 0
		}
		return binary.LittleEndian.Uint32(data[4*i:])
	}
	n := f.counts[op]
	f.counts[op]++
	if f.NAK != nil && f.NAK.Op == op && f.NAK.N == n {
		return f.reply(op, 0, nil, f.NAK.Code)
	}
	if f.mode == "stub" && len(data) > writeBlockSize+16 {
		return slipEncode(append(fakeHeader(op, 2), 0xC0, 0xEE)) // the stub's answer to an oversized packet
	}
	switch op {
	case opSync:
		val := uint32(0x20120707)
		if f.mode == "stub" {
			val = 0
		}
		var out []byte
		for i := 0; i < 8; i++ {
			out = append(out, f.reply(op, val, nil, 0)...)
		}
		return out
	case opReadReg:
		return f.reply(op, f.readReg(w(0)), nil, 0)
	case opWriteReg:
		c := fakeChips[f.Chip]
		for i := 0; i+16 <= len(data); i += 16 {
			a, v := w(i/4), w(i/4+1)
			f.regs[a] = v
			if a == c.spiBase && v&(1<<18) != 0 { // SPI_CMD_USR: run the RDID and clear the busy bit
				f.regs[c.spiBase+c.w0] = f.FlashID
				f.regs[a] = 0
				f.busyReads = f.Polls
			}
		}
		return f.reply(op, 0, nil, 0)
	case opMemBegin:
		return f.reply(op, 0, nil, 0)
	case opMemData:
		if len(data) < 16 || checksum(data[16:]) != chk {
			return f.reply(op, 0, nil, 0x07)
		}
		return f.reply(op, 0, nil, 0)
	case opMemEnd:
		out := f.reply(op, 0, nil, 0)
		f.mode = "stub"
		return append(out, slipEncode([]byte("OHAI"))...)
	case opEraseFlash:
		if f.mode != "stub" {
			return f.reply(op, 0, nil, 0x05) // stub-only: the ROM calls it an invalid message
		}
		for i := range f.flash {
			f.flash[i] = 0xFF
		}
		f.erased = true
		return f.reply(op, 0, nil, 0)
	case opFlashBegin:
		if f.mode == "stub" && len(data) != 16 {
			return f.reply(op, 0, nil, 0x01) // verify_data_len(command, 16) || …: a C "||" yields 1
		}
		return f.reply(op, 0, nil, 0)
	case opFlashDeflBegin:
		if f.mode == "stub" && len(data) != 16 {
			return f.reply(op, 0, nil, 0x01)
		}
		if f.mode == "stub" && w(2) > writeBlockSize {
			return f.reply(op, 0, nil, 0xC2)
		}
		f.defl = &fakeDefl{size: w(0), blocks: w(1), blockSize: w(2), off: w(3)}
		return f.reply(op, 0, nil, 0)
	case opFlashDeflData:
		if f.UnplugAfterBlocks > 0 && n+1 >= f.UnplugAfterBlocks && f.Port != nil {
			f.Port.Unplug()
			return nil
		}
		if len(data) < 16 || f.defl == nil {
			return f.fail(op, 0x05, 0xC6)
		}
		payload := data[16:]
		if checksum(payload) != chk {
			return f.fail(op, 0x07, 0xC1)
		}
		if w(1) != f.defl.seq || int(w(0)) != len(payload) {
			return f.fail(op, 0x05, 0xC0)
		}
		f.defl.parts.Write(payload)
		f.defl.seq++
		return f.reply(op, 0, nil, 0)
	case opFlashDeflEnd:
		f.finishDefl()
		return f.reply(op, 0, nil, 0)
	case opSPIFlashMD5:
		f.finishDefl() // the real stub has written every block by the time the MD5 is asked for
		a, sz := int(w(0)), int(w(1))
		if a < 0 || sz < 0 || a+sz > len(f.flash) {
			return f.fail(op, 0x0A, 0xC4)
		}
		sum := md5.Sum(f.flash[a : a+sz])
		if f.mode == "stub" {
			return f.reply(op, 0, sum[:], 0)
		}
		return f.reply(op, 0, []byte(hex.EncodeToString(sum[:])), 0)
	default:
		return f.fail(op, 0x05, 0xFF)
	}
}

func fakeHeader(op byte, bodyLen int) []byte {
	h := make([]byte, 8)
	h[0], h[1] = 1, op
	binary.LittleEndian.PutUint16(h[2:], uint16(bodyLen))
	return h
}

// finishDefl inflates what FLASH_DEFL_DATA brought and writes it into the flash.
func (f *FakeESP) finishDefl() {
	d := f.defl
	if d == nil || d.parts.Len() == 0 {
		return
	}
	f.defl = nil
	r, err := zlib.NewReader(bytes.NewReader(d.parts.Bytes()))
	if err != nil {
		return
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return
	}
	if f.CorruptWrite && len(raw) > 0 {
		raw[len(raw)/2] ^= 0x01
	}
	if int(d.off)+len(raw) <= len(f.flash) {
		copy(f.flash[d.off:], raw)
	}
}

// Flash is a copy of the fake's flash, after any write still being received is applied.
func (f *FakeESP) Flash() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setup()
	f.finishDefl()
	return append([]byte(nil), f.flash...)
}

// SetFlash fills the fake's flash from offset 0 (an old firmware to be overwritten, say).
func (f *FakeESP) SetFlash(b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setup()
	copy(f.flash, b)
}

// Mode is what the fake is running: "off", "rom" (the bootloader), "stub" or "app".
func (f *FakeESP) Mode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setup()
	return f.mode
}

// Erased says whether ERASE_FLASH was done.
func (f *FakeESP) Erased() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.erased
}

// Count is how many commands with opcode op it has received.
func (f *FakeESP) Count(op byte) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[op]
}

// Resets is how many times RTS was released (each a reset of the chip).
func (f *FakeESP) Resets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resets
}
