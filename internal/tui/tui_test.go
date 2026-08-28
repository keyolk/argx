package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
	"github.com/keyolk/argx/internal/config"
)

// newTestModel builds a model with a fixed set of applications and no live
// client. Nothing in these tests issues a request: they assert state
// transitions, and any command returned is left un-invoked (calling it would
// hit the network).
func newTestModel(t *testing.T, names ...string) *Model {
	t.Helper()
	// Isolate from the developer's own argocd session and $BROWSER.
	t.Setenv("HOME", t.TempDir())

	fleet := argocd.NewFleet([]config.Context{{Name: "test", Server: "argocd.test"}})
	m := New(context.Background(), fleet, &config.Config{})
	m.width, m.height = 120, 40

	for _, n := range names {
		var a argocd.Application
		a.Context = "test"
		a.Metadata.Name = n
		a.Metadata.Namespace = "argocd"
		a.Spec.Project = "default"
		a.Status.Sync.Status = "Synced"
		a.Status.Health.Status = "Healthy"
		m.apps = append(m.apps, a)
	}
	m.applyAppFilter()
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(t *testing.T, m *Model, keys ...string) tea.Cmd {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(key(k))
	}
	return cmd
}

// ---- multi-select ----

func TestSpaceMarksAndAdvances(t *testing.T) {
	m := newTestModel(t, "alpha", "beta", "gamma")

	press(t, m, " ")
	if !m.appMarks["test/alpha"] {
		t.Error("space should mark the row under the cursor")
	}
	if m.appCur != 1 {
		t.Errorf("cursor = %d, want 1 — space should advance so a run marks with one repeated key", m.appCur)
	}

	press(t, m, " ")
	if len(m.appMarks) != 2 {
		t.Errorf("marks = %d, want 2", len(m.appMarks))
	}
}

func TestSpaceUnmarks(t *testing.T) {
	m := newTestModel(t, "alpha", "beta")
	press(t, m, " ")
	m.appCur = 0
	press(t, m, " ")
	if m.appMarks["test/alpha"] {
		t.Error("space on a marked row should unmark it")
	}
}

// Actions must behave identically whether or not anything is marked: with no
// marks they act on the cursor row.
func TestMarkedAppsFallsBackToCursor(t *testing.T) {
	m := newTestModel(t, "alpha", "beta", "gamma")
	m.appCur = 2

	got := m.markedApps()
	if len(got) != 1 || got[0].Name() != "gamma" {
		t.Fatalf("markedApps() = %v, want just the cursor row (gamma)", names(got))
	}

	m.appMarks["test/alpha"] = true
	m.appMarks["test/beta"] = true
	got = m.markedApps()
	if len(got) != 2 {
		t.Fatalf("markedApps() = %v, want the two marked apps", names(got))
	}
}

// The marked set must come back in display order so the confirmation modal
// lists targets the way the user sees them.
func TestMarkedAppsUsesDisplayOrder(t *testing.T) {
	m := newTestModel(t, "alpha", "beta", "gamma")
	m.appMarks["test/gamma"] = true
	m.appMarks["test/alpha"] = true

	got := names(m.markedApps())
	want := []string{"alpha", "gamma"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// "mark all" must mean all *visible* rows: with a filter active, marking apps
// the user cannot see is how a sync hits the wrong thing.
func TestMarkAllRespectsFilter(t *testing.T) {
	m := newTestModel(t, "web-prod", "web-dev", "api-prod")
	m.appFilter = "prod"
	m.applyAppFilter()

	press(t, m, "a")
	if len(m.appMarks) != 2 {
		t.Fatalf("marks = %d, want 2 — only the filtered rows", len(m.appMarks))
	}
	if m.appMarks["test/web-dev"] {
		t.Error("a filtered-out app must not be marked")
	}

	press(t, m, "a")
	if len(m.appMarks) != 0 {
		t.Errorf("a second `a` should clear the filtered marks, got %d", len(m.appMarks))
	}
}

// A mark on an app that has disappeared from the server must not survive a
// reload, or a later sync silently targets something the user never saw.
func TestReloadPrunesStaleMarks(t *testing.T) {
	m := newTestModel(t, "alpha", "beta")
	m.appMarks["test/alpha"] = true
	m.appMarks["test/beta"] = true

	var gone argocd.Application
	gone.Context = "test"
	gone.Metadata.Name = "alpha"
	m.Update(appsMsg{apps: []argocd.Application{gone}})

	if m.appMarks["test/beta"] {
		t.Error("a mark for an app no longer on the server must be pruned")
	}
	if !m.appMarks["test/alpha"] {
		t.Error("a mark for an app that still exists must survive")
	}
}

func TestTreeMarksKeyOnUID(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.tree = []argocd.TreeRow{
		{Node: argocd.Node{ResourceRef: argocd.ResourceRef{UID: "u1", Kind: "Pod", Name: "web-1"}}},
		{Node: argocd.Node{ResourceRef: argocd.ResourceRef{UID: "u2", Kind: "Pod", Name: "web-2"}}},
	}
	m.applyTreeFilter()
	m.screen = screenApp
	m.tab = tabResources

	press(t, m, " ")
	if !m.treeMarks["u1"] {
		t.Error("tree marks should key on UID")
	}

	// The same pod name recreated with a new UID must not inherit the mark.
	m.tree[0].Node.UID = "u3"
	m.pruneTreeMarks()
	if m.treeMarks["u1"] {
		t.Error("a mark for a UID no longer in the tree must be pruned")
	}
}

// ---- navigation ----

func TestQuitOnlyAtRoot(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.push(screenApp)

	press(t, m, "q")
	if m.screen != screenApps {
		t.Fatalf("q inside a drill-down should go back, screen = %v", m.screen)
	}

	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q at the application list should quit")
	}
}

func TestEscapePopsScreenStack(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.push(screenApp)
	m.push(screenDiff)

	press(t, m, "esc")
	if m.screen != screenApp {
		t.Errorf("screen = %v, want the tree", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenApps {
		t.Errorf("screen = %v, want the application list", m.screen)
	}
	// Popping past the root must stay put rather than panicking.
	press(t, m, "esc")
	if m.screen != screenApps {
		t.Errorf("screen = %v, want to stay at the root", m.screen)
	}
}

func TestCursorStaysOnAppAcrossFilterChange(t *testing.T) {
	m := newTestModel(t, "alpha", "beta", "gamma")
	m.appCur = 2 // gamma

	m.appFilter = "a"
	m.applyAppFilter()

	if got := m.currentApp(); got == nil || got.Name() != "gamma" {
		t.Errorf("cursor moved off gamma on a filter that still matches it")
	}
}

// ---- key ownership ----

// A new binding must not swallow the navigation keys the rest of the app owns.
func TestUnboundKeysDoNotMoveTheCursor(t *testing.T) {
	m := newTestModel(t, "alpha", "beta", "gamma")
	for _, k := range []string{"x", "z", "w", "v"} {
		before := m.appCur
		press(t, m, k)
		if m.appCur != before {
			t.Errorf("%q moved the cursor from %d to %d", k, before, m.appCur)
		}
		if len(m.appMarks) != 0 {
			t.Errorf("%q marked something", k)
		}
	}
}

// ---- confirmation gating ----

// Sync mutates a cluster, so it must never fire straight off a keypress.
func TestSyncRequiresConfirmation(t *testing.T) {
	m := newTestModel(t, "alpha")

	press(t, m, "s")
	if m.overlay != overlaySyncOpts {
		t.Fatalf("s should open the sync options modal, overlay = %v", m.overlay)
	}

	press(t, m, "enter")
	if m.overlay != overlayConfirm {
		t.Fatalf("the options modal should lead to a confirmation, overlay = %v", m.overlay)
	}
	if m.confirm.action == nil {
		t.Fatal("the confirmation should carry the action to run")
	}

	press(t, m, "n")
	if m.overlay != overlayNone {
		t.Error("n should dismiss the confirmation")
	}
	if m.confirm.action != nil {
		t.Error("declining should discard the pending action")
	}
}

func TestSyncOptionsToggle(t *testing.T) {
	m := newTestModel(t, "alpha")
	press(t, m, "s")

	press(t, m, "p")
	if !m.syncOpts.prune {
		t.Error("p should toggle prune on")
	}
	press(t, m, "d")
	if !m.syncOpts.dryRun {
		t.Error("d should toggle dry-run on")
	}
	press(t, m, "p")
	if m.syncOpts.prune {
		t.Error("p should toggle prune back off")
	}
}

// An overlay must own the keyboard: keys that act on the screen behind it are
// how people sync the wrong thing.
func TestOverlaySwallowsUnderlyingKeys(t *testing.T) {
	m := newTestModel(t, "alpha", "beta", "gamma")
	press(t, m, "s") // open the sync modal
	before := m.appCur

	press(t, m, "j", "j")
	if m.appCur != before {
		t.Errorf("j moved the list cursor while a modal was open (%d → %d)", before, m.appCur)
	}
}

// Opening many browser tabs at once is easy to trigger with a stray `a`, so
// past a handful it must ask first.
func TestOpenManyAppsAsksFirst(t *testing.T) {
	m := newTestModel(t, "a1", "a2", "a3", "a4", "a5", "a6", "a7")
	press(t, m, "a") // mark all seven
	press(t, m, "o")

	if m.overlay != overlayConfirm {
		t.Fatalf("opening 7 tabs should ask first, overlay = %v", m.overlay)
	}
	if !strings.Contains(strings.Join(m.confirm.body, " "), "7") {
		t.Errorf("the prompt should say how many tabs: %v", m.confirm.body)
	}
}

func TestOpenFewAppsDoesNotAsk(t *testing.T) {
	m := newTestModel(t, "a1", "a2")
	press(t, m, " ") // mark a1
	cmd := press(t, m, "o")

	if m.overlay != overlayNone {
		t.Errorf("opening one tab should not ask, overlay = %v", m.overlay)
	}
	if cmd == nil {
		t.Error("o should return a command that opens the browser")
	}
}

// ---- stale responses ----

// A result whose target changed mid-flight must be dropped, not rendered over
// whatever the user navigated to instead.
func TestStaleResponseIsDropped(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.pager = []string{"current"}
	m.reqID = 5

	m.Update(pagerMsg{id: 4, title: "old", lines: []string{"stale"}})

	if m.pager[0] != "current" {
		t.Errorf("a stale response overwrote the view: %v", m.pager)
	}
}

func TestCurrentResponseIsApplied(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.reqID = 5

	m.Update(pagerMsg{id: 5, title: "fresh", lines: []string{"new"}})

	if m.pager[0] != "new" || m.pagerTitle != "fresh" {
		t.Errorf("the current response was not applied: %v / %q", m.pager, m.pagerTitle)
	}
}

// ---- rendering ----

func TestViewRendersExactHeight(t *testing.T) {
	m := newTestModel(t, "alpha", "beta")
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 14}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		got := strings.Count(m.View(), "\n") + 1
		if got != size[1] {
			t.Errorf("%dx%d rendered %d lines, want %d",
				size[0], size[1], got, size[1])
		}
	}
}

func TestViewBelowMinimumShowsAMessage(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})

	out := m.View()
	if !strings.Contains(out, "too small") {
		t.Errorf("below the minimum size the user must be told, got:\n%s", out)
	}
}

func TestNarrowLayoutDropsColumnsRatherThanSqueezing(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.width = 62
	name, proj, dst, _ := m.appColumns()
	if proj != 0 || dst != 0 {
		t.Errorf("at 62 columns project/destination should be dropped, got %d/%d", proj, dst)
	}
	if name < 12 {
		t.Errorf("the name column should keep a usable width, got %d", name)
	}
}

// Auto-refresh off means a tick does no work, so an idle argx renders nothing.
func TestTickDoesNothingWhenAutoRefreshOff(t *testing.T) {
	m := newTestModel(t, "alpha")
	_, cmd := m.Update(tickMsg{})
	if cmd != nil {
		t.Error("a tick with auto-refresh off should schedule no work")
	}
}

func names(apps []argocd.Application) []string {
	out := make([]string, len(apps))
	for i := range apps {
		out[i] = apps[i].Name()
	}
	return out
}
