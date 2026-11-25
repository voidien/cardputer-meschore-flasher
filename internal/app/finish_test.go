package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/store"
	"meshflash/internal/ui"
)

var quit = ending{}

// The end of the program is bounded: a Job that never finishes stopping is waited for shutdownWait after a quit, and
// after a crash for lostWait and then shutdownWait once stopped, never more; screen.log says it was still stopping,
// and that the app is erased when the last picture said so. shutdownWait leaves the saves time before the launcher's
// SIGKILL, 3 s after its SIGTERM.
func TestShutdownIsBounded(t *testing.T) {
	if shutdownWait <= 0 || shutdownWait > 2*time.Second {
		t.Errorf("shutdownWait is %v: the launcher's SIGKILL comes 3 s after its SIGTERM", shutdownWait)
	}
	if lostWait <= 0 || lostWait > 3*time.Minute {
		t.Errorf("lostWait is %v, want at most about 3 min (owner question 19)", lostWait)
	}

	j, cancels := stuckJob(flash.JobFlash, flash.RiskErased)
	m := &fakeModel{job: j}
	asked := fakeWaits(t, nil, true)
	log, mem := newLog()
	finish(m, quit, &radioBook{}, log, "meshflash", io.Discard, nil)
	if !reflect.DeepEqual(*asked, []time.Duration{shutdownWait}) || m.shutdowns != 1 || cancels() != 1 {
		t.Errorf("after a quit: waited %v, %d shutdowns, %d cancels; want one wait of %v, once each", *asked,
			m.shutdowns, cancels(), shutdownWait)
	}
	want := "meshflash: the flash was still stopping when meshflash ended: its app is erased, so the board has no " +
		"working firmware until it is flashed again.\n"
	if mem.String() != want {
		t.Errorf("screen.log %q, want %q", mem.String(), want)
	}

	j, cancels = stuckJob(flash.JobSetup, flash.RiskSettings)
	m = &fakeModel{job: j}
	asked = fakeWaits(t, nil, true, true)
	log, mem = newLog()
	var stderr bytes.Buffer
	finish(m, ending{lost: true, why: "it crashed"}, &radioBook{}, log, "meshflash", &stderr, nil)
	if !reflect.DeepEqual(*asked, []time.Duration{lostWait, shutdownWait}) || m.shutdowns != 0 || cancels() != 1 {
		t.Errorf("after a crash: waited %v, %d shutdowns, %d cancels; want %v and %v, no shutdown, one cancel",
			*asked, m.shutdowns, cancels(), lostWait, shutdownWait)
	}
	for _, w := range []string{
		"meshflash: the screens ended (it crashed) while the setup went on: it is left to finish with no screen, " +
			"for up to 3 min\n",
		"meshflash: the setup hadn't ended after 3 min: stopping it the safe way\n",
		"meshflash: the setup was still stopping when meshflash ended.\n",
	} {
		if !strings.Contains(mem.String(), w) {
			t.Errorf("screen.log %q lacks %q", mem.String(), w)
		}
	}
	if !strings.Contains(stderr.String(), "waiting up to 3 min") {
		t.Errorf("stderr %q doesn't say why meshflash hasn't ended", stderr.String())
	}
}

// A setup cut off by a quit writes to screen.log how it ended: its Title, every Summary paragraph, the State it left
// the node in and what to do (not its Log, which the backend wrote), and never the password it was sent. A download stopped by a quit is waited for but
// leaves no lines: it changed no board.
func TestShutdownLogsTheOutcome(t *testing.T) {
	const password = "hunter2-is-secret"
	req := flash.SetupRequest{Settings: nodecli.Settings{Name: "Hilltop 2", AdminPassword: password}}
	// The backend's worker words the Outcome from the request it was given, password and all: the password prints
	// as •••• (nodecli.Secret), and finish writes only what the Outcome says.
	stopped := flash.Outcome{Stopped: true, Title: "Setup stopped",
		Summary: []string{fmt.Sprintf("Sent: the name “%s”, the admin password %v, the radio.", req.Settings.Name,
			req.Settings.AdminPassword), "Stopped before the reboot."},
		State:    "Its new radio is saved but it hasn't restarted: still on its old radio until it does.",
		Recovery: "Set it up again to finish, or restart it to switch.",
		Log:      "setup stopped after set radio (step 4 of 20)"}
	j, _ := backendJob(flash.JobSetup, stopped)
	asked := fakeWaits(t, nil)
	log, mem := newLog()
	finish(&fakeModel{job: j}, quit, &radioBook{}, log, "meshflash", io.Discard, nil)
	want := "meshflash: how the setup ended: Setup stopped\n" +
		"meshflash:   Sent: the name “Hilltop 2”, the admin password ••••, the radio.\n" +
		"meshflash:   Stopped before the reboot.\n" +
		"meshflash:   Left with: Its new radio is saved but it hasn't restarted: still on its old radio until it does.\n" +
		"meshflash:   What to do: Set it up again to finish, or restart it to switch.\n"
	if mem.String() != want {
		t.Errorf("screen.log:\n%s\nwant:\n%s", mem.String(), want)
	}
	if strings.Contains(mem.String(), password) {
		t.Error("screen.log holds the admin password")
	}
	// Outcome.Log has one writer, the backend, which wrote it as the Job finished: finish never repeats it.
	if strings.Contains(mem.String(), stopped.Log) {
		t.Errorf("finish wrote Outcome.Log, which the backend already wrote:\n%s", mem.String())
	}
	if len(*asked) > 1 {
		t.Errorf("waited %v for a Job that stopped", *asked)
	}

	j, cancels := backendJob(flash.JobDownload, flash.Outcome{Stopped: true, Title: "Download stopped"})
	fakeWaits(t, nil)
	log, mem = newLog()
	finish(&fakeModel{job: j}, quit, &radioBook{}, log, "meshflash", io.Discard, nil)
	if mem.String() != "" || cancels() == 0 {
		t.Errorf("a download stopped by a quit: %d cancels, screen.log %q", cancels(), mem.String())
	}

	log, mem = newLog()
	m := &fakeModel{}
	finish(m, quit, &radioBook{}, log, "meshflash", io.Discard, nil)
	if m.shutdowns != 1 || mem.String() != "" {
		t.Errorf("nothing running: %d shutdowns, screen.log %q", m.shutdowns, mem.String())
	}
}

// A setup the screens never saw end has the radio its node took saved all the same, after a quit or a crash; one that
// didn't get that far, a flash and a setup still stopping save nothing.
func TestShutdownSavesSentRadio(t *testing.T) {
	sent := &flash.Sent{Radio: nodecli.Radio{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8}, TxDBm: 22, PathHashBytes: 1}
	want := []store.Radio{{FreqMHz: 869.618, BWkHz: 62.5, SF: 8, CR: 8, TxDBm: 22, PathHashBytes: 1}}
	if !reflect.DeepEqual([]store.Radio{ui.SavedRadio(*sent)}, want) {
		t.Fatalf("ui.SavedRadio(%+v) = %+v", *sent, ui.SavedRadio(*sent))
	}
	for _, end := range []ending{quit, {lost: true, why: "its terminal closed"}} {
		j, _ := backendJob(flash.JobSetup, flash.Outcome{Stopped: true, Title: "Setup stopped", Sent: sent})
		book := &radioBook{}
		// After a crash lostWait is over at once, and the cancel then ends the setup; after a quit Shutdown's cancel
		// does. The wait after a cancel never runs out: the Job ends first.
		fakeWaits(t, nil, end.lost)
		log, _ := newLog()
		finish(&fakeModel{job: j}, end, book, log, "meshflash", io.Discard, nil)
		if !reflect.DeepEqual(book.saved, want) {
			t.Errorf("lost %v: saved %+v, want %+v", end.lost, book.saved, want)
		}
	}

	for _, c := range []struct {
		what string
		job  func() *flash.Job
	}{
		{"a setup stopped before set radio", func() *flash.Job {
			j, _ := backendJob(flash.JobSetup, flash.Outcome{Stopped: true, Title: "Setup stopped"})
			return j
		}},
		{"a flash", func() *flash.Job {
			j, _ := backendJob(flash.JobFlash, flash.Outcome{Stopped: true, Title: "Flashing stopped", Sent: sent})
			return j
		}},
		{"a setup still stopping", func() *flash.Job {
			j, _ := stuckJob(flash.JobSetup, flash.RiskRadioSent)
			return j
		}},
	} {
		book := &radioBook{}
		fakeWaits(t, nil, c.what == "a setup still stopping")
		log, _ := newLog()
		finish(&fakeModel{job: c.job()}, quit, book, log, "meshflash", io.Discard, nil)
		if len(book.saved) != 0 {
			t.Errorf("%s: saved %+v", c.what, book.saved)
		}
	}

	j, _ := backendJob(flash.JobSetup, flash.Outcome{Stopped: true, Sent: sent})
	fakeWaits(t, nil)
	log, mem := newLog()
	finish(&fakeModel{job: j}, quit, &radioBook{err: errors.New("disk full")}, log, "meshflash", io.Discard, nil)
	if !strings.Contains(mem.String(), "the node took its new radio, but it can't be remembered in config.json: "+
		"disk full") {
		t.Errorf("a radio that can't be saved isn't in screen.log: %q", mem.String())
	}
}

// After a crash or a closed terminal (owner question 19) the flash under way is left to finish with no screen: what
// was typed is saved, the flash isn't stopped, and screen.log says how it ended. A model the crash broke doesn't stop
// that; a download is stopped at once.
func TestCrashLetsTheFlashFinish(t *testing.T) {
	flashed := flash.Outcome{OK: true, Title: "Flashed",
		Summary: []string{"Checked: the MD5 of the flash matches the file."}}
	j, cancels := backendJob(flash.JobFlash, flash.Outcome{Stopped: true, Title: "Flashing stopped"})
	m := &fakeModel{job: j, brokenSaves: true}
	asked := fakeWaits(t, func(i int) {
		if i == 0 {
			go j.Finish(flashed) // the flash ends by itself, while finish waits
		}
	})
	log, mem := newLog()
	finish(m, ending{lost: true, why: "it crashed"}, &radioBook{}, log, "meshflash", io.Discard, nil)
	if cancels() != 0 || m.shutdowns != 0 || m.unsents != 1 {
		t.Errorf("%d cancels, %d shutdowns, %d saves of what was typed; want the flash left alone and one save",
			cancels(), m.shutdowns, m.unsents)
	}
	if !reflect.DeepEqual(*asked, []time.Duration{lostWait}) {
		t.Errorf("waited %v, want %v", *asked, lostWait)
	}
	for _, w := range []string{
		"meshflash: saving what was typed failed: the model was left half-changed\n",
		"meshflash: the screens ended (it crashed) while the flash went on",
		"meshflash: how the flash ended: Flashed\nmeshflash:   Checked: the MD5 of the flash matches the file.\n",
	} {
		if !strings.Contains(mem.String(), w) {
			t.Errorf("screen.log %q lacks %q", mem.String(), w)
		}
	}

	j, cancels = backendJob(flash.JobDownload, flash.Outcome{Stopped: true, Title: "Download stopped"})
	asked = fakeWaits(t, nil)
	log, mem = newLog()
	finish(&fakeModel{job: j}, ending{lost: true, why: "its terminal closed"}, &radioBook{}, log, "meshflash",
		io.Discard, nil)
	if cancels() == 0 || len(*asked) > 1 || (len(*asked) == 1 && (*asked)[0] != shutdownWait) || mem.String() != "" {
		t.Errorf("a download after a lost terminal: %d cancels, waited %v, screen.log %q; want it stopped at once",
			cancels(), *asked, mem.String())
	}
}

// A flash left to finish with no screen after a crash is stopped the safe way when a SIGTERM (the launcher's, after
// an Esc hold) or a SIGINT (Ctrl+C in the CLI app's terminal, cooked again after the crash) arrives: within the
// shutdown's wait, so screen.log gets how it ended before the launcher's SIGKILL.
func TestCrashThenSignalStopsTheFlash(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(signalName(sig), func(t *testing.T) {
			stopped := flash.Outcome{Stopped: true, Title: "Flashing stopped", State: "Its app is erased.",
				Recovery: "Press r; meshflash then asks you to press reset twice quickly."}
			j, cancels := backendJob(flash.JobFlash, stopped)
			asked := fakeWaits(t, nil)
			stops := make(chan os.Signal, 1)
			stops <- sig
			log, mem := newLog()
			finish(&fakeModel{job: j}, ending{lost: true, why: "it crashed"}, &radioBook{}, log, "meshflash",
				io.Discard, stops)
			if cancels() != 1 {
				t.Errorf("%d cancels, want the flash stopped once", cancels())
			}
			// lostWait's timer, never fired; then the stop's wait, unless the Job had already ended by then.
			if a := *asked; len(a) == 0 || a[0] != lostWait || len(a) > 2 || len(a) == 2 && a[1] != shutdownWait {
				t.Errorf("waited %v, want %v then at most %v", a, lostWait, shutdownWait)
			}
			for _, w := range []string{
				"meshflash: told to end (" + signalName(sig) + ") while the flash went on with no screen: stopping it " +
					"the safe way\n",
				"meshflash: how the flash ended: Flashing stopped\n",
				"meshflash:   Left with: Its app is erased.\n",
			} {
				if !strings.Contains(mem.String(), w) {
					t.Errorf("screen.log %q lacks %q", mem.String(), w)
				}
			}
		})
	}
}
