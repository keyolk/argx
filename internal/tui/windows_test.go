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

// The per-application payload drops the selectors, so they are recovered from
// the project's copy — otherwise a window renders with no indication of what it
// covers.
func TestSelectorsAreRecoveredFromTheProject(t *testing.T) {
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
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want just the assigned window", len(rows))
	}
	if len(rows[0].w.Applications) == 0 {
		t.Error("the row lost its selectors — they only exist on the project's copy")
	}
	if !rows[0].active {
		t.Error("the window is open and should say so")
	}
}

// Only the windows that govern this application are listed. A project's other
// windows govern other applications, and listing them made the reader work out
// which lines were about the thing they were looking at.
func TestOnlyApplicableWindowsAreListed(t *testing.T) {
	mine := win("allow", "1 15 * * *", "5h", "web-prod*")
	other := win("deny", "0 3 * * *", "24h", "someone-else*")

	m := windowModel(t,
		&argocd.AppSyncWindows{
			AssignedWindows: []argocd.SyncWindow{mine},
			CanSync:         true,
		},
		[]argocd.SyncWindow{other, mine},
	)

	rows := m.windowRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want only the window that applies", len(rows))
	}
	if rows[0].w.Schedule != mine.Schedule {
		t.Errorf("listed %q, want the applicable window", rows[0].w.Schedule)
	}

	m.screen = screenWindows
	out := m.View()
	if strings.Contains(out, "someone-else*") {
		t.Errorf("a window governing other applications was listed:\n%s", out)
	}
}

// Open windows lead: what is in effect right now is what the reader came for.
func TestOpenWindowsSortFirst(t *testing.T) {
	closed := win("allow", "0 3 * * *", "1h", "web-prod*")
	openNow := win("deny", "1 15 * * *", "5h", "web-prod*")

	m := windowModel(t,
		&argocd.AppSyncWindows{
			AssignedWindows: []argocd.SyncWindow{closed, openNow},
			ActiveWindows:   []argocd.SyncWindow{openNow},
			CanSync:         false,
		},
		[]argocd.SyncWindow{closed, openNow},
	)

	rows := m.windowRows()
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if !rows[0].active || rows[1].active {
		t.Errorf("ordering = active:%v,%v — the open window must lead",
			rows[0].active, rows[1].active)
	}
}

// A project with windows, none of which match, is a different answer from a
// project with no windows at all — and the reader needs to tell them apart.
func TestNoMatchingWindowSaysTheProjectHasSome(t *testing.T) {
	m := windowModel(t,
		&argocd.AppSyncWindows{CanSync: true},
		[]argocd.SyncWindow{win("deny", "0 3 * * *", "24h", "someone-else*")},
	)
	m.screen = screenWindows

	out := m.View()
	if !strings.Contains(out, "no sync window applies") {
		t.Errorf("the view should say no window applies:\n%s", out)
	}
	if !strings.Contains(out, "the project has 1") {
		t.Errorf("it should say the project has windows that did not match:\n%s", out)
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
	w := win("deny", "0 3 * * *", "24h")
	m := windowModel(t,
		&argocd.AppSyncWindows{AssignedWindows: []argocd.SyncWindow{w}, CanSync: true},
		[]argocd.SyncWindow{w},
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
	current := win("allow", "current", "1h", "web-prod*")
	m := windowModel(t,
		&argocd.AppSyncWindows{
			AssignedWindows: []argocd.SyncWindow{current},
			CanSync:         true,
		},
		[]argocd.SyncWindow{current},
	)
	m.windowID = 99

	stale := win("deny", "stale", "1h", "web-prod*")
	m.Update(windowsMsg{
		id:      4,
		windows: &argocd.AppSyncWindows{AssignedWindows: []argocd.SyncWindow{stale}},
		project: []argocd.SyncWindow{stale},
	})

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

			ws := []argocd.SyncWindow{
				win("deny", "3 0 * * *", "24h", "a-very-long-application-pattern-prod*", "*a-very-long-application-pattern-prod*"),
				win("allow", "30 13 * * *", "6h30m", "another-long-one*"),
				{Kind: "deny", Schedule: "0 0 * * 0", Duration: "48h"},
			}
			m := windowModel(t,
				&argocd.AppSyncWindows{AssignedWindows: ws, CanSync: false},
				ws,
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
	three := []argocd.SyncWindow{
		win("allow", "a", "1h"), win("deny", "b", "2h"), win("allow", "c", "3h"),
	}
	m := windowModel(t,
		&argocd.AppSyncWindows{AssignedWindows: three, CanSync: true},
		three,
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

	// Argo CD's own UI links to the windows tab from an application's
	// SyncWindow badge; landing on the project overview instead leaves the
	// reader one click from what they were looking at.
	url := m.projectWindowsURL(m.app)
	if !strings.Contains(url, "/settings/projects/") {
		t.Errorf("url = %q, want the project settings page", url)
	}
	if !strings.Contains(url, m.app.Spec.Project) {
		t.Errorf("url = %q, want it to name the project", url)
	}
	if !strings.Contains(url, "tab=windows") {
		t.Errorf("url = %q, want it to land on the windows tab", url)
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

// The two payloads come from separate calls, and these windows are edited by
// automation — a window present in one and not the other is not hypothetical.
// When the project's copy is missing, its selectors and zone are unknown, and
// saying so beats stating a default that would be wrong.
func TestMissingProjectCopyIsMarkedNotGuessed(t *testing.T) {
	// The per-application payload drops the selectors and the zone.
	assigned := argocd.SyncWindow{Kind: "allow", Schedule: "1 15 * * *", Duration: "5h"}

	m := windowModel(t,
		&argocd.AppSyncWindows{
			AssignedWindows: []argocd.SyncWindow{assigned},
			CanSync:         true,
		},
		// The project's list no longer contains it.
		[]argocd.SyncWindow{win("deny", "0 3 * * *", "24h", "something-else*")},
	)
	m.screen = screenWindows

	rows := m.windowRows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the assigned window", len(rows))
	}
	if rows[0].detailed {
		t.Error("the row claims it has the project's detail when it does not")
	}

	out := m.View()
	// "the whole project" is what an empty selector set legitimately means, so
	// rendering it here would state the opposite of the truth.
	if strings.Contains(out, "the whole project") {
		t.Errorf("a window with unknown selectors was rendered as covering everything:\n%s", out)
	}
	if !strings.Contains(out, "unavailable") {
		t.Errorf("the missing detail should be marked:\n%s", out)
	}
	// An Asia/Seoul window read as UTC is off by nine hours — the difference
	// between "open now" and "opens tonight".
	if strings.Contains(out, "UTC") {
		t.Errorf("an unknown zone was rendered as UTC:\n%s", out)
	}
}

// When the project's copy is present, the selectors and zone come from it.
func TestPresentProjectCopySuppliesTheDetail(t *testing.T) {
	assigned := argocd.SyncWindow{Kind: "allow", Schedule: "1 15 * * *", Duration: "5h"}
	full := win("allow", "1 15 * * *", "5h", "web-prod*")

	m := windowModel(t,
		&argocd.AppSyncWindows{
			AssignedWindows: []argocd.SyncWindow{assigned},
			CanSync:         true,
		},
		[]argocd.SyncWindow{full},
	)
	m.screen = screenWindows

	if !m.windowRows()[0].detailed {
		t.Fatal("the project's copy should have supplied the detail")
	}
	out := m.View()
	if !strings.Contains(out, "web-prod*") {
		t.Errorf("the selectors are missing:\n%s", out)
	}
	if !strings.Contains(out, "Asia/Seoul") {
		t.Errorf("the zone is missing:\n%s", out)
	}
}

// The placeholder shown when nothing applies explains why, and an explanation
// cut off at the terminal edge explains nothing.
func TestEmptyPlaceholderWraps(t *testing.T) {
	for _, w := range []int{140, 120, 100, 80, 60} {
		m := windowModel(t,
			&argocd.AppSyncWindows{CanSync: true},
			[]argocd.SyncWindow{win("deny", "0 3 * * *", "24h", "other*")},
		)
		m.screen = screenWindows
		m.Update(tea.WindowSizeMsg{Width: w, Height: 16})

		out := m.View()
		for i, line := range strings.Split(out, "\n") {
			if got := lipglossWidth(line); got > w {
				t.Errorf("w=%d line %d is %d cells:\n%q", w, i, got, line)
			}
		}
		// The whole sentence must survive, wrapped rather than truncated.
		flat := strings.Join(strings.Fields(stripANSI(out)), " ")
		if !strings.Contains(flat, "the project has 1, none matching") {
			t.Errorf("w=%d: the explanation was cut off:\n%s", w, out)
		}
	}
}
