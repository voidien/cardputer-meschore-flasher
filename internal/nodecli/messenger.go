package nodecli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
)

// MessengerRadio is the radio a messenger app on the Zero left for meshflash to offer (SPEC.md 8.4).
type MessengerRadio struct {
	// Radio is the messenger's radio setting, checked as SPEC.md 8.4 says.
	Radio Radio
	// TxDBm is the messenger's TX power, valid when HasTx; the board's own limit still applies (TxLimit).
	TxDBm int
	// HasTx says the file gave a TX power.
	HasTx bool
}

// Found says these settings came from a file that was used. ReadMessengerRadio gives the zero value whenever it
// skips a file, and a file it uses always has a frequency (150–2500 MHz), so the note need not be read to tell "used"
// from "skipped" or from "no file read at all".
func (r MessengerRadio) Found() bool { return r.Radio.FreqMHz > 0 }

// MessengerPath is where a messenger keeps its settings: ~/.config/mesh/config.json (or under $XDG_CONFIG_HOME).
func MessengerPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "mesh", "config.json")
}

// maxMessengerFile is the most read from the file: a settings file is a few hundred bytes.
const maxMessengerFile = 64 << 10

// ReadMessengerRadio reads the "radio" object of a messenger's config.json (SPEC.md 8.4) and nothing else from it.
// The file is only ever opened for reading. A missing, unreadable or out-of-range file is never an error that stops
// setup: it gives the zero value and a note, in plain English, for the setup form. A good file gives an empty note.
//
//	{"radio": {"freq_mhz": 869.618, "bw_khz": 62.5, "sf": 8, "cr": 8, "tx_dbm": 22}}
//
// freq_mhz 150–2500, bw_khz one of Bandwidths, sf 5–12, cr 5–8, tx_dbm optional (MinTxDBm to DefaultMaxTxDBm).
// Every value must be there and right, or none is used.
func ReadMessengerRadio(path string) (MessengerRadio, string) {
	const from = "Your messenger's radio settings weren't used: "
	// Only a regular file is opened: opening a FIFO (or a device) for reading would wait for a writer, and the
	// setup form with it.
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return MessengerRadio{}, from + "there's no " + path + "."
		}
		return MessengerRadio{}, from + path + " can't be read."
	}
	if !info.Mode().IsRegular() {
		return MessengerRadio{}, from + path + " isn't a file."
	}
	f, err := os.Open(path)
	if err != nil {
		return MessengerRadio{}, from + path + " can't be read."
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMessengerFile+1))
	if err != nil {
		return MessengerRadio{}, from + path + " can't be read."
	}
	if len(data) > maxMessengerFile {
		return MessengerRadio{}, from + path + " is too big to be a settings file."
	}
	var file struct {
		Radio *struct {
			FreqMHz *float64 `json:"freq_mhz"`
			BWkHz   *float64 `json:"bw_khz"`
			SF      *float64 `json:"sf"`
			CR      *float64 `json:"cr"`
			TxDBm   *float64 `json:"tx_dbm"`
		} `json:"radio"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return MessengerRadio{}, from + path + " isn't valid JSON, or its radio values aren't numbers."
	}
	r := file.Radio
	if r == nil {
		return MessengerRadio{}, from + path + " has no radio settings."
	}
	if r.FreqMHz == nil || r.BWkHz == nil || r.SF == nil || r.CR == nil {
		return MessengerRadio{}, from + "it lacks one of freq_mhz, bw_khz, sf and cr."
	}
	whole := func(x float64) (int, bool) {
		if x != math.Trunc(x) || math.Abs(x) > 1e6 {
			return 0, false
		}
		return int(x), true
	}
	out := MessengerRadio{Radio: Radio{FreqMHz: *r.FreqMHz, BWkHz: *r.BWkHz}}
	var okSF, okCR bool
	out.Radio.SF, okSF = whole(*r.SF)
	out.Radio.CR, okCR = whole(*r.CR)
	switch {
	case !okSF || !okCR:
		return MessengerRadio{}, from + "its sf and cr must be whole numbers."
	case *r.FreqMHz < 150 || *r.FreqMHz > 2500:
		return MessengerRadio{}, from + fmt.Sprintf("its frequency, %g MHz, isn't 150 to 2500 MHz.", *r.FreqMHz)
	}
	if _, ok := bandwidth(out.Radio.BWkHz); !ok {
		return MessengerRadio{}, from + fmt.Sprintf("its bandwidth, %g kHz, isn't one the radio takes.", *r.BWkHz)
	}
	if out.Radio.SF < 5 || out.Radio.SF > 12 {
		return MessengerRadio{}, from + fmt.Sprintf("its spreading factor, %d, isn't 5 to 12.", out.Radio.SF)
	}
	if out.Radio.CR < 5 || out.Radio.CR > 8 {
		return MessengerRadio{}, from + fmt.Sprintf("its coding rate, %d, isn't 5 to 8.", out.Radio.CR)
	}
	if r.TxDBm != nil {
		tx, ok := whole(*r.TxDBm)
		if !ok || tx < MinTxDBm || tx > DefaultMaxTxDBm {
			return MessengerRadio{}, from + fmt.Sprintf("its tx_dbm must be a whole number from %d to %d.", MinTxDBm, DefaultMaxTxDBm)
		}
		out.TxDBm, out.HasTx = tx, true
	}
	out.Radio = out.Radio.Clean()
	return out, ""
}
