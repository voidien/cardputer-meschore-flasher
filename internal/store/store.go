package store

// Phase 2 contract (docs/phase2-design.md): builder A writes the bodies. The signatures and doc comments are fixed;
// the placeholders below return a "not built yet" error (LoadConfig and LoadDraft return nothing saved), so callers
// compile and run before the store is written.

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var errNotBuilt = errors.New("store: not built yet (Phase 2 contract placeholder)")

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
func (s *Store) LoadConfig() (Config, error) { return Config{}, nil }

// RememberBoard records the catalog board picked for a board's USB serial number (flash.Port.Key), keeping
// everything else in config.json. An empty serial is ErrNoKey; an empty device forgets the serial.
func (s *Store) RememberBoard(serial, device string) error { return errNotBuilt }

// SaveRadio records the radio settings a setup sent, keeping the boards.
func (s *Store) SaveRadio(r Radio) error { return errNotBuilt }

// LoadDraft is the unsent setup form kept under key (flash.Port.DraftKey), and whether there is one. It never fails:
// a missing or damaged drafts.json has no drafts.
func (s *Store) LoadDraft(key string) (Draft, bool) { return Draft{}, false }

// SaveDraft keeps an unsent setup form under key (flash.Port.DraftKey), replacing the one it had, keeping every
// other board's. An unreadable drafts.json is replaced. An empty key is ErrNoKey. Whether a form is worth keeping
// (changed from its defaults) is the caller's to decide: SaveDraft saves what it is given.
func (s *Store) SaveDraft(key string, d Draft) error { return errNotBuilt }

// DeleteDraft removes the draft kept under key (after the setup was sent and worked, or the form went back to its
// defaults). A key with no draft is not an error.
func (s *Store) DeleteDraft(key string) error { return errNotBuilt }
