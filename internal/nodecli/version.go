package nodecli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a MeshCore firmware version as `ver` reports it.
type Version struct {
	// Major, Minor and Patch are the version number: 1, 18, 0 for v1.18.0.
	Major, Minor, Patch int
	// Commit is the short git hash after the version number ("81dbae5"), or empty when ver gave none (a build made
	// without MeshCore's build.sh prints just "v1.18.0").
	Commit string
	// Raw is ver's whole reply, as the node printed it: "v1.18.0-81dbae5 (Build: 15-Oct-2026)".
	Raw string
}

// verRE is config.meshcore.io's parseFirmwareVersion pattern (src/gui.js:24-29, MIT, Copyright (c) 2026 Rastislav
// Vysoky), unanchored as there, plus the commit hash that build.sh appends (docs/meshcore-flashing.md 7.3): at
// least 4 hex digits ending at a word boundary, so a suffix that only starts with hex letters ("-beta", "-dev")
// isn't taken for a commit.
var verRE = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)(?:-([0-9a-fA-F]{4,40})\b)?`)

// ParseVersion reads a version out of ver's reply. It fails when the reply has no "X.Y.Z" in it.
func ParseVersion(reply string) (Version, error) {
	m := verRE.FindStringSubmatch(reply)
	if m == nil {
		return Version{}, fmt.Errorf("nodecli: %q has no firmware version in it", reply)
	}
	v := Version{Commit: strings.ToLower(m[4]), Raw: reply}
	var err error
	for i, p := range []*int{&v.Major, &v.Minor, &v.Patch} {
		if *p, err = strconv.Atoi(m[i+1]); err != nil {
			return Version{}, fmt.Errorf("nodecli: %q: version number out of range", reply)
		}
	}
	return v, nil
}

// AtLeast says whether v is major.minor.patch or newer.
func (v Version) AtLeast(major, minor, patch int) bool {
	if v.Major != major {
		return v.Major > major
	}
	if v.Minor != minor {
		return v.Minor > minor
	}
	return v.Patch >= patch
}

// String is the version as MeshCore's tags and file names write it: "v1.18.0".
func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Build is the version and commit as they appear in a release file's name ("v1.18.0-81dbae5"), or just the version
// when ver gave no commit.
func (v Version) Build() string {
	if v.Commit == "" {
		return v.String()
	}
	return v.String() + "-" + v.Commit
}

// NamedIn says whether a firmware file name (Xiao_nrf52_repeater-v1.18.0-81dbae5.zip) is this build: after an nRF52
// flash the bootloader never says whether the image was good, so the app asking `ver` and finding the file's
// version is the check (docs/meshcore-flashing.md 5.7). Without a commit from ver only the version is compared.
func (v Version) NamedIn(fileName string) bool {
	name := strings.ToLower(fileName)
	if v.Commit == "" {
		return strings.Contains(name, v.String()+"-") || strings.Contains(name, v.String()+".")
	}
	// The hash in a file name can be longer than ver's when 7 characters were ambiguous (git --short), so the
	// file's hash only has to start with ver's.
	return strings.Contains(name, v.String()+"-"+v.Commit)
}

// Same says whether w is the same firmware: the same version number, and the same commit when both have one.
func (v Version) Same(w Version) bool {
	if v.Major != w.Major || v.Minor != w.Minor || v.Patch != w.Patch {
		return false
	}
	return v.Commit == "" || w.Commit == "" || strings.HasPrefix(v.Commit, w.Commit) || strings.HasPrefix(w.Commit, v.Commit)
}
