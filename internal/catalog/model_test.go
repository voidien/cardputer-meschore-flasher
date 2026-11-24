package catalog

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	liveOnce sync.Once
	live     *Catalog
	liveErr  error
)

// liveCatalog is the saved live catalogs resolved, once for all tests: they only read it.
func liveCatalog(t testing.TB) *Catalog {
	t.Helper()
	liveOnce.Do(func() {
		live, liveErr = Resolve(goldenCases[0].raw(t), goldenCases[1].raw(t), DefaultSites)
	})
	if liveErr != nil {
		t.Fatal(liveErr)
	}
	return live
}

// The T1000-E's LoRaWAN warning sits inside its "github" source, where the web flasher never reads it; meshflash
// shows it for the board, for every role, as plain text.
func TestNoticesT1000E(t *testing.T) {
	t.Parallel() // heavy, independent: the shared catalogs are resolved once and only read
	c := liveCatalog(t)
	d := c.Find("  seeed studio sensecap t1000-e ")
	if d == nil {
		t.Fatal("no T1000-E")
	}
	const warning = "Please do not flash MeshCore firmware to LoRaWAN version of the t1000e, it can permantently brick the device"
	if len(d.Notices) == 0 || d.Notices[0] != warning {
		t.Fatalf("T1000-E notices %q", d.Notices)
	}
	var ble *Firmware
	for i := range d.Firmware {
		if d.Firmware[i].Role == "companionBle" && d.Firmware[i].Source == SourceMeshCore {
			ble = &d.Firmware[i]
		}
	}
	if ble == nil || len(ble.Notices) != 1 || ble.Notices[0] != warning {
		t.Fatalf("companionBle notices %+v", ble)
	}
	// The catalog nests it in companionBle only, but it is about the board: every role carries it, ZephCore's too,
	// so a screen showing only the chosen firmware's notices still shows it.
	roles := map[string]bool{}
	for _, fw := range d.Firmware {
		roles[fw.Source+"/"+fw.Role] = true
		if !slices.Contains(fw.Notices, warning) {
			t.Errorf("%s %s: no LoRaWAN warning in %q", fw.Source, fw.Role, fw.Notices)
		}
	}
	if !roles["meshcore/repeater"] || !roles["zephcore/repeater"] {
		t.Errorf("T1000-E roles %v", roles)
	}
	if d.SoftDevice != SoftDeviceS140v7 {
		t.Errorf("T1000-E SoftDevice %#x, want 0x0123 (erase FLASH_ERASE_nrf52_softdevice_v7.zip)", d.SoftDevice)
	}
}

// Notices are plain text: tags stripped, entities decoded, link addresses never shown, ${bootloader} filled (here
// only inside an address, so it goes too), and a bare "Read more" dropped.
func TestNoticesPlainText(t *testing.T) {
	c := liveCatalog(t)
	seen := map[string]bool{}
	for _, d := range c.Devices {
		for _, n := range d.Notices {
			seen[n] = true
			if strings.ContainsAny(n, "<>") || strings.Contains(n, "http") || strings.Contains(n, "${") ||
				strings.Contains(n, "/firmware") || strings.Contains(n, "Read more") {
				t.Errorf("%s: notice not plain text: %q", d.Name, n)
			}
		}
	}
	for _, want := range []string{
		"We strongly recommend installing OTAFIX bootloader by @oltaco for more reliable Bluetooth OTA DFU.",
		"WARNING: We strongly recommend installing OTAFIX bootloader by @oltaco if you plan to update firmware via OTA bluetooth, as factory bootloader does not support it!",
		"For first time users, we recommend reading Ripple User Guide",
		"You can use this firmware with openHop Repeater",
	} {
		if !seen[want] {
			t.Errorf("missing notice %q", want)
		}
	}

	for in, want := range map[string]string{
		`<article class='x'>A <b>bold</b> &amp; <a href="https://x.y/?a=1&b=2">linked</a> word. <a href='/z'>Read more</a></article>`: "A bold & linked word.",
		`one<br>two<p>three</p>`:            "one\ntwo\nthree",
		`a < b and c > d`:                   "a < b and c > d",
		`<a title="x>y" href="/q">kept</a>`: "kept",
		`<a href="/q">unclosed`:             "unclosed",
		`  spaced   out  `:                  "spaced out",
		``:                                  "",
	} {
		if got := noticeText(in); got != want {
			t.Errorf("noticeText(%q) = %q, want %q", in, got, want)
		}
	}
}

// renderNotice fills ${field} from the device (an array's first item), and the result has no address once plain.
func TestRenderNoticeFields(t *testing.T) {
	cfg, _ := parseJS([]byte(`{"notice":{"k":"<a href='/firmware/${bootloader}'>OTAFIX</a> for ${name} ${missing}${erase}"}}`))
	dev, _ := parseJS([]byte(`{"name":"Board","bootloader":["bl.uf2","bl.zip"],"erase":""}`))
	key := jsStr("k")
	if got := renderNotice(cfg, dev, key); got != "<a href='/firmware/bl.uf2'>OTAFIX</a> for Board " {
		t.Fatalf("renderNotice: %q", got)
	}
	if got := noticeText(renderNotice(cfg, dev, key)); got != "OTAFIX for Board" {
		t.Fatalf("text: %q", got)
	}
	if got := renderNotice(cfg, dev, jsStr("<b>literal</b>")); got != "<b>literal</b>" {
		t.Fatalf("literal notice: %q", got)
	}
}

// ZephCore's boards join MeshCore's by name; the two ZephCore-only boards come after MeshCore's.
func TestZephCoreMerge(t *testing.T) {
	_, mesh := goldenCases[0].resolved(t)
	c := liveCatalog(t)
	if len(c.Devices) != len(mesh)+2 {
		t.Fatalf("%d devices, want %d MeshCore + 2 ZephCore-only", len(c.Devices), len(mesh))
	}
	merged := 0
	for i, d := range c.Devices[:len(mesh)] {
		if d.Name != mesh[i].Name || d.Source != SourceMeshCore {
			t.Fatalf("device %d is %s (%s), want %s first in MeshCore's order", i, d.Name, d.Source, mesh[i].Name)
		}
		zephSeen := false
		for _, fw := range d.Firmware {
			if fw.Source == SourceZephCore {
				zephSeen = true
				if fw.Role != "companion" && fw.Role != "repeater" {
					t.Errorf("%s: ZephCore role %q", d.Name, fw.Role)
				}
			} else if zephSeen {
				t.Errorf("%s: MeshCore firmware after ZephCore's", d.Name)
			}
		}
		if zephSeen {
			merged++
		}
	}
	var only []string
	for _, d := range c.Devices[len(mesh):] {
		if d.Source != SourceZephCore {
			t.Errorf("%s: source %s", d.Name, d.Source)
		}
		only = append(only, d.Name)
	}
	if merged != 27 || strings.Join(only, ", ") != "LilyGo T-Lora C6, Seeed Studio Xiao C6" {
		t.Errorf("merged %d (want 27), ZephCore only %q", merged, only)
	}
	t.Logf("ZephCore: %d boards, %d matched MeshCore's by name, %d added (%s)", merged+len(only), merged, len(only),
		strings.Join(only, ", "))

	// MeshCore's Wio Tracker L1 Pro erase file names no SoftDevice; ZephCore's says v7.
	if d := c.Find("Seeed Studio Wio Tracker L1 Pro"); d == nil || d.SoftDevice != SoftDeviceS140v7 {
		t.Errorf("Wio Tracker L1 Pro SoftDevice from ZephCore: %+v", d)
	}
	// Neither catalog's erase or bootloader file may be written, whatever the merge.
	d := c.Find("Seeed Studio Xiao nRF52 WIO")
	for _, n := range []string{"Xiao_nrf52_QSPIFlash_Format-v1.2.zip", "ZephCore_SoftDevice_v7_formatter.zip",
		"ZephCore_SoftDevice_v7_formatter.uf2"} {
		if !d.neverWrite[n] {
			t.Errorf("Xiao nRF52: %s not refused", n)
		}
	}
}

// A ZephCore board whose name matches a MeshCore board of another chip family stays a board of its own.
func TestZephCoreMergeNeedsSameChip(t *testing.T) {
	mesh := Raw{Config: []byte(`{"staticPath":"/firmware","device":[{"name":"Board","type":"esp32","firmware":[
		{"role":"repeater","version":{"v1":{"files":[{"type":"flash-update","name":"a.bin","title":"a"}]}}}]}]}`)}
	zeph := Raw{Config: []byte(`{"staticPath":"/firmware","device":[{"name":"board ","type":"nrf52","firmware":[
		{"role":"repeater","version":{"v1":{"files":[{"type":"flash","name":"b.zip","title":"b"}]}}}]}]}`)}
	c, err := Resolve(mesh, zeph, Sites{MeshCore: "https://m", ZephCore: "https://z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Devices) != 2 || c.Devices[1].Source != SourceZephCore || c.Devices[1].Firmware[0].Versions[0].Files[0].URL != "https://z/firmware/b.zip" {
		t.Fatalf("%+v", c.Devices)
	}
}

// noflash boards stay in the list so the screen can say they are download only; Choose refuses them.
func TestNoFlashListed(t *testing.T) {
	c := liveCatalog(t)
	n := 0
	for _, d := range c.Devices {
		if d.Type != NoFlash {
			continue
		}
		n++
		if d.Flashable() {
			t.Errorf("%s flashable", d.Name)
		}
		v := d.Firmware[0].Versions[0]
		if _, err := Choose(d, v, Update); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "download only") {
			t.Errorf("%s: %v", d.Name, err)
		}
	}
	if n != 2 {
		t.Errorf("%d noflash boards, want 2 (the RPI Pico W boards)", n)
	}
}

// Ripple's builds are marked closed source; version order is the catalog's, newest first.
func TestFirmwareFields(t *testing.T) {
	c := liveCatalog(t)
	d := c.Find("LilyGo T-Deck")
	var ripple, repeater *Firmware
	for i := range d.Firmware {
		switch fw := &d.Firmware[i]; {
		case fw.Class == "ripple" && ripple == nil:
			ripple = fw
		case fw.Role == "repeater" && fw.Source == SourceMeshCore:
			repeater = fw
		}
	}
	if ripple == nil || !ripple.ClosedSource || ripple.Title != "Ripple GUI" {
		t.Fatalf("ripple %+v", ripple)
	}
	if repeater == nil || repeater.ClosedSource || repeater.Title != "Repeater" || repeater.Versions[0].Name != "v1.18.0" ||
		repeater.Versions[1].Name != "v1.17.1" {
		t.Fatalf("repeater %+v", repeater)
	}
	if n := repeater.Versions[0].Notes; strings.Contains(n, "\r") || n == "" {
		t.Errorf("notes %q", n)
	}
	if d.SoftDevice != 0 {
		t.Errorf("an ESP32 has no SoftDevice: %#x", d.SoftDevice)
	}
}
