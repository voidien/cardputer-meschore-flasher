package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// synthetic makes deterministic bytes that start with first (no real firmware in the repository).
func synthetic(n int, first ...byte) []byte {
	b := make([]byte, n)
	x := uint32(2463534242)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	copy(b, first)
	return b
}

func espImage(n int) []byte { return synthetic(n, 0xE9, 0x06, 0x02, 0x3F) }

// classicMerged is a classic ESP32's merged image: 4 KB of 0xFF, then the bootloader's 0xE9 at 0x1000.
func classicMerged(n int) []byte {
	b := synthetic(n)
	for i := 0; i < 0x1000; i++ {
		b[i] = 0xFF
	}
	b[0x1000] = 0xE9
	return b
}

func dfuZip(t testing.TB) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, data := range map[string][]byte{
		"manifest.json": []byte(`{"manifest":{"application":{"bin_file":"firmware.bin","dat_file":"firmware.dat"}}}`),
		"firmware.dat":  {0x52, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 1, 0, 0xb6, 0, 0, 0},
		"firmware.bin":  synthetic(80 << 10),
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func uf2(n int) []byte {
	b := synthetic(n)
	binary.LittleEndian.PutUint32(b, uf2Magic0)
	binary.LittleEndian.PutUint32(b[4:], uf2Magic1)
	return b
}

// releaseFile is a MeshCore release file as the catalog gives it, on the fake sites.
func releaseFile(s Sites, tag, name, repo string) File {
	n := "/releases/download/" + tag + "/" + name + "?repo=" + repo
	f := File{Type: "flash-update", Name: n, Title: name, URL: s.MeshCore + n, Source: SourceMeshCore}
	f.Fallback = gitHubFallback(s.GitHub, n)
	return f
}

func inlineFile(s Sites, name string) File {
	return File{Type: "flash-update", Name: name, Title: "App firmware", URL: s.MeshCore + "/firmware/" + name, Source: SourceMeshCore}
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestDownload(t *testing.T) {
	f := newFakeServer(t)
	c, clock, dir := newTestClient(t, f)
	img := espImage(300 << 10)
	file := releaseFile(f.sites(), "repeater-v1.18.0", "Heltec_v3_repeater-v1.18.0-81dbae5.bin", "github")
	f.set("/mc/releases/download/repeater-v1.18.0/Heltec_v3_repeater-v1.18.0-81dbae5.bin?repo=github",
		fakeResp{ctype: "application/octet-stream", body: img})

	var last [2]int64
	calls := 0
	e, err := c.Download(context.Background(), file, func(done, total int64) { last = [2]int64{done, total}; calls++ })
	if err != nil {
		t.Fatal(err)
	}
	want := Entry{Name: "Heltec_v3_repeater-v1.18.0-81dbae5.bin", URL: file.URL, Size: int64(len(img)), SHA256: sha(img),
		Saved: clock.Now()}
	if e != want {
		t.Fatalf("entry %+v, want %+v", e, want)
	}
	if last != [2]int64{int64(len(img)), int64(len(img))} || calls < 2 {
		t.Errorf("progress: %d calls, last %v", calls, last)
	}
	if got := f.requests(); len(got) != 1 || f.agents[0] != "Mozilla/5.0 (compatible; meshflash/1.2.3)" || f.encs[0] != "identity" {
		t.Errorf("requests %q, agents %q, encodings %q", got, f.agents, f.encs)
	}
	assertMode(t, filepath.Join(dir, "files"), 0o700|os.ModeDir)
	assertMode(t, filepath.Join(dir, "files", e.Name), 0o600)
	assertMode(t, filepath.Join(dir, "files", "index.json"), 0o600)

	if got, err := c.Verify(e.Name); err != nil || got != want {
		t.Fatalf("Verify: %+v %v", got, err)
	}
	data, got, err := c.ReadVerified(e.Name)
	if err != nil || got != want || !bytes.Equal(data, img) {
		t.Fatalf("ReadVerified: %v", err)
	}
	if got, ok := c.Saved(file); !ok || got != want {
		t.Errorf("Saved: %+v %v", got, ok)
	}
	if _, ok := c.Saved(inlineFile(f.sites(), e.Name)); ok {
		t.Error("Saved matched a file with the same name from another address")
	}
	if list, err := c.List(); err != nil || len(list) != 1 || list[0] != want {
		t.Errorf("List: %+v %v", list, err)
	}
	assertNoTemp(t, dir)
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(filepath.Join(dir, "files"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
}

// Every refused download leaves nothing behind and says why.
func TestDownloadRefusals(t *testing.T) {
	page := []byte("<!DOCTYPE html><html><body>Error</body></html>" + strings.Repeat(" ", 60<<10))
	for _, c := range []struct {
		name string
		file string
		resp fakeResp
		want string
	}{
		{"404", "a.bin", fakeResp{status: 404, body: []byte("nope")}, "HTTP 404"},
		{"500", "a.bin", fakeResp{status: 500, body: espImage(100 << 10)}, "HTTP 500"},
		{"html type", "a.bin", fakeResp{ctype: "text/html; charset=utf-8", body: espImage(100 << 10)}, "web page"},
		{"html body", "a.bin", fakeResp{ctype: "application/octet-stream", body: page}, "web page"},
		{"tiny", "a.bin", fakeResp{ctype: "application/octet-stream", body: espImage(10 << 10)}, "too small"},
		{"short", "a.bin", fakeResp{body: espImage(100 << 10), shortBy: 4096}, "stopped early"},
		{"not esp32", "a.bin", fakeResp{ctype: "application/octet-stream", body: synthetic(100<<10, 0x7F)}, "0xE9"},
		{"app ff", "a.bin", fakeResp{ctype: "application/octet-stream", body: classicMerged(100 << 10)}, "0xE9"},
		{"not zip", "a.zip", fakeResp{ctype: "application/zip", body: synthetic(100 << 10)}, "doesn't open as a zip"},
		{"not uf2", "a.uf2", fakeResp{ctype: "application/octet-stream", body: synthetic(100 << 10)}, "UF2"},
		{"compressed", "a.bin", fakeResp{body: espImage(100 << 10), header: map[string]string{"Content-Encoding": "br"}}, "compressed"},
		{"md5 header", "a.bin", fakeResp{body: espImage(100 << 10), header: map[string]string{
			"X-Ms-Blob-Content-Md5": base64.StdEncoding.EncodeToString(make([]byte, 16))}}, "X-Ms-Blob-Content-Md5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeServer(t)
			cl, _, dir := newTestClient(t, f)
			f.set("/mc/firmware/"+c.file, c.resp)
			_, err := cl.Download(context.Background(), inlineFile(f.sites(), c.file), nil)
			if !errors.Is(err, ErrBadAnswer) || !strings.Contains(err.Error(), c.want) ||
				!strings.Contains(err.Error(), f.sites().MeshCore+"/firmware/"+c.file) {
				t.Fatalf("got %v, want a refusal naming the URL and saying %q", err, c.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "files", c.file)); !errors.Is(err, os.ErrNotExist) {
				t.Error("refused file saved")
			}
			if list, _ := cl.List(); len(list) != 0 {
				t.Errorf("index %+v", list)
			}
			if len(f.requests()) != 1 {
				t.Errorf("requests %q: an inline file has no fallback", f.requests())
			}
			assertNoTemp(t, dir)
		})
	}
}

// The kinds of file meshflash can flash, and the classic ESP32 merged image whose bootloader is at 0x1000.
func TestDownloadKinds(t *testing.T) {
	f := newFakeServer(t)
	c, _, _ := newTestClient(t, f)
	for name, body := range map[string][]byte{
		"Heltec_v2_repeater-v1.18.0-81dbae5-merged.bin": classicMerged(200 << 10),
		"Heltec_v3_repeater-v1.18.0-81dbae5-merged.bin": espImage(200 << 10),
		"RAK_4631_repeater-v1.18.0-81dbae5.zip":         dfuZip(t),
		"RAK_4631_repeater-v1.18.0-81dbae5.uf2":         uf2(100 << 10),
	} {
		f.set("/mc/firmware/"+name, fakeResp{body: body})
		e, err := c.Download(context.Background(), inlineFile(f.sites(), name), nil)
		if err != nil || e.SHA256 != sha(body) {
			t.Errorf("%s: %+v %v", name, e, err)
		}
	}
	if list, _ := c.List(); len(list) != 4 {
		t.Errorf("%d saved", len(list))
	}
}

// A MeshCore release file the flasher site fails to give comes from GitHub, as GitHub serves it: a redirect to its
// storage, which sends Content-Length and the base64 MD5.
func TestDownloadGitHubFallback(t *testing.T) {
	img := espImage(400 << 10)
	sum := md5.Sum(img)
	md5Header := base64.StdEncoding.EncodeToString(sum[:])
	const asset = "/releases/download/companion-v1.17.1/Heltec_v3_companion_radio_ble-v1.17.1-d929643.bin"
	const ghPath = "/gh/meshcore-dev/MeshCore" + asset

	type step struct {
		primary, github fakeResp
		wantErr         string // "" for success from GitHub
	}
	for name, s := range map[string]step{
		"404 then GitHub": {
			primary: fakeResp{status: 404},
			github:  fakeResp{body: img, header: map[string]string{"X-Ms-Blob-Content-Md5": md5Header}},
		},
		"web page then GitHub": {
			primary: fakeResp{ctype: "text/html", body: []byte("<html>")},
			github:  fakeResp{body: img},
		},
		"MD5 mismatch": {
			primary: fakeResp{status: 404},
			github:  fakeResp{body: append(espImage(400 << 10)[:1000:1000], make([]byte, 399<<10+24)...), header: map[string]string{"X-Ms-Blob-Content-Md5": md5Header}},
			wantErr: "X-Ms-Blob-Content-Md5",
		},
		"GitHub without Content-Length": {
			primary: fakeResp{status: 404},
			github:  fakeResp{body: img, noLength: true},
			wantErr: "no Content-Length",
		},
		"GitHub short": {
			primary: fakeResp{status: 502},
			github:  fakeResp{body: img, shortBy: 1},
			wantErr: "stopped early",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeServer(t)
			c, _, dir := newTestClient(t, f)
			file := releaseFile(f.sites(), "companion-v1.17.1", "Heltec_v3_companion_radio_ble-v1.17.1-d929643.bin", "github")
			f.set("/mc"+asset+"?repo=github", s.primary)
			// GitHub answers 302 to its asset storage.
			f.set(ghPath, fakeResp{status: 302, header: map[string]string{"Location": "/assets/blob?sig=x"}})
			f.set("/assets/blob?sig=x", s.github)
			e, err := c.Download(context.Background(), file, nil)
			if want := []string{"/mc" + asset + "?repo=github", ghPath, "/assets/blob?sig=x"}; strings.Join(f.requests(), " ") != strings.Join(want, " ") {
				t.Errorf("requests %q", f.requests())
			}
			if s.wantErr != "" {
				if !errors.Is(err, ErrBadAnswer) || !strings.Contains(err.Error(), s.wantErr) ||
					!strings.Contains(err.Error(), f.sites().MeshCore) || !strings.Contains(err.Error(), "GitHub too") {
					t.Fatalf("got %v, want both failures, the second saying %q", err, s.wantErr)
				}
				if list, _ := c.List(); len(list) != 0 {
					t.Error("saved")
				}
				assertNoTemp(t, dir)
				return
			}
			if err != nil || e.URL != file.Fallback || e.SHA256 != sha(img) {
				t.Fatalf("%+v %v", e, err)
			}
			if got, ok := c.Saved(file); !ok || got.URL != file.Fallback {
				t.Errorf("Saved after the fallback: %+v %v", got, ok)
			}
		})
	}

	// Only MeshCore's own files have a fallback; a Ripple file failing is just a failure.
	f := newFakeServer(t)
	c, _, _ := newTestClient(t, f)
	ripple := releaseFile(f.sites(), "t-deck-plus-v12.3", "Ultra-TDeck-v12.3.bin", "github-ripple")
	if ripple.Fallback != "" {
		t.Fatalf("Ripple fallback %q", ripple.Fallback)
	}
	if _, err := c.Download(context.Background(), ripple, nil); !strings.Contains(err.Error(), "HTTP 404") || len(f.requests()) != 1 {
		t.Fatalf("%v %q", err, f.requests())
	}
	// A download the owner stopped isn't retried from GitHub.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	file := releaseFile(f.sites(), "companion-v1.17.1", "Heltec_v3_companion_radio_ble-v1.17.1-d929643.bin", "github")
	if _, err := c.Download(ctx, file, nil); !errors.Is(err, context.Canceled) || len(f.requests()) != 1 {
		t.Fatalf("cancelled: %v %q", err, f.requests())
	}
}

// Verify, run before every flash, catches a saved file that changed or vanished.
func TestVerifyCatchesChange(t *testing.T) {
	f := newFakeServer(t)
	c, _, dir := newTestClient(t, f)
	img := espImage(120 << 10)
	f.set("/mc/firmware/a.bin", fakeResp{body: img})
	if _, err := c.Download(context.Background(), inlineFile(f.sites(), "a.bin"), nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "files", "a.bin")
	changed := append([]byte(nil), img...)
	changed[5000] ^= 0x40
	os.WriteFile(p, changed, 0o600)
	if _, err := c.Verify("a.bin"); !errors.Is(err, ErrDamaged) || !strings.Contains(err.Error(), "download it again") {
		t.Fatalf("Verify of a changed file: %v", err)
	}
	if _, _, err := c.ReadVerified("a.bin"); !errors.Is(err, ErrDamaged) {
		t.Fatalf("ReadVerified of a changed file: %v", err)
	}
	os.WriteFile(p, img[:len(img)-1], 0o600)
	if _, err := c.Verify("a.bin"); !errors.Is(err, ErrDamaged) {
		t.Fatalf("Verify of a shortened file: %v", err)
	}
	os.Remove(p)
	if _, err := c.Verify("a.bin"); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("Verify of a deleted file: %v", err)
	}
	if _, err := c.Verify("b.bin"); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("Verify of a file never saved: %v", err)
	}
	if list, _ := c.List(); len(list) != 0 {
		t.Errorf("List shows a file that is gone: %+v", list)
	}
}

func TestListAndDelete(t *testing.T) {
	f := newFakeServer(t)
	c, clock, dir := newTestClient(t, f)
	for _, name := range []string{"old.bin", "new.bin"} {
		f.set("/mc/firmware/"+name, fakeResp{body: espImage(60 << 10)})
		if _, err := c.Download(context.Background(), inlineFile(f.sites(), name), nil); err != nil {
			t.Fatal(err)
		}
		clock.Advance(time.Hour)
	}
	list, err := c.List()
	if err != nil || len(list) != 2 || list[0].Name != "new.bin" || list[1].Name != "old.bin" {
		t.Fatalf("List: %+v %v", list, err)
	}
	if err := c.Delete("new.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "new.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Error("file still there")
	}
	if list, _ := c.List(); len(list) != 1 || list[0].Name != "old.bin" {
		t.Fatalf("after Delete: %+v", list)
	}
	if err := c.Delete("new.bin"); err != nil {
		t.Errorf("Delete twice: %v", err)
	}
	if _, err := c.Verify("old.bin"); err != nil {
		t.Errorf("the other file: %v", err)
	}
}

// A file is saved only under a plain name of its own: never a path, a hidden file or the index.
func TestUnsafeNames(t *testing.T) {
	f := newFakeServer(t)
	c, _, _ := newTestClient(t, f)
	for _, name := range []string{"index.json", ".hidden.bin", "a%2F..%2Fb.bin", "a b.bin", ""} {
		file := File{Type: "flash", Name: name, URL: f.sites().MeshCore + "/firmware/" + name}
		if _, err := c.Download(context.Background(), file, nil); err == nil {
			t.Errorf("%q downloaded", name)
		}
	}
	if len(f.requests()) != 0 {
		t.Errorf("requests %q", f.requests())
	}
	for _, name := range []string{"../a.bin", "a/b.bin", "index.json", ".tmp-1"} {
		if _, err := c.Verify(name); err == nil {
			t.Errorf("Verify(%q)", name)
		}
		if err := c.Delete(name); err == nil {
			t.Errorf("Delete(%q)", name)
		}
	}
}

// A damaged index doesn't stop downloads; the files it listed have to be fetched again.
func TestDownloadDamagedIndex(t *testing.T) {
	f := newFakeServer(t)
	c, _, dir := newTestClient(t, f)
	os.MkdirAll(filepath.Join(dir, "files"), 0o700)
	os.WriteFile(filepath.Join(dir, "files", "index.json"), []byte("{broken"), 0o600)
	if _, err := c.List(); !errors.Is(err, ErrDamaged) {
		t.Fatalf("List: %v", err)
	}
	f.set("/mc/firmware/a.bin", fakeResp{body: espImage(60 << 10)})
	if _, err := c.Download(context.Background(), inlineFile(f.sites(), "a.bin"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify("a.bin"); err != nil {
		t.Fatal(err)
	}
}

// Verify and ReadVerified while a Download replaces the same file wait for it: they check the new file against the
// new record or the old against the old, never a mix (they did, and told the owner to download it again).
func TestVerifyDuringDownload(t *testing.T) {
	f := newFakeServer(t)
	c, _, _ := newTestClient(t, f)
	f.set("/mc/firmware/a.bin", fakeResp{body: espImage(60 << 10)})
	if _, err := c.Download(context.Background(), inlineFile(f.sites(), "a.bin"), nil); err != nil {
		t.Fatal(err)
	}
	errs := hammer(t, func() error { _, _, err := c.ReadVerified("a.bin"); return err }, func(i int) {
		// Different bytes under the same name each time, as a file the site replaced would be.
		f.set("/mc/firmware/a.bin", fakeResp{body: synthetic(60<<10+i, 0xE9, byte(i))})
		if _, err := c.Download(context.Background(), inlineFile(f.sites(), "a.bin"), nil); err != nil {
			t.Error(err)
		}
	})
	if errs != nil {
		t.Fatalf("ReadVerified during Download: %d failures, first %v", len(errs), errs[0])
	}
}
