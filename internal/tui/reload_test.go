package tui

import (
	"testing"

	"github.com/keyolk/argx/internal/argocd"
)

func testApp(name string) argocd.Application {
	var a argocd.Application
	a.Context = "test"
	a.Metadata.Name = name
	a.Metadata.Namespace = "argocd"
	return a
}

func testTreeRow(uid string) argocd.TreeRow {
	var r argocd.TreeRow
	r.Node.Kind = "Pod"
	r.Node.Name = uid
	r.Node.UID = uid
	return r
}

// A reload that adds an application above the cursor must leave the cursor on
// the same application. Reading the previous key through the old row indices
// after the swap resolved to the new neighbour, and `s` then synced it.
func TestReloadKeepsCursorOnSameAppWhenOneIsAddedAbove(t *testing.T) {
	m := newTestModel(t, "beta", "gamma", "delta")
	m.appCur = 1 // gamma
	if a := m.currentApp(); a == nil || a.Name() != "gamma" {
		t.Fatalf("setup: cursor on %v, want gamma", a)
	}

	m.Update(appsMsg{apps: []argocd.Application{
		testApp("alpha"), testApp("beta"), testApp("gamma"), testApp("delta"),
	}})

	if a := m.currentApp(); a == nil || a.Name() != "gamma" {
		t.Errorf("cursor moved to %v after reload, want gamma", a)
	}
	if got := m.markedApps(); len(got) != 1 || got[0].Name() != "gamma" {
		t.Errorf("the next action would target %v, want gamma", names(got))
	}
}

// Same ordering bug in the resource tree.
func TestTreeReloadKeepsCursorOnSameResource(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.screen = screenApp
	m.app = &m.apps[0]
	m.Update(treeMsg{id: m.treeID, app: m.app, rows: []argocd.TreeRow{
		testTreeRow("b"), testTreeRow("c"),
	}})
	m.treeCur = 1 // c

	m.Update(treeMsg{id: m.treeID, app: m.app, rows: []argocd.TreeRow{
		testTreeRow("a"), testTreeRow("b"), testTreeRow("c"),
	}})

	if n := m.currentNode(); n == nil || n.UID != "c" {
		t.Errorf("cursor moved to %v after reload, want c", n)
	}
}
