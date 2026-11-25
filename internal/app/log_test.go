package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// screen.log is started afresh each run, the owner's only, with its name, version and start time first (S5).
func TestScreenLogFreshEachRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache", "meshflash")
	open := func() *os.File {
		t.Helper()
		f, err := openScreenLog(dir, "meshflash", "0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	contents := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, "screen.log"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	mode := func(path string) os.FileMode {
		t.Helper()
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}

	before := time.Now().Add(-time.Second)
	f := open()
	fmt.Fprintln(f, "meshflash: the first run's line")
	f.Close()
	first := regexp.MustCompile(`^meshflash 0\.1\.0 started (\S+)\n`).FindStringSubmatch(contents())
	if first == nil {
		t.Fatalf("first line %q, want \"meshflash 0.1.0 started <RFC3339>\"", contents())
	}
	if at, err := time.Parse(time.RFC3339, first[1]); err != nil || at.Before(before) || at.After(time.Now()) {
		t.Errorf("started %q: %v, not now", first[1], err)
	}
	if m := mode(dir); m != 0o700 {
		t.Errorf("its folder is %v, want 0700", m)
	}
	if m := mode(filepath.Join(dir, "screen.log")); m != 0o600 {
		t.Errorf("screen.log is %v, want 0600", m)
	}

	open().Close()
	if got := contents(); strings.Contains(got, "first run") || strings.Count(got, "\n") != 1 {
		t.Errorf("the second run's screen.log is %q: the first run's lines are still there", got)
	}

	os.Chmod(filepath.Join(dir, "screen.log"), 0o644)
	open().Close()
	if m := mode(filepath.Join(dir, "screen.log")); m != 0o600 {
		t.Errorf("a screen.log left readable by others is still %v, want 0600", m)
	}

	file := filepath.Join(t.TempDir(), "a-file")
	os.WriteFile(file, nil, 0o600)
	if _, err := openScreenLog(filepath.Join(file, "meshflash"), "meshflash", "0.1.0"); err == nil {
		t.Error("a cache folder that can't be made gave a screen.log")
	}
}

// When screen.log can't be opened, its lines go to stderr on the LCD (the launcher's journal) and nowhere in a
// terminal, whose stderr is the screen; stderr says why either way.
func TestScreenLogFallback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	os.WriteFile(file, nil, 0o600)
	p := stubProgram(t)
	for _, onLCD := range []bool{true, false} {
		var stderr bytes.Buffer
		log := p.openLog(filepath.Join(file, "meshflash"), flags{screen: onLCD}, &stderr)
		if !strings.Contains(stderr.String(), "can't write screen.log") {
			t.Errorf("on the LCD %v: stderr %q doesn't say screen.log failed", onLCD, stderr.String())
		}
		stderr.Reset()
		log.linef("meshflash: a line")
		if got := stderr.String(); (got == "meshflash: a line\n") != onLCD || log.file != nil {
			t.Errorf("on the LCD %v: a line went to stderr as %q", onLCD, got)
		}
		log.close()
	}
}

// The screens, the backend and internal/screen write screen.log from their own goroutines: every line arrives whole,
// one newline each, and nothing written after the end of the program fails.
func TestScreenLogLinesNeverInterleave(t *testing.T) {
	var buf bytes.Buffer
	log := &logWriter{w: &buf}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				if i%2 == 0 {
					log.linef("writer %d: line %d\n", g, i)
				} else {
					fmt.Fprintf(log, "writer %d: line %d\n", g, i)
				}
			}
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	whole := regexp.MustCompile(`^writer [0-7]: line [0-9]+$`)
	for _, l := range lines {
		if !whole.MatchString(l) {
			t.Fatalf("a line was cut or mixed with another: %q", l)
		}
	}
	if len(lines) != 800 {
		t.Errorf("%d lines, want 800", len(lines))
	}
	log.close()
	log.linef("a probe's line as the program ends")
	io.WriteString(log, "and another\n")
	if strings.Contains(buf.String(), "probe") {
		t.Error("a line written after close reached the log")
	}
}
