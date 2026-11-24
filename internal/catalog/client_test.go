package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUserAgent(t *testing.T) {
	if got := UserAgent("0.1.0"); got != "Mozilla/5.0 (compatible; meshflash/0.1.0)" {
		t.Fatal(got)
	}
	if got := UserAgent(""); got != "Mozilla/5.0 (compatible; meshflash/dev)" {
		t.Fatal(got)
	}
}

// Update fetches the five files in the order js/catalog.js asks for them, saves them whole with the time, and Load
// gives back the same catalog with no network.
func TestUpdateAndLoad(t *testing.T) {
	t.Parallel() // heavy, independent: the shared catalogs are resolved once and only read
	f := newFakeServer(t)
	serveCatalog(t, f)
	c, clock, dir := newTestClient(t, f)

	if _, err := c.Load(); !errors.Is(err, ErrNoCatalog) {
		t.Fatalf("Load with nothing saved: %v", err)
	}
	cat, err := c.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Devices) != 72 {
		t.Errorf("%d devices, want 70 MeshCore + 2 ZephCore-only", len(cat.Devices))
	}
	if !cat.Fetched.Equal(clock.Now()) {
		t.Errorf("fetched %v, want %v", cat.Fetched, clock.Now())
	}

	// The same requests as the reference's loadCatalog on each site (goldens' "requests"), and nothing else.
	var want []string
	for _, gc := range goldenCases[:2] {
		prefix := "/mc"
		if gc.source == SourceZephCore {
			prefix = "/zc"
		}
		for _, r := range loadGolden(t, gc).Requests {
			want = append(want, prefix+r)
		}
	}
	if got := f.requests(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("requests %q, want %q", got, want)
	}
	for _, ua := range f.agents {
		if ua != "Mozilla/5.0 (compatible; meshflash/1.2.3)" {
			t.Errorf("User-Agent %q", ua)
		}
	}

	// Sizes: Size is the catalog as saved; Downloaded is what came over the wire (gzip makes it smaller).
	var size int64
	for _, name := range []string{"meshcore-config.json.gz", "meshcore-releases-github.json.gz",
		"meshcore-releases-github-ripple.json.gz", "zephcore-config.json.gz", "zephcore-releases-github-zephcore.json.gz"} {
		size += int64(len(readGz(t, name)))
	}
	if cat.Size != size || cat.Downloaded <= 0 || cat.Downloaded >= size/3 {
		t.Errorf("size %d (want %d), downloaded %d", cat.Size, size, cat.Downloaded)
	}
	t.Logf("catalog update: %d bytes, %d over the wire", cat.Size, cat.Downloaded)

	// Saved as fetched, the owner's only.
	catDir := filepath.Join(dir, "catalog")
	assertMode(t, catDir, 0o700|os.ModeDir)
	ents, err := os.ReadDir(catDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
		assertMode(t, filepath.Join(catDir, e.Name()), 0o600)
	}
	sort.Strings(names)
	if got := strings.Join(names, " "); got != "config-zephcore.json config.json fetched.json releases-github-ripple.json releases-github-zephcore.json releases-github.json" {
		t.Errorf("saved %s", got)
	}
	if saved, _ := os.ReadFile(filepath.Join(catDir, "config.json")); string(saved) != string(readGz(t, "meshcore-config.json.gz")) {
		t.Error("config.json isn't saved as fetched")
	}
	for _, leftover := range []string{"catalog.new", "catalog.old"} {
		if _, err := os.Stat(filepath.Join(dir, leftover)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind", leftover)
		}
	}

	// File addresses are on the site the catalog came from.
	d := cat.Find("Heltec v3")
	file := d.Firmware[0].Versions[0].Files[0]
	if !strings.HasPrefix(file.URL, f.sites().MeshCore+"/") || !strings.HasPrefix(file.Fallback, f.sites().GitHub+"/meshcore-dev/MeshCore/releases/download/") {
		t.Errorf("Heltec v3 file %+v", file)
	}

	// Load: the same catalog, offline.
	f.srv.Close()
	loaded, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, cat) {
		t.Error("Load gives a different catalog from the Update that saved it")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode() & (os.ModePerm | os.ModeDir); got != want {
		t.Errorf("%s: mode %v, want %v", path, got, want)
	}
}

// Any failure leaves the saved catalog as it was, and says which URL failed and why.
func TestUpdateFailureKeepsCache(t *testing.T) {
	f := newFakeServer(t)
	serveSmallCatalog(f)
	c, clock, _ := newTestClient(t, f)
	first, err := c.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(24 * time.Hour)

	// One release list answers 503: the last one fetched from flasher.meshcore.io.
	f.set("/mc/releases?repo=github-ripple", fakeResp{status: 503, body: []byte("busy")})
	_, err = c.Update(context.Background())
	var fe *FetchError
	if !errors.As(err, &fe) || fe.Status != 503 || !errors.Is(err, ErrBadAnswer) ||
		!strings.Contains(err.Error(), f.sites().MeshCore+"/releases?repo=github-ripple: ") ||
		!strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("Update with a 503: %v", err)
	}
	assertSameCatalog(t, c, first)

	// ZephCore's site unreachable (a closed port on the loopback): no network, the old catalog stays.
	serveSmallCatalog(f)
	dead := newFakeServer(t)
	dead.srv.Close()
	sites := f.sites()
	sites.ZephCore = dead.srv.URL
	c2 := NewClient(c.cache, "1.2.3", Options{Sites: sites, Clock: clock})
	_, err = c2.Update(context.Background())
	if !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), dead.srv.URL+"/config-zephcore.json") {
		t.Fatalf("Update with ZephCore unreachable: %v", err)
	}
	assertSameCatalog(t, c, first)

	// A catalog the resolver refuses (a pattern RE2 can't compile) is not saved either.
	f.set("/zc/config-zephcore.json", fakeResp{ctype: "application/json", body: []byte(
		`{"staticPath":"/firmware","device":[{"name":"x","type":"esp32","firmware":[{"github-zephcore":{"type":"zephcore","files":{"flash-update":"(?<=a)b"}}}]}]}`)})
	if _, err := c.Update(context.Background()); !errors.Is(err, ErrBadAnswer) || !strings.Contains(err.Error(), "saved one stays") {
		t.Fatalf("Update with a bad pattern: %v", err)
	}
	assertSameCatalog(t, c, first)

	// A stopped request.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Update(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Update: %v", err)
	}
	assertSameCatalog(t, c, first)
}

func assertSameCatalog(t *testing.T, c *Client, want *Catalog) {
	t.Helper()
	got, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Fetched.Equal(want.Fetched) || !reflect.DeepEqual(got, want) {
		t.Fatalf("the saved catalog changed (fetched %v, was %v)", got.Fetched, want.Fetched)
	}
}

// A Wi-Fi sign-in page answers 200 with HTML; it must never become the catalog.
func TestUpdateRefusesHTML(t *testing.T) {
	page := []byte("<!DOCTYPE html><html><body>Sign in to Hotel Wi-Fi</body></html>")
	for name, r := range map[string]fakeResp{
		"text/html":         {ctype: "text/html; charset=utf-8", body: page},
		"html sent as json": {ctype: "application/json", body: page},
		"not json":          {ctype: "application/json", body: []byte("hello")},
		"json, not catalog": {ctype: "application/json", body: []byte(`[1,2]`)},
		"gzip of html":      {ctype: "application/json", body: page, gzip: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeServer(t)
			serveSmallCatalog(f)
			f.set("/mc/config.json", r)
			c, _, dir := newTestClient(t, f)
			_, err := c.Update(context.Background())
			if !errors.Is(err, ErrBadAnswer) || !strings.Contains(err.Error(), f.sites().MeshCore+"/config.json") {
				t.Fatalf("Update: %v", err)
			}
			if _, err := c.Load(); !errors.Is(err, ErrNoCatalog) {
				t.Fatalf("something was saved: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "catalog")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("catalog folder made")
			}
		})
	}
	// A release list that is a web page too.
	f := newFakeServer(t)
	serveSmallCatalog(f)
	f.set("/zc/releases?repo=github-zephcore", fakeResp{ctype: "text/html", body: []byte("<html></html>")})
	c, _, _ := newTestClient(t, f)
	if _, err := c.Update(context.Background()); !errors.Is(err, ErrBadAnswer) || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("Update: %v", err)
	}
}

// Load checks every saved file against its SHA-256, and finds the previous catalog after an update that stopped
// between its two renames.
func TestLoadDamagedAndRecovered(t *testing.T) {
	f := newFakeServer(t)
	serveSmallCatalog(f)
	c, _, dir := newTestClient(t, f)
	cat, err := c.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	catDir := filepath.Join(dir, "catalog")
	if err := os.Rename(catDir, catDir+".old"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Load(); err != nil || !reflect.DeepEqual(got, cat) {
		t.Fatalf("Load from catalog.old: %v", err)
	}
	// The next update replaces it cleanly.
	if _, err := c.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(catDir + ".old"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("catalog.old left behind")
	}

	p := filepath.Join(catDir, "releases-github.json")
	data, _ := os.ReadFile(p)
	data[50] ^= 1
	os.WriteFile(p, data, 0o600)
	if _, err := c.Load(); !errors.Is(err, ErrDamaged) || !strings.Contains(err.Error(), "releases-github.json") {
		t.Fatalf("Load of a changed file: %v", err)
	}
}

// Two catalogs naming the same release key keep separate files.
func TestUpdateSameKeyTwice(t *testing.T) {
	f := newFakeServer(t)
	cfg := func(name, file string) []byte {
		return []byte(`{"staticPath":"/firmware","device":[{"name":"` + name + `","type":"esp32","firmware":[{"role":"repeater","github":{"type":"repeater","files":{"flash-update":"` + file + `"}}}]}]}`)
	}
	rel := func(file string) []byte {
		return []byte(`[{"version":"v1","type":"repeater","files":[{"name":"` + file + `","url":"/releases/download/repeater-v1/` + file + `"}]}]`)
	}
	f.set("/mc/config.json", fakeResp{body: cfg("A", "a"), ctype: "application/json"})
	f.set("/mc/releases?repo=github", fakeResp{body: rel("a.bin"), ctype: "application/json"})
	f.set("/zc/config-zephcore.json", fakeResp{body: cfg("B", "b"), ctype: "application/json"})
	f.set("/zc/releases?repo=github", fakeResp{body: rel("b.bin"), ctype: "application/json"})
	c, _, dir := newTestClient(t, f)
	cat, err := c.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "catalog", "zephcore-releases-github.json")); err != nil {
		t.Fatal(err)
	}
	loaded, err := c.Load()
	if err != nil || !reflect.DeepEqual(loaded, cat) || len(cat.Devices) != 2 {
		t.Fatalf("%v %+v", err, cat)
	}
	if u := cat.Devices[1].Firmware[0].Versions[0].Files[0]; u.Fallback != "" || !strings.HasPrefix(u.URL, f.sites().ZephCore) {
		t.Errorf("ZephCore file %+v", u)
	}
}

// A redirect may not drop from https to http.
func TestCheckRedirect(t *testing.T) {
	req := func(s string) *http.Request {
		u, _ := url.Parse(s)
		return &http.Request{URL: u}
	}
	if err := checkRedirect(req("http://x/a"), []*http.Request{req("https://github.com/a")}); err == nil {
		t.Error("https to http followed")
	}
	if err := checkRedirect(req("https://release-assets.githubusercontent.com/a"), []*http.Request{req("https://github.com/a")}); err != nil {
		t.Error(err)
	}
	many := make([]*http.Request, 10)
	for i := range many {
		many[i] = req("https://x/")
	}
	if err := checkRedirect(req("https://x/"), many); err == nil {
		t.Error("11th redirect followed")
	}
}

// Load while an Update swaps the catalog folder gives the old catalog or the new one, never a false ErrDamaged (it
// did, reading a file the swap had just moved away).
func TestLoadDuringUpdate(t *testing.T) {
	f := newFakeServer(t)
	serveSmallCatalog(f)
	c, _, _ := newTestClient(t, f)
	if _, err := c.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	errs := hammer(t, func() error { _, err := c.Load(); return err }, func(int) {
		if _, err := c.Update(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if errs != nil {
		t.Fatalf("Load during Update: %d failures, first %v", len(errs), errs[0])
	}
}

// hammer runs read in three goroutines while write runs 30 times, and gives the errors read returned.
func hammer(t *testing.T, read func() error, write func(i int)) []error {
	t.Helper()
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	stop := make(chan struct{})
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := read(); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < 30; i++ {
		write(i)
	}
	close(stop)
	wg.Wait()
	return errs
}
