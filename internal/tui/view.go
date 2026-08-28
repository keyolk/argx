package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	case screenApp:
		body = m.renderAppTab()
	case screenWindows:
		body = m.renderWindows()
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
		return m.renderOverlay(frame)
	}
	return frame
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
		if t == m.tab {
			parts = append(parts, m.st.selected.Render(label))
			continue
		}
		parts = append(parts, m.st.dim.Render(label))
	}
	return strings.Join(parts, m.st.dim.Render(m.gl.tabSep))
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
		"   ID   "+padRight("DEPLOYED", 20)+" "+padRight("REVISION", 12)+" BY", m.width)))

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
			m.st.dim.Render(padRight(fmt.Sprint(e.ID), 4)) + " " +
			style.Render(padRight(when, 20)) + " " +
			m.st.info.Render(padRight(rev, 12)) + " " +
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
	for i := 0; i < len(rows) && len(lines) < h; i++ {
		r := rows[i]
		if r.kind == detailSection {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			if len(lines) < h {
				lines = append(lines, m.st.header.Render(truncate(r.label, m.width)))
			}
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
			m.st.dim.Render(padRight(truncate(r.label, labelW), labelW)) + "  " +
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
