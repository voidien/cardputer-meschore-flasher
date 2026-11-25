package ui

// The Version screen's goldens and keys (docs/phase2-design.md 9.4).

import (
	"strings"
	"testing"
	"time"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
)

// savedFile is the record of a version's file of type t, saved at testNow less ago.
func savedFile(t testing.TB, f flow, ver, typ string, size int64, ago time.Duration) catalog.Entry {
	t.Helper()
	for _, v := range f.fw.Versions {
		if v.Name != ver {
			continue
		}
		if file, ok := fileOfType(v, typ); ok {
			return catalog.Entry{Name: file.Base(), URL: file.URL, Size: size, SHA256: testSum,
				Saved: testNow.Add(-ago)}
		}
	}
	t.Fatalf("%s has no %s file", ver, typ)
	return catalog.Entry{}
}

// versionAt is the Version screen of the flow's role, the saved files as given.
func versionAt(t *testing.T, w, h int, f flow, saved []catalog.Entry) (*driver, *fakeBackend) {
	t.Helper()
	fb := screensBackend(t, f.port)
	fb.saved = saved
	d := newDriver(t, w, h, fb)
	d.m.flow = flow{port: f.port, dev: f.dev, fw: f.fw}
	d.run(d.m.openVersion())
	return d, fb
}

// The mock: a Xiao nRF52 WIO's repeater, v1.18.0 saved, with its size.
func TestVersionSaved(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		f := flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "repeater", "v1.18.0")
		d, _ := versionAt(t, w, h, f, []catalog.Entry{savedFile(t, f, "v1.18.0", "flash", 361_000, time.Hour)})
		d.check(w, h)
	})
}

// An ESP32 version says which of its two files is saved (owner question 2).
func TestVersionESP32Saved(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		f := flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4", "repeater", "v1.18.0")
		d, _ := versionAt(t, w, h, f, []catalog.Entry{
			savedFile(t, f, "v1.18.0", "flash-update", 361_000, time.Hour),
			savedFile(t, f, "v1.17.1", "flash-wipe", 1_300_000, 2*time.Hour),
			savedFile(t, f, "v1.17.0", "flash-update", 358_000, 3*time.Hour),
			savedFile(t, f, "v1.17.0", "flash-wipe", 1_290_000, 3*time.Hour),
		})
		d.check(w, h)
	})
}

// d on an ESP32 version downloads both its files, so update and full wipe both work offline later (owner question
// 3); the ones saved are skipped; when all are, it says so.
func TestVersionDownloadsBothESP32Files(t *testing.T) {
	f := flowFor(t, heltecV4Port("ttyACM0"), "Heltec v4", "repeater", "v1.18.0")
	d, fb := versionAt(t, 45, 10, f, nil)
	var asked [][]catalog.File
	fb.download = func(files []catalog.File) *flash.Job {
		asked = append(asked, files)
		return finishedJob(flash.JobDownload, flash.Outcome{OK: true, Title: "Downloaded"})
	}
	d.keys("d")
	d.settle()
	if callCount(fb, "Download") != 1 || len(asked) != 1 || len(asked[0]) != 2 ||
		asked[0][0].Type != "flash-update" || asked[0][1].Type != "flash-wipe" {
		t.Fatalf("d asked %v", asked)
	}
	if d.m.screen != scrVersion {
		t.Errorf("after the download: screen %d", d.m.screen)
	}
	fb.set(func(fb *fakeBackend) {
		fb.saved = []catalog.Entry{savedFile(t, f, "v1.18.0", "flash-update", 361_000, time.Hour)}
	})
	d.run(d.m.savedFiles(scrVersion))
	d.keys("d")
	d.settle()
	if len(asked) != 2 || len(asked[1]) != 1 || asked[1][0].Type != "flash-wipe" {
		t.Fatalf("with the update saved, d asked %v", asked[1:])
	}
	fb.set(func(fb *fakeBackend) {
		fb.saved = append(fb.saved, savedFile(t, f, "v1.18.0", "flash-wipe", 1_300_000, time.Hour))
	})
	d.run(d.m.savedFiles(scrVersion))
	d.keys("d")
	if len(asked) != 2 || d.m.note != "Saved already, 24 Nov." {
		t.Errorf("with both saved: %d downloads, note %q", len(asked), d.m.note)
	}
}

// After a download from Version, "saved" shows with no key (S16); a failure shows its Summary until the next key.
func TestVersionAfterDownload(t *testing.T) {
	f := flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "repeater", "v1.18.0")
	d, fb := versionAt(t, 45, 10, f, nil)
	j := newJob(flash.JobDownload)
	fb.download = func([]catalog.File) *flash.Job { return j }
	d.keys("d")
	if d.m.screen != scrDownload {
		t.Fatalf("d: screen %d", d.m.screen)
	}
	fb.set(func(fb *fakeBackend) {
		fb.saved = []catalog.Entry{savedFile(t, f, "v1.18.0", "flash", 361_000, 0)}
	})
	j.Finish(flash.Outcome{OK: true, Title: "Downloaded"})
	d.settle()
	if d.m.screen != scrVersion {
		t.Fatalf("at the download's end: screen %d", d.m.screen)
	}
	bodyHas(d, "saved · 361 KB")
	j2 := newJob(flash.JobDownload)
	fb.download = func([]catalog.File) *flash.Job { return j2 }
	d.keys("j", "d")
	j2.Finish(flash.Outcome{Title: "Download failed", Summary: []string{"Couldn't download: no network, or the server didn't answer."}})
	d.settle()
	bodyHas(d, "Couldn't download: no network")
	d.keys("k")
	bodyLacks(d, "Couldn't download")
}

// Enter goes to Confirm, n to the notes, Esc back to the roles.
func TestVersionKeys(t *testing.T) {
	f := flowFor(t, xiaoPort("ttyACM0"), "Seeed Studio Xiao nRF52 WIO", "repeater", "v1.18.0")
	d, fb := versionAt(t, 45, 10, f, nil)
	fb.prepare = prepareAs(true, nil)
	versionTo(d, "v1.17.1")
	d.keys("n")
	if d.m.screen != scrNotes || !strings.Contains(titleOf(d), "Notes · v1.17.1") {
		t.Fatalf("n: screen %d, %q", d.m.screen, titleOf(d))
	}
	d.keys("esc", "enter")
	if d.m.screen != scrConfirm || d.m.flow.ver.Name != "v1.17.1" {
		t.Fatalf("enter: screen %d", d.m.screen)
	}
	d.keys("esc", "esc")
	if d.m.screen != scrRole {
		t.Errorf("esc, esc: screen %d", d.m.screen)
	}
}
