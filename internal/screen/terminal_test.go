package screen

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// openPty opens a pseudo-terminal pair: what's written to the first arrives as typed input on the second.
func openPty(t *testing.T) (master, tty *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pseudo-terminals here:", err)
	}
	t.Cleanup(func() { m.Close() })
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return m, s
}

// Started from the CLI app, the terminal mustn't echo the keys over the picture, nor keep them for the shell.
func TestQuietTerminal(t *testing.T) {
	master, tty := openPty(t)
	fd := int(tty.Fd())
	before, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if before.Lflag&unix.ECHO == 0 || before.Lflag&unix.ICANON == 0 {
		t.Skip("the new terminal doesn't start echoing lines")
	}
	restore := quietTerminal(fd)
	now, _ := unix.IoctlGetTermios(fd, unix.TCGETS)
	if now.Lflag&(unix.ECHO|unix.ICANON|unix.ISIG) != 0 {
		t.Errorf("while the client runs, echo, lines or signals are still on: lflag %#x", now.Lflag)
	}
	master.Write([]byte("Mara rings the bell\nrm -rf something\n"))
	waitForInput(t, fd)
	restore()
	if n, _ := unix.IoctlGetInt(fd, unix.TIOCINQ); n != 0 {
		t.Errorf("%d typed bytes left for the shell to run", n)
	}
	after, _ := unix.IoctlGetTermios(fd, unix.TCGETS)
	if after.Lflag != before.Lflag || after.Iflag != before.Iflag {
		t.Errorf("the terminal wasn't put back: lflag %#x, want %#x", after.Lflag, before.Lflag)
	}
	if restore := quietTerminal(int(master.Fd()) + 1000); restore == nil {
		t.Error("no restore function for something that isn't a terminal")
	}
}

// waitForInput waits until the terminal holds what the master wrote.
func waitForInput(t *testing.T, fd int) {
	t.Helper()
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if n, err := unix.Poll(fds, 2000); err != nil || n == 0 {
		t.Fatalf("the typed input never arrived (%d, %v)", n, err)
	}
}
