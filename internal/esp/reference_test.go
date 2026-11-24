package esp

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"meshflash/internal/serialport"
)

// The recordings in testdata were made by reference/esp32 (make record): the web flasher's own js/flash.js and
// lib/esp32.js (esptool-js 0.5.4) run in Node against fake.mjs's FakeEsp, which FakeESP ports. Each comes plain and
// with MD5=1, esptool-js's own MD5 verify switched on, which shows where SPI_FLASH_MD5 goes.

type recording struct {
	name  string
	chip  Chip
	image string
	addr  uint32
	erase bool
	pid   uint16
	regs  map[uint32]uint32
	polls int
}

var recordings = []recording{
	{name: "esp32-update", chip: ESP32, image: "esp32-app.bin", addr: 0x10000, pid: 0xea60},
	{name: "esp32-wipe", chip: ESP32, image: "esp32-merged.bin", addr: 0x0, erase: true, pid: 0xea60},
	{name: "s3-update", chip: ESP32S3, image: "s3-app.bin", addr: 0x10000, pid: 0xea60},
	{name: "c3-jtag-wipe", chip: ESP32C3, image: "c3-merged.bin", addr: 0x0, erase: true, pid: 0x1001},
	// The ESP32-S3 on its USB-OTG port (0x800-byte stub blocks), with ECO0 eFuses (other eFuse reads) and 2 busy
	// polls of SPI_CMD: the value-dependent parts of the reference.
	{name: "s3-otg-eco0", chip: ESP32S3, image: "s3-app.bin", addr: 0x10000, pid: 0xea60,
		regs: map[uint32]uint32{0x3fcef14c: 3, 0x6000706c: 1, 0x60007050: 1 << 24}, polls: 2},
}

// nodeEvent is one line of a run.mjs recording.
type nodeEvent struct {
	Ev   string         `json:"ev"`
	Opts map[string]any `json:"opts"`
	DTR  *bool          `json:"dataTerminalReady"`
	RTS  *bool          `json:"requestToSend"`
	Ms   float64        `json:"ms"`
	Hex  string         `json:"hex"`
	Why  string         `json:"why"`
	Msg  string         `json:"msg"`
}

func loadRecording(t *testing.T, name string) []nodeEvent {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var evs []nodeEvent
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e nodeEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		evs = append(evs, e)
	}
	if last := evs[len(evs)-1]; last.Ev != "done" {
		t.Fatalf("%s: the reference run didn't finish: %+v", name, last)
	}
	return evs
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// wireFrames splits a byte stream into whole SLIP frames, C0s included, skipping bytes outside frames (a banner).
func wireFrames(stream []byte) [][]byte {
	var out [][]byte
	start := -1
	for i, b := range stream {
		if b != slipEnd {
			continue
		}
		if start >= 0 && i > start+1 {
			out = append(out, stream[start:i+1])
			start = -1
		} else {
			start = i
		}
	}
	return out
}

func unslip(t *testing.T, wire []byte) []byte {
	t.Helper()
	p, ok := slipDecode(wire[1 : len(wire)-1])
	if !ok {
		t.Fatalf("bad SLIP frame %x", wire)
	}
	return p
}

// Tokens are what the comparison sees: each frame the host sends, each line change and each sleep, in order.
func nodeTokens(t *testing.T, evs []nodeEvent) []string {
	var toks []string
	for _, e := range evs {
		switch e.Ev {
		case "signals":
			if e.DTR != nil {
				toks = append(toks, fmt.Sprintf("dtr=%v", *e.DTR))
			}
			if e.RTS != nil {
				toks = append(toks, fmt.Sprintf("rts=%v", *e.RTS))
			}
		case "sleep":
			toks = append(toks, fmt.Sprintf("sleep %v", e.Ms))
		case "tx":
			for _, f := range wireFrames(mustHex(t, e.Hex)) {
				toks = append(toks, "tx "+hex.EncodeToString(f))
			}
		}
	}
	return toks
}

func goTokens(t *testing.T, log *serialport.Log) []string {
	var toks []string
	for _, e := range log.Events() {
		switch e.Ev {
		case "dtr", "rts":
			toks = append(toks, fmt.Sprintf("%s=%v", e.Ev, *e.On))
		case "sleep":
			toks = append(toks, fmt.Sprintf("sleep %v", e.Ms))
		case "tx":
			for _, f := range wireFrames(mustHex(t, e.Hex)) {
				toks = append(toks, "tx "+hex.EncodeToString(f))
			}
		}
	}
	return toks
}

var syncToken = "tx " + hex.EncodeToString(slipEncode(packet(opSync, append([]byte{7, 7, 0x12, 0x20}, bytes.Repeat([]byte{0x55}, 32)...), 0)))

// collapseSyncs keeps one SYNC of each run of SYNCs: how many go out before one is answered depends on how the
// boot banner was chunked (esptool-js 0.5.4 bug 4.10a), not on the protocol.
func collapseSyncs(toks []string) []string {
	var out []string
	for i, tok := range toks {
		if tok == syncToken && i > 0 && toks[i-1] == syncToken {
			continue
		}
		out = append(out, tok)
	}
	return out
}

func withoutOps(t *testing.T, toks []string, ops ...byte) []string {
	var out []string
	for _, tok := range toks {
		if strings.HasPrefix(tok, "tx ") {
			p := unslip(t, mustHex(t, tok[3:]))
			if bytes.IndexByte(ops, p[1]) >= 0 {
				continue
			}
		}
		out = append(out, tok)
	}
	return out
}

// describe makes a token readable in a failure message.
func describe(t *testing.T, tok string) string {
	if !strings.HasPrefix(tok, "tx ") {
		return tok
	}
	p := unslip(t, mustHex(t, tok[3:]))
	s := fmt.Sprintf("tx %s size=%d", opName(p[1]), binary.LittleEndian.Uint16(p[2:]))
	if len(p) >= 12 {
		s += fmt.Sprintf(" w0=0x%08x", binary.LittleEndian.Uint32(p[8:]))
	}
	if len(tok) > 120 {
		return s + " " + tok[3:80] + "…"
	}
	return s + " " + tok[3:]
}

func compareTokens(t *testing.T, what string, got, want []string) {
	t.Helper()
	n := min(len(got), len(want))
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			var ctx strings.Builder
			for j := max(0, i-3); j < min(n, i+3); j++ {
				mark := "  "
				if j == i {
					mark = "> "
				}
				fmt.Fprintf(&ctx, "%s%d go:  %s\n%s%d ref: %s\n", mark, j, describe(t, got[j]), mark, j, describe(t, want[j]))
			}
			t.Fatalf("%s: first difference at %d of %d/%d:\n%s", what, i, len(got), len(want), ctx.String())
		}
	}
	if len(got) != len(want) {
		var extra []string
		if len(got) > n {
			for _, tok := range got[n:min(len(got), n+5)] {
				extra = append(extra, "go:  "+describe(t, tok))
			}
		} else {
			for _, tok := range want[n:min(len(want), n+5)] {
				extra = append(extra, "ref: "+describe(t, tok))
			}
		}
		t.Fatalf("%s: %d tokens from Go, %d from the reference; next:\n%s", what, len(got), len(want), strings.Join(extra, "\n"))
	}
}

// referenceStream is the reference's own zlib stream (pako, level 9): the FLASH_DEFL_DATA payloads in order.
func referenceStream(t *testing.T, evs []nodeEvent) []byte {
	var z []byte
	for _, e := range evs {
		if e.Ev != "tx" {
			continue
		}
		for _, f := range wireFrames(mustHex(t, e.Hex)) {
			if p := unslip(t, f); p[1] == opFlashDeflData {
				z = append(z, p[8+16:]...)
			}
		}
	}
	return z
}

// rxFrames are the board's replies, without the SYNC replies (their number follows the SYNC count) and banners.
func rxFrames(t *testing.T, stream []byte) []string {
	var out []string
	for _, f := range wireFrames(stream) {
		if p := unslip(t, f); len(p) >= 2 && p[0] == 1 && p[1] == opSync {
			continue
		}
		out = append(out, hex.EncodeToString(f))
	}
	return out
}

func nodeRX(t *testing.T, evs []nodeEvent) []byte {
	var b []byte
	for _, e := range evs {
		if e.Ev == "rx" && e.Why != "banner" {
			b = append(b, mustHex(t, e.Hex)...)
		}
	}
	return b
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rig is a fake board on a fake port in virtual time, recording everything.
type rig struct {
	log   *serialport.Log
	clock *serialport.VirtualClock
	dev   *FakeESP
	port  *serialport.Fake
}

func newRig(chip Chip, jtag bool) *rig {
	r := &rig{log: &serialport.Log{}, dev: NewFakeESP(chip)}
	r.clock = serialport.NewVirtualClock(r.log)
	r.dev.USBJTAG = jtag
	return r
}

// open plugs the board in (after the test has set the fake's fields).
func (r *rig) open() *serialport.Fake {
	r.port = serialport.NewFake(r.dev, r.clock, r.log, serialport.Options{Baud: 115200})
	r.dev.Port = r.port
	return r.port
}

func (r *rig) options(rec recording) Options {
	vid := uint16(0x10c4)
	if rec.pid == usbJTAGSerialPID {
		vid = 0x303a
	}
	return Options{Clock: r.clock, USBVendor: vid, USBProduct: rec.pid, ExpectChip: rec.chip, Address: rec.addr, EraseChip: rec.erase}
}

// TestReference flashes each recorded case with the reference's compressed stream injected and compares every
// frame sent, every line change and every sleep with the Node recording of the web flasher.
func TestReference(t *testing.T) {
	for _, rec := range recordings {
		t.Run(rec.name, func(t *testing.T) {
			plain := loadRecording(t, rec.name)
			withMD5 := loadRecording(t, rec.name+"-md5")
			image := readTestdata(t, rec.image)
			refZ := referenceStream(t, plain)
			if !bytes.Equal(refZ, referenceStream(t, withMD5)) {
				t.Fatal("the two recordings compressed differently")
			}

			r := newRig(rec.chip, rec.pid == usbJTAGSerialPID)
			r.dev.Regs, r.dev.Polls = rec.regs, rec.polls
			port := r.open()
			o := r.options(rec)
			o.Compress = func(data []byte) ([]byte, error) {
				// The stream must be the reference's compression of exactly what Go pads and sends.
				zr, err := zlib.NewReader(bytes.NewReader(refZ))
				if err != nil {
					return nil, err
				}
				inflated, err := io.ReadAll(zr)
				if err != nil || !bytes.Equal(inflated, data) {
					t.Errorf("the reference's stream inflates to %d bytes, not the %d Go padded (err %v)", len(inflated), len(data), err)
				}
				return refZ, nil
			}
			var stages []Stage
			o.Progress = func(p Progress) {
				if len(stages) == 0 || stages[len(stages)-1] != p.Stage {
					stages = append(stages, p.Stage)
				}
			}
			res, err := Flash(context.Background(), port, image, o)
			if err != nil {
				t.Fatalf("Flash: %v\n%s", err, err.(*FlashError).Recovery())
			}

			got := collapseSyncs(goTokens(t, r.log))
			compareTokens(t, "against the MD5=1 recording", got, collapseSyncs(nodeTokens(t, withMD5)))
			compareTokens(t, "against the plain recording, without SPI_FLASH_MD5",
				withoutOps(t, got, opSPIFlashMD5), collapseSyncs(nodeTokens(t, plain)))

			// The Go fake answers as the Node fake did.
			compareTokens(t, "the fake's replies", rxFrames(t, r.log.RX()), rxFrames(t, nodeRX(t, withMD5)))

			padded := padImage(image)
			if got := r.dev.Flash()[rec.addr : int(rec.addr)+len(padded)]; !bytes.Equal(got, padded) {
				t.Error("the fake's flash doesn't hold the image")
			}
			if res.Size != len(padded) || res.Compressed != len(refZ) || res.Chip.Chip != rec.chip {
				t.Errorf("result %+v", res)
			}
			if rec.erase != r.dev.Erased() {
				t.Errorf("erased = %v, want %v", r.dev.Erased(), rec.erase)
			}
			want := []Stage{StageConnecting, StageStub, StageWriting, StageVerifying, StageResetting}
			if rec.erase {
				want = []Stage{StageConnecting, StageStub, StageErasing, StageWriting, StageVerifying, StageResetting}
			}
			if fmt.Sprint(stages) != fmt.Sprint(want) {
				t.Errorf("stages %v, want %v", stages, want)
			}
			if m := r.dev.Mode(); m != "app" {
				t.Errorf("after the reset the fake is in %q, want the app", m)
			}
			if n := r.dev.Count(opSync); n != 1 {
				t.Errorf("%d SYNCs, want 1 (the banner drained first)", n)
			}
		})
	}
}

// TestReferenceGoZlib flashes with Go's own compressor: everything but FLASH_DEFL_BEGIN (its block count) and the
// FLASH_DEFL_DATA blocks is the reference's, and the board ends up with the image.
func TestReferenceGoZlib(t *testing.T) {
	for _, rec := range recordings {
		t.Run(rec.name, func(t *testing.T) {
			withMD5 := loadRecording(t, rec.name+"-md5")
			image := readTestdata(t, rec.image)
			r := newRig(rec.chip, rec.pid == usbJTAGSerialPID)
			r.dev.Regs, r.dev.Polls = rec.regs, rec.polls
			port := r.open()
			if _, err := Flash(context.Background(), port, image, r.options(rec)); err != nil {
				t.Fatal(err)
			}
			strip := func(toks []string) []string { return withoutOps(t, toks, opFlashDeflBegin, opFlashDeflData) }
			compareTokens(t, "without the compressed blocks", strip(collapseSyncs(goTokens(t, r.log))), strip(collapseSyncs(nodeTokens(t, withMD5))))
			padded := padImage(image)
			if got := r.dev.Flash()[rec.addr : int(rec.addr)+len(padded)]; !bytes.Equal(got, padded) {
				t.Error("the fake's flash doesn't hold the image")
			}
		})
	}
}

// TestGoZlibInflates checks that Go's stream of each image inflates back to it, block marks and all.
func TestGoZlibInflates(t *testing.T) {
	for _, name := range []string{"esp32-app.bin", "esp32-merged.bin", "s3-app.bin", "c3-merged.bin"} {
		padded := padImage(readTestdata(t, name))
		z, err := ZlibCompress(padded)
		if err != nil {
			t.Fatal(err)
		}
		if z[0] != 0x78 || z[1] != 0xDA {
			t.Errorf("%s: zlib header %x, want 78da (best compression)", name, z[:2])
		}
		marks, err := inflateMarks(z, padded)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i := 1; i < len(marks); i++ {
			if marks[i] < marks[i-1] {
				t.Errorf("%s: marks go backwards: %v", name, marks)
			}
		}
		if marks[len(marks)-1] != len(padded) {
			t.Errorf("%s: last mark %d, want %d", name, marks[len(marks)-1], len(padded))
		}
	}
}
