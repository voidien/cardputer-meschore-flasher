package screen

import (
	"encoding/binary"
	"errors"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// DefaultKeyboard is the CardputerZero's keyboard (a TCA8418 keypad controller), as Linux names it.
const DefaultKeyboard = "/dev/input/by-path/platform-3f804000.i2c-event"

// Linux key codes the client handles (linux/input-event-codes.h).
const (
	keyEsc       = 1
	keyBackspace = 14
	keyTab       = 15
	keyEnter     = 28
	keyLeftCtrl  = 29
	keyLeftShift = 42
	keyRightShft = 54
	keyLeftAlt   = 56
	keySpace     = 57
	keyRightCtrl = 97
	keyRightAlt  = 100
	keyHome      = 102
	keyUp        = 103
	keyPageUp    = 104
	keyLeft      = 105
	keyRight     = 106
	keyEnd       = 107
	keyDown      = 108
	keyPageDown  = 109
	keyInsert    = 110
	keyDelete    = 111
)

// letters are the base layer's letter keys (the kernel's QWERTY codes).
var letters = map[uint16]rune{
	16: 'q', 17: 'w', 18: 'e', 19: 'r', 20: 't', 21: 'y', 22: 'u', 23: 'i', 24: 'o', 25: 'p',
	30: 'a', 31: 's', 32: 'd', 33: 'f', 34: 'g', 35: 'h', 36: 'j', 37: 'k', 38: 'l',
	44: 'z', 45: 'x', 46: 'c', 47: 'v', 48: 'b', 49: 'n', 50: 'm',
}

// digits are the number row, and what Shift makes of them (a US layout, as the launcher's xkb keymap).
var digits = map[uint16][2]rune{
	2: {'1', '!'}, 3: {'2', '@'}, 4: {'3', '#'}, 5: {'4', '$'}, 6: {'5', '%'},
	7: {'6', '^'}, 8: {'7', '&'}, 9: {'8', '*'}, 10: {'9', '('}, 11: {'0', ')'},
}

// symbols are the Sym layer. The kernel sends these key codes for it, and the launcher turns them into characters
// with /usr/share/keymaps/tca8418_keypad_m5stack_keymap.map; this is that file's table.
var symbols = map[uint16]rune{
	26: '!', 27: '@', 39: '#', 40: '$', 41: '%', 43: '^', 51: '&', 52: '*', 53: '(', 94: ')',
	55: '~', 69: '`', 70: '_', 71: '-', 72: '+', 73: '=', 74: '[', 75: ']', 76: '{', 77: '}',
	79: ';', 80: ':', 81: '\'', 82: '"', 83: '<', 85: '>', 86: '\\', 89: '|',
	90: ',', 91: '.', 92: '/', 93: '?',
}

var plainKeys = map[uint16]tea.KeyType{
	keyEsc: tea.KeyEsc, keyBackspace: tea.KeyBackspace, keyEnter: tea.KeyEnter, keyDelete: tea.KeyDelete,
	keyHome: tea.KeyHome, keyEnd: tea.KeyEnd, keyUp: tea.KeyUp, keyDown: tea.KeyDown, keyLeft: tea.KeyLeft,
	keyRight: tea.KeyRight, keyPageUp: tea.KeyPgUp, keyPageDown: tea.KeyPgDown, keyInsert: tea.KeyInsert,
}

// Keyboard turns key events into the key messages a terminal would have given the program: Shift and Ctrl are tracked
// here, and the Fn and Sym layers are already applied by the kernel. Alt is swallowed and adds nothing, as in the CLI
// app (docs/device.md, the keys table): the screens have no Alt combinations, and an Alt-marked key matches no
// one-key action, so a held Alt would only make keys seem dead.
type Keyboard struct {
	shift, ctrl bool
}

// Key handles one key event (value 1 pressed, 2 repeating, 0 released) and returns the message to send, if any.
func (k *Keyboard) Key(code uint16, value int32) (tea.KeyMsg, bool) {
	down := value != 0
	switch code {
	case keyLeftShift, keyRightShft:
		k.shift = down
		return tea.KeyMsg{}, false
	case keyLeftCtrl, keyRightCtrl:
		k.ctrl = down
		return tea.KeyMsg{}, false
	case keyLeftAlt, keyRightAlt:
		return tea.KeyMsg{}, false
	}
	if !down {
		return tea.KeyMsg{}, false
	}
	return k.translate(code)
}

func (k *Keyboard) translate(code uint16) (tea.KeyMsg, bool) {
	if r, ok := letters[code]; ok {
		if k.ctrl {
			return tea.KeyMsg{Type: tea.KeyType(r - 'a' + 1)}, true
		}
		if k.shift {
			r -= 'a' - 'A'
		}
		return runes(r), true
	}
	if d, ok := digits[code]; ok {
		if k.shift {
			return runes(d[1]), true
		}
		return runes(d[0]), true
	}
	if r, ok := symbols[code]; ok {
		return runes(r), true
	}
	switch code {
	case keySpace:
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, true
	case keyTab:
		if k.shift {
			return tea.KeyMsg{Type: tea.KeyShiftTab}, true
		}
		return tea.KeyMsg{Type: tea.KeyTab}, true
	}
	if t, ok := plainKeys[code]; ok {
		return tea.KeyMsg{Type: t}, true
	}
	return tea.KeyMsg{}, false
}

func runes(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// KeyEvent is one key going down (1), repeating (2) or up (0).
type KeyEvent struct {
	Code  uint16
	Value int32
}

// eventSize is struct input_event on 64-bit Linux: a timeval (two 8-byte fields), type, code and value.
const eventSize = 24

// ReadKeyEvents reads input events from r until it fails, calling fn for each key event (type EV_KEY).
func ReadKeyEvents(r io.Reader, fn func(KeyEvent)) error {
	buf := make([]byte, eventSize*16)
	pending := 0
	for {
		n, err := r.Read(buf[pending:])
		pending += n
		whole := pending / eventSize * eventSize
		for i := 0; i < whole; i += eventSize {
			e := buf[i : i+eventSize]
			if binary.LittleEndian.Uint16(e[16:]) == 1 {
				fn(KeyEvent{Code: binary.LittleEndian.Uint16(e[18:]), Value: int32(binary.LittleEndian.Uint32(e[20:]))})
			}
		}
		pending = copy(buf, buf[whole:pending])
		if err != nil {
			// The end of the events, or the device closed because the program is quitting.
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return nil
			}
			return err
		}
	}
}
