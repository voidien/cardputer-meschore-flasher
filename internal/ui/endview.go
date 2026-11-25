package ui

// How Result and Setup's end lay out a Job's Outcome when it doesn't fit (SPEC.md 4, design 8.9): the Summary
// scrolls a whole paragraph at a time (each is a sentence or two: "The board went away while writing (212 of 361
// KB)."), so no sentence shows only its first or last lines, and what the board is left with and what to do now stay
// pinned at the bottom.

// endPara is one paragraph of an end screen's pinned part. movable says it may scroll with the Summary when the
// pinned part leaves no room for the Summary's first paragraph: the generic retry line ("Press r to try again: …"),
// which the hint's "r retry" says too. State and Recovery never move.
type endPara struct {
	text    string
	movable bool
}

// endScroll is an end screen's scroll: top is the first paragraph shown of the part that scrolls, page how many were
// shown last (Space and b move by it).
type endScroll struct {
	top, page int
}

// endKey moves the scroll for j/k (a paragraph) and Space/b (a page), and says whether the key was one of them.
func (e *endScroll) key(k string) bool {
	switch k {
	case "j", "down":
		e.top++
	case "k", "up":
		e.top--
	case " ":
		e.top += max(1, e.page)
	case "b":
		e.top -= max(1, e.page)
	default:
		return false
	}
	return true
}

// endLayout lays out summary (its paragraphs) and pinned in h lines of w cells, and says whether it scrolls:
//
//   - all of it, when it fits;
//   - else the Summary scrolling above the pinned part, with blank lines between when the last page is short, so the
//     pinned part stays at the bottom;
//   - when the pinned part leaves less room than the Summary's first paragraph needs, its movable paragraphs scroll
//     after the Summary instead; and when even that leaves too little, everything scrolls together.
func endLayout(summary []string, pinned []endPara, w, h int, e *endScroll) ([]string, bool) {
	var paras [][]string
	for _, s := range summary {
		paras = append(paras, wrap(s, w))
	}
	var pins [][]string
	var movable []bool
	total := 0
	for _, p := range paras {
		total += len(p)
	}
	for _, p := range pinned {
		if p.text == "" {
			continue
		}
		pins = append(pins, wrap(p.text, w))
		movable = append(movable, p.movable)
		total += len(pins[len(pins)-1])
	}
	if total <= h {
		e.top, e.page = 0, len(paras)
		var out []string
		for _, p := range append(paras, pins...) {
			out = append(out, p...)
		}
		return out, false
	}
	first := 1
	if len(paras) > 0 {
		first = len(paras[0])
	}
	room := func(ps [][]string) int {
		n := h
		for _, p := range ps {
			n -= len(p)
		}
		return n
	}
	if room(pins) < first {
		var kept [][]string
		var moved [][]string
		for i, p := range pins {
			if movable[i] {
				moved = append(moved, p)
			} else {
				kept = append(kept, p)
			}
		}
		paras, pins = append(paras, moved...), kept
	}
	if room(pins) < first {
		paras, pins = append(paras, pins...), nil
	}
	r := room(pins)
	// The furthest the scroll goes: the first paragraph from which the rest fits.
	last := len(paras) - 1
	for used := 0; last >= 0 && used+len(paras[last]) <= r; last-- {
		used += len(paras[last])
	}
	e.top = max(0, min(e.top, last+1, len(paras)-1))
	var out []string
	shown := 0
	for _, p := range paras[min(e.top, len(paras)):] {
		if len(out)+len(p) > r {
			if len(out) == 0 {
				out = append(out, p[:r]...) // a paragraph taller than the room (a tiny screen): its first lines
				shown++
			}
			break
		}
		out = append(out, p...)
		shown++
	}
	e.page = shown
	for len(out) < r {
		out = append(out, "")
	}
	for _, p := range pins {
		out = append(out, p...)
	}
	return out, true
}
