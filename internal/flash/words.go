package flash

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"meshflash/internal/catalog"
	"meshflash/internal/nodecli"
)

// What the screens and screen.log are told. All the protocol knowledge of what a failure leaves the board with is
// here and in the jobs, in plain words the screens show as they are (docs/phase2-design.md 4.1, 4.2).

// The Outcome titles.
const (
	titleFlashed      = "Flashed"
	titleNotChecked   = "Flashed, not checked"
	titleStopped      = "Flashing stopped"
	titleNotFlashed   = "Not flashed"
	titleSetUp        = "Set up"
	titleSetDifferent = "Set up, but it came back different"
	titleSetupStopped = "Setup stopped"
	titleDownloaded   = "Downloaded"
	titleDownFailed   = "Download failed"
	titleDownStopped  = "Download stopped"
)

// Sentences more than one outcome uses.
const (
	nothingSent    = "Nothing was sent to the board: it still has its old firmware."
	appErased      = "Its app is erased."
	askDoublePress = "Press r; meshflash then asks you to press reset twice quickly."
	pressTwice     = "Press reset twice quickly, then press r."
	plugItIn       = "Plug it in and press r."
	notSaved       = "The file isn't saved; press y on Confirm to download it."
	nothingChanged = "Nothing on the node was changed."
	// zephCoreReason follows "Running ZephCore repeater v1.18.2: " on the Board (Node.Reason); zephCoreSetup stands
	// alone, as a stopped setup's Summary.
	zephCoreReason = "meshflash sets up only MeshCore's own repeater and room-server firmware."
	zephCoreSetup  = "ZephCore: " + zephCoreReason
	unconfirmed    = "Looks like its bootloader, but meshflash can't confirm it: press reset twice quickly."
	staleReason    = "Its last flash stopped part-way: meshflash asks you to press reset twice quickly when you flash it."
	atStartReason  = "It was already in its bootloader when meshflash started: meshflash asks you to press reset " +
		"twice quickly when you flash it."
	unknownSDAsk = "The catalog doesn't say which SoftDevice this board has: if the file needs another, the " +
		"bootloader refuses it and its app is erased. Flash anyway?"
	pressResetTwiceStep = "Press the board's reset button twice quickly"
	pressResetOnce      = "Press its reset button once to start it."
	touchedState        = "Nothing was written: it may be in its bootloader now, with its old firmware."
	// waitingOnUSB is a step waiting for the board to come back on USB; once it has, the step reads "Back on USB
	// (ttyACM0)". A step under way says what is being done, never what it hopes for.
	waitingOnUSB = "Waiting for it on USB"
	// stoppingErase is the erase step's note after a stop: nrfdfu checks between frames, so the stop waits for the
	// erase, whose running time and expected length the step shows beside it ("(0:04, about 10 s)").
	stoppingErase = "Stopping once the erase ends"
)

// splitRecovery makes esp's and nrfdfu's Recovery text State and Recovery: its first sentence says what the board
// is left with, the rest how to recover, so the Result screen never says the state twice (4.1, "Words"). A text of
// one sentence with a colon splits there ("Written and checked, but the board didn't restart: press its RESET
// button, …").
func splitRecovery(s string) (state, recovery string) {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1], strings.TrimSpace(s[i+2:])
	}
	if i := strings.Index(s, ": "); i >= 0 {
		return s[:i] + ".", sentence(s[i+2:])
	}
	return s, ""
}

// sentence starts s with a capital and ends it with a full stop.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "?") && !strings.HasSuffix(s, "!") {
		s += "."
	}
	return s
}

// clause is s as part of a sentence: no capital, no full stop ("Couldn't ask it: <clause>.").
func clause(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if s == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(s)
	if n < len(s) {
		next, _ := utf8.DecodeRuneInString(s[n:])
		if unicode.IsUpper(next) {
			return s // an acronym ("SHA-256", "USB"): leave it
		}
	}
	return string(unicode.ToLower(r)) + s[n:]
}

// plain is an error's text without the Go package prefixes the Phase 1 packages put on it ("nodecli: ", "esp: ",
// …), which mean nothing to the owner.
func plain(err error) string {
	s := err.Error()
	for {
		trimmed := s
		for _, p := range []string{"nodecli: ", "esp: ", "nrfdfu: ", "serial: ", "flash: ", "catalog: ", "fakeworld: ",
			"refused: "} {
			trimmed = strings.ReplaceAll(trimmed, p, "")
		}
		if trimmed == s {
			return s
		}
		s = trimmed
	}
}

// roleName names a firmware as the screens do: its title, and " · " and its subtitle when there is one
// ("Companion · Bluetooth"); never by its key.
func roleName(fw catalog.Firmware) string {
	if fw.SubTitle != "" {
		return fw.Title + " · " + fw.SubTitle
	}
	return fw.Title
}

// what names a flash: "Repeater v1.18.0".
func what(req Request) string { return roleName(req.Firmware) + " " + req.Version.Name }

// sizeText is a size in decimal units, as the screens write it ("361 KB", "1.3 MB").
func sizeText(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d bytes", n)
	case n < 1000*1000:
		return fmt.Sprintf("%d KB", (n+500)/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}

// secs is a wait in whole seconds, at least 1, for a note ("about 8 s").
func secs(d time.Duration) int {
	s := int(d.Seconds() + 0.5)
	if s < 1 {
		s = 1
	}
	return s
}

// meshCoreConsole says the firmware is MeshCore's own repeater or room server: the only firmware meshflash asks its
// version after a flash and offers to set up (4.1).
func meshCoreConsole(fw catalog.Firmware) bool {
	return fw.Source == catalog.SourceMeshCore && (fw.Role == "repeater" || fw.Role == "roomServer")
}

// consoleName is what the board should answer as: "a repeater", "a room server".
func consoleName(fw catalog.Firmware) string {
	if fw.Role == "roomServer" {
		return "a room server"
	}
	return "a repeater"
}

// isZephCore says a console's ver names ZephCore (case-insensitive "zeph"): setup knows only MeshCore's own.
func isZephCore(info nodecli.Info) bool {
	return strings.Contains(strings.ToLower(info.Version.Raw), "zeph")
}

// settableReason is why a node that answered can't be set up (nodecli.Info.Settable), worded to follow the Board's
// "Running repeater v1.10.0: " (Node.Reason): "flash v1.11.0 or newer to set it up here.".
func settableReason(info nodecli.Info) string {
	if !info.Version.AtLeast(1, 11, 0) {
		return "flash v1.11.0 or newer to set it up here."
	}
	return "meshflash sets up only repeater and room-server firmware."
}

// stopped says err is a context ending: the owner (or a quit) stopped the job.
func stopped(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
