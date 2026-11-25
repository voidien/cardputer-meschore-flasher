package ui

// The Role screen's goldens and keys (docs/phase2-design.md 9.3).

import (
	"testing"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/usbid"
)

// wioL1Port is a Seeed Wio Tracker L1 running its app.
func wioL1Port(tty string) flash.Port {
	return portOf(usbid.Port{Name: tty, Dev: "/dev/" + tty, VID: 0x2886, PID: 0x1667, BCD: 0x0100,
		Manufacturer: "Seeed Studio", Product: "Seeed Wio Tracker L1", Serial: "3F6A1C0D9E2B4875",
		Interface: usbid.InterfaceNRFApp, Driver: "cdc_acm", USBPath: "1-1.3"})
}

// roleAt is the Role screen for the board named on port p, opened as Choose board's Enter opens it.
func roleAt(t *testing.T, w, h int, p flash.Port, dev string) *driver {
	t.Helper()
	d := newDriver(t, w, h, screensBackend(t, p))
	d.m.flow = flow{port: p, dev: testCatalog(t).Find(dev)}
	if d.m.flow.dev == nil {
		t.Fatalf("no %s", dev)
	}
	d.m.openRole()
	return d
}

// MeshCore's roles, then ZephCore's, then Ripple's, each under its heading; the selected role's notice above, while
// there is room for it (here Ripple's, the last role, so the notice has scrolled off at 45×10 and 40×10).
func TestRoleGroups(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := roleAt(t, w, h, wioL1Port("ttyACM0"), "Seeed Studio Wio Tracker L1 Pro")
		roleTo(d, catalog.SourceMeshCore, "gui")
		d.check(w, h)
	})
}

// The selected role's notice shows above the list, and changes with the selection.
func TestRoleNotice(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := roleAt(t, w, h, wioL1Port("ttyACM0"), "Seeed Studio Wio Tracker L1 Pro")
		roleTo(d, catalog.SourceMeshCore, "repeater")
		d.check(w, h)
	})
}

// The T1000-E's LoRaWAN warning, which the web flasher never shows, shown as it is.
func TestRoleT1000ENotice(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		roleAt(t, w, h, rakPort("ttyACM0"), "Seeed Studio SenseCAP T1000-E").check(w, h)
	})
}

// A board only ZephCore has: its roles alone (reached only here: Choose board refuses the ESP32-C6).
func TestRoleZephCoreOnly(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		roleAt(t, w, h, heltecV4Port("ttyACM0"), "Seeed Studio Xiao C6").check(w, h)
	})
}

// Enter picks the role and lists its versions; Esc goes back to Choose board as it was left.
func TestRoleKeys(t *testing.T) {
	d := roleAt(t, 45, 10, wioL1Port("ttyACM0"), "Seeed Studio Wio Tracker L1 Pro")
	order := d.m.roleOrder()
	if last := d.m.flow.dev.Firmware[order[len(order)-1]]; !last.ClosedSource {
		t.Errorf("Ripple isn't last: %+v", last.Title)
	}
	roleTo(d, catalog.SourceZephCore, "repeater")
	d.keys("enter")
	if d.m.screen != scrVersion || d.m.flow.fw.Source != catalog.SourceZephCore || d.m.flow.fw.Role != "repeater" {
		t.Fatalf("Enter: screen %d, role %+v", d.m.screen, d.m.flow.fw)
	}
	if titleOf(d)[:len("Repeater · Seeed")] != "Repeater · Seeed" {
		t.Errorf("Version's title: %q", titleOf(d))
	}
	d.keys("esc", "esc")
	if d.m.screen != scrChoose {
		t.Errorf("esc, esc: screen %d", d.m.screen)
	}
}
