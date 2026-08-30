package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

// sched builds a waiting schedule due at `in` from now.
func sched(id int, name string, in time.Duration) scheduled {
	return scheduled{
		id: id, appName: name, context: "test",
		app:       argocd.AppRef{Context: "test", Name: name, AppNamespace: "argocd"},
		at:        time.Now().Add(in),
		window:    argocd.SyncWindow{Kind: "allow", Schedule: "0 15 * * *", Duration: "2h", TimeZone: "Asia/Seoul"},
		revision:  "abc123",
		targetRev: "main",
		state:     scheduleWaiting,
	}
}

// A schedule fires only once its time has come. Firing early would sync into
// the closed window this feature exists to avoid.
func TestOnlyDueSchedulesRun(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		s    scheduled
		want bool
	}{
		{"an hour away", sched(1, "later", time.Hour), false},
		{"a second away", sched(2, "soon", time.Second), false},
		{"a second ago", sched(3, "now", -time.Second), true},
		{"no wait at all", scheduled{state: scheduleWaiting}, true},
	}
	for _, c := range cases {
		if got := c.s.due(now); got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}

	// A schedule that already ran does not run again, whatever its time says.
	done := sched(4, "done", -time.Hour)
	done.state = scheduleDone
	if done.due(now) {
		t.Error("a finished schedule must not run again")
	}
	running := sched(5, "running", -time.Hour)
	running.state = scheduleRunning
	if running.due(now) {
		t.Error("a schedule already in flight must not be issued twice")
	}
}

// The soonest sync leads and finished rows fall to the bottom: what has not
// happened yet is what the reader is checking on.
func TestSchedulesSortSoonestFirst(t *testing.T) {
	m := newTestModel(t)
	late := sched(1, "late", 3*time.Hour)
	soon := sched(2, "soon", time.Minute)
	finished := sched(3, "finished", -time.Hour)
	finished.state = scheduleDone
	finished.ranAt = time.Now()

	m.schedules = []scheduled{late, finished, soon}
	m.sortSchedules()

	got := []string{m.schedules[0].appName, m.schedules[1].appName, m.schedules[2].appName}
	want := []string{"soon", "late", "finished"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// Only unfinished schedules count as pending — that number gates the ticker and
// the quit prompt, so counting a finished one would wake argx forever and ask
// about work that is already done.
func TestPendingCountsOnlyUnfinished(t *testing.T) {
	m := newTestModel(t)
	waiting := sched(1, "waiting", time.Hour)
	running := sched(2, "running", -time.Minute)
	running.state = scheduleRunning
	for _, st := range []scheduleState{scheduleDone, scheduleFailed, scheduleCancelled} {
		s := sched(int(st)+10, "finished", -time.Hour)
		s.state = st
		m.schedules = append(m.schedules, s)
	}
	m.schedules = append(m.schedules, waiting, running)

	if got := m.pendingSchedules(); got != 2 {
		t.Errorf("pending = %d, want 2 (one waiting, one running)", got)
	}
}

// Cancelling a waiting sync keeps the row with its reason. A row that vanished
// would take the record of what was asked for with it.
func TestCancelKeepsTheRow(t *testing.T) {
	m := newTestModel(t)
	m.schedules = []scheduled{sched(1, "web", time.Hour)}

	m.cancelSchedule(1)

	if len(m.schedules) != 1 {
		t.Fatalf("schedules = %d, want the row kept", len(m.schedules))
	}
	if m.schedules[0].state != scheduleCancelled {
		t.Errorf("state = %v, want cancelled", m.schedules[0].state)
	}
	if m.schedules[0].reason == "" {
		t.Error("a cancelled row should say why it did not run")
	}
	if m.pendingSchedules() != 0 {
		t.Error("a cancelled sync is no longer pending")
	}
}

// A sync already in flight cannot be recalled, and saying it was would be the
// worst kind of lie here — the sync happens either way.
func TestCancelRefusesARunningSync(t *testing.T) {
	m := newTestModel(t)
	s := sched(1, "web", -time.Minute)
	s.state = scheduleRunning
	m.schedules = []scheduled{s}

	m.cancelSchedule(1)

	if m.schedules[0].state != scheduleRunning {
		t.Errorf("state = %v, want it left running", m.schedules[0].state)
	}
	if m.toast == "" {
		t.Error("refusing to cancel should say so rather than doing nothing visible")
	}
}

// Clearing drops the finished rows and keeps the pending ones.
func TestClearFinishedKeepsPending(t *testing.T) {
	m := newTestModel(t)
	done := sched(1, "done", -time.Hour)
	done.state = scheduleDone
	m.schedules = []scheduled{done, sched(2, "waiting", time.Hour)}

	if n := m.clearFinishedSchedules(); n != 1 {
		t.Errorf("cleared %d, want 1", n)
	}
	if len(m.schedules) != 1 || m.schedules[0].appName != "waiting" {
		t.Fatalf("kept %v, want just the waiting one", m.schedules)
	}
}

// A result for a row that was cleared mid-flight is reported, not dropped: the
// sync still happened.
func TestResultForAClearedRowIsAnError(t *testing.T) {
	m := newTestModel(t)
	err := m.applyScheduleResult(scheduleRunMsg{id: 99, state: scheduleDone})
	if err == nil {
		t.Error("a result with no row should be reported, not silently dropped")
	}
}

// The ticker starts when the first schedule arrives and does not start a second
// one when more are added — two tickers would double the wakeups.
func TestTickerStartsOnceForPendingSchedules(t *testing.T) {
	m := newTestModel(t)

	if cmd := m.addSchedules(scheduledMsg{items: []scheduled{sched(1, "web", time.Hour)}}); cmd == nil {
		t.Fatal("the first schedule should start the ticker")
	}
	if cmd := m.addSchedules(scheduledMsg{items: []scheduled{sched(2, "api", time.Hour)}}); cmd != nil {
		t.Error("a second schedule must not start a second ticker")
	}
	// Once everything has finished, the next batch starts it again.
	for i := range m.schedules {
		m.schedules[i].state = scheduleDone
	}
	if cmd := m.addSchedules(scheduledMsg{items: []scheduled{sched(3, "db", time.Hour)}}); cmd == nil {
		t.Error("scheduling again after everything finished should restart the ticker")
	}
}

// An idle argx renders zero frames: with nothing pending the tick does no work
// and asks for no further tick.
func TestTickWithNothingPendingStopsTicking(t *testing.T) {
	m := newTestModel(t)
	_, cmd := m.Update(scheduleTickMsg(time.Now()))
	if cmd != nil {
		t.Error("a tick with no pending schedules should not schedule another")
	}
}

// A window argx could not read is reported rather than scheduled around: a
// schedule computed from a window it does not understand would fire at the
// wrong time, which is exactly the rejected sync this avoids.
func TestUnschedulableApplicationsAreReported(t *testing.T) {
	m := newTestModel(t)
	m.addSchedules(scheduledMsg{failed: []string{"web: cannot parse schedule"}})

	if !strings.Contains(m.toast, "web") {
		t.Errorf("toast = %q, want the application that could not be scheduled named", m.toast)
	}
	if len(m.schedules) != 0 {
		t.Error("nothing should have been scheduled")
	}
}

// Quitting with pending schedules asks first, because quitting is what cancels
// them — there is no daemon to pick them up.
func TestQuitAsksWhenSchedulesArePending(t *testing.T) {
	m := newTestModel(t, "web")
	m.schedules = []scheduled{sched(1, "web", time.Hour)}

	_, cmd := m.Update(key("q"))

	if cmd != nil {
		t.Error("q should open a prompt, not quit outright, while syncs are waiting")
	}
	if m.overlay != overlayConfirm {
		t.Fatalf("overlay = %v, want the quit confirmation", m.overlay)
	}
	if !strings.Contains(strings.Join(m.confirm.body, " "), "web") {
		t.Error("the prompt should name what is about to be dropped")
	}
}

// Ctrl+C is the terminal's interrupt: it quits immediately even with schedules
// pending. An interrupt that asks a question is not an interrupt.
func TestCtrlCQuitsDespitePendingSchedules(t *testing.T) {
	m := newTestModel(t, "web")
	m.schedules = []scheduled{sched(1, "web", time.Hour)}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if cmd == nil {
		t.Fatal("ctrl+c must quit immediately")
	}
	if m.overlay != overlayNone {
		t.Error("ctrl+c must not open a prompt")
	}
}

// Nothing pending, nothing to warn about.
func TestQuitIsImmediateWithNoSchedules(t *testing.T) {
	m := newTestModel(t, "web")
	if _, cmd := m.Update(key("q")); cmd == nil {
		t.Error("q should quit outright when no sync is waiting")
	}
}

// W reaches the list from anywhere and toggles back, so getting out is the key
// that got you in.
func TestWTogglesTheScheduleList(t *testing.T) {
	m := newTestModel(t, "web")
	m.Update(key("W"))
	if m.screen != screenSchedule {
		t.Fatalf("screen = %v, want the schedule list", m.screen)
	}
	m.Update(key("W"))
	if m.screen != screenApps {
		t.Errorf("screen = %v, want back at the application list", m.screen)
	}
}

// The list is navigable and its cursor stays inside it — the bug that made j/k
// silently do nothing in the window view.
func TestScheduleListNavigates(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.schedules = []scheduled{
		sched(1, "a", time.Minute), sched(2, "b", 2*time.Minute), sched(3, "c", 3*time.Minute),
	}
	m.screen = screenSchedule

	m.Update(key("j"))
	m.Update(key("j"))
	if m.scheduleCur != 2 {
		t.Errorf("cursor = %d, want 2 after two j", m.scheduleCur)
	}
	// Past the end stays on the last row rather than pointing at nothing.
	m.Update(key("j"))
	if m.scheduleCur != 2 {
		t.Errorf("cursor = %d, want it clamped to the last row", m.scheduleCur)
	}
	m.Update(key("g"))
	if m.scheduleCur != 0 {
		t.Errorf("cursor = %d, want the top after g", m.scheduleCur)
	}
}

// The view names what each row is waiting for. "waiting" alone leaves the
// reader guessing whether argx is stuck or the window is hours away.
func TestScheduleViewNamesTheWindow(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	m.schedules = []scheduled{sched(1, "web-frontend", 3*time.Hour)}
	m.screen = screenSchedule

	out := stripANSI(m.View())
	for _, want := range []string{"web-frontend", "waiting", "0 15 * * *", "Asia/Seoul"} {
		if !strings.Contains(out, want) {
			t.Errorf("the view does not mention %q:\n%s", want, out)
		}
	}
}

// A declined sync shows its reason. A row that says only "cancelled" sends the
// reader to the Argo CD UI to work out what argx already knew.
func TestDeclinedScheduleShowsItsReason(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	s := sched(1, "web-frontend", -time.Minute)
	m.schedules = []scheduled{s}
	m.screen = screenSchedule

	m.Update(scheduleRunMsg{
		id: 1, state: scheduleCancelled, reason: "target revision changed: main → release-2",
	})

	out := stripANSI(m.View())
	if !strings.Contains(out, "target revision changed") {
		t.Errorf("the reason is missing from the view:\n%s", out)
	}
	if !strings.Contains(out, "cancelled") {
		t.Errorf("the state is missing from the view:\n%s", out)
	}
}

// The empty list says how to fill it rather than showing a blank screen.
func TestEmptyScheduleListExplainsItself(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.screen = screenSchedule

	if out := stripANSI(m.View()); !strings.Contains(out, "no scheduled syncs") {
		t.Errorf("empty list should explain itself:\n%s", out)
	}
}

// Opening the sync modal on an application whose window is closed pre-selects
// waiting: syncing now would be refused, and the reader would find out by
// having a failed operation recorded against the application.
func TestSyncModalDefaultsToWaitingWhenBlocked(t *testing.T) {
	m := appModel(t, nil)
	m.Update(windowsMsg{id: m.windowID, windows: &argocd.AppSyncWindows{
		AssignedWindows: []argocd.SyncWindow{{Kind: "deny", Schedule: "0 0 * * *", Duration: "4h"}},
		CanSync:         false,
	}})
	m.appMarks[m.app.Key()] = true
	m.screen = screenApps

	m.Update(key("s"))

	if m.overlay != overlaySyncOpts {
		t.Fatalf("overlay = %v, want the sync options", m.overlay)
	}
	if !m.syncOpts.schedule {
		t.Error("a blocked application should default to waiting for its window")
	}
}

// With syncing allowed the modal defaults to syncing now: waiting for a window
// that is open would be a delay nobody asked for.
func TestSyncModalDefaultsToSyncingWhenAllowed(t *testing.T) {
	m := appModel(t, nil)
	m.Update(windowsMsg{id: m.windowID, windows: &argocd.AppSyncWindows{CanSync: true}})
	m.appMarks[m.app.Key()] = true
	m.screen = screenApps

	m.Update(key("s"))

	if m.syncOpts.schedule {
		t.Error("an unblocked application should sync now, not wait")
	}
}

// w toggles waiting in the modal, and confirming a scheduled sync says the
// waiting only lasts while argx runs.
func TestScheduleConfirmationSaysItIsSessionOnly(t *testing.T) {
	m := appModel(t, nil)
	m.appMarks[m.app.Key()] = true
	m.screen = screenApps
	m.Update(key("s"))
	m.Update(key("w"))

	if !m.syncOpts.schedule {
		t.Fatal("w should toggle waiting on")
	}
	m.Update(key("enter"))

	if m.overlay != overlayConfirm {
		t.Fatalf("overlay = %v, want the confirmation", m.overlay)
	}
	body := stripANSI(strings.Join(m.confirm.body, " "))
	if !strings.Contains(m.confirm.title, "Schedule") {
		t.Errorf("title = %q, want it to say this schedules rather than syncs", m.confirm.title)
	}
	if !strings.Contains(body, "argx") {
		t.Errorf("the prompt should say the waiting ends when argx does:\n%s", body)
	}
}

// The wait is rendered for a human, not as a Go duration.
func TestFormatWait(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Hour, "now"},
		{0, "now"},
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
	}
	for _, c := range cases {
		if got := formatWait(c.d); got != c.want {
			t.Errorf("formatWait(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// A due schedule is marked in flight by the tick and is not issued twice, which
// is what stops a slow server from getting the same sync every ten seconds.
func TestTickIssuesDueSchedulesOnce(t *testing.T) {
	m := newTestModel(t)
	m.schedules = []scheduled{sched(1, "due", -time.Minute), sched(2, "later", time.Hour)}

	_, cmd := m.Update(scheduleTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("a due schedule should produce work and another tick")
	}
	if m.schedules[0].state != scheduleRunning {
		t.Errorf("due schedule state = %v, want running", m.schedules[0].state)
	}
	if m.schedules[1].state != scheduleWaiting {
		t.Errorf("the later one should still be waiting, got %v", m.schedules[1].state)
	}

	// A second tick before the first finishes must not issue it again.
	m.Update(scheduleTickMsg(time.Now()))
	if m.schedules[0].state != scheduleRunning {
		t.Errorf("state = %v, want it still running and not re-issued", m.schedules[0].state)
	}
}
