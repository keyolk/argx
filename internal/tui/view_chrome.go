package tui

// The status line, the footer hints, and the modal overlays — the frame around
// whatever body is being rendered.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderStatus is the one-line state readout above the footer: counts, marks,
// filter, and the transient toast.
func (m *Model) renderStatus() string {
	var parts []string

	switch m.screen {
	case screenApps:
		parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d/%d apps", len(m.appRows), len(m.apps))))
		if n := len(m.fleetErrs); n > 0 {
			// Named, not counted: "1 server failed" leaves the reader wondering
			// whether the application they came for is missing or simply on the
			// server that did not answer.
			names := make([]string, 0, n)
			for _, e := range m.fleetErrs {
				names = append(names, e.Context)
			}
			parts = append(parts,
				m.st.err.Render("unreachable: "+strings.Join(names, ", ")+" (E)"))
		}
		if n := len(m.appMarks); n > 0 {
			parts = append(parts, m.st.mark.Render(fmt.Sprintf("%d marked", n)))
		}
		if !m.appFilter.empty() || m.filtering {
			parts = append(parts, m.renderFilter(m.appFilter.String()))
			if m.filtering {
				// The candidate list replaces the field hint when there is one:
				// the reader just asked what the options are, and showing both
				// buries the answer.
				if len(m.completionHint) > 0 {
					parts = append(parts, m.st.accent.Render(
						joinCandidates(m.completionHint, m.width/2)))
				} else {
					parts = append(parts, m.st.dim.Render(appFilterHint))
				}
			}
		}
	case screenAppSets:
		broken := 0
		for _, i := range m.appsetRows {
			if m.appsets[i].Degraded() {
				broken++
			}
		}
		parts = append(parts, m.st.dim.Render(
			fmt.Sprintf("%d/%d sets", len(m.appsetRows), len(m.appsets))))
		if broken > 0 {
			parts = append(parts, m.st.err.Render(fmt.Sprintf("%d with errors", broken)))
		}
		if n := len(m.fleetErrs); n > 0 {
			names := make([]string, 0, n)
			for _, e := range m.fleetErrs {
				names = append(names, e.Context)
			}
			parts = append(parts, m.st.err.Render("unreachable: "+strings.Join(names, ", ")+" (E)"))
		}
		if strings.TrimSpace(m.appsetFilter) != "" || m.filtering {
			parts = append(parts, m.renderFilter(m.appsetFilter))
			if m.filtering {
				parts = append(parts, m.st.dim.Render(appsetFilterHint))
			}
		}

	case screenApp:
		switch m.tab {
		case tabResources:
			parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d/%d resources", len(m.treeRows), len(m.tree))))
			if n := len(m.treeMarks); n > 0 {
				parts = append(parts, m.st.mark.Render(fmt.Sprintf("%d marked", n)))
			}
		case tabHistory:
			parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d deployments", len(m.histRows()))))
		case tabDetails:
			// The marker is named by showing it, not by spelling it: the glyph
			// differs per icon set, and a hardcoded "*" would disagree with the
			// rows it describes.
			parts = append(parts, m.st.dim.Render(
				m.st.mark.Render(m.gl.editable)+m.st.dim.Render(" rows are editable")))
		}
		if m.app != nil {
			a := m.app
			parts = append(parts,
				m.st.syncStyle(a.Status.Sync.Status).Render(a.Status.Sync.Status),
				m.st.healthStyle(a.Status.Health.Status).Render(a.Status.Health.Status))
			// A blocked sync is worth carrying on every tab: pressing `s` and
			// getting a rejection is a worse way to find out.
			if m.windows != nil && !m.windows.CanSync {
				parts = append(parts, m.st.err.Render("sync window: BLOCKED"))
			}
			if on, prune, selfHeal := a.AutoSync(); on {
				s := "auto"
				if prune {
					s += "+prune"
				}
				if selfHeal {
					s += "+heal"
				}
				parts = append(parts, m.st.dim.Render(s))
			}
		}
		if m.tab == tabResources && (!m.treeFilt.empty() || m.filtering) {
			parts = append(parts, m.renderFilter(m.treeFilt.String()))
			if m.filtering {
				parts = append(parts, m.st.dim.Render(resourceFilterHint))
			}
		}
	case screenWindows:
		rows := m.windowRows()
		applies := 0
		for _, r := range rows {
			if r.applies {
				applies++
			}
		}
		parts = append(parts, m.st.dim.Render(
			fmt.Sprintf("%d windows · %d apply here", len(rows), applies)))
		if m.windows != nil {
			if m.windows.CanSync {
				parts = append(parts, m.st.success.Render("syncing allowed now"))
			} else {
				parts = append(parts, m.st.err.Render("syncing BLOCKED now"))
			}
		}
		if m.app != nil {
			parts = append(parts, m.st.dim.Render(m.app.Name()))
		}

	default:
		total := len(m.pagerLines())
		parts = append(parts, m.st.dim.Render(fmt.Sprintf("line %d/%d", min(m.pagerTop+1, total), total)))
		if m.pagerFilt != "" || m.filtering {
			parts = append(parts, m.renderFilter(m.pagerFilt))
		}
	}

	if m.loading {
		parts = append(parts, m.st.warn.Render("… "+m.loadWhat))
	}
	// The toast lives for a few seconds; it is checked here rather than cleared
	// on a timer so no tick is needed just to expire it.
	if m.toast != "" && time.Since(m.toastAt) < 5*time.Second {
		parts = append(parts, m.st.success.Render(m.toast))
	}

	// Each part carries its own style already; the separator is styled on its
	// own rather than wrapping the joined string, which would nest escape
	// sequences and lose the inner colors after the first reset.
	return truncate(strings.Join(parts, m.st.dim.Render(m.gl.sep)), m.width)
}

// joinCandidates renders a completion list, bounded so it cannot push the rest
// of the status line off screen.
func joinCandidates(cands []string, budget int) string {
	if budget < 20 {
		budget = 20
	}
	var b strings.Builder
	for i, c := range cands {
		next := c
		if i > 0 {
			next = "  " + c
		}
		if lipgloss.Width(b.String())+lipgloss.Width(next) > budget {
			return b.String() + fmt.Sprintf("  +%d more", len(cands)-i)
		}
		b.WriteString(next)
	}
	return b.String()
}

// renderFilter draws the query with the text cursor at its actual position.
//
// The cursor is a reverse-video cell over the character it sits on rather than
// a bar appended to the end: a bar that always trailed the query said nothing
// about where an edit would land, which is the whole point of being able to
// move it.
func (m *Model) renderFilter(q string) string {
	if !m.filtering {
		return m.st.filter.Render("/" + q)
	}

	r := []rune(q)
	i := m.filterCur
	if i > len(r) {
		i = len(r)
	}
	if i < 0 {
		i = 0
	}

	// At the end of the query the cursor covers a space, so it stays visible
	// rather than vanishing past the last character.
	under := " "
	after := ""
	if i < len(r) {
		under = string(r[i])
		after = string(r[i+1:])
	}
	return m.st.filter.Render("/"+string(r[:i])) +
		m.st.cursorCell.Render(under) +
		m.st.filter.Render(after)
}

// renderFooter is the always-visible hint line, per screen.
func (m *Model) renderFooter() string {
	var hints []string
	switch m.screen {
	case screenApps:
		hints = []string{"space mark", "o browser", "d diff", "s sync", "S appsets", "/ filter", "? help", "q quit"}
	case screenApp:
		switch m.tab {
		case tabHistory:
			hints = []string{"enter rollback", "[ ] tabs", "d diff", "w windows", "o browser", "esc back"}
		case tabDetails:
			hints = []string{"enter edit", "[ ] tabs", "s sync", "w windows", "e events", "o browser", "esc back"}
		default:
			hints = []string{"space mark", "[ ] tabs", "enter manifest", "d diff", "l logs", "s sync", "w windows", "esc back"}
		}
	case screenAppSets:
		hints = []string{"enter apps", "y spec", "o browser", "S applications", "/ filter", "r reload", "? help"}
	case screenWindows:
		hints = []string{"j/k move", "o project", "O app", "r reload", "esc back", "? help"}
	case screenHelp:
		hints = []string{"esc back"}
	default:
		hints = []string{"j/k scroll", "/ grep", "esc back", "? help"}
	}

	// Drop hints from the right until the line fits, so a narrow terminal keeps
	// the most important ones rather than wrapping.
	for len(hints) > 1 {
		s := strings.Join(hints, "  ")
		if lipgloss.Width(s) <= m.width {
			break
		}
		hints = hints[:len(hints)-1]
	}
	return m.st.footer.Render(truncate(strings.Join(hints, "  "), m.width))
}

// renderOverlay draws a modal centered over the frame.
func (m *Model) renderOverlay(frame string) string {
	var box string
	switch m.overlay {
	case overlayError:
		w := m.modalContentWidth(76)
		lines := []string{m.st.err.Render("error"), ""}
		lines = append(lines, strings.Split(wrapText(m.errMsg, w), "\n")...)
		lines = append(lines, "", m.st.dim.Render("any key to dismiss"))
		box = m.st.modalErr.Render(strings.Join(
			clampModalBody(lines, w, m.modalContentHeight()), "\n"))

	case overlayConfirm:
		w := m.modalContentWidth(76)
		lines := []string{m.st.warn.Render(m.confirm.title), ""}
		lines = append(lines, m.confirm.body...)
		lines = append(lines, "", m.st.dim.Render("y confirm   ·   n / esc cancel"))
		box = m.st.modal.Render(strings.Join(
			clampModalBody(lines, w, m.modalContentHeight()), "\n"))

	case overlayRevPicker:
		box = m.renderRevPicker()

	case overlaySyncOpts:
		check := func(on bool) string {
			if on {
				return m.st.success.Render("[x]")
			}
			return m.st.dim.Render("[ ]")
		}
		scope := fmt.Sprintf("%d application(s)", len(m.syncOpts.targets))
		if m.screen == screenApp && m.tab == tabResources && m.app != nil {
			scope = fmt.Sprintf("%d resource(s) of %s", len(m.markedNodes()), m.app.Name())
		}
		lines := []string{
			m.st.accent.Render("Sync options"),
			m.st.dim.Render(scope),
			"",
			check(m.syncOpts.prune) + " " + m.st.info.Render("p") + " prune  " +
				m.st.dim.Render("(delete resources not in git)"),
			check(m.syncOpts.dryRun) + " " + m.st.info.Render("d") + " dry-run  " +
				m.st.dim.Render("(compute only, apply nothing)"),
			"",
			m.st.dim.Render("enter continue   ·   esc cancel"),
		}
		box = m.st.modal.Render(strings.Join(
			clampModalBody(lines, m.modalContentWidth(64), m.modalContentHeight()), "\n"))
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// renderRevPicker draws the branch/tag chooser.
func (m *Model) renderRevPicker() string {
	p := &m.revPicker
	w := m.modalContentWidth(64)

	lines := []string{
		m.st.accent.Render("Target revision"),
		m.st.filter.Render("> " + p.filter + "▏"),
		"",
	}

	switch {
	case p.loading:
		lines = append(lines, m.st.dim.Render("loading branches and tags…"))
	case p.err != "":
		lines = append(lines, m.st.err.Render(wrapText(p.err, w)))
	case len(p.rows) == 0:
		lines = append(lines, m.st.dim.Render(fmt.Sprintf("no ref matches %q", p.filter)))
	default:
		h := m.revPickerHeight()
		// The kind column is sized to the widest kind present and reserved
		// before the name gets its share: a branch and a tag can share a name,
		// and truncating the label away would hide which one is being picked.
		kindW := 0
		for _, i := range p.rows {
			if k := lipgloss.Width(p.items[i].kind); k > kindW {
				kindW = k
			}
		}
		nameW := w - kindW - 3
		if nameW < 8 {
			nameW = 8
		}
		for r := p.top; r < len(p.rows) && r < p.top+h; r++ {
			it := p.items[p.rows[r]]
			cursor := " "
			style := lipgloss.NewStyle()
			if r == p.cur {
				cursor = m.st.accent.Render(m.gl.cursor)
				style = m.st.selected
			}
			icon := ""
			switch it.kind {
			case "tag":
				icon = m.gl.prefix(m.gl.tagRef)
			case "branch", "default branch":
				icon = m.gl.prefix(m.gl.branchRef)
			}
			lines = append(lines, cursor+" "+icon+
				style.Render(padRight(truncate(it.name, nameW-lipgloss.Width(icon)), nameW-lipgloss.Width(icon)))+" "+
				m.st.dim.Render(it.kind))
		}
		if n := len(p.rows); n > h {
			lines = append(lines, m.st.dim.Render(fmt.Sprintf("… %d refs", n)))
		}
	}

	lines = append(lines, "",
		m.st.dim.Render("type to filter  ·  ↑↓ move  ·  enter select  ·  esc cancel"))
	return m.st.modal.Render(strings.Join(
		clampModalBody(lines, w, m.modalContentHeight()), "\n"))
}
