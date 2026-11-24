package catalog

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The goldens are the web flasher's own js/catalog.js and js/flash.js (45a5257) run in Node over the committed
// inputs (reference/catalog, `make record`). Each case is one catalog: the live MeshCore one with its two release
// lists, the live ZephCore one, and the git catalog at 45a5257 (with the broken "LilyGo T-Lora Pager" stub).
type goldenCase struct {
	name     string
	golden   string
	config   string            // request → testdata input
	releases map[string]string // source key → testdata input
	source   string
}

var goldenCases = []goldenCase{
	{
		name: "meshcore", golden: "golden-meshcore.json.gz", source: SourceMeshCore,
		config: "meshcore-config.json.gz",
		releases: map[string]string{
			"github":        "meshcore-releases-github.json.gz",
			"github-ripple": "meshcore-releases-github-ripple.json.gz",
		},
	},
	{
		name: "zephcore", golden: "golden-zephcore.json.gz", source: SourceZephCore,
		config:   "zephcore-config.json.gz",
		releases: map[string]string{"github-zephcore": "zephcore-releases-github-zephcore.json.gz"},
	},
	{
		name: "git", golden: "golden-git.json.gz", source: SourceMeshCore,
		config: "git-config.json.gz",
		releases: map[string]string{
			"github":        "meshcore-releases-github.json.gz",
			"github-ripple": "meshcore-releases-github-ripple.json.gz",
		},
	},
	// A small hand-written catalog (reference/catalog/edges) of what the live ones don't show: patterns that are a
	// literal between ^ and $ with decoy names around them, integer-like version names, pattern key order, a version
	// two releases share, untyped releases, a file without a name, a source without file patterns.
	{
		name: "edges", golden: "golden-edges.json.gz", source: SourceMeshCore,
		config:   "edges-config.json.gz",
		releases: map[string]string{"github": "edges-releases-github.json.gz"},
	},
}

func (gc goldenCase) configRequest() string {
	if gc.source == SourceZephCore {
		return "/config-zephcore.json"
	}
	return "/config.json"
}

func (gc goldenCase) raw(t testing.TB) Raw {
	r := Raw{Config: readGz(t, gc.config), Releases: map[string][]byte{}}
	for k, f := range gc.releases {
		r.Releases[k] = readGz(t, f)
	}
	return r
}

type golden struct {
	Host     string            `json:"host"`
	Inputs   map[string]string `json:"inputs"`
	Requests []string          `json:"requests"`
	Resolved json.RawMessage   `json:"resolved"`
	Firmware []goldenFirmware  `json:"firmware"`
}

type goldenFirmware struct {
	Device     int             `json:"device"`
	DeviceName string          `json:"deviceName"`
	DeviceType string          `json:"deviceType"`
	Firmware   int             `json:"firmware"`
	Role       string          `json:"role"`
	Title      string          `json:"title"`
	SubTitle   string          `json:"subTitle"`
	Notice     string          `json:"notice"`
	Versions   []goldenVersion `json:"versions"`
}

type goldenVersion struct {
	Version string      `json:"version"`
	Update  *goldenPick `json:"update"`
	Wipe    *goldenPick `json:"wipe"`
	Flash   *goldenPick `json:"flash"`
}

// goldenPick is what pickFlashFile returned, and esp32Address for it on an ESP32.
type goldenPick struct {
	Type    string  `json:"type"`
	Name    string  `json:"name"`
	Title   string  `json:"title"`
	Address *uint32 `json:"address"`
}

func readGz(t testing.TB, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return data
}

// resolvedCase is one golden case's inputs resolved once, shared by the tests, which only read it: the tree
// js/catalog.js would print and the devices built from it.
type resolvedCase struct {
	once sync.Once
	tree *jsValue
	devs []Device
	err  error
}

var resolvedCases = map[string]*resolvedCase{"meshcore": {}, "zephcore": {}, "git": {}, "edges": {}}

func (gc goldenCase) resolved(t testing.TB) (*jsValue, []Device) {
	t.Helper()
	rc := resolvedCases[gc.name]
	rc.once.Do(func() {
		if rc.tree, rc.err = resolveRaw(gc.raw(t)); rc.err == nil {
			site := DefaultSites.MeshCore
			if gc.source == SourceZephCore {
				site = DefaultSites.ZephCore
			}
			rc.devs, _ = buildDevices(rc.tree, gc.source, site, DefaultSites.GitHub)
		}
	})
	if rc.err != nil {
		t.Fatal(rc.err)
	}
	return rc.tree, rc.devs
}

var goldens sync.Map // golden file → golden, parsed once

func loadGolden(t testing.TB, gc goldenCase) golden {
	t.Helper()
	if g, ok := goldens.Load(gc.golden); ok {
		return g.(golden)
	}
	var g golden
	if err := json.Unmarshal(readGz(t, gc.golden), &g); err != nil {
		t.Fatalf("%s: %v", gc.golden, err)
	}
	goldens.Store(gc.golden, g)
	return g
}

// TestGoldenResolution: Go resolves the same inputs to exactly what js/catalog.js loadCatalog produced: every
// device, in order, every field, every version in order with its notes, every file with its name and title.
func TestGoldenResolution(t *testing.T) {
	t.Parallel() // heavy, independent: the shared catalogs are resolved once and only read
	for _, gc := range goldenCases {
		t.Run(gc.name, func(t *testing.T) {
			g := loadGolden(t, gc)
			raw := gc.raw(t)

			// The golden was recorded from exactly these inputs.
			inputs := map[string][]byte{gc.configRequest(): raw.Config}
			for k, b := range raw.Releases {
				inputs["/releases?repo="+k] = b
			}
			if len(inputs) != len(g.Inputs) {
				t.Fatalf("inputs: golden has %d, test has %d", len(g.Inputs), len(inputs))
			}
			for req, body := range inputs {
				sum := sha256.Sum256(body)
				if got := hex.EncodeToString(sum[:]); got != g.Inputs[req] {
					t.Fatalf("input %s: sha256 %s, golden was recorded from %s (run make record)", req, got, g.Inputs[req])
				}
			}

			cfg, err := parseJS(raw.Config)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := releaseKeys(cfg)
			if err != nil {
				t.Fatal(err)
			}
			requests := []string{gc.configRequest()}
			for _, k := range keys {
				requests = append(requests, "/releases?repo="+k)
			}
			if fmt.Sprint(requests) != fmt.Sprint(g.Requests) {
				t.Errorf("requests %v, reference made %v", requests, g.Requests)
			}

			cfg, _ = gc.resolved(t)
			want, err := parseJS(g.Resolved)
			if err != nil {
				t.Fatal(err)
			}
			if err := equalJS(cfg, want, "config"); err != nil {
				t.Fatal(err)
			}
			devs := cfg.field("device").arr
			files := 0
			for _, d := range devs {
				for _, fw := range d.field("firmware").arr {
					vs := fw.field("version").obj
					for _, name := range vs.ordered() {
						files += len(vs.get(name).field("files").arr)
					}
				}
			}
			t.Logf("%s: %d devices, %d files, identical to js/catalog.js", gc.name, len(devs), files)
		})
	}
}

// equalJS compares two JSON trees with key order: what JSON.stringify of the reference's object holds.
func equalJS(got, want *jsValue, path string) error {
	if got == nil || want == nil {
		if got != want {
			return fmt.Errorf("%s: got %v, want %v", path, got == nil, want == nil)
		}
		return nil
	}
	if got.kind != want.kind {
		return fmt.Errorf("%s: kind %d, want %d", path, got.kind, want.kind)
	}
	switch got.kind {
	case kBool:
		if got.b != want.b {
			return fmt.Errorf("%s: %v, want %v", path, got.b, want.b)
		}
	case kNumber:
		a, _ := strconv.ParseFloat(got.s, 64)
		b, _ := strconv.ParseFloat(want.s, 64)
		if a != b {
			return fmt.Errorf("%s: %s, want %s", path, got.s, want.s)
		}
	case kString:
		if got.s != want.s {
			return fmt.Errorf("%s: %q, want %q", path, got.s, want.s)
		}
	case kArray:
		if len(got.arr) != len(want.arr) {
			return fmt.Errorf("%s: %d items, want %d", path, len(got.arr), len(want.arr))
		}
		for i := range got.arr {
			if err := equalJS(got.arr[i], want.arr[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case kObject:
		gk, wk := got.obj.ordered(), want.obj.ordered()
		if strings.Join(gk, "\x00") != strings.Join(wk, "\x00") {
			return fmt.Errorf("%s: keys %q, want %q", path, gk, wk)
		}
		for _, k := range gk {
			if err := equalJS(got.obj.get(k), want.obj.get(k), path+"."+k); err != nil {
				return err
			}
		}
	}
	return nil
}

// TestGoldenChoices: for every version the reference offers, Choose picks the very file pickFlashFile picks, at the
// address esp32Address gives, and erases the chip exactly when the "Erase device" box would be ticked. The only
// differences are the refusals SPEC.md 6.2 asks for, listed here one by one.
func TestGoldenChoices(t *testing.T) {
	t.Parallel() // heavy, independent: the shared catalogs are resolved once and only read
	// source/device/role/kind the reference would flash and meshflash refuses.
	wantRefused := map[string][]string{
		"meshcore": nil,
		"zephcore": nil,
		"git": {
			// The broken merge stub: an esp32 entry holding the T-Echo Card's nRF52 .zip files. The reference writes
			// the .zip at 0x10000, after a chip erase when the box is ticked (docs/meshcore-flashing.md 3.10).
			"LilyGo T-Lora Pager/kissRadio/update",
			"LilyGo T-Lora Pager/kissRadio/wipe",
			"LilyGo T-Lora Pager/roomServer/update",
			"LilyGo T-Lora Pager/roomServer/wipe",
			// Git's inline Ripple build for the Wio Tracker L1 Pro lists its nRF52 .zip as flash-update; meshflash
			// takes only "flash" on nRF52 (live moved it to github-ripple with "flash").
			"Seeed Studio Wio Tracker L1 Pro/gui/flash",
		},
		"edges": {
			// Only flash-update files: the reference's wipe falls back to the first one and erases the chip under it.
			"Weird/repeater/wipe",
		},
	}
	for _, gc := range goldenCases {
		t.Run(gc.name, func(t *testing.T) {
			g := loadGolden(t, gc)
			_, devs := gc.resolved(t)
			refused := map[string]bool{}
			checked := 0
			for _, gf := range g.Firmware {
				if gf.Device >= len(devs) {
					t.Fatalf("golden device %d, Go has %d", gf.Device, len(devs))
				}
				d := devs[gf.Device]
				if d.Name != gf.DeviceName || string(d.Type) != gf.DeviceType {
					t.Fatalf("device %d: %s %s, reference %s %s", gf.Device, d.Name, d.Type, gf.DeviceName, gf.DeviceType)
				}
				fw := firmwareAt(d, gf.Firmware)
				if fw == nil {
					t.Fatalf("%s firmware %d missing", d.Name, gf.Firmware)
				}
				where := d.Name + "/" + fw.Role
				if fw.Role != gf.Role || fw.Title != gf.Title || fw.SubTitle != gf.SubTitle {
					t.Errorf("%s: role %q title %q/%q, reference %q %q/%q", where, fw.Role, fw.Title, fw.SubTitle,
						gf.Role, gf.Title, gf.SubTitle)
				}
				if text := noticeText(gf.Notice); text != "" && (len(fw.Notices) == 0 || fw.Notices[0] != text) {
					t.Errorf("%s: notices %q, reference's renderNotice gives %q", where, fw.Notices, text)
				}
				if len(fw.Versions) != len(gf.Versions) {
					t.Fatalf("%s: %d versions, reference %d", where, len(fw.Versions), len(gf.Versions))
				}
				for i, gv := range gf.Versions {
					v := fw.Versions[i]
					if v.Name != gv.Version {
						t.Fatalf("%s: version %d is %s, reference %s", where, i, v.Name, gv.Version)
					}
					try := func(kind Kind, label string, ref *goldenPick, erase bool) {
						checked++
						got, err := Choose(d, v, kind)
						if err != nil {
							if !errors.Is(err, ErrRefused) {
								t.Errorf("%s %s %s: error %v doesn't wrap ErrRefused", where, v.Name, label, err)
							}
							if ref != nil {
								refused[where+"/"+label] = true
							}
							return
						}
						if ref == nil {
							t.Errorf("%s %s %s: chose %s, the reference has no file", where, v.Name, label, got.File.Name)
							return
						}
						if got.File.Type != ref.Type || got.File.Name != ref.Name || got.File.Title != ref.Title {
							t.Errorf("%s %s %s: chose %s %s, reference %s %s", where, v.Name, label, got.File.Type,
								got.File.Name, ref.Type, ref.Name)
						}
						if ref.Address != nil && got.Address != *ref.Address {
							t.Errorf("%s %s %s: address %#x, reference %#x", where, v.Name, label, got.Address, *ref.Address)
						}
						if got.EraseChip != erase {
							t.Errorf("%s %s %s: EraseChip %v, reference eraseAll %v", where, v.Name, label, got.EraseChip, erase)
						}
					}
					switch d.Type {
					case ESP32:
						try(Update, "update", gv.Update, false)
						try(Wipe, "wipe", gv.Wipe, true)
					case NRF52:
						try(Update, "flash", gv.Flash, false)
						if _, err := Choose(d, v, Wipe); !errors.Is(err, ErrRefused) {
							t.Errorf("%s %s: nRF52 wipe not refused (%v)", where, v.Name, err)
						}
					default:
						if _, err := Choose(d, v, Update); !errors.Is(err, ErrRefused) {
							t.Errorf("%s %s: %s board not refused (%v)", where, v.Name, d.Type, err)
						}
					}
				}
			}
			var got []string
			for k := range refused {
				got = append(got, k)
			}
			sort.Strings(got)
			want := append([]string(nil), wantRefused[gc.name]...)
			sort.Strings(want)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("refused where the reference flashes:\n got %q\nwant %q", got, want)
			}
			t.Logf("%s: %d choices compared with pickFlashFile/esp32Address, %d refused as listed", gc.name, checked,
				len(got))
		})
	}
}

func firmwareAt(d Device, index int) *Firmware {
	for i := range d.Firmware {
		if d.Firmware[i].index == index {
			return &d.Firmware[i]
		}
	}
	return nil
}

// TestGoldenModelFiles: every model file carries the resolved name and title, and its URL is firmwareUrl on its
// catalog's site (ZephCore's files on ZephCore's site); exactly MeshCore's own release files have a GitHub fallback.
func TestGoldenModelFiles(t *testing.T) {
	t.Parallel() // heavy, independent: the shared catalogs are resolved once and only read
	for _, gc := range goldenCases {
		t.Run(gc.name, func(t *testing.T) {
			site := DefaultSites.MeshCore
			if gc.source == SourceZephCore {
				site = DefaultSites.ZephCore
			}
			_, devs := gc.resolved(t)
			var fallback, inline int
			for _, d := range devs {
				for _, fw := range d.Firmware {
					for _, v := range fw.Versions {
						for _, f := range v.Files {
							if f.Source != gc.source {
								t.Fatalf("%s: source %s", f.Name, f.Source)
							}
							if strings.HasPrefix(f.Name, "/") {
								if f.URL != site+f.Name {
									t.Fatalf("%s: URL %s", f.Name, f.URL)
								}
							} else {
								inline++
								if f.URL != site+"/firmware/"+f.Name {
									t.Fatalf("%s: URL %s", f.Name, f.URL)
								}
							}
							if gc.source == SourceMeshCore && strings.HasSuffix(f.Name, "?repo=github") {
								fallback++
								want := "https://github.com/meshcore-dev/MeshCore" + strings.TrimSuffix(f.Name, "?repo=github")
								if f.Fallback != want {
									t.Fatalf("%s: fallback %q, want %q", f.Name, f.Fallback, want)
								}
							} else if f.Fallback != "" {
								t.Fatalf("%s: fallback %q for a file that isn't MeshCore's own release", f.Name, f.Fallback)
							}
							if f.Base() == "" || checkName(f.Base()) != nil {
								t.Fatalf("%s: unsafe base %q", f.Name, f.Base())
							}
						}
					}
				}
			}
			if gc.source == SourceMeshCore && (fallback == 0 || inline == 0) {
				t.Fatalf("fallback %d, inline %d", fallback, inline)
			}
		})
	}
}

// The resolved catalog prints the same whichever way round its trees are compared (equalJS is symmetric), and the
// golden file is small enough to commit.
func TestTestdataSmall(t *testing.T) {
	ents, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 200<<10 {
			t.Errorf("testdata/%s is %d bytes (over 200 KB)", e.Name(), info.Size())
		}
		total += info.Size()
	}
	if total > 1<<20 {
		t.Errorf("testdata is %d bytes (over 1 MB)", total)
	}
	if !bytes.Equal(readGz(t, "zephcore-config.json.gz")[:1], []byte("{")) {
		t.Error("zephcore config doesn't look like JSON")
	}
}
