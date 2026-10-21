package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/link"
	"github.com/keyolk/tuikit/selection"
	"github.com/keyolk/tuikit/tuitest"

	"github.com/keyolk/argx/internal/argocd"
)

func linked(url, label string) string {
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

// The app name is an OSC 8 link to the app in the Argo CD UI: the URL `o`
// opens, built from the context's server.
func TestAppNameLinksToTheArgoCDUI(t *testing.T) {
	m := mouseApps(t, 3, 120, 20)
	want := linked("https://argocd.test/applications/svc-02", "svc-02")
	if !strings.Contains(m.View(), want) {
		t.Fatalf("the app name should link to its UI page:\n%q", m.View())
	}
}

// An app on a server argx has no context for gets no link: there is no URL
// to build, and the label stays plain.
func TestAppOnAnUnknownServerIsNotLinked(t *testing.T) {
	m := mouseApps(t, 2, 120, 20)
	m.apps[0].Context = "gone"
	m.applyAppFilter()
	if strings.Contains(m.View(), "\x1b]8;;https://argocd.test/applications/svc-01") ||
		strings.Contains(m.View(), linked("", "svc-01")) {
		t.Fatalf("an app without a server URL was linked:\n%q", m.View())
	}
	if !strings.Contains(stripANSI(m.View()), "svc-01") {
		t.Fatal("its name must still show")
	}
}

func TestAppSetNameLinksToTheArgoCDUI(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	var s argocd.ApplicationSet
	s.Context = "test"
	s.Metadata.Name = "fleet-gen"
	m.appsets = []argocd.ApplicationSet{s}
	m.appsetRows = []int{0}
	m.push(screenAppSets)
	want := linked("https://argocd.test/applicationsets/fleet-gen", "fleet-gen")
	if !strings.Contains(m.View(), want) {
		t.Fatalf("the ApplicationSet name should link to its UI page:\n%q", m.View())
	}
}

func TestContextNameLinksToItsServer(t *testing.T) {
	m := ctxModel(t, ctxRow("test", nil))
	if !strings.Contains(m.View(), linked("https://argocd.test", "test")) {
		t.Fatalf("the context name should link to its server:\n%q", m.View())
	}
}

// `o` was already the open-in-browser key in every context; it now refuses
// anything but http(s) before the opener runs.
func TestBrowserOpenRefusesANonHTTPURL(t *testing.T) {
	m := newTestModel(t)
	msg := m.openBrowserCmd([]string{"javascript:alert(1)"})()
	am, ok := msg.(actionMsg)
	if !ok || !errors.Is(am.err, link.ErrInvalid) {
		t.Fatalf("a javascript: URL should be refused with ErrInvalid, got %#v", msg)
	}
}

// --- selection v2 ---

// The pager's offset is set from the kit's Offset. The context panel's keys
// leave pagerTop past the end for its renderer to clamp; a drag past the top
// from there must still scroll the panel up one line. (View clamps pagerTop
// through its pointer receiver, and Bubble Tea renders between events, so the
// panes never see the raw value.)
func TestDragPastTheTopOfAnOverscrolledContextPanelScrollsUp(t *testing.T) {
	m := ctxModel(t, ctxRow("prod", nil))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: minHeight})
	m.ctxDetail = true
	for range 200 {
		m.Update(key("j")) // far past the end, as holding j leaves it
	}
	// The panel's top row, as View drew it.
	top := m.bodyRect().Y
	text := strings.TrimSpace(strings.Split(stripANSI(m.View()), "\n")[top])
	if text == "" {
		t.Fatal("setup: the panel's top row is blank")
	}
	x, y := at(t, m, text)
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	mouseDo(m, x, y-1, tea.MouseButtonLeft, tea.MouseActionMotion) // above the panel
	if _, yAfter := at(t, m, text); yAfter != y+1 {
		t.Fatalf("%q moved from row %d to %d; a drag past the top scrolls up one line", text, y, yAfter)
	}
}

func TestDragRestingPastThePagerBottomKeepsScrolling(t *testing.T) {
	old := selection.AutoScrollInterval
	selection.AutoScrollInterval = time.Millisecond
	t.Cleanup(func() { selection.AutoScrollInterval = old })
	m := logsModel(t)
	x, y := at(t, m, "log line 00")
	_, last := at(t, m, "log line 16")
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	cmd := mouseDo(m, x, last+1, tea.MouseButtonLeft, tea.MouseActionMotion)
	if m.pagerTop != 1 {
		t.Fatalf("setup: pagerTop = %d after one motion past the bottom", m.pagerTop)
	}
	for i := range 3 {
		var tick tea.Msg
		for _, msg := range tuitest.Drain(cmd, 200*time.Millisecond) {
			if _, ok := msg.(selection.AutoScrollMsg); ok {
				tick = msg
			}
		}
		if tick == nil {
			t.Fatalf("tick %d: a drag resting past the edge should schedule an AutoScrollMsg", i)
		}
		_, cmd = m.Update(tick)
		if m.pagerTop != i+2 {
			t.Fatalf("tick %d: pagerTop = %d, want %d", i, m.pagerTop, i+2)
		}
	}
}

func TestNoColorKeepsTheDragHighlight(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := logsModel(t)
	x, y := at(t, m, "log line 03")
	dragTo(m, x, y, x+len("log line")-1, y)
	if got := tuitest.Reversed(m.View()); len(got) != 1 || got[0] != "log line" {
		t.Fatalf("highlighted %q, want [log line]", got)
	}
}

// The release's command copies through selection.Clipboard, the same path the
// app takes, and the toast says how much.
func TestReleaseCopiesThroughTheClipboard(t *testing.T) {
	var got []string
	old := selection.Clipboard
	selection.Clipboard = func(s string) error { got = append(got, s); return nil }
	t.Cleanup(func() { selection.Clipboard = old })
	m := logsModel(t)
	x, y := at(t, m, "log line 03")
	cmd := dragTo(m, x, y, x+len("log line 03")-1, y)
	for _, msg := range tuitest.Drain(cmd, 0) {
		m.Update(msg)
	}
	if len(got) != 1 || got[0] != "log line 03" {
		t.Fatalf("clipboard got %q", got)
	}
	if !strings.Contains(stripANSI(m.View()), fmt.Sprintf("copied %d chars", len("log line 03"))) {
		t.Fatal("the copy should show its toast")
	}
}
