package flash

// Phase 2 contract (docs/phase2-design.md): this file is fixed and complete (the integrator's). It holds the rules
// both the backend (builder B) and the screens (builder C) need, written once so the two can't disagree: how a
// port is named and keyed, which boards meshflash can flash at all, which boards a port can be, and when a port is
// positively the nRF52 bootloader.

import (
	"fmt"
	"strings"

	"meshflash/internal/catalog"
	"meshflash/internal/usbid"
)

// PortOf is a sysfs port as the screens and the backend both see it: usbid.Classify's kind, the label the Board
// screen names it by (a bridge's chip, "CP210x", else the USB product string, else ""), usbid.Candidates, and the
// Key boards are remembered by. Backend.Scan builds every Port with it, and the screens' test fake does too.
func PortOf(p usbid.Port) Port {
	fp := Port{Port: p, Kind: usbid.Classify(p), Label: p.Product, Candidates: usbid.Candidates(p)}
	if fp.Kind == usbid.EspBridge {
		fp.Label = usbid.BridgeName(p)
	}
	if s := usbid.NormalSerial(p.Serial); usefulSerial(s) {
		fp.Key = s
	}
	return fp
}

// usefulSerial is usbid's rule (sameboard.go: usefulSerial, unexported there): an empty serial, all zeros, or a
// CP210x's factory "0001" can't tell two boards apart. The backend's tests check the two agree through SameBoard.
func usefulSerial(s string) bool {
	return s != "" && s != "0001" && strings.Trim(s, "0") != ""
}

// DraftKey is what an unsent setup form is kept under in drafts.json: the board's Key, or, for a board with no
// useful serial (a CH340, a CP210x saying "0001"), its USB socket and identity ("usb 1-1.2 10c4:ea60"), so a
// half-typed form survives on the board in that socket (S7; owner question 13 in docs/phase2-design.md). "" when it
// has neither. Boards are remembered on Choose board by Key only, never by this.
func (p Port) DraftKey() string {
	if p.Key != "" {
		return p.Key
	}
	if p.USBPath != "" {
		return "usb " + p.USBPath + " " + p.VIDPID()
	}
	return ""
}

// InBootloader says the port is positively an nRF52's Adafruit bootloader, as DFU requires (SPEC.md 6.3): classified
// as one and showing the bootloader's own interface string, "nRF Serial". A port usbid only infers is a bootloader
// from its VID:PID (no interface string) is not: the Board screen then asks for the double press of reset, and
// Flash never sends it START.
func (p Port) InBootloader() bool {
	return p.Kind == usbid.NRFBootloader && p.Interface == usbid.InterfaceNRFBootloader
}

// esp32C6Boards are the catalog boards with an ESP32-C6, which internal/esp doesn't flash (SPEC.md 6.2: the ESP32,
// ESP32-S3 and ESP32-C3 only). Both are ZephCore's alone; the catalog calls them "esp32" like the rest of the family.
var esp32C6Boards = map[string]bool{
	"LilyGo T-Lora C6":     true,
	"Seeed Studio Xiao C6": true,
}

// CanFlash says whether meshflash can flash a catalog board at all, whatever is plugged in: not a "download only"
// board (RP2040/RP2350, a .uf2 copied by hand) and not an ESP32-C6 (owner question 8: refused before any download).
// why is one sentence for the screens ("" when ok); short is a few words for a list row ("download only").
func CanFlash(d catalog.Device) (ok bool, why, short string) {
	switch {
	case !d.Flashable():
		return false, d.Name + " is download only: it takes a .uf2 file copied onto it, which meshflash doesn't do.",
			"download only"
	case d.Type == catalog.ESP32 && esp32C6Boards[d.Name]:
		return false, d.Name + " has an ESP32-C6, which meshflash can't flash.", "ESP32-C6"
	}
	return true, "", ""
}

// Fits says whether the board on p can be the catalog board d, checked before anything touches it (CLAUDE.md:
// match every file to the chip before touching the board): CanFlash, then the chip family. An nRF52 board needs a
// port that is an nRF52's app or bootloader; an ESP32 board an ESP32's USB-Serial-JTAG, TinyUSB or bridge port, or a
// port usbid doesn't know (the ESP32 ROM's chip read after SYNC still guards that case). An nRF52 board on a port
// usbid doesn't know is refused too: the 1200-baud touch would go to who knows what, and a double press of reset
// makes any Adafruit bootloader show "nRF Serial", which Classify always knows.
//
// Prepare and Flash refuse a Request that doesn't fit (Prepared.Problem; Outcome "Not flashed", nothing sent), and
// Choose board lists the boards that don't fit grey, with short after the name and why when Enter is pressed.
func Fits(d catalog.Device, p Port) (ok bool, why, short string) {
	if ok, why, short := CanFlash(d); !ok {
		return false, why, short
	}
	switch d.Type {
	case catalog.NRF52:
		switch p.Kind {
		case usbid.NRFApp, usbid.NRFBootloader:
			return true, "", ""
		case usbid.Unknown:
			return false, "meshflash can't tell this port is an nRF52's: press the board's reset button twice " +
				"quickly so it shows its bootloader, then pick it again.", "nRF52"
		}
		return false, fmt.Sprintf("%s is an nRF52 board, and this port is an ESP32's (%s).", d.Name, p.kindName()),
			"nRF52"
	case catalog.ESP32:
		switch p.Kind {
		case usbid.EspUSBJTAG, usbid.EspTinyUSB, usbid.EspBridge, usbid.Unknown:
			return true, "", ""
		}
		return false, fmt.Sprintf("%s is an ESP32 board, and this port is an nRF52's.", d.Name), "ESP32"
	}
	return false, d.Name + " isn't a board meshflash knows how to flash.", string(d.Type)
}

// kindName names an ESP32 port's USB for Fits' sentence: the bridge chip, else what the chip shows.
func (p Port) kindName() string {
	switch p.Kind {
	case usbid.EspBridge:
		if p.Label != "" {
			return p.Label
		}
		return "a USB bridge"
	case usbid.EspTinyUSB:
		return "TinyUSB"
	}
	return "USB-Serial-JTAG"
}
