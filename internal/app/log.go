package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// screenLog is the log's name, in the cache folder (~/.cache/<Name>).
const screenLog = "screen.log"

// openScreenLog starts screen.log afresh in cacheDir, the owner's only (0600 in a 0700 folder), its first line
// "<name> <version> started <RFC3339>" (S5). Unlike the kit's it is opened on every run, in the terminal too (SPEC.md
// 8): a quit that stops a flash writes there what the board is left with, whichever way meshflash was started.
func openScreenLog(cacheDir, name, version string) (*os.File, error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(cacheDir, screenLog), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	// The mode above is only for a new file; one an older build left, or one copied in, is made private too.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintln(f, name, version, "started", time.Now().Format(time.RFC3339)); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// openLog opens this run's screen.log. If it can't be opened, its lines go to stderr on the LCD (the launcher's
// journal) and nowhere in a terminal, where stderr is the screen; stderr says why either way, before the screens
// start (in a terminal it is still there once the alternate screen is gone).
func (p Program) openLog(cacheDir string, f flags, stderr io.Writer) *logWriter {
	file, err := openScreenLog(cacheDir, p.Name, p.Version)
	if err == nil {
		return &logWriter{w: file, file: file}
	}
	fmt.Fprintf(stderr, "%s: can't write %s: %v\n", p.Command, screenLog, err)
	if f.screen || f.screenTest {
		return &logWriter{w: stderr}
	}
	return &logWriter{w: io.Discard}
}

// logWriter is screen.log as the whole program writes it: the screens (ui.Options.Log), the backend (Deps.Logf), a
// demo's world (Env.Log), internal/screen and the app itself, from their own goroutines. Each Write is whole, under
// one lock, so lines never interleave.
type logWriter struct {
	mu   sync.Mutex
	w    io.Writer
	file *os.File // screen.log itself; nil when it couldn't be opened and the lines go elsewhere
}

func (l *logWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

// linef writes one line: format and args as fmt's, the newline added. It is also the backend's Deps.Logf.
func (l *logWriter) linef(format string, args ...any) { writeLine(l, format, args...) }

// close closes screen.log at the end of run. A line still coming after it (a probe's goroutine as the program ends)
// goes nowhere.
func (l *logWriter) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
	}
	l.w = io.Discard
}

// writeLine writes one line to w, with exactly one newline at its end, in one Write.
func writeLine(w io.Writer, format string, args ...any) {
	io.WriteString(w, strings.TrimRight(fmt.Sprintf(format, args...), "\n")+"\n")
}
