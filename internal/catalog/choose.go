package catalog

// Ported in part from the MeshCore web flasher, flasher.meshcore.dev @ 45a5257, js/flash.js (pickFlashFile,
// esp32Address, ESP32_APP_ADDRESS), changed as SPEC.md 6.2 asks: the file is chosen by kind with no fallback, and the
// erase is tied to the merged image.
// MIT License, Copyright (c) 2025 Rastislav Vysoky (see THIRD_PARTY_NOTICES).

import (
	"errors"
	"fmt"
	"strings"
)

// Kind is what the owner picked on the Confirm screen. There is no default: the zero Kind is refused, so nothing is
// written until the owner has chosen (SPEC.md 11, "no preselection").
type Kind int

// The kinds of flash.
const (
	// Update writes the app only: on an ESP32 the flash-update .bin at 0x10000 with no erase, which keeps the
	// identity, contacts and Bluetooth pairing; on an nRF52 the DFU .zip.
	Update Kind = iota + 1
	// Wipe erases the whole ESP32 chip and writes the flash-wipe merged image at 0x0. nRF52 boards have none.
	Wipe
)

// String names the kind as the Confirm screen says it.
func (k Kind) String() string {
	switch k {
	case Update:
		return "update"
	case Wipe:
		return "full wipe"
	}
	return "no choice"
}

// ESP32AppAddress is where an ESP32 app image goes (js/flash.js:7); a merged image goes at 0x0.
const ESP32AppAddress = 0x10000

// Flash is what to write: one file, where, and whether the whole chip is erased first.
type Flash struct {
	Kind    Kind   // what the owner chose
	File    File   // the one file to write, from the version
	Address uint32 // ESP32 only: 0x10000 for an update, 0x0 for a wipe; 0 for nRF52 (DFU has no address)
	// EraseChip is true only for an ESP32 wipe, and only together with the merged image at 0x0.
	EraseChip bool
}

// ErrRefused is wrapped by every error Choose returns: the catalog doesn't offer a safe file for that choice.
var ErrRefused = errors.New("refused")

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRefused}, a...)...)
}

// Choose picks the file to flash, as js/flash.js pickFlashFile and esp32Address do, with the fixes of SPEC.md 6.2:
//
//   - ESP32 Update: the version's flash-update file, a .bin that isn't a merged image, at 0x10000, no erase.
//   - ESP32 Wipe: its flash-wipe file, a -merged.bin, at 0x0, with the chip erased first. The erase belongs to the
//     merged image and is decided afresh for every flash (the reference's "Erase device" box stays ticked across
//     boards and can erase the chip under an app-only image).
//   - nRF52 Update: its "flash" file, a DFU .zip. There is no nRF52 Wipe.
//
// Anything else is refused with a reason the owner can read: a noflash board, a missing file (the reference would
// fall back to the first flash* file, which is how the git catalog's broken "LilyGo T-Lora Pager" entry would put an
// nRF52 .zip on an ESP32), a .zip for an ESP32 or a .bin for an nRF52, a "download" file, any erase or bootloader
// file the catalogs name (for this board or another), or a version that isn't the board's.
func Choose(d Device, v Version, k Kind) (Flash, error) {
	if !d.Flashable() {
		if d.Type == NoFlash {
			return Flash{}, refuse("%s is download only: its firmware is a .uf2 copied onto the board's USB drive, which meshflash doesn't do", d.Name)
		}
		return Flash{}, refuse("meshflash can't flash a %q board (%s)", d.Type, d.Name)
	}
	if k != Update && k != Wipe {
		return Flash{}, refuse("choose update or full wipe first")
	}
	if !d.hasVersion(v) {
		return Flash{}, refuse("version %s is not one of %s's", v.Name, d.Name)
	}

	switch d.Type {
	case ESP32:
		want, what := "flash-update", "update image (.bin at 0x10000)"
		if k == Wipe {
			want, what = "flash-wipe", "full-wipe image (-merged.bin at 0x0)"
		}
		f, ok := firstOfType(v, want)
		if !ok {
			return Flash{}, refuse("%s %s has no %s for %s", v.Name, k, what, d.Name)
		}
		if err := d.checkWritable(f); err != nil {
			return Flash{}, err
		}
		base := f.Base()
		lower := strings.ToLower(base)
		if !strings.HasSuffix(lower, ".bin") {
			return Flash{}, refuse("%s isn't an ESP32 image (.bin)", base)
		}
		merged := strings.HasSuffix(lower, "-merged.bin")
		if k == Update {
			if merged {
				return Flash{}, refuse("%s is a merged image, which belongs at 0x0 with a full wipe, not at 0x10000", base)
			}
			return Flash{Kind: Update, File: f, Address: ESP32AppAddress}, nil
		}
		if !merged {
			return Flash{}, refuse("%s isn't a merged image (-merged.bin), so it can't go at 0x0 after a chip erase", base)
		}
		return Flash{Kind: Wipe, File: f, Address: 0, EraseChip: true}, nil

	case NRF52:
		if k == Wipe {
			return Flash{}, refuse("nRF52 boards have no full wipe in meshflash: the catalog's only way is its erase formatter, which meshflash never writes; choose update")
		}
		f, ok := firstOfType(v, "flash")
		if !ok {
			return Flash{}, refuse("%s has no DFU package (.zip) for %s", v.Name, d.Name)
		}
		if err := d.checkWritable(f); err != nil {
			return Flash{}, err
		}
		if base := f.Base(); !strings.HasSuffix(strings.ToLower(base), ".zip") {
			return Flash{}, refuse("%s isn't an nRF52 DFU package (.zip)", base)
		}
		return Flash{Kind: Update, File: f}, nil
	}
	return Flash{}, refuse("meshflash can't flash a %q board (%s)", d.Type, d.Name)
}

// firstOfType is the version's first file of exactly this type, as the reference's files.find does.
func firstOfType(v Version, t string) (File, bool) {
	for _, f := range v.Files {
		if f.Type == t {
			return f, true
		}
	}
	return File{}, false
}

// checkWritable refuses every erase and bootloader file the catalogs name, for any board, should a version ever
// list one.
func (d *Device) checkWritable(f File) error {
	if d.neverWrite[f.Base()] || d.neverWrite[f.Name] {
		return refuse("%s is one of the catalog's erase or bootloader files, not firmware; meshflash never writes those",
			f.Base())
	}
	return nil
}

// hasVersion says whether v is one of the board's versions: the same name and the same files.
func (d *Device) hasVersion(v Version) bool {
	for _, fw := range d.Firmware {
		for _, dv := range fw.Versions {
			if dv.Name != v.Name || len(dv.Files) != len(v.Files) {
				continue
			}
			same := true
			for i := range dv.Files {
				if dv.Files[i] != v.Files[i] {
					same = false
					break
				}
			}
			if same {
				return true
			}
		}
	}
	return false
}
