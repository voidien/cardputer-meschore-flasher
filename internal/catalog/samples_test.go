package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// samplesDir is the research folder with real MeshCore v1.18.0 release files, which are never committed. Tests that
// use them run only when MESHFLASH_SAMPLES points there.
func samplesDir(t *testing.T) string {
	dir := os.Getenv("MESHFLASH_SAMPLES")
	if dir == "" {
		t.Skip("MESHFLASH_SAMPLES is not set")
	}
	return dir
}

// The real release files pass the download checks, served as the flasher site would serve them; the Heltec v3 app
// also comes through the GitHub path with the MD5 header GitHub sent for it (docs/meshcore-flashing.md 2.5).
func TestSamplesDownload(t *testing.T) {
	dir := samplesDir(t)
	samples := map[string]string{ // file → its sha256 (docs/meshcore-flashing.md 2.5)
		"fw-samples-esp32/Heltec_v2_repeater-v1.18.0-81dbae5-merged.bin": "5816692ba1a70341c961965758b89b2024011543941c5910de98480ab836f811",
		"fw-samples-esp32/Heltec_v3_repeater-v1.18.0-81dbae5-merged.bin": "396c8e2a65ffc8ce7618eed05574afae7cb8d63faeb3f23def9ce2260e869ef7",
		"fw-samples-esp32/Heltec_v3_repeater-v1.18.0-81dbae5.bin":        "c34b200b40760ca473868510b6c7089a8215587a5320d19dae1c30d883566349",
		"fw-samples-esp32/Xiao_C3_repeater-v1.18.0-81dbae5-merged.bin":   "b23b04802609343e7fd62768ecf03cc303f43daf7ba58090f34f21946a4ebcaa",
		"fw-samples-nrf52/RAK_4631_repeater-v1.18.0-81dbae5.zip":         "a5505147bd31aa83fdfb681525a25747250d670d46ca93e958db084b6fb9ec95",
		"fw-samples-nrf52/RAK_4631_repeater-v1.18.0-81dbae5.uf2":         "1a0c9adb8218c7b0e66e600706d532749875f912fe3f59481a2f916628e80328",
		"fw-samples-nrf52/Xiao_nrf52_repeater-v1.18.0-81dbae5.zip":       "97539d2029900719dc250a222cad7f2149ec90d2b3e360a07b4488cfd89430eb",
	}
	f := newFakeServer(t)
	c, _, _ := newTestClient(t, f)
	for rel, want := range samples {
		body, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(rel)
		file := releaseFile(f.sites(), "repeater-v1.18.0", name, "github")
		f.set("/mc/releases/download/repeater-v1.18.0/"+name+"?repo=github", fakeResp{body: body})
		e, err := c.Download(context.Background(), file, nil)
		if err != nil || e.SHA256 != want {
			t.Errorf("%s: %+v %v", name, e, err)
		}
	}

	// GitHub's answer for the Heltec v3 app: Content-Length and X-Ms-Blob-Content-Md5 3yiGqsQs3ExWGFy/84k/Wg==.
	body, err := os.ReadFile(filepath.Join(dir, "fw-samples-esp32/Heltec_v3_repeater-v1.18.0-81dbae5.bin"))
	if err != nil {
		t.Fatal(err)
	}
	f2 := newFakeServer(t)
	c2, _, _ := newTestClient(t, f2)
	file := releaseFile(f2.sites(), "repeater-v1.18.0", "Heltec_v3_repeater-v1.18.0-81dbae5.bin", "github")
	f2.set("/gh/meshcore-dev/MeshCore/releases/download/repeater-v1.18.0/Heltec_v3_repeater-v1.18.0-81dbae5.bin",
		fakeResp{body: body, header: map[string]string{"X-Ms-Blob-Content-Md5": "3yiGqsQs3ExWGFy/84k/Wg=="}})
	if e, err := c2.Download(context.Background(), file, nil); err != nil || e.URL != file.Fallback || e.Size != 1174704 {
		t.Fatalf("GitHub fallback with the real MD5: %+v %v", e, err)
	}
}
