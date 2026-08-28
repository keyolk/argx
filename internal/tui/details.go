package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The DETAILS tab is a field list: mostly read-only facts about the
// application, with a few rows that can be acted on. Making the actionable rows
// part of the same list — rather than hiding them behind separate keys — means
// the reader can see what argx is able to change without consulting the help.

// detailKind distinguishes rows the cursor can act on from plain readouts.
type detailKind int

const (
	detailStatic detailKind = iota
	// detailSection is a heading; the cursor skips it.
	detailSection
	detailRevision
	detailAutoSync
	detailAutoPrune
	detailSelfHeal
	detailTerminate
	// detailWindows opens the sync-window view.
	detailWindows
)

// detailRow is one line of the DETAILS tab.
type detailRow struct {
	kind  detailKind
	label string
	value string
	// note explains a row that cannot be acted on right now, e.g. a
	// multi-source application whose revision argx will not edit.
	note string
	// action is the hint shown when the cursor is on the row.
	action string
}

// actionable reports whether Enter does anything on this row.
func (r detailRow) actionable() bool {
	return r.kind != detailStatic && r.kind != detailSection && r.note == ""
}

// detailRows builds the DETAILS tab's content from the current application.
func (m *Model) detailRows() []detailRow {
	if m.app == nil {
		return nil
	}
	a := m.app
	src, nsrc := a.PrimarySource()
	on, prune, selfHeal := a.AutoSync()

	rows := []detailRow{
		{kind: detailSection, label: "SOURCE"},
	}

	rev := detailRow{
		kind: detailRevision, label: "target revision",
		value: orDash(src.TargetRevision), action: "enter: change",
	}
	if nsrc > 1 {
		// A merge patch cannot address one element of the sources array, so
		// argx declines rather than rewriting the wrong source.
		rev.note = fmt.Sprintf("%d sources — edit in the Argo CD UI", nsrc)
		rev.value = orDash(src.TargetRevision) + fmt.Sprintf(" (+%d more)", nsrc-1)
	}
	rows = append(rows, rev)

	rows = append(rows,
		detailRow{kind: detailStatic, label: "repo", value: orDash(src.RepoURL)},
	)
	if src.Chart != "" {
		rows = append(rows, detailRow{kind: detailStatic, label: "chart", value: src.Chart})
	} else {
		rows = append(rows, detailRow{kind: detailStatic, label: "path", value: orDash(src.Path)})
	}

	rows = append(rows,
		detailRow{kind: detailSection, label: "SYNC POLICY"},
		detailRow{
			kind: detailAutoSync, label: "auto-sync",
			value: onOff(on), action: "enter: toggle",
		},
	)

	pruneRow := detailRow{kind: detailAutoPrune, label: "  prune", value: onOff(prune), action: "enter: toggle"}
	healRow := detailRow{kind: detailSelfHeal, label: "  self-heal", value: onOff(selfHeal), action: "enter: toggle"}
	if !on {
		// The flags exist only inside an automated policy; offering them while
		// auto-sync is off would silently create one.
		pruneRow.note = "auto-sync is off"
		healRow.note = "auto-sync is off"
		pruneRow.value, healRow.value = "—", "—"
	}
	rows = append(rows, pruneRow, healRow)

	// The schedule sits with the sync policy because it answers the same
	// question — when does this application change — and a reader looking at
	// auto-sync is one row away from finding out a window blocks it anyway.
	win := detailRow{kind: detailWindows, label: "sync windows", action: "enter: view"}
	switch text, blocked := m.windowSummary(); {
	case text == "":
		win.value = m.st.dim.Render("not loaded")
	case blocked:
		win.value = m.st.err.Render(text)
	default:
		win.value = text
	}
	rows = append(rows, win)

	rows = append(rows,
		detailRow{kind: detailSection, label: "DESTINATION"},
		detailRow{kind: detailStatic, label: "cluster", value: a.Spec.Destination.Cluster()},
		detailRow{kind: detailStatic, label: "namespace", value: orDash(a.Spec.Destination.Namespace)},
		detailRow{kind: detailStatic, label: "project", value: orDash(a.Spec.Project)},

		detailRow{kind: detailSection, label: "STATUS"},
		detailRow{kind: detailStatic, label: "sync", value: a.Status.Sync.Status},
		detailRow{kind: detailStatic, label: "health", value: a.Status.Health.Status},
		detailRow{kind: detailStatic, label: "synced revision", value: shortRev(a.Status.Sync.Revision)},
	)

	if a.Status.Health.Message != "" {
		rows = append(rows, detailRow{kind: detailStatic, label: "health message", value: a.Status.Health.Message})
	}

	if op := a.Status.OperationState; op != nil {
		rows = append(rows, detailRow{kind: detailStatic, label: "last operation", value: op.Phase})
		if op.Message != "" {
			rows = append(rows, detailRow{kind: detailStatic, label: "  message", value: op.Message})
		}
		term := detailRow{kind: detailTerminate, label: "  terminate", value: "", action: "enter: stop this sync"}
		if op.Phase != "Running" && op.Phase != "Terminating" {
			term.note = "no sync in progress"
		}
		rows = append(rows, term)
	}

	for _, c := range a.Status.Conditions {
		rows = append(rows, detailRow{kind: detailStatic, label: "condition " + c.Type, value: c.Message})
	}

	if !a.Status.ReconciledAt.IsZero() {
		rows = append(rows, detailRow{
			kind: detailStatic, label: "reconciled",
			value: a.Status.ReconciledAt.Local().Format("2006-01-02 15:04:05"),
		})
	}
	return rows
}

// moveDetail advances the cursor, skipping section headings so the cursor never
// lands somewhere Enter does nothing.
func (m *Model) moveDetail(d int) {
	rows := m.detailRows()
	if len(rows) == 0 {
		return
	}
	step := 1
	if d < 0 {
		step = -1
	}
	for n := 0; n < abs(d); n++ {
		i := m.detailCur
		for {
			i += step
			if i < 0 || i >= len(rows) {
				// Past either end: stay where the last valid row was rather
				// than wrapping, which loses the reader's place.
				i = m.detailCur
				break
			}
			if rows[i].kind != detailSection {
				break
			}
		}
		m.detailCur = i
	}
}

// handleDetailEnter acts on the row under the cursor.
func (m *Model) handleDetailEnter() (tea.Model, tea.Cmd) {
	rows := m.detailRows()
	if m.detailCur < 0 || m.detailCur >= len(rows) || m.app == nil {
		return m, nil
	}
	row := rows[m.detailCur]
	if row.note != "" {
		m.setToast(row.note)
		return m, nil
	}

	a := m.app
	on, prune, selfHeal := a.AutoSync()

	switch row.kind {
	case detailRevision:
		return m, m.openRevPicker()

	case detailAutoSync:
		// Turning auto-sync ON is the change that can move a cluster on its
		// own, so only that direction confirms. Turning it off is how you stop
		// a cluster from changing, and making that slow helps nobody.
		if on {
			return m, m.setAutoSyncCmd(false, prune, selfHeal)
		}
		m.confirm = confirmState{
			title: "Enable auto-sync?",
			body: []string{
				a.Name(),
				"",
				"The controller will sync this application to git automatically,",
				"including changes already waiting.",
			},
			action: func() tea.Cmd { return m.setAutoSyncCmd(true, prune, selfHeal) },
		}
		m.overlay = overlayConfirm
		return m, nil

	case detailAutoPrune:
		if !prune {
			m.confirm = confirmState{
				title: "Enable prune?",
				body: []string{
					a.Name(),
					"",
					"Auto-sync will DELETE resources that are no longer in git.",
				},
				action: func() tea.Cmd { return m.setAutoSyncCmd(true, true, selfHeal) },
			}
			m.overlay = overlayConfirm
			return m, nil
		}
		return m, m.setAutoSyncCmd(true, false, selfHeal)

	case detailSelfHeal:
		return m, m.setAutoSyncCmd(true, prune, !selfHeal)

	case detailWindows:
		m.push(screenWindows)
		return m, m.loadWindowsCmd(*a)

	case detailTerminate:
		m.confirm = confirmState{
			title: "Terminate the running sync?",
			body: []string{
				a.Name(),
				"",
				"The application is left partially applied at whatever the sync",
				"had reached.",
			},
			action: func() tea.Cmd { return m.terminateCmd() },
		}
		m.overlay = overlayConfirm
		return m, nil
	}
	return m, nil
}

// ---- revision picker ----

// openRevPicker loads the repository's branches and tags.
func (m *Model) openRevPicker() tea.Cmd {
	if m.app == nil {
		return nil
	}
	if !m.app.SingleSource() {
		m.setToast("multi-source application — edit in the Argo CD UI")
		return nil
	}
	m.revPicker = revPickerState{loading: true}
	m.overlay = overlayRevPicker
	return m.loadRefsCmd(*m.app)
}

// applyRevFilter recomputes the picker's visible rows.
func (m *Model) applyRevFilter() {
	p := &m.revPicker
	p.rows = p.rows[:0]
	q := strings.ToLower(p.filter)
	for i, it := range p.items {
		if q == "" || strings.Contains(strings.ToLower(it.name), q) {
			p.rows = append(p.rows, i)
		}
	}
	p.cur, p.top = 0, 0
}

// revPickerHeight is how many rows the picker shows, bounded so the modal never
// outgrows the terminal.
func (m *Model) revPickerHeight() int {
	h := m.height - 10
	if h > 14 {
		h = 14
	}
	if h < 3 {
		h = 3
	}
	return h
}

// currentRev is the revision under the picker's cursor.
func (m *Model) currentRev() (revItem, bool) {
	p := &m.revPicker
	if p.cur < 0 || p.cur >= len(p.rows) {
		return revItem{}, false
	}
	return p.items[p.rows[p.cur]], true
}

// handleRevPickerKey drives the picker. It has its own filter rather than
// reusing the screen filter: the screen behind it keeps its own query, and
// clobbering that on Esc would be a surprise.
func (m *Model) handleRevPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.revPicker
	h := m.revPickerHeight()

	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		return m, nil
	case "ctrl+n", "down":
		p.cur++
	case "ctrl+p", "up":
		p.cur--
	case "pgdown":
		p.cur += h / 2
	case "pgup":
		p.cur -= h / 2
	case "backspace":
		if p.filter != "" {
			r := []rune(p.filter)
			p.filter = string(r[:len(r)-1])
			m.applyRevFilter()
		}
	case "enter":
		it, ok := m.currentRev()
		if !ok {
			return m, nil
		}
		return m, m.armRevisionChange(it)
	default:
		// Everything else types into the filter: the picker exists to narrow a
		// long branch list, so typing must filter rather than navigate.
		if len(msg.Runes) > 0 {
			p.filter += string(msg.Runes)
			m.applyRevFilter()
			return m, nil
		}
	}

	if p.cur >= len(p.rows) {
		p.cur = len(p.rows) - 1
	}
	if p.cur < 0 {
		p.cur = 0
	}
	if p.cur < p.top {
		p.top = p.cur
	}
	if p.cur >= p.top+h {
		p.top = p.cur - h + 1
	}
	if p.top < 0 {
		p.top = 0
	}
	return m, nil
}

// armRevisionChange confirms the revision change before it is applied.
func (m *Model) armRevisionChange(it revItem) tea.Cmd {
	if m.app == nil {
		return nil
	}
	a := m.app
	from := orDash(a.TargetRevision())
	on, _, _ := a.AutoSync()

	body := []string{
		a.Name(),
		"",
		"  from  " + from,
		"  to    " + it.name + "  (" + it.kind + ")",
	}
	if on {
		// This is the whole point of the workflow: pointing a live,
		// auto-syncing application at a branch deploys that branch as soon as
		// the controller notices.
		body = append(body, "",
			"auto-sync is ON — the controller will deploy this",
			"revision without a further confirmation.")
	}

	m.confirm = confirmState{
		title:  "Change target revision?",
		body:   body,
		action: func() tea.Cmd { return m.setRevisionCmd(it.name) },
	}
	m.overlay = overlayConfirm
	return nil
}

// ---- history actions ----

// armRollback confirms a rollback to a past deployment.
func (m *Model) armRollback() tea.Cmd {
	h := m.currentHistory()
	if h == nil || m.app == nil {
		return nil
	}
	a := m.app
	on, _, _ := a.AutoSync()

	body := []string{
		a.Name(),
		"",
		fmt.Sprintf("  to revision %s (id %d)", shortRev(h.Rev()), h.ID),
		"  deployed " + h.DeployedAt.Local().Format("2006-01-02 15:04:05") + " by " + h.Who(),
	}
	if on {
		// Argo CD rejects this outright; saying so up front beats letting the
		// user confirm a destructive-sounding action just to get an error.
		body = append(body, "",
			"auto-sync is ON — Argo CD will refuse the rollback.",
			"Turn auto-sync off in DETAILS first.")
	}

	m.confirm = confirmState{
		title:  "Roll back?",
		body:   body,
		action: func() tea.Cmd { return m.rollbackCmd(h.ID) },
	}
	m.overlay = overlayConfirm
	return nil
}

// ---- small helpers ----

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
