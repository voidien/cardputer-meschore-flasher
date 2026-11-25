package ui

// What the screens read from the catalog (docs/phase2-design.md 9): how a role is named, the newest version, which
// of a version's files is saved, and which boards a saved file belongs to. Pure functions over catalog types: no
// I/O.

import (
	"errors"
	"strings"

	"meshflash/internal/catalog"
	"meshflash/internal/nodecli"
)

// roleName names a role as the catalog does: its title, plus " · " and its subtitle when it has one ("Companion ·
// Bluetooth"); never by its key (design 8.3).
func roleName(f catalog.Firmware) string {
	if f.SubTitle != "" {
		return f.Title + " · " + f.SubTitle
	}
	return f.Title
}

// devName, fwName and verName are the picked board, role and version, "" until picked.
func (m *Model) devName() string {
	if m.flow.dev == nil {
		return ""
	}
	return m.flow.dev.Name
}

func (m *Model) fwName() string {
	if m.flow.fw == nil {
		return ""
	}
	return roleName(*m.flow.fw)
}

func (m *Model) verName() string {
	if m.flow.ver == nil {
		return ""
	}
	return m.flow.ver.Name
}

// newest is the newest MeshCore community version in the catalog ("v1.18.0"), "" when none parses: the version the
// Board's catalog line and the update's summary name (ZephCore's and Ripple's numbers count differently).
func newest(c *catalog.Catalog) string {
	var best nodecli.Version
	found := false
	for _, d := range c.Devices {
		for _, f := range d.Firmware {
			if f.Source != catalog.SourceMeshCore || f.Class != "community" {
				continue
			}
			for _, v := range f.Versions {
				pv, err := nodecli.ParseVersion(v.Name)
				if err != nil {
					continue
				}
				if !found || !best.AtLeast(pv.Major, pv.Minor, pv.Patch) {
					best, found = pv, true
				}
			}
		}
	}
	if !found {
		return ""
	}
	return best.String()
}

// catalogViews caches, for one catalog, its newest version and its files by name.
type catalogViews struct {
	cat    *catalog.Catalog
	newest string
	files  map[string][]fileOwner
	byName map[string]int // the boards by name as catalog.Catalog.Find matches them (case and spaces ignored)
}

// fileOwner is a catalog file, and the board, role and version that list it.
type fileOwner struct {
	file catalog.File
	owner
}

// views is the cache for the catalog showing, made afresh when the catalog changes: the newest version at once, the
// file index the first time Saved files needs it.
func (m *Model) views() *catalogViews {
	v := &m.more.views
	if v.cat != m.cat || m.cat == nil {
		*v = catalogViews{cat: m.cat}
		if m.cat != nil {
			v.newest = newest(m.cat)
			v.byName = map[string]int{}
			for i := len(m.cat.Devices) - 1; i >= 0; i-- { // the first of a name wins, as in Find
				v.byName[strings.ToLower(strings.TrimSpace(m.cat.Devices[i].Name))] = i
			}
		}
	}
	return v
}

// find is catalog.Catalog.Find through the cache: the board with this name, ignoring case and surrounding spaces.
func (m *Model) find(name string) *catalog.Device {
	if m.cat == nil {
		return nil
	}
	if i, ok := m.views().byName[strings.ToLower(strings.TrimSpace(name))]; ok {
		return &m.cat.Devices[i]
	}
	return nil
}

// catalogLine is the Board's last line: the catalog's age and newest version, or why there is none.
func (m *Model) catalogLine() string {
	switch {
	case m.cat != nil:
		s := "Catalog from " + day(m.cat.Fetched, m.now())
		if n := m.views().newest; n != "" {
			s += ": newest " + n
		}
		return s
	case m.catErr == nil && !m.more.catRead:
		return "Reading the saved catalog…"
	case errors.Is(m.catErr, catalog.ErrDamaged):
		return "The saved catalog is damaged. Join Wi-Fi and press u."
	case m.catErr == nil || errors.Is(m.catErr, catalog.ErrNoCatalog):
		return "No catalog saved. Join Wi-Fi and press u."
	}
	return "Can't read the saved catalog: " + plainErr(m.catErr)
}

// fileOfType is a version's first file of a type, as catalog.Choose picks it.
func fileOfType(v catalog.Version, t string) (catalog.File, bool) {
	for _, f := range v.Files {
		if f.Type == t {
			return f, true
		}
	}
	return catalog.File{}, false
}

// flashFiles are the files of a version meshflash would flash: an ESP32's update and full-wipe images, an nRF52's
// DFU package. Never a download-only file, and never an erase or bootloader file (they aren't a version's files of
// these types; Choose refuses them anyway).
func flashFiles(d *catalog.Device, v catalog.Version) []catalog.File {
	if d == nil {
		return nil
	}
	var out []catalog.File
	types := []string{"flash"}
	if d.Type == catalog.ESP32 {
		types = []string{"flash-update", "flash-wipe"}
	}
	for _, t := range types {
		if f, ok := fileOfType(v, t); ok {
			out = append(out, f)
		}
	}
	return out
}

// savedEntry is the saved file that is f: the same name, from its URL or its GitHub fallback (as
// catalog.Client.Saved matches it, without touching the disk).
func savedEntry(saved []catalog.Entry, f catalog.File) (catalog.Entry, bool) {
	base := f.Base()
	for _, e := range saved {
		if e.Name == base && (e.URL == f.URL || (f.Fallback != "" && e.URL == f.Fallback)) {
			return e, true
		}
	}
	return catalog.Entry{}, false
}

// savedNote is what a Version row says of its files (owner question 2): "saved · 361 KB" for an nRF52's package; for
// an ESP32's two images which one is saved, "update saved · 361 KB", "wipe saved · 1.3 MB", or "both saved"; ""
// when none is.
func savedNote(d *catalog.Device, v catalog.Version, saved []catalog.Entry) string {
	if d == nil {
		return ""
	}
	if d.Type != catalog.ESP32 {
		for _, f := range flashFiles(d, v) {
			if e, ok := savedEntry(saved, f); ok {
				return "saved · " + byteSize(e.Size)
			}
		}
		return ""
	}
	var upd, wipe catalog.Entry
	var hasUpd, hasWipe bool
	if f, ok := fileOfType(v, "flash-update"); ok {
		upd, hasUpd = savedEntry(saved, f)
	}
	if f, ok := fileOfType(v, "flash-wipe"); ok {
		wipe, hasWipe = savedEntry(saved, f)
	}
	switch {
	case hasUpd && hasWipe:
		return "both saved"
	case hasUpd:
		return "update saved · " + byteSize(upd.Size)
	case hasWipe:
		return "wipe saved · " + byteSize(wipe.Size)
	}
	return ""
}

// owner is a board, role and version a saved file belongs to.
type owner struct {
	dev string
	fw  string
	ver string
}

// fileIndex is every file of the catalog by the name it is saved as, with the board, role and version listing it, in
// catalog order.
func fileIndex(c *catalog.Catalog) map[string][]fileOwner {
	idx := map[string][]fileOwner{}
	for _, d := range c.Devices {
		for _, f := range d.Firmware {
			for _, v := range f.Versions {
				for _, file := range v.Files {
					base := file.Base()
					idx[base] = append(idx[base], fileOwner{file: file,
						owner: owner{dev: d.Name, fw: roleName(f), ver: v.Name}})
				}
			}
		}
	}
	return idx
}

// owners are the catalog boards whose version lists a saved file (the same name, from its URL or GitHub fallback),
// in catalog order: Saved files shows the first, and how many more.
func (m *Model) owners(e catalog.Entry) []owner {
	if m.cat == nil {
		return nil
	}
	v := m.views()
	if v.files == nil {
		v.files = fileIndex(m.cat)
	}
	var out []owner
	for _, fo := range v.files[e.Name] {
		if e.URL == fo.file.URL || (fo.file.Fallback != "" && e.URL == fo.file.Fallback) {
			out = append(out, fo.owner)
		}
	}
	return dedupeOwners(out)
}

// dedupeOwners keeps the first of each board: a file listed by two roles of one board is one board's.
func dedupeOwners(in []owner) []owner {
	seen := map[string]bool{}
	var out []owner
	for _, o := range in {
		if !seen[o.dev] {
			seen[o.dev] = true
			out = append(out, o)
		}
	}
	return out
}

// matches says a board's name holds the find box's text, ignoring case.
func matches(name, filter string) bool {
	return filter == "" || strings.Contains(strings.ToLower(name), strings.ToLower(strings.TrimSpace(filter)))
}
