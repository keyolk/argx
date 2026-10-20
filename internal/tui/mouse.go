package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/toast"
)

// Mouse is an augmentation of the keyboard, not a second command set. argx can
// sync, roll back and edit specs, so the rule that matters most here is what a
// click is not: no gesture reaches a write action or its confirmation. Each
// one resolves to a cursor move or to an action a key already runs.
//
//   - left click on a row: select it. A second click on the row that was
//     already selected runs what Enter runs where Enter only reads: open the
//     application, list an application set's apps, open a resource's manifest,
//     open a context's details. HISTORY (Enter arms a rollback), DETAILS
//     (Enter edits the spec), sync windows and scheduled syncs only select.
//   - wheel over the body: three of the screen's own down/up actions, so a
//     list moves three rows and a pager scrolls three lines.
//   - overlays own the mouse: the wheel moves their cursor where j/k does, a
//     click outside closes them as Esc does (the confirm dialog answers No),
//     and nothing reaches the screen behind. Rows inside an overlay are not
//     clickable: toggling prune or choosing a revision stays on its keys.
//   - the filter prompt and the graph view: ignored. A click never types, and
//     the graph's rows do not map one to one onto lines.
//   - right/middle click, release, motion, drag: ignored.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.tooSmall {
		return m, nil
	}
	if m.overlay != overlayNone {
		return m.overlayMouse(msg)
	}
	if m.filtering {
		return m, nil
	}
	body := m.bodyRect()
	if !body.Contains(msg.X, msg.Y) {
		return m, nil
	}

	if d := mouse.Delta(msg); d != 0 {
		act := aNavDown
		if d < 0 {
			act, d = aNavUp, -d
		}
		var cmds []tea.Cmd
		for range d {
			_, c := m.runAction(act)
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	}
	if !mouse.IsLeftPress(msg) {
		return m, nil
	}
	// A click is a keystroke for toasts: an error seen before it is read.
	m.toasts = toast.OnKey(m.toasts)

	t, ok := m.clickTarget(body, msg)
	if !ok {
		m.clicks = mouse.Clicks{} // a click on nothing breaks a double
		return m, nil
	}
	wasSelected := t.selected
	t.sel()
	var double bool
	m.clicks, double = m.clicks.Press(t.id, m.clock())
	if double && wasSelected && t.open != "" {
		return m.runAction(t.open)
	}
	return m, nil
}

// rowTarget is the row under a click: its identity, how to select it, and the
// action a second click runs ("" when the screen's Enter is not read-only).
type rowTarget struct {
	id       string
	selected bool
	sel      func()
	open     string
}

// bodyRect is where View draws the screen body: below the one-line header,
// above the status and footer lines.
func (m *Model) bodyRect() mouse.Rect {
	return mouse.Rect{X: 0, Y: 1, W: m.width, H: m.bodyHeight()}
}

// listRect is the body minus its column-header line, where one-line rows are
// drawn.
func (m *Model) listRect(body mouse.Rect) mouse.Rect {
	return mouse.Rect{X: body.X, Y: body.Y + 1, W: body.W, H: max(0, body.H-1)}
}

// clickTarget resolves a click to the row under it, using the same offsets and
// row sets the renderers draw from.
func (m *Model) clickTarget(body mouse.Rect, msg tea.MouseMsg) (rowTarget, bool) {
	list := m.listRect(body)
	switch m.screen {
	case screenApps:
		h := mouse.RowHit(list, m.appTop, len(m.appRows), msg)
		if !h.HasItem {
			return rowTarget{}, false
		}
		return rowTarget{
			id:       "app:" + m.apps[m.appRows[h.Index]].Key(),
			selected: h.Index == m.appCur,
			sel:      func() { m.appCur = h.Index },
			open:     aAppsOpen,
		}, true

	case screenAppSets:
		h := mouse.RowHit(list, m.appsetTop, len(m.appsetRows), msg)
		if !h.HasItem {
			return rowTarget{}, false
		}
		return rowTarget{
			id:       "set:" + m.appsets[m.appsetRows[h.Index]].Key(),
			selected: h.Index == m.appsetCur,
			sel:      func() { m.appsetCur = h.Index },
			open:     aSetsOpen,
		}, true

	case screenApp:
		return m.appTabTarget(body, list, msg)

	case screenWindows:
		h := mouse.RowHit(list, m.windowTop, len(m.windowRows()), msg)
		if !h.HasItem {
			return rowTarget{}, false
		}
		return rowTarget{
			id:       "window",
			selected: h.Index == m.windowCur,
			sel:      func() { m.windowCur = h.Index },
		}, true

	case screenSchedule:
		first := min(m.scheduleTop, len(m.schedules))
		h := mouse.BlockHit(list, first, m.scheduleHeights(), msg)
		if !h.HasItem {
			return rowTarget{}, false
		}
		return rowTarget{
			id:       "sched",
			selected: h.Index == m.scheduleCur,
			sel:      func() { m.scheduleCur = h.Index },
		}, true

	case screenContexts:
		if !m.ctxLoaded || m.ctxDetail {
			return rowTarget{}, false
		}
		h := mouse.RowHit(list, m.ctxTop, len(m.ctxRows), msg)
		if !h.HasItem {
			return rowTarget{}, false
		}
		return rowTarget{
			id:       "ctx:" + m.ctxRows[h.Index].name,
			selected: h.Index == m.ctxCur,
			sel:      func() { m.ctxCur = h.Index },
			open:     aCtxOpen,
		}, true
	}
	return rowTarget{}, false
}

// appTabTarget is clickTarget for the application view's three tabs.
func (m *Model) appTabTarget(body, list mouse.Rect, msg tea.MouseMsg) (rowTarget, bool) {
	switch m.tab {
	case tabResources:
		if m.treeGraphActive() {
			return rowTarget{}, false
		}
		h := mouse.RowHit(list, m.treeTop, len(m.treeRows), msg)
		if !h.HasItem {
			return rowTarget{}, false
		}
		return rowTarget{
			id:       "node:" + m.tree[m.treeRows[h.Index]].Node.UID,
			selected: h.Index == m.treeCur,
			sel:      func() { m.treeCur = h.Index },
			open:     aTreeManifest,
		}, true

	case tabHistory:
		// Enter here arms a rollback, so a second click must not run it.
		h := mouse.RowHit(list, m.histTop, len(m.histRows()), msg)
		if !h.HasItem {
			return rowTarget{}, false
		}
		return rowTarget{
			id:       "hist",
			selected: h.Index == m.histCur,
			sel:      func() { m.histCur = h.Index },
		}, true

	case tabDetails:
		// Enter here edits the spec, so a second click must not run it.
		rows := m.detailRows()
		layout := detailLayout(rows, body.H)
		line := msg.Y - body.Y
		if line < 0 || line >= len(layout) {
			return rowTarget{}, false
		}
		i := layout[line]
		if i < 0 || rows[i].kind == detailSection {
			return rowTarget{}, false
		}
		return rowTarget{
			id:       "detail",
			selected: i == m.detailCur,
			sel:      func() { m.detailCur = i },
		}, true
	}
	return rowTarget{}, false
}

// scheduleHeights is the line count of each scheduled sync as renderSchedule
// draws it: one line, plus a second when there is a reason to give.
func (m *Model) scheduleHeights() []int {
	hs := make([]int, len(m.schedules))
	for i, s := range m.schedules {
		hs[i] = 1
		if m.scheduleDetail(s) != "" {
			hs[i] = 2
		}
	}
	return hs
}

// overlayMouse keeps the overlay's ownership of the mouse: nothing reaches the
// screen behind it, and what it does is what its own keys do.
func (m *Model) overlayMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if d := mouse.Delta(msg); d != 0 {
		down, up := m.overlayWheelActions()
		act := down
		if d < 0 {
			act = up
		}
		if act == "" {
			return m, nil
		}
		for range abs(d) {
			m.handleOverlayKey(act, tea.KeyMsg{})
		}
		return m, nil
	}
	if !mouse.IsLeftPress(msg) || m.overlayRect().Contains(msg.X, msg.Y) {
		return m, nil
	}
	// Esc for the overlay: the confirm dialog answers No, never Yes.
	return m.handleOverlayKey(m.overlayCloseAction(), tea.KeyMsg{})
}

// overlayWheelActions are the actions a wheel notch stands for in the overlay,
// or "" when its keys have nothing to scroll.
func (m *Model) overlayWheelActions() (down, up string) {
	switch m.overlay {
	case overlaySyncOpts:
		return aOptDown, aOptUp
	case overlayRevPicker:
		return aRevDown, aRevUp
	case overlayContainer:
		return aPickDown, aPickUp
	case overlayPalette:
		return aPalDown, aPalUp
	}
	return "", ""
}

// overlayCloseAction is the action Esc runs in the overlay. The error overlay
// closes on any key, so it has no action name.
func (m *Model) overlayCloseAction() string {
	switch m.overlay {
	case overlayConfirm:
		return aConfirmNo
	case overlaySyncOpts:
		return aOptCancel
	case overlayRevPicker:
		return aRevCancel
	case overlayContainer:
		return aPickCancel
	case overlayPalette:
		return aPalClose
	}
	return ""
}

// overlayRect is where View centers the overlay's box (render.Center puts it at
// ((w-bw)/2, (h-bh)/2) of the frame).
func (m *Model) overlayRect() mouse.Rect {
	box := m.overlayBox()
	lines := strings.Split(box, "\n")
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	return mouse.Rect{
		X: max(0, (m.width-w)/2), Y: max(0, (m.height-len(lines))/2),
		W: w, H: len(lines),
	}
}

func (m *Model) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}
