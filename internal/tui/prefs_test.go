package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/prefs"
)

// quits runs cmd as Bubble Tea would and reports whether it ends the program:
// quitting flushes ui.json first, and only the flush's Saved{Final} quits.
func quits(t *testing.T, m *Model, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case prefs.Saved:
		if !msg.Final {
			return false
		}
		_, next := m.Update(msg)
		return next != nil && quits(t, m, next)
	}
	return false
}

// Quitting writes the state, then quits — a debounced write still pending
// when the program exits would be lost.
func TestQuitFlushesStateBeforeExiting(t *testing.T) {
	m := newTestModel(t, "alpha", "bravo")
	m.handleKey(key("/"))
	m.handleKey(key("b"))
	m.handleKey(key("enter"))

	_, cmd := m.Update(key("q"))
	if !quits(t, m, cmd) {
		t.Fatal("q did not quit")
	}
	data, err := os.ReadFile(prefs.Path(prefsApp))
	if err != nil {
		t.Fatalf("ui.json was not written: %v", err)
	}
	if !strings.Contains(string(data), `"app_filter": "b"`) {
		t.Errorf("ui.json = %s, want the filter saved", data)
	}
}

// Filter, tab, auto-refresh and scheduled syncs come back on the next start.
func TestUIStateRoundTrips(t *testing.T) {
	m := newTestModel(t, "web")
	m.appFilter = parseAppFilter("proj:web")
	m.lastTab = tabHistory
	m.autoRefresh = true
	s := sched(7, "web", time.Hour)
	s.opts.Prune = true
	m.schedules = []scheduled{s}

	_, cmd := m.Update(key("q")) // a pending schedule asks first
	if m.overlay != overlayConfirm {
		t.Fatal("setup: q should ask with a pending schedule")
	}
	_, cmd = m.Update(key("y"))
	if !quits(t, m, cmd) {
		t.Fatal("y did not quit")
	}

	// A fresh model in the same state directory.
	m2 := newTestModelSameState(t, "web")
	if m2.appFilter.raw != "proj:web" || m2.lastTab != tabHistory || !m2.autoRefresh {
		t.Errorf("restored filter=%q tab=%v auto=%v", m2.appFilter.raw, m2.lastTab, m2.autoRefresh)
	}
	if len(m2.schedules) != 1 || m2.schedules[0].id != 7 || !m2.schedules[0].opts.Prune ||
		m2.schedules[0].state != scheduleWaiting {
		t.Fatalf("schedules = %+v, want the waiting one back", m2.schedules)
	}
	if m2.restored != 1 {
		t.Errorf("restored = %d, want 1 pending", m2.restored)
	}
	// New ids continue after the restored ones, never colliding.
	if m2.scheduleID < 7 {
		t.Errorf("scheduleID = %d, want at least 7", m2.scheduleID)
	}
}

// A sync that was in flight when argx exited is not run again on restore: its
// outcome is unknown, and repeating it could sync twice.
func TestInFlightScheduleIsNotRerunAfterRestart(t *testing.T) {
	m := newTestModel(t)
	s := sched(1, "web", -time.Minute)
	s.state = scheduleSyncing
	n := m.restoreUI(uiState{Schedules: []savedSchedule{{ID: 1, AppName: "web", State: s.state.String()}}})
	if n != 0 {
		t.Errorf("pending = %d, want 0", n)
	}
	if m.schedules[0].state != scheduleFailed || !strings.Contains(m.schedules[0].reason, "in flight") {
		t.Errorf("restored = %v %q, want it marked with the reason", m.schedules[0].state, m.schedules[0].reason)
	}
}

// An Update that changes nothing persisted writes nothing.
func TestUnchangedStateIsNotRewritten(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.savedUI = nil
	if m.persistUI() == nil {
		t.Fatal("setup: the first persist should save")
	}
	if m.persistUI() != nil {
		t.Error("a second persist with nothing changed should not save again")
	}
}
