package ui

// Phase 2 contract (docs/phase2-design.md): builder D owns this file (and presets.go and any setup*.go it adds).
// The methods hooks.go names keep their signatures; their bodies here are placeholders. setupScreen is builder D's
// to fill: builder C never reads or writes it. Every top-level name builder D adds starts with setup, preset or pick.

import (
	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
)

// setupScreen is the Setup screen's state: reading the node, the form (its rows, the focused one, a text box or
// pick list open, the presets list, a just-cancelled text box's text), the setup under way, and its end.
type setupScreen struct{}

// openSetup starts Setup for the board on p (hooks.go).
func (m *Model) openSetup(p flash.Port, device string, firstBoot bool) tea.Cmd {
	m.screen = scrSetup
	return nil
}

// setupKey handles a key on scrSetup.
func (m *Model) setupKey(msg tea.KeyMsg) tea.Cmd { return nil }

// setupView draws scrSetup's body and gives its key hint.
func (m *Model) setupView(w, h int) ([]string, string) { return nil, "esc back  ? help" }

// setupTitle is the status line's title on scrSetup.
func (m *Model) setupTitle() string { return "Set up" }

// setupSubject is the board scrSetup is about, for the status mark.
func (m *Model) setupSubject() (*flash.Port, bool) { return nil, true }

// setupEditing says a text box is open on scrSetup.
func (m *Model) setupEditing() bool { return false }

// setupResult takes a message nobody else knows; ok says it was Setup's.
func (m *Model) setupResult(msg tea.Msg) (tea.Cmd, bool) { return nil, false }

// setupProgress takes a picture of the running setup Job.
func (m *Model) setupProgress(p flash.Progress) tea.Cmd { return nil }

// setupHelp is Help's list of scrSetup's keys.
func (m *Model) setupHelp() []string { return nil }

// setupConfirmText words a question on scrSetup.
func (m *Model) setupConfirmText(k confirmKind) string { return "" }

// setupConfirm does one of Setup's questions after y.
func (m *Model) setupConfirm(k confirmKind) tea.Cmd { return nil }

// saveSetupDraft keeps the open form as a draft for its board.
func (m *Model) saveSetupDraft() {}

// closeSetup ends Setup: clears both passwords and cancels a ReadNode still running.
func (m *Model) closeSetup() {}
