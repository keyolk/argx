package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

// ---- the confirm prompt ----

// Every confirm in argx guards something destructive, so the cursor starts on
// No and enter takes whatever it is on. An enter reflex on a modal that just
// appeared must not sync a cluster.
func TestConfirmStartsOnNoAndEnterTakesIt(t *testing.T) {
	m := newTestModel(t, "alpha")
	ran := false
	m.confirm = confirmState{
		title:  "Sync?",
		action: func() tea.Cmd { ran = true; return nil },
	}
	m.overlay = overlayConfirm

	press(t, m, "enter")
	if ran {
		t.Error("enter on a fresh prompt must not run the action — the cursor starts on No")
	}
	if m.overlay != overlayNone {
		t.Error("it should still have closed the prompt")
	}
}

// Moving to Yes and pressing enter is the other half: the answer is reachable
// by the same two keys that work on every list.
func TestConfirmMovesToYesAndCommits(t *testing.T) {
	for _, k := range []string{"l", "right", "h", "left", "tab"} {
		m := newTestModel(t, "alpha")
		ran := false
		m.confirm = confirmState{action: func() tea.Cmd { ran = true; return nil }}
		m.overlay = overlayConfirm

		press(t, m, k, "enter")
		if !ran {
			t.Errorf("%q then enter should have confirmed", k)
		}
	}
}

// y and n still answer outright, without moving anything: they are faster once
// you know them, and every existing habit depends on them.
func TestConfirmStillAnswersWithYAndN(t *testing.T) {
	m := newTestModel(t, "alpha")
	ran := false
	m.confirm = confirmState{action: func() tea.Cmd { ran = true; return nil }}
	m.overlay = overlayConfirm
	press(t, m, "y")
	if !ran {
		t.Error("y should confirm from wherever the cursor is")
	}

	m = newTestModel(t, "alpha")
	ran = false
	m.confirm = confirmState{action: func() tea.Cmd { ran = true; return nil }}
	m.overlay = overlayConfirm
	// Even with the cursor moved onto Yes, n cancels.
	press(t, m, "l", "n")
	if ran {
		t.Error("n should cancel even with the cursor on Yes")
	}
	if m.overlay != overlayNone {
		t.Error("n should close the prompt")
	}
}

// The prompt has to say which way enter will go, and say it without color: the
// caret is the part a monochrome terminal can still read.
func TestConfirmShowsWhichChoiceIsSelected(t *testing.T) {
	m := fixtureModel(t, 100, 24)
	m.confirm = confirmState{title: "Sync?", body: []string{"one application"}}
	m.overlay = overlayConfirm

	if out := m.View(); !strings.Contains(out, "› No") {
		t.Errorf("the cursor should start on No and be visible without color:\n%s", out)
	}
	m.confirm.yes = true
	if out := m.View(); !strings.Contains(out, "› Yes") {
		t.Errorf("the caret should follow the cursor:\n%s", out)
	}
}

// ---- the sync options modal ----

// The toggles were reachable only by knowing p, d and w. They are a list, so
// they move like one.
func TestSyncOptionsCursorTogglesRows(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.overlay = overlaySyncOpts

	press(t, m, " ")
	if !m.syncOpts.prune {
		t.Error("space on the first row should toggle prune")
	}
	press(t, m, "j", " ")
	if !m.syncOpts.dryRun {
		t.Error("j then space should toggle dry-run")
	}
	press(t, m, "j", "l")
	if !m.syncOpts.schedule {
		t.Error("l should flip the checkbox under the cursor")
	}
}

// The cursor cannot leave the list, in either direction.
func TestSyncOptionsCursorStaysInRange(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.overlay = overlaySyncOpts

	press(t, m, "k", "k", "k")
	if m.syncOpts.cur != 0 {
		t.Errorf("cur = %d, want it clamped at the top", m.syncOpts.cur)
	}
	press(t, m, "j", "j", "j", "j", "j")
	if want := len(m.syncOptToggles()) - 1; m.syncOpts.cur != want {
		t.Errorf("cur = %d, want it clamped at %d", m.syncOpts.cur, want)
	}
}

// The letters keep working: a reader who learned them should not have to
// relearn the modal because it grew a cursor.
func TestSyncOptionsLettersStillToggle(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.overlay = overlaySyncOpts

	press(t, m, "p", "d", "w")
	if !m.syncOpts.prune || !m.syncOpts.dryRun || !m.syncOpts.schedule {
		t.Errorf("p/d/w should each still toggle: %+v", m.syncOpts)
	}
	if m.syncOpts.cur != 0 {
		t.Errorf("a letter should not move the cursor, cur = %d", m.syncOpts.cur)
	}
}

// The cursor has to be visible, or it is not a cursor.
func TestSyncOptionsShowsItsCursor(t *testing.T) {
	m := fixtureModel(t, 100, 24)
	m.overlay = overlaySyncOpts

	out := m.View()
	if !strings.Contains(out, "› [ ] p prune") {
		t.Errorf("the cursor should start on the first toggle:\n%s", out)
	}
	m.syncOpts.cur = 2
	if out := m.View(); !strings.Contains(out, "› [ ] w wait") {
		t.Errorf("the caret should follow the cursor:\n%s", out)
	}
}

// ---- h/l and the arrows across the screens ----

// Every list that drills in must do it with → as well as enter. The one screen
// where l cannot carry that half is the resource tree, where l is already logs
// — which is exactly why the arrow has to work there.
func TestRightArrowDrillsInOnEveryList(t *testing.T) {
	m := newTestModel(t, "alpha")
	press(t, m, "right")
	if m.screen != screenApp {
		t.Errorf("→ on the application list should drill in, screen = %v", m.screen)
	}

	// The RESOURCES tab: l is logs, so → is the only drill-in arrow.
	m = newTestModel(t, "alpha")
	press(t, m, "enter")
	m.app = &m.apps[0]
	m.tree = []argocd.TreeRow{
		{Node: argocd.Node{ResourceRef: argocd.ResourceRef{UID: "u1", Kind: "Pod", Name: "web-0"}}},
	}
	m.applyTreeFilter()
	press(t, m, "right")
	if m.screen != screenManifest {
		t.Errorf("→ on the resource tree should open the manifest, screen = %v", m.screen)
	}
}

// ← goes back wherever esc does, so the pair is symmetric on every screen a
// reader can drill into.
func TestLeftArrowGoesBack(t *testing.T) {
	m := newTestModel(t, "alpha")
	press(t, m, "enter")
	if m.screen != screenApp {
		t.Fatalf("setup: expected to be in the application view, got %v", m.screen)
	}
	press(t, m, "left")
	if m.screen != screenApps {
		t.Errorf("← should go back to the list, screen = %v", m.screen)
	}
}
