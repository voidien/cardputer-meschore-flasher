package catalog

import (
	"errors"
	"strings"
	"testing"
)

// testDevice builds a board with one firmware and one version holding the given files (type:name pairs).
func testDevice(typ DeviceType, files ...string) (Device, Version) {
	v := Version{Name: "v1.18.0"}
	for _, tf := range files {
		t, name, _ := strings.Cut(tf, ":")
		v.Files = append(v.Files, File{Type: t, Name: name, Title: name, URL: "https://flasher.meshcore.io/firmware/" + name,
			Source: SourceMeshCore})
	}
	d := Device{Name: "Test board", Type: typ, Source: SourceMeshCore,
		Firmware:   []Firmware{{Role: "repeater", Versions: []Version{v}}},
		neverWrite: map[string]bool{"FLASH_ERASE_nrf52_softdevice_v6.zip": true, "bl-0.9.2-OTAFIX2.2.zip": true}}
	return d, v
}

func TestChooseESP32(t *testing.T) {
	d, v := testDevice(ESP32, "flash-wipe:B_repeater-v1.18.0-81dbae5-merged.bin", "flash-update:B_repeater-v1.18.0-81dbae5.bin")
	got, err := Choose(d, v, Update)
	if err != nil || got.File.Name != "B_repeater-v1.18.0-81dbae5.bin" || got.Address != 0x10000 || got.EraseChip || got.Kind != Update {
		t.Fatalf("update: %+v %v", got, err)
	}
	got, err = Choose(d, v, Wipe)
	if err != nil || got.File.Name != "B_repeater-v1.18.0-81dbae5-merged.bin" || got.Address != 0 || !got.EraseChip || got.Kind != Wipe {
		t.Fatalf("wipe: %+v %v", got, err)
	}
	// The erase is decided afresh: an update right after a wipe never erases.
	if got, _ := Choose(d, v, Update); got.EraseChip {
		t.Fatal("update after wipe erases the chip")
	}
}

func TestChooseNRF52(t *testing.T) {
	d, v := testDevice(NRF52, "flash:R_repeater-v1.18.0-81dbae5.zip", "download:R_repeater-v1.18.0-81dbae5.uf2")
	got, err := Choose(d, v, Update)
	if err != nil || got.File.Name != "R_repeater-v1.18.0-81dbae5.zip" || got.Address != 0 || got.EraseChip {
		t.Fatalf("nRF52 update: %+v %v", got, err)
	}
}

// Every refusal says why, and wraps ErrRefused.
func TestChooseRefusals(t *testing.T) {
	type tc struct {
		name  string
		typ   DeviceType
		files []string
		kind  Kind
		want  string
	}
	for _, c := range []tc{
		{"no kind chosen", ESP32, []string{"flash-update:a.bin"}, 0, "choose update or full wipe"},
		{"nRF52 wipe", NRF52, []string{"flash:a.zip"}, Wipe, "no full wipe"},
		{"noflash", NoFlash, []string{"download:a.uf2"}, Update, "download only"},
		{"unknown chip", "rp2040", []string{"flash:a.uf2"}, Update, "can't flash"},
		{"zip for ESP32 update", ESP32, []string{"flash-update:a.zip"}, Update, "isn't an ESP32 image"},
		{"zip for ESP32 wipe", ESP32, []string{"flash-wipe:a-merged.zip"}, Wipe, "isn't an ESP32 image"},
		{"bin for nRF52", NRF52, []string{"flash:a.bin"}, Update, "isn't an nRF52 DFU package"},
		{"uf2 for nRF52", NRF52, []string{"flash:a.uf2"}, Update, "isn't an nRF52 DFU package"},
		{"download only on nRF52", NRF52, []string{"download:a.uf2"}, Update, "has no DFU package"},
		{"download only on ESP32", ESP32, []string{"download:a.bin"}, Update, "has no update image"},
		{"missing wipe", ESP32, []string{"flash-update:a.bin"}, Wipe, "has no full-wipe image"},
		{"missing update", ESP32, []string{"flash-wipe:a-merged.bin"}, Update, "has no update image"},
		// The reference would fall back to the first flash* file: the git stub's nRF52 .zip at 0x10000.
		{"flash zip on ESP32", ESP32, []string{"flash:LilyGo_T-Echo_Card_room_server-v1.18.0-81dbae5.zip"}, Update, "has no update image"},
		{"flash-update zip on nRF52", NRF52, []string{"flash-update:Ultra-WioTrackerL1-v10.1.zip"}, Update, "has no DFU package"},
		{"merged as update", ESP32, []string{"flash-update:a-merged.bin"}, Update, "merged image, which belongs at 0x0"},
		{"app as wipe", ESP32, []string{"flash-wipe:a.bin"}, Wipe, "isn't a merged image"},
		{"erase file", NRF52, []string{"flash:FLASH_ERASE_nrf52_softdevice_v6.zip"}, Update, "never writes"},
		{"bootloader file", NRF52, []string{"flash:bl-0.9.2-OTAFIX2.2.zip"}, Update, "never writes"},
	} {
		d, v := testDevice(c.typ, c.files...)
		got, err := Choose(d, v, c.kind)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %+v, %v; want a refusal saying %q", c.name, got, err, c.want)
		}
	}

	// A version that isn't the board's (the screen mixed two boards up) is refused.
	d, _ := testDevice(ESP32, "flash-update:a.bin")
	_, other := testDevice(ESP32, "flash-update:b.bin")
	if _, err := Choose(d, other, Update); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "not one of") {
		t.Errorf("another board's version: %v", err)
	}
}

// The git catalog's broken second "LilyGo T-Lora Pager" (an esp32 entry holding the T-Echo Card's nRF52 files) is
// refused for every version and kind; the real T-Lora Pager's Ripple builds are offered as the reference offers them.
func TestChooseGitStub(t *testing.T) {
	t.Parallel() // heavy, independent: the shared catalogs are resolved once and only read
	_, devs := goldenCases[2].resolved(t)
	var stub, real *Device
	for i := range devs {
		if devs[i].Name == "LilyGo T-Lora Pager" {
			if devs[i].Firmware[0].Class == "ripple" {
				real = &devs[i]
			} else {
				stub = &devs[i]
			}
		}
	}
	if stub == nil || real == nil {
		t.Fatal("the git catalog has two LilyGo T-Lora Pager entries")
	}
	n := 0
	for _, fw := range stub.Firmware {
		for _, v := range fw.Versions {
			for _, k := range []Kind{Update, Wipe} {
				n++
				if f, err := Choose(*stub, v, k); !errors.Is(err, ErrRefused) {
					t.Errorf("stub %s %s %s: chose %+v", fw.Role, v.Name, k, f)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("the stub has no versions")
	}
	if f, err := Choose(*real, real.Firmware[0].Versions[0], Update); err != nil || f.Address != 0x10000 {
		t.Errorf("real T-Lora Pager: %+v %v", f, err)
	}
}

func TestKindString(t *testing.T) {
	if Update.String() != "update" || Wipe.String() != "full wipe" || Kind(0).String() != "no choice" {
		t.Fatal(Update, Wipe, Kind(0))
	}
}

// Every erase and bootloader file either catalog names is refused on every board: a version of one board that lists
// another board's formatter, or ZephCore's, is no more firmware than its own would be. A "bootloader" given as one
// name instead of a list counts too.
func TestChooseRefusesAnyCatalogsEraseOrBootloader(t *testing.T) {
	version := func(file string) string {
		return `{"role":"repeater","version":{"v1":{"files":[{"type":"flash","name":"` + file + `","title":"x"}]}}}`
	}
	mesh := Raw{Config: []byte(`{"staticPath":"/firmware","device":[
		{"name":"A","type":"nrf52","erase":"FLASH_ERASE_nrf52_softdevice_v6.zip","firmware":[` +
		version("FLASH_ERASE_nrf52_softdevice_v7.zip") + `,` + version("/firmware/b_bootloader-OTAFIX.zip") + `,` +
		version("ZephCore_SoftDevice_v7_formatter.zip") + `,` + version("A_repeater-v1.zip") + `]},
		{"name":"B","type":"nrf52","erase":"FLASH_ERASE_nrf52_softdevice_v7.zip","bootloader":"b_bootloader-OTAFIX.zip",
			"firmware":[` + version("B_repeater-v1.zip") + `]}]}`)}
	zeph := Raw{Config: []byte(`{"staticPath":"/firmware","device":[
		{"name":"Z","type":"nrf52","erase":"ZephCore_SoftDevice_v7_formatter.zip","firmware":[` +
		version("FLASH_ERASE_nrf52_softdevice_v6.uf2") + `]}]}`)}
	c, err := Resolve(mesh, zeph, DefaultSites)
	if err != nil {
		t.Fatal(err)
	}
	a, z := c.Find("A"), c.Find("Z")
	for i, fw := range a.Firmware[:3] {
		if f, err := Choose(*a, fw.Versions[0], Update); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "never writes") {
			t.Errorf("A firmware %d: chose %s (%v)", i, f.File.Name, err)
		}
	}
	if f, err := Choose(*a, a.Firmware[3].Versions[0], Update); err != nil || f.File.Name != "A_repeater-v1.zip" {
		t.Errorf("A's own firmware: %+v %v", f, err)
	}
	if f, err := Choose(*z, z.Firmware[0].Versions[0], Update); !errors.Is(err, ErrRefused) {
		t.Errorf("ZephCore board given MeshCore's erase .uf2 name: chose %s (%v)", f.File.Name, err)
	}
}
