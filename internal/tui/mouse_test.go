package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
)

// at finds where View drew text, so a click lands on the cell the reader sees
// rather than on a coordinate the test computed with the code's own arithmetic.
func at(t *testing.T, m *Model, text string) (x, y int) {
	t.Helper()
	for i, line := range strings.Split(m.View(), "\n") {
		plain := stripANSI(line)
		if j := strings.Index(plain, text); j >= 0 {
			return lipgloss.Width(plain[:j]), i
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, stripANSI(m.View()))
	return 0, 0
}

func mouseMsg(x, y int, b tea.MouseButton, a tea.MouseAction) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: b, Action: a}
}

// click sends a left press through Update and returns the command it issued.
// The command is never run: it would talk to Argo CD.
func click(m *Model, x, y int) tea.Cmd {
	_, cmd := m.Update(mouseMsg(x, y, tea.MouseButtonLeft, tea.MouseActionPress))
	return cmd
}

func wheel(m *Model, x, y int, b tea.MouseButton) {
	m.Update(mouseMsg(x, y, b, tea.MouseActionPress))
}

// mouseApps is the application list with n applications named svc-01..svc-nn.
func mouseApps(t *testing.T, n, w, h int) *Model {
	t.Helper()
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("svc-%02d", i+1)
	}
	m := newTestModel(t, names...)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	t0 := time.Unix(0, 0)
	m.now = func() time.Time { return t0 }
	return m
}

func TestClickSelectsTheRowUnderThePointer(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	x, y := at(t, m, "svc-03")
	click(m, x, y)
	if got := m.currentApp().Name(); got != "svc-03" {
		t.Fatalf("selected %q, want svc-03", got)
	}
	if m.screen != screenApps {
		t.Fatal("one click selects; it must not open the application")
	}
}

func TestSecondClickOnTheSelectedRowOpensIt(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	x, y := at(t, m, "svc-02")
	if cmd := click(m, x, y); cmd != nil {
		t.Fatal("the first click must not issue any command")
	}
	cmd := click(m, x, y)
	if m.screen != screenApp || m.currentApp().Name() != "svc-02" {
		t.Fatalf("screen=%v: the second click should open svc-02 as Enter does", m.screen)
	}
	if cmd == nil {
		t.Fatal("opening loads the tree, as Enter does")
	}
}

func TestTwoClicksOnDifferentRowsDoNotOpen(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	x1, y1 := at(t, m, "svc-01")
	x2, y2 := at(t, m, "svc-02")
	click(m, x1, y1)
	click(m, x2, y2)
	if m.screen != screenApps || m.currentApp().Name() != "svc-02" {
		t.Fatalf("screen=%v sel=%s", m.screen, m.currentApp().Name())
	}
}

// A key between two clicks breaks the pair. Click a row, j, k back to it,
// click again inside the double-click window: that is a fresh first click,
// so it selects and must not open the application.
func TestKeyBetweenClicksBreaksTheDoubleClick(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	x, y := at(t, m, "svc-02")
	click(m, x, y)
	m.Update(key("j"))
	m.Update(key("k"))
	if got := m.currentApp().Name(); got != "svc-02" {
		t.Fatalf("selected %q after j, k; want back on svc-02", got)
	}
	if cmd := click(m, x, y); cmd != nil {
		t.Fatal("a key between the clicks must break the double click; a command was issued")
	}
	if m.screen != screenApps {
		t.Fatalf("screen=%v: a key between the clicks must break the double click", m.screen)
	}
	if got := m.currentApp().Name(); got != "svc-02" {
		t.Fatalf("selected %q, want svc-02 still selected", got)
	}
}

func TestASlowSecondClickIsNotADoubleClick(t *testing.T) {
	m := mouseApps(t, 3, 120, 30)
	x, y := at(t, m, "svc-02")
	click(m, x, y)
	later := time.Unix(10, 0)
	m.now = func() time.Time { return later }
	click(m, x, y)
	if m.screen != screenApps {
		t.Fatal("two clicks ten seconds apart are two selections")
	}
}

func TestClickOnAScrolledListHitsTheVisibleRow(t *testing.T) {
	m := mouseApps(t, 40, 120, 14)
	press(t, m, "G")
	if m.appTop == 0 {
		t.Fatal("setup: the list should be scrolled")
	}
	x, y := at(t, m, "svc-35")
	click(m, x, y)
	if got := m.currentApp().Name(); got != "svc-35" {
		t.Fatalf("selected %q, want svc-35: the scroll offset must count", got)
	}
}

func TestNarrowLayoutStillHitsRows(t *testing.T) {
	m := mouseApps(t, 6, minWidth, minHeight)
	x, y := at(t, m, "svc-04")
	click(m, x, y)
	if got := m.currentApp().Name(); got != "svc-04" {
		t.Fatalf("selected %q, want svc-04", got)
	}
}

func TestWheelMovesThreeRows(t *testing.T) {
	m := mouseApps(t, 20, 120, 30)
	x, y := at(t, m, "svc-01")
	wheel(m, x, y, tea.MouseButtonWheelDown)
	if m.appCur != 3 {
		t.Fatalf("appCur=%d, want 3", m.appCur)
	}
	wheel(m, x, y, tea.MouseButtonWheelUp)
	if m.appCur != 0 {
		t.Fatalf("appCur=%d, want 0", m.appCur)
	}
}

func TestWheelOnThePagerScrollsThreeLines(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	m := pagerModel(t, lines)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	wheel(m, 5, 5, tea.MouseButtonWheelDown)
	if m.pagerTop != 3 {
		t.Fatalf("pagerTop=%d, want 3", m.pagerTop)
	}
}

func TestClicksOnNothingDoNothing(t *testing.T) {
	m := mouseApps(t, 2, 120, 30)
	x, y := at(t, m, "svc-01")
	click(m, x, y)

	// The column header, the empty rows below the last application, the status
	// and footer lines.
	for _, yy := range []int{1, y + 5, m.height - 2, m.height - 1} {
		click(m, 3, yy)
		if m.appCur != 0 || m.screen != screenApps || m.overlay != overlayNone {
			t.Fatalf("click at y=%d changed state: cur=%d screen=%v", yy, m.appCur, m.screen)
		}
	}
	// An empty-area click breaks a double click in progress.
	click(m, x, y)
	click(m, 3, y+5)
	click(m, x, y)
	if m.screen != screenApps {
		t.Fatal("a click on nothing between two clicks must break the double")
	}
}

func TestReleaseMotionDragAndOtherButtonsAreIgnored(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	x, y := at(t, m, "svc-03")
	for _, msg := range []tea.MouseMsg{
		mouseMsg(x, y, tea.MouseButtonLeft, tea.MouseActionRelease),
		mouseMsg(x, y, tea.MouseButtonNone, tea.MouseActionMotion),
		mouseMsg(x, y, tea.MouseButtonLeft, tea.MouseActionMotion),
		mouseMsg(x, y, tea.MouseButtonRight, tea.MouseActionPress),
		mouseMsg(x, y, tea.MouseButtonMiddle, tea.MouseActionPress),
	} {
		m.Update(msg)
		if m.appCur != 0 || m.screen != screenApps {
			t.Fatalf("%+v moved the cursor to %d", msg, m.appCur)
		}
	}
}

func TestTheFilterPromptIgnoresTheMouse(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	press(t, m, "/")
	if !m.filtering {
		t.Fatal("/ opens the filter prompt")
	}
	x, y := at(t, m, "svc-03")
	click(m, x, y)
	wheel(m, x, y, tea.MouseButtonWheelDown)
	if m.appCur != 0 || !m.filtering {
		t.Fatalf("appCur=%d filtering=%v: the prompt owns input", m.appCur, m.filtering)
	}
}

// ---- the resource tree ----

func mouseTree(t *testing.T) *Model {
	t.Helper()
	m := appModel(t, nil)
	var nodes []argocd.Node
	for i, n := range []string{"web-one", "web-two", "web-three"} {
		nodes = append(nodes, argocd.Node{ResourceRef: argocd.ResourceRef{
			UID: fmt.Sprintf("u%d", i), Kind: "Pod", Name: n, Namespace: "web",
		}})
	}
	tree := &argocd.Tree{Nodes: nodes}
	m.Update(treeMsg{id: m.treeID, app: m.app, rows: tree.Flatten("argocd", "test")})
	m.screen, m.tab = screenApp, tabResources
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	t0 := time.Unix(0, 0)
	m.now = func() time.Time { return t0 }
	return m
}

func TestTreeSecondClickOpensTheManifestLikeEnter(t *testing.T) {
	m := mouseTree(t)
	x, y := at(t, m, "web-two")
	click(m, x, y)
	if m.currentNode().Name != "web-two" || m.screen != screenApp {
		t.Fatalf("one click selects only: node=%s screen=%v", m.currentNode().Name, m.screen)
	}
	click(m, x, y)
	if m.screen != screenManifest {
		t.Fatalf("screen=%v, want the manifest", m.screen)
	}
}

func TestTreeGraphViewIgnoresClicks(t *testing.T) {
	m := mouseTree(t)
	m.treeGraph = true
	x, y := at(t, m, "web-three")
	click(m, x, y)
	click(m, x, y)
	if m.treeCur != 0 || m.screen != screenApp {
		t.Fatalf("treeCur=%d screen=%v: graph lines do not map onto rows", m.treeCur, m.screen)
	}
}

// ---- screens whose Enter writes ----

func TestHistoryClickSelectsButNeverArmsARollback(t *testing.T) {
	m := appModel(t, withHistory)
	m.tab = tabHistory
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	t0 := time.Unix(0, 0)
	m.now = func() time.Time { return t0 }

	x, y := at(t, m, "bbbbbbb")
	click(m, x, y)
	if m.histCur != 1 {
		t.Fatalf("histCur=%d, want 1", m.histCur)
	}
	// Checked after every click: a later outside click would close a dialog
	// the earlier one had wrongly opened.
	for i := range 3 {
		click(m, x, y)
		if m.overlay != overlayNone || m.confirm.action != nil {
			t.Fatalf("click %d armed a rollback; that stays on enter/b", i+2)
		}
	}
}

func TestDetailsClickSelectsButNeverEdits(t *testing.T) {
	m := appModel(t, withAutoSync(false, false))
	m.tab = tabDetails
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	t0 := time.Unix(0, 0)
	m.now = func() time.Time { return t0 }

	rows := m.detailRows()
	want := -1
	for i, r := range rows {
		if r.kind != detailSection && i != m.detailCur && r.label != "" {
			want = i
			break
		}
	}
	if want < 0 {
		t.Fatal("setup: no second selectable row")
	}
	x, y := at(t, m, rows[want].label)
	click(m, x, y)
	if m.detailCur != want {
		t.Fatalf("detailCur=%d, want %d", m.detailCur, want)
	}
	for i := range 3 {
		click(m, x, y)
		if m.overlay != overlayNone || m.screen != screenApp {
			t.Fatalf("click %d: overlay=%v screen=%v: a click must not edit", i+2, m.overlay, m.screen)
		}
	}
}

func TestScheduleClickHitsTwoLineRows(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.schedules = []scheduled{sched(1, "job-one", time.Hour), sched(2, "job-two", 2*time.Hour)}
	m.push(screenSchedule)

	x2, y2 := at(t, m, "job-two")
	click(m, x2, y2)
	if m.scheduleCur != 1 {
		t.Fatalf("scheduleCur=%d, want 1", m.scheduleCur)
	}
	// Each row has a second line saying what it waits for. A click on it
	// belongs to its own row; a one-line-per-row count would give it to the next.
	x1, y1 := at(t, m, "job-one")
	click(m, x1, y1+1)
	if m.scheduleCur != 0 {
		t.Fatalf("scheduleCur=%d after clicking job-one's second line, want 0", m.scheduleCur)
	}
	for range 3 {
		click(m, x1, y1)
		if len(m.schedules) != 2 || m.schedules[0].state != scheduleWaiting || m.overlay != overlayNone {
			t.Fatal("clicking a scheduled sync must not cancel or run it")
		}
	}
}

// ---- contexts and application sets ----

func TestContextSecondClickOpensDetails(t *testing.T) {
	m := ctxModel(t, ctxRow("prod-a", nil), ctxRow("prod-b", nil))
	t0 := time.Unix(0, 0)
	m.now = func() time.Time { return t0 }
	x, y := at(t, m, "prod-b")
	click(m, x, y)
	if m.ctxCur != 1 || m.ctxDetail {
		t.Fatalf("ctxCur=%d ctxDetail=%v after one click", m.ctxCur, m.ctxDetail)
	}
	click(m, x, y)
	if !m.ctxDetail {
		t.Fatal("the second click should open the details, as enter does")
	}
}

func TestAppSetSecondClickDoesWhatEnterDoes(t *testing.T) {
	sets := []argocd.ApplicationSet{
		appset("sb-prod", "gen-one", "default", gitGen()),
		appset("sb-prod", "gen-two", "default", clusterGen()),
	}
	byMouse := setModel(t, sets...)
	t0 := time.Unix(0, 0)
	byMouse.now = func() time.Time { return t0 }
	x, y := at(t, byMouse, "gen-two")
	click(byMouse, x, y)
	if byMouse.appsetCur != 1 || byMouse.screen != screenAppSets {
		t.Fatalf("appsetCur=%d screen=%v after one click", byMouse.appsetCur, byMouse.screen)
	}
	click(byMouse, x, y)

	byKey := setModel(t, sets...)
	byKey.appsetCur = 1
	press(t, byKey, "enter")
	if byMouse.screen != byKey.screen || byMouse.toastText() != byKey.toastText() {
		t.Fatalf("mouse: screen=%v toast=%q; enter: screen=%v toast=%q",
			byMouse.screen, byMouse.toastText(), byKey.screen, byKey.toastText())
	}
}

// ---- overlays ----

func TestConfirmOwnsTheMouseAndOutsideAnswersNo(t *testing.T) {
	m := mouseApps(t, 5, 120, 30)
	ran := false
	m.confirm = confirmState{
		title: "Sync svc-01?", body: []string{"1 application"},
		action: func() tea.Cmd { ran = true; return nil },
	}
	m.overlay = overlayConfirm
	before := m.appCur

	// Inside the box.
	x, y := at(t, m, "Sync svc-01?")
	click(m, x, y)
	if m.overlay != overlayConfirm || m.appCur != before || ran {
		t.Fatal("a click inside the dialog must do nothing")
	}

	// Even with the cursor on Yes, an outside click is No.
	m.overlay, m.confirm.yes = overlayConfirm, true
	click(m, 0, 2)
	if m.overlay != overlayNone || ran {
		t.Fatalf("overlay=%v ran=%v: outside must close as No and never confirm", m.overlay, ran)
	}
	if m.appCur != before || m.screen != screenApps {
		t.Fatal("the closing click must not reach the list")
	}
}

func TestSyncOptionsWheelMovesTheCursorAndOutsideCancels(t *testing.T) {
	m := mouseApps(t, 3, 120, 30)
	press(t, m, "s")
	if m.overlay != overlaySyncOpts {
		t.Fatalf("s should open the sync options, overlay=%v", m.overlay)
	}
	wheel(m, 60, 15, tea.MouseButtonWheelDown)
	if m.syncOpts.cur != 2 || m.appCur != 0 {
		t.Fatalf("cur=%d appCur=%d: the wheel moves the modal's cursor, not the list's", m.syncOpts.cur, m.appCur)
	}
	// A click on an option must not flip it: prune stays on its key.
	x, y := at(t, m, "prune")
	click(m, x, y)
	if m.syncOpts.prune || m.overlay != overlaySyncOpts {
		t.Fatal("clicking an option row must not toggle prune")
	}
	click(m, 0, 2)
	if m.overlay != overlayNone || m.confirm.action != nil {
		t.Fatalf("overlay=%v: outside cancels without arming a confirmation", m.overlay)
	}
}

func TestErrorOverlayClosesOnOutsideClickOnly(t *testing.T) {
	m := mouseApps(t, 3, 120, 30)
	m.showError(errors.New("boom happened"))
	x, y := at(t, m, "boom happened")
	click(m, x, y)
	if m.overlay != overlayError {
		t.Fatal("a click inside the message keeps it open")
	}
	click(m, 0, 2)
	if m.overlay != overlayNone {
		t.Fatal("a click outside dismisses it")
	}
}

func TestPaletteWheelMovesAndOutsideCloses(t *testing.T) {
	m := mouseApps(t, 3, 120, 30)
	press(t, m, ":")
	if m.overlay != overlayPalette {
		t.Fatalf("overlay=%v, want the palette", m.overlay)
	}
	wheel(m, 60, 15, tea.MouseButtonWheelDown)
	if m.palette.cur != 3 || m.appCur != 0 {
		t.Fatalf("palette.cur=%d appCur=%d", m.palette.cur, m.appCur)
	}
	click(m, 0, 2)
	if m.overlay != overlayNone {
		t.Fatal("an outside click closes the palette as esc does")
	}
}

func TestContainerPickerOutsideCancels(t *testing.T) {
	m := podModel(t)
	t0 := time.Unix(0, 0)
	m.now = func() time.Time { return t0 }
	press(t, m, "l")
	m.Update(containersMsg{containers: []argocd.Container{
		container("app", "app:1"), container("side", "side:1"),
	}})
	if m.overlay != overlayContainer {
		t.Fatalf("overlay=%v, want the container picker", m.overlay)
	}
	wheel(m, 60, 10, tea.MouseButtonWheelDown)
	if m.picker.cur != 1 {
		t.Fatalf("picker.cur=%d, want 1 (clamped)", m.picker.cur)
	}
	click(m, 0, 2)
	if m.overlay != overlayNone || m.screen != screenApp {
		t.Fatalf("overlay=%v screen=%v: outside cancels without choosing", m.overlay, m.screen)
	}
}
