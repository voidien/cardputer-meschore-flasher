package screen

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBattery(t *testing.T) {
	dir := t.TempDir()
	supply := func(name, kind, capacity string) {
		os.MkdirAll(filepath.Join(dir, name), 0o700)
		os.WriteFile(filepath.Join(dir, name, "type"), []byte(kind+"\n"), 0o600)
		if capacity != "" {
			os.WriteFile(filepath.Join(dir, name, "capacity"), []byte(capacity+"\n"), 0o600)
		}
	}
	clock := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	if got := Battery(dir, now)(); got != "" {
		t.Errorf("no power supplies gave %q", got)
	}
	supply("usb", "USB", "")
	supply("bq27220-0", "Battery", "87")
	battery := Battery(dir, now)
	if got := battery(); got != "87%" {
		t.Errorf("got %q, want 87%%", got)
	}
	supply("bq27220-0", "Battery", "86")
	if got := battery(); got != "87%" {
		t.Errorf("read again within the minute: %q", got)
	}
	clock = clock.Add(time.Minute)
	if got := battery(); got != "86%" {
		t.Errorf("a minute later got %q, want 86%%", got)
	}
	supply("bq27220-0", "Battery", "garbage")
	clock = clock.Add(time.Minute)
	if got := battery(); got != "" {
		t.Errorf("an unreadable charge gave %q", got)
	}
	clock = clock.Add(-time.Hour) // the clock set back: read again rather than trust a reading from the future
	supply("bq27220-0", "Battery", "50")
	if got := battery(); got != "50%" {
		t.Errorf("after the clock went back got %q", got)
	}
}
