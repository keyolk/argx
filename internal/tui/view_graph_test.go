package tui

import (
	"strings"
	"testing"
)

// The graph view must show the application name, every resource name, and the
// left-to-right connectors joining them.
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
		"┌─", "┬─", "└─", // fan-out and fan-in connectors
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
	if strings.Contains(out, "┌─") || strings.Contains(out, "┬─") {
		t.Errorf("a filtered graph view drew connectors instead of falling back to the list:\n%s", out)
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

// Moving the cursor onto a resource with siblings must not desync the
// viewport: the graph's own row (a leaf-per-row count) has to stay in lock
// step with clampScroll, or the cursor can scroll off screen while treeCur
// still reports a valid row.
func TestGraphCursorStaysInViewport(t *testing.T) {
	m := fixtureModel(t, 120, 6) // a short body so scrolling actually engages
	fixtureTree(t, m)
	m.treeGraph = true
	m.clampScroll()

	for m.treeCur < len(m.treeRows)-1 {
		m.moveTree(1)
		row, total, ok := m.currentGraphRow()
		if !ok {
			t.Fatalf("currentGraphRow() not ok at treeCur=%d", m.treeCur)
		}
		if row < m.graphTop || row >= m.graphTop+m.bodyHeight() {
			t.Fatalf("cursor row %d out of viewport [%d, %d) at treeCur=%d (total=%d)",
				row, m.graphTop, m.graphTop+m.bodyHeight(), m.treeCur, total)
		}
	}
}
