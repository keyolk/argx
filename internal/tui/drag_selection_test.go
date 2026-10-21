package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/tuitest"
)

func mouseDo(m *Model, x, y int, b tea.MouseButton, a tea.MouseAction) tea.Cmd {
	_, cmd := m.Update(mouseMsg(x, y, b, a))
	return cmd
}

func dragTo(m *Model, x0, y0, x1, y1 int) tea.Cmd {
	mouseDo(m, x0, y0, tea.MouseButtonLeft, tea.MouseActionPress)
	mouseDo(m, x1, y1, tea.MouseButtonLeft, tea.MouseActionMotion)
	return mouseDo(m, x1, y1, tea.MouseButtonNone, tea.MouseActionRelease)
}

// atCell is at() with a full ANSI strip. The package's stripANSI leaves
// fragments of render.Dim's sequences on the dimmed frame under an overlay,
// which shifts every column found there.
func atCell(t *testing.T, m *Model, text string) (x, y int) {
	t.Helper()
	for i, line := range strings.Split(m.View(), "\n") {
		plain := ansi.Strip(line)
		if j := strings.Index(plain, text); j >= 0 {
			return ansi.StringWidth(plain[:j]), i
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, ansi.Strip(m.View()))
	return 0, 0
}

func copiedText(m *Model) string { return m.textSel.Text(m.selPanes(), m.View()) }

// logsModel is the log pager with more lines than the body holds.
func logsModel(t *testing.T) *Model {
	m := mouseApps(t, 3, 120, 20)
	for i := range 60 {
		m.pager = append(m.pager, fmt.Sprintf("log line %02d level=info msg=ok", i))
	}
	m.push(screenLogs)
	return m
}

func TestDragPastThePagerBottomScrollsAndKeepsScrolledOutLines(t *testing.T) {
	m := logsModel(t)
	x, y := at(t, m, "log line 00")
	_, below := at(t, m, "log line 16") // the last body row; the status line is under it
	below++
	if strings.Contains(stripANSI(m.View()), "log line 20") {
		t.Fatal("setup: line 20 should be below the page")
	}
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	for range 4 {
		mouseDo(m, x+40, below, tea.MouseButtonLeft, tea.MouseActionMotion)
	}
	if m.pagerTop != 4 {
		t.Fatalf("pagerTop=%d after four drags past the bottom, want 4", m.pagerTop)
	}
	if cmd := mouseDo(m, x+40, below, tea.MouseButtonNone, tea.MouseActionRelease); cmd == nil {
		t.Fatal("release must return the copy command")
	}
	got := copiedText(m)
	if !strings.HasPrefix(got, "log line 00") || !strings.Contains(got, "log line 20") {
		t.Fatalf("copy %q must run from the scrolled-out line 00 to line 20", got)
	}
}

func TestPagerDoubleClickPicksAWord(t *testing.T) {
	m := logsModel(t)
	x, y := at(t, m, "level=info")
	mouseDo(m, x+2, y, tea.MouseButtonLeft, tea.MouseActionPress)
	mouseDo(m, x+2, y, tea.MouseButtonNone, tea.MouseActionRelease)
	if cmd := mouseDo(m, x+2, y, tea.MouseButtonLeft, tea.MouseActionPress); cmd == nil {
		t.Fatal("a word pick copies")
	}
	if got := copiedText(m); got != "level" {
		t.Fatalf("word pick = %q, want level", got)
	}
}

func TestDragInTheListStaysInTheBody(t *testing.T) {
	m := mouseApps(t, 3, 120, 20)
	x, y := at(t, m, "svc-02")
	lines := strings.Split(stripANSI(m.View()), "\n")
	footer := len(lines) - 1
	dragTo(m, x, y, x+10, footer)
	got := copiedText(m)
	if !strings.HasPrefix(got, "svc-02") || !strings.Contains(got, "svc-03") {
		t.Fatalf("copy %q should run from svc-02 down the list", got)
	}
	if strings.Contains(got, strings.TrimSpace(lines[footer])) {
		t.Fatalf("copy %q leaked the footer", got)
	}
	runs := tuitest.Reversed(m.View())
	if len(runs) == 0 {
		t.Fatal("the selection is not drawn")
	}
	for _, r := range runs {
		if strings.TrimSpace(r) == "" {
			continue // the body's empty rows under the last app
		}
		if strings.Contains(strings.TrimSpace(lines[footer]), strings.TrimSpace(r)) || strings.Contains(r, "NAME") {
			t.Fatalf("highlight %q left the body", r)
		}
	}
}

func TestSideBySideDragStaysInItsColumn(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m.pager = []string{"-  replicas: 3", "+  replicas: 5", "-  image: old", "+  image: new"}
	m.screen, m.sxs = screenDiff, true
	x, y := at(t, m, "replicas: 3")
	rx, ry := at(t, m, "image: new")
	dragTo(m, x, y, rx+5, ry)
	got := copiedText(m)
	if !strings.Contains(got, "replicas: 3") || !strings.Contains(got, "image: old") {
		t.Fatalf("copy %q should hold the left column", got)
	}
	if strings.Contains(got, "replicas: 5") || strings.Contains(got, "new") || strings.Contains(got, "│") {
		t.Fatalf("copy %q leaked the right column or the gutter", got)
	}
	for _, r := range tuitest.Reversed(m.View()) {
		if strings.Contains(r, "│") || strings.Contains(r, "new") || strings.Contains(r, "replicas: 5") {
			t.Fatalf("highlight %q crosses the gutter", r)
		}
	}
}

func TestPressWithoutMotionIsStillAClick(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	x, y := at(t, m, "svc-03")
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	mouseDo(m, x, y, tea.MouseButtonNone, tea.MouseActionRelease)
	if got := m.currentApp().Name(); got != "svc-03" {
		t.Fatalf("selected %q, want svc-03: a still press is the app's click", got)
	}
	if m.textSel.Active() || len(tuitest.Reversed(m.View())) != 0 {
		t.Fatal("a click without motion must not leave a selection")
	}
}

func TestSecondClickOnAListRowIsStillOpenNotAWordPick(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	x, y := at(t, m, "svc-02")
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	mouseDo(m, x, y, tea.MouseButtonNone, tea.MouseActionRelease)
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if m.screen != screenApp {
		t.Fatalf("screen=%v: a list keeps second click = Enter", m.screen)
	}
	if m.textSel.Active() {
		t.Fatal("a list pane must not pick a word on a double click")
	}
}

func TestKeyClearsTheSelection(t *testing.T) {
	m := logsModel(t)
	x, y := at(t, m, "log line 02")
	dragTo(m, x, y, x+8, y)
	if !m.textSel.Active() {
		t.Fatal("setup: the drag should leave a selection")
	}
	m.Update(key("j"))
	if m.textSel.Active() || len(tuitest.Reversed(m.View())) != 0 {
		t.Fatal("a key must clear the selection")
	}
}

func TestErrorOverlayTextIsSelectableInsideItsBorder(t *testing.T) {
	m := mouseApps(t, 3, 120, 30)
	m.showError(errors.New("sync failed: revision 7f3a9c2 not found"))
	x, y := atCell(t, m, "sync failed")
	dragTo(m, x+3, y, 0, y) // out through the left border onto the list
	if got := copiedText(m); got != "sync" {
		t.Fatalf("copy %q, want the overlay text up to the press only", got)
	}
	if got := tuitest.Reversed(m.View()); len(got) != 1 || got[0] != "sync" {
		t.Fatalf("highlighted %q, want only the overlay's [sync]", got)
	}
}

func TestListUnderAnOverlayIsNotSelectable(t *testing.T) {
	m := mouseApps(t, 3, 120, 30)
	x, y := at(t, m, "svc-01")
	m.showError(errors.New("boom"))
	dragTo(m, x, y, x+5, y+1)
	if m.textSel.Active() || len(tuitest.Reversed(m.View())) != 0 {
		t.Fatal("the list under an overlay must not be selectable")
	}
}

// The context panel's last row is "… N more" while lines remain below: a
// drag onto it scrolls rather than selecting the hidden line under it.
func TestDragOntoTheContextMoreMarkerScrolls(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", nil))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: minHeight})
	m.ctxDetail = true
	x, y := at(t, m, "CONNECTION")
	_, marker := at(t, m, "more line")
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	mouseDo(m, x+30, marker, tea.MouseButtonLeft, tea.MouseActionMotion)
	if m.pagerTop != 1 {
		t.Fatalf("pagerTop=%d after a drag onto the marker, want 1", m.pagerTop)
	}
}
