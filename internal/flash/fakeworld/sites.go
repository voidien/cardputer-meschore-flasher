package fakeworld

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// The flasher sites, as internal/catalog's own test server lays them out (catalog/server_test.go): MeshCore's under
// /mc, ZephCore's under /zc, GitHub's release downloads under /gh, all on one handler and looked up by path and
// query whatever the host. Deps reaches it through an http.Client whose transport calls the handler directly (no
// socket at all); DepsOver through an httptest.Server on the loopback interface.

// failure is a FailPath: every request whose path starts with prefix gets status and ctype.
type failure struct {
	prefix string
	status int
	ctype  string
}

// quiet marks the requests made while Deps saves the catalog for withCatalog: they are not the owner's, so Requests
// leaves them out.
type quietKey struct{}

func (w *World) serveHTTP(rw http.ResponseWriter, r *http.Request) {
	key := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	w.mu.Lock()
	if r.Context().Value(quietKey{}) == nil {
		w.requests = append(w.requests, key)
	}
	var fail *failure
	for i := range w.fails {
		if strings.HasPrefix(r.URL.Path, w.fails[i].prefix) {
			f := w.fails[i]
			fail = &f
		}
	}
	w.mu.Unlock()
	if fail != nil {
		body := []byte("the fake site was told to fail this path\n")
		if strings.Contains(fail.ctype, "html") {
			body = []byte("<!DOCTYPE html><html><head><title>Sign in to the Wi-Fi</title></head><body>Sign in</body></html>\n")
		}
		if fail.ctype != "" {
			rw.Header().Set("Content-Type", fail.ctype)
		}
		rw.Header().Set("Content-Length", fmt.Sprint(len(body)))
		rw.WriteHeader(fail.status)
		rw.Write(body)
		return
	}
	if err := loadCommitted(); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if body, ok := committed.raw[key]; ok {
		rw.Header().Set("Content-Type", "application/json; charset=utf-8")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			var b bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&b, gzip.BestSpeed)
			zw.Write(body)
			zw.Close()
			body = b.Bytes()
			rw.Header().Set("Content-Encoding", "gzip")
		}
		rw.Header().Set("Content-Length", fmt.Sprint(len(body)))
		rw.WriteHeader(http.StatusOK)
		rw.Write(body)
		return
	}
	fi, ok := committed.files[key]
	if !ok {
		http.Error(rw, "not found", http.StatusNotFound)
		return
	}
	body := w.fileBytes(fi)
	rw.Header().Set("Content-Type", "application/octet-stream")
	rw.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if strings.HasPrefix(key, "/gh/") {
		// GitHub's storage sends the body's MD5, which the catalog client checks on the fallback.
		sum := md5.Sum(body)
		rw.Header().Set("X-Ms-Blob-Content-Md5", base64.StdEncoding.EncodeToString(sum[:]))
	}
	rw.WriteHeader(http.StatusOK)
	rw.Write(body)
}

// fileBytes is the file the sites serve for fi now: made up from its name, unless ServeAs or ServeSoftDevice changed
// what it holds.
func (w *World) fileBytes(fi *fileInfo) []byte {
	w.mu.Lock()
	fw, as := w.serveAs[fi.base]
	sd, hasSD := w.serveSD[fi.base]
	size := w.appSize
	w.mu.Unlock()
	if !as {
		fw = firmwareOf(fi)
	}
	if !hasSD {
		sd = fi.device.SoftDevice
	}
	return serve(fi, fw, sd, size)
}

// ServeAs makes the sites serve file (a saved name, catalog.File.Base) holding fw instead of what its name says:
// a file a board then comes back from running another version, or nothing that answers (Role "companion").
func (w *World) ServeAs(file string, fw Firmware) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.serveAs[file] = fw
}

// ServeSoftDevice makes the sites serve the DFU package file built for SoftDevice sd (an nrfdfu.SoftDevice… ID)
// rather than its board's: a package the backend must refuse before it touches the board.
func (w *World) ServeSoftDevice(file string, sd uint16) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.serveSD[file] = sd
}

// memTransport answers a request by calling the handler in this goroutine: no socket, no port.
type memTransport struct{ w *World }

func (t memTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.w.offlineErr(); err != nil {
		return nil, err
	}
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	rec := httptest.NewRecorder()
	t.w.serveHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	// The world's own requests (the demo saving its catalog at start: quietKey) aren't the owner's Wi-Fi: never paced.
	if rate := t.w.fetchRate; rate > 0 && r.Context().Value(quietKey{}) == nil {
		resp.Body = &pacedBody{r: resp.Body, rate: rate, ctx: r.Context()}
	}
	return resp, nil
}

// pacedBody hands out a response body at rate bytes a second of real time: the demo's (World.fetchRate), so a
// download shows on the Download screen as it would over Wi-Fi rather than ending before it can be seen. Tests never
// pace: everything there is virtual time.
type pacedBody struct {
	r    io.ReadCloser
	rate int
	ctx  context.Context
}

func (b *pacedBody) Read(p []byte) (int, error) {
	if len(p) > 8<<10 {
		p = p[:8<<10]
	}
	n, err := b.r.Read(p)
	if n > 0 {
		select {
		case <-time.After(time.Duration(n) * time.Second / time.Duration(b.rate)):
		case <-b.ctx.Done():
			return n, b.ctx.Err()
		}
	}
	return n, err
}

func (b *pacedBody) Close() error { return b.r.Close() }

// offlineTransport is DepsOver's: SetOffline fails a request before it leaves, as no network does; otherwise it goes
// to the httptest server.
type offlineTransport struct {
	w    *World
	next http.RoundTripper
}

func (t offlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.w.offlineErr(); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(r)
}

// offlineErr is what no network looks like to net/http: a dial that fails, which internal/catalog words as
// catalog.ErrUnreachable.
func (w *World) offlineErr() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.offline {
		return nil
	}
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("network is unreachable (fakeworld: offline)")}
}

// quietClient is an http.Client over the handler whose requests Requests leaves out.
func (w *World) quietClient() *http.Client {
	return &http.Client{Transport: quietTransport{memTransport{w}}}
}

type quietTransport struct{ next http.RoundTripper }

func (t quietTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.next.RoundTrip(r.WithContext(context.WithValue(r.Context(), quietKey{}, true)))
}
