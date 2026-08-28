package tui

// The RESOURCES tab's tree, the pager views (diff, manifest, logs, events),
// and the help screen.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderTree is the resource tree of the focused application.
func (m *Model) renderTree() string {
	h := m.bodyHeight()
	if len(m.treeRows) == 0 {
		txt := "loading resource tree…"
		if !m.loading {
			txt = "no resources"
			if !m.treeFilt.empty() {
				txt = fmt.Sprintf("no resources match %q", m.treeFilt.String())
			}
		}
		return m.emptyBody(h, txt)
	}

	lines := make([]string, 0, h)
	lines = append(lines, m.st.header.Render("  H  KIND/NAME"))

	// Indentation is suppressed while a filter is active: the connectors would
	// draw branches to parents the filter removed, which reads as a broken tree.
	indented := m.treeFilt.empty()

	for r := m.treeTop; r < len(m.treeRows) && len(lines) < h; r++ {
		row := m.tree[m.treeRows[r]]
		n := row.Node
		cur := r == m.treeCur

		mark := " "
		if m.treeMarks[n.UID] {
			mark = m.st.mark.Render(m.gl.marked)
		}
		cursor := " "
		if cur {
			cursor = m.st.accent.Render(m.gl.cursor)
		}

		hs := n.HealthStatus()
		health := m.st.healthStyle(hs).Render(m.gl.healthGlyph(hs))

		prefix := ""
		if indented && row.Depth > 0 {
			prefix = strings.Repeat(m.gl.blank, row.Depth-1)
			if row.Last {
				prefix += m.gl.corner
			} else {
				prefix += m.gl.branch
			}
		}

		// With icons on, the kind is shown as a symbol rather than spelled out:
		// the shape is what a reader scans for, and the word costs the width
		// that the name needs. Without icons the word is the only signal, so it
		// stays.
		var label string
		nameStyle := lipgloss.NewStyle()
		if cur {
			nameStyle = m.st.selected
		}
		if icon := m.gl.kindIcon(n.Kind); icon != "" {
			label = m.st.kindStyle(n.Kind).Render(icon) + " " + nameStyle.Render(n.Name)
		} else {
			label = m.st.dim.Render(n.Kind+" ") + nameStyle.Render(n.Name)
		}

		// Trailing detail: the one fact that matters per kind, not every info
		// entry Argo CD attaches.
		var detail string
		switch {
		case n.IsPod():
			detail = n.InfoValue("Status Reason")
			if r := n.InfoValue("Restart Count"); r != "" && r != "0" {
				detail += " restarts=" + r
			}
		case n.Kind == "Deployment" || n.Kind == "StatefulSet" || n.Kind == "ReplicaSet":
			detail = n.InfoValue("Revision")
		case n.Kind == "Service":
			detail = n.InfoValue("Type")
		}
		if detail != "" {
			detail = " " + m.st.dim.Render(detail)
		}

		line := cursor + mark + " " + health + " " + prefix + label + detail
		lines = append(lines, truncate(line, m.width))
	}
	return padBody(lines, h)
}

// renderPager renders diff / manifest / logs / events with diff-aware coloring.
func (m *Model) renderPager() string {
	h := m.bodyHeight()
	all := m.pagerLines()
	if len(all) == 0 {
		txt := "loading…"
		if !m.loading {
			txt = "(empty)"
			if m.pagerFilt != "" {
				txt = fmt.Sprintf("no lines match %q", m.pagerFilt)
			}
		}
		return m.emptyBody(h, txt)
	}

	colorize := m.screen == screenDiff
	lines := make([]string, 0, h)
	for i := m.pagerTop; i < len(all) && len(lines) < h; i++ {
		l := truncate(all[i], m.width)
		if colorize {
			switch {
			case strings.HasPrefix(l, "+"):
				l = m.st.diffAdd.Render(l)
			case strings.HasPrefix(l, "-"):
				l = m.st.diffDel.Render(l)
			case strings.HasPrefix(l, "==="), strings.HasPrefix(l, "@@"):
				l = m.st.diffHunk.Render(l)
			}
		}
		lines = append(lines, l)
	}
	return padBody(lines, h)
}

// helpLines builds the help content. It is a []string rather than a rendered
// block so the help scrolls with the same pager keys as every other long view;
// a help screen taller than the terminal that silently cuts off is worse than
// no help.
func (m *Model) helpLines() []string {
	type row struct{ k, d string }
	sections := []struct {
		title string
		rows  []row
	}{
		{"navigation", []row{
			{"j/k, ↑/↓", "move"},
			{"ctrl+d / ctrl+u", "half page"},
			{"g / G", "top / bottom"},
			{"enter, l", "drill in"},
			{"esc, h, q", "back (q quits at the app list)"},
			{"/", "filter (esc clears, enter keeps)"},
			{"?", "this help"},
		}},
		{"multi-select", []row{
			{"space", "toggle mark and advance"},
			{"a", "mark / unmark every filtered row"},
			{"", "actions apply to marks, or to the cursor row when none"},
		}},
		{"applications", []row{
			{"o", "open in browser"},
			{"d", "diff (desired vs live)"},
			{"e", "events"},
			{"r / R", "refresh / hard refresh"},
			{"s", "sync (options, then confirm)"},
			{"ctrl+r", "reload the list"},
			{"A", "toggle 15s auto-refresh"},
		}},
		{"application view", []row{
			{"[ / ]  tab", "previous / next tab"},
			{"1 / 2 / 3", "RESOURCES / HISTORY / DETAILS"},
			{"o", "open the application in browser"},
			{"r", "reload"},
			{"esc, h, q", "back to the list"},
		}},
		{"RESOURCES tab", []row{
			{"enter", "live manifest"},
			{"d", "diff of the marked resources"},
			{"l / L", "pod logs"},
			{"s", "sync the marked resources"},
			{"/", "filter — see below"},
		}},
		{"resource filter", []row{
			{"web", "name contains web"},
			{"kind:pod, k:pod", "kind starts with pod"},
			{"status:degraded", "health (status:none = unchecked kinds)"},
			{"ns:prod", "namespace"},
			{"", "terms are ANDed: kind:pod status:degraded"},
		}},
		{"HISTORY tab", []row{
			{"enter, b", "roll back to this deployment"},
			{"d", "diff against live"},
		}},
		{"DETAILS tab", []row{
			{"enter", "change the * row under the cursor"},
			{"", "revision, auto-sync, prune, self-heal, terminate"},
			{"s", "sync"},
			{"e", "events"},
		}},
		{"status letters", []row{
			{"S / !", "Synced / OutOfSync"},
			{"H P D M Z", "Healthy Progressing Degraded Missing Suspended"},
		}},
	}

	var lines []string
	for _, s := range sections {
		lines = append(lines, m.st.accent.Render(s.title))
		for _, r := range s.rows {
			lines = append(lines, "  "+m.st.info.Render(padRight(r.k, 18))+m.st.dim.Render(r.d))
		}
		lines = append(lines, "")
	}
	return lines
}

// renderHelp shows the help content through the pager viewport.
func (m *Model) renderHelp() string {
	h := m.bodyHeight()
	all := m.helpLines()
	out := make([]string, 0, h)
	for i := m.pagerTop; i < len(all) && len(out) < h; i++ {
		out = append(out, truncate(all[i], m.width))
	}
	return padBody(out, h)
}

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
		if m.appFilter != "" || m.filtering {
			parts = append(parts, m.st.filter.Render("/"+m.appFilter+m.caret()))
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
			parts = append(parts, m.st.filter.Render("/"+m.treeFilt.String()+m.caret()))
			if m.filtering {
				parts = append(parts, m.st.dim.Render(resourceFilterHint))
			}
		}
	default:
		total := len(m.pagerLines())
		parts = append(parts, m.st.dim.Render(fmt.Sprintf("line %d/%d", min(m.pagerTop+1, total), total)))
		if m.pagerFilt != "" || m.filtering {
			parts = append(parts, m.st.filter.Render("/"+m.pagerFilt+m.caret()))
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

func (m *Model) caret() string {
	if m.filtering {
		return "▏"
	}
	return ""
}

// renderFooter is the always-visible hint line, per screen.
func (m *Model) renderFooter() string {
	var hints []string
	switch m.screen {
	case screenApps:
		hints = []string{"space mark", "o browser", "d diff", "s sync", "r refresh", "e events", "/ filter", "? help", "q quit"}
	case screenApp:
		switch m.tab {
		case tabHistory:
			hints = []string{"enter rollback", "[ ] tabs", "d diff", "o browser", "esc back", "? help"}
		case tabDetails:
			hints = []string{"enter edit", "[ ] tabs", "s sync", "e events", "o browser", "esc back", "? help"}
		default:
			hints = []string{"space mark", "[ ] tabs", "enter manifest", "d diff", "l logs", "s sync", "o browser", "esc back"}
		}
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
