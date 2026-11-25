package flash

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"meshflash/internal/catalog"
	"meshflash/internal/esp"
	"meshflash/internal/nrfdfu"
	"meshflash/internal/usbid"
)

// checked is a Request matched to its file without touching the board (match before touch, 4.1): Choose's pick, the
// saved file's record and the very bytes whose SHA-256 was checked, the DFU package made from those bytes and passed
// for the board's SoftDevice, or why not.
type checked struct {
	fl      catalog.Flash
	saved   bool
	entry   catalog.Entry
	data    []byte
	pkg     *nrfdfu.Package
	notes   []string
	problem string
}

// check runs every check of Prepare: the family (Fits), catalog.Choose, the saved file (Saved, then ReadVerified, its
// URL checked again), and the image's chip or the DFU package and its SoftDevice. It reads only the SD card: a file
// not saved is said so (saved false, no problem), never downloaded.
func (b *Backend) check(req Request) checked {
	var c checked
	if ok, why, _ := Fits(req.Device, req.Port); !ok {
		c.problem = why
		return c
	}
	fl, err := catalog.Choose(req.Device, req.Version, req.Kind)
	if err != nil {
		c.problem = sentence(plain(err))
		return c
	}
	c.fl = fl
	if _, ok := b.d.Catalog.Saved(fl.File); !ok {
		return c
	}
	data, entry, err := b.d.Catalog.ReadVerified(fl.File.Base())
	switch {
	case errors.Is(err, catalog.ErrNotSaved):
		return c
	case errors.Is(err, catalog.ErrDamaged):
		c.problem = fmt.Sprintf("The saved copy of %s has changed since it was downloaded on %s: delete it in Saved "+
			"files and download it again.", fl.File.Base(), entry.Saved.Format("2 Jan"))
		return c
	case err != nil:
		c.problem = "The saved file can't be read: " + clause(plain(err)) + "."
		return c
	}
	// ReadVerified goes by name: a Download of another catalog file of the same name could have landed since Saved
	// looked (map-backend.md G5).
	if entry.URL != fl.File.URL && (fl.File.Fallback == "" || entry.URL != fl.File.Fallback) {
		c.problem = fmt.Sprintf("The saved %s came from another address than this version's: download it again.",
			fl.File.Base())
		return c
	}
	c.saved, c.entry, c.data = true, entry, data
	switch req.Device.Type {
	case catalog.ESP32:
		img, err := esp.ImageChip(data, fl.Address)
		if err != nil {
			c.problem = "The file isn't an image meshflash can write there: " + clause(plain(err)) + "."
			return c
		}
		if ch, ok := usbid.DeviceChip(req.Device.Name); ok {
			if want, ok := esp.ParseChip(string(ch)); ok && want != img {
				c.problem = fmt.Sprintf("The file is for the %s, and %s has an %s.", img, req.Device.Name, want)
				return c
			}
		}
		if !img.Supported() {
			c.problem = fmt.Sprintf("The file is for the %s, which meshflash can't flash.", img)
			return c
		}
	case catalog.NRF52:
		pkg, err := nrfdfu.OpenPackage(data)
		if err != nil {
			c.problem = "The file isn't a DFU package meshflash can write: " + clause(plain(err)) + "."
			return c
		}
		note, err := pkg.CheckSoftDevice(req.Device.SoftDevice)
		if err != nil {
			c.problem = "The file is built for another SoftDevice than this board's, so its bootloader would refuse " +
				"it after erasing its app: " + clause(strings.TrimPrefix(plain(err), "the firmware needs a different SoftDevice: ")) + "."
			return c
		}
		if note != "" {
			c.notes = append(c.notes, note)
		}
		c.pkg = pkg
	}
	return c
}

// prepare is Prepare: check, as the Confirm screen shows it.
func (b *Backend) prepare(req Request) Prepared {
	c := b.check(req)
	p := Prepared{Flash: c.fl, Saved: c.saved && c.problem == "", Entry: c.entry, Problem: c.problem,
		Notes: c.notes}
	if c.problem != "" {
		p.Saved = false
	}
	if req.Device.Type == catalog.NRF52 && req.Device.SoftDevice == 0 && c.problem == "" {
		// Owner question 20: Confirm says so and asks a second y.
		p.Ask = unknownSDAsk
	}
	return p
}

// download is Download: one file after another, a step each with its bytes, the Entries saved.
func (b *Backend) download(ctx context.Context, files []catalog.File) *Job {
	ctx, cancel := context.WithCancel(ctx)
	j := NewJob(JobDownload, b.d.Clock, cancel)
	files = slices.Clone(files)
	go func() {
		var o Outcome
		defer func() {
			if p := recover(); p != nil {
				o = Outcome{Title: titleDownFailed, Summary: []string{"meshflash went wrong while downloading."},
					Log: fmt.Sprintf("download: internal error: %v", p)}
				b.logf("%s", o.Log)
			}
			j.Finish(o)
		}()
		o = b.runDownload(ctx, j, files)
	}()
	return j
}

func (b *Backend) runDownload(ctx context.Context, j *Job, files []catalog.File) Outcome {
	var entries []catalog.Entry
	var summary []string
	for i, f := range files {
		name := f.Base()
		start := b.d.Clock.Now()
		j.Set(func(p *Progress) {
			p.Steps = append(p.Steps, Step{Text: name, State: StepNow, Total: -1, Started: start})
		})
		e, err := b.d.Catalog.DownloadFrom(ctx, f, func(done, total int64) {
			j.Set(func(p *Progress) {
				p.Steps[i].Done, p.Steps[i].Total = done, total
			})
		}, func(url string) {
			j.Set(func(p *Progress) { p.Steps[i].From = url })
		})
		took := b.d.Clock.Now().Sub(start)
		if err != nil {
			why := downloadWhy(name, err)
			j.Set(func(p *Progress) {
				p.Steps[i].State, p.Steps[i].Took, p.Steps[i].Note = StepFailed, took, why
			})
			if ctx.Err() != nil {
				return Outcome{Stopped: true, Title: titleDownStopped, Entries: entries,
					Summary: append(summary, "Stopped before "+name+" was saved.")}
			}
			log := fmt.Sprintf("download of %s failed: %v", name, err)
			b.logf("%s", log)
			return Outcome{Title: titleDownFailed, Entries: entries, Summary: append(summary, why), Log: log}
		}
		entries = append(entries, e)
		summary = append(summary, fmt.Sprintf("Saved %s (%s), SHA-256 %.8s….", name, sizeText(int(e.Size)), e.SHA256))
		j.Set(func(p *Progress) {
			p.Steps[i].State, p.Steps[i].Took = StepDone, took
			p.Steps[i].Done, p.Steps[i].Total = e.Size, e.Size
		})
	}
	return Outcome{OK: true, Title: titleDownloaded, Summary: summary, Entries: entries}
}

// downloadWhy says why a download failed, in words: no network, the server's status, or what was wrong with what
// it sent.
func downloadWhy(name string, err error) string {
	var fe *catalog.FetchError
	switch {
	case errors.Is(err, catalog.ErrUnreachable):
		return "No network, or the server didn't answer: join Wi-Fi and try again."
	case errors.As(err, &fe) && fe.Status != 0 && fe.Status != 200:
		return fmt.Sprintf("The server answered HTTP %d for %s.", fe.Status, name)
	case errors.As(err, &fe):
		why := strings.TrimPrefix(plain(fe.Err), catalog.ErrBadAnswer.Error()+": ")
		return "Couldn't download " + name + ": " + clause(why) + "."
	}
	return "Couldn't download " + name + ": " + clause(plain(err)) + "."
}
