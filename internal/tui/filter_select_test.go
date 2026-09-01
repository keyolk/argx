package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- marking without leaving the prompt ----

// Narrowing and selecting are one activity: filter, take those rows, filter
// again, take those. Closing the prompt in between means retyping the query.
func TestMarkingWorksWhileFiltering(t *testing.T) {
	m := markModel(t, "web-a", "web-b", "api-a")

	press(t, m, "/")
	for _, r := range "web" {
		press(t, m, string(r))
	}
	if len(m.appRows) != 2 {
		t.Fatalf("setup: filter matched %d rows, want the two web apps", len(m.appRows))
	}
	if !m.filtering {
		t.Fatal("setup: the prompt should still be open")
	}

	press(t, m, "right")
	if !m.filtering {
		t.Error("marking must not close the prompt — the query is what it was built on")
	}
	if got := markedNames(m); strings.Join(got, ",") != "web-a" {
		t.Errorf("marked %v, want the row under the cursor", got)
	}
	if m.appCur != 1 {
		t.Errorf("cursor = %d, want 1 — → advances like space does", m.appCur)
	}
}

// The whole point is building a selection across several queries: what one
// filter marked has to survive the next one.
func TestMarksSurviveRefilteringFromThePrompt(t *testing.T) {
	m := markModel(t, "web-a", "api-a")

	press(t, m, "/")
	for _, r := range "web" {
		press(t, m, string(r))
	}
	press(t, m, "right")

	// Retype the query for the other app, still without closing the prompt.
	press(t, m, "ctrl+u")
	for _, r := range "api" {
		press(t, m, string(r))
	}
	press(t, m, "right")

	if len(m.appMarks) != 2 {
		t.Errorf("marks = %d, want one from each filter: %v", len(m.appMarks), m.appMarks)
	}
}

// shift+↑↓ extend here too — the letters cannot, since J and K are text.
func TestShiftArrowsExtendWhileFiltering(t *testing.T) {
	m := markModel(t, "web-a", "web-b", "web-c")

	press(t, m, "/")
	press(t, m, "w")
	press(t, m, "shift+down", "shift+down")

	if got := markedNames(m); strings.Join(got, ",") != "web-a,web-b,web-c" {
		t.Errorf("marked %v, want the rows the cursor passed", got)
	}
	if !m.filtering {
		t.Error("extending must not close the prompt")
	}
}

// The mark keys are modified for a reason: the unmodified ones are text, and a
// filter that could not contain a space or a J would be useless.
func TestPlainKeysStillTypeWhileFiltering(t *testing.T) {
	m := markModel(t, "a b", "JK")

	press(t, m, "/")
	press(t, m, " ", "J", "K")
	if got := m.appFilter.raw; got != " JK" {
		t.Errorf("query = %q, want the keys to have been typed", got)
	}
	if len(m.appMarks) != 0 {
		t.Errorf("nothing should have been marked, got %v", m.appMarks)
	}
}

// A pager has no marks, so these keys have nothing to act on there. They must
// do nothing rather than panic on a nil cursor.
func TestMarkKeysAreInertWhereThereAreNoMarks(t *testing.T) {
	m := markModel(t, "alpha")
	m.screen = screenLogs
	m.pager = []string{"one", "two"}

	press(t, m, "/")
	press(t, m, "right", "shift+down")
	if !m.filtering {
		t.Error("the prompt should still be open")
	}
}

// ---- esc clears a standing filter ----

// Enter keeps the query, which leaves the reader on a narrowed list whose only
// way back to everything was to reopen the prompt and erase what they typed.
func TestEscClearsAStandingFilter(t *testing.T) {
	m := markModel(t, "web-a", "api-a")

	press(t, m, "/")
	for _, r := range "web" {
		press(t, m, string(r))
	}
	press(t, m, "enter")
	if m.filtering || len(m.appRows) != 1 {
		t.Fatalf("setup: want a closed prompt over 1 row, got filtering=%v rows=%d",
			m.filtering, len(m.appRows))
	}

	press(t, m, "esc")
	if m.appFilter.raw != "" {
		t.Errorf("esc should have cleared the query, got %q", m.appFilter.raw)
	}
	if len(m.appRows) != 2 {
		t.Errorf("the whole list should be back, got %d rows", len(m.appRows))
	}
	if m.screen != screenApps {
		t.Errorf("clearing the filter is one level; it must not also leave the screen (screen=%v)", m.screen)
	}
}

// The marks are what the filtering was for. Dropping a selection built across
// several filters because the reader wanted the whole list back is the surprise
// in the expensive direction.
func TestEscKeepsTheMarksItWasBuiltWith(t *testing.T) {
	m := markModel(t, "web-a", "api-a")

	press(t, m, "/")
	for _, r := range "web" {
		press(t, m, string(r))
	}
	press(t, m, "right", "enter", "esc")

	if len(m.appMarks) != 1 {
		t.Errorf("marks = %v, want the selection to have survived", m.appMarks)
	}
}

// Once nothing is narrowing the list, esc goes back to being the key that
// unwinds a screen.
func TestEscStillLeavesTheScreenWithNoFilter(t *testing.T) {
	m := markModel(t, "alpha")
	press(t, m, "enter")
	if m.screen != screenApp {
		t.Fatalf("setup: expected the application view, got %v", m.screen)
	}

	press(t, m, "esc")
	if m.screen != screenApps {
		t.Errorf("esc with nothing to clear should leave the screen, got %v", m.screen)
	}
}

// Two things narrow a list and they come off in the order they went on: `m`
// sits in front of the query, so one esc must not jump both.
func TestEscUnwindsMarkedOnlyBeforeTheQuery(t *testing.T) {
	m := markModel(t, "web-a", "web-b", "api-a")

	press(t, m, "/")
	for _, r := range "web" {
		press(t, m, string(r))
	}
	press(t, m, "enter", "a", "m")
	if !m.markedOnly {
		t.Fatal("setup: m should have narrowed the list to the marks")
	}

	press(t, m, "esc")
	if m.markedOnly {
		t.Error("the first esc should have taken the marked-only view")
	}
	if m.appFilter.raw != "web" {
		t.Errorf("it must not also take the query, got %q", m.appFilter.raw)
	}

	press(t, m, "esc")
	if m.appFilter.raw != "" {
		t.Errorf("the second esc should take the query, got %q", m.appFilter.raw)
	}
	if m.screen != screenApps {
		t.Errorf("neither esc should leave the screen, got %v", m.screen)
	}
}

// The resource tree has its own query, and it unwinds the same way — without
// dropping the reader back to the application list on the first press.
func TestEscClearsTheResourceFilterBeforeLeaving(t *testing.T) {
	m := markModel(t, "alpha")
	press(t, m, "enter")
	m.app = &m.apps[0]
	m.screen, m.tab = screenApp, tabResources
	m.treeFilt = parseResourceFilter("kind:pod")

	press(t, m, "esc")
	if m.treeFilt.raw != "" {
		t.Errorf("esc should clear the resource filter, got %q", m.treeFilt.raw)
	}
	if m.screen != screenApp {
		t.Errorf("it must not also leave the tree, got %v", m.screen)
	}
}

// ctrl+c is the terminal's interrupt and must not be caught by any of this.
func TestInterruptStillQuitsFromAFilteredList(t *testing.T) {
	m := markModel(t, "web-a")
	press(t, m, "/")
	press(t, m, "w")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c should quit even mid-filter")
	}
}

// A search in a manifest or diff narrows the same way, and esc must take the
// search before it takes the view the reader searched.
func TestEscClearsAPagerSearchBeforeLeaving(t *testing.T) {
	m := markModel(t, "alpha")
	m.push(screenDiff)
	m.pager = []string{"a", "b"}
	m.pagerFilt = "image"

	press(t, m, "esc")
	if m.pagerFilt != "" {
		t.Errorf("esc should clear the search, got %q", m.pagerFilt)
	}
	if m.screen != screenDiff {
		t.Errorf("it must not also leave the view, got %v", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenApps {
		t.Errorf("the second esc should leave, got %v", m.screen)
	}
}

// q quits outright from anywhere and is not part of the unwind chain — a
// filtered list must not turn it into "clear the filter".
func TestQuitIsUnaffectedByAStandingFilter(t *testing.T) {
	m := markModel(t, "web-a")
	press(t, m, "/")
	press(t, m, "w")
	press(t, m, "enter")

	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q should still quit from a filtered list")
	}
}

// A range select is the innermost level of all and must stay in front of the
// filter it is being made under.
func TestEscCancelsARangeBeforeTouchingTheFilter(t *testing.T) {
	m := markModel(t, "web-a", "web-b")
	press(t, m, "/")
	press(t, m, "w")
	press(t, m, "enter", "v")
	if m.visualFrom < 0 {
		t.Fatal("setup: v should have opened a range")
	}

	press(t, m, "esc")
	if m.visualFrom >= 0 {
		t.Error("the first esc should cancel the range")
	}
	if m.appFilter.raw != "w" {
		t.Errorf("it must not also clear the filter, got %q", m.appFilter.raw)
	}
}

// ---- bulk selection, from a filtered list ----

// "All" on a filtered list means "all of what I just narrowed to". It needs no
// key of its own inside the prompt: Enter closes the prompt and keeps the
// query, so the ordinary `a` already means exactly that.
func TestMarkAllAfterClosingThePromptKeepsTheQueryScope(t *testing.T) {
	m := markModel(t, "web-a", "web-b", "api-a")

	press(t, m, "/")
	for _, r := range "web" {
		press(t, m, string(r))
	}
	press(t, m, "enter", "a")

	if got := markedNames(m); strings.Join(got, ",") != "web-a,web-b" {
		t.Errorf("marked %v, want every row the filter left", got)
	}
	if m.appMarks["test/api-a"] {
		t.Error("a row the filter hid must not be marked")
	}
	if m.appFilter.raw != "web" {
		t.Errorf("enter must keep the query, got %q", m.appFilter.raw)
	}
}

// And the selection carries into the next query, which is what makes it
// buildable across several: filter, a, filter again, +.
func TestBulkMarksAccumulateAcrossQueries(t *testing.T) {
	m := markModel(t, "web-a", "web-b", "api-a")

	press(t, m, "/")
	for _, r := range "web" {
		press(t, m, string(r))
	}
	press(t, m, "enter", "a")

	press(t, m, "/")
	press(t, m, "ctrl+u")
	for _, r := range "api" {
		press(t, m, string(r))
	}
	press(t, m, "enter", "+")

	if len(m.appMarks) != 3 {
		t.Errorf("marks = %d, want the two web apps plus the api one: %v",
			len(m.appMarks), m.appMarks)
	}
}

// ---- the arrows are an inverse pair ----

// ← steps back onto the row → last marked and unmarks it, so a run marked one
// row too far is undone by pressing the other arrow rather than by remembering
// which row it was.
func TestLeftArrowUndoesRightArrow(t *testing.T) {
	m := markModel(t, "web-a", "web-b", "web-c")
	press(t, m, "/")
	press(t, m, "w")

	press(t, m, "right", "right")
	if got := markedNames(m); strings.Join(got, ",") != "web-a,web-b" {
		t.Fatalf("marked %v, want the two rows → passed", got)
	}

	press(t, m, "left")
	if got := markedNames(m); strings.Join(got, ",") != "web-a" {
		t.Errorf("marked %v — ← should have taken back the last one", got)
	}
	press(t, m, "left")
	if got := markedNames(m); len(got) != 0 {
		t.Errorf("marked %v — ← again should have taken the first too", got)
	}
	if !m.filtering {
		t.Error("neither arrow may close the prompt")
	}
}

// The arrows must not reach the query, or they would insert a stray rune.
func TestArrowsDoNotReachTheQuery(t *testing.T) {
	m := markModel(t, "web-a", "web-b")
	press(t, m, "/")
	press(t, m, "w")
	press(t, m, "right", "left")

	if m.appFilter.raw != "w" {
		t.Errorf("the arrows leaked into the query: %q", m.appFilter.raw)
	}
}

// Where there is nothing to mark the arrows keep their text-cursor meaning:
// taking a key away from editing in order to do nothing is worse than leaving
// it alone.
func TestArrowsStillMoveTheTextCursorWhereThereAreNoMarks(t *testing.T) {
	m := markModel(t, "alpha")
	m.screen = screenAppSets
	press(t, m, "/")
	for _, r := range "abc" {
		press(t, m, string(r))
	}
	if m.filterCur != 3 {
		t.Fatalf("setup: cursor = %d, want 3", m.filterCur)
	}

	press(t, m, "left", "left")
	if m.filterCur != 1 {
		t.Errorf("cursor = %d, want the arrows to still move it here", m.filterCur)
	}
	press(t, m, "right")
	if m.filterCur != 2 {
		t.Errorf("cursor = %d, want 2", m.filterCur)
	}
}

// ctrl+b / ctrl+f are what editing the query falls back to on a list that
// marks, so they have to actually work there.
func TestReadlineMotionsStillEditTheQueryOnAMarkingList(t *testing.T) {
	m := markModel(t, "web-a")
	press(t, m, "/")
	for _, r := range "wb" {
		press(t, m, string(r))
	}

	press(t, m, "ctrl+b")
	if m.filterCur != 1 {
		t.Fatalf("ctrl+b should move a character back, cursor = %d", m.filterCur)
	}
	press(t, m, "e")
	if m.appFilter.raw != "web" {
		t.Errorf("query = %q, want the insertion at the cursor", m.appFilter.raw)
	}
}

// A range opened before the prompt is still the innermost level: Esc inside
// the prompt abandons the range and leaves the query alone. Otherwise the range
// survives an Esc that erased the rows it was being made over.
func TestEscCancelsARangeFromInsideThePrompt(t *testing.T) {
	m := markModel(t, "web-a", "web-b")

	press(t, m, "/")
	press(t, m, "w")
	press(t, m, "enter", "v") // open the range on the filtered list
	press(t, m, "/")          // and reopen the prompt over it
	if m.visualFrom < 0 || !m.filtering {
		t.Fatalf("setup: want an open range under an open prompt (visualFrom=%d filtering=%v)",
			m.visualFrom, m.filtering)
	}

	press(t, m, "esc")
	if m.visualFrom >= 0 {
		t.Error("esc should have cancelled the range")
	}
	if !m.filtering || m.appFilter.raw != "w" {
		t.Errorf("it must not also close the prompt or clear the query (filtering=%v query=%q)",
			m.filtering, m.appFilter.raw)
	}

	// With the range gone, the next Esc goes back to closing the prompt.
	press(t, m, "esc")
	if m.filtering {
		t.Error("the second esc should close the prompt")
	}
}

// The keys that mark are exactly the keys the prompt refuses to treat as text
// or as text-cursor movement, and no others.
func TestMarkKeysDoNotReachTheQuery(t *testing.T) {
	for k := range filterMarkKeys {
		m := markModel(t, "web-a")
		press(t, m, "/")
		press(t, m, "w")
		press(t, m, k)

		if m.appFilter.raw != "w" {
			t.Errorf("%s leaked into the query: %q", k, m.appFilter.raw)
		}
	}
}
