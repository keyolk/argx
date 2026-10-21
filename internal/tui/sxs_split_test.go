package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/prefs"
	"github.com/keyolk/tuikit/split"
	"github.com/keyolk/tuikit/toast"
	"github.com/keyolk/tuikit/tuitest"
)

// Column-split tests read positions out of View(): the gutter's `│`, the
// text of each side. Nothing recomputes the layout arithmetic.

var sxsDiff = []string{"-  replicas: 3", "+  replicas: 5", "-  image: old", "+  image: new", " kind: Deployment"}

func sxsModel(t *testing.T, w, h int) *Model {
	t.Helper()
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.pager = append([]string(nil), sxsDiff...)
	m.screen, m.sxs = screenDiff, true
	return m
}

// gutterAt is the column of the gutter's rule on the row showing text.
func gutterAt(t *testing.T, m *Model, text string) (x, y int) {
	t.Helper()
	_, y = atCell(t, m, text)
	row := ansi.Strip(strings.Split(m.View(), "\n")[y])
	for _, g := range []string{"│", "┃"} {
		if i := strings.Index(row, g); i >= 0 {
			return ansi.StringWidth(row[:i]), y
		}
	}
	t.Fatalf("no gutter on row %d: %q", y, row)
	return 0, 0
}

func TestGutterDragMovesTheColumns(t *testing.T) {
	m := sxsModel(t, 140, 20)
	x, y := gutterAt(t, m, "replicas: 3")
	rx, _ := atCell(t, m, "replicas: 5")

	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if !strings.Contains(ansi.Strip(m.View()), "┃") {
		t.Error("a press on the gutter did not start a drag (no heavy rule)")
	}
	mouseDo(m, x+15, y+1, tea.MouseButtonLeft, tea.MouseActionMotion)
	if got := tuitest.Reversed(m.View()); len(got) != 0 {
		t.Errorf("a gutter drag selected %q", got)
	}
	mouseDo(m, x+15, y+1, tea.MouseButtonNone, tea.MouseActionRelease)

	nx, _ := gutterAt(t, m, "replicas: 3")
	if nx != x+15 {
		t.Fatalf("gutter at %d after dragging from %d by +15", nx, x)
	}
	if nrx, _ := atCell(t, m, "replicas: 5"); nrx != rx+15 {
		t.Errorf("right column text at %d, want %d", nrx, rx+15)
	}
	if strings.Contains(ansi.Strip(m.View()), "┃") {
		t.Error("the drag highlight stayed after release")
	}

	// A drag that starts in the new left-column area (right of the old
	// gutter) selects the left column only.
	lx, ly := atCell(t, m, "image: old")
	dragTo(m, x+3, ly, lx, ly)
	got := copiedText(m)
	if strings.Contains(got, "new") || strings.Contains(got, "│") {
		t.Errorf("a drag left of the moved gutter copied %q (crossed into the right column)", got)
	}
	panes := m.selPanes()
	if len(panes) != 2 || panes[0].Rect.X+panes[0].Rect.W+3 != panes[1].Rect.X || panes[1].Rect.X != nx+2 {
		t.Errorf("selection panes %+v do not sit around the gutter at %d", panes, nx)
	}
}

func TestColumnPressStillSelectsAndGutterPressNever(t *testing.T) {
	m := sxsModel(t, 140, 20)
	lx, ly := atCell(t, m, "replicas: 3")
	dragTo(m, lx, ly, lx+8, ly)
	if got := copiedText(m); !strings.Contains(got, "replicas") {
		t.Errorf("a drag in the left column copied %q", got)
	}
	m = sxsModel(t, 140, 20)
	x, y := gutterAt(t, m, "replicas: 3")
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	mouseDo(m, x, y+2, tea.MouseButtonLeft, tea.MouseActionMotion)
	if m.textSel.Active() {
		t.Error("a gutter press started a text selection")
	}
}

func TestDoubleClickOnTheGutterResets(t *testing.T) {
	m := sxsModel(t, 140, 20)
	x0, y := gutterAt(t, m, "replicas: 3")
	press(t, m, ">")
	x, _ := gutterAt(t, m, "replicas: 3")
	if x == x0 {
		t.Fatal("> did not move the gutter")
	}
	for range 2 {
		mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
		mouseDo(m, x, y, tea.MouseButtonNone, tea.MouseActionRelease)
	}
	if got, _ := gutterAt(t, m, "replicas: 3"); got != x0 {
		t.Errorf("double click left the gutter at %d, want %d", got, x0)
	}
}

func TestColumnKeysMoveTheGutter(t *testing.T) {
	m := sxsModel(t, 140, 20)
	x0, _ := gutterAt(t, m, "replicas: 3")
	press(t, m, ">")
	x1, _ := gutterAt(t, m, "replicas: 3")
	press(t, m, "<", "<")
	x2, _ := gutterAt(t, m, "replicas: 3")
	press(t, m, "=")
	x3, _ := gutterAt(t, m, "replicas: 3")
	if !(x1 > x0 && x2 < x0 && x3 == x0) {
		t.Errorf("gutter %d, > %d, << %d, = %d", x0, x1, x2, x3)
	}
	for _, c := range baseKeymap.Conflicts(allStacks()...) {
		t.Errorf("conflict: %s", c)
	}
	if aSplitGrow != split.ActionGrow || aSplitShrink != split.ActionShrink || aSplitReset != split.ActionReset {
		t.Error("argx's split action names drifted from the kit's")
	}
}

func TestColumnKeysInFooterAndHelp(t *testing.T) {
	m := sxsModel(t, 300, 20)
	if f := stripANSI(m.renderFooter()); !strings.Contains(f, "<> columns") {
		t.Errorf("wide side-by-side footer lacks the columns hint: %q", f)
	}
	help := strings.Join(m.helpLines(), "\n")
	for _, want := range []string{"widen the left", "narrow the left", "equal columns"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	m.sxs = false
	if f := stripANSI(m.renderFooter()); strings.Contains(f, "columns") {
		t.Errorf("unified diff footer offers %q", f)
	}
	press(t, m, ">")
	if !strings.Contains(m.toastText(), "side-by-side") {
		t.Errorf("> in the unified diff said %q", m.toastText())
	}
}

func TestGrowPastTheLimitThenShrinkMovesAtOnce(t *testing.T) {
	m := sxsModel(t, 140, 20)
	for range 40 {
		press(t, m, ">")
	}
	far, _ := gutterAt(t, m, "replicas: 3")
	if !strings.Contains(m.toastText(), "limit") {
		t.Errorf("a key at the limit said %q", m.toastText())
	}
	if _, _, r := m.sxsLayout(); r.W < 30 {
		t.Errorf("right column %d wide at the limit, want >= 30", r.W)
	}
	press(t, m, "<")
	if x, _ := gutterAt(t, m, "replicas: 3"); x >= far {
		t.Errorf("one < after overshooting left the gutter at %d (was %d): dead zone", x, far)
	}
}

func TestColumnRatioIsSavedAndRestored(t *testing.T) {
	m := sxsModel(t, 140, 20)
	x0, _ := gutterAt(t, m, "replicas: 3")
	press(t, m, ">", ">", ">")
	x, _ := gutterAt(t, m, "replicas: 3")
	_, cmd := m.Update(key("q"))
	if !quits(t, m, cmd) {
		t.Fatal("q did not quit")
	}
	data, _ := os.ReadFile(prefs.Path(prefsApp))
	if !strings.Contains(string(data), `"diff_split"`) {
		t.Fatalf("ui.json = %s, want diff_split", data)
	}
	m2 := newTestModelSameState(t)
	m2.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m2.pager, m2.screen, m2.sxs = append([]string(nil), sxsDiff...), screenDiff, true
	if got, _ := gutterAt(t, m2, "replicas: 3"); got != x || got == x0 {
		t.Errorf("restored gutter at %d, saved at %d (default %d)", got, x, x0)
	}
}

func TestGarbageColumnRatioKeepsEqualColumns(t *testing.T) {
	x0, _ := gutterAt(t, sxsModel(t, 140, 20), "replicas: 3")
	for _, raw := range []string{`{"diff_split":{"ratio":40}}`, `{"diff_split":{"ratio":-0.5}}`, `{"diff_split":{}}`} {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		path := prefs.Path(prefsApp)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		m := newTestModelSameState(t)
		m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
		m.pager, m.screen, m.sxs = append([]string(nil), sxsDiff...), screenDiff, true
		if got, _ := gutterAt(t, m, "replicas: 3"); got != x0 {
			t.Errorf("%s: gutter at %d, want %d", raw, got, x0)
		}
	}
}

func TestColumnsFitNarrowTerminals(t *testing.T) {
	for _, sz := range [][2]int{{100, 24}, {80, 24}, {60, 24}, {220, 40}} {
		for _, k := range []string{">", "<"} {
			m := sxsModel(t, sz[0], sz[1])
			for range 30 {
				press(t, m, k)
			}
			rows := strings.Split(m.View(), "\n")
			if len(rows) != sz[1] {
				t.Errorf("%dx%d after %s: %d rows", sz[0], sz[1], k, len(rows))
			}
			for i, r := range rows {
				if w := ansi.StringWidth(r); w > sz[0] {
					t.Errorf("%dx%d after %s: row %d is %d wide", sz[0], sz[1], k, i, w)
				}
			}
		}
	}
	// 60 and 80 columns are below two columns: unified, and the keys say so.
	m := sxsModel(t, 60, 24)
	if strings.Contains(ansi.Strip(m.View()), "│ ") && m.sxsActive() {
		t.Error("60 columns shows two columns")
	}
}

// A gutter press is the split's alone: the click handler, which takes any
// press as reading a sticky error, never sees it.
func TestGutterPressNeverReachesTheClickHandler(t *testing.T) {
	m := sxsModel(t, 140, 20)
	t0 := time.Now()
	m.toasts = toast.WithClock(m.toasts, func() time.Time { return t0 })
	m.setToastErr("boom")
	// Past the minimum dwell: the next press would acknowledge it.
	m.toasts = toast.WithClock(m.toasts, func() time.Time { return t0.Add(time.Hour) })
	x, y := gutterAt(t, m, "replicas: 3")
	mouseDo(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	mouseDo(m, x, y, tea.MouseButtonNone, tea.MouseActionRelease)
	if !strings.Contains(m.toastText(), "boom") {
		t.Error("a gutter press reached the click handler (it acknowledged the error)")
	}
	lx, ly := atCell(t, m, "replicas: 3")
	mouseDo(m, lx, ly, tea.MouseButtonLeft, tea.MouseActionPress)
	if strings.Contains(m.toastText(), "boom") {
		t.Error("control: a column press did not acknowledge the error")
	}
}
