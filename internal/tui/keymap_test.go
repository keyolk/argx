package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNormalizeCJKKeyMapsJamoByPhysicalPosition(t *testing.T) {
	for jamo, want := range map[string]string{
		"ㅂ": "q", "ㅁ": "a", "ㅋ": "z", "ㅓ": "j", "ㅏ": "k",
		"ㅃ": "Q", "ㄲ": "R",
	} {
		if got := normalizeCJKKey(key(jamo)).String(); got != want {
			t.Errorf("normalizeCJKKey(%q) = %q, want %q", jamo, got, want)
		}
	}
}

func TestNormalizeCJKKeyLeavesEverythingElseAlone(t *testing.T) {
	// Latin keys, digits, and composed syllables pass through: a composed
	// syllable only reaches the TUI as committed text, never as a shortcut.
	for _, k := range []string{"q", "R", "0", "가"} {
		if got := normalizeCJKKey(key(k)).String(); got != k {
			t.Errorf("normalizeCJKKey(%q) = %q, want it unchanged", k, got)
		}
	}
	for _, in := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyCtrlC}, {Type: tea.KeyEsc}} {
		if got := normalizeCJKKey(in); got.String() != in.String() {
			t.Errorf("normalizeCJKKey(%q) = %q, want it unchanged", in.String(), got.String())
		}
	}
}

func TestNormalizeCJKKeyLeavesPasteAndAltAlone(t *testing.T) {
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'ㅂ'}, Paste: true}
	if got := normalizeCJKKey(paste); got.String() != paste.String() {
		t.Errorf("pasted jamo was rewritten to %q", got.String())
	}
	// alt+<jamo> is a chord, not text: argx binds alt+b / alt+f in the filter,
	// and rewriting the rune would change which chord fires.
	alt := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'ㅂ'}, Alt: true}
	if got := normalizeCJKKey(alt); got.String() != alt.String() {
		t.Errorf("alt chord was rewritten to %q", got.String())
	}
}

// --- through the real dispatcher --------------------------------------------

// Under a Korean input source every shortcut arrives as a jamo. They have to
// map back, or the app list is unusable until the input source is switched.
func TestHangulNavigatesTheAppListLikeLatin(t *testing.T) {
	m := newTestModel(t, "alpha", "bravo")
	m.appCur = 0

	// `ㅓ` is the physical `j`.
	m.handleKey(key("ㅓ"))
	if m.appCur != 1 {
		t.Fatalf("appCur = %d after ㅓ; want 1", m.appCur)
	}
	// `ㅏ` is the physical `k`.
	m.handleKey(key("ㅏ"))
	if m.appCur != 0 {
		t.Fatalf("appCur = %d after ㅏ; want 0", m.appCur)
	}
}

func TestHangulQuitsLikeLatinQ(t *testing.T) {
	m := newTestModel(t, "alpha")
	// `ㅂ` sits on the physical `q` key.
	_, cmd := m.handleKey(key("ㅂ"))
	if cmd == nil {
		t.Fatal("ㅂ (physical q) produced no command; expected quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ㅂ (physical q) did not quit")
	}
}

// An unbound jamo must stay a no-op rather than being mistaken for a nearby
// binding. `S` toggles the AppSet list, and the 2-set layout has no shifted
// jamo on the `s` key -- so `S` is simply unreachable from a Korean input
// source, and `ㄴ` (physical lowercase `s`) must not stand in for it.
func TestUnboundHangulDoesNotToggleTheAppSetList(t *testing.T) {
	m := newTestModel(t, "alpha")
	if m.screen != screenApps {
		t.Fatalf("screen = %v, want the app list", m.screen)
	}
	m.handleKey(key("ㄴ"))
	if m.screen != screenApps {
		t.Fatal("ㄴ (physical lowercase s) must not toggle the AppSet list")
	}
}

// A jamo typed into the filter is the query — a Korean app name would
// otherwise be unsearchable.
func TestFilterPromptKeepsHangulVerbatim(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.handleKey(key("/"))
	if !m.filtering {
		t.Fatal("/ did not open the filter prompt")
	}
	m.handleKey(key("ㅂ"))
	if got := *m.filterTarget(); got != "ㅂ" {
		t.Fatalf("filter = %q, want the jamo verbatim", got)
	}
}

// The overlays are all shortcuts (y/n, j/k, space), so they normalize — a
// confirmation you cannot answer is worse than one you answer by accident.
func TestHangulAnswersTheConfirmOverlay(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.overlay = overlayConfirm
	m.confirm = confirmState{yes: true}

	// `ㅜ` is the physical `n`, which cancels.
	m.handleKey(key("ㅜ"))
	if m.overlay != overlayNone {
		t.Fatal("ㅜ (physical n) did not dismiss the confirmation")
	}
}
