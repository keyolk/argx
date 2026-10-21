package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/render"
)

// View renders the whole frame. Pure: no I/O, no state mutation.
func (m *Model) View() string {
	if m.tooSmall {
		return m.renderTooSmall()
	}

	var body string
	switch m.screen {
	case screenApps:
		body = m.renderApps()
	case screenAppSets:
		body = m.renderAppSets()
	case screenApp:
		body = m.renderAppTab()
	case screenWindows:
		body = m.renderWindows()
	case screenSchedule:
		body = m.renderSchedule()
	case screenContexts:
		body = m.renderContexts()
	case screenHelp:
		body = m.renderHelp()
	default:
		body = m.renderPager()
	}

	frame := lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		body,
		m.renderStatus(),
		m.renderFooter(),
	)

	if m.overlay != overlayNone {
		frame = m.renderOverlay(frame)
	}
	return m.textSel.Render(frame, m.selPanes(), m.st.selection)
}

// renderTooSmall is what the user sees below the minimum size — an explicit
// message rather than a garbled layout.
func (m *Model) renderTooSmall() string {
	msg := fmt.Sprintf("terminal too small\n\n%d×%d\nneed at least %d×%d",
		m.width, m.height, minWidth, minHeight)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		m.st.warn.Render(msg))
}

func (m *Model) renderHeader() string {
	var left string
	switch m.screen {
	case screenApps:
		left = m.st.title.Render("argx") + m.st.dim.Render(" · applications")
	case screenAppSets:
		left = m.st.title.Render("argx") + m.st.dim.Render(" · application sets")
	case screenApp:
		name := ""
		if m.app != nil {
			name = m.app.Name()
		}
		// The application name stays put and the tabs sit beside it, so
		// switching tabs never moves what identifies the thing being edited.
		// The server prefix stays with it: which Argo CD a spec edit lands on
		// is not something to leave to the reader's memory.
		left = m.st.accent.Render(name) + "  " + m.renderTabBar()
		if m.multiServer() && m.app != nil {
			left = m.ctxStyle(m.app.Context).Render(m.app.Context) +
				m.st.dim.Render(" · ") + left
		}
	case screenWindows:
		name := ""
		if m.app != nil {
			name = m.app.Spec.Project
		}
		left = m.st.title.Render("argx") + m.st.dim.Render(" · sync windows · ") +
			m.st.accent.Render(name)
	case screenSchedule:
		left = m.st.title.Render("argx") + m.st.dim.Render(" · scheduled syncs")
	case screenContexts:
		left = m.st.title.Render("argx") + m.st.dim.Render(" · contexts")
		if m.ctxDetail {
			if r := m.currentContext(); r != nil {
				left += m.st.dim.Render(" · ") + m.ctxStyle(r.name).Render(r.name)
			}
		}
	case screenHelp:
		left = m.st.title.Render("argx") + m.st.dim.Render(" · help")
	default:
		left = m.st.title.Render("argx") + m.st.dim.Render(" · ") + m.st.accent.Render(m.pagerTitle)
	}

	// With one server the header names it; with several the name belongs on
	// each row instead, and the header reports the fleet's size and health.
	var right string
	switch {
	case !m.multiServer():
		right = m.ctxStyle(m.fleet.Names()[0]).Render(m.fleet.Names()[0])
	case len(m.fleetErrs) > 0:
		right = m.st.err.Render(fmt.Sprintf("%d/%d servers",
			len(m.fleet.Names())-len(m.fleetErrs), len(m.fleet.Names())))
	default:
		right = m.st.dim.Render(fmt.Sprintf("%d servers", len(m.fleet.Names())))
	}
	if m.autoRefresh {
		right = m.st.warn.Render("auto ") + right
	}

	gapW := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gapW < 1 {
		// Drop the context name rather than wrapping the header onto a second
		// line, which would shift the body and break spatial memory.
		return truncate(left, m.width)
	}
	return left + strings.Repeat(" ", gapW) + right
}

// renderTabBar draws the three tab labels with the active one highlighted.
//
// The label set never changes and the active tab is marked by weight and
// color rather than by adding a marker, so the bar keeps a constant width and
// nothing beside it shifts.
func (m *Model) renderTabBar() string {
	parts := make([]string, 0, len(allTabs))
	for i, t := range allTabs {
		// The number is always shown — it is the key that selects the tab, and
		// an icon that replaced it would hide the binding.
		label := fmt.Sprintf("%d %s%s", i+1, m.gl.prefix(m.gl.tabIcon(t)), t)
		style := m.st.dim
		if t == m.tab {
			style = m.st.selected
		}
		// The badge is styled on its own, after the label: what a tab holds is
		// worth seeing before switching to it, and its color is the point.
		parts = append(parts, style.Render(label)+m.tabBadge(t))
	}
	return strings.Join(parts, m.st.dim.Render(m.gl.tabSep))
}

// tabBadge is what a tab holds, shown on the tab itself so the reader knows
// before switching: RESOURCES counts resources out of sync, HISTORY counts
// deployments, DETAILS marks a degraded application. Empty when there is
// nothing to say — a badge reading 0 is noise.
func (m *Model) tabBadge(t tab) string {
	if m.app == nil {
		return ""
	}
	switch t {
	case tabResources:
		if n := m.app.OutOfSyncCount(); n > 0 {
			return " " + m.st.warn.Render(fmt.Sprintf("%s%d", m.gl.outOfSync, n))
		}
	case tabHistory:
		if n := len(m.app.Status.History); n > 0 {
			return " " + m.st.dim.Render(fmt.Sprintf("%d", n))
		}
	case tabDetails:
		if m.app.Status.Health.Status == "Degraded" {
			return " " + m.st.err.Render(m.gl.no)
		}
	}
	return ""
}

// renderAppTab dispatches to the active tab's renderer.
func (m *Model) renderAppTab() string {
	switch m.tab {
	case tabHistory:
		return m.renderHistory()
	case tabDetails:
		return m.renderDetails()
	default:
		return m.renderTree()
	}
}

// renderHistory lists past deployments, newest first.
func (m *Model) renderHistory() string {
	h := m.bodyHeight()
	rows := m.histRows()
	if len(rows) == 0 {
		txt := "no deploy history"
		if m.loading {
			txt = "loading…"
		}
		return m.emptyBody(h, txt)
	}

	lines := make([]string, 0, h)
	lines = append(lines, m.st.header.Render(truncate(
		"   ID   "+render.PadRight("DEPLOYED", 20)+" "+render.PadRight("REVISION", 12)+" BY", m.width)))

	for r := m.histTop; r < len(rows) && len(lines) < h; r++ {
		e := rows[r]
		cursor := " "
		if r == m.histCur {
			cursor = m.st.accent.Render(m.gl.cursor)
		}
		// The newest entry is what is live now; saying so beats making the
		// reader infer it from the ordering.
		suffix := ""
		if r == 0 {
			suffix = m.st.success.Render("  ← current")
		}

		style := lipgloss.NewStyle()
		if r == m.histCur {
			style = m.st.selected
		}

		when := "—"
		if !e.DeployedAt.IsZero() {
			when = e.DeployedAt.Local().Format("2006-01-02 15:04")
		}
		when = m.gl.prefix(m.gl.clock) + when
		rev := m.gl.prefix(m.gl.revision) + shortRev(e.Rev())
		who := m.gl.prefix(m.gl.person) + e.Who()

		line := cursor + "  " +
			m.st.dim.Render(render.PadRight(fmt.Sprint(e.ID), 4)) + " " +
			style.Render(render.PadRight(when, 20)) + " " +
			m.st.info.Render(render.PadRight(rev, 12)) + " " +
			m.st.dim.Render(who) + suffix
		lines = append(lines, truncate(line, m.width))
	}
	return padBody(lines, h)
}

// renderDetails lists the application's spec and status fields, with the
// actionable ones marked.
func (m *Model) renderDetails() string {
	h := m.bodyHeight()
	rows := m.detailRows()
	if len(rows) == 0 {
		txt := "loading…"
		if !m.loading {
			txt = "no application loaded"
		}
		return m.emptyBody(h, txt)
	}

	// The label column is sized to the content so values line up, and capped so
	// one long label cannot push every value off a narrow terminal.
	labelW := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.label); w > labelW {
			labelW = w
		}
	}
	if max := m.width / 3; labelW > max {
		labelW = max
	}

	lines := make([]string, 0, h)
	for _, i := range detailLayout(rows, h) {
		if i < 0 {
			lines = append(lines, "")
			continue
		}
		r := rows[i]
		if r.kind == detailSection {
			lines = append(lines, m.st.header.Render(truncate(r.label, m.width)))
			continue
		}

		cursor := " "
		if i == m.detailCur {
			cursor = m.st.accent.Render(m.gl.cursor)
		}
		// A row argx can change is marked in the row itself, so what is
		// editable is visible without moving the cursor over everything.
		edit := " "
		if r.actionable() {
			edit = m.st.mark.Render(m.gl.editable)
		}

		valStyle := lipgloss.NewStyle()
		switch {
		case r.note != "":
			valStyle = m.st.dim
		case i == m.detailCur:
			valStyle = m.st.selected
		}

		line := cursor + edit + " " +
			m.st.dim.Render(render.PadRight(truncate(r.label, labelW), labelW)) + "  " +
			valStyle.Render(r.value)
		if r.note != "" {
			line += m.st.dim.Render("  (" + r.note + ")")
		} else if i == m.detailCur && r.action != "" {
			line += m.st.dim.Render("   " + r.action)
		}
		lines = append(lines, truncate(line, m.width))
	}
	return padBody(lines, h)
}

// renderApps is the application list: mark, status letters, name, project,
// destination, revision.

// detailLayout maps each screen line of the DETAILS tab to the row drawn on it,
// or -1 for the blank line that sets a section off. renderDetails draws from
// it and the mouse handler hit-tests against it, so the two cannot disagree
// about where a row is.
func detailLayout(rows []detailRow, h int) []int {
	var out []int
	for i, r := range rows {
		if len(out) >= h {
			break
		}
		if r.kind == detailSection && len(out) > 0 {
			out = append(out, -1)
		}
		if len(out) < h {
			out = append(out, i)
		}
	}
	return out
}
