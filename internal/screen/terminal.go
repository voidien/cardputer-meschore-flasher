package screen

import "golang.org/x/sys/unix"

// quietTerminal is for --screen started from a terminal (the CLI app, or ssh -t): the device's keyboard reaches the
// terminal as well as the client, so the terminal would echo each key over the picture, turn Ctrl+C into SIGINT,
// and keep every line typed for the shell, which would run the beats as commands once the client ended. It switches
// echo, line editing and those signals off while the client runs; the returned function throws away what was typed
// and puts the terminal back. Anything that isn't a terminal is left alone.
func quietTerminal(fd int) (restore func()) {
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return func() {}
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	quiet.Iflag &^= unix.IXON
	quiet.Cc[unix.VMIN], quiet.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &quiet); err != nil {
		return func() {}
	}
	return func() {
		_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
		_ = unix.IoctlSetTermios(fd, unix.TCSETS, old)
	}
}
