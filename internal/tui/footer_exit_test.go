package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Every hint list ends in the way out. A footer that drops "q quit" to make
// room for "d diff" has cut the wrong thing — someone who cannot find the exit
// kills the terminal window.
func TestFooterAlwaysKeepsTheWayOut(t *testing.T) {
	for _, w := range []int{minWidth, 70, 80, 120} {
		for _, sc := range []screen{screenApps, screenApp, screenAppSets, screenWindows, screenSchedule} {
			m := newTestModel(t, "web")
			m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
			m.screen = sc

			out := stripANSI(m.renderFooter())
			if !strings.Contains(out, "q quit") {
				t.Errorf("@%d screen %v lost the exit hint:\n%s", w, sc, out)
			}
		}
	}
}
