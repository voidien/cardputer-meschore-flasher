package esp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"meshflash/internal/serialport"
)

var (
	// ErrNoBootloader means no reset got an answer from the ROM bootloader. Its text is what the owner should do.
	ErrNoBootloader = errors.New("esp: the board didn't answer from its bootloader. Hold its BOOT button, press and release RESET, release BOOT, and try again")
	// ErrWrongChip means the chip isn't the one the image (or the board chosen) is for. Nothing was written.
	ErrWrongChip = errors.New("esp: wrong chip")
	// ErrUnsupportedChip means meshflash can't flash this chip: only the ESP32, ESP32-S3 and ESP32-C3 (the chips in
	// MeshCore's catalog) carry a flasher stub here.
	ErrUnsupportedChip = errors.New("esp: meshflash can't flash this chip")
	// ErrImage means the file isn't an ESP image fit for where it's going.
	ErrImage = errors.New("esp: not a usable ESP firmware image")
	// ErrTooBig means the image doesn't fit in the flash the board reports.
	ErrTooBig = errors.New("esp: the image doesn't fit in the board's flash")
	// ErrVerify means the MD5 the board computed over what was written isn't the image's.
	ErrVerify = errors.New("esp: the flash doesn't match the file")
	// ErrCompress means the Compressor's stream doesn't inflate back to the image.
	ErrCompress = errors.New("esp: the compressed image doesn't inflate back to the image")
)

// chipDetectMagicReg holds a value that tells the chips apart (esptool-js CHIP_DETECT_MAGIC_REG_ADDR).
const chipDetectMagicReg = 0x40001000

// bannerQuiet is how long the board must stay quiet before its boot banner is taken to be over. A ROM banner is a
// few lines printed at once; at 115200 baud they take about 15 ms.
const bannerQuiet = 100 * time.Millisecond

// Defaults for the connect cap (SPEC.md 6.2: a few resets, about 10 s).
const (
	DefaultConnectAttempts = 4
	DefaultConnectTimeout  = 10 * time.Second
)

// bootModeRe finds the boot mode in a boot banner, as esptool-js's _connectAttempt does, to say why SYNC failed.
// (?s) lets ".*" reach "waiting for download" on the banner's next line; esptool-js's regex can't, so it never
// reports download mode.
var bootModeRe = regexp.MustCompile(`(?s)boot:(0x[0-9a-fA-F]+)(.*waiting for download)?`)

// Loader talks to one ESP32's ROM bootloader, then to the flasher stub, over a serial port. Its methods are the
// steps of a flash in the reference's order; Flash runs them all. A Loader is not safe for concurrent use.
//
// Ported from esptool-js 0.5.4 src/esploader.ts (ESPLoader), Apache-2.0, Copyright 2019 Espressif Systems
// (Shanghai) CO LTD; modified as the package comment lists.
type Loader struct {
	ctx   context.Context
	port  serialport.Port
	clock serialport.Clock
	o     Options
	link  link

	dtr      bool // the last DTR sent, which setRTS sends again
	stub     bool // the flasher stub is running: replies end in 2 status bytes, not 4
	stubSeen bool // every SYNC reply had value 0: a stub was already running
	synced   bool
	touched  bool   // an erase or a data block was sent: the flash may have changed
	ramBlock uint32 // MEM_DATA block size for the stub upload
	chip     *chipDef
}

// NewLoader prepares to talk to the board on port, which the caller has opened at 115200 baud and will close. It
// uses o's Clock, USBProduct, ConnectAttempts, ConnectTimeout, Progress and Logf.
func NewLoader(ctx context.Context, port serialport.Port, o Options) *Loader {
	if ctx == nil {
		ctx = context.Background()
	}
	if o.Clock == nil {
		o.Clock = serialport.RealClock
	}
	l := &Loader{ctx: ctx, port: port, clock: o.Clock, o: o, ramBlock: 0x1800}
	l.link = link{ctx: ctx, port: port, clock: o.Clock}
	return l
}

func (l *Loader) logf(format string, args ...any) {
	if l.o.Logf != nil {
		l.o.Logf(format, args...)
	}
}

func (l *Loader) progress(p Progress) {
	if l.o.Progress != nil {
		if l.chip != nil {
			p.Chip = l.chip.chip
		}
		l.o.Progress(p)
	}
}

// Touched says whether an erase or a data block has been sent, so the flash may no longer hold the old firmware.
func (l *Loader) Touched() bool { return l.touched }

// fatal says whether an error ends the connect loop: the port gone or the context done, as opposed to a SYNC that
// got no (or no good) answer.
func fatal(err error) bool {
	var se *StatusError
	return !errors.Is(err, ErrNoReply) && !errors.Is(err, ErrUnsupportedCommand) && !errors.As(err, &se)
}

// Connect resets the board into its ROM bootloader and syncs with it, as esptool-js's connect does: per attempt a
// reset (UsbJtagSerialReset when the USB product ID is 0x1001, else ClassicReset with a 50 ms, then 550 ms, hold of
// IO0, alternating), the boot banner set aside, then up to 5 SYNCs. Unlike esptool-js 0.5.4 it gives up after
// Options.ConnectAttempts resets or once Options.ConnectTimeout has passed, with ErrNoBootloader.
func (l *Loader) Connect() error {
	attempts, budget := l.o.ConnectAttempts, l.o.ConnectTimeout
	if attempts <= 0 {
		attempts = DefaultConnectAttempts
	}
	if budget <= 0 {
		budget = DefaultConnectTimeout
	}
	l.logf("Connecting...")
	start := l.clock.Now()
	var last error
	var heard []byte
	tried := 0
	for i := 0; i < attempts; i++ {
		if i > 0 && l.clock.Now().Sub(start) >= budget {
			break
		}
		tried++
		l.progress(Progress{Stage: StageConnecting, Attempt: i + 1})
		var err error
		switch {
		case l.o.USBProduct == usbJTAGSerialPID:
			err = l.usbJTAGSerialReset()
		case i%2 == 0:
			err = l.classicReset(50)
		default:
			err = l.classicReset(550)
		}
		if err != nil {
			return fmt.Errorf("esp: resetting the board: %w", err)
		}
		banner, err := l.link.drainBanner(bootLogTimeout, bannerQuiet, bootLogTimeout)
		if err != nil {
			return fmt.Errorf("esp: reading the boot banner: %w", err)
		}
		if len(banner) > 0 {
			heard = banner
		}
		for try := 0; try < 5; try++ {
			err := l.sync()
			if err == nil {
				l.synced = true
				return nil
			}
			if fatal(err) {
				return err
			}
			last = err
		}
	}
	detail := fmt.Sprintf("%d resets in %.1f s", tried, l.clock.Now().Sub(start).Seconds())
	if m := bootModeRe.FindSubmatch(heard); m != nil {
		if len(m[2]) > 0 {
			detail += fmt.Sprintf("; it is in download mode (boot %s) but doesn't answer SYNC", m[1])
		} else {
			detail += fmt.Sprintf("; it started in boot mode %s, not the bootloader", m[1])
		}
	} else if len(heard) == 0 {
		detail += "; it printed nothing"
	}
	if last != nil {
		detail += "; last: " + strings.TrimPrefix(last.Error(), "esp: ")
	}
	return fmt.Errorf("%w (%s)", ErrNoBootloader, detail)
}

// sync sends one SYNC and reads the 8 replies the ROM sends to it: the first within 100 ms, the rest within 3 s
// each (esptool-js sync()). A value of 0 in every reply means a stub is already running.
func (l *Loader) sync() error {
	payload := make([]byte, 36)
	copy(payload, []byte{0x07, 0x07, 0x12, 0x20})
	for i := 4; i < 36; i++ {
		payload[i] = 0x55
	}
	val, _, err := l.command(opSync, payload, 0, syncTimeout)
	if err != nil {
		return err
	}
	stub := val == 0
	for i := 0; i < 7; i++ {
		if val, _, err = l.readReply(opSync, defaultTimeout); err != nil {
			return err
		}
		stub = stub && val == 0
	}
	l.stubSeen = stub
	if stub {
		l.stub = true // it answers with the stub's 2 status bytes from now on
	}
	return nil
}

// ReadReg reads a 32-bit register (READ_REG).
func (l *Loader) ReadReg(addr uint32) (uint32, error) {
	v, _, err := l.command(opReadReg, words(addr), 0, defaultTimeout)
	return v, err
}

// WriteReg writes a 32-bit register (WRITE_REG with mask 0xFFFFFFFF and no delay, as esptool-js's writeReg sends
// it).
func (l *Loader) WriteReg(addr, value uint32) error {
	_, _, err := l.command(opWriteReg, words(addr, value, 0xFFFFFFFF, 0), 0, defaultTimeout)
	return err
}

// DetectChip reads the chip-detect register and names the chip. An unknown value, or a chip meshflash has no stub
// for, is ErrUnsupportedChip.
func (l *Loader) DetectChip() (Chip, error) {
	magic, err := l.ReadReg(chipDetectMagicReg)
	if err != nil {
		return "", err
	}
	d := chipByMagic(magic)
	if d == nil {
		return "", fmt.Errorf("%w: unknown chip (magic 0x%08x)", ErrUnsupportedChip, magic)
	}
	l.chip = d
	l.logf("Detecting chip type... %s", d.name)
	if d.stubFile == "" {
		return d.chip, fmt.Errorf("%w: %s", ErrUnsupportedChip, d.name)
	}
	return d.chip, nil
}

// ReadChipInfo sends the reference's informational reads (chip description, features, crystal, MAC twice, and
// the ESP32-S3's postConnect check, which picks the stub upload's block size). Nothing needs them to flash, but
// sending them keeps the bytes the reference's.
func (l *Loader) ReadChipInfo() (ChipInfo, error) {
	if l.chip == nil || l.chip.info == nil {
		return ChipInfo{}, fmt.Errorf("%w: detect a supported chip first", ErrUnsupportedChip)
	}
	info, err := l.chip.info(l)
	if err != nil {
		return info, err
	}
	l.logf("Chip is %s", info.Description)
	l.logf("Features: %s", strings.Join(info.Features, ", "))
	l.logf("Crystal is %dMHz", info.CrystalMHz)
	l.logf("MAC: %s", info.MAC)
	return info, nil
}

// RunStub uploads the flasher stub into RAM (MEM_BEGIN, MEM_DATA blocks, MEM_END) and waits for its "OHAI"
// (esptool-js runStub). From then on replies carry the stub's 2 status bytes.
func (l *Loader) RunStub() error {
	if l.chip == nil || l.chip.stubFile == "" {
		return fmt.Errorf("%w: detect a supported chip first", ErrUnsupportedChip)
	}
	if l.stubSeen {
		l.logf("Stub is already running. No upload is necessary.")
		l.stub = true
		return nil
	}
	s, err := loadStub(l.chip.stubFile)
	if err != nil {
		return err
	}
	l.logf("Uploading stub...")
	for _, seg := range []struct {
		data []byte
		addr uint32
	}{{s.text, s.textStart}, {s.data, s.dataStart}} {
		if len(seg.data) == 0 {
			continue
		}
		bs := int(l.ramBlock)
		blocks := (len(seg.data) + bs - 1) / bs
		if _, _, err := l.command(opMemBegin, words(uint32(len(seg.data)), uint32(blocks), l.ramBlock, seg.addr), 0, defaultTimeout); err != nil {
			return err
		}
		for seq := 0; seq < blocks; seq++ {
			blk := seg.data[seq*bs : min((seq+1)*bs, len(seg.data))]
			data := append(words(uint32(len(blk)), uint32(seq), 0, 0), blk...)
			if _, _, err := l.command(opMemData, data, checksum(blk), defaultTimeout); err != nil {
				return err
			}
		}
	}
	l.logf("Running stub...")
	var isEntry uint32
	if s.entry == 0 {
		isEntry = 1
	}
	if _, _, err := l.command(opMemEnd, words(isEntry, s.entry), 0, memEndTimeout); err != nil {
		return err
	}
	deadline := l.clock.Now().Add(defaultTimeout)
	for {
		f, err := l.link.frame(deadline)
		if errors.Is(err, errDeadline) {
			return fmt.Errorf("esp: the flasher stub didn't start (no OHAI): %w", ErrNoReply)
		}
		if err != nil {
			return fmt.Errorf("esp: waiting for the flasher stub: %w", err)
		}
		if string(f) == "OHAI" {
			break
		}
	}
	l.stub = true
	l.logf("Stub running...")
	return nil
}

// FlashID reads the SPI flash's JEDEC ID through the SPI controller's registers, as esptool-js's flashId does
// (runSpiflashCommand(0x9F, no data, 24 bits)): byte 0 is the manufacturer, byte 2 the size code.
func (l *Loader) FlashID() (uint32, error) {
	if l.chip == nil || l.chip.stubFile == "" {
		return 0, fmt.Errorf("%w: detect a supported chip first", ErrUnsupportedChip)
	}
	d := l.chip
	base := d.spiBase
	const (
		usrCommand = 1 << 31
		usrMISO    = 1 << 28
		cmdUSR     = 1 << 18
		rdid       = 0x9F
		readBits   = 24
	)
	r := &regReader{l: l}
	oldUSR := r.read(base + d.spiUSR)
	oldUSR2 := r.read(base + d.spiUSR2)
	write := func(addr, v uint32) {
		if r.err == nil {
			r.err = l.WriteReg(addr, v)
		}
	}
	write(base+d.spiMISODlen, readBits-1)
	write(base+d.spiUSR, usrCommand|usrMISO)
	write(base+d.spiUSR2, 7<<28|rdid)
	write(base+d.spiW0, 0)
	write(base, cmdUSR)
	done := false
	for i := 0; i < 10 && r.err == nil; i++ {
		if r.read(base)&cmdUSR == 0 {
			done = true
			break
		}
	}
	if r.err != nil {
		return 0, r.err
	}
	if !done {
		return 0, errors.New("esp: the SPI flash command did not complete in time")
	}
	id := r.read(base + d.spiW0)
	write(base+d.spiUSR, oldUSR)
	write(base+d.spiUSR2, oldUSR2)
	if r.err != nil {
		return 0, r.err
	}
	l.logf("Manufacturer: %x", id&0xff)
	l.logf("Device: %x%x", (id>>8)&0xff, (id>>16)&0xff)
	if size := FlashSize(id); size > 0 {
		l.logf("Detected flash size: %dMB", size>>20)
	}
	return id, nil
}

// FlashSize is the flash size a flash ID's size code gives (0x12 = 256 KB … 0x18 = 16 MB, esptool-js
// DETECTED_FLASH_SIZES), or 0 when the code isn't one of those.
func FlashSize(flashID uint32) int {
	code := (flashID >> 16) & 0xff
	if code < 0x12 || code > 0x18 {
		return 0
	}
	return 256 << 10 << (code - 0x12)
}

// EraseFlash erases the whole chip (ERASE_FLASH, stub only), allowing esptool-js's 120 s.
func (l *Loader) EraseFlash() error {
	if !l.stub {
		return fmt.Errorf("%w: ERASE_FLASH needs the flasher stub", ErrUnsupportedCommand)
	}
	l.logf("Erasing flash (this may take a while)...")
	l.touched = true
	start := l.clock.Now()
	if _, _, err := l.command(opEraseFlash, nil, 0, chipEraseTimeout); err != nil {
		return err
	}
	l.logf("Chip erase completed successfully in %.1fs", l.clock.Now().Sub(start).Seconds())
	return nil
}

// WriteDeflated writes size bytes at address from the zlib stream z, as esptool-js's writeFlash does with the stub:
// FLASH_DEFL_BEGIN (4 words: size, blocks, 0x4000, address), FLASH_DEFL_DATA blocks of 0x4000 bytes of z, each
// with a timeout sized by how much the previous block inflated to, then READ_REG of the chip-detect register,
// which returns once the stub has written every block. marks are inflateMarks(z, …); acked is told after each
// block how many bytes of the image the board has acknowledged.
func (l *Loader) WriteDeflated(address uint32, size int, z []byte, marks []int, acked func(n int)) error {
	if !l.stub {
		return fmt.Errorf("%w: compressed writing is done through the flasher stub", ErrUnsupportedCommand)
	}
	blocks := (len(z) + writeBlockSize - 1) / writeBlockSize
	if len(marks) != blocks {
		return fmt.Errorf("esp: %d block marks for %d blocks", len(marks), blocks)
	}
	l.logf("Compressed %d bytes to %d...", size, len(z))
	if _, _, err := l.command(opFlashDeflBegin, words(uint32(size), uint32(blocks), writeBlockSize, address), 0, defaultTimeout); err != nil {
		return err
	}
	start := l.clock.Now()
	timeout := firstBlockTimeout
	prev := 0
	for seq := 0; seq < blocks; seq++ {
		blk := z[seq*writeBlockSize : min((seq+1)*writeBlockSize, len(z))]
		l.logf("Writing at 0x%x... (%d%%)", address+uint32(prev), 100*(seq+1)/blocks)
		// The stub acknowledges a block when it has received it, and writes it while the next one arrives: so a
		// block's reply is given the time the previous block's writing needs.
		blockTimeout := timeoutPerMB(eraseWritePerMB, marks[seq]-prev)
		l.touched = true
		data := append(words(uint32(len(blk)), uint32(seq), 0, 0), blk...)
		if _, _, err := l.command(opFlashDeflData, data, checksum(blk), timeout); err != nil {
			return err
		}
		timeout = blockTimeout
		prev = marks[seq]
		if acked != nil {
			acked(prev)
		}
	}
	if _, err := l.readRegTimeout(chipDetectMagicReg, timeout); err != nil {
		return err
	}
	l.logf("Wrote %d bytes (%d compressed) at 0x%x in %.3f seconds.", size, len(z), address, l.clock.Now().Sub(start).Seconds())
	return nil
}

func (l *Loader) readRegTimeout(addr uint32, timeout time.Duration) (uint32, error) {
	v, _, err := l.command(opReadReg, words(addr), 0, timeout)
	return v, err
}

// FlashMD5 asks the board for the MD5 of size bytes of flash at address (SPI_FLASH_MD5). The stub answers with 16
// raw bytes, the ROM with 32 hex digits (esptool-js 0.5.4 decodes the ROM's form wrongly, bug 4.10e).
func (l *Loader) FlashMD5(address uint32, size int) ([16]byte, error) {
	var sum [16]byte
	_, data, err := l.command(opSPIFlashMD5, words(address, uint32(size), 0, 0), 0, timeoutPerMB(md5PerMB, size))
	if err != nil {
		return sum, err
	}
	switch len(data) {
	case 16:
		copy(sum[:], data)
	case 32:
		b, err := hex.DecodeString(strings.ToLower(string(data)))
		if err != nil {
			return sum, fmt.Errorf("esp: the MD5 reply %q isn't hex", data)
		}
		copy(sum[:], b)
	default:
		return sum, fmt.Errorf("esp: the MD5 reply is %d bytes, not 16 or 32", len(data))
	}
	return sum, nil
}

// LeaveFlashMode sends what esptool-js sends after writing with the stub: FLASH_BEGIN(0, 0), then
// FLASH_DEFL_END(1), "stay in the loader". The reset that follows starts the firmware.
func (l *Loader) LeaveFlashMode() error {
	l.logf("Leaving...")
	if _, _, err := l.command(opFlashBegin, words(0, 0, writeBlockSize, 0), 0, defaultTimeout); err != nil {
		return err
	}
	_, _, err := l.command(opFlashDeflEnd, words(1), 0, defaultTimeout)
	return err
}

// HardReset restarts the board into its firmware the way flash.js does after writing (reset.go, hardReset).
func (l *Loader) HardReset() error {
	return l.hardReset()
}
