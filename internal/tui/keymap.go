package tui

import tea "github.com/charmbracelet/bubbletea"

// keymap.go normalizes keystrokes that arrive under a CJK input source.
//
// With the OS input source set to Korean, pressing the `q` key emits `ㅂ`, so
// every single-letter shortcut silently stops working until the user switches
// back to English. normalizeCJKKey maps each jamo back to the Latin key at the
// same physical position on a US QWERTY keyboard — the same idea as vim's
// `langmap`.

// hangulToLatin maps jamo produced by the 2-set (두벌식) Korean layout to the
// Latin key at the same physical position on a US QWERTY keyboard.
//
// Shifted jamo (double consonants, ㅒ/ㅖ) map to the uppercase Latin letter,
// which is what the same physical chord would have produced in English.
var hangulToLatin = map[rune]rune{
	// unshifted row
	'ㅂ': 'q', 'ㅈ': 'w', 'ㄷ': 'e', 'ㄱ': 'r', 'ㅅ': 't',
	'ㅛ': 'y', 'ㅕ': 'u', 'ㅑ': 'i', 'ㅐ': 'o', 'ㅔ': 'p',
	'ㅁ': 'a', 'ㄴ': 's', 'ㅇ': 'd', 'ㄹ': 'f', 'ㅎ': 'g',
	'ㅗ': 'h', 'ㅓ': 'j', 'ㅏ': 'k', 'ㅣ': 'l',
	'ㅋ': 'z', 'ㅌ': 'x', 'ㅊ': 'c', 'ㅍ': 'v', 'ㅠ': 'b',
	'ㅜ': 'n', 'ㅡ': 'm',
	// shifted row
	'ㅃ': 'Q', 'ㅉ': 'W', 'ㄸ': 'E', 'ㄲ': 'R', 'ㅆ': 'T',
	'ㅒ': 'O', 'ㅖ': 'P',
}

// normalizeCJKKey rewrites a single-jamo key message to the Latin key at the
// same physical position, so shortcuts fire under a CJK input source.
//
// The message is returned unchanged when it is not a single rune, is a paste,
// or carries alt — those already arrive as Latin, and rewriting a chord would
// break bindings like alt+f. Ctrl chords never reach here as KeyRunes.
func normalizeCJKKey(msg tea.KeyMsg) tea.KeyMsg {
	if msg.Type != tea.KeyRunes || msg.Paste || msg.Alt || len(msg.Runes) != 1 {
		return msg
	}
	latin, ok := hangulToLatin[msg.Runes[0]]
	if !ok {
		return msg
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{latin}}
}
