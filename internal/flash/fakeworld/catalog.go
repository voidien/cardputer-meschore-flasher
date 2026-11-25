package fakeworld

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"

	"meshflash/internal/catalog"
	"meshflash/internal/esp"
	"meshflash/internal/usbid"
)

// The committed catalog: byte-for-byte copies of internal/catalog/testdata's saved live catalogs (TestCatalogCopies
// checks), embedded so the demo binary carries them and the sites can serve them with no file on disk.
//
//go:embed catalog/*.json.gz
var catalogFiles embed.FS

// The five files the sites serve, by the path (and query) internal/catalog asks for them under.
var catalogRoutes = []struct{ path, file string }{
	{"/mc/config.json", "meshcore-config.json.gz"},
	{"/mc/releases?repo=github", "meshcore-releases-github.json.gz"},
	{"/mc/releases?repo=github-ripple", "meshcore-releases-github-ripple.json.gz"},
	{"/zc/config-zephcore.json", "zephcore-config.json.gz"},
	{"/zc/releases?repo=github-zephcore", "zephcore-releases-github-zephcore.json.gz"},
}

// fileInfo is what the sites know about one firmware file the catalog lists: enough to make it up.
type fileInfo struct {
	base    string // its saved name (catalog.File.Base)
	typ     string // flash-update, flash-wipe, flash, download
	device  catalog.Device
	role    string // the catalog's role key (repeater, roomServer, companionBle, …)
	source  string // catalog.SourceMeshCore or SourceZephCore
	version string // the catalog's version name ("v1.18.0", "1.18.2")
}

// committed is the committed catalog resolved once per process with Sites, and every firmware file it lists by the
// path (with its query) the sites serve it under, the GitHub fallback's too. Resolving takes about a second under
// -race on a PC, so every world shares it, read-only.
var committed struct {
	once  sync.Once
	cat   *catalog.Catalog
	files map[string]*fileInfo // URL path + "?" + query → the file
	raw   map[string][]byte    // catalog route → its JSON, gunzipped
	err   error
}

func loadCommitted() error {
	committed.once.Do(func() {
		committed.raw = map[string][]byte{}
		for _, r := range catalogRoutes {
			b, err := gunzipFile(r.file)
			if err != nil {
				committed.err = err
				return
			}
			committed.raw[r.path] = b
		}
		raw := committed.raw
		mc := catalog.Raw{Config: raw["/mc/config.json"], Releases: map[string][]byte{
			"github":        raw["/mc/releases?repo=github"],
			"github-ripple": raw["/mc/releases?repo=github-ripple"],
		}}
		zc := catalog.Raw{Config: raw["/zc/config-zephcore.json"], Releases: map[string][]byte{
			"github-zephcore": raw["/zc/releases?repo=github-zephcore"],
		}}
		cat, err := catalog.Resolve(mc, zc, Sites)
		if err != nil {
			committed.err = fmt.Errorf("fakeworld: the committed catalog doesn't resolve: %w", err)
			return
		}
		committed.cat = cat
		committed.files = map[string]*fileInfo{}
		for _, d := range cat.Devices {
			for _, fw := range d.Firmware {
				for _, v := range fw.Versions {
					for _, f := range v.Files {
						fi := &fileInfo{base: f.Base(), typ: f.Type, device: d, role: fw.Role, source: fw.Source,
							version: v.Name}
						for _, u := range []string{f.URL, f.Fallback} {
							if k := routeKey(u); k != "" {
								if _, dup := committed.files[k]; !dup {
									committed.files[k] = fi
								}
							}
						}
					}
				}
			}
		}
	})
	return committed.err
}

// Catalog is the committed catalog (the five files the sites serve), resolved once per process with Sites: the
// catalog a Deps with withCatalog loads, for tests that need its boards and versions without loading it again. It is
// shared: never change it.
func Catalog() (*catalog.Catalog, error) {
	if err := loadCommitted(); err != nil {
		return nil, err
	}
	return committed.cat, nil
}

// routeKey is a URL's path plus "?" and its query: what the sites look a request up by, whatever the host.
func routeKey(u string) string {
	if u == "" {
		return ""
	}
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	k := p.EscapedPath()
	if p.RawQuery != "" {
		k += "?" + p.RawQuery
	}
	return k
}

func gunzipFile(name string) ([]byte, error) {
	z, err := catalogFiles.ReadFile("catalog/" + name)
	if err != nil {
		return nil, err
	}
	r, err := gzip.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, fmt.Errorf("fakeworld: %s: %w", name, err)
	}
	return io.ReadAll(r)
}

// chipOf is the ESP32 chip of a catalog board, for the image header of the files made up for it: the USB table's
// (usbid.DeviceChip), the ESP32-C6 for the two ZephCore-only C6 boards (not in the table), else the classic ESP32.
func chipOf(d catalog.Device) esp.Chip {
	if c, ok := usbid.DeviceChip(d.Name); ok {
		if ec, ok := esp.ParseChip(string(c)); ok {
			return ec
		}
	}
	if strings.Contains(d.Name, "C6") {
		return esp.ESP32C6
	}
	return esp.ESP32
}
