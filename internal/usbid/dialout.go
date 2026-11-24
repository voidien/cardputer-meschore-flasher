package usbid

import (
	"os"
	"os/user"
	"strconv"
)

// DialoutHint is what the owner runs once so meshflash can open serial ports (SPEC.md §9.3). The reboot is needed
// because the launcher's user manager keeps the groups it started with: restarting the app isn't enough (research
// §6.7).
const DialoutHint = "sudo usermod -aG dialout void\nsudo reboot"

// DialoutStatus says whether this process has the standard permission to open serial ports: udev gives /dev/ttyACM*
// and /dev/ttyUSB* to group dialout, mode 0660 (research §6.7), so it has it when it runs as root or has dialout among
// its groups. When it hasn't, hint is DialoutHint, for the Board screen to show.
//
// It looks at this process's groups (os.Getgroups), not at /etc/group: after usermod the group file already lists
// the user, but a process started before the reboot still lacks the group and still can't open the port.
//
// ok == false is a strong hint, not proof: a udev rule giving void the ttys, or an ACL from setfacl (research §6.7's
// alternatives), lets a process outside dialout open them too. So an open() that fails with EACCES is the real
// answer, and a port that opens is fine whatever this says.
func DialoutStatus() (ok bool, hint string) {
	return dialoutStatus(osCreds{
		euid:        os.Geteuid,
		egid:        os.Getegid,
		groups:      os.Getgroups,
		lookupGroup: user.LookupGroup, // pure Go with CGO_ENABLED=0: it reads /etc/group
	})
}

// osCreds is what DialoutStatus asks the system, so tests can give it any user.
type osCreds struct {
	euid        func() int
	egid        func() int
	groups      func() ([]int, error)
	lookupGroup func(name string) (*user.Group, error)
}

func dialoutStatus(c osCreds) (bool, string) {
	if c.euid() == 0 {
		return true, ""
	}
	g, err := c.lookupGroup("dialout")
	if err != nil {
		return false, DialoutHint
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return false, DialoutHint
	}
	if c.egid() == gid {
		return true, ""
	}
	groups, err := c.groups()
	if err != nil {
		return false, DialoutHint
	}
	for _, id := range groups {
		if id == gid {
			return true, ""
		}
	}
	return false, DialoutHint
}
