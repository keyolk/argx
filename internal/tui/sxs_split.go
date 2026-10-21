package tui

import (
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/split"
)

// The side-by-side diff's column boundary is resizable (tuikit split). The
// columns were equal halves; a manifest whose desired side carries long
// image references reads better with that side wider, and the reverse for a
// live side full of status.
//
// renderSideBySide sizes the columns from sxsLayout, and the drag selection
// and the mouse use the same rects. Header and hunk rows still span the full
// width over the divider: they describe the comparison, not a side.

// sxsSplitModel is m.sxsSplit configured. The configuration is applied on
// every use, so a test's Model lays out like the program. The look is the
// old gutter: three cells, ` │ `. Each side keeps 30 cells, where a manifest
// line is still more than its indentation.
func (m *Model) sxsSplitModel() split.Model {
	s := m.sxsSplit
	s.Orientation = split.Horizontal
	s.Divider = 3
	s.MinA, s.MinB = 30, 30
	s.Default = 0.5
	return s
}

// sxsActive reports that the diff is on screen in two columns.
func (m *Model) sxsActive() bool {
	return m.screen == screenDiff && m.sxs && m.sxsAvailable() && !m.tooSmall
}

// sxsLayout is the left column, the gutter and the right column in the body.
func (m *Model) sxsLayout() (left, gutter, right mouse.Rect) {
	return m.sxsSplitModel().Layout(m.bodyRect())
}

// sxsGutterRow is one row of the gutter as the split draws it: ` │ ` (heavy
// while dragged), dim like the old gutter.
func (m *Model) sxsGutterRow(w int) string {
	s := m.sxsSplitModel()
	return split.Divider(s.Orientation, mouse.Rect{W: w, H: 1}, m.st.dim, s.Dragging())
}

// sxsBindings are the kit's `>` grow, `<` shrink, `=` reset in the diff
// view, footer last and only while two columns show (actionVisible).
func sxsBindings() []keys.Binding {
	// Shrink first, so the grouped hint reads "<> columns", left key first.
	order := map[string]int{split.ActionShrink: 0, split.ActionGrow: 1, split.ActionReset: 2}
	bs := split.Bindings(ctxDiff)
	slices.SortFunc(bs, func(a, b keys.Binding) int { return order[a.Action] - order[b.Action] })
	for i := range bs {
		bs[i].Group = "diffs"
		bs[i].Help = map[string]string{
			split.ActionGrow:   "side by side: widen the left (live) column",
			split.ActionShrink: "side by side: narrow the left (live) column",
			split.ActionReset:  "side by side: equal columns",
		}[bs[i].Action]
		if bs[i].Action != split.ActionReset {
			bs[i].Footer, bs[i].FooterGroup = 9, "columns"
		}
	}
	return bs
}

// handleSplitKey moves the column boundary by one key step. Off the
// two-column view, or at a limit, it says so: a key that silently does
// nothing is a key people stop trusting.
func (m *Model) handleSplitKey(action string) (tea.Model, tea.Cmd) {
	if !m.sxsActive() {
		m.setToast("columns resize in the side-by-side diff (s)")
		return m, nil
	}
	var r split.Result
	m.sxsSplit, r = m.sxsSplitModel().Handle(action, m.bodyRect())
	if !r.Changed {
		m.setToast(fmt.Sprintf("columns at their limit (%d cells each at least)", m.sxsSplitModel().MinA))
	}
	// The ratio is in uiState: Update's persistUI queues the write.
	return m, nil
}

// splitMouse gives a mouse event to the column split before the selection
// and the click handler (the kit's order), and reports whether it took it:
// a press on the gutter, the drag that follows, its release.
func (m *Model) splitMouse(msg tea.MouseMsg) bool {
	if !m.sxsActive() || m.overlay != overlayNone || m.filtering {
		return false
	}
	var r split.Result
	m.sxsSplit, r = m.sxsSplitModel().Update(msg, m.bodyRect())
	if r.Consumed && mouse.IsLeftPress(msg) {
		// The selection never sees this press, so it would keep its
		// highlight over cells the resize is about to move.
		m.textSel.Clear()
	}
	return r.Consumed
}
