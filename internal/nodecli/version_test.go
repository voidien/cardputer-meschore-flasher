package nodecli

import "testing"

func TestParseVersion(t *testing.T) {
	for _, c := range []struct {
		in                  string
		major, minor, patch int
		commit, build       string
	}{
		{"v1.18.0-81dbae5 (Build: 15-Oct-2026)", 1, 18, 0, "81dbae5", "v1.18.0-81dbae5"},
		// The config tool's own example (gui.js:25).
		{"v1.13.0-295f67d (Build: 15-Feb-2026)", 1, 13, 0, "295f67d", "v1.13.0-295f67d"},
		// A build without build.sh: MyMesh.h's fallbacks.
		{"v1.18.0 (Build: 15 Oct 2026)", 1, 18, 0, "", "v1.18.0"},
		// A hash longer than 7 when 7 was ambiguous.
		{"v1.14.1-467959cc (Build: 20-Mar-2026)", 1, 14, 1, "467959cc", "v1.14.1-467959cc"},
		// A suffix that only starts with hex letters isn't a commit.
		{"v1.18.0-beta (Build: 15-Oct-2026)", 1, 18, 0, "", "v1.18.0"},
		{"v1.18.0-dev", 1, 18, 0, "", "v1.18.0"},
		{"v1.18.0-81dbae5-dirty (Build: 15-Oct-2026)", 1, 18, 0, "81dbae5", "v1.18.0-81dbae5"},
	} {
		v, err := ParseVersion(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if v.Major != c.major || v.Minor != c.minor || v.Patch != c.patch || v.Commit != c.commit || v.Raw != c.in {
			t.Errorf("%q: got %+v", c.in, v)
		}
		if v.Build() != c.build {
			t.Errorf("%q: Build() = %q, want %q", c.in, v.Build(), c.build)
		}
	}
	for _, bad := range []string{"Unknown command", "", "v1.18"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	v, _ := ParseVersion("v1.14.0-9f1a3ea")
	for _, c := range []struct {
		a, b, p int
		want    bool
	}{
		{1, 14, 0, true}, {1, 13, 9, true}, {1, 14, 1, false}, {1, 15, 0, false}, {0, 99, 99, true}, {2, 0, 0, false},
	} {
		if got := v.AtLeast(c.a, c.b, c.p); got != c.want {
			t.Errorf("v1.14.0 AtLeast(%d.%d.%d) = %v", c.a, c.b, c.p, got)
		}
	}
	w, _ := ParseVersion("v1.18.0-81dbae5 (Build: 15-Oct-2026)")
	if !w.NamedIn("Xiao_nrf52_repeater-v1.18.0-81dbae5.zip") || !w.NamedIn("Heltec_v3_room_server-v1.18.0-81dbae59-merged.bin") {
		t.Error("v1.18.0-81dbae5 not found in its own file names")
	}
	if w.NamedIn("Xiao_nrf52_repeater-v1.17.1-d929643.zip") {
		t.Error("v1.18.0 matched a v1.17.1 file")
	}
	bare, _ := ParseVersion("v1.18.0 (Build: 15 Oct 2026)")
	if !bare.NamedIn("RAK_4631_repeater-v1.18.0-81dbae5.zip") || bare.NamedIn("RAK_4631_repeater-v1.18.01.zip") {
		t.Error("a version without a commit should match on the version alone")
	}
	other, _ := ParseVersion("v1.18.0-0000000")
	if !w.Same(bare) || w.Same(other) || !w.Same(w) {
		t.Error("Same is wrong")
	}
}
