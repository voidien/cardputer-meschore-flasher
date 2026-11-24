package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"meshflash/internal/serialport"
)

// Timeouts and limits for talking to the servers. The Zero's Wi-Fi can be slow on a hilltop, so a download is
// bounded by silence (stall), not by its total time alone.
const (
	ConnectTimeout  = 15 * time.Second // opening a connection, and the TLS handshake
	HeaderTimeout   = 30 * time.Second // from the request to the response's headers
	CatalogTimeout  = 2 * time.Minute  // one catalog or release list, body included
	DownloadStall   = 30 * time.Second // a download that receives nothing this long is stopped
	DownloadTimeout = 10 * time.Minute // a whole firmware download

	maxJSON = 32 << 20 // a catalog or release list (about 1 MB today)
	maxFile = 16 << 20 // a firmware file: no ESP32 flash or nRF52 app is bigger
)

// Errors a Client returns, for errors.Is. Every error also says, in words, which URL and why.
var (
	// ErrNoCatalog: nothing is saved yet. Join Wi-Fi and update.
	ErrNoCatalog = errors.New("no catalog saved")
	// ErrUnreachable: no answer from the server (no network, a timeout, a refused connection).
	ErrUnreachable = errors.New("no network, or the server didn't answer")
	// ErrBadAnswer: the server answered with something other than what was asked for (an HTTP error, a web page,
	// broken JSON, a file that isn't firmware).
	ErrBadAnswer = errors.New("the server's answer isn't usable")
	// ErrDamaged: a saved file no longer matches what was recorded when it was saved.
	ErrDamaged = errors.New("the saved copy has changed")
)

// FetchError is a failed request: the URL, the HTTP status when there was one, and why.
type FetchError struct {
	URL    string // what was asked for
	Status int    // 0 when no HTTP answer came
	Err    error  // why, wrapping ErrUnreachable or ErrBadAnswer
}

// Error names the URL and says why, for the screen.
func (e *FetchError) Error() string { return e.URL + ": " + e.Err.Error() }

// Unwrap lets errors.Is find ErrUnreachable or ErrBadAnswer.
func (e *FetchError) Unwrap() error { return e.Err }

// Options adjust a Client; the zero value talks to the real servers with the wall clock.
type Options struct {
	Sites Sites            // zero: DefaultSites
	HTTP  *http.Client     // nil: a client with the timeouts above that refuses https→http redirects
	Clock serialport.Clock // nil: the wall clock
}

// Client fetches the catalog and firmware files and keeps them under one cache folder:
//
//	<cache>/catalog/   config.json, config-zephcore.json, releases-<key>.json as fetched, and fetched.json (when,
//	                   from where, sizes, SHA-256s)
//	<cache>/files/     each downloaded file under its own name, and index.json (name, URL, size, SHA-256, when)
//
// Everything it writes is the owner's only (files 0600, folders 0700) and is replaced whole, never half-written. It
// does nothing in the background: every request is one the owner asked for. It is safe to use from several
// goroutines.
type Client struct {
	cache string
	sites Sites
	ua    string
	http  *http.Client
	clock serialport.Clock

	// mu is held while the catalog folder or files/ (a file and its index.json entry) is changed or read, so a reader
	// never sees half of a change: the network work of Update and Download happens outside it.
	mu sync.Mutex
}

// DefaultCacheDir is ~/.cache/meshflash (or $XDG_CACHE_HOME/meshflash).
func DefaultCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "meshflash"), nil
}

// NewClient makes a Client keeping its files under cacheDir. version is meshflash's, for the User-Agent.
func NewClient(cacheDir, version string, o Options) *Client {
	if o.Sites == (Sites{}) {
		o.Sites = DefaultSites
	}
	if o.HTTP == nil {
		o.HTTP = newHTTPClient()
	}
	if o.Clock == nil {
		o.Clock = serialport.RealClock
	}
	return &Client{cache: cacheDir, sites: o.Sites, ua: UserAgent(version), http: o.HTTP, clock: o.Clock}
}

// UserAgent is the User-Agent meshflash sends: its name and version inside the browser form the flasher site needs
// before it serves a whole file (MeshCore FAQ 5.13; SPEC.md 2, agreed with the owner).
func UserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return "Mozilla/5.0 (compatible; meshflash/" + version + ")"
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   ConnectTimeout,
			ResponseHeaderTimeout: HeaderTimeout,
			IdleConnTimeout:       30 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: checkRedirect,
	}
}

// checkRedirect follows up to 10 redirects (GitHub sends one, to its asset storage) but never from https to http.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refused a redirect from https to %s", req.URL.Scheme)
	}
	return nil
}

// netError words a failed request: a timeout or no network is ErrUnreachable, anything else a bad answer.
func netError(ctx context.Context, u string, err error) error {
	var ne net.Error
	switch {
	case errors.Is(err, context.Canceled) && ctx.Err() != nil:
		return &FetchError{URL: u, Err: fmt.Errorf("stopped: %w", context.Canceled)}
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return &FetchError{URL: u, Err: fmt.Errorf("%w (timed out)", ErrUnreachable)}
	}
	var oe *net.OpError
	var de *net.DNSError
	if errors.As(err, &oe) || errors.As(err, &de) {
		return &FetchError{URL: u, Err: fmt.Errorf("%w (%v)", ErrUnreachable, unwrapURL(err))}
	}
	return &FetchError{URL: u, Err: fmt.Errorf("%w: %v", ErrBadAnswer, unwrapURL(err))}
}

// unwrapURL drops net/http's `Get "https://…":` prefix, since FetchError already names the URL.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func badAnswer(u string, status int, format string, a ...any) error {
	return &FetchError{URL: u, Status: status, Err: fmt.Errorf("%w: "+format, append([]any{ErrBadAnswer}, a...)...)}
}

// isHTMLType says whether a Content-Type is a web page.
func isHTMLType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}

// looksLikeHTML says whether a body starts like a web page, whatever its Content-Type says.
func looksLikeHTML(b []byte) bool {
	b = bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), " \t\r\n")
	if len(b) > 64 {
		b = b[:64]
	}
	s := strings.ToLower(string(b))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html") || strings.HasPrefix(s, "<head") ||
		strings.HasPrefix(s, "<body")
}

// countingReader counts the bytes that came over the network, before any gzip decoding.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// getJSON fetches one catalog or release list. It asks for gzip itself, so it can count what came over the network
// (Help says how much an update downloads), and refuses an HTTP error, a web page (a Wi-Fi sign-in page answers 200
// with HTML) and a body too big to be a catalog. It returns the decoded body and the bytes received.
func (c *Client) getJSON(ctx context.Context, u string) ([]byte, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, CatalogTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, &FetchError{URL: u, Err: err}
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, netError(ctx, u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, badAnswer(u, resp.StatusCode, "HTTP %d", resp.StatusCode)
	}
	if isHTMLType(resp.Header.Get("Content-Type")) {
		return nil, 0, badAnswer(u, resp.StatusCode, "got a web page, not the catalog (a Wi-Fi sign-in page?)")
	}
	wire := &countingReader{r: resp.Body}
	var body io.Reader = wire
	switch enc := strings.ToLower(resp.Header.Get("Content-Encoding")); enc {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(wire)
		if err != nil {
			return nil, wire.n, badAnswer(u, resp.StatusCode, "broken gzip: %v", err)
		}
		body = zr
	default:
		return nil, 0, badAnswer(u, resp.StatusCode, "unknown Content-Encoding %q", enc)
	}
	data, err := io.ReadAll(io.LimitReader(body, maxJSON+1))
	if err != nil {
		return nil, wire.n, netError(ctx, u, err)
	}
	if len(data) > maxJSON {
		return nil, wire.n, badAnswer(u, resp.StatusCode, "more than %d MB: not a catalog", maxJSON>>20)
	}
	if looksLikeHTML(data) {
		return nil, wire.n, badAnswer(u, resp.StatusCode, "got a web page, not the catalog (a Wi-Fi sign-in page?)")
	}
	return data, wire.n, nil
}

// fetchedFile is one file of a catalog update, as fetched and as saved.
type fetchedFile struct {
	File       string `json:"file"`   // its name in <cache>/catalog
	Source     string `json:"source"` // SourceMeshCore or SourceZephCore
	Key        string `json:"key,omitempty"`
	URL        string `json:"url"`
	Size       int64  `json:"size"`
	Downloaded int64  `json:"downloaded"`
	SHA256     string `json:"sha256"`

	body []byte
}

// catalogManifest is <cache>/catalog/fetched.json, written last: a catalog folder without one is incomplete.
type catalogManifest struct {
	Fetched   time.Time     `json:"fetched"`
	UserAgent string        `json:"userAgent"`
	Files     []fetchedFile `json:"files"`
}

// releaseKeyName is what a release source key may look like: "github", "github-ripple", "github-zephcore". It goes
// into a URL and a file name, so nothing else is accepted.
var releaseKeyName = regexp.MustCompile(`^github[A-Za-z0-9_.-]{0,40}$`)

// Update fetches both catalogs and every release list they name (today: config.json, /releases?repo=github and
// /releases?repo=github-ripple from flasher.meshcore.io; config-zephcore.json and /releases?repo=github-zephcore
// from zephcore.meshcore.dev), resolves them, and only then replaces the saved copy. On any failure the saved
// catalog stays as it was and the error names the URL and why. The release lists asked for are the ones each
// catalog names, as js/catalog.js loadCatalog asks for them.
func (c *Client) Update(ctx context.Context) (*Catalog, error) {
	var files []fetchedFile
	raws := map[string]*Raw{}
	for _, src := range []struct{ source, site, config string }{
		{SourceMeshCore, c.sites.MeshCore, "config.json"},
		{SourceZephCore, c.sites.ZephCore, "config-zephcore.json"},
	} {
		raw := &Raw{Releases: map[string][]byte{}}
		raws[src.source] = raw
		u := src.site + "/" + src.config
		body, wire, err := c.getJSON(ctx, u)
		if err != nil {
			return nil, err
		}
		cfg, err := parseJS(body)
		if err == nil && cfg.kind != kObject {
			err = errors.New("not a JSON object")
		}
		var keys []string
		if err == nil {
			keys, err = releaseKeys(cfg)
		}
		if err != nil {
			return nil, badAnswer(u, http.StatusOK, "not a catalog: %v", err)
		}
		raw.Config = body
		files = append(files, fetchedFile{File: src.config, Source: src.source, URL: u, Downloaded: wire, body: body})

		for _, key := range keys {
			if !releaseKeyName.MatchString(key) {
				return nil, badAnswer(u, http.StatusOK, "release source %q is not a name meshflash accepts", key)
			}
			ru := src.site + "/releases?repo=" + url.QueryEscape(key)
			body, wire, err := c.getJSON(ctx, ru)
			if err != nil {
				return nil, err
			}
			if v, err := parseJS(body); err != nil || v.kind != kArray {
				if err == nil {
					err = errors.New("not a JSON array")
				}
				return nil, badAnswer(ru, http.StatusOK, "not a release list: %v", err)
			}
			raw.Releases[key] = body
			files = append(files, fetchedFile{File: "releases-" + key + ".json", Source: src.source, Key: key, URL: ru,
				Downloaded: wire, body: body})
		}
	}
	// Two catalogs could name the same key one day; their files must not collide.
	seen := map[string]bool{}
	for i := range files {
		if seen[files[i].File] {
			files[i].File = files[i].Source + "-" + files[i].File
		}
		seen[files[i].File] = true
	}

	cat, err := Resolve(*raws[SourceMeshCore], *raws[SourceZephCore], c.sites)
	if err != nil {
		return nil, fmt.Errorf("%w: the new catalog can't be read, so the saved one stays: %v", ErrBadAnswer, err)
	}
	m := catalogManifest{Fetched: c.clock.Now().UTC().Truncate(time.Second), UserAgent: c.ua, Files: files}
	for i := range m.Files {
		f := &m.Files[i]
		sum := sha256.Sum256(f.body)
		f.Size, f.SHA256 = int64(len(f.body)), hex.EncodeToString(sum[:])
		cat.Size += f.Size
		cat.Downloaded += f.Downloaded
	}
	cat.Fetched = m.Fetched
	if err := c.saveCatalog(m); err != nil {
		return nil, fmt.Errorf("saving the catalog: %w", err)
	}
	return cat, nil
}

func (c *Client) catalogDir() string { return filepath.Join(c.cache, "catalog") }

// saveCatalog replaces <cache>/catalog as a whole: the new files go into catalog.new, which then takes the old
// folder's place by two renames. A crash between them leaves catalog.old, which Load still reads.
func (c *Client) saveCatalog(m catalogManifest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := c.catalogDir()
	tmp, old := dir+".new", dir+".old"
	if err := os.MkdirAll(c.cache, 0o700); err != nil {
		return err
	}
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return err
	}
	for _, f := range m.Files {
		if err := writeSynced(filepath.Join(tmp, f.File), f.body); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeSynced(filepath.Join(tmp, "fetched.json"), append(data, '\n')); err != nil {
		return err
	}
	syncDir(tmp)
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(old); err != nil {
			return err
		}
		if err := os.Rename(dir, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	syncDir(c.cache)
	return os.RemoveAll(old)
}

// Load reads the saved catalog, with no network, and resolves it. ErrNoCatalog when none is saved; ErrDamaged when
// a saved file doesn't match its recorded SHA-256. An Update running at the same time is waited for, so Load gives
// the old catalog or the new one, never a mix or a false ErrDamaged.
func (c *Client) Load() (*Catalog, error) {
	raws, cat, err := c.readSaved()
	if err != nil {
		return nil, err
	}
	// Resolving takes a while on the Zero; the files are read, so the lock isn't needed for it.
	resolved, err := Resolve(*raws[SourceMeshCore], *raws[SourceZephCore], c.sites)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	resolved.Fetched, resolved.Size, resolved.Downloaded = cat.Fetched, cat.Size, cat.Downloaded
	return resolved, nil
}

// readSaved reads and checks the saved catalog files, holding the lock saveCatalog holds while it swaps the folder.
func (c *Client) readSaved() (map[string]*Raw, *Catalog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := c.catalogDir()
	data, err := os.ReadFile(filepath.Join(dir, "fetched.json"))
	if errors.Is(err, os.ErrNotExist) {
		// An update that stopped between its two renames left the previous catalog here.
		dir = dir + ".old"
		data, err = os.ReadFile(filepath.Join(dir, "fetched.json"))
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNoCatalog
	}
	if err != nil {
		return nil, nil, err
	}
	var m catalogManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, fmt.Errorf("%w: %s: %v", ErrDamaged, filepath.Join(dir, "fetched.json"), err)
	}
	raws := map[string]*Raw{
		SourceMeshCore: {Releases: map[string][]byte{}},
		SourceZephCore: {Releases: map[string][]byte{}},
	}
	cat := &Catalog{Fetched: m.Fetched}
	for _, f := range m.Files {
		raw, ok := raws[f.Source]
		if !ok || f.File != filepath.Base(f.File) || strings.HasPrefix(f.File, ".") {
			return nil, nil, fmt.Errorf("%w: fetched.json lists %q", ErrDamaged, f.File)
		}
		body, err := os.ReadFile(filepath.Join(dir, f.File))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrDamaged, err)
		}
		sum := sha256.Sum256(body)
		if int64(len(body)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 {
			return nil, nil, fmt.Errorf("%w: %s doesn't match its SHA-256 from %s", ErrDamaged, f.File,
				m.Fetched.Format(time.DateTime))
		}
		if f.Key == "" {
			raw.Config = body
		} else {
			raw.Releases[f.Key] = body
		}
		cat.Size += f.Size
		cat.Downloaded += f.Downloaded
	}
	if raws[SourceMeshCore].Config == nil {
		return nil, nil, fmt.Errorf("%w: no MeshCore catalog in %s", ErrDamaged, dir)
	}
	return raws, cat, nil
}

// writeSynced writes a new file, 0600, and flushes it to the card before returning.
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeAtomic replaces path whole: a temporary file in the same folder, flushed, then renamed over it.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	syncDir(dir)
	return nil
}

// syncDir flushes a folder's entries, so a rename survives a power cut. A failure only loses durability.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
