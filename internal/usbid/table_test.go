package usbid

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// catalogNames reads testdata/catalog-names.txt: the "name" of every device in the live catalog of 24 Nov 2026
// (flasher.meshcore.io/config.json, saved as catalog-live2/flasher.meshcore.io_config.json__cb_20261124 in the
// research), one per line, in the catalog's order. TestCatalogNamesMatchSamples checks it against that file.
func catalogNames(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "catalog-names.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func catalogIndex(t *testing.T) map[string]int {
	t.Helper()
	idx := map[string]int{}
	for i, n := range catalogNames(t) {
		idx[n] = i
	}
	return idx
}

// TestTableNamesAreTheCatalogs: every board in the table is spelled exactly as the live catalog spells it, the table
// is in the catalog's order (Candidates relies on that), and every catalog board is either in the table or in
// notIdentified with a reason, never both.
func TestTableNamesAreTheCatalogs(t *testing.T) {
	idx := catalogIndex(t)
	if len(idx) != 70 {
		t.Fatalf("testdata/catalog-names.txt has %d names, want the live catalog's 70", len(idx))
	}
	inTable := map[string]bool{}
	last := -1
	for _, d := range table {
		i, ok := idx[d.name]
		if !ok {
			t.Errorf("table: %q is not a name in the live catalog", d.name)
			continue
		}
		if i <= last {
			t.Errorf("table: %q is out of the catalog's order", d.name)
		}
		last = i
		if inTable[d.name] {
			t.Errorf("table: %q twice", d.name)
		}
		inTable[d.name] = true
		if len(d.ids) == 0 {
			t.Errorf("table: %q has no USB identity", d.name)
		}
	}
	for name, why := range notIdentified {
		if _, ok := idx[name]; !ok {
			t.Errorf("notIdentified: %q is not a name in the live catalog", name)
		}
		if inTable[name] {
			t.Errorf("notIdentified: %q is in the table too", name)
		}
		if why == "" {
			t.Errorf("notIdentified: %q has no reason", name)
		}
	}
	for _, name := range catalogNames(t) {
		if _, skip := notIdentified[name]; !inTable[name] && !skip {
			t.Errorf("catalog board %q is in neither the table nor notIdentified", name)
		}
	}
}

// TestTableRows checks each row is well formed: a mode, a known chip, a note on every unverified row (so the owner
// sees why), and no product string on a bridge row (a bridge's strings say nothing about the board).
func TestTableRows(t *testing.T) {
	chips := map[Chip]bool{ChipUnknown: true, ESP32: true, ESP32S3: true, ESP32C3: true, NRF52840: true, RP2040: true}
	modes := map[Mode]bool{ModeApp: true, ModeBootloader: true, ModeROM: true, ModeEither: true}
	for _, d := range table {
		if !chips[d.chip] {
			t.Errorf("%s: chip %q", d.name, d.chip)
		}
		for _, id := range d.ids {
			if !modes[id.mode] {
				t.Errorf("%s %04x:%04x: mode %q", d.name, id.vid, id.pid, id.mode)
			}
			if id.unverified && !strings.Contains(strings.ToLower(id.note), "unverified") &&
				!strings.Contains(id.note, "not yet seen") {
				t.Errorf("%s %04x:%04x: unverified with no note saying so: %q", d.name, id.vid, id.pid, id.note)
			}
			if _, isBridge := bridges[[2]uint16{id.vid, id.pid}]; isBridge && id.product != "" {
				t.Errorf("%s %04x:%04x: a bridge row with a product", d.name, id.vid, id.pid)
			}
			if d.chip == NRF52840 && id.mode == ModeROM || d.chip != NRF52840 && id.mode == ModeBootloader {
				t.Errorf("%s %04x:%04x: mode %s on %s", d.name, id.vid, id.pid, id.mode, d.chip)
			}
		}
	}
}

func TestDeviceChip(t *testing.T) {
	for _, tt := range []struct {
		name string
		chip Chip
		ok   bool
	}{
		{"Heltec v4", ESP32S3, true},
		{"Heltec v2", ESP32, true},
		{"Seeed Studio Xiao C3", ESP32C3, true},
		{"RAK WisBlock / WisMesh (RAK 4631)", NRF52840, true},
		{"LilyGo T-Deck Max", ChipUnknown, false},
		{"Elecrow ThinkNode M9", ChipUnknown, false},
		{"No such board", ChipUnknown, false},
	} {
		chip, ok := DeviceChip(tt.name)
		if chip != tt.chip || ok != tt.ok {
			t.Errorf("DeviceChip(%q) = %q, %v; want %q, %v", tt.name, chip, ok, tt.chip, tt.ok)
		}
	}
}

// TestTableMatchesResearchMap checks the table against the research's own mapping of each live catalog board to the
// MeshCore board files its firmware is built from: scripts/usbids/map_live.json, written by the research's
// map_devices.py from the live catalog of 24 Nov 2026 and MeshCore at 75b04ef0, not from this table. So it is the
// one check of the table's content that the Go code didn't produce (lookup.golden is the table turned round). It
// runs only when MESHFLASH_SAMPLES points at the research samples. For each catalog board:
//
//   - the table's chip is the board file's mcu, and fits the catalog's type;
//   - nRF52: every board file the firmware is built from gives an app identity (hwids[0], usb_product) in the table,
//     and the table has no app identity none of them gives (this caught the MeshTower V2's KISS modem firmware,
//     built for the MeshSolar);
//   - ESP32: the identities follow the USB flags (research §6.2): USB_MODE=1 with CDC_ON_BOOT=1, or a C3, is the
//     USB-Serial-JTAG in app and ROM; USB_MODE=0 with CDC_ON_BOOT=1 is a TinyUSB app identity plus the
//     USB-Serial-JTAG in ROM; CDC_ON_BOOT=0 or a classic ESP32 is a bridge chip; and the table has no kind of
//     identity no board file gives;
//   - a board with no MeshCore build is Ripple-only (unverified ROM rows, chip unknown) or in notIdentified.
func TestTableMatchesResearchMap(t *testing.T) {
	dir := os.Getenv("MESHFLASH_SAMPLES")
	if dir == "" {
		t.Skip("MESHFLASH_SAMPLES not set")
	}
	b, err := os.ReadFile(filepath.Join(dir, "scripts", "usbids", "map_live.json"))
	if err != nil {
		t.Fatal(err)
	}
	var research []struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Boards []struct {
			Board      string          `json:"board"`
			MCU        string          `json:"mcu"`
			HWIDs      [][]string      `json:"hwids"`
			USBProduct string          `json:"usb_product"`
			USBFlags   []string        `json:"usb_flags"`
			ExtraFlags json.RawMessage `json:"extra_flags"`
		} `json:"boards"`
	}
	if err := json.Unmarshal(b, &research); err != nil {
		t.Fatal(err)
	}
	var mapNames []string
	for _, r := range research {
		mapNames = append(mapNames, r.Name)
	}
	if want := catalogNames(t); strings.Join(mapNames, "\n") != strings.Join(want, "\n") {
		t.Fatalf("map_live.json names the catalog's boards differently from testdata/catalog-names.txt")
	}
	devices := map[string]*device{}
	for i := range table {
		devices[table[i].name] = &table[i]
	}

	for _, r := range research {
		d := devices[r.Name]
		if d == nil {
			if _, ok := notIdentified[r.Name]; !ok {
				t.Errorf("%s: not in the table or notIdentified", r.Name)
			}
			continue
		}
		switch r.Type {
		case "nrf52":
			if d.chip != NRF52840 {
				t.Errorf("%s: catalog type nrf52, table chip %q", r.Name, d.chip)
			}
		case "esp32":
			if d.chip != ESP32 && d.chip != ESP32S3 && d.chip != ESP32C3 && d.chip != ChipUnknown {
				t.Errorf("%s: catalog type esp32, table chip %q", r.Name, d.chip)
			}
		case "noflash":
			if d.chip != RP2040 {
				t.Errorf("%s: catalog type noflash, table chip %q", r.Name, d.chip)
			}
			continue // built for another platform, whose identity the research couldn't read from a board file
		default:
			t.Errorf("%s: catalog type %q", r.Name, r.Type)
		}

		if len(r.Boards) == 0 {
			for _, id := range d.ids {
				if d.chip != ChipUnknown || !id.unverified || id.mode != ModeROM {
					t.Errorf("%s: no MeshCore build, yet the table has a verified or non-ROM row, or a chip", r.Name)
				}
			}
			continue
		}

		// What the board files say, and what the table has.
		appIDs := map[string]bool{} // nRF52 "vid:pid|product" from the board files
		kinds := map[string]bool{}  // ESP32 "jtag", "tinyusb", "bridge" from the board files' flags
		for _, bf := range r.Boards {
			if Chip(bf.MCU) != d.chip {
				t.Errorf("%s: board file %s has mcu %q, table chip %q", r.Name, bf.Board, bf.MCU, d.chip)
			}
			if d.chip == NRF52840 {
				if len(bf.HWIDs) == 0 || len(bf.HWIDs[0]) != 2 {
					t.Fatalf("%s: board file %s has no hwids", r.Name, bf.Board)
				}
				vid, err1 := strconv.ParseUint(bf.HWIDs[0][0], 0, 16)
				pid, err2 := strconv.ParseUint(bf.HWIDs[0][1], 0, 16)
				if err1 != nil || err2 != nil {
					t.Fatalf("%s: board file %s hwids %v", r.Name, bf.Board, bf.HWIDs[0])
				}
				appIDs[fmt.Sprintf("%04x:%04x|%s", vid, pid, bf.USBProduct)] = true
				continue
			}
			kinds[espUSBKind(t, r.Name, bf.MCU, bf.ExtraFlags, bf.USBFlags)] = true
		}

		if d.chip == NRF52840 {
			have := map[string]bool{}
			for _, id := range d.ids {
				if id.mode == ModeApp || id.mode == ModeEither {
					have[fmt.Sprintf("%04x:%04x|%s", id.vid, id.pid, id.product)] = true
				}
			}
			for k := range appIDs {
				if !have[k] {
					t.Errorf("%s: its firmware shows %s, which the table doesn't list", r.Name, k)
				}
			}
			for k := range have {
				if !appIDs[k] {
					t.Errorf("%s: the table lists app identity %s, which no board file gives", r.Name, k)
				}
			}
			continue
		}

		tableKinds := map[string]bool{}
		for _, id := range d.ids {
			_, isBridge := bridges[[2]uint16{id.vid, id.pid}]
			switch {
			case isBridge:
				tableKinds["bridge"] = true
			case id.vid == vidEspressif && id.product == productUSBJTAG && id.mode == ModeEither:
				tableKinds["jtag"] = true
			case id.vid == vidEspressif && id.product != productUSBJTAG && id.mode == ModeApp:
				tableKinds["tinyusb"] = true
			case id.vid == vidEspressif && id.product == productUSBJTAG && id.mode == ModeROM:
				tableKinds["rom"] = true
			default:
				t.Errorf("%s: a row of no known kind: %+v", r.Name, id)
			}
		}
		if kinds["tinyusb"] || kinds["jtag"] {
			kinds["rom"] = true // a native-USB board's ROM is the USB-Serial-JTAG (research §6.2)
		}
		if tableKinds["jtag"] {
			tableKinds["rom"] = true // the USB-Serial-JTAG row is ModeEither: it covers the ROM too
		}
		if !reflect.DeepEqual(kinds, tableKinds) {
			t.Errorf("%s: the board files give %v, the table has %v", r.Name, keys(kinds), keys(tableKinds))
		}
	}
}

// espUSBKind says which USB path an ESP32 board file and its env's flags give the console (research §6.2): "jtag",
// "tinyusb" or "bridge". The env's flags come after the board file's, so the last value of each flag wins.
func espUSBKind(t *testing.T, name, mcu string, extra json.RawMessage, envFlags []string) string {
	t.Helper()
	var flags []string
	if err := json.Unmarshal(extra, &flags); err != nil {
		var s string
		if json.Unmarshal(extra, &s) == nil {
			flags = strings.Fields(s)
		}
	}
	usbMode, cdcOnBoot := "", "0"
	for _, f := range append(flags, envFlags...) {
		f = strings.TrimPrefix(strings.ReplaceAll(f, " ", ""), "-D")
		if v, ok := strings.CutPrefix(f, "ARDUINO_USB_MODE="); ok {
			usbMode = v
		}
		if v, ok := strings.CutPrefix(f, "ARDUINO_USB_CDC_ON_BOOT="); ok {
			cdcOnBoot = v
		}
	}
	switch {
	case mcu == "esp32" || cdcOnBoot == "0":
		return "bridge"
	case mcu == "esp32c3" || usbMode == "1":
		return "jtag"
	case usbMode == "0":
		return "tinyusb"
	}
	t.Errorf("%s: can't tell its USB path from mcu %q and flags %v %v", name, mcu, flags, envFlags)
	return ""
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestCatalogNamesMatchSamples checks testdata/catalog-names.txt against the saved live catalog in the research
// samples, and rewrites it from there with -update. It runs only when MESHFLASH_SAMPLES points at the samples
// (/home/user/meshflash-research), since the catalog is too big to keep in testdata.
func TestCatalogNamesMatchSamples(t *testing.T) {
	dir := os.Getenv("MESHFLASH_SAMPLES")
	if dir == "" {
		t.Skip("MESHFLASH_SAMPLES not set")
	}
	b, err := os.ReadFile(filepath.Join(dir, "catalog-live2", "flasher.meshcore.io_config.json__cb_20261124"))
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Device []struct {
			Name string `json:"name"`
		} `json:"device"`
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for _, d := range cat.Device {
		want.WriteString(d.Name + "\n")
	}
	golden(t, "catalog-names.txt", []byte(want.String()))
}
