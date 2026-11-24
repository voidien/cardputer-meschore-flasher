package usbid

import "testing"

func TestSameBoard(t *testing.T) {
	native := func(vid, pid uint16, product, serial, path string) Port {
		return Port{VID: vid, PID: pid, Product: product, Serial: serial, USBPath: path, Driver: "cdc_acm"}
	}
	cp := func(serial, path string) Port {
		return Port{VID: 0x10c4, PID: 0xea60, Serial: serial, USBPath: path, Driver: "cp210x"}
	}
	ch := func(path string) Port { return Port{VID: 0x1a86, PID: 0x7523, USBPath: path, Driver: "ch341"} }

	tests := []struct {
		name string
		a, b Port
		want bool
	}{
		{"nRF52 app → bootloader: the same device ID",
			native(0x239a, 0x8029, "WisCore RAK4631 Board", "D2A1F3C4B5E69788", "1-1.3"),
			native(0x239a, 0x002a, "WisBlock RAK4631", "D2A1F3C4B5E69788", "1-1.3"), true},
		{"Wio L1 app → bootloader: same VID:PID, same serial",
			native(0x2886, 0x1667, "Seeed Wio Tracker L1", "0F1E2D3C4B5A6978", "1-1.4"),
			native(0x2886, 0x1667, "Wio Tracker L1", "0F1E2D3C4B5A6978", "1-1.4"), true},
		{"two RAKs", native(0x239a, 0x8029, "WisCore RAK4631 Board", "D2A1F3C4B5E69788", "1-1.3"),
			native(0x239a, 0x8029, "WisCore RAK4631 Board", "D2A1F3C4B5E69789", "1-1.3"), false},
		{"S3 TinyUSB app → ROM: the MAC without and with colons",
			native(0x303a, 0x0002, "Heltec Vision Master E213", "6055F9C02718", "1-1.3"),
			native(0x303a, 0x1001, productUSBJTAG, "60:55:F9:C0:27:18", "1-1.3"), true},
		{"case doesn't matter", native(0x303a, 0x0002, "x", "6055f9c02718", "1-1.3"),
			native(0x303a, 0x1001, productUSBJTAG, "60:55:F9:C0:27:18", "1-1.3"), true},
		{"two Heltec v4s, even in the same socket", native(0x303a, 0x1001, productUSBJTAG, "60:55:F9:C0:27:18", "1-1.3"),
			native(0x303a, 0x1001, productUSBJTAG, "60:55:F9:C0:27:19", "1-1.3"), false},
		{"a serial and a board moved to another socket", native(0x239a, 0x8029, "x", "D2A1F3C4B5E69788", "1-1.3"),
			native(0x239a, 0x002a, "y", "D2A1F3C4B5E69788", "1-1.2"), true},
		{"CP2102 with the factory serial, same socket", cp("0001", "1-1.2"), cp("0001", "1-1.2"), true},
		{"CP2102 with the factory serial, other socket", cp("0001", "1-1.2"), cp("0001", "1-1.3"), false},
		{"CP2104 with a real serial, follows the serial", cp("01A2B3C4", "1-1.2"), cp("01A2B3C4", "1-1.3"), true},
		{"CH340 with no serial, same socket", ch("1-1.4"), ch("1-1.4"), true},
		{"CH340 with no serial, other socket", ch("1-1.4"), ch("1-1.2"), false},
		{"a CH340 and a CP2102 in the same socket", ch("1-1.4"), cp("0001", "1-1.4"), false},
		{"no USB path", ch(""), ch(""), false},
		{"native boards with no serial are never assumed the same", native(0x239a, 0x8029, "x", "", "1-1.3"),
			native(0x239a, 0x8029, "x", "", "1-1.3"), false},
		{"all-zero serials tell nothing", native(0x239a, 0x8029, "x", "0000000000000000", "1-1.3"),
			native(0x239a, 0x8029, "x", "0000000000000000", "1-1.3"), false},
		{"one serial missing", native(0x239a, 0x8029, "x", "D2A1F3C4B5E69788", "1-1.3"),
			native(0x239a, 0x002a, "y", "", "1-1.3"), false},
	}
	for _, tt := range tests {
		if got := SameBoard(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: SameBoard = %v, want %v", tt.name, got, tt.want)
		}
		if got := SameBoard(tt.b, tt.a); got != tt.want {
			t.Errorf("%s (swapped): SameBoard = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestSameBoardWioL1OnTheTree follows the owner's Wio Tracker L1 Pro from its app into its bootloader through Scan,
// as the flashing code will: same VID:PID, new product, interface and tty name.
func TestSameBoardWioL1OnTheTree(t *testing.T) {
	f := newFakeSys(t)
	f.plugACM("ttyACM0", wioL1App)
	f.plugACM("ttyACM1", xiaoNRF) // another board plugged in alongside
	before := f.scan()
	f.unplug("ttyACM0", wioL1App)
	f.plugACM("ttyACM0", wioL1Bootloader)
	after := f.scan()

	app := before[0]
	var matches []string
	for _, p := range after {
		if SameBoard(app, p) {
			matches = append(matches, p.Name)
		}
	}
	if len(matches) != 1 || matches[0] != "ttyACM0" {
		t.Fatalf("the Wio L1 after the touch: %v, want [ttyACM0]", matches)
	}
	if Classify(after[0]) != NRFBootloader {
		t.Errorf("Classify = %v", Classify(after[0]))
	}
}

func TestNormalSerial(t *testing.T) {
	for in, want := range map[string]string{
		"60:55:F9:C0:27:18": "6055F9C02718",
		"6055f9c02718":      "6055F9C02718",
		" D2A1F3C4B5E69788": "D2A1F3C4B5E69788",
		"":                  "",
	} {
		if got := NormalSerial(in); got != want {
			t.Errorf("NormalSerial(%q) = %q, want %q", in, got, want)
		}
	}
}
