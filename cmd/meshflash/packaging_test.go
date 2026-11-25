package main

import (
	"image/png"
	"os"
	"strings"
	"testing"
)

// The home-screen entry "MeshFlash" must pass the launcher's checks (cp0_desktop_exec_policy, the kit's
// docs/device.md "Apps started from the home screen") once make install-desktop fills in the path, start meshflash on
// the LCD, and point at the icon installed with it (S32). The launcher reads it as key=value lines under its header,
// so the header comes first and no key is given twice.
func TestHomeScreenEntry(t *testing.T) {
	b, err := os.ReadFile("../../packaging/meshflash.desktop.in")
	if err != nil {
		t.Fatal(err)
	}
	const bin = "/home/void/.local/bin/meshflash"
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(b), "@BIN@", bin), "\n"), "\n")
	if lines[0] != "[Desktop Entry]" {
		t.Errorf("first line %q, want [Desktop Entry]", lines[0])
	}
	entry := map[string]string{}
	for _, line := range lines[1:] {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Errorf("line %q isn't key=value", line)
			continue
		}
		if _, twice := entry[k]; twice {
			t.Errorf("%s is given twice", k)
		}
		entry[k] = v
	}
	if entry["Name"] != "MeshFlash" || entry["Terminal"] != "false" || entry["Type"] != "Application" {
		t.Errorf("entry %v", entry)
	}
	exec := entry["Exec"]
	if exec != bin+" --screen" {
		t.Errorf("Exec=%q, want meshflash on the LCD", exec)
	}
	if len(exec) > 512 || strings.ContainsAny(exec, "|&;<>`$\\") {
		t.Errorf("the launcher refuses Exec=%q (at most 512 characters, none of |&;<>`$\\)", exec)
	}
	if entry["Icon"] != "share/images/meshflash_100.png" {
		t.Errorf("Icon=%q, want the icon make install-desktop puts in share/images", entry["Icon"])
	}
}

// The icon is a 100×100 PNG like the launcher's own (S33), on a clear ground: the tile's corners are transparent and
// its middle isn't.
func TestIcon(t *testing.T) {
	f, err := os.Open("../../packaging/meshflash_100.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil || cfg.Width != 100 || cfg.Height != 100 {
		t.Fatalf("the icon isn't a 100×100 PNG like the launcher's: %+v, %v", cfg, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	im, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]int{{0, 0}, {99, 0}, {0, 99}, {99, 99}} {
		if _, _, _, a := im.At(p[0], p[1]).RGBA(); a != 0 {
			t.Errorf("corner %v isn't clear (alpha %d)", p, a)
		}
	}
	if _, _, _, a := im.At(50, 50).RGBA(); a != 0xffff {
		t.Errorf("the middle isn't opaque (alpha %d)", a)
	}
}
