// Package store keeps what meshflash remembers between runs, in ~/.config/meshflash (SPEC.md 8):
//
//	config.json   the radio settings last sent to a node, and the catalog board picked for each USB serial number
//	drafts.json   an unsent setup form per board (flash.Port.DraftKey), without passwords
//
// Every file is the owner's alone (0600, in 0700 folders) and is replaced whole: written to a temporary file in the
// same folder, flushed to the SD card, renamed over the old one, and the folder flushed, so a battery that dies
// mid-write leaves the old file or the new one, never half of either. A damaged file reads as empty, so meshflash
// always starts.
//
// It is the CardputerZero app kit's internal/store (code/internal/store/store.go, the owner's own code) in shape:
// Loom's stories, recaps and offline cache are gone. The catalog and the downloaded firmware are internal/catalog's
// (~/.cache/meshflash/catalog and files), and screen.log is internal/app's; this package only names the cache folder.
//
// It never holds a password: no type here has a field for one (S7, S22).
//
// This file is part of the Phase 2 contract (docs/phase2-design.md): its types are fixed. store.go holds the
// functions, whose signatures are fixed too.
package store

import (
	"errors"
	"time"
)

// The files, in the config folder.
const (
	ConfigFile = "config.json"
	DraftsFile = "drafts.json"
)

// ErrNoKey is what the draft and board functions return for an empty key: a board without a useful USB serial (a
// CH340 has none, many CP210x say "0001") is never remembered on Choose board, since it can't be told from another.
var ErrNoKey = errors.New("store: the board has no serial number to remember it by")

// Config is config.json.
//
//	{
//	  "radio": {"freq_mhz": 869.618, "bw_khz": 62.5, "sf": 8, "cr": 8, "tx_dbm": 22, "path_hash_bytes": 1},
//	  "boards": {"D2A1F3C4B5E69788": "RAK WisBlock 4631"}
//	}
type Config struct {
	// Radio is the radio part of the last setup sent: written once the node has answered its "set radio" step, even
	// when a later step fails (the node saved it). nil when no setup has sent one.
	Radio *Radio `json:"radio,omitempty"`
	// Boards is the board picked on Choose board for each board's USB serial number: flash.Port.Key (the serial
	// in one form, upper case without colons) → the catalog's name for the board, nothing else. The next time the
	// same board is plugged in, Choose board's cursor starts there; the owner still confirms.
	Boards map[string]string `json:"boards,omitempty"`
}

// Radio is the radio settings sent, with the key names and units of the messenger's file (SPEC.md 8.4), so the two
// files read alike.
type Radio struct {
	FreqMHz float64 `json:"freq_mhz"`
	BWkHz   float64 `json:"bw_khz"`
	SF      int     `json:"sf"`
	CR      int     `json:"cr"` // 5–8, meaning 4/5 to 4/8
	TxDBm   int     `json:"tx_dbm"`
	// PathHashBytes is the path hash size sent, 1–3; 0 when none was (firmware before v1.14.0).
	PathHashBytes int `json:"path_hash_bytes,omitempty"`
}

// Draft is a setup form that was not sent, as typed: each row's text, so a half-typed frequency comes back exactly as
// it was left. Pick-list rows (bandwidth, spreading factor, coding rate, path hash) hold the value's text too.
// An empty string is a row the owner left at its default; a row the owner emptied on purpose (a blank latitude,
// which sends nothing: SPEC.md 4) is "" and named in Cleared, so it comes back blank rather than as the node's value.
//
// drafts.json keys each draft by flash.Port.DraftKey: the board's USB serial number in one form, or for a board with
// no useful serial its USB socket and identity ("usb 1-1.2 10c4:ea60"), when the owner chooses that (owner question
// 13 in docs/phase2-design.md). A draft whose Role isn't the node's role when the form reopens gives only the rows
// both forms have, and the form says so.
//
// It has no field for the admin or guest password, by design: a password is typed each time and never written
// anywhere (SPEC.md 4, 8).
//
//	{"D2A1F3C4B5E69788": {"role": "repeater", "name": "Hilltop 2", "freq": "869.6", "bw": "62.5", "sf": "8",
//	  "cr": "8", "tx": "22", "path_hash": "1", "lat": "", "lon": "", "advert": "", "cleared": ["lat", "lon"],
//	  "saved": "2026-11-24T14:32:00Z"}}
type Draft struct {
	// Role is the form's shape: nodecli.RoleRepeater or nodecli.RoleRoomServer.
	Role     string `json:"role"`
	Name     string `json:"name"`
	Freq     string `json:"freq"`
	BW       string `json:"bw"`
	SF       string `json:"sf"`
	CR       string `json:"cr"`
	TX       string `json:"tx"`
	PathHash string `json:"path_hash"`
	Lat      string `json:"lat"`
	Lon      string `json:"lon"`
	Advert   string `json:"advert"`
	// Cleared names the rows emptied on purpose, by their JSON keys above ("lat", "lon", "advert", …).
	Cleared []string  `json:"cleared,omitempty"`
	Saved   time.Time `json:"saved"`
}
