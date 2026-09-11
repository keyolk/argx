package tui

// The RESOURCES tab's left-to-right graph layout — an alternative rendering of
// the same tree the indented view draws, boxes connected by lines, in the
// style of the Argo CD web UI (and argo9s's graph view).
//
// It is a rendering choice only: the cursor still walks treeRows in the same
// DFS order it always has, so the filter, marks, diff and sync all keep
// working exactly as they do in the indented view. Only what a row looks like
// on screen differs.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
)

// A node's box is three content lines — kind, name, health — plus a border
// top and bottom. boxGap is the blank line left between two sibling boxes so
// the connector routed between them has somewhere to run. boxPitch is the
// vertical space one leaf reserves for itself: its own box plus that gap.
const (
	boxContentLines = 3
	boxHeight       = boxContentLines + 2
	boxGap          = 1
	boxPitch        = boxHeight + boxGap
)

// graphNode is one resource positioned in the graph.
type graphNode struct {
	treeIdx int // index into m.tree
	depth   int
	// center is the display line its box is vertically centered on — the
	// line its "name" row falls on, which is also where a connector attaches.
	center   int
	children []*graphNode
}

// graphLayout is the whole tree, reduced to a column (depth) and a center
// line for every node.
type graphLayout struct {
	roots     []*graphNode
	byTreeIdx map[int]*graphNode
	// totalLines is how tall the rendered graph is, in terminal lines.
	totalLines int
	maxDepth   int
	// appCenter is where the synthetic application box sits, centered over
	// its roots the same way any parent centers over its children.
	appCenter int
}

// buildGraphLayout reconstructs the tree's parent/child edges from the
// flattened, depth-annotated rows and assigns each node a center line.
//
// The edges are rebuilt from Depth alone rather than carried through from
// argocd.Tree.Flatten: the flattened list is already a DFS preorder consistent
// with depth (a child immediately follows its parent, before the parent's next
// sibling), so a stack keyed by depth recovers the same structure without
// threading a second copy of it through the model.
//
// Lines are assigned post-order: a leaf takes the next box-pitch slot in
// sequence, and a parent centers on the midpoint of its children's centers.
// That is the same layout a hand-drawn org chart uses, and it is why a
// single-child chain (Deployment → ReplicaSet → Pod) ends up with every box
// on the same center line while a branching node centers on its middle child.
func buildGraphLayout(tree []argocd.TreeRow) *graphLayout {
	g := &graphLayout{byTreeIdx: map[int]*graphNode{}}
	var stack []*graphNode
	for i, row := range tree {
		d := row.Depth
		if d > len(stack) {
			d = len(stack) // defensive: Flatten never produces this
		}
		stack = stack[:d]
		n := &graphNode{treeIdx: i, depth: d}
		if d == 0 {
			g.roots = append(g.roots, n)
		} else {
			parent := stack[d-1]
			parent.children = append(parent.children, n)
		}
		stack = append(stack, n)
		g.byTreeIdx[i] = n
		if d > g.maxDepth {
			g.maxDepth = d
		}
	}

	leaves := 0
	var place func(n *graphNode) int
	place = func(n *graphNode) int {
		if len(n.children) == 0 {
			n.center = leaves*boxPitch + boxHeight/2
			leaves++
			return n.center
		}
		minC, maxC := -1, -1
		for _, c := range n.children {
			cc := place(c)
			if minC == -1 || cc < minC {
				minC = cc
			}
			if cc > maxC {
				maxC = cc
			}
		}
		n.center = (minC + maxC) / 2
		return n.center
	}
	for _, r := range g.roots {
		place(r)
	}
	g.totalLines = leaves*boxPitch - boxGap
	if g.totalLines < boxHeight {
		g.totalLines = boxHeight
	}

	if len(g.roots) > 0 {
		minC, maxC := g.roots[0].center, g.roots[0].center
		for _, r := range g.roots {
			if r.center < minC {
				minC = r.center
			}
			if r.center > maxC {
				maxC = r.center
			}
		}
		g.appCenter = (minC + maxC) / 2
	}
	return g
}

// treeGraphActive reports whether the graph layout should be drawn instead of
// the indented tree.
//
// It stands down for a text filter or the marked-only view for the same
// reason the indented tree suppresses its connectors under a filter: both
// narrow the tree to a subset of rows, and drawing edges to a parent the
// narrowing removed reads as a broken graph rather than a filtered one. The
// graph is for browsing the whole tree; narrowing it falls back to the list
// that already handles a partial view correctly.
func (m *Model) treeGraphActive() bool {
	return m.treeGraph && m.treeFilt.empty() && !m.markedOnly && len(m.tree) > 0
}

// currentGraphLine is the display line the cursor's node is centered on, and
// the graph's total line count — what clampScroll needs to keep the viewport
// following the cursor in graph coordinates rather than list-index ones.
func (m *Model) currentGraphLine() (line, total int, ok bool) {
	if m.treeCur < 0 || m.treeCur >= len(m.treeRows) {
		return 0, 0, false
	}
	idx := m.treeRows[m.treeCur]
	g := buildGraphLayout(m.tree)
	n, found := g.byTreeIdx[idx]
	if !found {
		return 0, 0, false
	}
	return n.center, g.totalLines, true
}

// graphParentRow finds the row (an index into treeRows) of the current node's
// parent, so h/← can move the cursor toward the root the graph draws it
// toward — the one direction the DFS order behind j/k cannot reach directly.
//
// It walks the flattened tree backwards to the nearest preceding row one
// depth shallower, the same technique buildGraphLayout uses to recover edges
// from Flatten's DFS-preorder-with-depth encoding.
func (m *Model) graphParentRow() (int, bool) {
	if m.treeCur < 0 || m.treeCur >= len(m.treeRows) {
		return 0, false
	}
	idx := m.treeRows[m.treeCur]
	depth := m.tree[idx].Depth
	if depth == 0 {
		return 0, false
	}
	for i := idx - 1; i >= 0; i-- {
		if m.tree[i].Depth == depth-1 {
			for r, ti := range m.treeRows {
				if ti == i {
					return r, true
				}
			}
			return 0, false
		}
	}
	return 0, false
}

// boxChar picks the box-drawing character joining a connector cell's four
// sides. up/down say whether the vertical bar continues past this line;
// left says a horizontal line arrives from the parent's side; right says one
// leaves toward a child's.
func (m *Model) boxChar(up, down, left, right bool) string {
	if m.gl.set == iconsASCII {
		switch {
		case (left || right) && (up || down):
			return "+"
		case left, right:
			return "-"
		case up, down:
			return "|"
		default:
			return " "
		}
	}
	switch {
	case up && down && left && right:
		return "┼"
	case up && down && left:
		return "┤"
	case up && down && right:
		return "├"
	case up && down:
		return "│"
	case down && left && right:
		return "┬"
	case down && left:
		return "┐"
	case down && right:
		return "┌"
	case up && left && right:
		return "┴"
	case up && left:
		return "┘"
	case up && right:
		return "└"
	case left, right:
		return "─"
	default:
		return " "
	}
}

// connectorColumn draws the span joining a set of parents (all in one column)
// to their children (the next column over), one three-cell string per display
// line: a dash reaching back toward the parent, the trunk character, and a
// dash reaching forward toward a child — or three blanks where neither
// applies.
//
// Every parent's children occupy a line range disjoint from every other
// parent's at the same depth — proven by construction: buildGraphLayout
// assigns leaf centers in DFS order, so one subtree's whole span of lines
// comes before or after another's, never inside it. That is what makes it
// safe to build one shared array here rather than tracking overlaps.
func (m *Model) connectorColumn(parents []*graphNode, totalLines int) []string {
	col := make([]string, totalLines)
	for i := range col {
		col[i] = "   "
	}
	dash := "─"
	if m.gl.set == iconsASCII {
		dash = "-"
	}
	for _, p := range parents {
		if len(p.children) == 0 {
			continue
		}
		minC, maxC := p.children[0].center, p.children[0].center
		childAt := make(map[int]bool, len(p.children))
		for _, c := range p.children {
			childAt[c.center] = true
			if c.center < minC {
				minC = c.center
			}
			if c.center > maxC {
				maxC = c.center
			}
		}
		for r := minC; r <= maxC; r++ {
			left := r == p.center
			right := childAt[r]
			up := r != minC
			down := r != maxC
			ld, rd := " ", " "
			if left {
				ld = dash
			}
			if right {
				rd = dash
			}
			col[r] = ld + m.boxChar(up, down, left, right) + rd
		}
	}
	return col
}

// maxGraphColWidth caps a box's content width. A tree deep enough to spend
// its whole terminal width on one long resource name is rare enough that
// truncating it costs less than reserving the width for everyone.
const maxGraphColWidth = 22

// boxGlyphs is the border character set for a resource box: rounded corners
// in Unicode and Nerd Font, plain ASCII where the terminal cannot be trusted
// with anything else.
func (m *Model) boxGlyphs() (tl, tr, bl, br, h, v string) {
	if m.gl.set == iconsASCII {
		return "+", "+", "+", "+", "-", "|"
	}
	return "╭", "╮", "╰", "╯", "─", "│"
}

// boxLines draws one resource's box: a top border, its three content lines
// padded to w, and a bottom border. The border is accented when this is the
// node under the cursor — the same "which row is current" question the
// indented tree answers with a highlighted name, answered here by
// highlighting the whole box.
func (m *Model) boxLines(w int, cur bool, content [boxContentLines]string) []string {
	tl, tr, bl, br, h, v := m.boxGlyphs()
	border := m.st.dim
	if cur {
		border = m.st.accent
	}
	top := border.Render(tl + strings.Repeat(h, w+2) + tr)
	bot := border.Render(bl + strings.Repeat(h, w+2) + br)
	vv := border.Render(v)
	lines := make([]string, 0, boxHeight)
	lines = append(lines, top)
	for _, s := range content {
		lines = append(lines, vv+" "+padRight(truncate(s, w), w)+" "+vv)
	}
	lines = append(lines, bot)
	return lines
}

// nodeContent renders a resource's three box lines — kind, name, health —
// the same facts the indented tree shows per row, in the same order, so the
// two views agree on what a resource is called.
func (m *Model) nodeContent(n *argocd.Node, cur bool) (content [boxContentLines]string, width int) {
	kind := m.st.kindStyle(n.Kind).Render(m.gl.prefix(m.gl.kindIcon(n.Kind)) + n.Kind)
	nameStyle := lipgloss.NewStyle().Bold(true)
	if cur {
		nameStyle = m.st.selected
	}
	name := nameStyle.Render(n.Name)
	hs := n.HealthStatus()
	label := hs
	if label == "" {
		// Argo CD has no health check for this kind — a ConfigMap, most CRDs
		// — and saying so beats leaving the line blank, which reads as data
		// that failed to load rather than a kind that has none to report.
		label = "no health check"
	}
	health := m.st.healthStyle(hs).Render(m.gl.healthGlyph(hs) + " " + label)

	content = [boxContentLines]string{kind, name, health}
	for _, s := range content {
		if w := lipgloss.Width(s); w > width {
			width = w
		}
	}
	if width > maxGraphColWidth {
		width = maxGraphColWidth
	}
	return content, width
}

// renderTreeGraph draws the resource tree left to right as boxes joined by
// lines: the application on the left, its managed resources fanning out to
// the right, each column one depth deeper than the last.
func (m *Model) renderTreeGraph() string {
	h := m.bodyHeight()
	g := buildGraphLayout(m.tree)

	byDepth := make([][]*graphNode, g.maxDepth+1)
	for _, n := range g.byTreeIdx {
		byDepth[n.depth] = append(byDepth[n.depth], n)
	}

	cur := -1
	if m.treeCur >= 0 && m.treeCur < len(m.treeRows) {
		cur = m.treeRows[m.treeCur]
	}

	// One column of rendered lines per depth, each box written into its
	// [center-2, center+2] span; everywhere else in the column starts blank.
	cols := make([][]string, g.maxDepth+1)
	for d := 0; d <= g.maxDepth; d++ {
		w := 0
		type placed struct {
			n       *graphNode
			content [boxContentLines]string
		}
		items := make([]placed, 0, len(byDepth[d]))
		for _, n := range byDepth[d] {
			content, cw := m.nodeContent(&m.tree[n.treeIdx].Node, n.treeIdx == cur)
			if cw > w {
				w = cw
			}
			items = append(items, placed{n, content})
		}
		boxW := w + 4 // two border columns, two padding columns
		col := make([]string, g.totalLines)
		blank := strings.Repeat(" ", boxW)
		for i := range col {
			col[i] = blank
		}
		for _, it := range items {
			lines := m.boxLines(w, it.n.treeIdx == cur, it.content)
			top := it.n.center - boxHeight/2
			for i, l := range lines {
				if r := top + i; r >= 0 && r < len(col) {
					col[r] = l
				}
			}
		}
		cols[d] = col
	}

	// The application box, drawn the same way as any resource's — the
	// synthetic parent every root fans out from.
	appCol := make([]string, g.totalLines)
	appW := 0
	if m.app != nil {
		kind := m.st.dim.Render("Application")
		name := m.st.accent.Render(m.app.Name())
		status := m.st.syncStyle(m.app.Status.Sync.Status).Render(m.app.Status.Sync.Status) +
			" " + m.st.healthStyle(m.app.Status.Health.Status).Render(m.app.Status.Health.Status)
		w := 0
		for _, s := range []string{kind, name, status} {
			if lw := lipgloss.Width(s); lw > w {
				w = lw
			}
		}
		if w > maxGraphColWidth {
			w = maxGraphColWidth
		}
		appW = w + 4
		lines := m.boxLines(w, false, [boxContentLines]string{kind, name, status})
		top := g.appCenter - boxHeight/2
		for i := range appCol {
			appCol[i] = strings.Repeat(" ", appW)
		}
		for i, l := range lines {
			if r := top + i; r >= 0 && r < len(appCol) {
				appCol[r] = l
			}
		}
	}

	appNode := &graphNode{center: g.appCenter, children: g.roots}
	gapApp := m.connectorColumn([]*graphNode{appNode}, g.totalLines)
	gaps := make([][]string, g.maxDepth)
	for d := 0; d < g.maxDepth; d++ {
		gaps[d] = m.connectorColumn(byDepth[d], g.totalLines)
	}

	lines := make([]string, 0, h)
	for r := m.graphTop; r < g.totalLines && len(lines) < h; r++ {
		var b strings.Builder
		if m.app != nil {
			b.WriteString(appCol[r])
			b.WriteString(gapApp[r])
		}
		for d := 0; d <= g.maxDepth; d++ {
			b.WriteString(cols[d][r])
			if d < g.maxDepth {
				b.WriteString(gaps[d][r])
			}
		}
		lines = append(lines, truncate(b.String(), m.width))
	}
	return padBody(lines, h)
}
