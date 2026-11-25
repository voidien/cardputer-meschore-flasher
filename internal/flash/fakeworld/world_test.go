package fakeworld

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/esp"
	"meshflash/internal/nodecli"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/serialport"
	"meshflash/internal/usbid"
)

// TestCatalogCopies: the catalog the world serves is byte for byte internal/catalog's saved live catalog.
func TestCatalogCopies(t *testing.T) {
	entries, err := catalogFiles.ReadDir("catalog")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(catalogRoutes) {
		t.Errorf("%d files embedded, %d served", len(entries), len(catalogRoutes))
	}
	for _, r := range catalogRoutes {
		ours, err := catalogFiles.ReadFile("catalog/" + r.file)
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := os.ReadFile(filepath.Join("..", "..", "catalog", "testdata", r.file))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ours, theirs) {
			t.Errorf("%s differs from internal/catalog/testdata's", r.file)
		}
	}
	cat, err := Catalog()
	if err != nil || len(cat.Devices) != 72 {
		t.Fatalf("%v, %d boards", err, len(cat.Devices))
	}
}

// The files the sites make up pass every check the catalog client, internal/esp and internal/nrfdfu make, and run
// what their names say once written.
func TestServedFiles(t *testing.T) {
	w := New()
	d, err := w.Deps(t.TempDir(), "test", false)
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := Catalog()
	for _, tc := range []struct {
		device, source, role string
		kind                 catalog.Kind
		chip                 esp.Chip
		sd                   uint16
		runs                 Firmware
	}{
		{"Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update, esp.ESP32S3, 0,
			Firmware{Role: nodecli.RoleRepeater, Version: "v1.18.0-81dbae5"}},
		{"Heltec v4", catalog.SourceMeshCore, "roomServer", catalog.Wipe, esp.ESP32S3, 0,
			Firmware{Role: nodecli.RoleRoomServer, Version: "v1.18.0-81dbae5"}},
		{"Heltec v2", catalog.SourceMeshCore, "repeater", catalog.Wipe, esp.ESP32, 0,
			Firmware{Role: nodecli.RoleRepeater, Version: "v1.18.0-81dbae5"}},
		{"Heltec v4", catalog.SourceZephCore, "repeater", catalog.Update, esp.ESP32S3, 0,
			Firmware{Role: nodecli.RoleRepeater, Version: "1.18.2-zephcore"}},
		{"Seeed Studio Xiao C6", catalog.SourceZephCore, "repeater", catalog.Update, esp.ESP32C6, 0,
			Firmware{Role: nodecli.RoleRepeater, Version: "1.18.2-zephcore"}},
		{"RAK WisBlock / WisMesh (RAK 4631)", catalog.SourceMeshCore, "companionBle", catalog.Update, "",
			nrfdfu.SoftDeviceS140v6, Firmware{Role: "companion", Version: "v1.18.0-81dbae5"}},
		{"Seeed Studio Xiao nRF52 WIO", catalog.SourceMeshCore, "repeater", catalog.Update, "",
			nrfdfu.SoftDeviceS140v7, Firmware{Role: nodecli.RoleRepeater, Version: "v1.18.0-81dbae5"}},
	} {
		dev := cat.Find(tc.device)
		var fl catalog.Flash
		for _, fw := range dev.Firmware {
			if fw.Source == tc.source && fw.Role == tc.role {
				if fl, err = catalog.Choose(*dev, fw.Versions[0], tc.kind); err != nil {
					t.Fatal(err)
				}
			}
		}
		e, err := d.Catalog.Download(context.Background(), fl.File, nil)
		if err != nil {
			t.Errorf("%s %s: %v", tc.device, fl.File.Base(), err)
			continue
		}
		data, _, err := d.Catalog.ReadVerified(e.Name)
		if err != nil {
			t.Fatal(err)
		}
		var app []byte
		if tc.chip != "" {
			chip, err := esp.ImageChip(data, fl.Address)
			if err != nil || chip != tc.chip {
				t.Errorf("%s: chip %v, %v", e.Name, chip, err)
			}
			app = data
			if fl.Address == 0 {
				app = data[esp.AppAddress:]
			}
		} else {
			pkg, err := nrfdfu.OpenPackage(data)
			if err != nil {
				t.Fatalf("%s: %v", e.Name, err)
			}
			if _, err := pkg.CheckSoftDevice(tc.sd); err != nil {
				t.Errorf("%s: %v", e.Name, err)
			}
			app = pkg.Bin
		}
		if fw, ok := parseMarker(app); !ok || fw != tc.runs {
			t.Errorf("%s runs %+v (%v), want %+v", e.Name, fw, ok, tc.runs)
		}
		// Written part-way over another image of the same size, it runs nothing.
		other := appImage(seedOf("another"), Firmware{Role: "companion", Version: "v1"}, 0)
		half := append(append([]byte(nil), app[:len(app)/2]...), other[len(app)/2:]...)
		if _, ok := parseMarker(half); ok && len(app) == len(other)+32 {
			t.Errorf("%s: a half-written image runs", e.Name)
		}
	}
	if got := w.Requests(); len(got) != 7 {
		t.Errorf("requests %q", got)
	}
}

// The world catches a backend breaking its rules, and says which, never with a command's bytes.
func TestProblemsCatchBadBackends(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(w *World, b *Board) error
		want string
	}{
		{"left open", func(w *World, b *Board) error {
			_, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200, Exclusive: true})
			return err
		}, "is still open"},
		{"not exclusive", func(w *World, b *Board) error {
			p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200})
			if err == nil {
				p.Close()
			}
			return err
		}, "without Exclusive"},
		{"no baud", func(w *World, b *Board) error {
			p, err := w.open("/dev/ttyACM0", serialport.Options{Exclusive: true})
			if err == nil {
				p.Close()
			}
			return err
		}, "without a baud"},
		{"a probe that changes the node", func(w *World, b *Board) error {
			p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200, Exclusive: true})
			if err != nil {
				return err
			}
			defer p.Close()
			_, err = p.Write([]byte("set name Hill\r"))
			return err
		}, "isn't ver, get <name> or board"},
		{"a probe asking the guest password", func(w *World, b *Board) error {
			p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200, Exclusive: true})
			if err != nil {
				return err
			}
			defer p.Close()
			_, err = p.Write([]byte("get guest.password\r"))
			return err
		}, "asked get guest.password"},
		{"a touch on an ESP32", func(w *World, b *Board) error {
			p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 1200, Exclusive: true})
			if err == nil {
				p.Close()
			}
			return err
		}, "isn't an nRF52 app's port"},
		{"FLASH_BEGIN outside a flash", func(w *World, b *Board) error {
			p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200, Exclusive: true})
			if err != nil {
				return err
			}
			defer p.Close()
			_, err = p.Write([]byte{0xC0, 0x00, 0x10, 0x10, 0x00, 0, 0, 0, 0, 0xC0})
			return err
		}, "FLASH_BEGIN"},
		{"a password in another command", func(w *World, b *Board) error {
			p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200, Exclusive: true})
			if err != nil {
				return err
			}
			p.(*worldPort).OpenedFor(purposeSetup)
			defer p.Close()
			p.Write([]byte("password Sekrit99\r"))
			_, err = p.Write([]byte("set lat Sekrit99\r"))
			return err
		}, "holds a password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := New()
			b := HeltecV4(Firmware{Role: nodecli.RoleRepeater, Version: "v1.18.0"})
			w.Plug(b)
			if err := tc.do(w, b); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(w.Problems(), "\n")
			if !strings.Contains(got, tc.want) {
				t.Errorf("problems %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "Sekrit99") || strings.Contains(got, "Hill") {
				t.Errorf("a problem quotes what was sent: %q", got)
			}
		})
	}
}

// The nRF52's own rules: a probe opening a bootloader, START into a used session, SLIP to an app, and a tty left
// at 1200 baud.
func TestProblemsNRF52(t *testing.T) {
	t.Run("a probe opens the bootloader", func(t *testing.T) {
		w := New()
		rak := RAK4631(Firmware{Role: "companion"})
		rak.DoublePressReset()
		w.Plug(rak)
		p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200, Exclusive: true})
		if err != nil {
			t.Fatal(err)
		}
		p.Close()
		if got := strings.Join(w.Problems(), "\n"); !strings.Contains(got, "opened the bootloader's port") {
			t.Errorf("problems %q", got)
		}
	})
	t.Run("the touch, and a tty left at 1200", func(t *testing.T) {
		w := New()
		rak := RAK4631(Firmware{Role: "companion"})
		w.Plug(rak)
		if err := nrfdfu.Touch(w.open, "/dev/ttyACM0", w.clock); err != nil {
			t.Fatal(err)
		}
		w.clock.Advance(2 * time.Second)
		ports, _ := w.Scan()
		if len(ports) != 1 || ports[0].Name != "ttyACM0" || ports[0].Interface != usbid.InterfaceNRFBootloader {
			t.Fatalf("after the touch: %+v", ports)
		}
		if got := strings.Join(w.Problems(), "\n"); !strings.Contains(got, "left at 1200 baud") {
			t.Errorf("problems %q", got)
		}
		// Two DFU sessions on one bootloader session: the second START goes into a session that already had one.
		pkg, err := nrfdfu.OpenPackage(dfuPackage(appImage(1, Firmware{Role: "companion", Version: "v1"}, 0), 0))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pkg.CheckSoftDevice(nrfdfu.SoftDeviceS140v6); err != nil {
			t.Fatal(err)
		}
		rak.mu.Lock()
		rak.boot, _ = nrfdfu.NewFakeBootloader("mute-from:2") // START and INIT answered, then nothing
		rak.mu.Unlock()
		for i := 0; i < 2; i++ {
			p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200, Exclusive: true})
			if err != nil {
				t.Fatal(err)
			}
			p.(*worldPort).OpenedFor(purposeFlash)
			if err := nrfdfu.Flash(context.Background(), p, w.clock, pkg, nil); err == nil {
				t.Fatal("a muted bootloader took the image")
			}
			p.Close()
			got := strings.Join(w.Problems(), "\n")
			if stale := strings.Contains(got, "START sent into a bootloader session that already had START"); stale != (i == 1) {
				t.Errorf("session %d: problems %q", i+1, got)
			}
		}
	})
	t.Run("SLIP to an app", func(t *testing.T) {
		w := New()
		w.Plug(RAK4631(Firmware{Role: "companion"}))
		p, err := w.open("/dev/ttyACM0", serialport.Options{Baud: 115200, Exclusive: true})
		if err != nil {
			t.Fatal(err)
		}
		p.(*worldPort).OpenedFor(purposeFlash)
		p.Write([]byte{0xC0, 0x00, 0x08, 0xC0})
		p.Close()
		if got := strings.Join(w.Problems(), "\n"); !strings.Contains(got, "SLIP frame to the nRF52's app") {
			t.Errorf("problems %q", got)
		}
	})
}

// The boards move between modes as the real ones do.
func TestTransitions(t *testing.T) {
	t.Run("double press from Bluetooth DFU", func(t *testing.T) {
		w := New()
		rak := RAK4631(Firmware{Role: "companion"})
		w.Plug(rak)
		rak.mu.Lock()
		rak.mode = modeBTDFU
		rak.tty = ""
		rak.mu.Unlock()
		if ports, _ := w.Scan(); len(ports) != 0 {
			t.Fatalf("%+v", ports)
		}
		rak.PressReset()
		w.clock.Advance(2 * time.Second)
		if ports, _ := w.Scan(); len(ports) != 0 {
			t.Errorf("a single press brought it back: %+v", ports)
		}
		rak.DoublePressReset()
		w.clock.Advance(500 * time.Millisecond)
		if ports, _ := w.Scan(); len(ports) != 0 {
			t.Errorf("back at once: %+v", ports)
		}
		w.clock.Advance(time.Second)
		ports, _ := w.Scan()
		if len(ports) != 1 || ports[0].PID != 0x0029 || ports[0].Interface != usbid.InterfaceNRFBootloader {
			t.Errorf("%+v", ports)
		}
		if n := len(rak.Sessions()); n != 1 {
			t.Errorf("%d sessions", n)
		}
	})
	t.Run("At", func(t *testing.T) {
		w := New()
		var at time.Time
		w.At(3*time.Second, func() { at = w.clock.Now() })
		start := w.clock.Now()
		w.clock.Advance(2 * time.Second)
		if !at.IsZero() {
			t.Error("ran early")
		}
		w.clock.Advance(2 * time.Second)
		if at.Sub(start) != 4*time.Second {
			t.Errorf("ran at %v", at.Sub(start))
		}
	})
	t.Run("battery, clock, dialout, messenger", func(t *testing.T) {
		w := New()
		dir := t.TempDir()
		if err := w.SetMessenger(dir, nodecli.MessengerRadio{Radio: nodecli.DefaultRadio, TxDBm: 20, HasTx: true}); err != nil {
			t.Fatal(err)
		}
		d, _ := w.Deps(t.TempDir(), "test", false)
		if p, ok := d.Battery(); p != 87 || !ok {
			t.Errorf("battery %d %v", p, ok)
		}
		w.SetBatteryUnreadable()
		if p, ok := d.Battery(); p != -1 || !ok {
			t.Errorf("unreadable %d %v", p, ok)
		}
		w.SetBattery(-1)
		if _, ok := d.Battery(); ok {
			t.Error("a battery on a PC")
		}
		if !d.ClockSynced() {
			t.Error("not synced")
		}
		if mr, note := nodecli.ReadMessengerRadio(d.MessengerPath); note != "" || mr.TxDBm != 20 {
			t.Errorf("messenger %+v %q", mr, note)
		}
		w.SetDialout(false)
		if ok, hint := d.Dialout(); ok || hint != usbid.DialoutHint {
			t.Errorf("dialout %v %q", ok, hint)
		}
	})
}

// The demo's world: two boards, the catalog saved, the battery at 87%.
func TestDemo(t *testing.T) {
	var log bytes.Buffer
	d, err := Demo(t.TempDir(), "test", &log)
	if err != nil {
		t.Fatal(err)
	}
	ports, err := d.Scan()
	if err != nil || len(ports) != 2 {
		t.Fatalf("%v %+v", err, ports)
	}
	cat, err := d.Catalog.Load()
	if err != nil || len(cat.Devices) != 72 {
		t.Fatalf("%v", err)
	}
	if p, ok := d.Battery(); p != 87 || !ok {
		t.Errorf("battery %d", p)
	}
	d.Logf("a line %d", 1)
	if log.String() != "a line 1\n" {
		t.Errorf("log %q", log.String())
	}
}
