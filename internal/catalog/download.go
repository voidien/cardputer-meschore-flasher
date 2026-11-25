package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// MinFileSize is the smallest body accepted as firmware. MeshCore's FAQ (5.13) warns that a plain wget can get a
// "very small file (10K bytes …)" instead of the firmware; every real image is hundreds of KB.
const MinFileSize = 50 << 10

// Entry is one saved file, as files/index.json records it.
type Entry struct {
	Name   string    `json:"name"`   // the file's own name in <cache>/files
	URL    string    `json:"url"`    // where it came from (the flasher site, or GitHub when that failed)
	Size   int64     `json:"size"`   // bytes
	SHA256 string    `json:"sha256"` // hex, taken while downloading and checked again before every flash
	Saved  time.Time `json:"saved"`  // when it was downloaded (UTC, to the second)
}

type fileIndex struct {
	Files []Entry `json:"files"`
}

func (c *Client) filesDir() string { return filepath.Join(c.cache, "files") }

// safeName is what a saved file may be called: the catalog's own names (letters, digits, "._+-()"), never a path,
// a hidden file, or the index itself.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+()-]{0,199}$`)

func checkName(name string) error {
	if !safeName.MatchString(name) || name == "index.json" {
		return fmt.Errorf("%w: %q is not a name meshflash saves a file under", ErrBadAnswer, name)
	}
	return nil
}

// Download fetches one of the catalog's files and saves it under its own name in <cache>/files, recording its URL,
// size, SHA-256 and time in files/index.json. It is refused, and nothing is saved, when the answer isn't 200, is a
// web page, is compressed, stops before its Content-Length, is under MinFileSize, or isn't the kind of file its name
// says (a .bin not starting with the ESP32 image byte 0xE9, a .zip that doesn't open, a .uf2 without UF2 blocks).
// A MeshCore release file ("?repo=github") that the flasher site fails to give is tried once more from GitHub, which
// must also send a Content-Length; when the answer carries an X-Ms-Blob-Content-Md5 (GitHub's storage does), the
// body must match it. progress, if not nil, is called as bytes arrive with the total, or -1 when it isn't known; when
// the download falls back to GitHub it starts again from 0 with GitHub's total.
func (c *Client) Download(ctx context.Context, f File, progress func(done, total int64)) (Entry, error) {
	return c.DownloadFrom(ctx, f, progress, nil)
}

// DownloadFrom is Download, also telling from (if not nil) each URL as its fetch starts: f.URL, then f.Fallback
// when the flasher site didn't give the file. So a screen can say where the file is coming from without reading an
// error's words.
func (c *Client) DownloadFrom(ctx context.Context, f File, progress func(done, total int64), from func(url string)) (Entry, error) {
	if from == nil {
		from = func(string) {}
	}
	name := f.Base()
	if err := checkName(name); err != nil {
		return Entry{}, err
	}
	if f.Type == "" || f.URL == "" {
		return Entry{}, fmt.Errorf("%w: %s has no address in the catalog", ErrBadAnswer, name)
	}
	if err := os.MkdirAll(c.filesDir(), 0o700); err != nil {
		return Entry{}, err
	}

	used := f.URL
	from(f.URL)
	tmp, size, sum, err := c.fetchFile(ctx, f.URL, name, false, progress)
	if err != nil && f.Fallback != "" && ctx.Err() == nil {
		first := err
		used = f.Fallback
		from(f.Fallback)
		tmp, size, sum, err = c.fetchFile(ctx, f.Fallback, name, true, progress)
		if err != nil {
			err = fmt.Errorf("%w; GitHub too: %w", first, err)
		}
	}
	if err != nil {
		return Entry{}, err
	}
	defer os.Remove(tmp)

	entry := Entry{Name: name, URL: used, Size: size, SHA256: sum, Saved: c.clock.Now().UTC().Truncate(time.Second)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Rename(tmp, filepath.Join(c.filesDir(), name)); err != nil {
		return Entry{}, err
	}
	syncDir(c.filesDir())
	idx, err := c.readIndex()
	if err != nil {
		// A damaged index is started afresh: the files it listed can't be checked any more and must be fetched
		// again, which Verify says when it is asked.
		idx = fileIndex{}
	}
	idx.put(entry)
	if err := c.writeIndex(idx); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// fetchFile downloads one URL into a temporary file in <cache>/files and checks it. It returns the temporary file's
// path, the size and the SHA-256.
func (c *Client) fetchFile(ctx context.Context, u, name string, fromGitHub bool, progress func(done, total int64)) (string, int64, string, error) {
	ctx, cancel := context.WithTimeout(ctx, DownloadTimeout)
	defer cancel()
	// Silence for DownloadStall stops the download; the timer restarts with every read.
	var stalled atomic.Bool
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	watchdog := time.AfterFunc(DownloadStall, func() { stalled.Store(true); stop() })
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0, "", &FetchError{URL: u, Err: err}
	}
	req.Header.Set("User-Agent", c.ua)
	// Plain bytes, so the body can be checked against Content-Length and the MD5 as sent.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		if stalled.Load() {
			return "", 0, "", &FetchError{URL: u, Err: fmt.Errorf("%w (no answer for %v)", ErrUnreachable, DownloadStall)}
		}
		return "", 0, "", netError(ctx, u, err)
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	if status != http.StatusOK {
		return "", 0, "", badAnswer(u, status, "HTTP %d", status)
	}
	if isHTMLType(resp.Header.Get("Content-Type")) {
		return "", 0, "", badAnswer(u, status, "got a web page, not %s", name)
	}
	if enc := strings.ToLower(resp.Header.Get("Content-Encoding")); enc != "" && enc != "identity" {
		return "", 0, "", badAnswer(u, status, "the file came compressed (%s)", enc)
	}
	total := resp.ContentLength
	if fromGitHub && total < 0 {
		return "", 0, "", badAnswer(u, status, "no Content-Length, so the download can't be checked")
	}
	if total > maxFile {
		return "", 0, "", badAnswer(u, status, "%d bytes: too big for firmware", total)
	}
	var wantMD5 []byte
	if h := resp.Header.Get("X-Ms-Blob-Content-Md5"); h != "" {
		if wantMD5, err = base64.StdEncoding.DecodeString(h); err != nil || len(wantMD5) != md5.Size {
			return "", 0, "", badAnswer(u, status, "unreadable X-Ms-Blob-Content-Md5 %q", h)
		}
	}

	tmp, err := os.CreateTemp(c.filesDir(), ".tmp-*")
	if err != nil {
		return "", 0, "", err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return "", 0, "", err
	}
	sha, md := sha256.New(), md5.New()
	head := &headWriter{max: esp32BootloaderOffset + 1}
	w := io.MultiWriter(tmp, sha, md, head)
	var done int64
	buf := make([]byte, 32<<10)
	body := io.LimitReader(resp.Body, maxFile+1)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			watchdog.Reset(DownloadStall)
			if _, err := w.Write(buf[:n]); err != nil {
				return "", 0, "", err
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			switch {
			case stalled.Load():
				return "", 0, "", &FetchError{URL: u, Status: status,
					Err: fmt.Errorf("%w (nothing received for %v, after %d bytes)", ErrUnreachable, DownloadStall, done)}
			case errors.Is(rerr, io.ErrUnexpectedEOF) && total >= 0:
				return "", 0, "", badAnswer(u, status, "the download stopped early: %d of %d bytes", done, total)
			}
			return "", 0, "", netError(ctx, u, rerr)
		}
	}
	switch {
	case done > maxFile:
		return "", 0, "", badAnswer(u, status, "more than %d MB: too big for firmware", maxFile>>20)
	case total >= 0 && done != total:
		return "", 0, "", badAnswer(u, status, "the download stopped early: %d of %d bytes", done, total)
	case looksLikeHTML(head.b):
		return "", 0, "", badAnswer(u, status, "got a web page, not %s", name)
	case done < MinFileSize:
		return "", 0, "", badAnswer(u, status, "only %d bytes: too small to be firmware (an error page?)", done)
	case wantMD5 != nil && !bytes.Equal(md.Sum(nil), wantMD5):
		return "", 0, "", badAnswer(u, status, "the body doesn't match its X-Ms-Blob-Content-Md5")
	}
	if err := checkContent(tmp, name, head.b, done); err != nil {
		return "", 0, "", badAnswer(u, status, "%v", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, "", err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, "", err
	}
	ok = true
	return tmp.Name(), done, hex.EncodeToString(sha.Sum(nil)), nil
}

// headWriter keeps the first bytes written through it.
type headWriter struct {
	b   []byte
	max int
}

func (h *headWriter) Write(p []byte) (int, error) {
	if room := h.max - len(h.b); room > 0 {
		h.b = append(h.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// An ESP32 image header starts with 0xE9; a classic ESP32's second-stage bootloader sits at 0x1000.
const (
	espImageMagic         = 0xE9
	esp32BootloaderOffset = 0x1000
)

func allFF(b []byte) bool {
	for _, c := range b {
		if c != 0xFF {
			return false
		}
	}
	return true
}

// UF2 block magic numbers (https://github.com/microsoft/uf2): the start of every 512-byte block.
const (
	uf2Magic0 = 0x0A324655
	uf2Magic1 = 0x9E5D5157
)

// checkContent checks the file is what its name says: an ESP32 image starts with 0xE9, or a classic ESP32's merged
// image has it at 0x1000 (docs/meshcore-flashing.md 2.6); a DFU package opens as a zip; a .uf2 starts with a UF2
// block. Deeper checks (chip ID, the DFU manifest and CRC) belong to internal/esp and internal/nrfdfu.
func checkContent(f *os.File, name string, head []byte, size int64) error {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".bin":
		if len(head) > 0 && head[0] == espImageMagic {
			return nil
		}
		// A classic ESP32's merged image has its bootloader at 0x1000, after 4 KB of 0xFF (the Heltec v2 sample
		// starts ff ff ff ff, with 0xE9 at 0x1000); S3 and C3 put it at 0x0.
		if strings.HasSuffix(strings.ToLower(name), "-merged.bin") && len(head) > esp32BootloaderOffset &&
			head[esp32BootloaderOffset] == espImageMagic && allFF(head[:esp32BootloaderOffset]) {
			return nil
		}
		return fmt.Errorf("%s doesn't start like an ESP32 image (0xE9)", name)
	case ".zip":
		if _, err := zip.NewReader(f, size); err != nil {
			return fmt.Errorf("%s doesn't open as a zip: %v", name, err)
		}
	case ".uf2":
		if len(head) < 8 || binary.LittleEndian.Uint32(head) != uf2Magic0 || binary.LittleEndian.Uint32(head[4:]) != uf2Magic1 {
			return fmt.Errorf("%s doesn't start with a UF2 block", name)
		}
	}
	return nil
}

func (c *Client) readIndex() (fileIndex, error) {
	var idx fileIndex
	data, err := os.ReadFile(filepath.Join(c.filesDir(), "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return idx, err
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return fileIndex{}, fmt.Errorf("%w: files/index.json: %v", ErrDamaged, err)
	}
	return idx, nil
}

func (c *Client) writeIndex(idx fileIndex) error {
	sort.Slice(idx.Files, func(i, j int) bool { return idx.Files[i].Name < idx.Files[j].Name })
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(c.filesDir(), "index.json"), append(data, '\n'))
}

func (idx *fileIndex) put(e Entry) {
	for i := range idx.Files {
		if idx.Files[i].Name == e.Name {
			idx.Files[i] = e
			return
		}
	}
	idx.Files = append(idx.Files, e)
}

func (idx *fileIndex) get(name string) (Entry, bool) {
	for _, e := range idx.Files {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// ErrNotSaved: the file isn't in files/index.json (never downloaded, or deleted).
var ErrNotSaved = errors.New("not saved")

// Saved says whether this exact catalog file is saved: its name is in the index, from its URL or its GitHub
// fallback. It doesn't hash the file; Verify does.
func (c *Client) Saved(f File) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx, err := c.readIndex()
	if err != nil {
		return Entry{}, false
	}
	e, ok := idx.get(f.Base())
	if !ok || (e.URL != f.URL && (f.Fallback == "" || e.URL != f.Fallback)) {
		return Entry{}, false
	}
	if _, err := os.Stat(filepath.Join(c.filesDir(), e.Name)); err != nil {
		return Entry{}, false
	}
	return e, true
}

// Verify hashes a saved file again and checks it against the size and SHA-256 recorded when it was downloaded. Run
// it before every flash: ErrDamaged when the file changed, ErrNotSaved when it isn't there. A Download of the same
// name running at the same time is waited for, so it never reads the new file against the old record.
func (c *Client) Verify(name string) (Entry, error) {
	_, e, err := c.readVerified(name, false)
	return e, err
}

// ReadVerified reads a saved file whole and checks those very bytes against the index, so what is flashed is what
// was checked.
func (c *Client) ReadVerified(name string) ([]byte, Entry, error) {
	return c.readVerified(name, true)
}

func (c *Client) readVerified(name string, keep bool) ([]byte, Entry, error) {
	if err := checkName(name); err != nil {
		return nil, Entry{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	idx, err := c.readIndex()
	if err != nil {
		return nil, Entry{}, err
	}
	e, ok := idx.get(name)
	if !ok {
		return nil, Entry{}, fmt.Errorf("%s: %w", name, ErrNotSaved)
	}
	f, err := os.Open(filepath.Join(c.filesDir(), name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, e, fmt.Errorf("%s: %w (it was deleted)", name, ErrNotSaved)
	}
	if err != nil {
		return nil, e, err
	}
	defer f.Close()
	var h hash.Hash = sha256.New()
	var buf bytes.Buffer
	w := io.Writer(h)
	if keep {
		w = io.MultiWriter(h, &buf)
	}
	n, err := io.Copy(w, io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, e, err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); n != e.Size || sum != e.SHA256 {
		return nil, e, fmt.Errorf("%w: %s is not the file downloaded on %s (SHA-256 %.8s… is now %.8s…); download it again",
			ErrDamaged, name, e.Saved.Format(time.DateOnly), e.SHA256, sum)
	}
	if !keep {
		return nil, e, nil
	}
	return buf.Bytes(), e, nil
}

// List gives the saved files whose file is still there, newest first.
func (c *Client) List() ([]Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx, err := c.readIndex()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range idx.Files {
		if checkName(e.Name) != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(c.filesDir(), e.Name)); err == nil {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Saved.Equal(out[j].Saved) {
			return out[i].Saved.After(out[j].Saved)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Delete removes a saved file and its index entry. Deleting a file that isn't there is not an error.
func (c *Client) Delete(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Remove(filepath.Join(c.filesDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	idx, err := c.readIndex()
	if err != nil {
		return err
	}
	kept := idx.Files[:0]
	for _, e := range idx.Files {
		if e.Name != name {
			kept = append(kept, e)
		}
	}
	idx.Files = kept
	return c.writeIndex(idx)
}
