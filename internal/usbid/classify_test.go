package usbid

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata from the code (and from the samples with MESHFLASH_SAMPLES)")

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		p    Port
		want Kind
	}{
		{"Heltec v4: the S3's USB-Serial-JTAG", Port{VID: 0x303a, PID: 0x1001, Product: productUSBJTAG}, EspUSBJTAG},
		{"Station G2: TinyUSB borrowing 303a:1001", Port{VID: 0x303a, PID: 0x1001, Product: "BQ Station G2",
			Interface: "TinyUSB CDC"}, EspTinyUSB},
		{"E213: TinyUSB at 303a:0002", Port{VID: 0x303a, PID: 0x0002, Product: "Heltec Vision Master E213"},
			EspTinyUSB},
		{"S3 ROM over USB-OTG", Port{VID: 0x303a, PID: 0x0009}, EspTinyUSB},
		{"CP2102", Port{VID: 0x10c4, PID: 0xea60, Driver: "cp210x"}, EspBridge},
		{"CH9102 binds cdc_acm", Port{VID: 0x1a86, PID: 0x55d4, Driver: "cdc_acm"}, EspBridge},
		{"CH340K", Port{VID: 0x1a86, PID: 0x7522, Driver: "ch341"}, EspBridge},
		{"a CP2102C, not in the table, by its driver", Port{VID: 0x10c4, PID: 0xea64, Driver: "cp210x"}, EspBridge},
		{"an FTDI cable", Port{VID: 0x0403, PID: 0x6001, Driver: "ftdi_sio"}, Unknown},
		{"RAK4631 app", Port{VID: 0x239a, PID: 0x8029, Product: "WisCore RAK4631 Board",
			Interface: "TinyUSB Serial"}, NRFApp},
		{"RAK4631 app, interface string unreadable", Port{VID: 0x239a, PID: 0x8029,
			Product: "WisCore RAK4631 Board"}, NRFApp},
		{"RAK4631 bootloader after the touch", Port{VID: 0x239a, PID: 0x002a, Product: "WisBlock RAK4631",
			Interface: "nRF Serial"}, NRFBootloader},
		{"RAK4631 bootloader, by its VID:PID alone", Port{VID: 0x239a, PID: 0x002a}, NRFBootloader},
		{"ProMicro bootloader", Port{VID: 0x239a, PID: 0x00b3, Product: "ProMicro NRF52840",
			Interface: "nRF Serial"}, NRFBootloader},
		{"ProMicro app", Port{VID: 0x239a, PID: 0x00b3, Product: "ProMicro NRF52840",
			Interface: "TinyUSB Serial"}, NRFApp},
		// The same VID:PID and product in both modes: nothing proves the bootloader, so not NRFBootloader.
		{"ProMicro, no interface string", Port{VID: 0x239a, PID: 0x00b3, Product: "ProMicro NRF52840"}, NRFApp},
		{"Wio L1 app", Port{VID: 0x2886, PID: 0x1667, Product: "Seeed Wio Tracker L1",
			Interface: "TinyUSB Serial"}, NRFApp},
		{"Wio L1 bootloader", Port{VID: 0x2886, PID: 0x1667, Product: "Wio Tracker L1",
			Interface: "nRF Serial"}, NRFBootloader},
		{"Wio L1 bootloader, by its product alone", Port{VID: 0x2886, PID: 0x1667, Product: "Wio Tracker L1"},
			NRFBootloader},
		{"Wio L1, interface beats product", Port{VID: 0x2886, PID: 0x1667, Product: "Wio Tracker L1",
			Interface: "TinyUSB Serial"}, NRFApp},
		{"a factory bootloader the table doesn't know", Port{VID: 0x239a, PID: 0x1234,
			Interface: "nRF Serial"}, NRFBootloader},
		{"Xiao nRF52 app", Port{VID: 0x2886, PID: 0x8044, Product: "XIAO nRF52840"}, NRFApp},
		{"Pico W", Port{VID: 0x2e8a, PID: 0xf00a, Product: "Pico W"}, Unknown},
	}
	for _, tt := range tests {
		if got := Classify(tt.p); got != tt.want {
			t.Errorf("%s: Classify = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func names(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Device)
	}
	return out
}

// find returns the candidate for device, failing the test when it's missing.
func find(t *testing.T, cs []Candidate, device string) Candidate {
	t.Helper()
	for _, c := range cs {
		if c.Device == device {
			return c
		}
	}
	t.Fatalf("%q is not among the candidates %q", device, names(cs))
	return Candidate{}
}

// TestCandidatesOwnersBoards checks the suggestions for each of the owner's boards (SPEC.md §11), read from the fake
// tree as the Board screen would read them.
func TestCandidatesOwnersBoards(t *testing.T) {
	scanOne := func(b usbBoard) Port {
		t.Helper()
		f := newFakeSys(t)
		f.plugACM("ttyACM0", b)
		return f.scan()[0]
	}

	t.Run("Heltec v4", func(t *testing.T) {
		p := scanOne(heltecV4)
		got := Candidates(p)
		// Every board whose USB-Serial-JTAG can show, app or ROM, in catalog order: USB alone can't do better.
		want := []string{
			"Heltec v4 + Expansion Kit (Touch)", "Heltec v4 R8 + Expansion Kit (Touch)", "LilyGo T-Deck",
			"LilyGo T-Deck Max", "LilyGo T-Deck Pro", "LilyGo T-Deck Pro v1.1", "LilyGo T5 E-Paper S3 Pro (H752-XX)",
			"LilyGo T-Lora Pager", "LilyGo T-Display Pro", "LilyGo T-Watch S3 Plus", "LilyGo T-Watch Ultra",
			"Seeed Studio Wio Tracker L2 Pro", "LilyGo T3 S3 (SX126x)", "LilyGo T3 S3 (SX127x)", "LilyGo T-Beam 1W",
			"LilyGo T-Beam Supreme (SX1262)", "Heltec Vision Master E213", "Heltec Vision Master E290", "Heltec v4",
			"Heltec v4 R8", "Heltec Wireless Tracker v2", "Heltec Wireless Tracker", "RAK WisBlock 3112",
			"Seeed Studio Xiao C3", "Seeed Studio Xiao S3 WIO", "UnitEng/BQ Voyage Station G3", "UnitEng Station G2",
		}
		if !reflect.DeepEqual(names(got), want) {
			t.Errorf("Candidates:\n got %q\nwant %q", names(got), want)
		}
		if c := find(t, got, "Heltec v4"); c.Chip != ESP32S3 || c.Mode != ModeEither || c.Unverified {
			t.Errorf("Heltec v4: %+v", c)
		}
		if c := find(t, got, "Seeed Studio Xiao C3"); c.Chip != ESP32C3 {
			t.Errorf("Xiao C3: %+v", c)
		}
		// A TinyUSB board shows the USB-Serial-JTAG only from its ROM.
		if c := find(t, got, "Heltec Vision Master E213"); c.Mode != ModeROM || c.Note == "" {
			t.Errorf("E213: %+v", c)
		}
		if c := find(t, got, "LilyGo T-Deck Max"); !c.Unverified || c.Chip != ChipUnknown {
			t.Errorf("T-Deck Max: %+v", c)
		}
		// Their USB-Serial-JTAG is inferred from a generic board file plus an env flag (research §6.3).
		for _, name := range []string{"Heltec Wireless Tracker", "RAK WisBlock 3112"} {
			if c := find(t, got, name); !c.Unverified || c.Mode != ModeEither || c.Note == "" {
				t.Errorf("%s: %+v", name, c)
			}
		}
	})

	t.Run("RAK4631", func(t *testing.T) {
		got := Candidates(scanOne(rakApp))
		want := []string{"GAT-IoT GAT562 Tracker", "GAT-IoT GAT562 30s", "RAK WisBlock / WisMesh (RAK 4631)",
			"RAK WisMesh Tag", "Muzi Works R1 Neo"}
		if !reflect.DeepEqual(names(got), want) {
			t.Errorf("Candidates:\n got %q\nwant %q", names(got), want)
		}
		for _, c := range got {
			if c.Chip != NRF52840 || c.Mode != ModeApp || c.Unverified {
				t.Errorf("%+v", c)
			}
		}
	})

	t.Run("RAK4631 in its bootloader", func(t *testing.T) {
		got := Candidates(scanOne(rakBootloader))
		want := []string{"GAT-IoT GAT562 Tracker", "GAT-IoT GAT562 30s", "Keepteen LT1",
			"RAK WisBlock / WisMesh (RAK 4631)", "Muzi Works R1 Neo"}
		if !reflect.DeepEqual(names(got), want) {
			t.Errorf("Candidates:\n got %q\nwant %q", names(got), want)
		}
		if c := find(t, got, "RAK WisBlock / WisMesh (RAK 4631)"); c.Mode != ModeBootloader || c.Unverified {
			t.Errorf("RAK4631: %+v", c)
		}
		if c := find(t, got, "Muzi Works R1 Neo"); !c.Unverified {
			t.Errorf("R1 Neo's bootloader is a guess: %+v", c)
		}
	})

	t.Run("Xiao nRF52", func(t *testing.T) {
		got := Candidates(scanOne(xiaoNRF))
		want := []string{"Ikoka Stick", "Ikoka Nano", "Seeed Studio Xiao nRF52 WIO"}
		if !reflect.DeepEqual(names(got), want) {
			t.Errorf("Candidates:\n got %q\nwant %q", names(got), want)
		}
	})

	t.Run("Wio Tracker L1 Pro, app and bootloader", func(t *testing.T) {
		want := []string{"Seeed Studio Wio Tracker L1 Pro", "Seeed Studio Wio Tracker L1 EINK"}
		for _, tc := range []struct {
			b    usbBoard
			mode Mode
		}{{wioL1App, ModeApp}, {wioL1Bootloader, ModeBootloader}} {
			got := Candidates(scanOne(tc.b))
			if !reflect.DeepEqual(names(got), want) {
				t.Errorf("%s: Candidates %q, want %q", tc.mode, names(got), want)
			}
			for _, c := range got {
				if c.Mode != tc.mode {
					t.Errorf("%s: %+v", tc.mode, c)
				}
			}
		}
	})
}

func TestCandidatesNarrowing(t *testing.T) {
	tests := []struct {
		name string
		p    Port
		want []string
		mode Mode
	}{
		{"the product names one board", Port{VID: 0x239a, PID: 0x8029, Product: "HT-n5262T1"},
			[]string{"Heltec Mesh Node T1"}, ModeApp},
		{"TinyUSB: the product decides", Port{VID: 0x303a, PID: 0x1001, Product: "BQ Station G2"},
			[]string{"UnitEng Station G2"}, ModeApp},
		{"an unknown product narrows nothing", Port{VID: 0x303a, PID: 0x0002, Product: "something else"},
			[]string{"Heltec Vision Master E213", "Heltec Vision Master E290", "Heltec Wireless Tracker v2"}, ModeApp},
		{"CP210x: every board with one", Port{VID: 0x10c4, PID: 0xea60, Product: "CP2102 USB to UART Bridge Controller"},
			[]string{"LilyGo T-Beam (SX1262)", "LilyGo T-Beam 1.2 (SX1276)", "Heltec v2", "Heltec v3",
				"Heltec Heltec Wireless Paper", "Heltec WSL3"}, ModeEither},
		{"CH9102", Port{VID: 0x1a86, PID: 0x55d4},
			[]string{"LilyGo T-Beam (SX1262)", "LilyGo T-Beam 1.2 (SX1276)", "LilyGo LoRa32 V2.1_1.6"}, ModeEither},
		{"ProMicro bootloader: the interface decides", Port{VID: 0x239a, PID: 0x00b3, Product: "ProMicro NRF52840",
			Interface: "nRF Serial"}, []string{"ProMicro nrf52 (faketec)"}, ModeBootloader},
		{"ProMicro app", Port{VID: 0x239a, PID: 0x00b3, Product: "ProMicro NRF52840", Interface: "TinyUSB Serial"},
			[]string{"ProMicro nrf52 (faketec)"}, ModeApp},
		{"ProMicro, nothing proves the mode", Port{VID: 0x239a, PID: 0x00b3, Product: "ProMicro NRF52840"},
			[]string{"ProMicro nrf52 (faketec)"}, ModeEither},
		{"Keepteen app shares the ProMicro's PID", Port{VID: 0x239a, PID: 0x00b3, Product: "Keepteen LT1",
			Interface: "TinyUSB Serial"}, []string{"Keepteen LT1"}, ModeApp},
		{"T1000-E bootloader", Port{VID: 0x2886, PID: 0x0057, Product: "T1000-E", Interface: "nRF Serial"},
			[]string{"Seeed Studio SenseCAP T1000-E", "Seeed Studio SenseCAP MeshTracker X1"}, ModeBootloader},
		{"Xiao Sense bootloader", Port{VID: 0x2886, PID: 0x0045, Product: "XIAO nRF52840 Sense"},
			[]string{"Seeed Studio Xiao nRF52 WIO"}, ModeBootloader},
		// A product other than the USB-Serial-JTAG's rules out every board showing that: only TinyUSB boards remain.
		{"TinyUSB with a product the table doesn't know", Port{VID: 0x303a, PID: 0x1001, Product: "LilyGo TBeam 1W v2"},
			[]string{"LilyGo T-Beam 1W", "UnitEng/BQ Voyage Station G3", "UnitEng Station G2"}, ModeApp},
		// Nothing says app or bootloader: both of the board's identities match, so the mode stays open.
		{"Wio L1 with strings that say nothing", Port{VID: 0x2886, PID: 0x1667, Product: "?"},
			[]string{"Seeed Studio Wio Tracker L1 Pro", "Seeed Studio Wio Tracker L1 EINK"}, ModeEither},
		{"unknown identity", Port{VID: 0x0403, PID: 0x6001}, nil, ""},
		{"bootloader the table doesn't know", Port{VID: 0x239a, PID: 0x1234, Interface: "nRF Serial"}, nil, ""},
		// The MeshTower V2's catalog KISS modem firmware is the MeshSolar's, so it shows the MeshSolar's identity.
		{"HT-n5262 on 239a:8029", Port{VID: 0x239a, PID: 0x8029, Product: "HT-n5262", Interface: "TinyUSB Serial"},
			[]string{"Heltec T114", "Heltec MeshSolar / MeshTower", "Heltec MeshTower V2"}, ModeApp},
	}
	for _, tt := range tests {
		got := Candidates(tt.p)
		if !reflect.DeepEqual(names(got), tt.want) {
			t.Errorf("%s: Candidates\n got %q\nwant %q", tt.name, names(got), tt.want)
		}
		for _, c := range got {
			if c.Mode != tt.mode {
				t.Errorf("%s: %s has mode %s, want %s", tt.name, c.Device, c.Mode, tt.mode)
			}
		}
	}
}

// TestCandidatesOtherModeIdentity: a port whose nRF52 interface string proves a mode the table has no identity for
// at that VID:PID (a vendor's bootloader keeping the app's PID, say) still lists the boards the identity points to,
// but in the mode the port proved and unverified, never in the table's mode. Before the fix, a port saying
// "nRF Serial" got every board in ModeApp while Classify called it NRFBootloader.
func TestCandidatesOtherModeIdentity(t *testing.T) {
	tests := []struct {
		name string
		p    Port
		want []string
		mode Mode
	}{
		{"RAK4631 app identity, bootloader interface", Port{VID: 0x239a, PID: 0x8029,
			Product: "WisCore RAK4631 Board", Interface: "nRF Serial"},
			[]string{"GAT-IoT GAT562 Tracker", "GAT-IoT GAT562 30s", "RAK WisBlock / WisMesh (RAK 4631)",
				"RAK WisMesh Tag", "Muzi Works R1 Neo"}, ModeBootloader},
		{"Xiao bootloader identity, app interface", Port{VID: 0x2886, PID: 0x0044, Product: "XIAO nRF52840",
			Interface: "TinyUSB Serial"}, []string{"Ikoka Stick", "Ikoka Nano", "Seeed Studio Xiao nRF52 WIO"}, ModeApp},
		// The ProMicro shares 239a:00b3 in its bootloader, but this port's product is the Keepteen's, so it is a
		// Keepteen in a bootloader the table doesn't know, not a ProMicro.
		{"Keepteen app identity, bootloader interface", Port{VID: 0x239a, PID: 0x00b3, Product: "Keepteen LT1",
			Interface: "nRF Serial"}, []string{"Keepteen LT1"}, ModeBootloader},
		// The product is the Wio L1 bootloader's, the interface its app's: the interface decides the mode, and the
		// contradiction makes it unverified.
		{"Wio L1, interface contradicting the product", Port{VID: 0x2886, PID: 0x1667, Product: "Wio Tracker L1",
			Interface: "TinyUSB Serial"}, []string{"Seeed Studio Wio Tracker L1 Pro", "Seeed Studio Wio Tracker L1 EINK"},
			ModeApp},
		// An nRF52 interface string on an ESP32's identity: no ESP32 board is offered in a bootloader mode it hasn't.
		{"USB-Serial-JTAG with the nRF bootloader's interface", Port{VID: 0x303a, PID: 0x1001,
			Product: productUSBJTAG, Interface: "nRF Serial"}, nil, ""},
	}
	for _, tt := range tests {
		got := Candidates(tt.p)
		if !reflect.DeepEqual(names(got), tt.want) {
			t.Errorf("%s: Candidates\n got %q\nwant %q", tt.name, names(got), tt.want)
		}
		for _, c := range got {
			if c.Mode != tt.mode || !c.Unverified || !strings.HasPrefix(c.Note, "Unverified") || c.Chip != NRF52840 {
				t.Errorf("%s: %+v", tt.name, c)
			}
		}
	}
}

// TestCandidatesAgreeWithClassify plugs in every identity in the table, with the product it names and one it
// doesn't, under each interface string an nRF52 can show (and none), and checks the candidates never contradict
// Classify: an nRF52 kind gets only nRF52 boards, NRFBootloader only in ModeBootloader, NRFApp never in
// ModeBootloader, and an ESP32 kind never gets an nRF52 or a bootloader mode. The flashing code trusts Classify
// and the screens show the candidates, so the two must tell the owner the same thing.
func TestCandidatesAgreeWithClassify(t *testing.T) {
	var ports []Port
	for _, d := range table {
		for _, id := range d.ids {
			for _, product := range []string{id.product, "?"} {
				for _, iface := range []string{"", InterfaceNRFApp, InterfaceNRFBootloader} {
					ports = append(ports, Port{VID: id.vid, PID: id.pid, Product: product, Interface: iface})
				}
			}
		}
	}
	ports = append(ports, Port{VID: vidEspressif, PID: pidS3ROMOTG})
	for _, p := range ports {
		k := Classify(p)
		for _, c := range Candidates(p) {
			bad := false
			switch k {
			case NRFBootloader:
				bad = c.Chip != NRF52840 || c.Mode != ModeBootloader
			case NRFApp:
				bad = c.Chip != NRF52840 || c.Mode == ModeBootloader
			case EspUSBJTAG, EspTinyUSB, EspBridge:
				bad = c.Chip == NRF52840 || c.Chip == RP2040 || c.Mode == ModeBootloader
			case Unknown:
				bad = c.Chip == NRF52840
			}
			if bad {
				t.Errorf("%s product=%q interface=%q is %v, but a candidate is %+v", p.VIDPID(), p.Product,
					p.Interface, k, c)
			}
		}
	}
}

// TestCandidatesS3ROMOverOTG: 303a:0009 isn't expected, but if it shows it can only be a native-USB S3 board.
func TestCandidatesS3ROMOverOTG(t *testing.T) {
	got := Candidates(Port{VID: 0x303a, PID: 0x0009})
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	for _, c := range got {
		if c.Mode != ModeROM || (c.Chip != ESP32S3 && c.Chip != ChipUnknown) {
			t.Errorf("%+v", c)
		}
		if c.Device == "Seeed Studio Xiao C3" || c.Device == "Heltec v3" {
			t.Errorf("%s can't show 303a:0009", c.Device)
		}
	}
	find(t, got, "Heltec v4")
	find(t, got, "Heltec Vision Master E213")
}

// TestEveryIdentityFindsItsBoard plugs in, in turn, every identity the table lists and checks its board is among the
// candidates, once, with the identity's mode, and that the candidates are in catalog order: no row is unreachable.
func TestEveryIdentityFindsItsBoard(t *testing.T) {
	order := catalogIndex(t)
	for _, d := range table {
		for _, id := range d.ids {
			p := portFor(id)
			got := Candidates(p)
			c := find(t, got, d.name)
			if c.Mode != id.mode && !(id.mode == ModeEither && c.Mode == provedMode(p)) {
				t.Errorf("%s via %s: mode %s, want %s", d.name, p.VIDPID(), c.Mode, id.mode)
			}
			if c.Chip != d.chip || c.Unverified != id.unverified || c.Note != id.note {
				t.Errorf("%s via %s: %+v", d.name, p.VIDPID(), c)
			}
			last := -1
			seen := map[string]bool{}
			for _, c := range got {
				if seen[c.Device] {
					t.Errorf("%s via %s: %s listed twice", d.name, p.VIDPID(), c.Device)
				}
				seen[c.Device] = true
				if order[c.Device] <= last {
					t.Errorf("%s via %s: candidates out of catalog order: %q", d.name, p.VIDPID(), names(got))
				}
				last = order[c.Device]
			}
			if k := Classify(p); d.chip == NRF52840 && k != NRFApp && k != NRFBootloader {
				t.Errorf("%s via %s: Classify = %v", d.name, p.VIDPID(), k)
			}
		}
	}
}

// portFor builds the port an identity describes, with the interface string its firmware would show.
func portFor(id usbID) Port {
	p := Port{VID: id.vid, PID: id.pid, Product: id.product}
	if p.Product == "" {
		p.Product = "a product the table doesn't name"
	}
	switch {
	case id.mode == ModeBootloader:
		p.Interface = InterfaceNRFBootloader
	case id.vid == vidAdafruit || id.vid == vidSeeed:
		p.Interface = InterfaceNRFApp
	}
	return p
}

func provedMode(p Port) Mode {
	_, m := classify(p)
	return m
}

// TestLookupGolden writes every lookup the table can answer into testdata/lookup.golden: for each identity in the
// table, and for each VID:PID with a product the table doesn't know, the kind and the candidates. It's the table
// turned round (research §6.4's reverse lookup), for review, and it catches any change to what a port suggests.
// Rewrite it with go test ./internal/usbid -run TestLookupGolden -args -update, and read the diff.
func TestLookupGolden(t *testing.T) {
	var ports []Port
	seen := map[string]bool{}
	add := func(p Port) {
		key := fmt.Sprintf("%04x:%04x|%s|%s", p.VID, p.PID, p.Product, p.Interface)
		if !seen[key] {
			seen[key] = true
			ports = append(ports, p)
		}
	}
	for _, d := range table {
		for _, id := range d.ids {
			add(portFor(id))
			add(Port{VID: id.vid, PID: id.pid, Product: "?"})
		}
	}
	add(Port{VID: vidEspressif, PID: pidS3ROMOTG})
	sort.SliceStable(ports, func(i, j int) bool {
		a, b := ports[i], ports[j]
		if a.VID != b.VID {
			return a.VID < b.VID
		}
		if a.PID != b.PID {
			return a.PID < b.PID
		}
		if a.Product != b.Product {
			return a.Product < b.Product
		}
		return a.Interface < b.Interface
	})

	var buf bytes.Buffer
	fmt.Fprintln(&buf, "# Every lookup the USB ID table answers. Written by TestLookupGolden; rewrite with -update.")
	fmt.Fprintln(&buf, "# A port's VID:PID, product and interface string → Classify, then Candidates:")
	fmt.Fprintln(&buf, "# board | chip | mode | unverified | note. Product \"?\" is one the table doesn't name.")
	for _, p := range ports {
		fmt.Fprintf(&buf, "\n%s product=%q interface=%q → %v\n", p.VIDPID(), p.Product, p.Interface, Classify(p))
		for _, c := range Candidates(p) {
			chip := string(c.Chip)
			if chip == "" {
				chip = "?"
			}
			line := fmt.Sprintf("    %s | %s | %s", c.Device, chip, c.Mode)
			if c.Unverified {
				line += " | unverified"
			}
			if c.Note != "" {
				line += " | " + c.Note
			}
			fmt.Fprintln(&buf, line)
		}
	}
	golden(t, "lookup.golden", buf.Bytes())
}

// golden compares got with testdata/name, or rewrites the file with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with -update)", err)
	}
	if !bytes.Equal(got, want) {
		gl, wl := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Fatalf("%s differs at line %d:\n got %q\nwant %q\n(rewrite with -update if the change is right)",
					path, i+1, g, w)
			}
		}
	}
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{Unknown: "unknown", EspUSBJTAG: "esp-usb-jtag", EspTinyUSB: "esp-tinyusb",
		EspBridge: "esp-bridge", NRFApp: "nrf-app", NRFBootloader: "nrf-bootloader", Kind(99): "unknown"} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

func TestBridgeName(t *testing.T) {
	for _, tt := range []struct {
		p    Port
		want string
	}{
		{Port{VID: 0x10c4, PID: 0xea60}, "CP210x"},
		{Port{VID: 0x1a86, PID: 0x7523, Driver: "ch341"}, "CH340"},
		{Port{VID: 0x1a86, PID: 0x55d4, Driver: "cdc_acm"}, "CH9102"},
		{Port{VID: 0x1a86, PID: 0x5523, Driver: "ch341"}, "CH34x"},
		{Port{VID: 0x303a, PID: 0x1001, Driver: "cdc_acm"}, ""},
	} {
		if got := BridgeName(tt.p); got != tt.want {
			t.Errorf("BridgeName(%s, %s) = %q, want %q", tt.p.VIDPID(), tt.p.Driver, got, tt.want)
		}
	}
}
