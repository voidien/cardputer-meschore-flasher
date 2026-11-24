package usbid

import (
	"errors"
	"os/user"
	"testing"
)

func TestDialoutStatus(t *testing.T) {
	dialout := func(name string) (*user.Group, error) {
		if name != "dialout" {
			return nil, user.UnknownGroupError(name)
		}
		return &user.Group{Gid: "20", Name: "dialout"}, nil
	}
	creds := func(euid, egid int, groups []int) osCreds {
		return osCreds{
			euid:        func() int { return euid },
			egid:        func() int { return egid },
			groups:      func() ([]int, error) { return groups, nil },
			lookupGroup: dialout,
		}
	}
	// void on the CardputerZero today: video, input, sudo (kit device.md:28).
	voidNow := creds(1000, 1000, []int{1000, 44, 105, 27})

	tests := []struct {
		name string
		c    osCreds
		ok   bool
	}{
		{"void, not in dialout", voidNow, false},
		{"void after usermod and a reboot", creds(1000, 1000, []int{1000, 20, 44, 105, 27}), true},
		{"dialout as the primary group", creds(1000, 20, nil), true},
		{"root", creds(0, 0, nil), true},
		{"no dialout group on this system", osCreds{euid: voidNow.euid, egid: voidNow.egid, groups: voidNow.groups,
			lookupGroup: func(string) (*user.Group, error) { return nil, user.UnknownGroupError("dialout") }}, false},
		{"getgroups fails", osCreds{euid: voidNow.euid, egid: voidNow.egid, lookupGroup: dialout,
			groups: func() ([]int, error) { return nil, errors.New("no") }}, false},
		{"a group file with a strange GID", osCreds{euid: voidNow.euid, egid: voidNow.egid, groups: voidNow.groups,
			lookupGroup: func(string) (*user.Group, error) { return &user.Group{Gid: "x"}, nil }}, false},
	}
	for _, tt := range tests {
		ok, hint := dialoutStatus(tt.c)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v, want %v", tt.name, ok, tt.ok)
		}
		if wantHint := map[bool]string{true: "", false: DialoutHint}[tt.ok]; hint != wantHint {
			t.Errorf("%s: hint = %q, want %q", tt.name, hint, wantHint)
		}
	}
	// The hint is exactly SPEC.md §9.3's two commands.
	if DialoutHint != "sudo usermod -aG dialout void\nsudo reboot" {
		t.Errorf("DialoutHint = %q", DialoutHint)
	}
	// The real one runs without failing; whether this machine's user is in dialout isn't the test's business.
	if ok, hint := DialoutStatus(); ok == (hint != "") {
		t.Errorf("DialoutStatus() = %v, %q", ok, hint)
	}
}
