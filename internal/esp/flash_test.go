package esp

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"meshflash/internal/serialport"
)

var (
	esp32Update = recordings[0]
	esp32Wipe   = recordings[1]
	s3Update    = recordings[2]
)

func flashErr(t *testing.T, err error) *FlashError {
	t.Helper()
	var fe *FlashError
	if !errors.As(err, &fe) {
		t.Fatalf("error %v (%T) isn't a *FlashError", err, err)
	}
	if fe.Recovery() == "" {
		t.Error("no recovery advice")
	}
	return fe
}

func countTX(t *testing.T, log *serialport.Log) int {
	return len(wireFrames(log.TX()))
}

// The file is checked against the board chosen before anything is sent.
func TestWrongImageRefusedBeforeTheBoard(t *testing.T) {
	r := newRig(ESP32S3, false)
	port := r.open()
	o := r.options(s3Update)
	_, err := Flash(context.Background(), port, readTestdata(t, "esp32-app.bin"), o)
	fe := flashErr(t, err)
	if !errors.Is(err, ErrWrongChip) || fe.Stage != StageCheck || fe.Touched {
		t.Fatalf("got %v (stage %v)", err, fe.Stage)
	}
	if n := countTX(t, r.log); n != 0 {
		t.Errorf("%d frames sent; nothing should be", n)
	}
	for _, e := range r.log.Events() {
		if e.Ev == "dtr" || e.Ev == "rts" {
			t.Fatalf("the lines were touched: %+v", e)
		}
	}
	if !strings.Contains(fe.Recovery(), "Nothing was sent") {
		t.Errorf("recovery %q", fe.Recovery())
	}
}

// A board whose chip isn't the image's is refused after SYNC, before the stub goes up, and is reset back into its
// firmware.
func TestWrongChipRefusedBeforeUpload(t *testing.T) {
	for _, tc := range []struct {
		name   string
		board  Chip
		expect Chip
	}{
		{"no expectation", ESP32C3, ""},
		{"expected esp32", ESP32S3, ESP32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(tc.board, false)
			port := r.open()
			o := r.options(esp32Update)
			o.ExpectChip = tc.expect
			_, err := Flash(context.Background(), port, readTestdata(t, "esp32-app.bin"), o)
			fe := flashErr(t, err)
			if !errors.Is(err, ErrWrongChip) || fe.Stage != StageStub || fe.Touched {
				t.Fatalf("got %v (stage %v, touched %v)", err, fe.Stage, fe.Touched)
			}
			for _, op := range []byte{opMemBegin, opMemData, opEraseFlash, opFlashDeflBegin, opFlashDeflData} {
				if n := r.dev.Count(op); n != 0 {
					t.Errorf("%d × %s sent", n, opName(op))
				}
			}
			if n := r.dev.Count(opReadReg); n != 1 {
				t.Errorf("%d READ_REGs, want only the chip-detect one", n)
			}
			if !strings.Contains(err.Error(), tc.board.String()) || !strings.Contains(fe.Recovery(), "Flash untouched") {
				t.Errorf("error %q, recovery %q", err, fe.Recovery())
			}
			if m := r.dev.Mode(); m != "app" {
				t.Errorf("the board is left in %q; the RTS pulse should start its app", m)
			}
		})
	}
}

func TestUnsupportedChip(t *testing.T) {
	r := newRig(ESP32, false)
	r.dev.Regs = map[uint32]uint32{chipDetectMagicReg: 0x2ce0806f} // an ESP32-C6
	port := r.open()
	_, err := Flash(context.Background(), port, readTestdata(t, "esp32-app.bin"), r.options(esp32Update))
	fe := flashErr(t, err)
	if !errors.Is(err, ErrUnsupportedChip) || fe.Stage != StageStub || !strings.Contains(err.Error(), "ESP32-C6") {
		t.Fatalf("got %v", err)
	}
	if r.dev.Count(opMemBegin) != 0 {
		t.Error("the stub was uploaded")
	}
}

func TestEraseNeedsMergedImage(t *testing.T) {
	r := newRig(ESP32, false)
	port := r.open()
	o := r.options(esp32Update)
	o.EraseChip = true
	_, err := Flash(context.Background(), port, readTestdata(t, "esp32-app.bin"), o)
	if fe := flashErr(t, err); !errors.Is(err, ErrImage) || fe.Stage != StageCheck {
		t.Fatalf("got %v", err)
	}
	if countTX(t, r.log) != 0 {
		t.Error("something was sent")
	}
}

// Only an update (0x10000, no erase) or a full wipe (erase, 0x0) is flashed. An ESP32-S3 app passes the header
// check at 0x0 (ImageChip can't tell it from a merged image), so before this rule the zero Options wrote it over
// the bootloader, and with EraseChip wiped the chip first; both "succeeded".
func TestOnlyUpdateOrWipe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chip  Chip
		image string
		addr  uint32
		erase bool
	}{
		{"zero options: an S3 app at 0x0", ESP32S3, "s3-app.bin", 0, false},
		{"a merged image at 0x0 without the erase", ESP32C3, "c3-merged.bin", 0, false},
		{"an ESP32 image at the bootloader's 0x1000", ESP32, "esp32-app.bin", 0x1000, false},
		{"the partition table's 0x8000", ESP32, "esp32-app.bin", 0x8000, false},
		{"an erase under an app at 0x10000", ESP32, "esp32-app.bin", AppAddress, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(tc.chip, false)
			port := r.open()
			_, err := Flash(context.Background(), port, readTestdata(t, tc.image),
				Options{Clock: r.clock, ExpectChip: tc.chip, Address: tc.addr, EraseChip: tc.erase})
			fe := flashErr(t, err)
			if !errors.Is(err, ErrImage) || fe.Stage != StageCheck || fe.Touched {
				t.Fatalf("got %v (stage %v)", err, fe.Stage)
			}
			for _, e := range r.log.Events() {
				if e.Ev == "tx" || e.Ev == "dtr" || e.Ev == "rts" {
					t.Fatalf("the board was touched: %+v", e)
				}
			}
			if got := r.dev.Flash()[:4]; !bytes.Equal(got, []byte{0xFF, 0xFF, 0xFF, 0xFF}) {
				t.Errorf("flash at 0x0 is %x", got)
			}
		})
	}
}

// ExpectChip takes the forms ParseChip takes; the catalog's device type ("esp32" for every ESP32-family board) is
// not a chip, and for an ESP32-S3 board it is refused before the board is touched.
func TestExpectChipForms(t *testing.T) {
	t.Run("ESP32-S3", func(t *testing.T) {
		r := newRig(ESP32S3, false)
		port := r.open()
		o := r.options(s3Update)
		o.ExpectChip = "ESP32-S3"
		if _, err := Flash(context.Background(), port, readTestdata(t, "s3-app.bin"), o); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("the catalog's esp32 for an S3 file", func(t *testing.T) {
		r := newRig(ESP32S3, false)
		port := r.open()
		o := r.options(s3Update)
		o.ExpectChip = "esp32"
		_, err := Flash(context.Background(), port, readTestdata(t, "s3-app.bin"), o)
		if fe := flashErr(t, err); !errors.Is(err, ErrWrongChip) || fe.Stage != StageCheck || countTX(t, r.log) != 0 {
			t.Fatalf("got %v (stage %v)", err, fe.Stage)
		}
	})
}

// A refused command surfaces as a *StatusError with the stub's or the ROM's code.
func TestNAKSurfaces(t *testing.T) {
	t.Run("stub refuses a data block", func(t *testing.T) {
		r := newRig(ESP32S3, false)
		r.dev.NAK = &FakeNAK{Op: opFlashDeflData, N: 1, Code: 0xC1}
		port := r.open()
		_, err := Flash(context.Background(), port, readTestdata(t, "s3-app.bin"), r.options(s3Update))
		fe := flashErr(t, err)
		var se *StatusError
		if !errors.As(err, &se) || se.Op != opFlashDeflData || se.Code != 0xC1 || !se.Stub {
			t.Fatalf("got %v", err)
		}
		if fe.Stage != StageWriting || !fe.Touched || fe.Written == 0 || fe.Written >= fe.Total {
			t.Errorf("stage %v touched %v written %d of %d", fe.Stage, fe.Touched, fe.Written, fe.Total)
		}
		if !strings.Contains(err.Error(), "bad data checksum") || !strings.Contains(fe.Recovery(), "flash the update again") {
			t.Errorf("error %q, recovery %q", err, fe.Recovery())
		}
		if r.dev.Count(opFlashDeflData) != 2 || r.dev.Count(opSPIFlashMD5) != 0 {
			t.Error("it carried on after the NAK")
		}
	})
	t.Run("ROM refuses the stub upload", func(t *testing.T) {
		r := newRig(ESP32, false)
		r.dev.NAK = &FakeNAK{Op: opMemData, N: 0, Code: 0x07}
		port := r.open()
		_, err := Flash(context.Background(), port, readTestdata(t, "esp32-app.bin"), r.options(esp32Update))
		fe := flashErr(t, err)
		var se *StatusError
		if !errors.As(err, &se) || se.Op != opMemData || se.Code != 0x07 || se.Stub || se.Status[0] != 1 {
			t.Fatalf("got %v", err)
		}
		if fe.Stage != StageStub || fe.Touched || !strings.Contains(fe.Recovery(), "Flash untouched") {
			t.Errorf("stage %v touched %v: %s", fe.Stage, fe.Touched, fe.Recovery())
		}
	})
	t.Run("stub refuses the erase", func(t *testing.T) {
		r := newRig(ESP32, false)
		r.dev.NAK = &FakeNAK{Op: opEraseFlash, Code: 0xC4}
		port := r.open()
		_, err := Flash(context.Background(), port, readTestdata(t, "esp32-merged.bin"), r.options(esp32Wipe))
		fe := flashErr(t, err)
		if fe.Stage != StageErasing || !fe.Touched || !strings.Contains(fe.Recovery(), "flash the full wipe again") {
			t.Errorf("%v: stage %v touched %v: %s", err, fe.Stage, fe.Touched, fe.Recovery())
		}
	})
}

// Pulling the board out mid-write ends the flash at once with what state the board is in.
func TestUnplugMidWrite(t *testing.T) {
	image := readTestdata(t, "s3-app.bin")
	r := newRig(ESP32S3, false)
	r.dev.UnplugAfterBlocks = 2
	port := r.open()
	o := r.options(s3Update)
	var last Progress
	o.Progress = func(p Progress) { last = p }
	_, err := Flash(context.Background(), port, image, o)
	fe := flashErr(t, err)
	if !errors.Is(err, serialport.ErrGone) || fe.Stage != StageWriting || !fe.Touched {
		t.Fatalf("got %v (stage %v)", err, fe.Stage)
	}
	padded := padImage(image)
	z, _ := ZlibCompress(padded)
	marks, _ := inflateMarks(z, padded)
	if fe.Written != marks[0] || fe.Total != len(padded) || last.Done != marks[0] {
		t.Errorf("written %d of %d (progress %d), want %d", fe.Written, fe.Total, last.Done, marks[0])
	}
	if !strings.Contains(fe.Recovery(), "Partially written") || !strings.Contains(fe.Recovery(), "hold BOOT, press and release RESET, release BOOT") ||
		!strings.Contains(fe.Recovery(), "update again") {
		t.Errorf("recovery %q", fe.Recovery())
	}
	if !strings.Contains(err.Error(), "while writing") {
		t.Errorf("error %q", err)
	}
}

// A board that never answers gives up after the cap with the owner's way out: about 10 s when it stays silent,
// sooner when it answers each reset by starting its app (4 resets).
func TestNoBootloader(t *testing.T) {
	for _, tc := range []struct {
		name        string
		banner      string
		resets      int
		least, most time.Duration
		says        string
	}{
		{"silent", "", 3, 10 * time.Second, 12 * time.Second, "it printed nothing"},
		{"running its app", "rst:0x1 (POWERON_RESET),boot:0x13 (SPI_FAST_FLASH_BOOT)\r\nMeshCore repeater\r\n", 4, 2 * time.Second, 6 * time.Second, "boot mode 0x13"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(ESP32, false)
			r.dev.IgnoreResets = 1000
			r.dev.AppBanner = []byte(tc.banner)
			port := r.open()
			start := r.clock.Now()
			var attempts []int
			o := r.options(esp32Update)
			o.Progress = func(p Progress) {
				if p.Stage == StageConnecting {
					attempts = append(attempts, p.Attempt)
				}
			}
			_, err := Flash(context.Background(), port, readTestdata(t, "esp32-app.bin"), o)
			fe := flashErr(t, err)
			if !errors.Is(err, ErrNoBootloader) || fe.Stage != StageConnecting || fe.Touched {
				t.Fatalf("got %v", err)
			}
			if took := r.clock.Now().Sub(start); took < tc.least || took > tc.most {
				t.Errorf("gave up after %v", took)
			}
			if len(attempts) != tc.resets || r.dev.Resets() != tc.resets {
				t.Errorf("attempts %v, resets %d, want %d", attempts, r.dev.Resets(), tc.resets)
			}
			for _, want := range []string{"Hold its BOOT button, press and release RESET, release BOOT", tc.says} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			if !strings.Contains(fe.Recovery(), "hold BOOT") {
				t.Errorf("recovery %q", fe.Recovery())
			}
			// The classic reset alternates its IO0 hold: 50 ms, then 550 ms.
			var holds []float64
			for _, e := range r.log.Events() {
				if e.Ev == "sleep" && e.Ms != 100 {
					holds = append(holds, e.Ms)
				}
			}
			if len(holds) < 2 || holds[0] != 50 || holds[1] != 550 {
				t.Errorf("holds %v", holds)
			}
			// Only SYNCs went out, 5 per reset.
			frames := wireFrames(r.log.TX())
			for _, f := range frames {
				if "tx "+hexString(f) != syncToken {
					t.Fatalf("sent %x", f)
				}
			}
			if len(frames) != 5*tc.resets {
				t.Errorf("%d SYNCs for %d resets", len(frames), tc.resets)
			}
		})
	}
}

// tinyUSB is an ESP32-S3 app on TinyUSB (303a:0002): the line states (DTR,RTS) (1,0) then (0,0), the end of the
// classic reset, reboot it into the ROM, which comes back as a new USB device, so this port goes away
// (docs/meshcore-flashing.md 6.2, source inference).
type tinyUSB struct {
	port     *serialport.Fake
	dtr, rts bool
}

func (d *tinyUSB) Receive([]byte) []byte { return nil }

func (d *tinyUSB) Lines(dtr, rts bool, _ int) []byte {
	if d.dtr && !d.rts && !dtr && !rts && d.port != nil {
		d.port.Unplug()
	}
	d.dtr, d.rts = dtr, rts
	return nil
}

// The contract internal/flash relies on to follow such a board to its new port: Flash stops while connecting,
// with serialport.ErrGone, the flash untouched, and nothing but the reset sent.
func TestTinyUSBReenumerates(t *testing.T) {
	log := &serialport.Log{}
	clock := serialport.NewVirtualClock(log)
	dev := &tinyUSB{}
	port := serialport.NewFake(dev, clock, log, serialport.Options{})
	dev.port = port
	_, err := Flash(context.Background(), port, readTestdata(t, "s3-app.bin"),
		Options{Clock: clock, USBVendor: 0x303a, USBProduct: 0x0002, ExpectChip: ESP32S3, Address: AppAddress})
	fe := flashErr(t, err)
	if !errors.Is(err, serialport.ErrGone) || fe.Stage != StageConnecting || fe.Touched {
		t.Fatalf("got %v (stage %v)", err, fe.Stage)
	}
	if !strings.Contains(fe.Recovery(), "Flash untouched") {
		t.Errorf("recovery %q", fe.Recovery())
	}
	if n := countTX(t, log); n != 0 {
		t.Errorf("%d frames sent to a port that went away", n)
	}
}

// esptool-js 0.5.4 never syncs once the first reset is missed (bug 4.10b, the prototype's esp32-ignore1); here the
// second reset, with the longer hold, gets through.
func TestSecondResetConnects(t *testing.T) {
	r := newRig(ESP32, false)
	r.dev.IgnoreResets = 1
	port := r.open()
	if _, err := Flash(context.Background(), port, readTestdata(t, "esp32-app.bin"), r.options(esp32Update)); err != nil {
		t.Fatal(err)
	}
	if r.dev.Resets() != 2+1 { // two connect attempts, then the hard reset's RTS pulse
		t.Errorf("%d resets", r.dev.Resets())
	}
	if m := r.dev.Mode(); m != "app" {
		t.Errorf("left in %q", m)
	}
}

func TestMD5Mismatch(t *testing.T) {
	r := newRig(ESP32, false)
	r.dev.CorruptWrite = true
	port := r.open()
	_, err := Flash(context.Background(), port, readTestdata(t, "esp32-merged.bin"), r.options(esp32Wipe))
	fe := flashErr(t, err)
	if !errors.Is(err, ErrVerify) || fe.Stage != StageVerifying || !fe.Touched || fe.Written != fe.Total {
		t.Fatalf("got %v (stage %v, %d of %d)", err, fe.Stage, fe.Written, fe.Total)
	}
	if !strings.Contains(fe.Recovery(), "doesn't match the file") {
		t.Errorf("recovery %q", fe.Recovery())
	}
	if r.dev.Count(opFlashBegin) != 0 || r.dev.Count(opFlashDeflEnd) != 0 {
		t.Error("it carried on after the mismatch")
	}
	if m := r.dev.Mode(); m != "app" {
		t.Errorf("left in %q; the RTS pulse should reset it", m)
	}
}

func TestTooBig(t *testing.T) {
	r := newRig(ESP32S3, false)
	r.dev.FlashID = 0x1240ef // 256 KB
	port := r.open()
	image := append(append([]byte(nil), readTestdata(t, "s3-app.bin")...), make([]byte, 200<<10)...)
	_, err := Flash(context.Background(), port, image, r.options(s3Update))
	fe := flashErr(t, err)
	if !errors.Is(err, ErrTooBig) || fe.Stage != StageStub || fe.Touched {
		t.Fatalf("got %v", err)
	}
}

// The owner's "stop" cancels the context; the flash ends with what it had done.
func TestCancel(t *testing.T) {
	r := newRig(ESP32S3, false)
	port := r.open()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := r.options(s3Update)
	o.Progress = func(p Progress) {
		if p.Stage == StageWriting && p.Done > 0 {
			cancel()
		}
	}
	_, err := Flash(ctx, port, readTestdata(t, "s3-app.bin"), o)
	fe := flashErr(t, err)
	if !errors.Is(err, context.Canceled) || fe.Stage != StageWriting || !fe.Touched {
		t.Fatalf("got %v", err)
	}
	if r.dev.Count(opFlashDeflData) != 1 {
		t.Errorf("%d blocks sent after the stop", r.dev.Count(opFlashDeflData))
	}
}

// A stub left running answers SYNC with value 0: nothing is uploaded and the stub's framing is used throughout.
func TestStubAlreadyRunning(t *testing.T) {
	r := newRig(ESP32, false)
	r.dev.StubResident = true
	port := r.open()
	image := readTestdata(t, "esp32-app.bin")
	if _, err := Flash(context.Background(), port, image, r.options(esp32Update)); err != nil {
		t.Fatal(err)
	}
	if r.dev.Count(opMemBegin) != 0 {
		t.Error("the stub was uploaded again")
	}
	padded := padImage(image)
	if !bytes.Equal(r.dev.Flash()[0x10000:0x10000+len(padded)], padded) {
		t.Error("not written")
	}
}

// The ROM answers SPI_FLASH_MD5 in hex (unreachable in a normal flash, which talks to the stub).
func TestROMFlashMD5(t *testing.T) {
	r := newRig(ESP32, false)
	data := bytes.Repeat([]byte{0x12, 0x34}, 2048)
	r.dev.SetFlash(data)
	port := r.open()
	l := NewLoader(context.Background(), port, Options{Clock: r.clock})
	if err := l.Connect(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DetectChip(); err != nil {
		t.Fatal(err)
	}
	got, err := l.FlashMD5(0, len(data))
	if err != nil {
		t.Fatal(err)
	}
	if got != md5Sum(data) {
		t.Errorf("md5 %x", got)
	}
	// And ERASE_FLASH is refused before the stub runs.
	if err := l.EraseFlash(); !errors.Is(err, ErrUnsupportedCommand) {
		t.Errorf("erase without the stub: %v", err)
	}
}

func md5Sum(b []byte) [16]byte { return md5.Sum(b) }

// The boot banner says whether the reset reached download mode; "waiting for download" is on the next line.
func TestBootModeRe(t *testing.T) {
	for banner, want := range map[string][2]string{
		fakeChips[ESP32].banner:   {"0x3", "download"},
		fakeChips[ESP32C3].banner: {"0x0", "download"},
		"rst:0x1 (POWERON_RESET),boot:0x13 (SPI_FAST_FLASH_BOOT)\r\n": {"0x13", ""},
	} {
		m := bootModeRe.FindStringSubmatch(banner)
		if m == nil || m[1] != want[0] || (m[2] != "") != (want[1] != "") {
			t.Errorf("%q: %q", banner, m)
		}
	}
}

func TestRecovery(t *testing.T) {
	for _, tc := range []struct {
		fe   FlashError
		want string
	}{
		{FlashError{Stage: StageCheck, Err: ErrImage}, "Nothing was sent"},
		{FlashError{Stage: StageConnecting, Err: ErrNoBootloader}, "hold BOOT, press and release RESET, release BOOT"},
		{FlashError{Stage: StageStub, Err: ErrNoReply}, "Flash untouched"},
		{FlashError{Stage: StageStub, Err: ErrNoReply}, "try again"},
		// Retrying can't fix a mismatch between the file and the board, so the advice mustn't suggest it.
		{FlashError{Stage: StageStub, Err: fmt.Errorf("%w: ESP32-C3", ErrWrongChip)}, "trying again won't help"},
		{FlashError{Stage: StageStub, Err: fmt.Errorf("%w: ESP32-C6", ErrUnsupportedChip)}, "trying again won't help"},
		{FlashError{Stage: StageStub, Err: ErrTooBig}, "Flash untouched: the old firmware is still there. This file can't go on this board"},
		{FlashError{Stage: StageWriting, Err: ErrNoReply, Address: 0x10000}, "Flash untouched"},
		{FlashError{Stage: StageErasing, Touched: true, EraseChip: true, Err: ErrNoReply}, "flash the full wipe again"},
		{FlashError{Stage: StageWriting, Touched: true, Address: 0, Err: ErrNoReply}, "flash the full wipe again"},
		{FlashError{Stage: StageWriting, Touched: true, Address: 0x10000, Err: serialport.ErrGone}, "flash the update again"},
		{FlashError{Stage: StageVerifying, Touched: true, Err: ErrVerify}, "doesn't match the file"},
		{FlashError{Stage: StageVerifying, Touched: true, Err: ErrNoReply}, "not checked"},
		{FlashError{Stage: StageResetting, Touched: true, Err: ErrNoReply}, "press its RESET button"},
	} {
		if got := tc.fe.Recovery(); !strings.Contains(got, tc.want) {
			t.Errorf("%v at %v: %q lacks %q", tc.fe.Err, tc.fe.Stage, got, tc.want)
		}
	}
}

func TestFlashSize(t *testing.T) {
	for id, want := range map[uint32]int{0x1740ef: 8 << 20, 0x1840c8: 16 << 20, 0x1640ef: 4 << 20, 0x1240ef: 256 << 10, 0x0: 0, 0xffffff: 0} {
		if got := FlashSize(id); got != want {
			t.Errorf("FlashSize(%#x) = %d, want %d", id, got, want)
		}
	}
}

func TestTimeoutPerMB(t *testing.T) {
	if got := timeoutPerMB(eraseWritePerMB, 16384); got != 3*time.Second {
		t.Errorf("small block: %v", got)
	}
	if got := timeoutPerMB(eraseWritePerMB, 200000); got != 8*time.Second {
		t.Errorf("200 kB: %v", got)
	}
	if got := timeoutPerMB(md5PerMB, 1223776); got != time.Duration(8000*1.223776*float64(time.Millisecond)) {
		t.Errorf("md5: %v", got)
	}
}

func TestParseChip(t *testing.T) {
	for in, want := range map[string]Chip{"esp32s3": ESP32S3, "ESP32-S3": ESP32S3, " esp32 ": ESP32, "esp32-c3": ESP32C3} {
		if got, ok := ParseChip(in); !ok || got != want {
			t.Errorf("ParseChip(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := ParseChip("nrf52840"); ok {
		t.Error("nrf52840 parsed")
	}
	if !ESP32.Supported() || !ESP32S3.Supported() || !ESP32C3.Supported() || ESP32C6.Supported() || Chip("x").Supported() {
		t.Error("Supported")
	}
	if ESP32S3.String() != "ESP32-S3" {
		t.Error(ESP32S3.String())
	}
}
