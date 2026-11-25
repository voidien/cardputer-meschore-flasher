package ui

// The Download screen (SPEC.md 4, docs/phase2-design.md 9.7): a file not saved yet, from Confirm's y, or a version's
// files from Version's d; each file's bytes as they come, where from, and that Wi-Fi is needed. Esc stops it with no
// question: nothing saved changes. At the end it goes back where it came from, in the same Update (S16).

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
)

// downloadScreen is the Download screen's state: where it came from, the files asked for, the latest picture, and
// whether Esc stopped it (the screen then went back at once; the Job's end only refreshes what that screen shows).
type downloadScreen struct {
	from    screen
	files   []catalog.File
	p       flash.Progress
	stopped bool
}

// startDownload starts a download in Update and watches it (a Job, which returns at once).
func (m *Model) startDownload(files []catalog.File, from screen) tea.Cmd {
	if !m.canStart() || len(files) == 0 {
		return nil
	}
	j := m.backend.Download(context.Background(), files)
	cmd := m.startedJob(j)
	if m.ownJob() {
		m.dl = downloadScreen{from: from, files: files}
		m.screen = scrDownload
	}
	return cmd
}

// downloadProgress takes the download's pictures; at its end, back where it came from with what changed: Confirm
// checks the file again (and y waits for that, and half a second: design 8.8), Version lists what is saved; a
// failure's Summary shows in that screen's body.
func (m *Model) downloadProgress(p flash.Progress) tea.Cmd {
	m.dl.p = p
	if !p.Done {
		return nil
	}
	var failed []string
	if m.screen == scrDownload {
		if o := p.Outcome; !o.OK && !o.Stopped {
			failed = o.Summary
			if len(failed) == 0 {
				failed = []string{"Couldn't download it."}
			}
		}
		m.screen = m.dl.from
	}
	switch m.screen {
	case scrConfirm:
		m.conf.failed = failed
		m.conf.readyAt = m.now().Add(heldYGap)
		return m.prepare()
	case scrVersion:
		m.ver.failed = failed
		return m.savedFiles(scrVersion)
	}
	return nil
}

func (m *Model) downloadKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() != "esc" {
		return nil
	}
	if m.ownJob() {
		m.job.Cancel()
	}
	m.dl.stopped = true
	m.screen = m.dl.from
	return nil
}

// downloadFrom says where the file being fetched comes from: its site, or GitHub when the flasher site didn't have
// it (the backend's step says so in its Note).
func (m *Model) downloadFrom() string {
	steps := m.dl.p.Steps
	if n := len(steps); n > 0 && strings.Contains(strings.ToLower(steps[n-1].Note), "github") {
		return "From GitHub (the flasher site didn't have it)"
	}
	if len(m.dl.files) == 0 {
		return ""
	}
	i := max(0, min(len(steps)-1, len(m.dl.files)-1))
	return "From " + host(m.dl.files[i].URL)
}

func (m *Model) downloadView(w, h int) ([]string, string) {
	var foot []string
	if from := m.downloadFrom(); from != "" {
		foot = append(foot, wrap(from, w)...)
	}
	foot = append(foot, wrap("Wi-Fi needed.", w)...)
	room := max(1, h-len(foot)-1)
	steps := fitSteps(m.dl.p, w, room, m.now().Sub(m.jobSeenAt))
	if len(steps) == 0 {
		steps = wrap("Starting the download…", w)
	}
	body := append(steps, "")
	if len(body)+len(foot) > h {
		body = body[:len(body)-1]
	}
	return append(body, foot...), "esc stop  ? help"
}
