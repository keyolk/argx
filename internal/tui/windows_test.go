package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

func win(kind, sched, dur string, apps ...string) argocd.SyncWindow {
	return argocd.SyncWindow{
		Kind: kind, Schedule: sched, Duration: dur,
		TimeZone: "Asia/Seoul", Applications: apps,
	}
}

// windowModel puts a model inside an application with the given window state.
func windowModel(t *testing.T, w *argocd.AppSyncWindows, project []argocd.SyncWindow) *Model {
	t.Helper()
	m := appModel(t, nil)
	m.Update(windowsMsg{id: m.windowID, windows: w, project: project})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	return m
}

// The same window arrives in more than one list, and the per-application form
// drops the selectors — so it must be matched on the fields both carry, or a
// window is listed twice.
func TestWindowRowsDeduplicate(t *testing.T) {
	assigned := argocd.SyncWindow{Kind: "allow", Schedule: "1 15 * * *", Duration: "5h"}
	full := win("allow", "1 15 * * *", "5h", "web-prod*")

	m := windowModel(t,
		&argocd.AppSyncWindows{
			AssignedWindows: []argocd.SyncWindow{assigned},
			ActiveWindows:   []argocd.SyncWindow{assigned},
			CanSync:         true,
		},
		[]argocd.SyncWindow{full, win("deny", "0 3 * * *", "24h", "other*")},
	)

	rows := m.windowRows()
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 — the assigned window appears in both lists", len(rows))
	}
	// The project's copy is preferred, because it is the one with selectors.
	if len(rows[0].w.Applications) == 0 {
		t.Error("the deduplicated row lost its selectors")
	}
}

// A window that governs this application leads: the reader came here about
// their own application, and the rest is context.
func TestApplicableWindowsSortFirst(t *testing.T) {
	mine := win("allow", "1 15 * * *", "5h", "web-prod*")
	other := win("deny", "0 3 * * *", "24h", "other*")

	m := windowModel(t,
		&argocd.AppSyncWindows{
			AssignedWindows: []argocd.SyncWindow{mine},
			CanSync:         true,
		},
		[]argocd.SyncWindow{other, mine},
	)

	rows := m.windowRows()
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if !rows[0].applies || rows[1].applies {
		t.Errorf("ordering = applies:%v,%v — the applicable window must lead",
			rows[0].applies, rows[1].applies)
	}
}

// The project's other windows are shown, not hidden: a window whose selector
// nearly matched is the usual answer to "why is this not blocked/allowed".
func TestNonApplicableWindowsAreShownAndLabelled(t *testing.T) {
	m := windowModel(t,
		&argocd.AppSyncWindows{CanSync: true},
		[]argocd.SyncWindow{win("deny", "0 3 * * *", "24h", "someone-else*")},
	)
	m.screen = screenWindows

	out := m.View()
	if !strings.Contains(out, "someone-else*") {
		t.Errorf("a project window must still be listed:\n%s", out)
	}
	if !strings.Contains(out, "does not apply") {
		t.Errorf("a window that does not govern this app must say so:\n%s", out)
	}
}

// An empty selector set means the whole project; a blank cell would read as
// missing data rather than as "everything".
func TestWindowScopeNamesTheWholeProject(t *testing.T) {
	if got := windowScope(argocd.SyncWindow{Kind: "deny"}); !strings.Contains(got, "whole project") {
		t.Errorf("scope = %q, want it to say the window covers everything", got)
	}
	got := windowScope(win("allow", "x", "1h", "web-prod*"))
	if !strings.Contains(got, "web-prod*") {
		t.Errorf("scope = %q, want the pattern", got)
	}
	if c := windowScope(argocd.SyncWindow{Clusters: []string{"prod"}}); !strings.Contains(c, "cluster:prod") {
		t.Errorf("scope = %q, want the cluster named", c)
	}
	if n := windowScope(argocd.SyncWindow{Namespaces: []string{"web"}}); !strings.Contains(n, "ns:web") {
		t.Errorf("scope = %q, want the namespace named", n)
	}
}

// Argo CD's UI writes `foo*` and `*foo*` for one pattern; showing both doubles
// the width of every scope for no information.
func TestScopeDropsRedundantPatternPairs(t *testing.T) {
	got := windowScope(win("allow", "x", "1h", "web-prod*", "*web-prod*"))
	if strings.Count(got, "web-prod") != 1 {
		t.Errorf("scope = %q, want the pattern once", got)
	}
}

// A schedule with no zone is read in UTC; not saying so invites reading it as
// local time.
func TestZoneNamesTheDefault(t *testing.T) {
	if got := (argocd.SyncWindow{}).Zone(); got != "UTC" {
		t.Errorf("Zone() = %q, want UTC named explicitly", got)
	}
	if got := (argocd.SyncWindow{TimeZone: "Asia/Seoul"}).Zone(); got != "Asia/Seoul" {
		t.Errorf("Zone() = %q", got)
	}
}

// The server's verdict is reported, not recomputed: the precedence between
// allow and deny windows is the server's to define.
func TestSummaryReportsTheServersVerdict(t *testing.T) {
	blockedWin := win("deny", "0 0 * * *", "24h")

	blocked := windowModel(t, &argocd.AppSyncWindows{
		AssignedWindows: []argocd.SyncWindow{blockedWin},
		ActiveWindows:   []argocd.SyncWindow{blockedWin},
		CanSync:         false,
	}, nil)
	text, isBlocked := blocked.windowSummary()
	if !isBlocked || !strings.Contains(text, "BLOCKED") {
		t.Errorf("summary = %q blocked=%v, want it to say syncing is blocked", text, isBlocked)
	}

	none := windowModel(t, &argocd.AppSyncWindows{CanSync: true}, nil)
	if text, blocked := none.windowSummary(); blocked || text != "none" {
		t.Errorf("summary = %q blocked=%v, want none", text, blocked)
	}
}

// A blocked sync must be visible wherever the reader is: pressing `s` and being
// rejected is a worse way to find out.
func TestBlockedSyncIsFlaggedOnEveryTab(t *testing.T) {
	w := win("deny", "0 0 * * *", "24h")
	m := windowModel(t, &argocd.AppSyncWindows{
		AssignedWindows: []argocd.SyncWindow{w},
		ActiveWindows:   []argocd.SyncWindow{w},
		CanSync:         false,
	}, nil)

	for _, tb := range allTabs {
		m.screen, m.tab = screenApp, tb
		if !strings.Contains(m.View(), "BLOCKED") {
			t.Errorf("%v does not flag the blocked sync:\n%s", tb, m.View())
		}
	}
}

// No windows at all is a meaningful answer, not an empty screen.
func TestNoWindowsSaysSoPlainly(t *testing.T) {
	m := windowModel(t, &argocd.AppSyncWindows{CanSync: true}, nil)
	m.screen = screenWindows

	out := m.View()
	if !strings.Contains(out, "never blocked by a schedule") {
		t.Errorf("an application with no windows should say so:\n%s", out)
	}
}

// `w` opens the view from any tab, and Esc returns to where it was opened from.
func TestWindowViewIsReachableAndReturns(t *testing.T) {
	m := windowModel(t, &argocd.AppSyncWindows{CanSync: true}, nil)

	for _, tb := range allTabs {
		m.screen, m.tab = screenApp, tb
		press(t, m, "w")
		if m.screen != screenWindows {
			t.Fatalf("w from %v did not open the window view, screen = %v", tb, m.screen)
		}
		press(t, m, "esc")
		if m.screen != screenApp || m.tab != tb {
			t.Errorf("esc returned to screen=%v tab=%v, want the tab it was opened from (%v)",
				m.screen, m.tab, tb)
		}
	}
}

// argx shows windows and does not edit them: a change here reaches every
// application in the project at once.
func TestWindowViewHasNoMutatingKeys(t *testing.T) {
	m := windowModel(t,
		&argocd.AppSyncWindows{CanSync: true},
		[]argocd.SyncWindow{win("deny", "0 3 * * *", "24h")},
	)
	m.screen = screenWindows

	for _, k := range []string{"enter", "d", "s", "x", "a", " "} {
		press(t, m, k)
		if m.overlay != overlayNone {
			t.Errorf("%q opened %v — the window view is read-only", k, m.overlay)
			m.overlay = overlayNone
		}
		if m.screen != screenWindows {
			t.Errorf("%q left the window view for %v", k, m.screen)
			m.screen = screenWindows
		}
	}
}

// A stale response must not overwrite the view the user navigated to.
func TestStaleWindowResponseIsDropped(t *testing.T) {
	m := windowModel(t,
		&argocd.AppSyncWindows{CanSync: true},
		[]argocd.SyncWindow{win("allow", "current", "1h")},
	)
	m.reqID = 99

	m.Update(windowsMsg{id: 4, project: []argocd.SyncWindow{win("deny", "stale", "1h")}})

	rows := m.windowRows()
	if len(rows) != 1 || rows[0].w.Schedule != "current" {
		t.Errorf("a stale response overwrote the view: %+v", rows)
	}
}

// Every width must fit, in every icon set — the window view is a table like
// any other.
func TestWindowViewFitsTheTerminal(t *testing.T) {
	for _, env := range []string{"unicode", "nerd", "ascii"} {
		for _, size := range [][2]int{{150, 24}, {120, 20}, {100, 20}, {80, 24}, {60, 14}} {
			w, h := size[0], size[1]
			t.Setenv("ARGX_ICONS", env)
			t.Setenv("NO_COLOR", "1")

			m := windowModel(t,
				&argocd.AppSyncWindows{CanSync: false},
				[]argocd.SyncWindow{
					win("deny", "3 0 * * *", "24h", "a-very-long-application-pattern-prod*", "*a-very-long-application-pattern-prod*"),
					win("allow", "30 13 * * *", "6h30m", "another-long-one*"),
					{Kind: "deny", Schedule: "0 0 * * 0", Duration: "48h"},
				},
			)
			m.gl = newGlyphs()
			m.st = newStyles()
			m.st.initContexts(len(m.fleet.Names()))
			m.screen = screenWindows
			m.Update(tea.WindowSizeMsg{Width: w, Height: h})

			out := m.View()
			if got := strings.Count(out, "\n") + 1; got != h {
				t.Errorf("%s %dx%d rendered %d lines, want %d", env, w, h, got, h)
			}
			for i, line := range strings.Split(out, "\n") {
				if got := lipglossWidth(line); got > w {
					t.Errorf("%s %dx%d line %d is %d cells, want at most %d:\n%q",
						env, w, h, i, got, w, line)
				}
			}
		}
	}
}

// Entering an application issues two fetches at once. They must not share a
// request stamp: with one counter, the second command's stamp invalidates the
// first's response and the resource tree simply never arrives.
func TestConcurrentFetchesDoNotInvalidateEachOther(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.apps[0].Spec.Source = &argocd.Source{}

	press(t, m, "enter") // issues loadTreeCmd and loadWindowsCmd together

	// Both responses come back, in either order, and both must land.
	tree := &argocd.Tree{Nodes: []argocd.Node{
		{ResourceRef: argocd.ResourceRef{UID: "u1", Kind: "Pod", Name: "web"}},
	}}
	m.Update(treeMsg{
		id: m.treeID, app: &m.apps[0], rows: tree.Flatten("argocd", "test"),
	})
	m.Update(windowsMsg{
		id: m.windowID, windows: &argocd.AppSyncWindows{CanSync: true},
	})

	if m.app == nil {
		t.Fatal("the tree response was dropped — the two fetches share a request stamp")
	}
	if len(m.tree) != 1 {
		t.Errorf("tree = %d rows, want the one node", len(m.tree))
	}
	if m.windows == nil {
		t.Error("the windows response was dropped")
	}
}

// Each sequence still rejects its own stale responses.
func TestEachSequenceRejectsItsOwnStaleResponses(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.treeID, m.windowID = 5, 7

	m.Update(treeMsg{id: 4, app: &m.apps[0]})
	if m.app != nil {
		t.Error("a stale tree response was applied")
	}
	m.Update(windowsMsg{id: 6, windows: &argocd.AppSyncWindows{CanSync: true}})
	if m.windows != nil {
		t.Error("a stale windows response was applied")
	}
}

// The window view is a list like any other, and its keys must actually reach
// it: the screen was unreachable from the key dispatch once, so j and k did
// nothing at all.
func TestWindowViewNavigates(t *testing.T) {
	m := windowModel(t,
		&argocd.AppSyncWindows{CanSync: true},
		[]argocd.SyncWindow{
			win("allow", "a", "1h"), win("deny", "b", "2h"), win("allow", "c", "3h"),
		},
	)
	m.screen = screenWindows

	press(t, m, "j")
	if m.windowCur != 1 {
		t.Errorf("j moved to %d, want 1 — the key never reached the view", m.windowCur)
	}
	press(t, m, "j")
	if m.windowCur != 2 {
		t.Errorf("j moved to %d, want 2", m.windowCur)
	}
	press(t, m, "k")
	if m.windowCur != 1 {
		t.Errorf("k moved to %d, want 1", m.windowCur)
	}
	press(t, m, "G")
	if m.windowCur != 2 {
		t.Errorf("G moved to %d, want the last row", m.windowCur)
	}
	press(t, m, "g")
	if m.windowCur != 0 {
		t.Errorf("g moved to %d, want 0", m.windowCur)
	}
	// Past either end the cursor clamps rather than wrapping or going negative.
	press(t, m, "k", "k")
	if m.windowCur != 0 {
		t.Errorf("k past the top moved to %d, want 0", m.windowCur)
	}
	press(t, m, "down")
	if m.windowCur != 1 {
		t.Errorf("the down arrow moved to %d, want 1", m.windowCur)
	}
}

// Windows are edited on the project, so that is where `o` goes; the
// application's own page stays reachable with `O`.
func TestWindowViewOpensTheProject(t *testing.T) {
	m := windowModel(t, &argocd.AppSyncWindows{CanSync: true}, nil)
	m.screen = screenWindows

	url := m.projectURL(m.app)
	if !strings.Contains(url, "/settings/projects/") {
		t.Errorf("projectURL = %q, want the project settings page", url)
	}
	if !strings.Contains(url, m.app.Spec.Project) {
		t.Errorf("projectURL = %q, want it to name the project", url)
	}

	if cmd := press(t, m, "o"); cmd == nil {
		t.Error("o should open the project in a browser")
	}
	if cmd := press(t, m, "O"); cmd == nil {
		t.Error("O should open the application in a browser")
	}
	if m.screen != screenWindows {
		t.Errorf("opening a browser left the view for %v", m.screen)
	}
}
