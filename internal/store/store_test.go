package store

// The store's tests (docs/phase2-design.md 6), shaped after the kit's internal/store/store_test.go (the owner's own
// code: THIRD_PARTY_NOTICES). No test here runs in parallel: some replace readFile, which every other test reads.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	testRadio = Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8, TxDBm: 22, PathHashBytes: 1}
	testSaved = time.Date(2026, 11, 24, 14, 32, 0, 0, time.UTC)
	testDraft = Draft{Role: "repeater", Name: "Hilltop 2", Freq: "869.6", BW: "62.5", SF: "8", CR: "8", TX: "22",
		PathHash: "1", Cleared: []string{"lat", "lon"}, Saved: testSaved}
)

const (
	rakKey    = "D2A1F3C4B5E69788"
	socketKey = "usb 1-1.2 10c4:ea60" // a CP2102 saying "0001" (owner question 13)
)

// newStore is a store whose config folder doesn't exist yet (so the tests see the store make it), in a temporary
// folder of its own.
func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return New(filepath.Join(dir, "config", "meshflash"), filepath.Join(dir, "cache", "meshflash"))
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func missing(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// noTempFiles fails the test if a write left its temporary file in the folder.
func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("%s left behind in %s", e.Name(), dir)
		}
	}
}

// unreadable makes path fail to read with a permission error until the test ends, as a file chmod 0 does for anyone
// but root (the tests often run as root, which chmod can't stop).
func unreadable(t *testing.T, path string) {
	t.Helper()
	orig := readFile
	readFile = func(name string) ([]byte, error) {
		if name == path {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
		}
		return orig(name)
	}
	t.Cleanup(func() { readFile = orig })
}

// chmod0 takes every permission off path, and skips the test if it can be read all the same: as root, or with
// CAP_DAC_OVERRIDE, which chmod can't stop (unreadable's subtest covers those runs).
func chmod0(t *testing.T, path, covered string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err == nil {
		os.Chmod(path, 0o600)
		t.Skip("this user reads a file whatever its mode; " + covered + " covers it")
	}
}

// sameDraft compares two drafts, their times as instants (a time read back from JSON has its own Location).
func sameDraft(a, b Draft) bool {
	if !a.Saved.Equal(b.Saved) {
		return false
	}
	a.Saved, b.Saved = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func TestConfigIsPrivate(t *testing.T) {
	s := newStore(t)
	if c, err := s.LoadConfig(); err != nil || !reflect.DeepEqual(c, Config{}) {
		t.Fatalf("missing config: %+v, %v", c, err)
	}
	// Reading, and changes that change nothing, create nothing (OpenApp: nothing until something is saved).
	s.LoadDraft(rakKey)
	if err := s.DeleteDraft(rakKey); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberBoard(rakKey, ""); err != nil {
		t.Fatal(err)
	}
	if !missing(filepath.Dir(s.ConfigDir())) {
		t.Fatal("the config folder was made before anything was saved")
	}

	if err := s.RememberBoard(rakKey, "RAK WisBlock 4631"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraft(rakKey, testDraft); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{ConfigFile, DraftsFile} {
		if m := mode(t, filepath.Join(s.ConfigDir(), f)); m != 0o600 {
			t.Errorf("%s is %v, want 0600", f, m)
		}
	}
	// MkdirAll makes the missing parents too, all 0700.
	for _, dir := range []string{s.ConfigDir(), filepath.Dir(s.ConfigDir())} {
		if m := mode(t, dir); m != 0o700 {
			t.Errorf("folder %s is %v, want 0700", dir, m)
		}
	}
	noTempFiles(t, s.ConfigDir())
	if !missing(s.CacheDir()) {
		t.Error("the store wrote into the cache folder")
	}

	// A folder and a file made by hand, readable by anyone: the next save makes both the owner's alone, and keeps
	// what the file held.
	s = newStore(t)
	writeFile(t, filepath.Join(s.ConfigDir(), ConfigFile), `{"boards": {"AAAA": "Heltec v4"}}`)
	if err := os.Chmod(s.ConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(s.ConfigDir(), ConfigFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberBoard(rakKey, "RAK WisBlock 4631"); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, s.ConfigDir()); m != 0o700 {
		t.Errorf("a folder made 0755 is %v after a save, want 0700", m)
	}
	if m := mode(t, filepath.Join(s.ConfigDir(), ConfigFile)); m != 0o600 {
		t.Errorf("a config.json made 0644 is %v after a save, want 0600", m)
	}
	if c, err := s.LoadConfig(); err != nil || c.Boards["AAAA"] != "Heltec v4" || c.Boards[rakKey] == "" {
		t.Errorf("after the save: %+v, %v", c, err)
	}
}

// A config cut short (a battery that died as it was written, though the writes are atomic), not JSON, or the wrong
// shape is read as none, so meshflash starts; the next save replaces it.
func TestADamagedConfigIsNone(t *testing.T) {
	s := newStore(t)
	path := filepath.Join(s.ConfigDir(), ConfigFile)
	for _, bad := range []string{
		"",
		`{"radio": {"freq_mhz": 869.6`,
		`[1, 2]`,
		`{"radio": 7}`,
		`{"radio": {"sf": "8"}}`,
		`{"boards": {"D2A1F3C4B5E69788": 5}}`,
		`{"boards": ["RAK WisBlock 4631"]}`,
		`{"radio": {"sf": 1e100}}`,
		"\x00\x00\x00\x00\x00\x00\x00\x00", // a file whose blocks were never written
		`{"boards": {}} {"boards": {}}`,
		// A good half and a bad half: none of it, rather than boards paired with a radio that isn't the one sent.
		`{"boards": {"AAAA": "Heltec v4"}, "radio": {"freq_mhz": "869.618"}}`,
	} {
		writeFile(t, path, bad)
		if c, err := s.LoadConfig(); err != nil || !reflect.DeepEqual(c, Config{}) {
			t.Errorf("config %q: %+v, %v", bad, c, err)
		}
	}
	if err := s.RememberBoard(rakKey, "RAK WisBlock 4631"); err != nil {
		t.Fatal(err)
	}
	want := Config{Boards: map[string]string{rakKey: "RAK WisBlock 4631"}}
	if c, err := s.LoadConfig(); err != nil || !reflect.DeepEqual(c, want) {
		t.Errorf("saved over a damaged one: %+v, %v", c, err)
	}
	if !json.Valid(fileBytes(t, path)) {
		t.Errorf("config.json after the save: %q", fileBytes(t, path))
	}
	writeFile(t, path, "[1, 2]")
	if err := s.SaveRadio(testRadio); err != nil {
		t.Fatal(err)
	}
	if c, err := s.LoadConfig(); err != nil || c.Radio == nil || *c.Radio != testRadio || c.Boards != nil {
		t.Errorf("radio saved over a damaged one: %+v, %v", c, err)
	}
}

// A config.json that can't be read is an error for LoadConfig (the app starts without it and says so in
// screen.log), and is never written over: what it holds comes back once its permissions are fixed.
func TestUnreadableConfig(t *testing.T) {
	const saved = `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":8,"cr":8,"tx_dbm":22},"boards":{"AAAA":"Heltec v4"}}`
	leftAlone := func(t *testing.T, s *Store, permission bool) {
		t.Helper()
		_, err := s.LoadConfig()
		if err == nil {
			t.Fatal("an unreadable config read as none")
		}
		if permission && !errors.Is(err, fs.ErrPermission) {
			t.Errorf("LoadConfig: %v, want a permission error", err)
		}
		if err := s.RememberBoard(rakKey, "RAK WisBlock 4631"); err == nil {
			t.Error("RememberBoard wrote over an unreadable config")
		} else if permission && !errors.Is(err, fs.ErrPermission) {
			t.Errorf("RememberBoard: %v, want a permission error", err)
		}
		if err := s.SaveRadio(Radio{FreqMHz: 915, BWkHz: 250, SF: 10, CR: 5, TxDBm: 20}); err == nil {
			t.Error("SaveRadio wrote over an unreadable config")
		}
		noTempFiles(t, s.ConfigDir())
	}

	t.Run("chmod 0", func(t *testing.T) {
		s := newStore(t)
		path := filepath.Join(s.ConfigDir(), ConfigFile)
		writeFile(t, path, saved)
		chmod0(t, path, "the next subtest")
		leftAlone(t, s, true)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := string(fileBytes(t, path)); got != saved {
			t.Errorf("config.json is now %q", got)
		}
	})
	t.Run("permission denied", func(t *testing.T) {
		s := newStore(t)
		path := filepath.Join(s.ConfigDir(), ConfigFile)
		writeFile(t, path, saved)
		unreadable(t, path)
		leftAlone(t, s, true)
		if got := string(fileBytes(t, path)); got != saved {
			t.Errorf("config.json is now %q", got)
		}
	})
	t.Run("a folder in its place", func(t *testing.T) {
		s := newStore(t)
		path := filepath.Join(s.ConfigDir(), ConfigFile)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		leftAlone(t, s, false)
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Errorf("the folder named config.json: %v, %v", info, err)
		}
	})
}

func TestRememberBoard(t *testing.T) {
	s := newStore(t)
	if err := s.RememberBoard("", "RAK WisBlock 4631"); !errors.Is(err, ErrNoKey) {
		t.Errorf("an empty serial: %v, want ErrNoKey", err)
	}
	if !missing(s.ConfigDir()) {
		t.Error("an empty serial made the config folder")
	}
	if err := s.RememberBoard(rakKey, "RAK WisBlock 4631"); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberBoard("6055F9C02718", "Heltec v4"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRadio(testRadio); err != nil {
		t.Fatal(err)
	}
	// Picking another board for the same serial replaces its name and keeps everything else.
	if err := s.RememberBoard(rakKey, "RAK WisMesh Tag"); err != nil {
		t.Fatal(err)
	}
	want := Config{Radio: &testRadio, Boards: map[string]string{rakKey: "RAK WisMesh Tag", "6055F9C02718": "Heltec v4"}}
	if c, err := s.LoadConfig(); err != nil || !reflect.DeepEqual(c, want) {
		t.Errorf("after the second pick: %+v, %v", c, err)
	}
	// An empty device forgets the serial, keeping the rest; forgetting one never remembered changes nothing.
	if err := s.RememberBoard(rakKey, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberBoard("FFFF", ""); err != nil {
		t.Fatal(err)
	}
	want.Boards = map[string]string{"6055F9C02718": "Heltec v4"}
	if c, err := s.LoadConfig(); err != nil || !reflect.DeepEqual(c, want) {
		t.Errorf("after forgetting: %+v, %v", c, err)
	}
	if err := s.RememberBoard("6055F9C02718", ""); err != nil {
		t.Fatal(err)
	}
	if b := fileBytes(t, filepath.Join(s.ConfigDir(), ConfigFile)); bytes.Contains(b, []byte(`"boards"`)) {
		t.Errorf("no boards left, yet config.json is %s", b)
	}
	if c, err := s.LoadConfig(); err != nil || len(c.Boards) != 0 || c.Radio == nil || *c.Radio != testRadio {
		t.Errorf("after forgetting the last board: %+v, %v", c, err)
	}
}

func TestSaveRadio(t *testing.T) {
	s := newStore(t)
	if err := s.SaveRadio(testRadio); err != nil {
		t.Fatal(err)
	}
	if c, err := s.LoadConfig(); err != nil || c.Radio == nil || *c.Radio != testRadio || c.Boards != nil {
		t.Errorf("first radio: %+v, %v", c, err)
	}
	if err := s.RememberBoard(rakKey, "RAK WisBlock 4631"); err != nil {
		t.Fatal(err)
	}
	// A radio from firmware before v1.14.0 sends no path hash: 0, left out of the file.
	older := Radio{FreqMHz: 915.0, BWkHz: 250, SF: 10, CR: 5, TxDBm: 20}
	if err := s.SaveRadio(older); err != nil {
		t.Fatal(err)
	}
	c, err := s.LoadConfig()
	if err != nil || c.Radio == nil || *c.Radio != older || c.Boards[rakKey] != "RAK WisBlock 4631" {
		t.Errorf("second radio: %+v, %v", c, err)
	}
	if b := fileBytes(t, filepath.Join(s.ConfigDir(), ConfigFile)); bytes.Contains(b, []byte("path_hash_bytes")) {
		t.Errorf("no path hash sent, yet config.json is %s", b)
	}
	// The caller's copy is the caller's: changing it afterwards changes nothing saved.
	r := testRadio
	if err := s.SaveRadio(r); err != nil {
		t.Fatal(err)
	}
	r.SF = 12
	if c, err := s.LoadConfig(); err != nil || c.Radio == nil || *c.Radio != testRadio {
		t.Errorf("third radio: %+v, %v", c, err)
	}
}

func TestDrafts(t *testing.T) {
	s := newStore(t)
	path := filepath.Join(s.ConfigDir(), DraftsFile)
	if _, ok := s.LoadDraft(rakKey); ok {
		t.Error("a draft before any was saved")
	}
	if _, ok := s.LoadDraft(""); ok {
		t.Error("a draft for no key")
	}
	if err := s.SaveDraft("", testDraft); !errors.Is(err, ErrNoKey) {
		t.Errorf("SaveDraft with no key: %v, want ErrNoKey", err)
	}
	if err := s.DeleteDraft(""); !errors.Is(err, ErrNoKey) {
		t.Errorf("DeleteDraft with no key: %v, want ErrNoKey", err)
	}

	// Saved, loaded back exactly, Cleared included; a board on a USB socket keeps its own.
	socket := Draft{Role: "room_server", Name: "Hall", Freq: "869.", Lat: "51.5", Saved: testSaved.Add(time.Minute)}
	if err := s.SaveDraft(rakKey, testDraft); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraft(socketKey, socket); err != nil {
		t.Fatal(err)
	}
	if d, ok := s.LoadDraft(rakKey); !ok || !sameDraft(d, testDraft) {
		t.Errorf("RAK's draft: %+v, %v", d, ok)
	}
	if d, ok := s.LoadDraft(socketKey); !ok || !sameDraft(d, socket) {
		t.Errorf("the socket's draft: %+v, %v", d, ok)
	}
	if _, ok := s.LoadDraft("usb 1-1.3 10c4:ea60"); ok {
		t.Error("the board on the next socket has the socket's draft")
	}

	// Replaced, keeping the other board's.
	changed := testDraft
	changed.Name, changed.Cleared = "Hilltop 3", []string{"advert"}
	if err := s.SaveDraft(rakKey, changed); err != nil {
		t.Fatal(err)
	}
	if d, ok := s.LoadDraft(rakKey); !ok || !sameDraft(d, changed) {
		t.Errorf("RAK's replaced draft: %+v, %v", d, ok)
	}
	if d, ok := s.LoadDraft(socketKey); !ok || !sameDraft(d, socket) {
		t.Errorf("the socket's draft after RAK's was replaced: %+v, %v", d, ok)
	}

	// Deleted, keeping the other board's; deleting it again, or one never kept, is no error.
	if err := s.DeleteDraft(rakKey); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadDraft(rakKey); ok {
		t.Error("a deleted draft came back")
	}
	if d, ok := s.LoadDraft(socketKey); !ok || !sameDraft(d, socket) {
		t.Errorf("the socket's draft after RAK's was deleted: %+v, %v", d, ok)
	}
	for _, key := range []string{rakKey, "never kept"} {
		if err := s.DeleteDraft(key); err != nil {
			t.Errorf("DeleteDraft(%q): %v", key, err)
		}
	}
	if err := s.DeleteDraft(socketKey); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(fileBytes(t, path))); got != "{}" {
		t.Errorf("drafts.json with every draft deleted: %q", got)
	}

	// A damaged drafts.json has no drafts, a delete leaves it as it is, and a save replaces it.
	for _, bad := range []string{"", `{"` + rakKey + `": {"name": "Hill`, `[1, 2]`, `{"` + rakKey + `": {"saved": "yesterday"}}`,
		`{"` + rakKey + `": {"cleared": "lat"}}`} {
		writeFile(t, path, bad)
		if d, ok := s.LoadDraft(rakKey); ok {
			t.Errorf("drafts.json %q gave %+v", bad, d)
		}
		if err := s.DeleteDraft(rakKey); err != nil {
			t.Errorf("DeleteDraft on drafts.json %q: %v", bad, err)
		}
		if got := string(fileBytes(t, path)); got != bad {
			t.Errorf("a delete rewrote drafts.json %q as %q", bad, got)
		}
		if err := s.SaveDraft(socketKey, socket); err != nil {
			t.Fatal(err)
		}
		if d, ok := s.LoadDraft(socketKey); !ok || !sameDraft(d, socket) {
			t.Errorf("saved over drafts.json %q: %+v, %v", bad, d, ok)
		}
	}

	// An unreadable drafts.json has no drafts and is replaced by a save; a delete says it couldn't.
	t.Run("unreadable", func(t *testing.T) {
		if err := s.SaveDraft(rakKey, testDraft); err != nil {
			t.Fatal(err)
		}
		unreadable(t, path)
		if _, ok := s.LoadDraft(rakKey); ok {
			t.Error("an unreadable drafts.json gave a draft")
		}
		if err := s.DeleteDraft(rakKey); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("DeleteDraft on an unreadable drafts.json: %v, want a permission error", err)
		}
		if err := s.SaveDraft(socketKey, socket); err != nil {
			t.Fatal(err)
		}
		var all map[string]Draft
		if err := json.Unmarshal(fileBytes(t, path), &all); err != nil || len(all) != 1 || !sameDraft(all[socketKey], socket) {
			t.Errorf("drafts.json after replacing an unreadable one: %+v, %v", all, err)
		}
	})
	t.Run("chmod 0", func(t *testing.T) {
		chmod0(t, path, "the subtest before")
		if _, ok := s.LoadDraft(socketKey); ok {
			t.Error("an unreadable drafts.json gave a draft")
		}
		if err := s.SaveDraft(rakKey, testDraft); err != nil {
			t.Fatal(err)
		}
		if m := mode(t, path); m != 0o600 {
			t.Errorf("drafts.json replaced as %v", m)
		}
		if d, ok := s.LoadDraft(rakKey); !ok || !sameDraft(d, testDraft) {
			t.Errorf("after replacing it: %+v, %v", d, ok)
		}
	})
	noTempFiles(t, s.ConfigDir())
}

// The real build and the demo build each keep their own folders (S2), under XDG's or else under $HOME.
func TestEachAppKeepsItsOwnFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("os.UserConfigDir follows XDG_CONFIG_HOME on Linux, where meshflash runs; not on macOS or Windows")
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	mf, err := OpenApp("meshflash")
	if err != nil {
		t.Fatal(err)
	}
	demo, err := OpenApp("meshflash-demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ got, want string }{
		{mf.ConfigDir(), filepath.Join(home, "config", "meshflash")},
		{mf.CacheDir(), filepath.Join(home, "cache", "meshflash")},
		{demo.ConfigDir(), filepath.Join(home, "config", "meshflash-demo")},
		{demo.CacheDir(), filepath.Join(home, "cache", "meshflash-demo")},
	} {
		if c.got != c.want {
			t.Errorf("folder %s, want %s", c.got, c.want)
		}
	}
	if !missing(filepath.Join(home, "config")) {
		t.Error("OpenApp made a folder before anything was saved")
	}

	demoRadio := Radio{FreqMHz: 915, BWkHz: 250, SF: 10, CR: 5, TxDBm: 20}
	if err := mf.SaveRadio(testRadio); err != nil {
		t.Fatal(err)
	}
	if err := mf.SaveDraft(rakKey, testDraft); err != nil {
		t.Fatal(err)
	}
	if err := demo.SaveRadio(demoRadio); err != nil {
		t.Fatal(err)
	}
	if err := demo.RememberBoard(rakKey, "RAK WisBlock 4631"); err != nil {
		t.Fatal(err)
	}
	a, err := mf.LoadConfig()
	if err != nil || a.Radio == nil || *a.Radio != testRadio || a.Boards != nil {
		t.Errorf("meshflash's config: %+v, %v", a, err)
	}
	b, err := demo.LoadConfig()
	if err != nil || b.Radio == nil || *b.Radio != demoRadio || b.Boards[rakKey] != "RAK WisBlock 4631" {
		t.Errorf("meshflash-demo's config: %+v, %v", b, err)
	}
	if _, ok := demo.LoadDraft(rakKey); ok {
		t.Error("the demo sees the mf build's draft")
	}
	for _, f := range []string{
		filepath.Join(home, "config", "meshflash", ConfigFile),
		filepath.Join(home, "config", "meshflash", DraftsFile),
		filepath.Join(home, "config", "meshflash-demo", ConfigFile),
	} {
		if missing(f) {
			t.Errorf("%s wasn't written", f)
		}
	}
	if !missing(filepath.Join(home, "config", "meshflash-demo", DraftsFile)) {
		t.Error("the demo has a drafts.json it never saved")
	}
	if !missing(filepath.Join(home, "cache")) {
		t.Error("the store wrote into the cache folder")
	}

	// With no XDG folders set, the ones under $HOME.
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", filepath.Join(home, "void"))
	s, err := OpenApp("meshflash")
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigDir() != filepath.Join(home, "void", ".config", "meshflash") ||
		s.CacheDir() != filepath.Join(home, "void", ".cache", "meshflash") {
		t.Errorf("under $HOME: %s and %s", s.ConfigDir(), s.CacheDir())
	}

	// A relative XDG folder would depend on where meshflash was started from: refused.
	t.Setenv("XDG_CONFIG_HOME", "config")
	if s, err := OpenApp("meshflash"); err == nil {
		t.Errorf("a relative XDG_CONFIG_HOME gave %s", s.ConfigDir())
	}
}

// OpenApp's name is one folder: never ~/.config itself, nor a folder outside it.
func TestOpenAppRefusesOddNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, name := range []string{"", ".", "..", "/", "a/b", "../meshflash", "meshflash/"} {
		if s, err := OpenApp(name); err == nil {
			t.Errorf("OpenApp(%q) gave %s", name, s.ConfigDir())
		}
	}
}

// Changes from many goroutines at once (the screens, a setup's end, the app's shutdown) never undo each other.
func TestConcurrentChanges(t *testing.T) {
	s := newStore(t)
	const n = 24
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			serial := fmt.Sprintf("SERIAL%02d", i)
			key := fmt.Sprintf("usb 1-1.%d 10c4:ea60", i)
			if err := s.RememberBoard(serial, fmt.Sprint("Board ", i)); err != nil {
				t.Error(err)
			}
			if err := s.SaveRadio(Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 5 + i%8, CR: 5 + i%4, TxDBm: i}); err != nil {
				t.Error(err)
			}
			if err := s.SaveDraft(key, Draft{Role: "repeater", Name: fmt.Sprint("Node ", i), Saved: testSaved}); err != nil {
				t.Error(err)
			}
			if _, err := s.LoadConfig(); err != nil {
				t.Error(err)
			}
			if d, ok := s.LoadDraft(key); !ok || d.Name != fmt.Sprint("Node ", i) {
				t.Errorf("draft %d read back as %+v, %v", i, d, ok)
			}
			if i%2 == 1 {
				if err := s.DeleteDraft(key); err != nil {
					t.Error(err)
				}
			}
			if i%3 == 0 {
				if err := s.RememberBoard(serial, ""); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()

	c, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		serial := fmt.Sprintf("SERIAL%02d", i)
		got, had := c.Boards[serial]
		if i%3 == 0 && had {
			t.Errorf("%s was forgotten, yet is %q", serial, got)
		}
		if i%3 != 0 && got != fmt.Sprint("Board ", i) {
			t.Errorf("%s is %q", serial, got)
		}
		d, ok := s.LoadDraft(fmt.Sprintf("usb 1-1.%d 10c4:ea60", i))
		if i%2 == 1 && ok {
			t.Errorf("draft %d was deleted, yet is %+v", i, d)
		}
		if i%2 == 0 && (!ok || d.Name != fmt.Sprint("Node ", i)) {
			t.Errorf("draft %d: %+v, %v", i, d, ok)
		}
	}
	if r := c.Radio; r == nil || r.TxDBm < 0 || r.TxDBm >= n || r.SF != 5+r.TxDBm%8 || r.CR != 5+r.TxDBm%4 {
		t.Errorf("the radio is %+v, not one of those saved whole", r)
	}
	noTempFiles(t, s.ConfigDir())
}

// No type that reaches config.json or drafts.json has a field for a password (S7, S22), nor one of a type that
// could carry anything at all.
func TestDraftHasNoPasswordField(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(path string, ty reflect.Type)
	walk = func(path string, ty reflect.Type) {
		if seen[ty] {
			return
		}
		seen[ty] = true
		switch ty.Kind() {
		case reflect.String, reflect.Int, reflect.Float64:
		case reflect.Pointer, reflect.Slice:
			walk(path+"[]", ty.Elem())
		case reflect.Map:
			walk(path+"[key]", ty.Key())
			walk(path+"[]", ty.Elem())
		case reflect.Struct:
			if ty == reflect.TypeOf(time.Time{}) {
				return
			}
			for i := range ty.NumField() {
				f := ty.Field(i)
				key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
				if strings.Contains(strings.ToLower(f.Name), "pass") || strings.Contains(strings.ToLower(key), "pass") {
					t.Errorf("%s.%s (json %q) could hold a password", path, f.Name, key)
				}
				walk(path+"."+f.Name, f.Type)
			}
		default:
			t.Errorf("%s is a %v, which could carry anything", path, ty)
		}
	}
	walk("Config", reflect.TypeOf(Config{}))
	walk("drafts", reflect.TypeOf(map[string]Draft{}))
	if !seen[reflect.TypeOf(Radio{})] || !seen[reflect.TypeOf(Draft{})] {
		t.Error("the walk missed a type")
	}
}

// The formats SPEC.md 8 points at are the examples in types.go's doc comments: each decodes into its type with no
// key left over, and the store writes exactly those keys and values back, on one line.
func TestTheDocumentedFormats(t *testing.T) {
	examples := docExamples(t)
	strict := func(data string, v any) {
		t.Helper()
		dec := json.NewDecoder(strings.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			t.Fatalf("types.go's example %s: %v", data, err)
		}
	}
	sameJSON := func(name string, got []byte, want string) {
		t.Helper()
		var g, w any
		if err := json.Unmarshal(got, &g); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := json.Unmarshal([]byte(want), &w); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s is\n%s\nwant types.go's\n%s", name, got, want)
		}
	}

	var c Config
	strict(examples["Config"], &c)
	if want := (Config{Radio: &testRadio, Boards: map[string]string{rakKey: "RAK WisBlock 4631"}}); !reflect.DeepEqual(c, want) {
		t.Errorf("types.go's config: %+v", c)
	}
	s := newStore(t)
	if err := s.SaveRadio(*c.Radio); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberBoard(rakKey, c.Boards[rakKey]); err != nil {
		t.Fatal(err)
	}
	got := fileBytes(t, filepath.Join(s.ConfigDir(), ConfigFile))
	sameJSON("config.json", got, examples["Config"])
	const line = `{"radio":{"freq_mhz":869.618,"bw_khz":62.5,"sf":8,"cr":8,"tx_dbm":22,"path_hash_bytes":1},` +
		`"boards":{"D2A1F3C4B5E69788":"RAK WisBlock 4631"}}` + "\n"
	if string(got) != line {
		t.Errorf("config.json is %q, want %q", got, line)
	}

	var drafts map[string]Draft
	strict(examples["Draft"], &drafts)
	if d, ok := drafts[rakKey]; len(drafts) != 1 || !ok || !sameDraft(d, testDraft) {
		t.Errorf("types.go's drafts: %+v", drafts)
	}
	if err := s.SaveDraft(rakKey, drafts[rakKey]); err != nil {
		t.Fatal(err)
	}
	sameJSON("drafts.json", fileBytes(t, filepath.Join(s.ConfigDir(), DraftsFile)), examples["Draft"])

	// Written as typed: a name with "&" or "<" isn't escaped to &, so the file reads as the screen did.
	if err := s.RememberBoard("AAAA", "Board <A&B>"); err != nil {
		t.Fatal(err)
	}
	if b := fileBytes(t, filepath.Join(s.ConfigDir(), ConfigFile)); !bytes.Contains(b, []byte(`"Board <A&B>"`)) {
		t.Errorf("config.json is %s", b)
	}
}

// docExamples is the JSON example indented in the doc comment of each type in types.go, by type name.
func docExamples(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "types.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE || gen.Doc == nil {
			continue
		}
		var example []string
		for _, l := range strings.Split(gen.Doc.Text(), "\n") {
			if strings.HasPrefix(l, "\t") {
				example = append(example, strings.TrimPrefix(l, "\t"))
			}
		}
		if len(example) > 0 {
			out[gen.Specs[0].(*ast.TypeSpec).Name.Name] = strings.Join(example, "\n")
		}
	}
	for _, name := range []string{"Config", "Draft"} {
		if out[name] == "" {
			t.Fatalf("types.go's %s has no example in its doc comment", name)
		}
	}
	return out
}

// A write that fails leaves the old file as it was and no temporary file behind; one that fails before writing
// creates nothing.
func TestAFailedWriteLeavesTheOldFile(t *testing.T) {
	s := newStore(t)
	if err := s.SaveRadio(Radio{FreqMHz: math.NaN()}); err == nil {
		t.Error("a NaN frequency was saved")
	}
	if !missing(s.ConfigDir()) {
		t.Error("a radio JSON can't hold made the config folder")
	}
	if err := s.SaveRadio(testRadio); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.ConfigDir(), ConfigFile)
	before := string(fileBytes(t, path))
	if err := s.SaveRadio(Radio{FreqMHz: math.Inf(1)}); err == nil {
		t.Error("an infinite frequency was saved")
	}
	if after := string(fileBytes(t, path)); after != before {
		t.Errorf("config.json was %q, is %q", before, after)
	}

	// drafts.json can't be renamed over: a folder is in its place.
	if err := os.Mkdir(filepath.Join(s.ConfigDir(), DraftsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraft(rakKey, testDraft); err == nil {
		t.Error("a draft was saved over a folder")
	}
	if info, err := os.Stat(filepath.Join(s.ConfigDir(), DraftsFile)); err != nil || !info.IsDir() {
		t.Errorf("the folder named drafts.json: %v, %v", info, err)
	}
	noTempFiles(t, s.ConfigDir())
	if c, err := s.LoadConfig(); err != nil || c.Radio == nil || *c.Radio != testRadio {
		t.Errorf("config.json after the failed writes: %+v, %v", c, err)
	}
}
