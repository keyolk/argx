package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

// appModel builds a model already inside the application view, with one
// application whose spec the tests exercise.
func appModel(t *testing.T, mutate func(*argocd.Application)) *Model {
	t.Helper()
	m := newTestModel(t, "web-frontend")

	var a argocd.Application
	a.Context = "test"
	a.Metadata.Name = "web-frontend"
	a.Metadata.Namespace = "argocd"
	a.Spec.Project = "default"
	a.Spec.Source = &argocd.Source{
		RepoURL: "git@github.com:org/repo", Path: "charts/web", TargetRevision: "main",
	}
	a.Spec.Destination = argocd.Destination{Name: "prod-apne2", Namespace: "web"}
	a.Status.Sync.Status = "Synced"
	a.Status.Health.Status = "Healthy"
	if mutate != nil {
		mutate(&a)
	}

	m.app = &a
	m.screen = screenApp
	m.tab = tabResources
	return m
}

func withHistory(a *argocd.Application) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	a.Status.History = []argocd.RevisionHistory{
		{ID: 1, Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DeployedAt: now.Add(-2 * time.Hour)},
		{ID: 2, Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", DeployedAt: now.Add(-time.Hour)},
		{ID: 3, Revision: "cccccccccccccccccccccccccccccccccccccccc", DeployedAt: now},
	}
}

func withAutoSync(prune, selfHeal bool) func(*argocd.Application) {
	return func(a *argocd.Application) {
		a.Spec.SyncPolicy = &argocd.SyncPolicy{
			Automated: &argocd.AutomatedSync{Prune: prune, SelfHeal: selfHeal},
		}
	}
}

// ---- tab switching ----

func TestTabsCycleAndSelect(t *testing.T) {
	m := appModel(t, nil)

	press(t, m, "]")
	if m.tab != tabHistory {
		t.Errorf("] should advance to HISTORY, got %v", m.tab)
	}
	press(t, m, "]")
	if m.tab != tabDetails {
		t.Errorf("] should advance to DETAILS, got %v", m.tab)
	}
	press(t, m, "]")
	if m.tab != tabResources {
		t.Errorf("] should wrap to RESOURCES, got %v", m.tab)
	}
	press(t, m, "[")
	if m.tab != tabDetails {
		t.Errorf("[ should wrap backwards to DETAILS, got %v", m.tab)
	}

	for i, want := range []tab{tabResources, tabHistory, tabDetails} {
		press(t, m, string(rune('1'+i)))
		if m.tab != want {
			t.Errorf("%d should select %v, got %v", i+1, want, m.tab)
		}
	}
}

// Switching tabs must not unwind the screen stack: Esc from any tab returns to
// the application list, not to a previous tab.
func TestTabSwitchDoesNotTouchTheScreenStack(t *testing.T) {
	m := newTestModel(t, "alpha")
	press(t, m, "enter") // into the application view
	m.app = &m.apps[0]

	press(t, m, "]", "]", "[")
	if m.screen != screenApp {
		t.Fatalf("tab keys changed the screen to %v", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenApps {
		t.Errorf("esc should return to the application list, got %v", m.screen)
	}
}

// Each tab keeps its own cursor, so coming back lands where you left.
func TestTabsKeepTheirOwnCursor(t *testing.T) {
	m := appModel(t, withHistory)
	m.tree = []argocd.TreeRow{
		{Node: fnode("Pod", "a", "web", "Healthy")},
		{Node: fnode("Pod", "b", "web", "Healthy")},
		{Node: fnode("Pod", "c", "web", "Healthy")},
	}
	m.applyTreeFilter()

	press(t, m, "j", "j") // resources cursor to row 2
	press(t, m, "2", "j") // history cursor to row 1
	press(t, m, "1")

	if m.treeCur != 2 {
		t.Errorf("the RESOURCES cursor moved to %d while away", m.treeCur)
	}
	press(t, m, "2")
	if m.histCur != 1 {
		t.Errorf("the HISTORY cursor moved to %d while away", m.histCur)
	}
}

// A filter typed on one tab means something different on another, so it must
// not follow the reader across.
func TestTabSwitchClosesTheFilterPrompt(t *testing.T) {
	m := appModel(t, withHistory)
	press(t, m, "/", "p", "o", "d")
	if !m.filtering {
		t.Fatal("/ should open the filter prompt")
	}
	press(t, m, "]")
	if m.filtering {
		t.Error("switching tabs should close the filter prompt")
	}
}

// ---- history ----

// History is stored oldest-first and read newest-first.
func TestHistoryIsNewestFirst(t *testing.T) {
	m := appModel(t, withHistory)
	rows := m.histRows()
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].ID != 3 || rows[2].ID != 1 {
		t.Errorf("order = %d,%d,%d, want 3,2,1", rows[0].ID, rows[1].ID, rows[2].ID)
	}
}

func TestHistoryNavigation(t *testing.T) {
	m := appModel(t, withHistory)
	press(t, m, "2")

	press(t, m, "j")
	if m.histCur != 1 {
		t.Errorf("j moved to %d, want 1", m.histCur)
	}
	press(t, m, "G")
	if m.histCur != 2 {
		t.Errorf("G moved to %d, want the last row (2)", m.histCur)
	}
	press(t, m, "g")
	if m.histCur != 0 {
		t.Errorf("g moved to %d, want 0", m.histCur)
	}
	// Past either end the cursor must clamp, not wrap or go negative.
	press(t, m, "k", "k")
	if m.histCur != 0 {
		t.Errorf("k past the top moved to %d, want 0", m.histCur)
	}
}

// Rollback re-syncs a cluster, so it always confirms.
func TestRollbackRequiresConfirmation(t *testing.T) {
	m := appModel(t, withHistory)
	press(t, m, "2", "j") // HISTORY, second-newest

	press(t, m, "enter")
	if m.overlay != overlayConfirm {
		t.Fatalf("enter on a history row should confirm, overlay = %v", m.overlay)
	}
	body := strings.Join(m.confirm.body, "\n")
	if !strings.Contains(body, "id 2") {
		t.Errorf("the prompt should name the target history id:\n%s", body)
	}
}

// Argo CD refuses a rollback while auto-sync is on; saying so up front beats
// letting the user confirm just to receive an error.
func TestRollbackWarnsWhenAutoSyncIsOn(t *testing.T) {
	m := appModel(t, func(a *argocd.Application) {
		withHistory(a)
		withAutoSync(false, false)(a)
	})
	press(t, m, "2", "enter")

	body := strings.Join(m.confirm.body, "\n")
	if !strings.Contains(body, "auto-sync is ON") {
		t.Errorf("the prompt should warn that Argo CD will refuse it:\n%s", body)
	}
}

// ---- details ----

func TestDetailsCursorSkipsSectionHeadings(t *testing.T) {
	m := appModel(t, nil)
	press(t, m, "3")

	rows := m.detailRows()
	for i := 0; i < len(rows); i++ {
		if rows[m.detailCur].kind == detailSection {
			t.Fatalf("the cursor landed on a section heading at row %d", m.detailCur)
		}
		press(t, m, "j")
	}
}

// Turning auto-sync ON can move a cluster on its own, so it confirms; turning
// it off is how you stop that, and must not be slowed down.
func TestAutoSyncConfirmsOnlyWhenEnabling(t *testing.T) {
	off := appModel(t, nil)
	press(t, off, "3")
	off.detailCur = detailRowIndex(t, off, detailAutoSync)
	press(t, off, "enter")
	if off.overlay != overlayConfirm {
		t.Errorf("enabling auto-sync should confirm, overlay = %v", off.overlay)
	}

	on := appModel(t, withAutoSync(false, false))
	press(t, on, "3")
	on.detailCur = detailRowIndex(t, on, detailAutoSync)
	cmd := press(t, on, "enter")
	if on.overlay != overlayNone {
		t.Errorf("disabling auto-sync should not confirm, overlay = %v", on.overlay)
	}
	if cmd == nil {
		t.Error("disabling auto-sync should issue the patch")
	}
}

// Prune deletes resources, so enabling it confirms even though the surrounding
// auto-sync toggle is already on.
func TestPruneConfirmsWhenEnabling(t *testing.T) {
	m := appModel(t, withAutoSync(false, false))
	press(t, m, "3")
	m.detailCur = detailRowIndex(t, m, detailAutoPrune)

	press(t, m, "enter")
	if m.overlay != overlayConfirm {
		t.Fatalf("enabling prune should confirm, overlay = %v", m.overlay)
	}
	if !strings.Contains(strings.Join(m.confirm.body, " "), "DELETE") {
		t.Errorf("the prompt should say what prune does:\n%v", m.confirm.body)
	}
}

// The prune and self-heal flags live inside an automated policy; offering them
// while auto-sync is off would silently create one.
func TestPruneIsUnavailableWhileAutoSyncIsOff(t *testing.T) {
	m := appModel(t, nil)
	press(t, m, "3")
	m.detailCur = detailRowIndex(t, m, detailAutoPrune)

	cmd := press(t, m, "enter")
	if m.overlay != overlayNone || cmd != nil {
		t.Error("prune must do nothing while auto-sync is off")
	}
	if !strings.Contains(m.toast, "auto-sync is off") {
		t.Errorf("the reason should be shown, toast = %q", m.toast)
	}
}

// A merge patch cannot address one element of the sources array, so argx
// declines rather than rewriting the wrong source.
func TestMultiSourceRevisionIsNotEditable(t *testing.T) {
	m := appModel(t, func(a *argocd.Application) {
		a.Spec.Source = nil
		a.Spec.Sources = []argocd.Source{
			{RepoURL: "git@github.com:org/a", TargetRevision: "main"},
			{RepoURL: "git@github.com:org/b", TargetRevision: "v1"},
		}
	})
	press(t, m, "3")
	m.detailCur = detailRowIndex(t, m, detailRevision)

	rows := m.detailRows()
	if rows[m.detailCur].actionable() {
		t.Error("a multi-source application's revision must not be editable")
	}
	cmd := press(t, m, "enter")
	if m.overlay != overlayNone || cmd != nil {
		t.Error("enter on a multi-source revision row must do nothing")
	}
}

// Terminate is only meaningful while a sync is running.
func TestTerminateOnlyWhileRunning(t *testing.T) {
	idle := appModel(t, func(a *argocd.Application) {
		a.Status.OperationState = &argocd.OperationState{Phase: "Succeeded"}
	})
	press(t, idle, "3")
	idle.detailCur = detailRowIndex(t, idle, detailTerminate)
	if idle.detailRows()[idle.detailCur].actionable() {
		t.Error("terminate must not be offered when no sync is running")
	}

	running := appModel(t, func(a *argocd.Application) {
		a.Status.OperationState = &argocd.OperationState{Phase: "Running"}
	})
	press(t, running, "3")
	running.detailCur = detailRowIndex(t, running, detailTerminate)
	press(t, running, "enter")
	if running.overlay != overlayConfirm {
		t.Errorf("terminate should confirm, overlay = %v", running.overlay)
	}
}

// ---- revision picker ----

func TestRevisionPickerFiltersAndSelects(t *testing.T) {
	m := appModel(t, nil)
	press(t, m, "3")
	m.detailCur = detailRowIndex(t, m, detailRevision)
	press(t, m, "enter")

	if m.overlay != overlayRevPicker {
		t.Fatalf("enter on the revision row should open the picker, overlay = %v", m.overlay)
	}

	m.Update(refsMsg{items: []revItem{
		{name: "main", kind: "branch"},
		{name: "feature/tls", kind: "branch"},
		{name: "v1.2.3", kind: "tag"},
	}})
	if len(m.revPicker.rows) != 3 {
		t.Fatalf("the picker shows %d refs, want 3", len(m.revPicker.rows))
	}

	// Typing filters rather than navigating: the picker exists to narrow a long
	// branch list.
	press(t, m, "t", "l", "s")
	if len(m.revPicker.rows) != 1 {
		t.Fatalf("typing should narrow to 1 ref, got %d", len(m.revPicker.rows))
	}
	got, ok := m.currentRev()
	if !ok || got.name != "feature/tls" {
		t.Fatalf("the picker selected %v, want feature/tls", got)
	}

	press(t, m, "enter")
	if m.overlay != overlayConfirm {
		t.Fatalf("selecting a revision should confirm, overlay = %v", m.overlay)
	}
	body := strings.Join(m.confirm.body, "\n")
	if !strings.Contains(body, "main") || !strings.Contains(body, "feature/tls") {
		t.Errorf("the prompt should show both the old and new revision:\n%s", body)
	}
}

// Pointing a live, auto-syncing application at a branch deploys that branch —
// the reader must be told before confirming, not after.
func TestRevisionChangeWarnsWhenAutoSyncIsOn(t *testing.T) {
	m := appModel(t, withAutoSync(false, true))
	press(t, m, "3")
	m.detailCur = detailRowIndex(t, m, detailRevision)
	press(t, m, "enter")
	m.Update(refsMsg{items: []revItem{{name: "feature/x", kind: "branch"}}})
	press(t, m, "enter")

	body := strings.Join(m.confirm.body, "\n")
	if !strings.Contains(body, "auto-sync is ON") {
		t.Errorf("the prompt should warn that this deploys immediately:\n%s", body)
	}
}

// A branch and a tag can share a name; which one Argo CD resolves is not a
// detail to leave implicit.
func TestRevisionPickerLabelsRefKind(t *testing.T) {
	m := appModel(t, nil)
	press(t, m, "3")
	m.detailCur = detailRowIndex(t, m, detailRevision)
	press(t, m, "enter")
	m.Update(refsMsg{items: []revItem{
		{name: "release", kind: "branch"},
		{name: "release", kind: "tag"},
	}})

	out := m.View()
	if !strings.Contains(out, "branch") || !strings.Contains(out, "tag") {
		t.Errorf("the picker must label each ref's kind:\n%s", out)
	}
}

func TestRevisionPickerEscapeCancels(t *testing.T) {
	m := appModel(t, nil)
	press(t, m, "3")
	m.detailCur = detailRowIndex(t, m, detailRevision)
	press(t, m, "enter")

	press(t, m, "esc")
	if m.overlay != overlayNone {
		t.Errorf("esc should close the picker, overlay = %v", m.overlay)
	}
}

// ---- server responses ----

// The view must update from the server's response, not from what argx asked
// for: a normalized or partially-rejected patch would otherwise be reported as
// a change that did not happen.
func TestSpecResponseUpdatesFromTheServer(t *testing.T) {
	m := appModel(t, nil)

	var returned argocd.Application
	returned.Metadata.Name = "web-frontend"
	returned.Spec.Source = &argocd.Source{TargetRevision: "normalized-by-server"}

	m.Update(specMsg{app: &returned, text: "target revision → whatever"})

	if got := m.app.TargetRevision(); got != "normalized-by-server" {
		t.Errorf("the view shows %q — it should reflect the server's response", got)
	}
}

func TestSpecErrorSurfaces(t *testing.T) {
	m := appModel(t, nil)
	m.Update(specMsg{err: errString("permission denied")})

	if m.overlay != overlayError {
		t.Fatalf("a failed patch should surface, overlay = %v", m.overlay)
	}
	if !strings.Contains(m.errMsg, "permission denied") {
		t.Errorf("the error text is lost: %q", m.errMsg)
	}
}

// detailRowIndex finds the row of a given kind so the tests do not hardcode
// positions that shift whenever a field is added.
func detailRowIndex(t *testing.T, m *Model, kind detailKind) int {
	t.Helper()
	for i, r := range m.detailRows() {
		if r.kind == kind {
			return i
		}
	}
	t.Fatalf("no detail row of kind %v", kind)
	return 0
}

type errString string

func (e errString) Error() string { return string(e) }

// ---- rendering ----

// Every tab must fit the terminal at every size argx supports.
func TestTabsRenderWithinWidth(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {100, 24}, {80, 24}, {60, 14}} {
		w, h := size[0], size[1]
		m := appModel(t, func(a *argocd.Application) {
			withHistory(a)
			withAutoSync(true, true)(a)
			a.Status.OperationState = &argocd.OperationState{
				Phase: "Running", Message: strings.Repeat("failure detail ", 30),
			}
		})
		m.Update(tea.WindowSizeMsg{Width: w, Height: h})

		for _, tb := range allTabs {
			m.tab = tb
			for i, line := range strings.Split(m.View(), "\n") {
				if got := lipglossWidth(line); got > w {
					t.Errorf("%dx%d %v line %d is %d cells, want at most %d:\n%q",
						w, h, tb, i, got, w, line)
				}
			}
		}
	}
}

// The tab bar's width must not depend on which tab is active, or everything
// beside it shifts on every switch.
func TestTabBarWidthIsConstant(t *testing.T) {
	m := appModel(t, nil)
	widths := make([]int, 0, len(allTabs))
	for _, tb := range allTabs {
		m.tab = tb
		widths = append(widths, lipglossWidth(m.renderTabBar()))
	}
	for i := 1; i < len(widths); i++ {
		if widths[i] != widths[0] {
			t.Fatalf("the tab bar is %v cells wide across tabs — switching shifts the header", widths)
		}
	}
}

// The application name must stay visible on every tab: it identifies what the
// spec edits are about to change.
func TestApplicationNameIsVisibleOnEveryTab(t *testing.T) {
	m := appModel(t, withHistory)
	for _, tb := range allTabs {
		m.tab = tb
		if !strings.Contains(m.View(), "web-frontend") {
			t.Errorf("%v does not show the application name:\n%s", tb, m.View())
		}
	}
}

// Editable rows are marked in the row itself, so what argx can change is
// visible without moving the cursor over everything.
func TestDetailsMarksEditableRows(t *testing.T) {
	m := appModel(t, withAutoSync(true, true))
	m.tab = tabDetails

	out := m.View()
	if !strings.Contains(out, "* auto-sync") {
		t.Errorf("editable rows should carry a marker:\n%s", out)
	}
	if !strings.Contains(out, "rows are editable") {
		t.Errorf("the status line should explain the marker:\n%s", out)
	}
}

// The newest history entry is what is deployed now; saying so beats making the
// reader infer it from the ordering.
func TestHistoryLabelsTheCurrentDeployment(t *testing.T) {
	m := appModel(t, withHistory)
	m.tab = tabHistory

	if !strings.Contains(m.View(), "current") {
		t.Errorf("the newest deployment should be labelled:\n%s", m.View())
	}
}

// The filter prompt advertises the field prefixes, so they are discoverable
// without opening help.
func TestResourceFilterPromptShowsFieldHints(t *testing.T) {
	m := appModel(t, nil)
	press(t, m, "/")

	if !strings.Contains(m.View(), "kind:") {
		t.Errorf("the filter prompt should show the available fields:\n%s", m.View())
	}
}
