package usbid

// Kind is what sort of USB serial a port is: it says how the board behind it gets into its ROM or bootloader, and
// lets the screens say what is plugged in.
type Kind int

const (
	// Unknown is anything the table and the rules below don't recognise. The owner can still pick a board by hand.
	Unknown Kind = iota
	// EspUSBJTAG is an ESP32-S3 or C3's own USB-Serial-JTAG (303a:1001, product "USB JTAG/serial debug unit"): the
	// same identity whether the app or the ROM is running.
	EspUSBJTAG
	// EspTinyUSB is an ESP32-S3 app on arduino-esp32's TinyUSB serial: Espressif's VID with a board's own product
	// string, at 303a:0002 or borrowing 303a:1001. Resetting it into ROM makes it a new USB device, the
	// USB-Serial-JTAG. 303a:0009 (an S3's ROM over USB-OTG, not expected from MeshCore boards) is here too.
	EspTinyUSB
	// EspBridge is a USB-to-UART bridge chip (CP210x, CH340, CH340K, CH9102) in front of an ESP32's UART0. Every
	// bridge board in the catalog is an ESP32-family board.
	EspBridge
	// NRFApp is an nRF52 running its app, Adafruit's TinyUSB serial. Flashing it starts with the 1200-baud touch.
	NRFApp
	// NRFBootloader is an nRF52 in its Adafruit bootloader, ready for serial DFU.
	NRFBootloader
)

// String names the kind for logs and tests, as the research's suggested algorithm names them (§6.9), except that
// its "esp-hwcdc" is "esp-usb-jtag" here: the name of the hardware, which the ROM shows too, not of arduino-esp32's
// HWCDC class that only the app uses.
func (k Kind) String() string {
	switch k {
	case EspUSBJTAG:
		return "esp-usb-jtag"
	case EspTinyUSB:
		return "esp-tinyusb"
	case EspBridge:
		return "esp-bridge"
	case NRFApp:
		return "nrf-app"
	case NRFBootloader:
		return "nrf-bootloader"
	}
	return "unknown"
}

// Classify says what kind of USB serial p is.
//
// The rules, in order (research §6.2, §6.4):
//   - The interface string "nRF Serial" is the Adafruit bootloader's CDC interface, whatever the VID:PID.
//   - Espressif's VID: the product "USB JTAG/serial debug unit" is the chip's fixed USB-Serial-JTAG; any other product
//     is TinyUSB, even at 303a:1001, which three TinyUSB boards borrow (the research's advice: tell them apart by the
//     product string, not the PID).
//   - A bridge chip's VID:PID, or a port bound to the cp210x or ch341 driver (which only ever drive those chips).
//   - An nRF52 VID:PID from the table. Two boards keep the same VID:PID in the bootloader (239a:00b3 ProMicro and
//     2886:1667 Wio Tracker L1), so the interface string decides first ("TinyUSB Serial" is the app), then the
//     product, then the table. A port that nothing shows to be the bootloader is NRFApp: meshflash sends DFU only to
//     a port positively identified as the bootloader (SPEC.md §6.3), so the doubtful case must not claim to be one.
func Classify(p Port) Kind {
	k, _ := classify(p)
	return k
}

// classify also returns the mode the port's own strings prove, for Candidates: ModeBootloader or ModeApp when an nRF52
// interface string said so, "" otherwise.
func classify(p Port) (Kind, Mode) {
	if p.Interface == InterfaceNRFBootloader {
		return NRFBootloader, ModeBootloader
	}
	if p.VID == vidEspressif {
		if p.PID == pidUSBJTAG && p.Product == productUSBJTAG {
			return EspUSBJTAG, ""
		}
		return EspTinyUSB, ""
	}
	if BridgeName(p) != "" {
		return EspBridge, ""
	}
	var nrf []hit
	for _, h := range match(p) {
		if h.dev.chip == NRF52840 {
			nrf = append(nrf, h)
		}
	}
	if len(nrf) == 0 {
		return Unknown, ""
	}
	if p.Interface == InterfaceNRFApp {
		return NRFApp, ModeApp
	}
	for _, h := range nrf {
		if h.id.mode != ModeBootloader {
			return NRFApp, ""
		}
	}
	return NRFBootloader, ""
}

// BridgeName names the USB-to-UART chip behind p ("CP210x", "CH340", "CH340K", "CH9102"), or "" when p isn't one.
// The screens show it where a board's own name would be, since a bridge's strings say nothing about the board.
func BridgeName(p Port) string {
	if name, ok := bridges[[2]uint16{p.VID, p.PID}]; ok {
		return name
	}
	switch p.Driver {
	case "cp210x":
		return "CP210x"
	case "ch341":
		return "CH34x"
	}
	return ""
}

// Candidate is one catalog board a port could be.
type Candidate struct {
	Device string // the board's name, exactly as the catalog spells it
	Chip   Chip   // ChipUnknown when nobody has checked it
	Mode   Mode   // what the board is running if it is this board
	// Note is something the owner should know about this match, in plain English, or "".
	Note string
	// Unverified marks a match the research marks UNVERIFIED: a guess, or an inference from source code that nobody
	// has confirmed on a real board.
	Unverified bool
}

// Candidates lists the catalog boards p could be, in the catalog's order. Where the table narrows a VID:PID by its
// product string, a port with a known product gets only those boards; with a product the table doesn't know, it gets
// every board with that VID:PID, because a firmware's product then narrows nothing (all but the USB-Serial-JTAG's
// rows: its product is fixed in the chip). So an ambiguous identity gives several boards, an unknown one gives none,
// and the result is never more certain than the table: the owner still picks (SPEC.md §4).
//
// An nRF52 whose interface string proves the app or the bootloader keeps only nRF52 boards and only their identities
// for that mode, and a board listed as "either" (ProMicro) is then given that mode. A board that matches in two modes
// and nothing proves which is given ModeEither. Every candidate's mode agrees with Classify: a port Classify calls
// NRFBootloader never gets a candidate in ModeApp, and the other way round.
func Candidates(p Port) []Candidate {
	if p.VID == vidEspressif && p.PID == pidS3ROMOTG {
		return s3ROMOverOTG()
	}
	_, proved := classify(p)
	hits := match(p)
	if proved == "" {
		return pick(hits, "")
	}
	// The interface string is the Adafruit nRF52 stack's own, so only an nRF52 board can be behind the port.
	hits = nrf52Only(hits)
	if out := pick(hits, proved); len(out) > 0 {
		return out
	}
	// The table has this identity only in the other mode: a bootloader (or app) nobody has listed that shows the
	// other mode's VID:PID and product, as a vendor's factory bootloader might (research §6.3: UNVERIFIED), or a
	// product that contradicts the interface. Trust the interface string for the mode (it is the firmware's own word)
	// and the identity for the board: the boards it points to stay candidates, in the mode the port proved and
	// unverified, never in the table's mode. A port that says "nRF Serial" must not be offered as a board running its
	// app, and dropping the product instead would offer a board whose product the port doesn't show (a Keepteen
	// LT1's identity would become a ProMicro's).
	return otherMode(hits, proved)
}

// nrf52Only keeps the hits on nRF52 boards.
func nrf52Only(hits []hit) []hit {
	var out []hit
	for _, h := range hits {
		if h.dev.chip == NRF52840 {
			out = append(out, h)
		}
	}
	return out
}

// otherMode lists the boards of hits, one each, in mode, as unverified: the port proved a mode the table has no
// identity for at its VID:PID (see Candidates).
func otherMode(hits []hit, mode Mode) []Candidate {
	note := "Unverified: its bootloader is showing its firmware's USB identity, which no known bootloader does."
	if mode == ModeApp {
		note = "Unverified: its firmware is showing its bootloader's USB identity, which no known firmware does."
	}
	var out []Candidate
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.dev.name] {
			continue
		}
		seen[h.dev.name] = true
		out = append(out, Candidate{Device: h.dev.name, Chip: h.dev.chip, Mode: mode, Note: note, Unverified: true})
	}
	return out
}

// hit is one table identity that matched a port.
type hit struct {
	dev *device
	id  *usbID
}

// match finds the table identities for p's VID:PID, narrowed by its product: the rows naming p's product plus those
// that match any product, or, when no row names p's product, every row with p's VID:PID except the USB-Serial-JTAG's.
// A firmware's product string can change from build to build, so an unknown one narrows nothing; but the
// USB-Serial-JTAG's is fixed in the chip, so a port with any other product is certainly not showing it.
func match(p Port) []hit {
	all := matchID(p.VID, p.PID)
	var narrowed, fallback []hit
	named := false
	for _, h := range all {
		switch h.id.product {
		case "":
			narrowed = append(narrowed, h)
		case p.Product:
			narrowed = append(narrowed, h)
			named = true
		}
		if h.id.product != productUSBJTAG {
			fallback = append(fallback, h)
		}
	}
	if named {
		return narrowed
	}
	return fallback
}

// matchID finds every table identity with this VID:PID, in table order.
func matchID(vid, pid uint16) []hit {
	var hits []hit
	for i := range table {
		d := &table[i]
		for j := range d.ids {
			if d.ids[j].vid == vid && d.ids[j].pid == pid {
				hits = append(hits, hit{d, &d.ids[j]})
			}
		}
	}
	return hits
}

// pick turns hits into candidates, one per board, keeping only the identities that fit the mode the port proved.
// A board matched through two identities in different modes (a Wio Tracker L1 whose strings say nothing: its app
// and its bootloader share 2886:1667) gets ModeEither, and counts as verified if either identity is.
func pick(hits []hit, proved Mode) []Candidate {
	var out []Candidate
	at := map[string]int{}
	for _, h := range hits {
		mode := h.id.mode
		if proved != "" {
			switch mode {
			case ModeEither:
				mode = proved
			case proved:
			default:
				continue
			}
		}
		if i, seen := at[h.dev.name]; seen {
			if out[i].Mode != mode {
				out[i].Mode = ModeEither
			}
			out[i].Unverified = out[i].Unverified && h.id.unverified
			continue
		}
		at[h.dev.name] = len(out)
		out = append(out, Candidate{Device: h.dev.name, Chip: h.dev.chip, Mode: mode, Note: h.id.note,
			Unverified: h.id.unverified})
	}
	return out
}

// s3ROMOverOTG is what 303a:0009 could be: an ESP32-S3's ROM on its USB-OTG port (esptool loader.py:1343-1347,
// targets/esp32s3.py:17-19). MeshCore boards switch to the USB-Serial-JTAG before rebooting into ROM, so it isn't
// expected; if it shows up, it is one of the native-USB S3 boards (the ones with a 303a:1001 row), or a Ripple-only
// board whose chip nobody has checked. No board has been seen doing it, so every one is unverified.
func s3ROMOverOTG() []Candidate {
	var out []Candidate
	for _, h := range matchID(vidEspressif, pidUSBJTAG) {
		if h.dev.chip != ESP32S3 && h.dev.chip != ChipUnknown {
			continue
		}
		if len(out) > 0 && out[len(out)-1].Device == h.dev.name {
			continue
		}
		out = append(out, Candidate{Device: h.dev.name, Chip: h.dev.chip, Mode: ModeROM, Unverified: true,
			Note: "Unverified: an ESP32-S3 in ROM download mode on its USB-OTG port, which MeshCore boards don't use."})
	}
	return out
}
