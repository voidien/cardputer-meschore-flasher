package ui

// Phase 2 contract (docs/phase2-design.md): builder C owns this file. The methods hooks.go names keep their
// signatures; their bodies here are placeholders to replace (they may move into the screen files). The screen state
// types below are builder C's to fill. The frame that calls them is frame.go (fixed).

import (
	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
)

// flow is what has been picked so far on the way to a flash: the port on Board, the board on Choose board, the role,
// the version, and on Confirm the kind (zero until u or w on an ESP32: there is no default).
type flow struct {
	port flash.Port
	node *flash.Node // the port's probe answer, if any (dropped after a failed flash: design 9.9)
	dev  *catalog.Device
	fw   *catalog.Firmware
	ver  *catalog.Version
	kind catalog.Kind
}

// Each screen's own state (builder C's to fill).
type (
	boardScreen    struct{}
	chooseScreen   struct{}
	roleScreen     struct{}
	versionScreen  struct{}
	notesScreen    struct{}
	confirmScreen  struct{}
	downloadScreen struct{}
	flashingScreen struct{}
	resultScreen   struct{}
	updateScreen   struct{}
	filesScreen    struct{}
	modelMore      struct{}
)

// start loads the catalog (hooks.go).
func (m *Model) start() tea.Cmd { return nil }

// screenKey handles a key on every screen but Setup (hooks.go).
func (m *Model) screenKey(msg tea.KeyMsg) tea.Cmd { return nil }

// screenView draws every screen but Setup (hooks.go).
func (m *Model) screenView(w, h int) ([]string, string) { return nil, "? help" }

// screenTitle is the status line's title (hooks.go).
func (m *Model) screenTitle() string { return m.name }

// screenSubject is the board the screen is about (hooks.go).
func (m *Model) screenSubject() (*flash.Port, bool) { return nil, false }

// screenHelp is Help's list of the screen's keys (hooks.go).
func (m *Model) screenHelp() []string { return nil }

// screenTextBox says a text box is open (hooks.go).
func (m *Model) screenTextBox() bool { return false }

// screenConfirmText words a question (hooks.go).
func (m *Model) screenConfirmText(k confirmKind) string { return "" }

// screenConfirm does a question after y (hooks.go).
func (m *Model) screenConfirm(k confirmKind) tea.Cmd { return nil }

// screenMsg takes builder C's own messages (hooks.go).
func (m *Model) screenMsg(msg tea.Msg) (tea.Cmd, bool) { return nil, false }

// jobProgress takes a picture of a download or flash Job (hooks.go).
func (m *Model) jobProgress(p flash.Progress) tea.Cmd { return nil }

// afterScan is told of every scan (hooks.go).
func (m *Model) afterScan(prev []flash.Port) tea.Cmd { return nil }

// boardOpened is called when the Board shows again (hooks.go).
func (m *Model) boardOpened() tea.Cmd { return nil }

// cancelProbes stops every probe under way (hooks.go).
func (m *Model) cancelProbes() {}
