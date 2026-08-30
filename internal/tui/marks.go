package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Multi-select.
//
// Marking is how every destructive action in argx chooses its targets, so the
// selection has to be something the reader can build deliberately and inspect
// before acting. A key that means "select all" when half the rows are marked
// and "clear" when they all are is a key whose effect you find out by pressing
// it — which is the wrong way to discover you just marked 2,976 applications.
//
// The vocabulary is the same on both lists:
//
//	space   toggle this row and advance
//	v       range select — mark everything the cursor passes
//	a / A   mark all / clear all, each doing one thing
//	i       invert
//	+       add the filter's rows to the selection, keeping what is already there
//	m       show only what is marked
//
// Everything is scoped to the *filtered* rows. "All" meaning every application
// on every server would mark things the reader cannot see, and the one action
// they would take next is a sync.

// markScope is the list a mark operation applies to. Both lists have the same
// shape — visible rows, a key per row, a set of marks — so the operations are
// written once against this rather than twice against each list.
type markScope struct {
	// keys are the marks of the currently visible rows, in display order.
	keys []string
	// marks is the set being edited.
	marks map[string]bool
	// noun names what is being marked, for the toasts.
	noun string
}

// appScope is the application list's marks.
func (m *Model) appScope() markScope {
	keys := make([]string, 0, len(m.appRows))
	for _, i := range m.appRows {
		keys = append(keys, m.apps[i].Key())
	}
	return markScope{keys: keys, marks: m.appMarks, noun: "application"}
}

// treeScope is the resource tree's marks.
func (m *Model) treeScope() markScope {
	keys := make([]string, 0, len(m.treeRows))
	for _, i := range m.treeRows {
		keys = append(keys, m.tree[i].Node.UID)
	}
	return markScope{keys: keys, marks: m.treeMarks, noun: "resource"}
}

// markedCount is how many of the visible rows are marked.
func (s markScope) markedCount() int {
	n := 0
	for _, k := range s.keys {
		if s.marks[k] {
			n++
		}
	}
	return n
}

// hiddenCount is how many marks are outside the current filter.
//
// This is the number that has to be said out loud: a selection the reader
// cannot see is one they will act on without meaning to.
func (s markScope) hiddenCount() int {
	visible := make(map[string]bool, len(s.keys))
	for _, k := range s.keys {
		visible[k] = true
	}
	n := 0
	for k := range s.marks {
		if !visible[k] {
			n++
		}
	}
	return n
}

// markAll marks every visible row, keeping marks outside the filter.
func (s markScope) markAll() int {
	n := 0
	for _, k := range s.keys {
		if !s.marks[k] {
			s.marks[k] = true
			n++
		}
	}
	return n
}

// clearVisible unmarks every visible row and reports what it cleared and what
// it left behind.
//
// Only the visible ones: a reader who filtered to `proj:web` and pressed clear
// meant those, and silently dropping a selection they built under a different
// filter would be a surprise in the expensive direction. The remainder is
// reported rather than left silent.
func (s markScope) clearVisible() (cleared, hidden int) {
	for _, k := range s.keys {
		if s.marks[k] {
			delete(s.marks, k)
			cleared++
		}
	}
	return cleared, len(s.marks)
}

// clearAll drops every mark, filtered or not.
func (s markScope) clearAll() int {
	n := len(s.marks)
	for k := range s.marks {
		delete(s.marks, k)
	}
	return n
}

// invert flips the visible rows, leaving marks outside the filter alone.
func (s markScope) invert() int {
	for _, k := range s.keys {
		if s.marks[k] {
			delete(s.marks, k)
		} else {
			s.marks[k] = true
		}
	}
	return s.markedCount()
}

// markRange marks every row between two cursor positions, inclusive.
func (s markScope) markRange(from, to int) int {
	if from > to {
		from, to = to, from
	}
	if from < 0 {
		from = 0
	}
	if to >= len(s.keys) {
		to = len(s.keys) - 1
	}
	n := 0
	for i := from; i <= to; i++ {
		if !s.marks[s.keys[i]] {
			s.marks[s.keys[i]] = true
			n++
		}
	}
	return n
}

// ---- the keys ----

// handleMarkKey runs the mark vocabulary shared by both lists.
//
// It returns false when the key is not one of these, so each list's own handler
// can carry on with it.
func (m *Model) handleMarkKey(msg tea.KeyMsg, scope markScope, cur *int) (tea.Cmd, bool) {
	// While the list is narrowed to the selection, changing the selection
	// changes the list — otherwise unmarking a row leaves it on screen, and
	// clearing everything leaves a list of rows that are no longer marked.
	defer func() {
		if m.markedOnly {
			m.syncMarkedOnly()
		}
	}()

	switch msg.String() {
	case " ":
		// Space toggles and advances, so marking a run of adjacent rows is one
		// key repeated rather than an alternation of space and j.
		if *cur >= 0 && *cur < len(scope.keys) {
			k := scope.keys[*cur]
			if scope.marks[k] {
				delete(scope.marks, k)
			} else {
				scope.marks[k] = true
			}
			*cur++
			m.clampScroll()
		}
		return nil, true

	case "a":
		if n := scope.markAll(); n > 0 {
			m.setToast(fmt.Sprintf("marked %d more (%d total)", n, scope.markedCount()))
		} else if len(scope.keys) > 0 {
			// Pressing it again is not a mistake, but it should say that
			// nothing changed rather than appearing to do something.
			m.setToast("everything visible is already marked")
		}
		return nil, true

	case "A":
		cleared, hidden := scope.clearVisible()
		switch {
		case cleared == 0 && hidden == 0:
			m.setToast("nothing was marked")
		case hidden > 0:
			// The one case that must never be silent: marks the filter is
			// hiding survive, and the reader is about to act on them.
			m.setToast(fmt.Sprintf(
				"cleared %d · %d still marked outside the filter (A again to clear those too)",
				cleared, hidden))
		default:
			m.setToast(fmt.Sprintf("cleared %d", cleared))
		}
		// A second A, with nothing visible left to clear, takes the rest.
		if cleared == 0 && hidden > 0 {
			m.setToast(fmt.Sprintf("cleared %d hidden by the filter", scope.clearAll()))
		}
		m.visualFrom = -1
		return nil, true

	case "i":
		m.setToast(fmt.Sprintf("inverted — %d marked", scope.invert()))
		return nil, true

	case "+":
		// Additive, unlike a: the point is building a selection across several
		// filters, so this never removes anything.
		if n := scope.markAll(); n > 0 {
			m.setToast(fmt.Sprintf("added %d (%d total)", n, len(scope.marks)))
		} else {
			m.setToast("nothing new to add")
		}
		return nil, true

	case "v":
		if m.visualFrom >= 0 {
			// Ending the range marks it, including the row under the cursor.
			n := scope.markRange(m.visualFrom, *cur)
			m.visualFrom = -1
			m.setToast(fmt.Sprintf("marked %d", n))
			return nil, true
		}
		if len(scope.keys) == 0 {
			return nil, true
		}
		m.visualFrom = *cur
		// No toast: the status line carries the live row count for as long as
		// the range is open, and the footer names the two keys that end it.
		// Saying it a third time is how a status line stops being read.
		return nil, true

	case "m":
		// Show only what is marked. A selection of forty built across several
		// filters is otherwise something the reader has to take on trust.
		if len(scope.marks) == 0 {
			m.setToast("nothing is marked")
			return nil, true
		}
		m.markedOnly = !m.markedOnly
		if m.markedOnly {
			m.setToast(fmt.Sprintf("showing the %d marked %s(s) only",
				len(scope.marks), scope.noun))
		} else {
			m.setToast("showing everything again")
		}
		m.reapplyFilter()
		return nil, true
	}
	return nil, false
}

// syncMarkedOnly keeps the narrowed list honest after the selection changed.
//
// An empty selection turns the mode off rather than showing an empty list: a
// list with no rows and no explanation is a dead end, and the reader who just
// cleared their marks did not ask to be left staring at nothing.
func (m *Model) syncMarkedOnly() {
	marks := m.appMarks
	if m.screen == screenApp {
		marks = m.treeMarks
	}
	if len(marks) == 0 {
		m.markedOnly = false
		m.setToast("nothing marked — showing everything again")
	}
	m.reapplyFilter()
}

// inVisualRange reports whether a display row is inside the pending range.
func inVisualRange(m *Model, row int) bool {
	from, to, active := m.visualRange(m.listCursor())
	return active && row >= from && row <= to
}

// listCursor is the cursor of whichever list is in front.
func (m *Model) listCursor() int {
	if m.screen == screenApp {
		return m.treeCur
	}
	return m.appCur
}

// visualRange is the span the range-select is currently covering, or nothing.
//
// The rows in it are drawn as marked before they are marked, so the reader sees
// what v will take rather than committing blind.
func (m *Model) visualRange(cur int) (from, to int, active bool) {
	if m.visualFrom < 0 {
		return 0, 0, false
	}
	from, to = m.visualFrom, cur
	if from > to {
		from, to = to, from
	}
	return from, to, true
}
