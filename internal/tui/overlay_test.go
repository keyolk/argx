package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A modal is drawn over the frame, not over a blank screen: rows outside the
// box still show the list it is about.
func TestOverlayKeepsTheFrameBehindIt(t *testing.T) {
	m := newTestModel(t, "alpha-frontend", "beta-backend")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.showError(errors.New("something broke"))

	out := m.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "something broke") {
		t.Fatalf("the modal is missing:\n%s", plain)
	}
	if !strings.Contains(plain, "alpha-frontend") {
		t.Errorf("the frame behind the modal was dropped:\n%s", plain)
	}

	lines := strings.Split(out, "\n")
	if len(lines) != 30 {
		t.Errorf("overlay is %d lines, want the terminal height 30", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 120 {
			t.Errorf("line %d is %d cells, want at most 120", i, w)
		}
	}
}

// A wide character under the box's left edge must not shift the rest of the
// line past the terminal width.
func TestSpliceOverlayHandlesWideCharacterAtTheEdge(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 10, 1
	// "한" occupies cells 2-3; the box starts at cell 3, inside it.
	got := m.spliceOverlay("ab한defghi", "XXXX")
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("spliced line is %d cells, want at most 10: %q", w, stripANSI(got))
	}
	if !strings.Contains(stripANSI(got), "XXXX") {
		t.Errorf("box missing: %q", stripANSI(got))
	}
}
