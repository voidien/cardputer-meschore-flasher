package ui

// Phase 2 contract (docs/phase2-design.md): this file is fixed and complete (the integrator's). Flashing, Download
// (builder C) and Setup's sending and end (builder D) list a Job's steps with these, so the marks and words are the
// same everywhere and use only S15's glyphs. Download lists its file names as labels (fitLabelSteps).

import (
	"fmt"
	"strings"
	"time"

	"meshflash/internal/flash"
)

// showTook is how long a finished step must have taken for its time to show ("✓ Erased (10 s)"): the quick ones
// (the touch, a setup command) would only add noise.
const showTook = 5 * time.Second

// stepLines lists a Job's steps in w cells, each wrapped with a two-cell hanging indent (S11: wrapped, never cut).
// extra is how long ago, on the screens' clock, the picture arrived (m.now() − m.jobSeenAt), added to the running
// step's time so it moves between pictures. The marks, all from S15's checked glyphs:
//
//	✓ Erased (10 s)                                done: its bytes when it moved some, its time when ≥ 5 s
//	✓ Writing 361 KB (42 s)
//	▸ Writing 212 of 361 KB · 59% (0:38)           under way: bytes, percentage, running time
//	▸ Erasing (0:04, about 10 s)                   under way with no progress to show: Step.Expect
//	▸ Into the bootloader · attempt 2 of 4 (0:03)  under way with a Note
//	▸ Writing 212 of 361 KB: stopped, no answer    where it stopped, and why (Note)
//	· Clock (skipped: the Zero's clock isn't synchronised)
func stepLines(p flash.Progress, w int, extra time.Duration) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, stepGroup(p, s, w, extra)...)
	}
	return out
}

// fitSteps is stepLines in at most h lines, for a step list that would run off the screen (an nRF52 retry, a setup's
// twenty commands): the last steps are kept, so the running or failed one is always in view, and the oldest are
// folded into one line, "✓ 3 earlier steps". When even the last step alone takes more than h lines, its first h.
func fitSteps(p flash.Progress, w, h int, extra time.Duration) []string {
	return fitGroups(p, h, func(s flash.Step) []string { return stepGroup(p, s, w, extra) })
}

// fitLabelSteps is fitSteps for steps whose Text is a label, not words (a download's file name, often wider than
// the screen: 40 to 80 characters): each step is its mark and the label on one line, cut with "…" (S11: only a
// label is cut), and its bytes and time on the next (labelGroup).
func fitLabelSteps(p flash.Progress, w, h int, extra time.Duration) []string {
	return fitGroups(p, h, func(s flash.Step) []string { return labelGroup(p, s, w, extra) })
}

// fitGroups is fitSteps' folding, over each step's lines as group gives them.
func fitGroups(p flash.Progress, h int, group func(flash.Step) []string) []string {
	if h < 1 || len(p.Steps) == 0 {
		return nil
	}
	groups := make([][]string, len(p.Steps))
	total := 0
	for i, s := range p.Steps {
		groups[i] = group(s)
		total += len(groups[i])
	}
	if total <= h {
		var out []string
		for _, g := range groups {
			out = append(out, g...)
		}
		return out
	}
	kept, used := len(groups), 0
	for kept > 0 && used+len(groups[kept-1]) <= h-1 {
		kept--
		used += len(groups[kept])
	}
	if kept == len(groups) {
		last := groups[len(groups)-1]
		return last[:min(h, len(last))]
	}
	out := []string{"✓ " + plural(kept, "earlier step", "earlier steps")}
	for _, g := range groups[kept:] {
		out = append(out, g...)
	}
	return out
}

// stepGroup is one step's lines: its mark, then its words wrapped to w−2 cells, later lines indented under them.
func stepGroup(p flash.Progress, s flash.Step, w int, extra time.Duration) []string {
	mark, text := stepText(p, s, extra)
	lines := wrap(text, max(1, w-2))
	for i, l := range lines {
		if i == 0 {
			lines[i] = mark + " " + l
		} else {
			lines[i] = "  " + l
		}
	}
	return lines
}

// labelGroup is one label step's lines: its mark and its Text cut to one line, then what stepText adds after the
// Text (the bytes, a note, the time) wrapped under it with the same indent, "" when there is nothing.
func labelGroup(p flash.Progress, s flash.Step, w int, extra time.Duration) []string {
	mark, text := stepText(p, s, extra)
	lines := []string{mark + " " + cut(s.Text, max(1, w-2))}
	tail := strings.TrimSpace(strings.TrimPrefix(text, s.Text))
	tail = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(tail, ":"), "·"))
	if tail != "" {
		lines = append(lines, indented(tail, w)...)
	}
	return lines
}

// stepText is a step's mark and words.
func stepText(p flash.Progress, s flash.Step, extra time.Duration) (string, string) {
	var b strings.Builder
	b.WriteString(s.Text)
	switch s.State {
	case flash.StepDone:
		if s.Total > 0 {
			b.WriteString(" " + byteSize(s.Total))
		}
		if s.Note != "" {
			b.WriteString(" · " + s.Note)
		}
		if s.Took >= showTook {
			b.WriteString(" (" + took(s.Took) + ")")
		}
		return "✓", b.String()
	case flash.StepSkipped:
		if s.Note != "" {
			b.WriteString(" (skipped: " + s.Note + ")")
		} else {
			b.WriteString(" (skipped)")
		}
		return "·", b.String()
	case flash.StepFailed:
		if moved := bytesMoved(s); moved != "" {
			b.WriteString(" " + moved)
		}
		b.WriteString(": stopped")
		if s.Note != "" {
			b.WriteString(", " + s.Note)
		}
		return "▸", b.String()
	}
	if moved := bytesMoved(s); moved != "" {
		b.WriteString(" " + moved)
		if s.Total > 0 {
			fmt.Fprintf(&b, " · %d%%", percent(s.Done, s.Total))
		}
	}
	if s.Note != "" {
		b.WriteString(" · " + s.Note)
	}
	running := extra
	if !s.Started.IsZero() && !p.Now.IsZero() {
		running += p.Now.Sub(s.Started)
	}
	if s.Expect > 0 && s.Total == 0 {
		fmt.Fprintf(&b, " (%s, about %s)", elapsed(running), took(s.Expect))
	} else {
		fmt.Fprintf(&b, " (%s)", elapsed(running))
	}
	return "▸", b.String()
}

// bytesMoved words a step's bytes so far: "212 of 361 KB" (the unit once, the total's), "212 KB so far" when the
// total isn't known, "" when it moves none.
func bytesMoved(s flash.Step) string {
	switch {
	case s.Total > 0:
		return bytesOf(s.Done, s.Total)
	case s.Total < 0 && s.Done > 0:
		return byteSize(s.Done) + " so far"
	}
	return ""
}

// bytesOf words done of total in total's unit: "212 of 361 KB", "0.4 of 1.3 MB", "980 of 999 B".
func bytesOf(done, total int64) string {
	switch {
	case total < 1000:
		return fmt.Sprintf("%d of %d B", done, total)
	case total < 999_500:
		return fmt.Sprintf("%d of %d KB", (done+500)/1000, (total+500)/1000)
	}
	return fmt.Sprintf("%.1f of %.1f MB", float64(done)/1e6, float64(total)/1e6)
}

// percent is done of total, rounded ("59%" for 212 of 361 KB), but never 100 before the end.
func percent(done, total int64) int64 {
	p := (done*100 + total/2) / total
	if done < total {
		p = min(p, 99)
	}
	return p
}
