package tui

// The RESOURCES tab's tree, the pager views (diff, manifest, logs, events),
// and the help screen.

import (
	"fmt"
	"strings"

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
			{"←/→ in a filter", "move the text cursor; ↑/↓ move the list"},
			{"ctrl+a/e ctrl+w", "start / end of line, delete a word"},
			{"?", "this help"},
		}},
		{"multi-select", []row{
			{"space", "toggle mark and advance"},
			{"a", "mark / unmark every filtered row"},
			{"", "actions apply to marks, or to the cursor row when none"},
		}},
		{"applications", []row{
			{"S", "the application sets that generate them"},
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
			{"w", "sync windows"},
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
		{"application filter", []row{
			{"web", "name, context, project, destination, status,"},
			{"", "revision, repo, path — everything the row shows"},
			{"label:env=prod", "a label key and value; l: is the short form"},
			{"-l:env", "applications *without* the label"},
			{"ctx: proj: ns:", "server, project, destination namespace"},
			{"cluster: sync:", "destination cluster, sync status"},
			{"health:degraded", "health status"},
			{"tab", "complete the word under the cursor"},
		}},
		{"resource filter", []row{
			{"web", "name contains web"},
			{"kind:pod, k:pod", "kind starts with pod"},
			{"status:degraded", "health (status:none = unchecked kinds)"},
			{"ns:prod", "namespace"},
			{"label:app=web", "a label — only kinds Argo CD reports networking for"},
			{"", "terms are ANDed: kind:pod status:degraded"},
		}},
		{"HISTORY tab", []row{
			{"enter, b", "roll back to this deployment"},
			{"d", "diff against live"},
		}},
		{"DETAILS tab", []row{
			{"enter", "change the marked row under the cursor"},
			{"", "revision, auto-sync, prune, self-heal, terminate"},
			{"s", "sync"},
			{"e", "events"},
		}},
		{"application sets", []row{
			{"S", "switch between applications and application sets"},
			{"enter", "the applications this set generated"},
			{"y", "its full spec"},
			{"o", "open in browser"},
			{"/", "filter — gen:git, status:error, ctx:, proj:, ns:, label:"},
			{"", "a broken generator shows up here and nowhere else:"},
			{"", "the applications it would have made do not exist"},
		}},
		{"sync windows", []row{
			{"w", "the schedules that allow or block syncing"},
			{"j/k, g/G", "move through the list"},
			{"o", "open the project in browser — where windows are edited"},
			{"O", "open the application in browser"},
			{"r", "reload"},
			{"", "defined per AppProject; argx shows them, does not edit them"},
			{"", "a blocked sync is flagged in the status line on every tab"},
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
