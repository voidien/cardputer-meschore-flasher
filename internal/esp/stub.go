package esp

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// The flasher stubs the web flasher uploads: Espressif's legacy flasher stub v1.3.0 as bundled in esptool-js 0.5.4,
// GPL-2.0-or-later, its source in third_party/esptool-legacy-flasher-stub-v1.3.0/. stubs/README.md says where
// each file came from and how it was checked.
//
//go:embed stubs/*.json
var stubFiles embed.FS

// stubImage is one stub: its code (text) and data, where each goes in the chip's RAM, and where to start it.
type stubImage struct {
	text      []byte
	textStart uint32
	data      []byte
	dataStart uint32
	entry     uint32
}

// loadStub reads a stub as esptool-js's getStubJsonByChipName does: the JSON's base64 text and data, decoded.
func loadStub(name string) (*stubImage, error) {
	raw, err := stubFiles.ReadFile("stubs/" + name)
	if err != nil {
		return nil, fmt.Errorf("esp: no flasher stub %s: %w", name, err)
	}
	var j struct {
		Entry     uint32 `json:"entry"`
		Text      string `json:"text"`
		TextStart uint32 `json:"text_start"`
		Data      string `json:"data"`
		DataStart uint32 `json:"data_start"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("esp: flasher stub %s: %w", name, err)
	}
	s := &stubImage{textStart: j.TextStart, dataStart: j.DataStart, entry: j.Entry}
	if s.text, err = base64.StdEncoding.DecodeString(j.Text); err != nil {
		return nil, fmt.Errorf("esp: flasher stub %s text: %w", name, err)
	}
	if s.data, err = base64.StdEncoding.DecodeString(j.Data); err != nil {
		return nil, fmt.Errorf("esp: flasher stub %s data: %w", name, err)
	}
	return s, nil
}
