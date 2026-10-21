package tui

// The RESOURCES tab's tree, the pager views (diff, manifest, logs, events),
// and the help screen.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/render"
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

	if m.treeGraphActive() {
		return m.renderTreeGraph()
	}

	kindW := m.treeKindWidth()

	lines := make([]string, 0, h)
	lines = append(lines, m.st.header.Render(truncate(
		"  H  "+render.PadRight("KIND", kindW)+" NAME", m.width)))

	// Indentation is suppressed while a filter is active: the connectors would
	// draw branches to parents the filter removed, which reads as a broken tree.
	indented := m.treeFilt.empty()

	for r := m.treeTop; r < len(m.treeRows) && len(lines) < h; r++ {
		row := m.tree[m.treeRows[r]]
		n := row.Node
		cur := r == m.treeCur

		mark := " "
		switch {
		case m.treeMarks[n.UID]:
			mark = m.st.mark.Render(m.gl.marked)
		case inVisualRange(m, r):
			mark = m.st.dim.Render(m.gl.marked)
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

		// The kind is a column of its own, so every name starts at the same
		// place and the kinds line up to be scanned down. The icon rides in
		// front of the word rather than replacing it: the shape is quick to
		// recognise once you know it, and the word is what tells you which
		// shape you are looking at.
		nameStyle := lipgloss.NewStyle()
		if cur {
			nameStyle = m.st.selected
		}
		kindCell := m.gl.prefix(m.gl.kindIcon(n.Kind)) + n.Kind
		label := m.st.kindStyle(n.Kind).Render(
			render.PadRight(truncate(kindCell, kindW), kindW)) +
			" " + prefix + nameStyle.Render(n.Name)

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

		line := cursor + mark + " " + health + " " + label + detail
		lines = append(lines, truncate(line, m.width))
	}
	return padBody(lines, h)
}

// treeKindWidth sizes the kind column from the kinds actually in the tree.
//
// Computed rather than fixed: a tree of Pods and ReplicaSets should not reserve
// the width of CustomResourceDefinition, and one that does contain a long kind
// should not truncate it into ambiguity.
func (m *Model) treeKindWidth() int {
	if len(m.tree) == 0 {
		return 0
	}

	widths := make([]int, 0, len(m.tree))
	for _, row := range m.tree {
		cell := m.gl.prefix(m.gl.kindIcon(row.Node.Kind)) + row.Node.Kind
		widths = append(widths, lipgloss.Width(cell))
	}
	sort.Ints(widths)

	// The 90th percentile rather than the longest: one
	// ValidatingWebhookConfiguration in a tree of Pods would otherwise pad
	// every row out by seventeen columns for a kind that appears once. The
	// outliers truncate, which is the trade a fixed column is for.
	w := widths[len(widths)*9/10]
	if w > maxTreeKindCol {
		w = maxTreeKindCol
	}
	// On a narrow terminal the name matters more than the kind, so the column
	// gives way rather than squeezing the name out.
	if max := m.width / 4; w > max {
		w = max
	}
	return w
}

// maxTreeKindCol caps the kind column. Long enough for StatefulSet and its
// icon; past that the kinds are rare enough that truncation costs less than the
// width would. There is no floor: a tree of Pods and ReplicaSets should render
// a column exactly wide enough for "ReplicaSet", not one padded to a size
// nothing in it needs.
const maxTreeKindCol = 22

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

	// Two columns, when asked for and when there is room. A narrow terminal
	// falls back rather than refusing: a cramped unified diff is still a diff,
	// and a message telling the reader to widen their window is not.
	if m.sxsActive() {
		return padBody(m.renderSideBySide(sideBySide(all), m.pagerTop, h), h)
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
// block so the help scrolls (and searches) with the same pager keys as every
// other long view; a help screen taller than the terminal that silently cuts
// off is worse than no help.
//
// The key sections come from the binding table — the screen help was opened
// from first, then every other stack — so help cannot describe a key the
// dispatcher does not route. What follows them is reference that is not about
// keys: the filter syntax and the status letters.
func (m *Model) helpLines() []string {
	from := m.screen
	if from == screenHelp && len(m.prev) > 0 {
		from = m.prev[len(m.prev)-1]
	}
	stacks := append([][]keys.Ctx{m.stackFor(from)}, allStacks()...)
	secs := m.keymap().Help(m.keyOpts(), stacks...)
	// Every line, unmarked: the pager does the scrolling.
	lines := keys.HelpLines(secs, m.width, m.helpStyle())
	lines = append(lines, "")

	type row struct{ k, d string }
	notes := []struct {
		title string
		rows  []row
	}{
		{"application filter", []row{
			{"web", "name, context, project, destination, status,"},
			{"", "revision, repo, path — everything the row shows"},
			{"label:env=prod", "a label key and value; l: is the short form"},
			{"!l:env", "applications *without* the label"},
			{"ctx: proj: ns:", "server, project, destination namespace"},
			{"cluster: sync:", "destination cluster, sync status"},
			{"health:degraded", "health status"},
			{"!ctx:prod, !web", "a `!` prefix negates any field, name included"},
		}},
		{"resource filter", []row{
			{"web", "name contains web"},
			{"kind:pod, k:pod", "kind starts with pod"},
			{"status:degraded", "health (status:none = unchecked kinds)"},
			{"ns:prod", "namespace"},
			{"label:app=web", "a label — only kinds Argo CD reports networking for"},
			{"", "terms are ANDed: kind:pod status:degraded"},
			{"!kind:pod, !web", "a `!` prefix negates any field, name included"},
		}},
		{"application set filter", []row{
			{"gen:git", "generator; also status:error, ctx:, proj:, ns:, label:"},
			{"", "a broken generator shows up here and nowhere else"},
		}},
		{"multi-select", []row{
			{"", "everything is scoped to the filtered rows — marking what"},
			{"", "you cannot see is how a sync hits the wrong thing"},
			{"", "marks the filter hides are counted in the status line"},
			{"", "actions apply to marks, or to the cursor row when none"},
		}},
		{"scheduled syncs", []row{
			{"", "s then w waits for the sync window instead of syncing now:"},
			{"", "Argo CD records a sync into a closed window as a failed"},
			{"", "operation. They run only while argx runs, are kept across"},
			{"", "restarts, and each re-checks the target revision, the sync"},
			{"", "status, auto-sync and the window before firing"},
		}},
		{"status letters", []row{
			{"S / !", "Synced / OutOfSync"},
			{"H P D M Z", "Healthy Progressing Degraded Missing Suspended"},
		}},
		{"keys.yaml", []row{
			{"", keys.ConfigPath("argx")},
			{"", "rebinds by action name: `quit: [x]`, or per context:"},
			{"", "`tree.tree.logs: [L]` — the palette (:) shows the names"},
		}},
	}
	for _, s := range notes {
		lines = append(lines, m.st.accent.Render(s.title))
		for _, r := range s.rows {
			lines = append(lines, truncate("  "+m.st.info.Render(render.PadRight(r.k, 18))+m.st.dim.Render(r.d), m.width))
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
