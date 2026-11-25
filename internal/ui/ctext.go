package ui

// Builder C's words and small layout helpers, shared by the screens in model.go's files (docs/phase2-design.md 9).
// The frame's own helpers are in text.go and steps.go (fixed); these are only what the screens add.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
)

// keyHelp is one of Help's key lines: the key padded to 15 runes, then what it does (wrapIndent hangs the rest under
// the description).
func keyHelp(key, what string) string {
	return key + strings.Repeat(" ", max(1, 15-len([]rune(key)))) + what
}

// sentence ends s with a full stop unless it already ends a sentence, so a reason from the backend reads as one.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") ||
		strings.HasSuffix(s, "…") {
		return s
	}
	return s + "."
}

func itoa(n int) string { return strconv.Itoa(n) }

// roleWords names a node's role as it said it ("room_server" is "room server").
func roleWords(role string) string { return strings.ReplaceAll(role, "_", " ") }

// running is what a node that answered runs: "repeater v1.17.1", or the version alone when it gave no role;
// "ZephCore repeater v1.18.2" when its ver names ZephCore (as the backend tells it: flash's isZephCore).
func running(info nodecli.Info) string {
	v := info.Version.String()
	if info.Role != "" {
		v = roleWords(info.Role) + " " + v
	}
	if strings.Contains(strings.ToLower(info.Version.Raw), "zeph") {
		v = "ZephCore " + v
	}
	return v
}

// notSettable is the sentence for a node that answered but can't be set up: "Running repeater v1.10.0: flash
// v1.11.0 or newer to set it up here." (flash.Node.Reason is worded to follow it).
func notSettable(n flash.Node) string {
	if n.Reason == "" {
		return "Running " + running(n.Info) + "."
	}
	return "Running " + running(n.Info) + ": " + sentence(n.Reason)
}

// nodeSays is the Board's sentence about a probe's answer (design 9.1), "" when there is nothing to say (a silent
// board: its USB identity says what it can).
func nodeSays(n flash.Node) string {
	switch n.Problem {
	case flash.NoProblem:
		s := "Running " + running(n.Info)
		if n.HasCurrent && n.Current.Name != "" {
			s += ", “" + n.Current.Name + "”"
		}
		return s + "."
	case flash.Stuck:
		return "It echoes but doesn't answer: press its reset button (or unplug it if it has no battery) and press r."
	case flash.NotSettable:
		return notSettable(n)
	case flash.InBootloader:
		if n.Reason == "" {
			return "In its bootloader, ready to flash."
		}
		return sentence(n.Reason)
	case flash.Busy:
		return "Another program has this port open."
	case flash.Failed:
		if n.Reason == "" {
			return "Couldn't ask it."
		}
		return "Couldn't ask it: " + sentence(n.Reason)
	}
	// Silent says nothing; NoPermission is the dialout block under the list; Gone goes with the port.
	return ""
}

// answered says the node said what it is (ver, get role, board), so its Info is set.
func answered(n *flash.Node) bool {
	return n != nil && (n.Problem == flash.NoProblem || n.Problem == flash.NotSettable)
}

// stopWords are "Stop now?" on Flashing, worded by what stopping leaves (the picture's Risk), and Ctrl+C's question,
// the same ending "…and meshflash quits." (state.go's confirmStop and confirmQuit).
func stopWords(r flash.Risk, quit bool) string {
	var s string
	switch r {
	case flash.RiskErased:
		s = "Stop now? The board will have no working firmware until it's flashed again"
	case flash.RiskWritten:
		s = "Stop now? It's already written; meshflash just won't check it"
	case flash.RiskBootloader:
		s = "Stop now? Nothing is written yet; the board stays in its bootloader until reset"
	default:
		s = "Stop now? Nothing has been written yet: the board keeps its firmware"
	}
	if quit {
		return s + ", and meshflash quits."
	}
	return s + "."
}

// host is a URL's host ("flasher.meshcore.io"), or the URL itself when it has none.
func host(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	return p.Host
}

// follow moves *top so that lines [start, end) of a list show in h lines, moving as little as it can (a list keeps
// still while the selection moves inside it), and clamps it to the list.
func follow(top *int, start, end, n, h int) {
	if h < 1 {
		h = 1
	}
	if end-start > h {
		end = start + h
	}
	if start < *top {
		*top = start
	}
	if end > *top+h {
		*top = end - h
	}
	*top = max(0, min(*top, n-h))
}

// itemTop moves a list's top line forward to the start of the item it falls in (starts are each item's first line,
// in order), so no item shows only its last lines; the selected item, itself an item's start, stays in view.
func itemTop(top int, starts []int) int {
	for _, s := range starts {
		if s >= top {
			return s
		}
	}
	return top
}

// padRight pads s with spaces to w cells.
func padRight(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-width(s)))
}

// greyAll styles lines as secondary text, after wrapping (S14: style after wrapping, line by line).
func greyAll(lines []string) []string {
	for i, l := range lines {
		if l != "" {
			lines[i] = grey.Render(l)
		}
	}
	return lines
}

// listing words up to three names as "A, B, C".
func listing(names []string) string { return strings.Join(names, ", ") }

// shortHash is a SHA-256's first eight hex digits, as Confirm shows it ("97539d20…").
func shortHash(sum string) string {
	if len(sum) > 8 {
		return sum[:8] + "…"
	}
	return sum
}

// plainErr is an error's words as a sentence for a body.
func plainErr(err error) string { return sentence(fmt.Sprint(err)) }
