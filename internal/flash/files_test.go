package flash_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
	"meshflash/internal/flash/fakeworld"
	"meshflash/internal/nrfdfu"
)

// route is how a test reaches the sites: in memory (Deps), or through httptest on the loopback interface
// (DepsOver: SPEC.md 10's stand-in for S29, every route against the server).
type route struct {
	name string
	rig  func(t *testing.T, w *fakeworld.World, withCatalog bool) *rig
}

var routes = []route{
	{"memory", func(t *testing.T, w *fakeworld.World, withCatalog bool) *rig { return newRigOver(t, w, withCatalog) }},
	{"httptest", func(t *testing.T, w *fakeworld.World, withCatalog bool) *rig {
		srv := httptest.NewServer(w.Handler())
		t.Cleanup(srv.Close)
		dir := t.TempDir()
		d, err := w.DepsOver(srv, dir, "test", withCatalog)
		if err != nil {
			t.Fatal(err)
		}
		r := &rig{t: t, w: w, clientDir: dir, sites: catalog.Sites{MeshCore: srv.URL + "/mc",
			ZephCore: srv.URL + "/zc", GitHub: srv.URL + "/gh"}}
		d.Logf = func(format string, args ...any) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.logs = append(r.logs, fmt.Sprintf(format, args...))
		}
		r.d, r.b = d, flash.New(d)
		t.Cleanup(func() {
			if p := w.Problems(); len(p) > 0 {
				t.Errorf("the backend broke the world's rules:\n%s", strings.Join(p, "\n"))
			}
		})
		return r
	}},
}

// file is a catalog file by board, source, role and kind, at the rig's sites: the committed catalog's (resolved once
// for the package with fakeworld.Sites), its addresses moved to the httptest server's when the rig uses one, so no
// test loads a catalog it isn't testing (about a second each under -race).
func file(t *testing.T, r *rig, device, source, role string, kind catalog.Kind) catalog.File {
	t.Helper()
	d := committed(t).Find(device)
	for _, fw := range d.Firmware {
		if fw.Source == source && fw.Role == role {
			fl, err := catalog.Choose(*d, fw.Versions[0], kind)
			if err != nil {
				t.Fatal(err)
			}
			f := fl.File
			if r.sites != (catalog.Sites{}) {
				move := strings.NewReplacer(fakeworld.Sites.MeshCore, r.sites.MeshCore,
					fakeworld.Sites.ZephCore, r.sites.ZephCore, fakeworld.Sites.GitHub, r.sites.GitHub)
				f.URL, f.Fallback = move.Replace(f.URL), move.Replace(f.Fallback)
			}
			return f
		}
	}
	t.Fatalf("no %s %s for %s", source, role, device)
	return catalog.File{}
}

func TestDownload(t *testing.T) {
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			t.Run("progress", func(t *testing.T) {
				r := rt.rig(t, fakeworld.New(), false)
				f := file(t, r, "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Wipe)
				j := r.b.Download(context.Background(), []catalog.File{f})
				var parts []int64
				var seen uint64
				for {
					p, err := j.Next(context.Background(), seen)
					if err != nil {
						t.Fatal(err)
					}
					if len(p.Steps) == 1 {
						parts = append(parts, p.Steps[0].Done)
					}
					seen = p.Seq
					if p.Done {
						o := p.Outcome
						if !o.OK || o.Title != "Downloaded" || len(o.Entries) != 1 || o.Entries[0].Name != f.Base() ||
							p.Steps[0].Text != f.Base() || p.Steps[0].State != flash.StepDone ||
							p.Steps[0].Done != p.Steps[0].Total || o.Entries[0].URL != f.URL {
							t.Fatalf("%s\n%s", outcome(o), steps(p))
						}
						break
					}
				}
				if parts[len(parts)-1] != int64(len(mustRead(t, r, f))) {
					t.Errorf("progress %v", parts)
				}
				if got := r.w.Requests(); !slices.Equal(got, []string{routeOf(f.URL)}) {
					t.Errorf("requests %q", got)
				}
			})
			t.Run("a web page refused", func(t *testing.T) {
				w := fakeworld.New()
				r := rt.rig(t, w, false)
				f := file(t, r, "Heltec v4", catalog.SourceZephCore, "repeater", catalog.Update) // no GitHub fallback
				w.FailPath("/zc/releases/download/", 200, "text/html")
				p := wait(t, r.b.Download(context.Background(), []catalog.File{f}))
				o := p.Outcome
				if o.OK || o.Title != "Download failed" || !strings.Contains(o.Summary[0], "web page") ||
					p.Steps[0].State != flash.StepFailed || len(o.Entries) != 0 {
					t.Fatalf("%s\n%s", outcome(o), steps(p))
				}
				if _, ok := r.d.Catalog.Saved(f); ok {
					t.Error("saved a web page")
				}
			})
			t.Run("the GitHub fallback", func(t *testing.T) {
				w := fakeworld.New()
				r := rt.rig(t, w, false)
				f := file(t, r, "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
				w.FailPath("/mc/releases/download/", 503, "")
				p := wait(t, r.b.Download(context.Background(), []catalog.File{f}))
				if o := p.Outcome; !o.OK || o.Entries[0].URL != f.Fallback {
					t.Fatalf("%s\n%s", outcome(o), steps(p))
				}
				if got := r.w.Requests(); len(got) != 2 || !strings.HasPrefix(got[1], "/gh/meshcore-dev/MeshCore/") {
					t.Errorf("requests %q", got)
				}
			})
			t.Run("ZephCore and both ESP32 files", func(t *testing.T) {
				r := rt.rig(t, fakeworld.New(), false)
				files := []catalog.File{
					file(t, r, "Heltec v4", catalog.SourceZephCore, "repeater", catalog.Update),
					file(t, r, "Heltec v4", catalog.SourceZephCore, "repeater", catalog.Wipe),
					file(t, r, rakName, catalog.SourceZephCore, "repeater", catalog.Update),
				}
				p := wait(t, r.b.Download(context.Background(), files))
				if o := p.Outcome; !o.OK || len(o.Entries) != 3 || len(p.Steps) != 3 {
					t.Fatalf("%s\n%s", outcome(o), steps(p))
				}
				saved, err := r.b.SavedFiles()
				if err != nil || len(saved) != 3 {
					t.Fatalf("saved %d, %v", len(saved), err)
				}
				if err := r.b.DeleteFile(saved[0].Name); err != nil {
					t.Fatal(err)
				}
				if saved, _ := r.b.SavedFiles(); len(saved) != 2 {
					t.Errorf("%d saved after a delete", len(saved))
				}
			})
			t.Run("offline", func(t *testing.T) {
				w := fakeworld.New()
				r := rt.rig(t, w, false)
				f := file(t, r, "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
				w.SetOffline(true)
				p := wait(t, r.b.Download(context.Background(), []catalog.File{f}))
				if o := p.Outcome; o.OK || !strings.HasPrefix(o.Summary[0], "No network") || o.Log == "" {
					t.Fatalf("%s", outcome(o))
				}
				if !strings.Contains(r.log(), "download of") {
					t.Errorf("screen.log %q", r.log())
				}
			})
		})
	}
}

func mustRead(t *testing.T, r *rig, f catalog.File) []byte {
	t.Helper()
	b, _, err := r.d.Catalog.ReadVerified(f.Base())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func routeOf(u string) string {
	i := strings.Index(u, "://")
	j := strings.Index(u[i+3:], "/")
	return u[i+3+j:]
}

// A Download stopped mid-way.
func TestDownloadStopped(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := request(t, flash.Port{}, "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
	fl, _ := catalog.Choose(req.Device, req.Version, req.Kind)
	p := wait(t, r.b.Download(ctx, []catalog.File{fl.File}))
	if o := p.Outcome; o.OK || !o.Stopped || o.Title != "Download stopped" {
		t.Fatalf("%s", outcome(o))
	}
}

func TestUpdateCatalog(t *testing.T) {
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			w := fakeworld.New()
			r := rt.rig(t, w, false)
			if _, err := r.b.LoadCatalog(); !errors.Is(err, catalog.ErrNoCatalog) {
				t.Fatalf("no catalog yet: %v", err)
			}
			cat, err := r.b.UpdateCatalog(context.Background())
			if err != nil || len(cat.Devices) != 72 {
				t.Fatalf("%v, %d boards", err, len(cat.Devices))
			}
			if got := w.Requests(); len(got) != 5 {
				t.Errorf("requests %q", got)
			}
			w.SetOffline(true)
			if _, err := r.b.UpdateCatalog(context.Background()); !errors.Is(err, catalog.ErrUnreachable) {
				t.Errorf("offline: %v", err)
			}
			w.SetOffline(false)
			w.FailPath("/mc/config.json", 503, "")
			_, err = r.b.UpdateCatalog(context.Background())
			var fe *catalog.FetchError
			if !errors.As(err, &fe) || fe.Status != 503 {
				t.Errorf("503: %v", err)
			}
			if cat, err := r.b.LoadCatalog(); err != nil || len(cat.Devices) != 72 {
				t.Errorf("the saved catalog didn't stay: %v", err)
			}
			if !strings.Contains(r.log(), "catalog update failed") {
				t.Errorf("screen.log %q", r.log())
			}
		})
	}
}

func TestPrepare(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, v4, rak)
	esp := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)

	t.Run("not saved", func(t *testing.T) {
		p := r.b.Prepare(esp)
		if p.Saved || p.Problem != "" || p.Flash.File.Type != "flash-update" || p.Flash.Address != 0x10000 {
			t.Errorf("%+v", p)
		}
	})
	t.Run("no kind", func(t *testing.T) {
		req := esp
		req.Kind = 0
		if p := r.b.Prepare(req); p.Problem != "Choose update or full wipe first." || p.Flash != (catalog.Flash{}) {
			t.Errorf("%+v", p)
		}
	})
	t.Run("saved", func(t *testing.T) {
		r.save(esp)
		p := r.b.Prepare(esp)
		if !p.Saved || p.Problem != "" || p.Entry.Name != p.Flash.File.Base() || p.Ask != "" {
			t.Errorf("%+v", p)
		}
	})
	t.Run("changed since", func(t *testing.T) {
		wipe := esp
		wipe.Kind = catalog.Wipe
		fl := r.save(wipe)
		path := filepath.Join(r.cacheDir(), "files", fl.File.Base())
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		b[100] ^= 1
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		p := r.b.Prepare(wipe)
		if p.Saved || !strings.Contains(p.Problem, "has changed since it was downloaded") {
			t.Errorf("%+v", p)
		}
		// And a flash refuses it the same way, before touching the board.
		fp := r.flash(wipe)
		if fp.Outcome.Title != "Not flashed" || !strings.Contains(fp.Outcome.Recovery, "has changed") {
			t.Errorf("%s", outcome(fp.Outcome))
		}
	})
	t.Run("ESP32-C6", func(t *testing.T) {
		req := request(t, r.port(v4), "Seeed Studio Xiao C6", catalog.SourceZephCore, "repeater", catalog.Update)
		if p := r.b.Prepare(req); !strings.Contains(p.Problem, "ESP32-C6") {
			t.Errorf("%+v", p)
		}
	})
	t.Run("unknown SoftDevice asks", func(t *testing.T) {
		req := request(t, r.port(rak), "Seeed Studio Wio Tracker L1 EINK", catalog.SourceMeshCore, "companionBle",
			catalog.Update)
		p := r.b.Prepare(req)
		if p.Problem != "" || !strings.HasSuffix(p.Ask, "Flash anyway?") || !strings.Contains(p.Ask, "SoftDevice") {
			t.Errorf("%+v", p)
		}
		r.save(req)
		if p := r.b.Prepare(req); !p.Saved || len(p.Notes) != 1 || p.Ask == "" {
			t.Errorf("saved: %+v", p)
		}
	})
	t.Run("another SoftDevice", func(t *testing.T) {
		req := request(t, r.port(rak), rakName, catalog.SourceMeshCore, "roomServer", catalog.Update)
		fl, _ := catalog.Choose(req.Device, req.Version, req.Kind)
		r.w.ServeSoftDevice(fl.File.Base(), nrfdfu.SoftDeviceS140v7)
		r.save(req)
		if p := r.b.Prepare(req); p.Saved || !strings.Contains(p.Problem, "SoftDevice") {
			t.Errorf("%+v", p)
		}
	})
	if o := r.w.Opens(); len(o) != 0 {
		t.Errorf("Prepare opened %q", o)
	}
}

func (r *rig) cacheDir() string { return r.clientDir }

// TestPrepareRefusesOtherFamily: an nRF52 board on an ESP32's port, and the other way round, are refused before
// anything opens either port.
func TestPrepareRefusesOtherFamily(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	rak := fakeworld.RAK4631(repeater("v1.17.1", ""))
	r := newRig(t, v4, rak)
	for _, req := range []flash.Request{
		request(t, r.port(v4), rakName, catalog.SourceMeshCore, "repeater", catalog.Update),
		request(t, r.port(rak), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update),
	} {
		_, why, _ := flash.Fits(req.Device, req.Port)
		if p := r.b.Prepare(req); p.Problem != why || why == "" {
			t.Errorf("%s on %s: %+v", req.Device.Name, req.Port.Name, p)
		}
		r.save(req)
		p := r.flash(req)
		if o := p.Outcome; o.Title != "Not flashed" || o.Recovery != why ||
			o.State != "Nothing was sent to the board: it still has its old firmware." {
			t.Errorf("%s", outcome(o))
		}
	}
	if o := r.w.Opens(); len(o) != 0 {
		t.Errorf("opened %q", o)
	}
}

// TestFlashNeverFetches: a file not saved is "Not flashed", and nothing is fetched or opened.
func TestFlashNeverFetches(t *testing.T) {
	v4 := fakeworld.HeltecV4(repeater("v1.17.1", ""))
	r := newRig(t, v4)
	req := request(t, r.port(v4), "Heltec v4", catalog.SourceMeshCore, "repeater", catalog.Update)
	before := r.w.Requests()
	p := r.flash(req)
	o := p.Outcome
	if o.Title != "Not flashed" || o.Recovery != "The file isn't saved; press y on Confirm to download it." || o.Retry {
		t.Fatalf("%s", outcome(o))
	}
	if after := r.w.Requests(); !slices.Equal(after, before) {
		t.Errorf("fetched %q", after[len(before):])
	}
	if o := r.w.Opens(); len(o) != 0 {
		t.Errorf("opened %q", o)
	}
	// And with the file saved, a flash still fetches nothing.
	r.save(req)
	before = r.w.Requests()
	if p := r.flash(req); !p.Outcome.OK {
		t.Fatalf("%s", outcome(p.Outcome))
	}
	if after := r.w.Requests(); !slices.Equal(after, before) {
		t.Errorf("fetched %q", after[len(before):])
	}
}
