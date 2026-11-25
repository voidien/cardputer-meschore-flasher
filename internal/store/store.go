package store

// Phase 2 contract (docs/phase2-design.md 6): the signatures and doc comments are fixed; builder A wrote the bodies.
//
// Ported from the CardputerZero app kit's internal/store (code/internal/store/store.go: writeJSON, readJSON and
// LoadConfig's damaged-is-none rule), the owner's own code, part of meshflash under its MIT licence
// (THIRD_PARTY_NOTICES). Where it departs from the kit, and why:
//   - Every write flushes the file and then, after the rename, the folder (internal/catalog's writeAtomic and
//     syncDir), so the rename itself survives a battery that dies just after it. The kit flushed the file only (and
//     not even that for its cache, which has no counterpart here).
//   - Every change re-reads its file under s.mu: the frame, a setup's end and internal/app's shutdown can each write
//     config.json, and none may undo another's change.
//   - An unreadable config.json (not a damaged one) is never written over: it may hold the boards and the radio
//     that fixing its permissions brings back. The kit had no read-modify-write of its config to face this.
//   - SaveDraft saves what it is given; the kit dropped a blank draft itself, which here is the caller's call
//     (whether a form differs from its defaults only the form knows).
//   - A config folder that exists with a wider mode than 0700 is narrowed at the next save, so the package's
//     promise holds whoever made it; OpenApp refuses a name that isn't one plain folder name.
//   - Files are written without HTML escaping, so a board name with an "&" reads as typed in the file.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store is meshflash's config folder (and the name of its cache folder). It is safe for concurrent use: each change
// re-reads the file it changes, under a lock, so two changes never undo each other.
type Store struct {
	configDir string
	cacheDir  string
	mu        sync.Mutex
}

// OpenApp uses ~/.config/<name> and ~/.cache/<name> (or $XDG_CONFIG_HOME/<name> and $XDG_CACHE_HOME/<name>):
// "meshflash", or "meshflash-demo" for the demo build, which never mixes its files with the real one's (S2).
// Nothing is created until something is saved.
func OpenApp(name string) (*Store, error) {
	// One plain folder name, so the files can only land in a folder of the app's own: "" would put config.json
	// straight into ~/.config, and "../x" outside it.
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') ||
		strings.ContainsRune(name, os.PathSeparator) {
		return nil, fmt.Errorf("store: %q isn't an app's folder name", name)
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(cfg, name), filepath.Join(cache, name)), nil
}

// New uses the given folders (tests).
func New(configDir, cacheDir string) *Store { return &Store{configDir: configDir, cacheDir: cacheDir} }

// ConfigDir is the config folder (~/.config/meshflash).
func (s *Store) ConfigDir() string { return s.configDir }

// CacheDir is the cache folder (~/.cache/meshflash): internal/catalog keeps the catalog and the downloaded files in
// it, and internal/app its screen.log. The store writes nothing there.
func (s *Store) CacheDir() string { return s.cacheDir }

// LoadConfig reads config.json. A missing file is the zero Config; so is a damaged one (cut short, not JSON, or the
// wrong shape), so meshflash still starts. Only a file that can't be read at all (permissions) is an error; the
// caller starts anyway without it and says so in screen.log.
func (s *Store) LoadConfig() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := readJSON[Config](s.path(ConfigFile))
	if err != nil {
		return Config{}, err
	}
	return c, nil
}

// RememberBoard records the catalog board picked for a board's USB serial number (flash.Port.Key), keeping
// everything else in config.json. An empty serial is ErrNoKey; an empty device forgets the serial.
func (s *Store) RememberBoard(serial, device string) error {
	if serial == "" {
		return ErrNoKey
	}
	return s.changeConfig(func(c *Config) bool {
		old, had := c.Boards[serial]
		if device == "" {
			delete(c.Boards, serial) // the last one gone leaves an empty map, which omitempty leaves out
			return had
		}
		if had && old == device {
			return false
		}
		if c.Boards == nil {
			c.Boards = map[string]string{}
		}
		c.Boards[serial] = device
		return true
	})
}

// SaveRadio records the radio settings a setup sent, keeping the boards.
func (s *Store) SaveRadio(r Radio) error {
	return s.changeConfig(func(c *Config) bool {
		if c.Radio != nil && *c.Radio == r {
			return false
		}
		c.Radio = &r
		return true
	})
}

// LoadDraft is the unsent setup form kept under key (flash.Port.DraftKey), and whether there is one. It never fails:
// a missing or damaged drafts.json has no drafts.
func (s *Store) LoadDraft(key string) (Draft, bool) {
	if key == "" {
		return Draft{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// An unreadable file has no drafts either: the form then opens at its defaults, and the next save replaces it.
	all, err := readJSON[map[string]Draft](s.path(DraftsFile))
	if err != nil {
		return Draft{}, false
	}
	d, ok := all[key]
	return d, ok
}

// SaveDraft keeps an unsent setup form under key (flash.Port.DraftKey), replacing the one it had, keeping every
// other board's. An unreadable drafts.json is replaced. An empty key is ErrNoKey. Whether a form is worth keeping
// (changed from its defaults) is the caller's to decide: SaveDraft saves what it is given.
func (s *Store) SaveDraft(key string, d Draft) error {
	if key == "" {
		return ErrNoKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(DraftsFile)
	all, err := readJSON[map[string]Draft](path)
	if err != nil || all == nil {
		// Unreadable: LoadDraft already shows it as no drafts, so the form typed now is the only one there is.
		all = map[string]Draft{}
	}
	all[key] = d
	return writeJSON(path, all)
}

// DeleteDraft removes the draft kept under key (after the setup was sent and worked, or the form went back to its
// defaults). A key with no draft is not an error.
func (s *Store) DeleteDraft(key string) error {
	if key == "" {
		return ErrNoKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(DraftsFile)
	all, err := readJSON[map[string]Draft](path)
	if err != nil {
		// The draft may be in it, and would come back once the file can be read: say so rather than claim it's gone.
		return fmt.Errorf("store: the draft can't be removed, since %s can't be read: %w", DraftsFile, err)
	}
	if _, ok := all[key]; !ok {
		return nil // nothing written, so a missing drafts.json stays missing and a damaged one as it was
	}
	delete(all, key)
	return writeJSON(path, all)
}

func (s *Store) path(file string) string { return filepath.Join(s.configDir, file) }

// changeConfig re-reads config.json under the lock, lets change edit it, and writes it back if change says it
// changed anything (so a change that changes nothing creates no file and spends no write on the SD card). A
// damaged file reads as the zero Config and is written over. An unreadable one is left as it is and its error
// returned, as LoadConfig returns it: writing over it would lose for good the boards and radio that fixing its
// permissions would bring back.
func (s *Store) changeConfig(change func(*Config) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(ConfigFile)
	c, err := readJSON[Config](path)
	if err != nil {
		return fmt.Errorf("store: %s was left as it is, since it can't be read: %w", ConfigFile, err)
	}
	if !change(&c) {
		return nil
	}
	return writeJSON(path, c)
}

// readFile is os.ReadFile; the tests replace it to see a file that can't be read, which chmod can't show them when
// they run as root.
var readFile = os.ReadFile

// readJSON reads path as a T. A missing file is the zero T, and so is a damaged one: anything json.Unmarshal refuses
// (cut short by a battery that died, not JSON, the wrong shape, a time that isn't one), so meshflash always starts.
// Only a file that can't be read at all is an error.
func readJSON[T any](path string) (T, error) {
	var zero T
	data, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return zero, nil
	}
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, nil // not half of it: a field read before the damage could pair with the wrong rest
	}
	return v, nil
}

// writeJSON replaces path with v as JSON, whole or not at all, readable by the owner alone (S22): a temporary file in
// the same folder, 0600, flushed to the SD card, renamed over the old one, then the folder flushed. A battery that
// dies at any point leaves the old file or the new one, never half of either.
func writeJSON(path string, v any) error {
	// Encode first, so a value JSON can't hold (a NaN frequency) fails before anything is created.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := privateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmp.Name()) // no half-written file left behind
		}
	}()
	// CreateTemp already makes it 0600; the Chmod says so whatever it does in future.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	renamed = true
	syncDir(dir)
	return nil
}

// privateDir makes sure the config folder exists and is 0700. A folder it makes has its parent flushed, so the first
// file saved can't be lost with the folder's own entry. One that exists with a wider mode (made by hand, or under an
// odd umask) is narrowed to 0700, so the package's promise holds whoever made it; that is best effort, since the
// files in it are 0600 either way and only their names would show.
func privateDir(dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		syncDir(filepath.Dir(dir))
		info, err = os.Stat(dir)
	}
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode().Perm() != 0o700 {
		os.Chmod(dir, 0o700)
	}
	return nil
}

// syncDir flushes a folder's entries, so a rename survives a power cut. A failure only loses durability.
// (internal/catalog/client.go's syncDir, which is unexported there.)
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
