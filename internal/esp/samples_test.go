package esp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests use the real MeshCore v1.18.0 release files and the research prototype's full-size recordings, which
// are too big for the repository. They run only with MESHFLASH_SAMPLES pointing at the research folder:
//
//	MESHFLASH_SAMPLES=/home/user/meshflash-research go test ./internal/esp/ -run Samples

func samplesDir(t *testing.T) string {
	d := os.Getenv("MESHFLASH_SAMPLES")
	if d == "" {
		t.Skip("set MESHFLASH_SAMPLES to the research folder to run against the real MeshCore files")
	}
	return d
}

type sample struct {
	file  string
	chip  Chip
	addr  uint32
	erase bool
	pid   uint16
	// proto is the research prototype's recording of the web flasher writing this file, if there is one.
	proto string
}

var samples = []sample{
	{file: "Heltec_v2_repeater-v1.18.0-81dbae5-merged.bin", chip: ESP32, addr: 0, erase: true, pid: 0xea60, proto: "esp32-merged-wipe.jsonl"},
	{file: "Heltec_v3_repeater-v1.18.0-81dbae5-merged.bin", chip: ESP32S3, addr: 0, erase: true, pid: 0xea60},
	{file: "Heltec_v3_repeater-v1.18.0-81dbae5.bin", chip: ESP32S3, addr: AppAddress, pid: 0xea60, proto: "s3-update.jsonl"},
	{file: "Xiao_C3_repeater-v1.18.0-81dbae5-merged.bin", chip: ESP32C3, addr: 0, erase: true, pid: 0x1001, proto: "c3-jtag-wipe.jsonl"},
}

func TestSamplesImageChip(t *testing.T) {
	dir := samplesDir(t)
	for _, s := range samples {
		data, err := os.ReadFile(filepath.Join(dir, "fw-samples-esp32", s.file))
		if err != nil {
			t.Fatal(err)
		}
		if chip, err := ImageChip(data, s.addr); err != nil || chip != s.chip {
			t.Errorf("%s: %q, %v", s.file, chip, err)
		}
		if s.addr == 0 {
			if _, err := ImageChip(data, AppAddress); err == nil {
				t.Errorf("%s: a merged image passed as an app", s.file)
			}
		}
	}
}

// TestSamplesFlash writes each real file with Go's own compression and checks the fake's flash and the MD5.
func TestSamplesFlash(t *testing.T) {
	dir := samplesDir(t)
	for _, s := range samples {
		t.Run(s.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "fw-samples-esp32", s.file))
			if err != nil {
				t.Fatal(err)
			}
			r := newRig(s.chip, s.pid == usbJTAGSerialPID)
			port := r.open()
			o := r.options(recording{chip: s.chip, addr: s.addr, erase: s.erase, pid: s.pid})
			start := r.clock.Now()
			res, err := Flash(context.Background(), port, data, o)
			if err != nil {
				t.Fatal(err)
			}
			padded := padImage(data)
			if !bytes.Equal(r.dev.Flash()[s.addr:int(s.addr)+len(padded)], padded) {
				t.Error("the fake's flash doesn't hold the file")
			}
			t.Logf("%d bytes, Go zlib %d bytes in %d blocks, %v virtual", res.Size, res.Compressed,
				(res.Compressed+writeBlockSize-1)/writeBlockSize, r.clock.Now().Sub(start))
		})
	}
}

// TestSamplesPrototype compares Go with the research prototype's full-size recordings of the web flasher (made
// before sleeps were recorded, so frames and line changes only), the reference's pako stream injected.
func TestSamplesPrototype(t *testing.T) {
	dir := samplesDir(t)
	for _, s := range samples {
		if s.proto == "" {
			continue
		}
		t.Run(s.proto, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "fw-samples-esp32", s.file))
			if err != nil {
				t.Fatal(err)
			}
			evs := loadPrototype(t, filepath.Join(dir, "proto-esp32", "out", s.proto))
			refZ := referenceStream(t, evs)
			if s.proto == "esp32-merged-wipe.jsonl" {
				// The research saved pako's stream of this image on its own: 781,373 bytes, 48 blocks.
				pako, err := os.ReadFile(filepath.Join(dir, "proto-esp32", "out", "pako-heltecv2.z"))
				if err != nil || !bytes.Equal(pako, refZ) || len(refZ) != 781373 {
					t.Fatalf("pako stream: %d bytes, %v", len(refZ), err)
				}
			}
			r := newRig(s.chip, s.pid == usbJTAGSerialPID)
			port := r.open()
			o := r.options(recording{chip: s.chip, addr: s.addr, erase: s.erase, pid: s.pid})
			o.Compress = func([]byte) ([]byte, error) { return refZ, nil }
			if _, err := Flash(context.Background(), port, data, o); err != nil {
				t.Fatal(err)
			}
			noSleeps := func(toks []string) []string {
				var out []string
				for _, tok := range toks {
					if !strings.HasPrefix(tok, "sleep") {
						out = append(out, tok)
					}
				}
				return out
			}
			got := withoutOps(t, noSleeps(collapseSyncs(goTokens(t, r.log))), opSPIFlashMD5)
			compareTokens(t, "against the prototype's recording", got, collapseSyncs(nodeTokens(t, evs)))
		})
	}
}

// loadPrototype reads one of the prototype's plain .jsonl recordings (1.6 MB lines and all).
func loadPrototype(t *testing.T, path string) []nodeEvent {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var evs []nodeEvent
	for sc.Scan() {
		var e nodeEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return evs
}
