package catalog

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"meshflash/internal/serialport"
)

// fakeResp is one canned answer of fakeServer.
type fakeResp struct {
	status   int    // 0: 200
	ctype    string // Content-Type ("" leaves it to Go's sniffing)
	body     []byte
	gzip     bool              // gzip the body when the request accepts it (the catalog's JSON)
	header   map[string]string // more headers (X-Ms-Blob-Content-Md5, Location, Content-Encoding)
	shortBy  int               // declare a Content-Length this much bigger than the body, then hang up
	noLength bool              // send it chunked, with no Content-Length
}

// fakeServer stands in for flasher.meshcore.io (under /mc), zephcore.meshcore.dev (under /zc) and GitHub (under /gh)
// on one httptest server on the loopback interface. It logs every request and its User-Agent.
type fakeServer struct {
	srv *httptest.Server

	mu     sync.Mutex
	routes map[string]fakeResp // path, plus "?query" when there is one
	log    []string
	agents []string
	encs   []string // the Accept-Encoding of each request
	gz     map[string]gzBody
}

func newFakeServer(t testing.TB) *fakeServer {
	f := &fakeServer{routes: map[string]fakeResp{}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) sites() Sites {
	return Sites{MeshCore: f.srv.URL + "/mc", ZephCore: f.srv.URL + "/zc", GitHub: f.srv.URL + "/gh"}
}

func (f *fakeServer) set(path string, r fakeResp) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = r
}

func (f *fakeServer) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Path
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	f.mu.Lock()
	f.log = append(f.log, key)
	f.agents = append(f.agents, r.Header.Get("User-Agent"))
	f.encs = append(f.encs, r.Header.Get("Accept-Encoding"))
	resp, ok := f.routes[key]
	f.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	status := resp.status
	if status == 0 {
		status = http.StatusOK
	}
	if resp.shortBy > 0 {
		// Declare more than is sent and hang up, as a dropped connection does.
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(buf, "HTTP/1.1 %d OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n", status,
			len(resp.body)+resp.shortBy)
		buf.Write(resp.body)
		buf.Flush()
		conn.Close()
		return
	}
	if resp.ctype != "" {
		w.Header().Set("Content-Type", resp.ctype)
	}
	for k, v := range resp.header {
		w.Header().Set(k, v)
	}
	body := resp.body
	if resp.gzip && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		body = f.gzipped(key, body)
		w.Header().Set("Content-Encoding", "gzip")
	}
	if resp.noLength {
		w.WriteHeader(status)
		if len(body) > 0 {
			w.Write(body[:1])
			w.(http.Flusher).Flush() // headers go out now, so the rest is chunked
			w.Write(body[1:])
		}
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(status)
	w.Write(body)
}

// gzipped compresses a route's body once.
func (f *fakeServer) gzipped(key string, body []byte) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if z, ok := f.gz[key]; ok && z.plain == len(body) {
		return z.body
	}
	var b bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&b, gzip.BestSpeed)
	zw.Write(body)
	zw.Close()
	if f.gz == nil {
		f.gz = map[string]gzBody{}
	}
	f.gz[key] = gzBody{plain: len(body), body: b.Bytes()}
	return b.Bytes()
}

type gzBody struct {
	plain int
	body  []byte
}

// serveCatalog puts the saved live catalogs (the golden's inputs) on the fake sites.
func serveCatalog(t testing.TB, f *fakeServer) {
	json := "application/json; charset=utf-8"
	f.set("/mc/config.json", fakeResp{ctype: json, body: readGz(t, "meshcore-config.json.gz"), gzip: true})
	f.set("/mc/releases?repo=github", fakeResp{ctype: json, body: readGz(t, "meshcore-releases-github.json.gz"), gzip: true})
	// The Ripple list goes plain, so the test sees both kinds of answer.
	f.set("/mc/releases?repo=github-ripple", fakeResp{ctype: json, body: readGz(t, "meshcore-releases-github-ripple.json.gz")})
	f.set("/zc/config-zephcore.json", fakeResp{ctype: json, body: readGz(t, "zephcore-config.json.gz"), gzip: true})
	f.set("/zc/releases?repo=github-zephcore", fakeResp{ctype: json, body: readGz(t, "zephcore-releases-github-zephcore.json.gz"), gzip: true})
}

// serveSmallCatalog puts a small catalog of both kinds on the fake sites: enough for the tests of what happens around
// an update, which don't need the 1.6 MB of the live one.
func serveSmallCatalog(f *fakeServer) {
	json := "application/json"
	f.set("/mc/config.json", fakeResp{ctype: json, gzip: true, body: []byte(`{"staticPath":"/firmware",
		"role":{"repeater":{"title":"Repeater"},"gui":{"title":"Ripple GUI"}},
		"notice":{"warn":"<article>Careful <a href='/firmware/${bootloader}'>OTAFIX</a></article>"},
		"device":[
		{"name":"Heltec v3","type":"esp32","firmware":[
			{"class":"community","role":"repeater","github":{"type":"repeater","files":{
				"flash-wipe":"Heltec_v3_[rR]epeater.*?-merged\\.bin","flash-update":"Heltec_v3_[rR]epeater.*?-[a-f0-9]{7}\\.bin"}}},
			{"class":"ripple","role":"gui","github-ripple":{"type":"t","files":{"flash-update":"Ultra-H3-[^-]+\\.bin"}}}]},
		{"name":"RAK 4631","type":"nrf52","erase":"FLASH_ERASE_nrf52_softdevice_v6.zip","bootloader":["rak-OTAFIX.uf2"],
			"firmware":[{"class":"community","role":"repeater","notice":"warn","github":{"type":"repeater","files":{
				"flash":"RAK_4631_[rR]epeater.*?\\.zip","download":"RAK_4631_[rR]epeater.*?\\.uf2"}}}]}]}`)})
	f.set("/mc/releases?repo=github", fakeResp{ctype: json, body: []byte(`[
		{"version":"v1.18.0","type":"repeater","name":"Repeater v1.18.0","notes":"Release notes: https://blog\r\n","files":[
			{"name":"Heltec_v3_repeater-v1.18.0-81dbae5-merged.bin","url":"/releases/download/repeater-v1.18.0/Heltec_v3_repeater-v1.18.0-81dbae5-merged.bin"},
			{"name":"Heltec_v3_repeater-v1.18.0-81dbae5.bin","url":"/releases/download/repeater-v1.18.0/Heltec_v3_repeater-v1.18.0-81dbae5.bin"},
			{"name":"RAK_4631_repeater-v1.18.0-81dbae5.zip","url":"/releases/download/repeater-v1.18.0/RAK_4631_repeater-v1.18.0-81dbae5.zip"},
			{"name":"RAK_4631_repeater-v1.18.0-81dbae5.uf2","url":"/releases/download/repeater-v1.18.0/RAK_4631_repeater-v1.18.0-81dbae5.uf2"}]}]`)})
	f.set("/mc/releases?repo=github-ripple", fakeResp{ctype: json, body: []byte(`[
		{"version":"v12.3","type":"t","notes":"Change log","files":[{"name":"Ultra-H3-v12.3.bin","url":"/releases/download/t-v12.3/Ultra-H3-v12.3.bin"}]}]`)})
	f.set("/zc/config-zephcore.json", fakeResp{ctype: json, body: []byte(`{"staticPath":"/firmware",
		"role":{"repeater":{"title":"Repeater"}},"notice":{},"device":[
		{"name":"rak 4631 ","type":"nrf52","erase":"ZephCore_SoftDevice_v6_formatter.zip","firmware":[
			{"class":"community","role":"repeater","github-zephcore":{"type":"zephcore","files":{"flash":"rak4631-repeater-[^-]+\\.zip"}}}]},
		{"name":"Seeed Studio Xiao C6","type":"esp32","firmware":[
			{"class":"community","role":"repeater","github-zephcore":{"type":"zephcore","files":{
				"flash-wipe":"xiao_c6-repeater-[^-]+-merged\\.bin","flash-update":"xiao_c6-repeater-[^-]+\\.bin"}}}]}]}`)})
	f.set("/zc/releases?repo=github-zephcore", fakeResp{ctype: json, body: []byte(`[
		{"version":"v20260714.095706","files":[{"name":"old.zip","url":"/old.zip"}]},
		{"version":"1.18.2","type":"zephcore","notes":"Change Log:\nhttps://github.com/liquidraver/ZephCore","files":[
			{"name":"rak4631-repeater-da3513a.zip","url":"/releases/download/1.18.2-zephcore/rak4631-repeater-da3513a.zip"},
			{"name":"xiao_c6-repeater-da3513a-merged.bin","url":"/releases/download/1.18.2-zephcore/xiao_c6-repeater-da3513a-merged.bin"},
			{"name":"xiao_c6-repeater-da3513a.bin","url":"/releases/download/1.18.2-zephcore/xiao_c6-repeater-da3513a.bin"}]}]`)})
}

func newTestClient(t testing.TB, f *fakeServer) (*Client, *serialport.VirtualClock, string) {
	dir := t.TempDir()
	clock := serialport.NewVirtualClock(nil)
	return NewClient(dir, "1.2.3", Options{Sites: f.sites(), Clock: clock}), clock, dir
}
