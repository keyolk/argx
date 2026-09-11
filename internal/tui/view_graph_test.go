package tui

import (
	"strings"
	"testing"
)

// The graph view must show the application name, every resource name, and the
// boxes joined by connector lines.
func TestGraphRendersAppAndResources(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	fixtureTree(t, m)
	m.treeGraph = true
	m.clampScroll()

	out := m.renderTree()
	for _, want := range []string{
		"web-frontend", // the application, on the left
		"Deployment", "ReplicaSet", "Pod", "Service", "ConfigMap",
		"web-6d9f-abc12", "web-6d9f-def34",
		"╭", "╮", "╰", "╯", // box corners
		"┤", // web-6d9f-abc12 and web-6d9f-def34 fan out from the same replica set
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the graph view is missing %q:\n%s", want, out)
		}
	}
}

// A text filter falls back to the indented list: drawing a graph missing the
// nodes the filter removed would read as a broken graph rather than a
// filtered one.
func TestGraphFallsBackToListWhenFiltered(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	fixtureTree(t, m)
	m.treeGraph = true
	m.treeFilt = parseResourceFilter("kind:pod")
	m.applyTreeFilter()

	out := m.renderTree()
	if strings.Contains(out, "╭") {
		t.Errorf("a filtered graph view drew boxes instead of falling back to the list:\n%s", out)
	}
	if !strings.Contains(out, "web-6d9f-abc12") {
		t.Errorf("the filtered list is missing the resource it matched:\n%s", out)
	}
}

// The marked-only view narrows the tree the same way a filter does, so the
// graph steps aside for it too.
func TestGraphFallsBackToListWhenMarkedOnly(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	fixtureTree(t, m)
	m.treeGraph = true
	m.treeMarks["p1"] = true
	m.markedOnly = true
	m.applyTreeFilter()

	if m.treeGraphActive() {
		t.Fatal("graph view should stand down while marked-only is on")
	}
}

// Toggling the graph on and off must never crash and must always produce a
// line count the frame expects, regardless of terminal size.
func TestGraphTogglePreservesLineBudget(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {100, 24}, {80, 24}, {60, 14}} {
		w, h := size[0], size[1]
		m := fixtureModel(t, w, h)
		fixtureTree(t, m)
		m.treeGraph = true
		m.clampScroll()

		for i, line := range strings.Split(m.renderTree(), "\n") {
			if got := lipglossWidth(line); got > w {
				t.Errorf("%dx%d graph line %d is %d cells, want at most %d:\n%q",
					w, h, i, got, w, line)
			}
		}
	}
}

// Moving the cursor down must not desync the viewport: the graph's own line
// count (boxes are several lines tall) has to stay in lock step with
// clampScroll, or the cursor can scroll off screen while treeCur still
// reports a valid row.
func TestGraphCursorStaysInViewport(t *testing.T) {
	m := fixtureModel(t, 120, 6) // a short body so scrolling actually engages
	fixtureTree(t, m)
	m.treeGraph = true
	m.clampScroll()

	for m.treeCur < len(m.treeRows)-1 {
		m.moveTree(1)
		line, total, ok := m.currentGraphLine()
		if !ok {
			t.Fatalf("currentGraphLine() not ok at treeCur=%d", m.treeCur)
		}
		if line < m.graphTop || line >= m.graphTop+m.bodyHeight() {
			t.Fatalf("cursor line %d out of viewport [%d, %d) at treeCur=%d (total=%d)",
				line, m.graphTop, m.graphTop+m.bodyHeight(), m.treeCur, total)
		}
	}
}

// h and ← must step the cursor to the current node's parent while the graph
// is active — the one direction plain DFS order (what j/k walk) cannot reach
// directly, since a node's parent precedes it in that order but is not
// necessarily adjacent to it.
func TestGraphLeftMovesToParent(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	fixtureTree(t, m)
	m.screen = screenApp
	m.tab = tabResources
	m.treeGraph = true
	m.clampScroll()

	// Move onto the second pod (web-6d9f-def34), a grandchild of the
	// deployment through the replica set.
	for m.currentNode() == nil || m.currentNode().Name != "web-6d9f-def34" {
		prev := m.treeCur
		press(t, m, "j")
		if m.treeCur == prev {
			t.Fatal("never reached web-6d9f-def34 walking down the tree")
		}
	}

	press(t, m, "h")
	if got := m.currentNode(); got == nil || got.Name != "web-6d9f" {
		t.Fatalf("h from a pod should land on its replica set, got %v", got)
	}

	press(t, m, "left")
	if got := m.currentNode(); got == nil || got.Name != "web" {
		t.Fatalf("left from a replica set should land on its deployment, got %v", got)
	}

	// At a root, h/← has nowhere to go inside the graph, and it must not fall
	// through to leaving the application view — only Esc does that.
	m.prev = []screen{screenApps}
	press(t, m, "h")
	if m.screen != screenApp {
		t.Fatalf("h at a root node should do nothing, not leave the application view, screen = %v", m.screen)
	}
	if got := m.currentNode(); got == nil || got.Name != "web" {
		t.Fatalf("h at a root node should leave the cursor in place, got %v", got)
	}
}
