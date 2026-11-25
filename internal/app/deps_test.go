package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// No test reaches the real boards or the network: realDeps refuses to run in a test, and so does a Program without a
// Fake, which is why every test here gives its Program one (stubProgram, refusingFake).
func TestNoRealDepsInTests(t *testing.T) {
	refused := func(what string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s built the real dependencies in a test", what)
			}
		}()
		f()
	}
	refused("realDeps", func() { realDeps(Env{CacheDir: t.TempDir(), Version: "0.1.0"}) })
	refused("a Program without a Fake", func() {
		deps(Program{Name: "meshflash", Version: "0.1.0"}, Env{CacheDir: t.TempDir()})
	})
	if _, err := deps(stubProgram(t), Env{}); err != nil {
		t.Errorf("a Program with a Fake: %v", err)
	}
}

// No test sleeps in real time (CLAUDE.md's hard rules, SPEC.md 10): every clock is virtual. The two exceptions SPEC
// names are allowed and counted: the kit's SIGTERM test in this package (a signal has no other way to say it has
// been and gone) and internal/screen's tests copied from the kit. A guard's time.After, never waited out by a test
// that passes, is not a sleep.
func TestNoRealSleepsInTests(t *testing.T) {
	allowed := map[string]int{"internal/app/app_test.go": 1, "internal/screen/screen_test.go": -1}
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join("..", "..", top), func(path string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, filepath.Join("..", "..")+string(filepath.Separator)))
			n := strings.Count(string(b), "time."+"Sleep(") // split, so this file doesn't count itself
			if max, ok := allowed[rel]; n > 0 && (!ok || max >= 0 && n > max) {
				t.Errorf("%s sleeps in real time %d times: use the virtual clock", rel, n)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The battery as the screens need it: read afresh each time, a failure never kept; a battery that is there (or was)
// but can't be read is (-1, true), so the screens ask before flashing; (0, false) only where there has been none.
func TestBatteryUnreadable(t *testing.T) {
	dir := t.TempDir()
	read := battery(dir, false)
	expect := func(what string, wantN int, wantOK bool) {
		t.Helper()
		if n, ok := read(); n != wantN || ok != wantOK {
			t.Errorf("%s: (%d, %v), want (%d, %v)", what, n, ok, wantN, wantOK)
		}
	}
	supply := func(name, kind string) string {
		t.Helper()
		d := filepath.Join(dir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "type"), []byte(kind+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	capacity := func(d, v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(d, "capacity"), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	expect("no power supplies (a PC)", 0, false)
	supply("AC", "Mains")
	expect("a charger only", 0, false)

	bq := supply("bq27220-0", "Battery")
	expect("a battery with no capacity yet", -1, true)
	capacity(bq, "87\n")
	expect("87%", 87, true)
	for _, bad := range []string{"", "abc", "101", "-3", "87%"} {
		capacity(bq, bad)
		expect("capacity "+bad, -1, true)
	}
	capacity(bq, "64\n")
	expect("readable again", 64, true)
	if os.Geteuid() != 0 { // root reads it anyway
		os.Chmod(filepath.Join(bq, "capacity"), 0)
		expect("capacity unreadable", -1, true)
		os.Chmod(filepath.Join(bq, "capacity"), 0o644)
		expect("capacity readable", 64, true)
	}
	if err := os.RemoveAll(bq); err != nil {
		t.Fatal(err)
	}
	expect("the battery gone from the list after it was seen", -1, true)
	if n, ok := battery(dir, false)(); n != 0 || ok {
		t.Errorf("a fresh reading with no battery: (%d, %v), want (0, false)", n, ok)
	}
	// On the Zero a battery never listed (its fuel gauge's driver didn't load) is one that can't be read.
	if n, ok := battery(dir, true)(); n != -1 || !ok {
		t.Errorf("the Zero with no battery listed: (%d, %v), want (-1, true)", n, ok)
	}
}

// The Zero is told by its device tree's model, NUL-terminated as Linux gives it; a PC has none or another.
func TestOnZero(t *testing.T) {
	dir := t.TempDir()
	model := func(s string) string {
		p := filepath.Join(dir, strings.Fields(s)[2]) // a file each: "Compute", "4"
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, c := range []struct {
		path string
		want bool
	}{
		{model("Raspberry Pi Compute Module 0 Rev 1.0\x00"), true},
		{model("Raspberry Pi 4 Model B Rev 1.4\x00"), false},
		{filepath.Join(dir, "none"), false},
	} {
		if got := onZero(c.path); got != c.want {
			t.Errorf("onZero(%s) = %v", c.path, got)
		}
	}
}

// The clock is synchronised when adjtimex's state isn't TIME_ERROR and STA_UNSYNC is clear.
func TestClockSyncedReading(t *testing.T) {
	for _, c := range []struct {
		state  int
		status int32
		want   bool
	}{
		{unix.TIME_OK, 0, true},
		{unix.TIME_OK, unix.STA_PLL, true},
		{unix.TIME_INS, 0, true},
		{unix.TIME_OK, unix.STA_UNSYNC, false},
		{unix.TIME_OK, unix.STA_PLL | unix.STA_UNSYNC, false},
		{unix.TIME_ERROR, 0, false},
	} {
		if got := synced(c.state, c.status); got != c.want {
			t.Errorf("state %d, status %#x: synced %v, want %v", c.state, c.status, got, c.want)
		}
	}
}
