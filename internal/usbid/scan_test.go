package usbid

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestScanReadsACMAndUSBSerial plugs in one board of each layout and checks every field Scan reads: a cdc_acm board
// (tty under the interface) and a cp210x bridge (tty under the usb-serial port, under the interface).
func TestScanReadsACMAndUSBSerial(t *testing.T) {
	f := newFakeSys(t)
	f.plugACM("ttyACM0", heltecV4)
	f.plugUSBSerial("ttyUSB0", cp2102, "cp210x")

	got := f.scan()
	want := []Port{
		{Name: "ttyACM0", Dev: "/dev/ttyACM0", VID: 0x303a, PID: 0x1001, BCD: 0x0101, Manufacturer: "Espressif",
			Product: "USB JTAG/serial debug unit", Serial: "60:55:F9:C0:27:18", Interface: "", Driver: "cdc_acm",
			USBPath: "1-1.3"},
		{Name: "ttyUSB0", Dev: "/dev/ttyUSB0", VID: 0x10c4, PID: 0xea60, BCD: 0x0100, Manufacturer: "Silicon Labs",
			Product: "CP2102 USB to UART Bridge Controller", Serial: "0001", Driver: "cp210x", USBPath: "1-1.2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Scan:\n got %+v\nwant %+v", got, want)
	}
	if got[0].VIDPID() != "303a:1001" || got[1].VIDPID() != "10c4:ea60" {
		t.Errorf("VIDPID: %q, %q", got[0].VIDPID(), got[1].VIDPID())
	}
}

// TestScanMissingStrings: a CH340 has no manufacturer and no serial, so the kernel leaves those files out. They read
// as empty strings, and the driver is the interface's ("ch341"), not the usb-serial port's ("ch341-uart").
func TestScanMissingStrings(t *testing.T) {
	f := newFakeSys(t)
	f.plugUSBSerial("ttyUSB0", ch340, "ch341-uart")

	got := f.scan()
	want := []Port{{Name: "ttyUSB0", Dev: "/dev/ttyUSB0", VID: 0x1a86, PID: 0x7523, BCD: 0x0264,
		Product: "USB2.0-Serial", Driver: "ch341", USBPath: "1-1.4"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Scan:\n got %+v\nwant %+v", got, want)
	}
}

// TestScanOwnersBoards reads each of the owner's boards (SPEC.md §11) back from the tree, interface strings included,
// since those decide an nRF52's mode.
func TestScanOwnersBoards(t *testing.T) {
	for _, b := range []usbBoard{heltecV4, rakApp, rakBootloader, xiaoNRF, wioL1App, wioL1Bootloader} {
		t.Run(b.product, func(t *testing.T) {
			f := newFakeSys(t)
			f.plugACM("ttyACM0", b)
			got := f.scan()
			if len(got) != 1 {
				t.Fatalf("Scan found %d ports, want 1: %+v", len(got), got)
			}
			p := got[0]
			if p.VIDPID() != b.vid+":"+b.pid || p.Manufacturer != b.manufacturer || p.Product != b.product ||
				p.Serial != b.serial || p.Interface != b.iface || p.Driver != "cdc_acm" || p.USBPath != b.usbPath {
				t.Errorf("Scan read %+v from %+v", p, b)
			}
		})
	}
}

// TestScanOrderAndFilter: only ttyACM* and ttyUSB* are listed (not tty0 or ttyS0), numbers sort numerically, and
// ttyACM comes before ttyUSB.
func TestScanOrderAndFilter(t *testing.T) {
	f := newFakeSys(t)
	f.plugUSBSerial("ttyUSB0", cp2102, "cp210x")
	b10, b2 := heltecV4, rakApp
	b10.usbPath, b2.usbPath = "1-1.5", "1-1.6"
	f.plugACM("ttyACM10", b10)
	f.plugACM("ttyACM2", b2)

	var names []string
	for _, p := range f.scan() {
		names = append(names, p.Name)
	}
	if want := []string{"ttyACM2", "ttyACM10", "ttyUSB0"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names %v, want %v", names, want)
	}
}

// TestScanSkipsVanishingPorts: a tty whose device link is dangling (unplugged while Scan runs) or whose walk finds no
// USB device is left out rather than listed half-read.
func TestScanSkipsVanishingPorts(t *testing.T) {
	f := newFakeSys(t)
	f.plugACM("ttyACM0", heltecV4)
	// ttyACM1: its tty directory is there but the USB device above it has gone.
	gone := rakApp
	gone.usbPath = "1-1.7"
	f.plugACM("ttyACM1", gone)
	if err := os.RemoveAll(filepath.Join(f.root, gone.deviceDir(), "idVendor")); err != nil {
		t.Fatal(err)
	}
	// ttyACM2: the class entry is left but everything behind it has gone.
	f.link("class/tty/ttyACM2", filepath.Join(hubDir, "1-1.9/1-1.9:1.0/tty/ttyACM2"))

	got := f.scan()
	if len(got) != 1 || got[0].Name != "ttyACM0" {
		t.Errorf("Scan = %+v, want only ttyACM0", got)
	}
	// The walk from ttyACM1 must not climb past its own device into the hub, whose idVendor is 0424.
	for _, p := range got {
		if p.VID == 0x0424 || p.VID == 0x1d6b {
			t.Errorf("Scan read a hub's identity: %+v", p)
		}
	}
}

// TestScanResearchLayout runs Scan on the research's own fake tree layout (scripts/usbids/fakesys), where the class
// entries are plain directories holding a relative device link, so both layouts work.
func TestScanResearchLayout(t *testing.T) {
	root := t.TempDir()
	f := &fakeSys{t: t, root: root}
	f.write("devices/usb1/1-1", map[string]string{"idVendor": "2886", "idProduct": "8044", "bcdDevice": "0100",
		"manufacturer": "Seeed Studio", "product": "XIAO nRF52840", "serial": "8A4C2E1F0B3D5A69"})
	f.write("devices/usb1/1-1/1-1:1.0", map[string]string{"bInterfaceNumber": "00"})
	f.write("devices/usb1/1-2", map[string]string{"idVendor": "1a86", "idProduct": "7523", "bcdDevice": "0264",
		"product": "USB2.0-Serial"})
	f.mkdir("devices/usb1/1-2/1-2:1.0/ttyUSB0")
	f.write("devices/usb1/1-2/1-2:1.0", map[string]string{"bInterfaceNumber": "00"})
	f.mkdir("class/tty/ttyACM0")
	f.mkdir("class/tty/ttyUSB0")
	f.rawLink("class/tty/ttyACM0/device", "../../../devices/usb1/1-1/1-1:1.0")
	f.rawLink("class/tty/ttyUSB0/device", "../../../devices/usb1/1-2/1-2:1.0/ttyUSB0")

	got := f.scan()
	if len(got) != 2 || got[0].VIDPID() != "2886:8044" || got[0].USBPath != "1-1" ||
		got[1].VIDPID() != "1a86:7523" || got[1].USBPath != "1-2" || got[1].Serial != "" {
		t.Errorf("Scan = %+v", got)
	}
}

// TestScanResearchTree runs Scan on the research's own fake tree (scripts/usbids/fakesys in the samples) and checks it
// reads what the research's independent Python lister reads there. `python3 -I list_usb_ttys.py fakesys` prints:
//
//	/dev/ttyACM0: 303a:1001 bcd=0101 mfr='Espressif' product='USB JTAG/serial debug unit' serial='AA:BB:CC:DD:EE:FF' if=00 ifstr=None driver=None
//	/dev/ttyUSB0: 1a86:7523 bcd=0263 mfr=None product='USB2.0-Serial' serial=None if=00 ifstr=None driver=None
//
// Its None is Scan's "". The tree has no driver links, so Driver is "" and CH340 is still a bridge by its VID:PID.
// It runs only when MESHFLASH_SAMPLES points at the research samples.
func TestScanResearchTree(t *testing.T) {
	dir := os.Getenv("MESHFLASH_SAMPLES")
	if dir == "" {
		t.Skip("MESHFLASH_SAMPLES not set")
	}
	got, err := Scan(filepath.Join(dir, "scripts", "usbids", "fakesys"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Port{
		{Name: "ttyACM0", Dev: "/dev/ttyACM0", VID: 0x303a, PID: 0x1001, BCD: 0x0101, Manufacturer: "Espressif",
			Product: "USB JTAG/serial debug unit", Serial: "AA:BB:CC:DD:EE:FF", USBPath: "1-1"},
		{Name: "ttyUSB0", Dev: "/dev/ttyUSB0", VID: 0x1a86, PID: 0x7523, BCD: 0x0263, Product: "USB2.0-Serial",
			USBPath: "1-2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Scan:\n got %+v\nwant %+v", got, want)
	}
	if Classify(got[0]) != EspUSBJTAG || Classify(got[1]) != EspBridge {
		t.Errorf("Classify: %v, %v", Classify(got[0]), Classify(got[1]))
	}
}

// TestScanErrors: Scan fails only when it can't list the ttys at all.
func TestScanErrors(t *testing.T) {
	if _, err := Scan(t.TempDir()); err == nil {
		t.Error("Scan of a tree with no class/tty: no error")
	}
	if _, err := Scan(filepath.Join(t.TempDir(), "nowhere")); err == nil {
		t.Error("Scan of a missing root: no error")
	}
	f := newFakeSys(t)
	if got := f.scan(); len(got) != 0 {
		t.Errorf("Scan with no boards = %+v", got)
	}
}

// TestScanFollowsReenumeration plugs a RAK4631 in, does what the 1200-baud touch does to it (the app goes away, the
// bootloader comes back with another PID, strings and tty name but the same serial), and checks SameBoard follows it.
func TestScanFollowsReenumeration(t *testing.T) {
	f := newFakeSys(t)
	f.plugACM("ttyACM0", rakApp)
	before := f.scan()
	f.unplug("ttyACM0", rakApp)
	// Something still held ttyACM0, so the bootloader gets the next free minor (research §5.2).
	f.plugACM("ttyACM1", rakBootloader)
	after := f.scan()
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("before %+v, after %+v", before, after)
	}
	if Classify(before[0]) != NRFApp || Classify(after[0]) != NRFBootloader {
		t.Errorf("kinds %v → %v, want nrf-app → nrf-bootloader", Classify(before[0]), Classify(after[0]))
	}
	if !SameBoard(before[0], after[0]) {
		t.Errorf("SameBoard(%+v, %+v) = false", before[0], after[0])
	}
}
