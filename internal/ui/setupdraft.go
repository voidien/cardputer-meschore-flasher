package ui

// Setup's drafts (S7; docs/phase2-design.md 10.6, owner question 13): what was typed on the form and not sent is
// kept per board in drafts.json, under flash.Port.DraftKey (the board's USB serial number, or for a board without a
// useful one its USB socket and identity), and comes back when the form reopens. A draft never holds a password:
// store.Draft has no field for one.

import (
	"slices"
	"strings"

	"meshflash/internal/store"
)

// setupDraftRows are the rows a draft keeps (never a password), with their keys in drafts.json (store.Draft's JSON
// names, which Cleared uses).
var setupDraftRows = []struct {
	row setupRow
	key string
}{
	{setupName, "name"}, {setupFreq, "freq"}, {setupBW, "bw"}, {setupSF, "sf"}, {setupCR, "cr"},
	{setupTx, "tx"}, {setupPathHash, "path_hash"}, {setupLat, "lat"}, {setupLon, "lon"}, {setupAdvert, "advert"},
}

// setupDraftField is the field of d that keeps row r.
func setupDraftField(d *store.Draft, r setupRow) *string {
	switch r {
	case setupName:
		return &d.Name
	case setupFreq:
		return &d.Freq
	case setupBW:
		return &d.BW
	case setupSF:
		return &d.SF
	case setupCR:
		return &d.CR
	case setupTx:
		return &d.TX
	case setupPathHash:
		return &d.PathHash
	case setupLat:
		return &d.Lat
	case setupLon:
		return &d.Lon
	case setupAdvert:
		return &d.Advert
	}
	return nil
}

// saveSetupDraft keeps the open form as a draft for its board (hooks.go has the rules).
func (m *Model) saveSetupDraft() { _ = m.setupSaveDraft() }

// setupSaveDraft is saveSetupDraft, saying whether it failed (screen.log says why; Esc out of the form also tells
// the owner). It acts only while the form shows (the form, a text box, a pick list, the presets): never while
// reading, sending (y saved it first) or at the end, so after a setup that worked it can't bring back the draft just
// deleted. Each row is kept as typed when it differs from the form's default, "" when it doesn't (so a row left
// alone follows the node next time), and "" named in Cleared when the owner emptied it. A form back at its defaults
// deletes the board's draft. It may run twice in a row.
func (m *Model) setupSaveDraft() error {
	err := m.setupWriteDraft()
	if err != nil {
		m.logf("%s: couldn't keep what was typed on the setup form: %v", m.name, err)
	}
	return err
}

func (m *Model) setupWriteDraft() error {
	s := &m.setup
	switch s.mode {
	case setupForm, setupEditing, setupPicking, setupPresets:
	default:
		return nil
	}
	key := s.port.DraftKey()
	if key == "" || m.store == nil {
		return nil
	}
	vals := s.vals
	for r := range vals {
		vals[r] = s.setupText(setupRow(r)) // the open text box's half-typed line too
	}
	// The text of a text box Esc has just cancelled: an Esc hold's first tap, with its SIGTERM to come.
	if c := s.cancelled; c.ok && m.now().Sub(c.at) < setupCancelKeep {
		vals[c.row] = c.text
	}
	d := store.Draft{Role: s.node.Info.Role, Saved: m.now()}
	changed := false
	for _, dr := range setupDraftRows {
		v, def := vals[dr.row], s.defs[dr.row]
		switch {
		case v == def:
		case v == "":
			d.Cleared = append(d.Cleared, dr.key)
			changed = true
		default:
			*setupDraftField(&d, dr.row) = v
			changed = true
		}
	}
	if !changed {
		if _, ok := m.store.LoadDraft(key); ok {
			return m.store.DeleteDraft(key)
		}
		return nil
	}
	return m.store.SaveDraft(key, d)
}

// setupRestoreDraft puts the board's draft over the form's defaults: each row as typed, a row in Cleared blank, a
// row left at its default as the default is now. It says what it did, for m.note ("" when there was none).
func (m *Model) setupRestoreDraft() string {
	s := &m.setup
	key := s.port.DraftKey()
	if key == "" || m.store == nil {
		return ""
	}
	d, ok := m.store.LoadDraft(key)
	if !ok {
		return ""
	}
	for _, dr := range setupDraftRows {
		switch v := *setupDraftField(&d, dr.row); {
		case slices.Contains(d.Cleared, dr.key):
			s.vals[dr.row] = ""
		case v != "":
			s.vals[dr.row] = v
		}
	}
	// A repeater's form and a room server's share every row a draft keeps (only the guest password differs, and
	// no draft holds it), so a draft typed for the other role gives all its rows, and the note says which role.
	var b strings.Builder
	b.WriteString("Restored what was typed for ")
	if s.port.Key == "" {
		b.WriteString("the board on this USB socket")
	} else {
		b.WriteString("this board")
	}
	if d.Role != "" && d.Role != s.node.Info.Role {
		b.WriteString(" as a " + setupRoleWord(d.Role))
	}
	b.WriteString(".")
	return b.String()
}

// setupDropDraft deletes the board's draft once a setup has worked: what it held has been sent.
func (m *Model) setupDropDraft() {
	key := m.setup.port.DraftKey()
	if key == "" || m.store == nil {
		return
	}
	if _, ok := m.store.LoadDraft(key); !ok {
		return
	}
	if err := m.store.DeleteDraft(key); err != nil {
		m.logf("%s: couldn't delete the setup form's draft after the setup: %v", m.name, err)
	}
}

// setupRoleWord is a role as the screens say it: "repeater", "room server".
func setupRoleWord(role string) string { return strings.ReplaceAll(role, "_", " ") }
