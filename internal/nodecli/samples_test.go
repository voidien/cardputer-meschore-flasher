package nodecli

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Against real MeshCore v1.18.0 repeater releases (never in the repository): the version string, build date and
// ver format inside each image make the reply FakeNode gives, that reply's version is named in the file's name,
// and the board name `board` would answer is in the image. Runs only with MESHFLASH_SAMPLES set to the research
// folder (fw-samples-esp32, fw-samples-nrf52).
func TestSamples(t *testing.T) {
	root := os.Getenv("MESHFLASH_SAMPLES")
	if root == "" {
		t.Skip("MESHFLASH_SAMPLES not set")
	}
	boards := map[string]string{
		"Heltec_v2_repeater": "Heltec V2", "Heltec_v3_repeater": "Heltec V3", "Xiao_C3_repeater": "Xiao C3",
		"RAK_4631_repeater": "RAK 4631", "Xiao_nrf52_repeater": "Seeed Xiao-nrf52",
	}
	files, _ := filepath.Glob(filepath.Join(root, "fw-samples-*", "*repeater-v*"))
	if len(files) == 0 {
		t.Fatalf("no samples under %s", root)
	}
	verRE := regexp.MustCompile(`v\d+\.\d+\.\d+-[0-9a-f]{7,}`)
	dateRE := regexp.MustCompile(`\b\d\d-[A-Z][a-z]{2}-\d{4}\b`)
	fake := NewFakeNode(RoleRepeater, "v1.18.0").Version.Raw
	for _, path := range files {
		name := filepath.Base(path)
		if strings.HasSuffix(name, ".uf2") {
			continue
		}
		image := readImage(t, path)
		if !bytes.Contains(image, []byte("%s (Build: %s)")) || !bytes.Contains(image, []byte("Repeater ID: ")) {
			t.Errorf("%s: no ver format or banner", name)
		}
		ver, date := verRE.Find(image), dateRE.Find(image)
		if ver == nil || date == nil {
			t.Errorf("%s: no version or build date", name)
			continue
		}
		reply := string(ver) + " (Build: " + string(date) + ")"
		v, err := ParseVersion(reply)
		if err != nil || !v.NamedIn(name) {
			t.Errorf("%s: %q (%v) isn't named in the file", name, reply, err)
		}
		if reply != fake {
			t.Errorf("%s answers %q, FakeNode %q", name, reply, fake)
		}
		board := boards[name[:strings.Index(name, "-v")]]
		if board == "" || !bytes.Contains(image, []byte(board+"\x00")) {
			t.Errorf("%s: board name %q not in the image", name, board)
		}
	}
}

// readImage is the firmware in a release file: a .bin as it is, or a DFU .zip's firmware.bin.
func readImage(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".zip") {
		return data
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if strings.HasSuffix(f.Name, ".bin") {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			b, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
	t.Fatalf("%s: no .bin inside", path)
	return nil
}
