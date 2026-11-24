package nrfdfu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The full-size checks run only when MESHFLASH_SAMPLES points at the research folder, which holds real MeshCore
// v1.18.0 packages (too big, and not ours, to commit) and the research harness's recordings of dfu.js flashing them.

func samples(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("MESHFLASH_SAMPLES")
	if dir == "" {
		t.Skip("MESHFLASH_SAMPLES is not set")
	}
	return dir
}

// TestSampleRAK4631 flashes the real RAK4631 repeater package and compares every byte and sleep with dfu.js's
// recording of the same (proto-nrf52/out/rak4631-ok.jsonl: 865 frames, 454,160 bytes, 110 sleeps).
func TestSampleRAK4631(t *testing.T) {
	dir := samples(t)
	pkg := readPackage(t, filepath.Join(dir, "fw-samples-nrf52", "RAK_4631_repeater-v1.18.0-81dbae5.zip"))
	if len(pkg.Bin) != 440924 || pkg.Init.CRC16 != 0x49DF || len(pkg.Init.SDReq) != 1 || pkg.Init.SDReq[0] != SoftDeviceS140v6 {
		t.Fatalf("bin %d, init %+v", len(pkg.Bin), pkg.Init)
	}
	if _, err := pkg.CheckSoftDevice(SoftDeviceS140v6); err != nil {
		t.Error(err)
	}
	rec := loadRecording(t, filepath.Join(dir, "proto-nrf52", "out", "rak4631-ok.jsonl"))
	var wire, sleepCount int
	for _, w := range writes(rec) {
		wire += len(w)
	}
	for _, e := range rec {
		if e.T == "sleep" {
			sleepCount++
		}
	}
	if n := len(writes(rec)); n != 865 || wire != 454160 || sleepCount != 110 {
		t.Fatalf("recording: %d frames, %d bytes, %d sleeps", n, wire, sleepCount)
	}
	r := flashFake(t, pkg, "ok", func(b *FakeBootloader) { b.SoftDevice = SoftDeviceS140v6 })
	if r.err != nil {
		t.Fatal(r.err)
	}
	compareSteps(t, goSteps(r.log), nodeSteps(rec))
	if res := r.boot.Result(); !res.Valid || res.Received != 440924 || res.ImageCRC != 0x49DF {
		t.Errorf("fake bootloader: %+v", res)
	}
}

// TestSampleXiao checks the Xiao nRF52 package (S140 7.3.0) against the start of dfu.js's recordings of it, which
// end early by design (no ACK for INIT; a wrong ACK number on DATA#1), and flashes it whole.
func TestSampleXiao(t *testing.T) {
	dir := samples(t)
	pkg := readPackage(t, filepath.Join(dir, "fw-samples-nrf52", "Xiao_nrf52_repeater-v1.18.0-81dbae5.zip"))
	if len(pkg.Bin) != 360768 || pkg.Init.CRC16 != 0xE09E || len(pkg.Init.SDReq) != 1 || pkg.Init.SDReq[0] != SoftDeviceS140v7 {
		t.Fatalf("bin %d, init %+v", len(pkg.Bin), pkg.Init)
	}
	if _, err := pkg.CheckSoftDevice(SoftDeviceS140v6); !errors.Is(err, ErrWrongSoftDevice) {
		t.Errorf("a Xiao package on an S140 6.1.1 board: %v", err)
	}
	if _, err := pkg.CheckSoftDevice(SoftDeviceS140v7); err != nil {
		t.Fatal(err)
	}
	r := flashFake(t, pkg, "ok", func(b *FakeBootloader) { b.SoftDevice = SoftDeviceS140v7 })
	if r.err != nil {
		t.Fatal(r.err)
	}
	got := goSteps(r.log)
	for _, name := range []string{"xiao-drop-ack:1.jsonl", "xiao-bad-ack:2.jsonl"} {
		ref := nodeSteps(loadRecording(t, filepath.Join(dir, "proto-nrf52", "out", name)))
		ref = ref[:len(ref)-1] // the close after the failure
		// The recording's last write is a whole frame; the Go run carries on after it, so compare up to it.
		last := ref[len(ref)-1]
		g := append([]step(nil), got[:len(ref)]...)
		if last.Kind == "tx" && len(g[len(g)-1].Hex) > len(last.Hex) {
			g[len(g)-1].Hex = g[len(g)-1].Hex[:len(last.Hex)]
		}
		compareSteps(t, g, ref)
	}
	if res := r.boot.Result(); !res.Valid || res.Received != 360768 {
		t.Errorf("fake bootloader: %+v", res)
	}
}
