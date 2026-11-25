package nodecli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPresets(t *testing.T) {
	if len(Presets) != 27 {
		t.Fatalf("%d presets, want 27", len(Presets))
	}
	v, _ := ParseVersion("v1.15.0-dee3e26")
	titles := map[string]bool{}
	for _, p := range Presets {
		if titles[p.Title] {
			t.Errorf("%q twice", p.Title)
		}
		titles[p.Title] = true
		if err := CheckRadio(p.Radio, v); err != nil {
			t.Errorf("%s: %v", p.Title, err)
		}
		if p.PathHashBytes < 0 || p.PathHashBytes > 3 {
			t.Errorf("%s: path hash %d", p.Title, p.PathHashBytes)
		}
	}
	// Spot checks against docs/meshcore-flashing.md 7.8.
	for title, want := range map[string]string{
		"Australia":                 "915.8,250,10,5",
		"EU/UK (Narrow)":            "869.618,62.5,8,8",
		"Czech Republic (Narrow)":   "869.432,62.5,7,5",
		"Portugal 433":              "433.375,62.5,9,6",
		"Thailand":                  "921,62.5,7,5",
		"USA - Southern California": "927.875,62.5,7,5",
		"Vietnam (Deprecated)":      "920.25,250,11,5",
	} {
		found := false
		for _, p := range Presets {
			if p.Title == title {
				found = true
				if p.Radio.Arg() != want {
					t.Errorf("%s: %s, want %s", title, p.Radio.Arg(), want)
				}
			}
		}
		if !found {
			t.Errorf("no preset %q", title)
		}
	}
	if DefaultRadio.Arg() != "869.618,62.5,8,5" {
		t.Errorf("default %s", DefaultRadio.Arg())
	}
	// A preset without a path hash size leaves the setting alone.
	s := Settings{PathHashBytes: 2}
	Presets[5].Apply(&s) // Brazil
	if s.PathHashBytes != 2 || s.Radio.FreqMHz != 923.125 {
		t.Errorf("Brazil applied: %+v", s)
	}
	Presets[17].Apply(&s) // New Zealand (Gisborne): 1 byte
	if s.PathHashBytes != 1 {
		t.Errorf("Gisborne applied: %+v", s)
	}
}

func TestReadMessengerRadio(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o400); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good.json", `{"name":"other app","radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":8,"cr":8,"tx_dbm":22,"extra":1},"keys":["…"]}`)
	before, _ := os.Stat(good)
	r, note := ReadMessengerRadio(good)
	if note != "" || r.Radio != (Radio{869.618, 62.5, 8, 8}) || !r.HasTx || r.TxDBm != 22 || !r.Found() {
		t.Errorf("good file: %+v, %q", r, note)
	}
	if (MessengerRadio{}).Found() {
		t.Error("the zero MessengerRadio says it was found")
	}
	after, _ := os.Stat(good)
	if !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		t.Error("the file changed")
	}
	r, note = ReadMessengerRadio(write("notx.json", `{"radio":{"freq_mhz":910.525,"bw_khz":250,"sf":11,"cr":5}}`))
	if note != "" || r.HasTx || r.Radio.BWkHz != 250 {
		t.Errorf("without tx: %+v, %q", r, note)
	}
	for _, c := range []struct{ name, body, says string }{
		{"missing", "", "there's no"},
		{"notjson.json", `{"radio":`, "isn't valid JSON"},
		{"strings.json", `{"radio":{"freq_mhz":"869.618","bw_khz":62.5,"sf":8,"cr":8}}`, "isn't valid JSON"},
		{"noradio.json", `{"theme":"dark"}`, "has no radio settings"},
		{"partial.json", `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":8}}`, "lacks one of"},
		{"freq.json", `{"radio":{"freq_mhz":100,"bw_khz":62.5,"sf":8,"cr":8}}`, "isn't 150 to 2500 MHz"},
		{"bw.json", `{"radio":{"freq_mhz":869.618,"bw_khz":60,"sf":8,"cr":8}}`, "60 kHz, isn't one the radio takes"},
		{"sf.json", `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":13,"cr":8}}`, "spreading factor, 13"},
		{"sfhalf.json", `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":7.5,"cr":8}}`, "whole numbers"},
		{"cr.json", `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":8,"cr":4}}`, "coding rate, 4"},
		{"tx.json", `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":8,"cr":8,"tx_dbm":30}}`, "tx_dbm"},
		{"big.json", `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":8,"cr":8},"pad":"` + strings.Repeat("x", 70000) + `"}`, "too big"},
	} {
		path := filepath.Join(dir, c.name)
		if c.body != "" {
			path = write(c.name, c.body)
		}
		r, note := ReadMessengerRadio(path)
		if r != (MessengerRadio{}) || r.Found() || !strings.Contains(note, c.says) || !strings.HasPrefix(note, "Your messenger's radio settings weren't used: ") {
			t.Errorf("%s: %+v, %q", c.name, r, note)
		}
	}
	// A directory, or a FIFO (which would block an open for reading until someone writes), isn't opened.
	if _, note := ReadMessengerRadio(dir); !strings.Contains(note, "isn't a file") {
		t.Errorf("directory: %q", note)
	}
	fifo := filepath.Join(dir, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if r, note := ReadMessengerRadio(fifo); r != (MessengerRadio{}) || !strings.Contains(note, "isn't a file") {
		t.Errorf("FIFO: %+v %q", r, note)
	}
	// A file that can't be opened (no read permission; root reads it anyway, so only checked as non-root).
	locked := write("locked.json", `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":8,"cr":8}}`)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if _, note := ReadMessengerRadio(locked); !strings.Contains(note, "can't be read") {
			t.Errorf("unreadable: %q", note)
		}
	}
	if p := MessengerPath(); p != "" && !strings.HasSuffix(p, filepath.Join("mesh", "config.json")) {
		t.Errorf("MessengerPath %q", p)
	}
}
