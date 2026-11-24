package catalog

// Ported in part from the MeshCore web flasher, flasher.meshcore.dev @ 45a5257, js/catalog.js (firmwareUrl, in
// firmwarePath); the role and notice lookups the model is built with (roleValue, renderNotice) are in resolve.go.
// MIT License, Copyright (c) 2025 Rastislav Vysoky (see THIRD_PARTY_NOTICES).

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Sources: which catalog a board or firmware came from.
const (
	SourceMeshCore = "meshcore" // MeshCore's official catalog on flasher.meshcore.io
	SourceZephCore = "zephcore" // ZephCore's catalog on zephcore.meshcore.dev, a third-party port
)

// DeviceType is the board's chip family, which picks the flashing protocol.
type DeviceType string

// The device types the catalog uses.
const (
	ESP32   DeviceType = "esp32"   // ROM bootloader over SLIP (internal/esp)
	NRF52   DeviceType = "nrf52"   // Adafruit serial DFU (internal/nrfdfu)
	NoFlash DeviceType = "noflash" // RP2040/RP2350: a .uf2 copied onto a USB drive, which meshflash doesn't do
)

// SoftDevice FWIDs the nRF52 boards in the catalog use, as a DFU init packet's sd_req names them.
const (
	SoftDeviceS140v6 uint16 = 0x00B6 // S140 6.1.1
	SoftDeviceS140v7 uint16 = 0x0123 // S140 7.3.0
)

// Catalog is every board meshflash can offer, resolved from the saved catalogs.
type Catalog struct {
	// Devices: MeshCore's boards in its catalog's order, with ZephCore's firmware added to the boards both catalogs
	// name, then the boards only ZephCore has. Boards with no firmware version are left out, as the web flasher
	// leaves them out.
	Devices []Device
	// Fetched is when Update fetched it (zero for a catalog that was never saved).
	Fetched time.Time
	// Size is the bytes of the catalog files as saved; Downloaded is what the Update that saved them received over
	// the network (less when the server compressed them).
	Size, Downloaded int64
}

// Device is one board.
type Device struct {
	Name  string     // as the catalog names it: "Heltec v3", "Seeed Studio Xiao nRF52 WIO"
	Maker string     // the catalog's maker key (not always in its maker table; nothing depends on it)
	Type  DeviceType // which protocol flashes it
	// Source is where the board came from: SourceMeshCore, or SourceZephCore for a board only ZephCore lists.
	Source string
	// Firmware: the entries with at least one version, MeshCore's first, in catalog order.
	Firmware []Firmware
	// Notices: every distinct notice of the board, as plain text with no links: first the warnings nested in a
	// release source, such as the T1000-E's "do not flash the LoRaWAN version" warning that the web flasher never
	// shows (docs/meshcore-flashing.md 3.10), then its firmware entries' own notices, in catalog order.
	Notices []string
	// SoftDevice is the nRF52 SoftDevice FWID the catalog implies with its "erase" file's name (…softdevice_v6 is
	// 0x00B6, …_v7 is 0x0123); 0 when it doesn't say. The erase file itself is never offered.
	SoftDevice uint16

	// warnings are the notices nested in the release sources of any of the board's entries (shown or hidden): they
	// are about the board, so every firmware entry of the board carries them.
	warnings []string

	// neverWrite: every "erase" and "bootloader" file name either catalog lists, for any board, which Choose refuses
	// even if a version lists one (docs/meshcore-flashing.md 3.9; CLAUDE.md: never write anything the catalog lists
	// besides the firmware). One set, shared by every board of the Catalog and never changed once built.
	neverWrite map[string]bool
}

// Flashable says whether meshflash can write firmware to the board at all: ESP32 and nRF52 boards. A noflash board
// is listed so the screen can say "download only, not supported".
func (d *Device) Flashable() bool { return d.Type == ESP32 || d.Type == NRF52 }

// Firmware is one role a board can run, with its versions.
type Firmware struct {
	// Source is the catalog it came from: SourceMeshCore or SourceZephCore.
	Source string
	// Class is the catalog's grouping: "community" (open source), or "ripple" (Ripple Radios' freemium builds).
	Class string
	// ClosedSource marks Ripple's builds: closed source, third-party.
	ClosedSource bool
	// Role is the role key (companionBle, companionUsb, repeater, roomServer, kissRadio, gui, guiSD; ZephCore's
	// companion, repeater). Key behaviour on it, never on the title: titles carry variants such as "Repeater - 30dBm".
	Role string
	// Title and SubTitle are what the web flasher shows: the entry's own, else its role's.
	Title, SubTitle string
	// Notices: this entry's own notice, then the board's warnings nested in a release source (any entry's, from
	// either catalog), as plain text. So the T1000-E's LoRaWAN warning, which the catalog nests in its companionBle
	// entry only, is on every T1000-E role, ZephCore's too (docs/meshcore-flashing.md 3.10).
	Notices []string
	// Versions with at least one file, newest first for release versions, in catalog order for inline ones.
	Versions []Version

	index int // its place in the device's firmware list in the catalog, hidden entries counted
}

// Version is one release of a firmware.
type Version struct {
	Name  string // "v1.18.0", "1.17.1", "v12.3", "1.18.2"
	Notes string // the release notes as plain text (line ends tidied), often just a link
	Files []File // in the catalog's order: pattern order, then the release's file order
}

// File is one file of a version, as the catalog lists it.
type File struct {
	// Type is flash-update (ESP32 app at 0x10000), flash-wipe (ESP32 merged image at 0x0), flash (nRF52 DFU .zip)
	// or download (a .uf2, never flashed).
	Type string
	// Name is the catalog's: a release file's "/releases/download/<tag>/<file>?repo=<key>", or an inline file's name
	// under the catalog's static path.
	Name string
	// Title is what the web flasher shows: a release file's name, or an inline file's description.
	Title string
	// URL is where it is downloaded from (firmwareUrl on its catalog's site).
	URL string
	// Fallback is GitHub's address for the same file, for MeshCore's own release files ("?repo=github") only;
	// "" for every other file.
	Fallback string
	// Source is the catalog it came from.
	Source string
}

// Base is the file's own name, the last part of its URL path as sent (still escaped, so an encoded "/" can't make it
// a path): what it is saved as. "" when the URL ends in "/" or doesn't parse.
func (f File) Base() string {
	u, err := url.Parse(f.URL)
	if err != nil {
		return ""
	}
	p := u.EscapedPath()
	return p[strings.LastIndexByte(p, '/')+1:]
}

// Sites are the servers meshflash talks to. Tests point them at httptest servers.
type Sites struct {
	MeshCore string // MeshCore's flasher: catalog, releases and files
	ZephCore string // ZephCore's flasher: its catalog, releases and files
	GitHub   string // the fallback for MeshCore's release files
}

// DefaultSites are the real servers (SPEC.md 2).
var DefaultSites = Sites{
	MeshCore: "https://flasher.meshcore.io",
	ZephCore: "https://zephcore.meshcore.dev",
	GitHub:   "https://github.com",
}

// gitHubRepo is the repository whose releases "?repo=github" serves (docs/meshcore-flashing.md 2.4).
const gitHubRepo = "/meshcore-dev/MeshCore"

// Raw is one catalog as fetched: its config file and the /releases list of each release source it names.
type Raw struct {
	Config   []byte            // config.json or config-zephcore.json, as fetched
	Releases map[string][]byte // source key ("github") → body of /releases?repo=<key>
}

// Resolve turns the MeshCore and ZephCore catalogs into the Catalog: each resolved exactly as the web flasher's
// js/catalog.js loadCatalog does, then ZephCore's boards merged into MeshCore's by name (the same name ignoring case
// and surrounding spaces, and the same chip family). An empty zephcore.Config leaves ZephCore out.
func Resolve(meshcore, zephcore Raw, sites Sites) (*Catalog, error) {
	devs, never, err := resolveSource(meshcore, SourceMeshCore, sites)
	if err != nil {
		return nil, fmt.Errorf("MeshCore catalog: %w", err)
	}
	if zephcore.Config != nil {
		zdevs, znever, err := resolveSource(zephcore, SourceZephCore, sites)
		if err != nil {
			return nil, fmt.Errorf("ZephCore catalog: %w", err)
		}
		devs = mergeZephCore(devs, zdevs)
		for n := range znever {
			never[n] = true
		}
	}
	// Both catalogs' erase and bootloader files are refused on every board: a board's version listing another
	// board's formatter (or ZephCore's, or MeshCore's) is no more firmware than its own.
	for i := range devs {
		devs[i].neverWrite = never
	}
	return &Catalog{Devices: devs}, nil
}

// resolveSource resolves one catalog into devices (no merging), with every erase and bootloader file it names.
func resolveSource(raw Raw, source string, sites Sites) ([]Device, map[string]bool, error) {
	cfg, err := resolveRaw(raw)
	if err != nil {
		return nil, nil, err
	}
	site := sites.MeshCore
	if source == SourceZephCore {
		site = sites.ZephCore
	}
	devs, never := buildDevices(cfg, source, site, sites.GitHub)
	return devs, never, nil
}

// resolveRaw parses a catalog and its release lists and resolves them as loadCatalog does, giving the tree
// JSON.stringify would print.
func resolveRaw(raw Raw) (*jsValue, error) {
	cfg, err := parseJS(raw.Config)
	if err != nil {
		return nil, err
	}
	if cfg.kind != kObject {
		return nil, fmt.Errorf("the catalog is not a JSON object")
	}
	keys, err := releaseKeys(cfg)
	if err != nil {
		return nil, err
	}
	releases := map[string]*jsValue{}
	for _, k := range keys {
		body, ok := raw.Releases[k]
		if !ok {
			return nil, fmt.Errorf("no release list for %q", k)
		}
		v, err := parseJS(body)
		if err != nil {
			return nil, fmt.Errorf("release list %q: %w", k, err)
		}
		releases[k] = v
	}
	if err := resolveTree(cfg, releases); err != nil {
		return nil, err
	}
	return cfg, nil
}

var softDeviceName = regexp.MustCompile(`(?i)softdevice_v(\d+)`)

// softDevice reads the SoftDevice FWID from an nRF52 "erase" file name.
func softDevice(erase string) uint16 {
	m := softDeviceName.FindStringSubmatch(erase)
	if m == nil {
		return 0
	}
	switch m[1] {
	case "6":
		return SoftDeviceS140v6
	case "7":
		return SoftDeviceS140v7
	}
	return 0
}

// buildDevices makes the app's model from a resolved catalog tree. It is lenient: the tree already passed
// resolveTree, and a field that isn't a string reads as "". It also gives the set of every erase and bootloader file
// the catalog names, the dropped boards' too, and gives every board that set to refuse.
func buildDevices(cfg *jsValue, source, site, gitHub string) ([]Device, map[string]bool) {
	staticPath := jsString(cfg.field("staticPath"))
	never := map[string]bool{}
	for _, d := range cfg.field("device").arr {
		addNeverWrite(never, d)
	}
	var out []Device
	for _, d := range cfg.field("device").arr {
		dev := Device{
			Name:       d.field("name").str(),
			Maker:      d.field("maker").str(),
			Type:       DeviceType(d.field("type").str()),
			Source:     source,
			SoftDevice: softDevice(d.field("erase").str()),
			neverWrite: never,
		}

		// A nested notice is a warning about the board, so it is kept even when its entry has no version, and every
		// entry of the board carries it.
		for _, fw := range d.field("firmware").arr {
			for _, k := range fw.obj.ordered() {
				if strings.HasPrefix(k, "github") {
					if t := noticeText(renderNotice(cfg, d, fw.field(k).field("notice"))); t != "" {
						dev.warnings = appendNew(dev.warnings, t)
					}
				}
			}
		}
		dev.Notices = appendNew(dev.Notices, dev.warnings...)
		for i, fw := range d.field("firmware").arr {
			f := Firmware{
				Source:   source,
				Class:    fw.field("class").str(),
				Role:     fw.field("role").str(),
				Title:    roleValue(cfg, fw, "title"),
				SubTitle: roleValue(cfg, fw, "subTitle"),
				index:    i,
			}
			f.ClosedSource = f.Class == "ripple"
			if t := noticeText(renderNotice(cfg, d, fw.field("notice"))); t != "" {
				f.Notices = append(f.Notices, t)
			}
			f.Notices = appendNew(f.Notices, dev.warnings...)
			f.Versions = buildVersions(fw.field("version"), source, site, staticPath, gitHub)
			if len(f.Versions) > 0 {
				dev.Firmware = append(dev.Firmware, f)
				dev.Notices = appendNew(dev.Notices, f.Notices...)
			}
		}
		if len(dev.Firmware) > 0 {
			out = append(out, dev)
		}
	}
	return out, never
}

// addNeverWrite adds a board's "erase" and "bootloader" file names, as the catalog gives them and as the saved file
// would be named (the last part of a path), to the set Choose refuses. "bootloader" is a list in the catalogs; a
// lone name is taken too.
func addNeverWrite(never map[string]bool, d *jsValue) {
	add := func(name string) {
		if name == "" {
			return
		}
		never[name] = true
		never[name[strings.LastIndexByte(name, '/')+1:]] = true
	}
	if e := d.field("erase").str(); e != "" {
		add(e)
		// The web flasher's download menu swaps .zip for .uf2 (js/app.js:91); never that one either.
		add(strings.Replace(e, ".zip", ".uf2", 1))
	}
	switch bl := d.field("bootloader"); {
	case bl == nil:
	case bl.kind == kArray:
		for _, b := range bl.arr {
			add(b.str())
		}
	case bl.kind == kString:
		add(bl.s)
	}
}

// buildVersions lists the versions that have files, in the version object's key order.
func buildVersions(v *jsValue, source, site, staticPath, gitHub string) []Version {
	if v == nil || v.kind != kObject {
		return nil
	}
	var out []Version
	for _, name := range v.obj.ordered() {
		ver := v.obj.get(name)
		files := ver.field("files")
		if files == nil || files.kind != kArray || len(files.arr) == 0 {
			continue
		}
		version := Version{Name: name, Notes: tidyNotes(ver.field("notes").str())}
		for _, f := range files.arr {
			file := File{
				Type:   f.field("type").str(),
				Name:   jsString(f.field("name")),
				Title:  f.field("title").str(),
				Source: source,
			}
			file.URL = site + firmwarePath(staticPath, file.Name)
			if source == SourceMeshCore {
				file.Fallback = gitHubFallback(gitHub, file.Name)
			}
			version.Files = append(version.Files, file)
		}
		out = append(out, version)
	}
	return out
}

// firmwarePath is js/catalog.js:93 firmwareUrl: a name starting with "/" is a path on the site, anything else is a
// file under the catalog's static path. The browser resolves it against the page's origin; here it is always made
// absolute from the site's root.
func firmwarePath(staticPath, name string) string {
	p := name
	if !strings.HasPrefix(name, "/") {
		p = staticPath + "/" + name
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// gitHubFallback is GitHub's address for one of MeshCore's release files: the flasher serves
// /releases/download/<tag>/<file>?repo=github, and GitHub has the same path under meshcore-dev/MeshCore
// (docs/meshcore-flashing.md 2.4). Only "?repo=github"; Ripple's and ZephCore's files have no known other home.
func gitHubFallback(gitHub, name string) string {
	u, err := url.Parse(name)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "repo=github" {
		return ""
	}
	parts := strings.Split(u.Path, "/")
	// "", "releases", "download", tag, file
	if len(parts) != 5 || parts[0] != "" || parts[1] != "releases" || parts[2] != "download" {
		return ""
	}
	for _, p := range parts[3:] {
		if p == "" || p == "." || p == ".." {
			return ""
		}
	}
	return gitHub + gitHubRepo + u.EscapedPath()
}

// tidyNotes makes release notes plain text: Windows line ends become "\n", and blank space at either end goes.
func tidyNotes(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(strings.ReplaceAll(s, "\r", "\n"))
}

// appendNew adds the strings not already in list. It never writes into list's spare capacity, so lists that started
// as the same slice stay apart.
func appendNew(list []string, add ...string) []string {
	list = slices.Clip(list)
	for _, a := range add {
		dup := false
		for _, l := range list {
			if l == a {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, a)
		}
	}
	return list
}

// matchName is how ZephCore's boards are matched to MeshCore's: the same name, ignoring case and surrounding
// spaces. (No fuzzier match: a wrong merge would offer one board's firmware for another.)
func matchName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// mergeZephCore adds ZephCore's firmware to the MeshCore board of the same name and chip family, and adds the boards
// only ZephCore has after MeshCore's. A name that matches a board of another chip family is not merged: it is added
// as a ZephCore board of its own.
func mergeZephCore(meshcore, zephcore []Device) []Device {
	out := meshcore
	for _, z := range zephcore {
		merged := false
		for i := range out[:len(meshcore)] {
			m := &out[i]
			if m.Source != SourceMeshCore || matchName(m.Name) != matchName(z.Name) || m.Type != z.Type {
				continue
			}
			// The same board: each catalog's warnings about it go on the other's firmware too, and the board's
			// warnings stay first in its notices.
			for j := range m.Firmware {
				m.Firmware[j].Notices = appendNew(m.Firmware[j].Notices, z.warnings...)
			}
			for _, f := range z.Firmware {
				f.Notices = appendNew(f.Notices, m.warnings...)
				m.Firmware = append(m.Firmware, f)
			}
			m.warnings = appendNew(m.warnings, z.warnings...)
			m.Notices = appendNew(appendNew(nil, m.warnings...), appendNew(m.Notices, z.Notices...)...)
			if m.SoftDevice == 0 {
				m.SoftDevice = z.SoftDevice
			}
			merged = true
			break
		}
		if !merged {
			out = append(out, z)
		}
	}
	return out
}

// Find returns the board with this name (ignoring case and surrounding spaces), or nil.
func (c *Catalog) Find(name string) *Device {
	for i := range c.Devices {
		if matchName(c.Devices[i].Name) == matchName(name) {
			return &c.Devices[i]
		}
	}
	return nil
}
