package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/selection"
)

// Drag selection (E14): a drag selects text inside the pane under the press
// and the release copies it, the way the mod engine selects. Mouse capture
// turned the terminal's own drag-select off, and a manifest field, a log line
// or a revision is what users most want to copy out of argx.
//
//   - lists (applications, sets, tree, history, details, windows, schedule,
//     contexts) are List panes: a second click on the selected row stays what
//     it was (Enter where Enter only reads, nothing elsewhere).
//   - pagers (diff, manifest, logs, events, help, a context's detail) are Text
//     panes with their whole content, so a drag past the top or bottom
//     scrolls them (pagerTop is set to the kit's offset), and a drag resting
//     there keeps scrolling.
//     The side-by-side diff is two Text panes read from the frame: a header
//     row spans both columns, so neither column has one line per row to give.
//   - the resource graph is a Text pane read from the frame.
//   - an open overlay is the only thing selectable: its content is one Text
//     pane. Overlay rows are not clickable, so nothing there needs a List.
//
// Selection never reaches a write: the copy is the only side effect.

// selectionMouse feeds a mouse event or an auto-scroll tick to the selection
// before the click handler. A press that does not move is left to
// handleMouse, so click-to-select still works; only what the selection
// consumed (a drag, its release, a word pick) skips it.
func (m *Model) selectionMouse(msg tea.Msg) (tea.Model, tea.Cmd) {
	r, cmd := m.textSel.Update(msg, m.selPanes, m.View, m.clock())
	if r.Scroll != 0 && r.ScrollPane == pagerPaneID {
		// Set, not stepped through nav.down/nav.up: the kit computed the
		// offset from the one the pane reported, and every pager's renderer
		// clamps pagerTop again on the next frame.
		m.pagerTop = r.Offset
	}
	mm, isMouse := msg.(tea.MouseMsg)
	if r.Consumed || !isMouse {
		return m, cmd
	}
	_, click := m.handleMouse(mm)
	return m, tea.Batch(cmd, click)
}

// copied reports the clipboard write: a copy is silent otherwise, and a
// failed one (no pbcopy, no /dev/tty) must not look like a success.
func (m *Model) copied(msg selection.CopiedMsg) {
	if msg.Err != nil {
		m.setToastErr(msg.Note())
		return
	}
	m.setToast(msg.Note())
}

const pagerPaneID = "pager"

// selPanes lists the selectable regions in drawing order, from the numbers
// View and the click handler use (bodyRect, overlayRect).
func (m *Model) selPanes() []selection.Pane {
	if m.tooSmall || m.width == 0 {
		return nil
	}
	if m.overlay != overlayNone {
		return m.overlayPanes()
	}
	body := m.bodyRect()
	text := func(lines []string, offset int) []selection.Pane {
		if len(lines) == 0 {
			// The "loading…" / "(empty)" body has nothing to scroll.
			return []selection.Pane{{ID: pagerPaneID, Rect: body, Kind: selection.Text}}
		}
		return []selection.Pane{{ID: pagerPaneID, Rect: body, Kind: selection.Text, Lines: lines, Offset: offset}}
	}
	switch m.screen {
	case screenHelp:
		return text(m.helpLines(), m.pagerTop)
	case screenDiff, screenManifest, screenLogs, screenEvents:
		if m.sxsActive() {
			return m.sxsPanes()
		}
		return text(m.pagerLines(), m.pagerTop)
	case screenContexts:
		if m.ctxDetail {
			return m.contextDetailPanes(body)
		}
	case screenApp:
		if m.tab == tabResources && m.treeGraphActive() {
			return []selection.Pane{{ID: "graph", Rect: body, Kind: selection.Text}}
		}
	}
	return []selection.Pane{{ID: "list", Rect: body, Kind: selection.List}}
}

// contextDetailPanes is a context's detail panel with all of its lines.
// renderContextDetail spends the last row on "… N more" while lines remain
// below; that row is not a detail line, so it is left out of the pane and a
// drag onto it scrolls instead of selecting the hidden line under it.
func (m *Model) contextDetailPanes(body mouse.Rect) []selection.Pane {
	lines := m.contextDetailLines()
	if lines == nil {
		return []selection.Pane{{ID: pagerPaneID, Rect: body, Kind: selection.Text}}
	}
	// The panel draws the marker whenever it is taller than the body.
	if len(lines) > body.H {
		body.H--
	}
	return []selection.Pane{{ID: pagerPaneID, Rect: body, Kind: selection.Text, Lines: lines, Offset: m.pagerTop}}
}

// sxsPanes are the two columns of renderSideBySide, split by its gutter: the
// column split's rects, which renderSideBySide draws in.
func (m *Model) sxsPanes() []selection.Pane {
	left, _, right := m.sxsLayout()
	return []selection.Pane{
		{ID: "diff-left", Rect: left, Kind: selection.Text},
		{ID: "diff-right", Rect: right, Kind: selection.Text},
	}
}

// overlayPanes is the overlay's content inside its border and padding.
func (m *Model) overlayPanes() []selection.Pane {
	box := m.overlayRect()
	if box.W == 0 {
		return nil
	}
	content := box.InsetStyle(m.st.modal) // modalErr has the same frame
	return []selection.Pane{{ID: "overlay", Rect: content, Kind: selection.Text}}
}
