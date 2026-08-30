package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func markModel(t *testing.T, names ...string) *Model {
	t.Helper()
	m := newTestModel(t, names...)
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 24})
	return m
}

func markedNames(m *Model) []string {
	var out []string
	for _, i := range m.appRows {
		if m.appMarks[m.apps[i].Key()] {
			out = append(out, m.apps[i].Name())
		}
	}
	return out
}

// a marks and does not unmark. A key whose effect depends on how many rows
// happen to be marked is one whose effect you find out by pressing it — the
// wrong way to discover you just marked 2,976 applications.
func TestMarkAllOnlyMarks(t *testing.T) {
	m := markModel(t, "a", "b", "c")

	press(t, m, "a")
	if len(m.appMarks) != 3 {
		t.Fatalf("marks = %d, want all three", len(m.appMarks))
	}
	press(t, m, "a")
	if len(m.appMarks) != 3 {
		t.Errorf("a again should change nothing, got %d", len(m.appMarks))
	}
	if !strings.Contains(m.toast, "already marked") {
		t.Errorf("toast = %q, want it to say nothing changed", m.toast)
	}
}

// A clears what is visible, and says what it left behind. Marks the filter
// hides are still targets of the next sync.
func TestClearReportsWhatTheFilterHides(t *testing.T) {
	m := markModel(t, "web-prod", "web-dev", "api-prod")
	press(t, m, "a")
	m.appFilter = parseAppFilter("prod")
	m.applyAppFilter()

	press(t, m, "A")

	if len(m.appMarks) != 1 {
		t.Fatalf("marks = %d, want the hidden one kept", len(m.appMarks))
	}
	if !m.appMarks["test/web-dev"] {
		t.Error("the mark outside the filter should be the one that survived")
	}
	if !strings.Contains(m.toast, "outside the filter") {
		t.Errorf("toast = %q, want the surviving marks called out", m.toast)
	}

	// A second A, with nothing visible left, takes the rest — so the escape
	// hatch exists without a separate key.
	press(t, m, "A")
	if len(m.appMarks) != 0 {
		t.Errorf("marks = %d, want the hidden ones cleared too", len(m.appMarks))
	}
}

// With no filter there is nothing to warn about.
func TestClearIsQuietWithNothingHidden(t *testing.T) {
	m := markModel(t, "a", "b")
	press(t, m, "a")
	press(t, m, "A")

	if len(m.appMarks) != 0 {
		t.Fatalf("marks = %d, want none", len(m.appMarks))
	}
	if strings.Contains(m.toast, "outside") {
		t.Errorf("toast = %q, want no warning when nothing is hidden", m.toast)
	}
}

// The status line carries the hidden count for as long as it exists — a
// selection wider than the screen must not be invisible.
func TestHiddenMarksAreInTheStatusLine(t *testing.T) {
	m := markModel(t, "web-prod", "web-dev", "api-prod")
	press(t, m, "a")
	m.appFilter = parseAppFilter("prod")
	m.applyAppFilter()

	if out := stripANSI(m.View()); !strings.Contains(out, "3 marked (1 not shown)") {
		t.Errorf("the status line should name the hidden marks:\n%s", out)
	}
}

// Range select marks everything the cursor passed, inclusive at both ends.
func TestRangeSelectMarksTheSpan(t *testing.T) {
	m := markModel(t, "a", "b", "c", "d", "e")

	press(t, m, "v")
	press(t, m, "j")
	press(t, m, "j")
	press(t, m, "v")

	got := strings.Join(markedNames(m), ",")
	if got != "a,b,c" {
		t.Errorf("marked %q, want a,b,c — both ends included", got)
	}
	if m.visualFrom >= 0 {
		t.Error("the range should be over")
	}
}

// It works upwards too: the anchor is wherever v was pressed, not the top.
func TestRangeSelectWorksBackwards(t *testing.T) {
	m := markModel(t, "a", "b", "c", "d")
	press(t, m, "G")

	press(t, m, "v")
	press(t, m, "k")
	press(t, m, "v")

	got := strings.Join(markedNames(m), ",")
	if got != "c,d" {
		t.Errorf("marked %q, want c,d", got)
	}
}

// The pending range is drawn before it is taken, so the reader sees what v
// will mark rather than committing blind.
func TestPendingRangeIsVisible(t *testing.T) {
	m := markModel(t, "a", "b", "c")
	press(t, m, "v")
	press(t, m, "j")

	if _, _, active := m.visualRange(m.appCur); !active {
		t.Fatal("a range should be in progress")
	}
	if !inVisualRange(m, 0) || !inVisualRange(m, 1) {
		t.Error("the rows between the anchor and the cursor should be shown as pending")
	}
	if inVisualRange(m, 2) {
		t.Error("a row the cursor has not reached is not in the range")
	}
	out := stripANSI(m.View())
	if !strings.Contains(out, "range: 2 rows") {
		t.Errorf("the status line should count the pending rows:\n%s", out)
	}
	// Nothing is marked until v is pressed again.
	if len(m.appMarks) != 0 {
		t.Errorf("marks = %d, want none until the range is taken", len(m.appMarks))
	}
}

// Esc abandons the range rather than the screen — it is the innermost level.
func TestEscCancelsTheRange(t *testing.T) {
	m := markModel(t, "a", "b", "c")
	press(t, m, "v")
	press(t, m, "j")

	press(t, m, "esc")

	if m.visualFrom >= 0 {
		t.Error("esc should end the range")
	}
	if len(m.appMarks) != 0 {
		t.Error("a cancelled range marks nothing")
	}
	if m.screen != screenApps {
		t.Errorf("screen = %v, want to still be on the list", m.screen)
	}
}

// Invert flips the visible rows and leaves the rest alone.
func TestInvertFlipsTheVisibleRows(t *testing.T) {
	m := markModel(t, "web-prod", "web-dev", "api-prod")
	m.appMarks["test/web-prod"] = true
	m.appFilter = parseAppFilter("prod")
	m.applyAppFilter()

	press(t, m, "i")

	if m.appMarks["test/web-prod"] {
		t.Error("a marked visible row should have been unmarked")
	}
	if !m.appMarks["test/api-prod"] {
		t.Error("an unmarked visible row should have been marked")
	}
}

func TestInvertLeavesHiddenMarksAlone(t *testing.T) {
	m := markModel(t, "web-prod", "web-dev")
	m.appMarks["test/web-dev"] = true
	m.appFilter = parseAppFilter("prod")
	m.applyAppFilter()

	press(t, m, "i")

	if !m.appMarks["test/web-dev"] {
		t.Error("a mark the filter hides is not part of the inversion")
	}
}

// + is additive, which is what makes a selection buildable across filters.
func TestPlusAddsWithoutRemoving(t *testing.T) {
	m := markModel(t, "web-prod", "api-prod", "db-dev")

	m.appFilter = parseAppFilter("web")
	m.applyAppFilter()
	press(t, m, "+")

	m.appFilter = parseAppFilter("dev")
	m.applyAppFilter()
	press(t, m, "+")

	if len(m.appMarks) != 2 {
		t.Fatalf("marks = %d, want both filters' rows", len(m.appMarks))
	}
	if !m.appMarks["test/web-prod"] || !m.appMarks["test/db-dev"] {
		t.Errorf("marks = %v, want one from each filter", m.appMarks)
	}
}

// m narrows the list to the selection, so forty marks built across several
// filters can be checked before anything is done to them.
func TestMarkedOnlyNarrowsTheList(t *testing.T) {
	m := markModel(t, "a", "b", "c", "d")
	m.appMarks["test/a"] = true
	m.appMarks["test/c"] = true
	m.applyAppFilter()

	press(t, m, "m")

	if len(m.appRows) != 2 {
		t.Fatalf("rows = %d, want just the marked ones", len(m.appRows))
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "marked only") {
		t.Errorf("the narrowed list should say why it is short:\n%s", out)
	}

	press(t, m, "m")
	if len(m.appRows) != 4 {
		t.Errorf("rows = %d, want everything back", len(m.appRows))
	}
}

// It composes with the query rather than replacing it.
func TestMarkedOnlyComposesWithTheFilter(t *testing.T) {
	m := markModel(t, "web-prod", "web-dev", "api-prod")
	m.appMarks["test/web-prod"] = true
	m.appMarks["test/web-dev"] = true
	m.appFilter = parseAppFilter("prod")
	m.applyAppFilter()

	press(t, m, "m")

	if len(m.appRows) != 1 {
		t.Fatalf("rows = %d, want the one row that is both marked and matching", len(m.appRows))
	}
	if m.apps[m.appRows[0]].Name() != "web-prod" {
		t.Errorf("row = %s, want web-prod", m.apps[m.appRows[0]].Name())
	}
}

// With nothing marked it says so rather than showing an empty list.
func TestMarkedOnlyRefusesAnEmptySelection(t *testing.T) {
	m := markModel(t, "a", "b")
	press(t, m, "m")

	if m.markedOnly {
		t.Error("there is nothing to narrow to")
	}
	if !strings.Contains(m.toast, "nothing is marked") {
		t.Errorf("toast = %q, want it to say why", m.toast)
	}
}

// The same vocabulary works on the resource tree.
func TestMarkKeysWorkOnTheTree(t *testing.T) {
	m := appModel(t, nil)
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 24})
	fixtureTree(t, m)
	m.applyTreeFilter()
	m.tab = tabResources
	m.screen = screenApp

	press(t, m, "a")
	if len(m.treeMarks) != len(m.treeRows) {
		t.Fatalf("marks = %d, want every visible resource", len(m.treeMarks))
	}
	press(t, m, "i")
	if len(m.treeMarks) != 0 {
		t.Errorf("inverting a full selection empties it, got %d", len(m.treeMarks))
	}
	press(t, m, "A")
	if !strings.Contains(m.toast, "nothing was marked") {
		t.Errorf("toast = %q, want it to say there was nothing to clear", m.toast)
	}
}

// The footer names the mark keys. They sat undiscovered behind `space mark`,
// which taught marking one row and nothing else.
func TestFooterNamesTheMarkKeys(t *testing.T) {
	m := markModel(t, "a")
	out := stripANSI(m.renderFooter())
	for _, want := range []string{"space/a/A mark", "v range"} {
		if !strings.Contains(out, want) {
			t.Errorf("the footer does not mention %q:\n%s", want, out)
		}
	}
}

// While a range is open the footer names only the keys that end it: it is a
// mode, and the other hints are not what the reader needs.
func TestFooterDuringARange(t *testing.T) {
	m := markModel(t, "a", "b")
	press(t, m, "v")

	out := stripANSI(m.renderFooter())
	if !strings.Contains(out, "v take the range") || !strings.Contains(out, "esc cancel") {
		t.Errorf("the footer should name the keys that end the range:\n%s", out)
	}
	if strings.Contains(out, "S appsets") {
		t.Errorf("the unrelated hints should be out of the way:\n%s", out)
	}
}

// While the list is narrowed to the selection, changing the selection changes
// the list. Otherwise unmarking a row leaves it on screen, and clearing
// everything leaves a list of rows that are no longer marked.
func TestMarkedOnlyFollowsTheSelection(t *testing.T) {
	m := markModel(t, "a", "b", "c")
	m.appMarks["test/a"] = true
	m.appMarks["test/b"] = true
	m.applyAppFilter()
	press(t, m, "m")

	if len(m.appRows) != 2 {
		t.Fatalf("rows = %d, want the two marked", len(m.appRows))
	}
	press(t, m, " ")
	if len(m.appRows) != 1 {
		t.Errorf("rows = %d, want the unmarked row gone", len(m.appRows))
	}
}

// An empty selection turns the mode off rather than showing an empty list: a
// list with no rows and no explanation is a dead end, and the reader who just
// cleared their marks did not ask to be left staring at nothing.
func TestClearingEverythingLeavesMarkedOnly(t *testing.T) {
	m := markModel(t, "a", "b", "c")
	press(t, m, "a")
	press(t, m, "m")

	press(t, m, "A")

	if m.markedOnly {
		t.Error("with nothing marked there is nothing to narrow to")
	}
	if len(m.appRows) != 3 {
		t.Errorf("rows = %d, want the whole list back", len(m.appRows))
	}
	if !strings.Contains(m.toast, "showing everything") {
		t.Errorf("toast = %q, want it to explain the list came back", m.toast)
	}
}

// Space still marks and advances, and still works on the tree — it moved into
// the shared vocabulary and must not have changed behaviour on the way.
func TestSpaceStillMarksAndAdvances(t *testing.T) {
	m := markModel(t, "a", "b", "c")

	press(t, m, " ")
	if !m.appMarks["test/a"] {
		t.Error("space should mark the row under the cursor")
	}
	if m.appCur != 1 {
		t.Errorf("cursor = %d, want it advanced", m.appCur)
	}
	press(t, m, " ")
	press(t, m, "k")
	press(t, m, " ")
	if m.appMarks["test/b"] {
		t.Error("space on a marked row should unmark it")
	}
}
