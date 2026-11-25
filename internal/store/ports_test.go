package store_test

// The store against the contract's keys (docs/phase2-design.md 3.3, owner question 13): drafts kept by
// flash.Port.DraftKey, boards remembered by flash.Port.Key only. An external test, since internal/flash's rules are
// what make the keys, and the store itself never imports flash.

import (
	"errors"
	"testing"

	"meshflash/internal/flash"
	"meshflash/internal/store"
	"meshflash/internal/usbid"
)

func TestDraftKeysFromPorts(t *testing.T) {
	// The owner's boards, and two boards with no useful serial on neighbouring sockets.
	ports := []struct {
		name    string
		port    usbid.Port
		key     string // flash.Port.Key: what Choose board remembers by
		draftBy string // flash.Port.DraftKey
	}{
		{"RAK WisBlock 4631", usbid.Port{VID: 0x239a, PID: 0x8029, Product: "WisCore RAK4631 Board",
			Serial: "D2A1F3C4B5E69788", Interface: usbid.InterfaceNRFApp, Driver: "cdc_acm", USBPath: "1-1.3"},
			"D2A1F3C4B5E69788", "D2A1F3C4B5E69788"},
		{"Heltec v4", usbid.Port{VID: 0x303a, PID: 0x1001, Product: "USB JTAG/serial debug unit",
			Serial: "60:55:F9:C0:27:18", Driver: "cdc_acm", USBPath: "1-1.1"},
			"6055F9C02718", "6055F9C02718"},
		{"Heltec v3 on 1-1.2", usbid.Port{VID: 0x10c4, PID: 0xea60, Product: "CP2102 USB to UART Bridge Controller",
			Serial: "0001", Driver: "cp210x", USBPath: "1-1.2"},
			"", "usb 1-1.2 10c4:ea60"},
		{"Heltec v3 on 1-1.4", usbid.Port{VID: 0x10c4, PID: 0xea60, Product: "CP2102 USB to UART Bridge Controller",
			Serial: "0001", Driver: "cp210x", USBPath: "1-1.4"},
			"", "usb 1-1.4 10c4:ea60"},
		{"a CH340 board", usbid.Port{VID: 0x1a86, PID: 0x7523, Product: "USB2.0-Serial", Driver: "ch341",
			USBPath: "1-1.5"},
			"", "usb 1-1.5 1a86:7523"},
	}
	s := store.New(t.TempDir(), t.TempDir())
	for _, p := range ports {
		fp := flash.PortOf(p.port)
		if fp.Key != p.key || fp.DraftKey() != p.draftBy {
			t.Fatalf("%s: Key %q, DraftKey %q; want %q, %q", p.name, fp.Key, fp.DraftKey(), p.key, p.draftBy)
		}
		if err := s.SaveDraft(fp.DraftKey(), store.Draft{Role: "repeater", Name: p.name}); err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		err := s.RememberBoard(fp.Key, p.name)
		if p.key == "" && !errors.Is(err, store.ErrNoKey) {
			t.Errorf("%s has no useful serial, yet RememberBoard said %v", p.name, err)
		}
		if p.key != "" && err != nil {
			t.Errorf("%s: %v", p.name, err)
		}
	}
	for _, p := range ports {
		if d, ok := s.LoadDraft(flash.PortOf(p.port).DraftKey()); !ok || d.Name != p.name {
			t.Errorf("%s's draft: %+v, %v", p.name, d, ok)
		}
	}
	c, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Boards) != 2 || c.Boards["D2A1F3C4B5E69788"] != "RAK WisBlock 4631" || c.Boards["6055F9C02718"] != "Heltec v4" {
		t.Errorf("remembered boards: %v", c.Boards)
	}

	// A port with neither a useful serial nor a socket has no draft at all.
	lost := flash.PortOf(usbid.Port{VID: 0x1a86, PID: 0x7523, Driver: "ch341"})
	if err := s.SaveDraft(lost.DraftKey(), store.Draft{Name: "lost"}); !errors.Is(err, store.ErrNoKey) {
		t.Errorf("a port with no key: %v, want ErrNoKey", err)
	}
}
