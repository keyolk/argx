package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A removal and the addition that replaces it belong on one row. Reading
// `- replicas: 3` and `+ replicas: 5` eleven lines apart is the work the layout
// exists to remove.
func TestRemovalsPairWithAdditions(t *testing.T) {
	rows := sideBySide([]string{
		" spec:",
		"-  replicas: 3",
		"+  replicas: 5",
		" status: {}",
	})

	if len(rows) != 3 {
		t.Fatalf("rows = %d, want the change paired into one", len(rows))
	}
	if rows[1].kind != '~' {
		t.Errorf("kind = %q, want a replacement", rows[1].kind)
	}
	if rows[1].left != "  replicas: 3" || rows[1].right != "  replicas: 5" {
		t.Errorf("row = %q | %q, want the old and new value side by side",
			rows[1].left, rows[1].right)
	}
	// Context appears on both sides: it is what both documents say.
	if rows[0].left != rows[0].right || rows[0].kind != ' ' {
		t.Errorf("context row = %q | %q kind %q, want it unchanged on both sides",
			rows[0].left, rows[0].right, rows[0].kind)
	}
}

// Runs of unequal length pair as far as they go; the remainder stands alone.
func TestUnevenRunsPairAsFarAsTheyGo(t *testing.T) {
	rows := sideBySide([]string{
		"-  a: 1",
		"-  b: 2",
		"-  c: 3",
		"+  a: 9",
	})

	if len(rows) != 3 {
		t.Fatalf("rows = %d, want three", len(rows))
	}
	if rows[0].kind != '~' {
		t.Errorf("the first pair should be a replacement, got %q", rows[0].kind)
	}
	for _, i := range []int{1, 2} {
		if rows[i].kind != '-' {
			t.Errorf("row %d kind = %q, want a removal with nothing opposite", i, rows[i].kind)
		}
		if rows[i].right != "" {
			t.Errorf("row %d has a right side (%q) it should not", i, rows[i].right)
		}
	}
}

// An addition with nothing removed is a one-sided row, not a replacement.
func TestPureAdditionHasNoLeftSide(t *testing.T) {
	rows := sideBySide([]string{" ports:", "+  - containerPort: 8080"})

	if len(rows) != 2 {
		t.Fatalf("rows = %d, want two", len(rows))
	}
	if rows[1].kind != '+' || rows[1].left != "" {
		t.Errorf("row = %q | %q kind %q, want an addition with an empty left",
			rows[1].left, rows[1].right, rows[1].kind)
	}
}

// Headers and hunk markers describe the comparison rather than either side, so
// they span the width instead of being duplicated into both columns.
func TestHeadersSpanBothColumns(t *testing.T) {
	rows := sideBySide([]string{
		"=== Deployment apps/web",
		"@@ ...",
		" spec:",
	})

	for i := 0; i < 2; i++ {
		if rows[i].kind != 'h' {
			t.Errorf("row %d kind = %q, want a header", i, rows[i].kind)
		}
		if rows[i].right != "" {
			t.Errorf("row %d should not be duplicated into the right column", i)
		}
	}
}

// A run that ends the input still pairs — the flush at the end is easy to
// forget and loses the last change in the file.
func TestATrailingRunIsNotLost(t *testing.T) {
	rows := sideBySide([]string{" spec:", "-  old: 1", "+  new: 1"})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want the trailing change kept", len(rows))
	}
	if rows[1].kind != '~' {
		t.Errorf("the last change should still pair, got %q", rows[1].kind)
	}
}

// The missing side is filled rather than blank: an empty cell and a cell of
// spaces look identical, and the reader needs to see that the line exists on
// only one side.
func TestAMissingSideIsVisible(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m.pager = []string{" spec:", "-  gone: true"}
	m.screen = screenDiff
	m.sxs = true

	out := stripANSI(m.View())
	if !strings.Contains(out, "···") {
		t.Errorf("a one-sided change should show the gap:\n%s", out)
	}
}

// Below the minimum width the view falls back rather than refusing. A cramped
// unified diff is still a diff; a message telling the reader to widen their
// window is not.
func TestNarrowTerminalFallsBackToUnified(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.pager = []string{"-  replicas: 3", "+  replicas: 5"}
	m.screen = screenDiff
	m.sxs = true

	if m.sxsAvailable() {
		t.Fatal("80 columns is below the two-column minimum")
	}
	out := stripANSI(m.View())
	if strings.Contains(out, "│") {
		t.Errorf("a narrow terminal should not attempt two columns:\n%s", out)
	}
	// And it says why, so pressing the key does not appear to do nothing.
	if !strings.Contains(out, "needs 100 columns") {
		t.Errorf("the fallback should explain itself:\n%s", out)
	}
}

// The toggle only applies to diffs: a manifest or a log has no two sides.
func TestSideBySideIsDiffOnly(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m.pager = []string{"some log line", "another"}
	m.screen = screenLogs

	m.Update(key("s"))
	if m.sxs {
		t.Error("s should not toggle side-by-side outside the diff view")
	}
}

func TestSideBySideToggles(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m.pager = []string{"-  a: 1", "+  a: 2"}
	m.screen = screenDiff

	m.Update(key("s"))
	if !m.sxs {
		t.Fatal("s should turn it on")
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "│") {
		t.Errorf("two columns should be drawn:\n%s", out)
	}
	m.Update(key("s"))
	if m.sxs {
		t.Error("s should turn it off again")
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "-  a: 1") {
		t.Errorf("unified should be back, with its prefixes:\n%s", out)
	}
}

// Side-by-side lays out whatever the search and the noise filter produced,
// rather than re-reading the manifests — one pipeline, one answer.
func TestSideBySideRunsOnTheFilteredContent(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	// JSON, because that is what renderDiff produces — prettyJSON re-indents
	// both sides before the diff is taken.
	m.pager = []string{
		`   "metadata": {`,
		`-    "managedFields": [`,
		`-      {"manager": "kubectl"}`,
		`-    ],`,
		`-    "name": "web"`,
		`+    "name": "web-2"`,
		`   }`,
	}
	m.screen = screenDiff
	m.sxs = true

	out := stripANSI(m.View())
	if strings.Contains(out, "kubectl") {
		t.Errorf("the noise filter should still apply in two columns:\n%s", out)
	}
	if !strings.Contains(out, `"name": "web-2"`) {
		t.Errorf("the real change should survive:\n%s", out)
	}
}

// s means sync nearly everywhere and side-by-side in the diff view. Each key
// handler owns its own screen, but a regression here would have `s` syncing
// something while the reader thought they were changing a layout.
func TestSDoesNotSyncFromTheDiffView(t *testing.T) {
	m := appModel(t, nil)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m.pager = []string{"-  a: 1", "+  a: 2"}
	m.push(screenDiff)

	m.Update(key("s"))

	if m.overlay != overlayNone {
		t.Errorf("overlay = %v, want no sync modal from a diff", m.overlay)
	}
	if !m.sxs {
		t.Error("s in a diff should change the layout")
	}
}

// And the other way: s still syncs where it always did.
func TestSStillSyncsFromTheApplicationList(t *testing.T) {
	m := appModel(t, nil)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m.appMarks[m.app.Key()] = true
	m.screen = screenApps

	m.Update(key("s"))

	if m.overlay != overlaySyncOpts {
		t.Errorf("overlay = %v, want the sync options", m.overlay)
	}
	if m.sxs {
		t.Error("s outside a diff must not change the diff layout")
	}
}
