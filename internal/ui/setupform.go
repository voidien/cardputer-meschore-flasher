package ui

// The Setup form (SPEC.md 4 "Setup"; docs/phase2-design.md 10.3, 10.4): one row per setting, each with its value
// and, under it, its problem; text boxes for the typed rows and pick lists for the rows with a few allowed values.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/nodecli"
)

// setupRow is one row of the form, in SPEC.md 4's order.
type setupRow int

const (
	setupName setupRow = iota
	setupFreq
	setupBW
	setupSF
	setupCR
	setupTx
	setupPathHash
	setupLat
	setupLon
	setupAdvert
	setupAdmin
	setupGuest // a room server's only
	setupRowCount
)

// setupKind is how a row is edited.
type setupKind int

const (
	setupTyped  setupKind = iota // a text box
	setupPicked                  // a pick list of the allowed values
	setupSecret                  // a password's text box, masked
)

// setupLabelW is the width of the label column: "Spreading factor" and a space.
const setupLabelW = 17

// setupRows are the rows' labels, how each is edited, and a text box's limit in UTF-8 bytes (a name 1–23 bytes and
// a password 1–15, as the node keeps them: SPEC.md 4; the numbers no longer than any value they take).
var setupRows = [setupRowCount]struct {
	label string
	kind  setupKind
	limit int
}{
	setupName:     {"Name", setupTyped, nodecli.MaxName},
	setupFreq:     {"Frequency", setupTyped, 12},
	setupBW:       {"Bandwidth", setupPicked, 0},
	setupSF:       {"Spreading factor", setupPicked, 0},
	setupCR:       {"Coding rate", setupPicked, 0},
	setupTx:       {"TX power", setupTyped, 4},
	setupPathHash: {"Path hash", setupPicked, 0},
	setupLat:      {"Latitude", setupTyped, 12},
	setupLon:      {"Longitude", setupTyped, 12},
	setupAdvert:   {"Local advert", setupTyped, 4},
	setupAdmin:    {"Admin password", setupSecret, nodecli.MaxPassword},
	setupGuest:    {"Guest password", setupSecret, nodecli.MaxPassword},
}

// setupFieldRows maps nodecli.FieldError.Field to the row it is about. "Radio" names all four radio rows (brief
// G8), so their problems come from CheckRadio on a radio varying only that row instead (setupSettings).
var setupFieldRows = map[string]setupRow{
	"Name": setupName, "TX power": setupTx, "Path hash": setupPathHash, "Latitude": setupLat,
	"Longitude": setupLon, "Local advert": setupAdvert, "Admin password": setupAdmin, "Guest password": setupGuest,
}

// setupRoom says the node is a room server, whose form has the Guest password row.
func (m *Model) setupRoom() bool { return m.setup.node.Info.Role == nodecli.RoleRoomServer }

// setupFormRows are the rows the form shows, in order.
func (m *Model) setupFormRows() []setupRow {
	rows := make([]setupRow, 0, setupRowCount)
	for r := setupName; r < setupRowCount; r++ {
		if r != setupGuest || m.setupRoom() {
			rows = append(rows, r)
		}
	}
	return rows
}

// setupHasPathHash says the node's firmware has the path hash setting (v1.14.0 and newer: 7.5).
func (m *Model) setupHasPathHash() bool { return m.setup.node.Info.Version.AtLeast(1, 14, 0) }

// setupFillForm builds the form from the node's settings, the messenger's radio, the last radio sent and the draft
// (design 10.3), in that order of preference.
func (m *Model) setupFillForm(mr nodecli.MessengerRadio, note string) {
	s := &m.setup
	cur, info := s.node.Current, s.node.Info
	var d [setupRowCount]string
	d[setupName] = cur.Name

	// The radio: the messenger's when its file was used, else the last one sent when this firmware takes it, else
	// the node's own. (The backend gives no note and a zero radio when there is no messenger file to read at all.)
	radio := cur.Radio
	var last *nodecli.Radio
	if c := m.cfg.Radio; c != nil {
		r := nodecli.Radio{FreqMHz: c.FreqMHz, BWkHz: c.BWkHz, SF: c.SF, CR: c.CR}
		last = &r
	}
	switch {
	case note == "" && mr.Radio.FreqMHz > 0:
		radio = mr.Radio
	case last != nil && nodecli.CheckRadio(*last, info.Version) == nil:
		radio = *last
	}
	d[setupFreq] = setupFreqText(radio.FreqMHz)
	d[setupBW] = setupBWText(radio.BWkHz)
	d[setupSF] = strconv.Itoa(radio.SF)
	d[setupCR] = strconv.Itoa(radio.CR)

	// TX power (owner question 6): the messenger's, else the last sent, else the node's, capped at the board's
	// limit with a note saying so.
	tx := cur.TxDBm
	switch {
	case mr.HasTx:
		tx = mr.TxDBm
	case m.cfg.Radio != nil:
		tx = m.cfg.Radio.TxDBm
	}
	if limit, _ := nodecli.TxLimit(s.device, info.Board); tx > limit {
		s.capNote = fmt.Sprintf("%d dBm is above this board's limit: %d dBm.", tx, limit)
		tx = limit
	}
	d[setupTx] = strconv.Itoa(tx)

	// The path hash (owner question 6): the preset's whose radio the form starts with, else the last sent, else the
	// node's.
	ph := cur.PathHashBytes
	switch {
	case presetPathHash(radio) != 0:
		ph = presetPathHash(radio)
	case m.cfg.Radio != nil && m.cfg.Radio.PathHashBytes != 0:
		ph = m.cfg.Radio.PathHashBytes
	}
	if ph != 0 {
		d[setupPathHash] = strconv.Itoa(ph)
	}

	if cur.Lat != nil {
		d[setupLat] = setupDegText(*cur.Lat)
	}
	if cur.Lon != nil {
		d[setupLon] = setupDegText(*cur.Lon)
	}
	if a := cur.AdvertInterval; a != nil {
		d[setupAdvert] = setupAdvertText(*a)
	}

	s.defs, s.vals = d, d
	s.admin = setupPasswordBox(nil)
	// The guest password's default is the node's own, masked from the start; the copy in node is dropped.
	s.guest = setupPasswordBox([]rune(string(cur.GuestPassword)))
	s.node.Current.GuestPassword = ""
	if note := m.setupRestoreDraft(); note != "" {
		m.note = note
	}
	s.mode, s.focus, s.top = setupForm, setupName, 0
	m.setupCheck()
}

// setupPasswordBox is a password's editor holding text (its runes, not a copy), masked.
func setupPasswordBox(text []rune) editor {
	e := editor{limit: nodecli.MaxPassword, bytes: true, mask: true}
	e.text = e.fit(nil, text)
	e.pos = len(e.text)
	return e
}

// setupFreqText writes a frequency as the node keeps it, a float32 ("869.618", never "869.6179809").
func setupFreqText(f float64) string { return strconv.FormatFloat(f, 'f', -1, 32) }

// setupBWText writes a bandwidth as the pick list has it ("62.5", "31.25"), or as a float32 when it isn't one.
func setupBWText(bw float64) string {
	for _, b := range nodecli.Bandwidths {
		if math.Abs(bw-b.KHz) < 0.001 {
			return b.Text
		}
	}
	return strconv.FormatFloat(bw, 'f', -1, 32)
}

// setupDegText writes a latitude or longitude as read ("51.5074").
func setupDegText(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

// setupAdvertText writes a local advert interval: "off", or the minutes.
func setupAdvertText(mins int) string {
	if mins == 0 {
		return "off"
	}
	return strconv.Itoa(mins)
}

// setupText is a row's text as it stands: the open text box's while it is being edited. Never a password's.
func (s *setupScreen) setupText(r setupRow) string {
	if s.mode == setupEditing && s.focus == r && setupRows[r].kind != setupSecret {
		return s.box.Value()
	}
	return s.vals[r]
}

// setupSecretRunes is a password row's runes as they stand: the open box's while it is being edited.
func (s *setupScreen) setupSecretRunes(r setupRow) []rune {
	if s.mode == setupEditing && s.focus == r {
		return s.box.text
	}
	if r == setupGuest {
		return s.guest.text
	}
	return s.admin.text
}

// setupSettings is the form as nodecli.Settings, and each row's problem in words ("" for none): what nodecli.Check
// says, each radio row's own CheckRadio, and a row whose text isn't a number. The Settings hold the passwords as
// Secrets for the request and for Check; Go strings can't be wiped, so nothing keeps them past the call.
func (m *Model) setupSettings() (nodecli.Settings, [setupRowCount]string) {
	s := &m.setup
	info := s.node.Info
	var set nodecli.Settings
	var probs [setupRowCount]string
	set.Name = s.setupText(setupName)
	set.Device, set.ClockSynced = s.device, s.synced

	// Each radio row is checked alone, on a radio good in every other part, so its problem lands under it.
	radioRow := func(r setupRow, parsed bool, missing string, vary func(*nodecli.Radio)) {
		if !parsed {
			probs[r] = missing
			return
		}
		vary(&set.Radio)
		one := nodecli.DefaultRadio
		vary(&one)
		if err := nodecli.CheckRadio(one, info.Version); err != nil {
			probs[r] = setupProblem(err)
		}
	}
	f, ok := setupNumber(s.setupText(setupFreq))
	radioRow(setupFreq, ok, "Type the frequency in MHz, such as 869.618.", func(r *nodecli.Radio) { r.FreqMHz = f })
	bw, ok := setupNumber(s.setupText(setupBW))
	radioRow(setupBW, ok, "Pick a bandwidth from the list.", func(r *nodecli.Radio) { r.BWkHz = bw })
	sf, err := strconv.Atoi(strings.TrimSpace(s.setupText(setupSF)))
	radioRow(setupSF, err == nil, "Pick a spreading factor from the list.", func(r *nodecli.Radio) { r.SF = sf })
	cr, err := strconv.Atoi(strings.TrimSpace(s.setupText(setupCR)))
	radioRow(setupCR, err == nil, "Pick a coding rate from the list.", func(r *nodecli.Radio) { r.CR = cr })

	limit, _ := nodecli.TxLimit(s.device, info.Board)
	if tx, err := strconv.Atoi(strings.TrimSpace(s.setupText(setupTx))); err == nil {
		set.TxDBm = tx
	} else {
		probs[setupTx] = fmt.Sprintf("Type a whole number of dBm, %d to %d.", nodecli.MinTxDBm, limit)
	}

	if t := strings.TrimSpace(s.setupText(setupPathHash)); m.setupHasPathHash() && t != "" {
		if n, err := strconv.Atoi(t); err == nil {
			set.PathHashBytes = n
		} else {
			probs[setupPathHash] = "Pick 1, 2 or 3 bytes from the list."
		}
	}

	deg := func(r setupRow, lim float64, to **float64) {
		t := strings.TrimSpace(s.setupText(r))
		if t == "" {
			return // blank sends nothing
		}
		if x, ok := setupNumber(t); ok {
			*to = &x
			return
		}
		probs[r] = fmt.Sprintf("Type a number of degrees, -%g to %g, or nothing to leave it.", lim, lim)
	}
	deg(setupLat, 90, &set.Lat)
	deg(setupLon, 180, &set.Lon)

	if t := strings.TrimSpace(s.setupText(setupAdvert)); t != "" {
		mins, err := strconv.Atoi(t)
		if strings.EqualFold(t, "off") {
			mins, err = 0, nil
		}
		if err == nil {
			set.AdvertInterval = &mins
		} else {
			probs[setupAdvert] = "Type off, or a number of minutes from 60 to 240."
		}
	}

	set.AdminPassword = nodecli.Secret(string(s.setupSecretRunes(setupAdmin)))
	if m.setupRoom() {
		set.GuestPassword = nodecli.Secret(string(s.setupSecretRunes(setupGuest)))
	}

	for _, fe := range nodecli.Check(set, info) {
		if r, ok := setupFieldRows[fe.Field]; ok && probs[r] == "" {
			probs[r] = setupProblem(fe)
		}
	}
	return set, probs
}

// setupCheck finds each row's problem again, after anything on the form changed.
func (m *Model) setupCheck() { _, m.setup.problems = m.setupSettings() }

// setupNumber reads a decimal number as typed ("869.618", "-0.1278"); NaN and infinities aren't numbers here.
func setupNumber(t string) (float64, bool) {
	x, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	return x, err == nil && !math.IsNaN(x) && !math.IsInf(x, 0)
}

// setupProblem is a FieldError's problem as a sentence under its row: capitalised (the row's label already names
// the field), with nodecli's minus sign (−, U+2212) written as the ASCII hyphen the device's font has (S15).
func setupProblem(err error) string {
	p := err.Error()
	if fe, ok := err.(*nodecli.FieldError); ok {
		p = fe.Problem
	}
	p = strings.ReplaceAll(p, "−", "-")
	r, n := utf8.DecodeRuneInString(p)
	return string(unicode.ToUpper(r)) + p[n:]
}

// setupProblemRows are the rows with a problem, in form order.
func (m *Model) setupProblemRows() []setupRow {
	var out []setupRow
	for _, r := range m.setupFormRows() {
		if m.setup.problems[r] != "" {
			out = append(out, r)
		}
	}
	return out
}

// setupShown is a row's value as the form shows it, and whether it is grey (a placeholder, not a value).
func (m *Model) setupShown(r setupRow) (string, bool) {
	s := &m.setup
	t := s.vals[r]
	switch r {
	case setupAdmin, setupGuest:
		if len(s.setupSecretRunes(r)) > 0 {
			return nodecli.Mask, false // whatever its length
		}
		return "", false
	case setupFreq:
		if t != "" {
			return t + " MHz", false
		}
	case setupBW:
		if t != "" {
			return t + " kHz", false
		}
	case setupCR:
		if _, err := strconv.Atoi(t); err == nil {
			return "4/" + t, false
		}
	case setupTx:
		if t != "" {
			return t + " dBm", false
		}
	case setupPathHash:
		if !m.setupHasPathHash() {
			return "needs v1.14+", true
		}
		if n, err := strconv.Atoi(t); err == nil {
			return plural(n, "byte", "bytes"), false
		}
	case setupLat, setupLon:
		if strings.TrimSpace(t) == "" {
			return "not sent", true
		}
	case setupAdvert:
		switch n, err := strconv.Atoi(t); {
		case strings.TrimSpace(t) == "":
			return "not sent", true
		case strings.EqualFold(t, "off") || err == nil && n == 0:
			return "off", false
		case err == nil:
			return fmt.Sprintf("every %d minutes", n), false
		}
	}
	return t, false
}

// setupBoxHint is a grey line under an open text box saying what it takes, shown while the row has no problem.
func (m *Model) setupBoxHint(r setupRow) string {
	switch r {
	case setupName:
		return fmt.Sprintf("Up to %d bytes.", nodecli.MaxName)
	case setupFreq:
		return "In MHz, such as 869.618."
	case setupTx:
		limit, _ := nodecli.TxLimit(m.setup.device, m.setup.node.Info.Board)
		return fmt.Sprintf("In dBm, %d to %d on this board.", nodecli.MinTxDBm, limit)
	case setupLat:
		return "In degrees, -90 to 90; blank sends nothing."
	case setupLon:
		return "In degrees, -180 to 180; blank sends nothing."
	case setupAdvert:
		return "off, or 60 to 240 minutes; blank sends nothing."
	case setupAdmin, setupGuest:
		return fmt.Sprintf("Up to %d bytes. Sent to the node, never saved.", nodecli.MaxPassword)
	}
	return ""
}

// setupFormView is the form: the notes about what it was made from, then the rows, scrolled to keep the focused
// row and what is under it in view (design 8.9).
func (m *Model) setupFormView(w, h int) ([]string, string) {
	s := &m.setup
	var lines []string
	note := func(text string) {
		for _, l := range wrap(text, w) {
			lines = append(lines, grey.Render(l))
		}
	}
	if s.messengerNote != "" {
		note(s.messengerNote)
	}
	if !s.synced {
		note("The Zero's clock isn't synchronised: the node's clock won't be set.")
	}
	from, to := 0, 0
	rows := m.setupFormRows()
	for i, r := range rows {
		start := len(lines)
		lines = append(lines, m.setupRowLines(r, w)...)
		if r == s.focus {
			from, to = start, len(lines)
			if i == 0 {
				from = 0 // the first row shows the notes above it too
			}
		}
	}
	s.top = setupScroll(s.top, from, to, h, len(lines))
	lines = lines[s.top:min(len(lines), s.top+h)]
	if s.mode == setupEditing {
		return lines, "enter keep  esc cancel  tab next"
	}
	return lines, "enter edit  p presets  y send  ? help"
}

// setupRowLines is one row: its mark (▸ focused, ✎ being edited), its label, its value wrapped in the value column
// (or the open text box), then under it in grey its problem, the TX cap's note, or what the open box takes.
func (m *Model) setupRowLines(r setupRow, w int) []string {
	s := &m.setup
	lw := min(setupLabelW, max(2, w-8))
	vw := max(1, w-2-lw)
	mark := "  "
	editing := s.mode == setupEditing && s.focus == r
	switch {
	case editing:
		mark = "✎ "
	case s.focus == r:
		mark = "▸ "
	}
	label := cut(setupRows[r].label, lw-1)
	head := mark + label + strings.Repeat(" ", lw-width(label))
	var vals []string
	if editing {
		vals, _ = s.box.lines(vw, true)
	} else {
		text, dim := m.setupShown(r)
		for _, l := range wrap(text, vw) {
			if dim {
				l = grey.Render(l)
			}
			vals = append(vals, l)
		}
	}
	out := []string{strings.TrimRight(head+vals[0], " ")}
	if editing {
		out[0] = head + vals[0] // the cursor may be a space at the end
	}
	for _, l := range vals[1:] {
		out = append(out, strings.Repeat(" ", 2+lw)+l)
	}
	under := s.problems[r]
	switch {
	case under != "":
	case r == setupTx && s.capNote != "" && s.setupText(setupTx) == s.defs[setupTx]:
		under = s.capNote
	case editing:
		under = m.setupBoxHint(r)
	}
	if under != "" {
		for _, l := range wrap(under, max(1, w-4)) {
			out = append(out, grey.Render("    "+l))
		}
	}
	return out
}

// setupScroll is the scroll offset of an n-line view h lines high that keeps lines [from, to) in view, moving as
// little as it can from top (or showing from's line first when they don't fit).
func setupScroll(top, from, to, h, n int) int {
	switch {
	case to-from > h:
		top = from
	case from < top:
		top = from
	case to > top+h:
		top = to - h
	}
	return max(0, min(top, n-h))
}

// setupMove moves the focus by d rows; Tab (wrap) goes round from the last row to the first, j and k stop at the
// ends.
func (m *Model) setupMove(d int, wrapAround bool) {
	rows := m.setupFormRows()
	at := 0
	for i, r := range rows {
		if r == m.setup.focus {
			at = i
		}
	}
	at += d
	switch {
	case wrapAround:
		at = (at + len(rows)) % len(rows)
	case at < 0:
		at = 0
	case at >= len(rows):
		at = len(rows) - 1
	}
	m.setup.focus = rows[at]
}

// setupFormKey handles a key on the form.
func (m *Model) setupFormKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "tab":
		m.setupMove(1, true)
	case "shift+tab":
		m.setupMove(-1, true)
	case "j", "down":
		m.setupMove(1, false)
	case "k", "up":
		m.setupMove(-1, false)
	case "enter":
		m.setupOpenRow()
	case "p":
		m.presetOpen()
	case "y":
		return m.setupSend()
	case "esc":
		note := ""
		if err := m.setupSaveDraft(); err != nil {
			note = "Couldn't keep what was typed (screen.log says why)."
		}
		return m.toBoard(note)
	}
	return nil
}

// setupOpenRow opens the focused row: a text box holding its text, or a pick list on its value.
func (m *Model) setupOpenRow() {
	s := &m.setup
	r := s.focus
	switch setupRows[r].kind {
	case setupPicked:
		if r == setupPathHash && !m.setupHasPathHash() {
			m.note = "The path hash needs firmware v1.14.0 or newer; this node runs " +
				s.node.Info.Version.String() + "."
			return
		}
		m.pickOpen(r)
	case setupSecret:
		// A copy of the runes, so that typing never changes the kept password before Enter, and Esc can wipe it.
		s.box = setupPasswordBox(append([]rune(nil), s.setupSecretRunes(r)...))
		s.mode = setupEditing
	default:
		s.box = newBytesEditor(s.vals[r], setupRows[r].limit)
		s.mode = setupEditing
	}
}

// setupBoxKey handles a key in a text box (S21): Enter keeps, Esc cancels, Tab keeps and moves to the next row (a
// press: the frame drops a held Tab's repeats), and the rest edit the text.
func (m *Model) setupBoxKey(msg tea.KeyMsg) {
	s := &m.setup
	switch msg.Type {
	case tea.KeyEnter:
		m.setupKeepBox()
	case tea.KeyTab:
		m.setupKeepBox()
		m.setupMove(1, true)
	case tea.KeyShiftTab:
		m.setupKeepBox()
		m.setupMove(-1, true)
	case tea.KeyEsc:
		r := s.focus
		if setupRows[r].kind != setupSecret && s.box.Value() != s.vals[r] {
			s.cancelled = setupCancelled{ok: true, row: r, text: s.box.Value(), at: m.now()}
		}
		s.box.Clear()
		s.mode = setupForm
	default:
		if !s.box.key(msg) {
			return
		}
	}
	m.setupCheck()
}

// setupKeepBox keeps what the open text box holds as its row's value and closes it.
func (m *Model) setupKeepBox() {
	s := &m.setup
	r := s.focus
	switch {
	case r == setupAdmin:
		s.admin.Clear()
		s.admin, s.box = s.box, editor{} // the box's runes are the password now: moved, not copied
	case r == setupGuest:
		s.guest.Clear()
		s.guest, s.box = s.box, editor{}
	default:
		s.vals[r] = s.box.Value()
		s.box.Clear()
	}
	s.mode = setupForm
}

// pickOption is one value of a pick list: its text as the form keeps it, and as the list shows it.
type pickOption struct{ value, shown string }

// pickOptions are the values a pick-list row takes: the bandwidths the radios accept (nodecli.Bandwidths), the
// spreading factors 5–12, the coding rates 4/5–4/8 and the path hash sizes 1–3 bytes.
func pickOptions(r setupRow) []pickOption {
	var out []pickOption
	switch r {
	case setupBW:
		for _, b := range nodecli.Bandwidths {
			out = append(out, pickOption{b.Text, b.Text + " kHz"})
		}
	case setupSF:
		for sf := 5; sf <= 12; sf++ {
			out = append(out, pickOption{strconv.Itoa(sf), strconv.Itoa(sf)})
		}
	case setupCR:
		for cr := 5; cr <= 8; cr++ {
			out = append(out, pickOption{strconv.Itoa(cr), "4/" + strconv.Itoa(cr)})
		}
	case setupPathHash:
		for n := 1; n <= 3; n++ {
			out = append(out, pickOption{strconv.Itoa(n), plural(n, "byte", "bytes")})
		}
	}
	return out
}

// pickOpen opens the pick list of row r on its value, or the nearest one when its value isn't in the list.
func (m *Model) pickOpen(r setupRow) {
	s := &m.setup
	opts := pickOptions(r)
	s.pick, s.pickTop = 0, 0
	if x, ok := setupNumber(s.vals[r]); ok {
		best := math.Inf(1)
		for i, o := range opts {
			if v, _ := setupNumber(o.value); math.Abs(v-x) < best {
				s.pick, best = i, math.Abs(v-x)
			}
		}
	}
	s.mode = setupPicking
}

// pickKey handles a key in a pick list: j/k pick, Enter keeps, Esc cancels, Tab keeps and moves to the next row.
func (m *Model) pickKey(msg tea.KeyMsg) {
	s := &m.setup
	opts := pickOptions(s.focus)
	keep := func() { s.vals[s.focus], s.mode = opts[s.pick].value, setupForm }
	switch msg.String() {
	case "j", "down":
		s.pick = min(s.pick+1, len(opts)-1)
	case "k", "up":
		s.pick = max(s.pick-1, 0)
	case "enter":
		keep()
	case "tab":
		keep()
		m.setupMove(1, true)
	case "shift+tab":
		keep()
		m.setupMove(-1, true)
	case "esc":
		s.mode = setupForm
	}
	m.setupCheck()
}

// pickView is the open pick list under its row's label, scrolled to keep the selection in view.
func (m *Model) pickView(w, h int) ([]string, string) {
	s := &m.setup
	opts := pickOptions(s.focus)
	var list []string
	for i, o := range opts {
		mark := "  "
		if i == s.pick {
			mark = "▸ "
		}
		list = append(list, mark+cut(o.shown, w-2))
	}
	room := max(1, h-1)
	s.pickTop = setupScroll(s.pickTop, s.pick, s.pick+1, room, len(list))
	lines := []string{heading(cut(setupRows[s.focus].label, w))}
	lines = append(lines, list[s.pickTop:min(len(list), s.pickTop+room)]...)
	return lines, "j/k pick  enter keep  esc cancel  ? help"
}
