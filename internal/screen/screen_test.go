package screen

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// psf2 builds a 6×12 PSF2 font: glyph 0 blank, 1 "?" (top row inked), 2 "A" (left column inked, also mapped to Greek
// Alpha, with a combining sequence after it), 3 "“" (the full cell inked).
func psf2(t *testing.T) []byte {
	t.Helper()
	const w, h, n = 6, 12, 4
	var b bytes.Buffer
	for _, v := range []uint32{0x864ab572, 0, 32, 1, n, h, h, w} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	glyphs := make([][]byte, n)
	for i := range glyphs {
		glyphs[i] = make([]byte, h)
	}
	glyphs[1][0] = 0xfc
	for y := 0; y < h; y++ {
		glyphs[2][y] = 0x80
		glyphs[3][y] = 0xfc
	}
	for _, g := range glyphs {
		b.Write(g)
	}
	for _, entry := range []string{" ", "?", "AΑ\xfeÁ", "“"} {
		b.WriteString(entry)
		b.WriteByte(0xff)
	}
	return b.Bytes()
}

func testFont(t *testing.T) *Font {
	t.Helper()
	f, err := LoadFont(bytes.NewReader(psf2(t)))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestLoadFontPlainAndGzipped(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(psf2(t))
	zw.Close()
	for name, data := range map[string][]byte{"plain": psf2(t), "gzipped": gz.Bytes()} {
		f, err := LoadFont(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if f.W != 6 || f.H != 12 || f.RowBytes != 1 {
			t.Errorf("%s: size %d×%d, %d bytes a row", name, f.W, f.H, f.RowBytes)
		}
		if !f.Has('A') || !f.Has('Α') || !f.Has('“') || f.Has('B') {
			t.Errorf("%s: wrong characters covered", name)
		}
		if f.Glyph('B')[0] != 0xfc {
			t.Errorf("%s: a missing character isn't drawn as \"?\"", name)
		}
	}
	if _, err := LoadFont(strings.NewReader("not a font at all, just some text")); err == nil {
		t.Error("text loaded as a font")
	}
}

func TestPSF1(t *testing.T) {
	data := []byte{0x36, 0x04, 0x02, 2} // 256 glyphs of 2 rows, with a Unicode table
	glyphs := make([]byte, 256*2)
	glyphs['x'*2] = 0xff
	data = append(data, glyphs...)
	for i := 0; i < 256; i++ {
		if i == 'x' {
			data = binary.LittleEndian.AppendUint16(data, '×')
		}
		data = binary.LittleEndian.AppendUint16(data, 0xffff)
	}
	f, err := LoadFont(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if f.W != 8 || f.H != 2 || f.Glyph('×')[0] != 0xff {
		t.Errorf("PSF1 read wrong: %d×%d", f.W, f.H)
	}
}

func TestStandInsOnlyForTheSizeTheyreDrawnFor(t *testing.T) {
	f := testFont(t)
	f.AddStandIns()
	for _, r := range "○✎" {
		if !f.Has(r) || len(f.Glyph(r)) != 12 {
			t.Errorf("%q has no stand-in", r)
		}
	}
	other, _ := LoadFont(bytes.NewReader([]byte{0x36, 0x04, 0, 2}))
	if other != nil {
		other.AddStandIns()
		if other.Has('○') {
			t.Error("an 8-pixel font got a 6-pixel stand-in")
		}
	}
}

func TestParseColorsAndReverse(t *testing.T) {
	g := Parse("ab\x1b[90mc\x1b[0m\x1b[7md\x1b[27me\x1b[31;44mf\x1b[39;49mg", 10, 2)
	want := []Cell{
		{R: 'a', FG: DefaultFG, BG: DefaultBG},
		{R: 'b', FG: DefaultFG, BG: DefaultBG},
		{R: 'c', FG: 8, BG: DefaultBG},
		{R: 'd', FG: DefaultFG, BG: DefaultBG, Reverse: true},
		{R: 'e', FG: DefaultFG, BG: DefaultBG},
		{R: 'f', FG: 1, BG: 4},
		{R: 'g', FG: DefaultFG, BG: DefaultBG},
	}
	for i, c := range want {
		if g[0][i] != c {
			t.Errorf("cell %d = %+v, want %+v", i, g[0][i], c)
		}
	}
	if g[1][0] != blank {
		t.Error("the second row isn't blank")
	}
}

func TestParseKeepsToTheGrid(t *testing.T) {
	view := "a line that is longer than the grid is wide\nsecond\nthird\nfourth"
	g := Parse(view, 10, 3)
	if got := g.Text(); got != "a line tha\nsecond\nthird" {
		t.Errorf("text %q", got)
	}
}

func TestParseSkipsOtherSequencesAndControls(t *testing.T) {
	g := Parse("\x1b[?25l\x1b[2Ka\x1b]0;title\x07b\x1b[38;5;208mc\x1b[38;2;1;2;3md\te\r", 10, 1)
	if got := g.Text(); got != "abcde" {
		t.Errorf("text %q", got)
	}
	if g[0][2].FG != DefaultFG || g[0][3].FG != DefaultFG {
		t.Error("256-color or true color changed the color, or their arguments were read as other codes")
	}
}

func TestParseWideAndCombining(t *testing.T) {
	g := Parse("漢x é", 10, 1)
	if g[0][0].R != '漢' || g[0][1].R != ' ' || g[0][2].R != 'x' {
		t.Errorf("a wide character should take two cells: %q", g.Text())
	}
	if got := g.Text(); got != "漢 x e" {
		t.Errorf("a combining mark took a cell: %q", got)
	}
	if g := Parse("ab漢", 3, 1); g.Text() != "ab" {
		t.Errorf("a wide character that doesn't fit should be dropped: %q", g.Text())
	}
}

func TestParseMeasuresWholeCharacters(t *testing.T) {
	// Each of these is one character on screen, however many runes it's made of; what follows it must land where the
	// screens' wrapping (runewidth.StringWidth) planned, or the end of the line is pushed off the edge.
	for _, s := range []string{"👍🏽", "👨\u200d👩\u200d👧", "e\u0301", "🇫🇷", "❤\ufe0f", "1\ufe0f\u20e3", "漢"} {
		want := runewidth.StringWidth(s)
		g := Parse("a"+s+"\x1b[90mz\x1b[0m", 10, 1)
		if g[0][1+want].R != 'z' || g[0][1+want].FG != 8 {
			t.Errorf("%+q: z at the wrong column, or lost its color: %q", s, g.Text())
		}
		for x := 2; x < 1+want; x++ {
			if g[0][x].R != ' ' {
				t.Errorf("%+q: cell %d after its first should be blank, is %q", s, x, g[0][x].R)
			}
		}
	}
	if g := Parse("👍🏽 Mara", 7, 1); g.Text() != "👍  Mara" {
		t.Errorf("a line that fits once its emoji is measured whole was cut: %q", g.Text())
	}
	if g := Parse("e\u0301", 3, 1); g[0][0].R != 'e' || g[0][1].R != ' ' {
		t.Errorf("an accented letter should be drawn as its letter in one cell: %q", g.Text())
	}
	if g := Parse("\u200dab\u0085c", 5, 1); g.Text() != "abc" {
		t.Errorf("a lone joiner or a C1 control took a cell: %q", g.Text())
	}
	if g := Parse("a\xff\xfe\xc3b", 6, 1); g[0][0].R != 'a' || !strings.HasSuffix(g.Text(), "b") {
		t.Errorf("broken UTF-8 lost the text around it: %q", g.Text())
	}
}

// memFB is a framebuffer in memory that counts its writes.
type memFB struct {
	mu     sync.Mutex
	b      []byte
	writes int
}

func (m *memFB) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes++
	return copy(m.b[off:], p), nil
}

func (m *memFB) pixel(geo Geometry, x, y int) uint16 {
	return binary.LittleEndian.Uint16(m.b[y*geo.Stride+x*2:])
}

var device = Geometry{Width: 320, Height: 170, Stride: 640}

func TestScreenLayout(t *testing.T) {
	s, err := NewScreen(&memFB{b: make([]byte, 640*170)}, device, testFont(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Cols != 53 || s.Rows != 14 || s.left != 1 || s.top != 1 {
		t.Errorf("layout %d×%d at (%d, %d), want 53×14 at (1, 1)", s.Cols, s.Rows, s.left, s.top)
	}
	if _, err := NewScreen(&memFB{}, Geometry{Width: 60, Height: 30, Stride: 120}, testFont(t)); err == nil {
		t.Error("a screen too small for the client was accepted")
	}
}

func TestDrawPixels(t *testing.T) {
	fb := &memFB{b: make([]byte, 640*170)}
	s, _ := NewScreen(fb, device, testFont(t))
	g := Parse("A\x1b[90mA\x1b[0m\x1b[7mA\x1b[0m", s.Cols, s.Rows)
	if err := s.Draw(g); err != nil {
		t.Fatal(err)
	}
	fg, grey, bg := palette[DefaultFG], palette[8], palette[DefaultBG]
	x0, y0 := s.left, s.top
	checks := []struct {
		x, y int
		want uint16
		what string
	}{
		{x0, y0, fg, "the A's inked column"},
		{x0 + 1, y0, bg, "beside it"},
		{x0 + 6, y0 + 5, grey, "the grey A"},
		{x0 + 12, y0 + 11, bg, "the reversed A's ink, in the background color"},
		{x0 + 13, y0, fg, "the reversed A's background, in the text color"},
		{0, y0, bg, "the left margin"},
	}
	for _, c := range checks {
		if got := fb.pixel(device, c.x, c.y); got != c.want {
			t.Errorf("%s at (%d, %d) = %#04x, want %#04x", c.what, c.x, c.y, got, c.want)
		}
	}
}

func TestClearBlanksTheWholeScreen(t *testing.T) {
	fb := &memFB{b: make([]byte, 640*170)}
	for i := range fb.b {
		fb.b[i] = 0xff // what the launcher left: its "loading" picture
	}
	s, _ := NewScreen(fb, device, testFont(t))
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	bg := palette[DefaultBG]
	for _, p := range [][2]int{{0, 0}, {319, 0}, {0, 169}, {319, 169}, {160, 85}} {
		if got := fb.pixel(device, p[0], p[1]); got != bg {
			t.Errorf("(%d, %d) = %#04x after Clear, want the background %#04x", p[0], p[1], got, bg)
		}
	}
}

func TestDrawWritesOnlyChangedRows(t *testing.T) {
	fb := &memFB{b: make([]byte, 640*170)}
	s, _ := NewScreen(fb, device, testFont(t))
	s.Draw(Parse("one\ntwo\nthree", s.Cols, s.Rows))
	if fb.writes != s.Rows {
		t.Errorf("the first draw wrote %d rows, want all %d", fb.writes, s.Rows)
	}
	fb.writes = 0
	s.Draw(Parse("one\n2\nthree", s.Cols, s.Rows))
	if fb.writes != 1 {
		t.Errorf("changing one row wrote %d rows", fb.writes)
	}
	fb.writes = 0
	s.Draw(Parse("one\n2\nthree", s.Cols, s.Rows))
	if fb.writes != 0 {
		t.Errorf("an unchanged frame wrote %d rows", fb.writes)
	}
}

func TestFramebufferGeometry(t *testing.T) {
	dir := t.TempDir()
	write := func(name, v string) { os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o600) }
	write("virtual_size", "320,170")
	write("bits_per_pixel", "16")
	geo, err := FramebufferGeometry(dir)
	if err != nil || geo != device {
		t.Errorf("got %+v, %v", geo, err)
	}
	write("stride", "768")
	if geo, _ := FramebufferGeometry(dir); geo.Stride != 768 {
		t.Errorf("stride %d, want sysfs's 768", geo.Stride)
	}
	write("bits_per_pixel", "32")
	if _, err := FramebufferGeometry(dir); err == nil {
		t.Error("32 bits a pixel was accepted")
	}
}

func keyString(t *testing.T, kb *Keyboard, code uint16) string {
	t.Helper()
	msg, ok := kb.Key(code, 1)
	if !ok {
		return "(none)"
	}
	return msg.String()
}

func TestKeyboard(t *testing.T) {
	var kb Keyboard
	cases := []struct {
		code uint16
		want string
	}{
		{30, "a"}, {50, "m"}, {2, "1"}, {11, "0"}, {93, "?"}, {74, "["}, {81, "'"}, {82, "\""}, {94, ")"},
		{keySpace, " "}, {keyEnter, "enter"}, {keyTab, "tab"}, {keyBackspace, "backspace"}, {keyEsc, "esc"},
		{keyUp, "up"}, {keyDown, "down"}, {keyLeft, "left"}, {keyRight, "right"}, {keyHome, "home"}, {keyEnd, "end"},
		{keyPageUp, "pgup"}, {keyPageDown, "pgdown"}, {keyDelete, "delete"},
		{389, "(none)"}, {383, "(none)"}, {59, "(none)"}, // Fn, Sym and F1 send nothing themselves
	}
	for _, c := range cases {
		if got := keyString(t, &kb, c.code); got != c.want {
			t.Errorf("code %d = %q, want %q", c.code, got, c.want)
		}
	}
	if msg, _ := kb.Key(keySpace, 1); msg.Type != tea.KeySpace {
		t.Error("Space isn't a space key, which the text box looks for")
	}
}

func TestKeyboardModifiers(t *testing.T) {
	var kb Keyboard
	kb.Key(keyLeftShift, 1)
	if got := keyString(t, &kb, 30); got != "A" {
		t.Errorf("Shift+a = %q", got)
	}
	if got := keyString(t, &kb, 2); got != "!" {
		t.Errorf("Shift+1 = %q", got)
	}
	if got := keyString(t, &kb, keyTab); got != "shift+tab" {
		t.Errorf("Shift+Tab = %q", got)
	}
	kb.Key(keyLeftShift, 0)
	kb.Key(keyLeftCtrl, 1)
	if msg, _ := kb.Key(46, 1); msg.Type != tea.KeyCtrlC {
		t.Errorf("Ctrl+c = %q", msg.String())
	}
	kb.Key(keyLeftCtrl, 0)
	kb.Key(keyLeftAlt, 1)
	if got := keyString(t, &kb, keyLeftAlt); got != "(none)" {
		t.Errorf("Alt itself sent %q", got)
	}
	if got := keyString(t, &kb, 36); got != "j" {
		t.Errorf("Alt+j = %q, want a plain j: Alt adds nothing in the CLI app either", got)
	}
	kb.Key(keyRightAlt, 1)
	if got := keyString(t, &kb, keyEnter); got != "enter" {
		t.Errorf("Alt+Enter = %q, want a plain enter", got)
	}
	kb.Key(keyRightAlt, 0)
	kb.Key(keyLeftAlt, 0)
	if got := keyString(t, &kb, 30); got != "a" {
		t.Errorf("after letting go of the modifiers, a = %q", got)
	}
	if _, ok := kb.Key(30, 0); ok {
		t.Error("a key going up sent something")
	}
	if got, _ := kb.Key(keyBackspace, 2); got.Type != tea.KeyBackspace {
		t.Error("a held Backspace doesn't repeat")
	}
}

func event(code uint16, value int32) []byte {
	b := make([]byte, eventSize)
	binary.LittleEndian.PutUint16(b[16:], 1)
	binary.LittleEndian.PutUint16(b[18:], code)
	binary.LittleEndian.PutUint32(b[20:], uint32(value))
	return b
}

// trickle hands out its data a few bytes at a time, as a slow device might.
type trickle struct{ data []byte }

func (r *trickle) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), 7)], r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestReadKeyEventsAcrossReads(t *testing.T) {
	var data []byte
	data = append(data, event(30, 1)...)
	sync := event(0, 0)
	binary.LittleEndian.PutUint16(sync[16:], 0) // EV_SYN, not a key
	data = append(data, sync...)
	data = append(data, event(30, 0)...)
	var got []KeyEvent
	if err := ReadKeyEvents(&trickle{data: data}, func(e KeyEvent) { got = append(got, e) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (KeyEvent{30, 1}) || got[1] != (KeyEvent{30, 0}) {
		t.Errorf("events %+v", got)
	}
}

func TestEscTapAndHold(t *testing.T) {
	run := func(events []byte, wait time.Duration) (sent []string, quit bool) {
		var mu sync.Mutex
		pr, pw := io.Pipe()
		done := make(chan struct{})
		go func() {
			ReadKeys(pr, 50*time.Millisecond, func(m tea.Msg) {
				mu.Lock()
				sent = append(sent, m.(tea.KeyMsg).String())
				mu.Unlock()
			}, func() { mu.Lock(); quit = true; mu.Unlock() })
			close(done)
		}()
		pw.Write(events)
		time.Sleep(wait)
		pw.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		return sent, quit
	}
	tap := append(event(keyEsc, 1), event(keyEsc, 0)...)
	if sent, quit := run(tap, 120*time.Millisecond); quit || strings.Join(sent, ",") != "esc" {
		t.Errorf("a tap: sent %v, quit %v", sent, quit)
	}
	hold := append(append(event(keyEsc, 1), event(keyEsc, 2)...), event(keyEsc, 2)...)
	if sent, quit := run(hold, 120*time.Millisecond); !quit || strings.Join(sent, ",") != "esc" {
		t.Errorf("a hold: sent %v, quit %v (Esc should be sent once, then quit)", sent, quit)
	}
}

// echo is a model that shows the screen size it was given and what was typed.
type echo struct {
	w, h  int
	typed string
}

func (e echo) Init() tea.Cmd { return nil }

func (e echo) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		e.w, e.h = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.Type == tea.KeyRunes {
			e.typed += string(msg.Runes)
		}
	}
	return e, nil
}

func (e echo) View() string { return "typed: " + e.typed }

// fakeDevice is a screen size, a framebuffer file, a font and a FIFO for the keyboard, as Options.
func fakeDevice(t *testing.T) Options {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("virtual_size", []byte("320,170"))
	write("bits_per_pixel", []byte("16"))
	o := Options{
		Framebuffer: write("fb0", make([]byte, 640*170)), Sys: dir, Font: write("font.psf", psf2(t)),
		Keyboard: filepath.Join(dir, "kbd"), HoldToQuit: 200 * time.Millisecond,
	}
	if err := syscall.Mkfifo(o.Keyboard, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	return o
}

func TestRunOnAFakeDevice(t *testing.T) {
	o := fakeDevice(t)
	kbdPath, fbPath := o.Keyboard, o.Framebuffer
	go func() {
		w, err := os.OpenFile(kbdPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer w.Close()
		time.Sleep(100 * time.Millisecond)
		for _, code := range []uint16{35, 23} { // h, i
			w.Write(append(event(code, 1), event(code, 0)...))
		}
		time.Sleep(100 * time.Millisecond)
		w.Write(event(keyEsc, 1)) // and hold it
		time.Sleep(time.Second)
	}()
	final, err := Run(echo{}, o)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := final.(echo)
	if !ok {
		t.Fatalf("the final model is a %T, not the one passed in", final)
	}
	if e.w != 53 || e.h != 14 || e.typed != "hi" {
		t.Errorf("the model saw a %d×%d screen and %q typed", e.w, e.h, e.typed)
	}
	if fb, _ := os.ReadFile(fbPath); len(fb) != 640*170 || bytes.Count(fb, []byte{0}) != len(fb) {
		t.Error("the screen wasn't left clear (or the file grew)")
	}
}

// quitter quits on a key, and once it has, sends the process SIGTERM while Bubble Tea draws its last frame: what
// the launcher does when Esc is held (it signals just after the client's own Esc hold quits).
type quitter struct{ quitting bool }

func (q quitter) Init() tea.Cmd { return nil }

func (q quitter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		q.quitting = true
		return q, tea.Quit
	}
	return q, nil
}

func (q quitter) View() string {
	if q.quitting {
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(20 * time.Millisecond) // time for the signal to be taken while the program shuts down
	}
	return "bye"
}

func TestASIGTERMWhileQuittingDoesntHang(t *testing.T) {
	// The test process mustn't die of a SIGTERM that arrives after Run stops listening.
	keep := make(chan os.Signal, 1)
	signal.Notify(keep, syscall.SIGTERM)
	defer signal.Stop(keep)

	o := fakeDevice(t)
	go func() {
		w, err := os.OpenFile(o.Keyboard, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer w.Close()
		time.Sleep(100 * time.Millisecond)
		w.Write(append(event(30, 1), event(30, 0)...))
		time.Sleep(time.Second)
	}()
	returned := make(chan error, 1)
	go func() {
		_, err := Run(quitter{}, o)
		returned <- err
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run hung after a SIGTERM arrived while it was quitting")
	}
	if fb, _ := os.ReadFile(o.Framebuffer); bytes.Count(fb, []byte{0}) != len(fb) {
		t.Error("the screen wasn't left clear")
	}
}

// The test page ends on an Esc hold read from the same events it logs: it used to open the keyboard a second time
// for the hold, which a real input device allows (each reader gets every event) but a FIFO doesn't.
func TestRunTestEndsOnAnEscHold(t *testing.T) {
	o := fakeDevice(t)
	var log bytes.Buffer
	o.Log = &log
	go func() {
		w, err := os.OpenFile(o.Keyboard, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer w.Close()
		w.Write(append(event(30, 1), event(30, 0)...)) // a
		w.Write(event(keyEsc, 1))                      // and hold Esc
		time.Sleep(2 * time.Second)
	}()
	start := time.Now()
	if err := RunTest(o, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the test page took %v to end on a 200 ms Esc hold", took)
	}
	if !strings.Contains(log.String(), `"a" (code 30) down`) {
		t.Errorf("the key wasn't logged:\n%s", log.String())
	}
	if fb, _ := os.ReadFile(o.Framebuffer); bytes.Count(fb, []byte{0}) != len(fb) {
		t.Error("the screen wasn't left clear")
	}
}

// meshflash's page (the kit's asks for "Loom screen test" and "“Quotes”"): its name and every glyph its screens use.
func TestTestPageFits(t *testing.T) {
	for _, cols := range []int{53, 45} {
		g := Parse(TestPage(cols, `"a" (code 30)`, 3), cols, 12)
		text := g.Text()
		for _, want := range []string{"meshflash screen test", "~", Glyphs, "Key 3", "Hold Esc"} {
			if !strings.Contains(text, want) {
				t.Errorf("%d columns: %q missing from\n%s", cols, want, text)
			}
		}
	}
}

// FuzzParse checks that no frame, however broken, makes Parse panic, stall or draw outside its grid.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"a👍🏽b", "é\x1b[7m漢\x1b[0m", "\x1b]8;;x\x07🇫🇷\xff\xc3", "\x1b[38;5", "1️⃣\r\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		g := Parse(s, 7, 3)
		if len(g) != 3 {
			t.Fatalf("%d rows", len(g))
		}
		for _, row := range g {
			if len(row) != 7 {
				t.Fatalf("a row of %d cells", len(row))
			}
		}
	})
}
