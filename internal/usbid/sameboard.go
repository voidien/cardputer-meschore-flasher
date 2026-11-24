package usbid

import "strings"

// SameBoard says whether a and b are the same physical board, seen before and after a reset that may have changed
// its VID:PID, strings and tty name: the nRF52 1200-baud touch, or an ESP32-S3 TinyUSB app rebooting into ROM. The
// flashing code follows a board this way and never by tty name alone (SPEC.md §6.1).
//
//   - Two useful serial numbers decide it: equal after NormalSerial means the same board, different means not. An
//     nRF52's app and bootloader both show the chip's 16-hex-digit device ID (research §5.2; source analysis,
//     UNVERIFIED on hardware), and an ESP32-S3 shows its MAC as 12 hex digits under TinyUSB and with colons from the
//     ROM's USB-Serial-JTAG (§6.5; source inference).
//   - Bridges often have no useful serial (a CP210x's factory "0001", a CH340's none). Their identity doesn't change
//     on reset, so two bridges of the same VID:PID in the same USB socket are taken to be the same board.
//   - Anything else is not known to be the same board, so false: better to ask than to flash the wrong one.
func SameBoard(a, b Port) bool {
	sa, sb := NormalSerial(a.Serial), NormalSerial(b.Serial)
	if usefulSerial(sa) && usefulSerial(sb) {
		return sa == sb
	}
	if usefulSerial(sa) || usefulSerial(sb) {
		return false
	}
	return Classify(a) == EspBridge && Classify(b) == EspBridge &&
		a.VID == b.VID && a.PID == b.PID && a.USBPath != "" && a.USBPath == b.USBPath
}

// NormalSerial puts a USB serial number in one form so the same board compares equal across a reset: upper case,
// colons removed. An ESP32-S3's TinyUSB serial "6055F9C02718" and its ROM's "60:55:F9:C0:27:18" both become
// "6055F9C02718".
func NormalSerial(s string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
}

// usefulSerial says whether a normalised serial can tell boards apart. Empty, all zeros and the CP210x's factory
// "0001" (often left unprogrammed, research §6.2) can't.
func usefulSerial(s string) bool {
	if s == "" || s == "0001" {
		return false
	}
	return strings.Trim(s, "0") != ""
}
