package tui

// The RESOURCES tab's left-to-right graph layout — an alternative rendering of
// the same tree the indented view draws, in the style of the Argo CD web UI.
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

// graphNode is one resource positioned in the graph.
type graphNode struct {
	treeIdx  int // index into m.tree
	depth    int
	row      int // display row, shared across the whole graph
	children []*graphNode
}

// graphLayout is the whole tree, reduced to a column (depth) and row for every
// node.
type graphLayout struct {
	roots     []*graphNode
	byTreeIdx map[int]*graphNode
	rows      int // total display rows — one per leaf
	maxDepth  int
	// appRow is where the synthetic application node sits, centered over its
	// roots the same way any parent centers over its children.
	appRow int
}

// buildGraphLayout reconstructs the tree's parent/child edges from the
// flattened, depth-annotated rows and assigns each node a display row.
//
// The edges are rebuilt from Depth alone rather than carried through from
// argocd.Tree.Flatten: the flattened list is already a DFS preorder consistent
// with depth (a child immediately follows its parent, before the parent's next
// sibling), so a stack keyed by depth recovers the same structure without
// threading a second copy of it through the model.
//
// Rows are assigned post-order: a leaf takes the next row in sequence, and a
// parent takes the midpoint of its children's rows. That is the same layout a
// hand-drawn org chart uses, and it is why a single-child chain
// (Deployment → ReplicaSet → Pod) ends up sharing one row while a branching
// node centers on its middle child.
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

	rowCounter := 0
	var place func(n *graphNode) int
	place = func(n *graphNode) int {
		if len(n.children) == 0 {
			n.row = rowCounter
			rowCounter++
			return n.row
		}
		minR, maxR := -1, -1
		for _, c := range n.children {
			r := place(c)
			if minR == -1 || r < minR {
				minR = r
			}
			if r > maxR {
				maxR = r
			}
		}
		n.row = (minR + maxR) / 2
		return n.row
	}
	for _, r := range g.roots {
		place(r)
	}
	g.rows = rowCounter

	if len(g.roots) > 0 {
		minR, maxR := g.roots[0].row, g.roots[0].row
		for _, r := range g.roots {
			if r.row < minR {
				minR = r.row
			}
			if r.row > maxR {
				maxR = r.row
			}
		}
		g.appRow = (minR + maxR) / 2
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

// currentGraphRow is the display row the cursor's node occupies, and the
// graph's total row count — what clampScroll needs to keep the viewport
// following the cursor in graph coordinates rather than list-index ones.
func (m *Model) currentGraphRow() (row, total int, ok bool) {
	if m.treeCur < 0 || m.treeCur >= len(m.treeRows) {
		return 0, 0, false
	}
	idx := m.treeRows[m.treeCur]
	g := buildGraphLayout(m.tree)
	n, found := g.byTreeIdx[idx]
	if !found {
		return 0, 0, false
	}
	return n.row, g.rows, true
}

// boxChar picks the box-drawing character joining a connector cell's four
// sides. up/down say whether the vertical bar continues past this row; left
// says a horizontal line arrives from the parent's column; right says one
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

// connectorColumn draws the vertical span joining a set of parents (all in one
// column) to their children (the next column over), one two-cell string per
// display row: a box character plus a trailing dash that reaches toward a
// child on this row, or a space when none sits here.
//
// Every parent's children occupy a row range disjoint from every other
// parent's at the same depth — proven by construction: buildGraphLayout
// assigns leaf rows in DFS order, so one subtree's whole span of rows comes
// before or after another's, never inside it. That is what makes it safe to
// build one shared array here rather than tracking overlaps.
func (m *Model) connectorColumn(parents []*graphNode, rows int) []string {
	col := make([]string, rows)
	for i := range col {
		col[i] = m.boxChar(false, false, false, false)
	}
	for _, p := range parents {
		if len(p.children) == 0 {
			continue
		}
		minR, maxR := p.children[0].row, p.children[0].row
		childRow := make(map[int]bool, len(p.children))
		for _, c := range p.children {
			childRow[c.row] = true
			if c.row < minR {
				minR = c.row
			}
			if c.row > maxR {
				maxR = c.row
			}
		}
		for r := minR; r <= maxR; r++ {
			left := r == p.row
			right := childRow[r]
			up := r != minR
			down := r != maxR
			dash := " "
			if right {
				dash = "─"
				if m.gl.set == iconsASCII {
					dash = "-"
				}
			}
			col[r] = m.boxChar(up, down, left, right) + dash
		}
	}
	return col
}

// maxGraphColWidth caps a column's content width. A tree deep enough to spend
// its whole terminal width on one long resource name is rare enough that
// truncating it costs less than reserving the width for everyone.
const maxGraphColWidth = 24

// graphCell renders one node's content: health, kind, name — the same facts
// the indented tree shows per row, in the same order, so the two views agree
// on what a resource is called.
func (m *Model) graphCell(n *argocd.Node, cur bool) string {
	hs := n.HealthStatus()
	health := m.st.healthStyle(hs).Render(m.gl.healthGlyph(hs))
	kindCell := m.gl.prefix(m.gl.kindIcon(n.Kind)) + n.Kind
	nameStyle := lipgloss.NewStyle()
	if cur {
		nameStyle = m.st.selected
	}
	return health + " " + m.st.kindStyle(n.Kind).Render(kindCell) + " " + nameStyle.Render(n.Name)
}

// renderTreeGraph draws the resource tree left to right: the application on
// the left, its managed resources fanning out to the right, each column one
// depth deeper than the last.
func (m *Model) renderTreeGraph() string {
	h := m.bodyHeight()
	g := buildGraphLayout(m.tree)

	byDepthRow := make([]map[int]*graphNode, g.maxDepth+1)
	for d := range byDepthRow {
		byDepthRow[d] = map[int]*graphNode{}
	}
	for _, n := range g.byTreeIdx {
		byDepthRow[n.depth][n.row] = n
	}

	cur := -1
	if idx := m.treeRows; m.treeCur >= 0 && m.treeCur < len(idx) {
		cur = idx[m.treeCur]
	}

	// One column of rendered cells per depth, plus the width it settled on.
	cols := make([][]string, g.maxDepth+1)
	colWidth := make([]int, g.maxDepth+1)
	for d := 0; d <= g.maxDepth; d++ {
		cols[d] = make([]string, g.rows)
		for r := range cols[d] {
			cols[d][r] = ""
		}
		for r, n := range byDepthRow[d] {
			row := &m.tree[n.treeIdx]
			cell := m.graphCell(&row.Node, n.treeIdx == cur)
			if w := lipgloss.Width(cell); w > colWidth[d] {
				if w > maxGraphColWidth {
					w = maxGraphColWidth
				}
				colWidth[d] = w
			}
			cols[d][r] = cell
		}
		for r, cell := range cols[d] {
			cols[d][r] = padRight(truncate(cell, colWidth[d]), colWidth[d])
		}
	}

	appW := 0
	appLabel := ""
	if m.app != nil {
		appLabel = m.st.accent.Render(m.app.Name())
		appW = lipgloss.Width(appLabel)
		if appW > maxGraphColWidth {
			appW = maxGraphColWidth
			appLabel = truncate(appLabel, appW)
		}
	}

	// The gap between the application and depth 0 reuses the same connector
	// logic as every later gap: a synthetic parent whose children are the
	// tree's own roots.
	appNode := &graphNode{row: g.appRow, children: g.roots}
	gapApp := m.connectorColumn([]*graphNode{appNode}, g.rows)

	gaps := make([][]string, g.maxDepth)
	for d := 0; d < g.maxDepth; d++ {
		var parents []*graphNode
		for _, n := range byDepthRow[d] {
			parents = append(parents, n)
		}
		gaps[d] = m.connectorColumn(parents, g.rows)
	}

	lines := make([]string, 0, h)
	for r := m.graphTop; r < g.rows && len(lines) < h; r++ {
		var b strings.Builder
		if m.app != nil {
			if r == g.appRow {
				b.WriteString(padRight(appLabel, appW))
			} else {
				b.WriteString(strings.Repeat(" ", appW))
			}
			b.WriteString(" ")
			b.WriteString(gapApp[r])
			b.WriteString(" ")
		}
		for d := 0; d <= g.maxDepth; d++ {
			if cols[d][r] == "" {
				b.WriteString(strings.Repeat(" ", colWidth[d]))
			} else {
				b.WriteString(cols[d][r])
			}
			if d < g.maxDepth {
				b.WriteString(" ")
				b.WriteString(gaps[d][r])
				b.WriteString(" ")
			}
		}
		lines = append(lines, truncate(b.String(), m.width))
	}
	return padBody(lines, h)
}
