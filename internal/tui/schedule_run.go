package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Running a scheduled sync.
//
// The gap between scheduling and firing is measured in hours, and a lot can
// change in it: the target revision moves, someone edits the window, someone
// syncs by hand. A scheduled sync therefore re-checks its premises immediately
// before it runs, and declines rather than deploying something nobody agreed
// to. Declining is recorded with its reason — a schedule that vanished silently
// would be worse than one that fired wrongly, because nobody would know.

// scheduleRunMsg reports the outcome of one attempt.
type scheduleRunMsg struct {
	id     int
	state  scheduleState
	reason string
}

// runDueSchedules issues the syncs whose time has come.
//
// One command per schedule, so a slow server does not hold up the others and a
// failure is attributed to the right row.
func (m *Model) runDueSchedules(now time.Time) tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.schedules {
		s := &m.schedules[i]
		if !s.due(now) {
			continue
		}
		s.state = scheduleRunning
		cmds = append(cmds, m.runScheduleCmd(*s))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// runScheduleCmd re-checks a schedule's premises and, if they hold, syncs.
func (m *Model) runScheduleCmd(s scheduled) tea.Cmd {
	client, err := m.fleet.Client(s.context)
	if err != nil {
		return func() tea.Msg {
			return scheduleRunMsg{id: s.id, state: scheduleFailed, reason: err.Error()}
		}
	}
	ctx := m.ctx

	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 300*time.Second)
		defer cancel()

		decline := func(reason string) tea.Msg {
			return scheduleRunMsg{id: s.id, state: scheduleCancelled, reason: reason}
		}

		// The application as it is now, not as it was when scheduled.
		app, err := client.GetApplication(c, s.app.Name, s.app.AppNamespace)
		if err != nil {
			return scheduleRunMsg{id: s.id, state: scheduleFailed, reason: err.Error()}
		}
		app.Context = s.context

		// 1. Already Synced. Syncing anyway would be a no-op operation
		//    recorded against the application for no reason.
		if app.Status.Sync.Status == "Synced" && app.Status.Sync.Revision == s.revision {
			return decline("already synced at the scheduled revision")
		}

		// 2. The target moved. A sync is what you asked for at the moment you
		//    asked for it; deploying a revision that arrived in the meantime is
		//    not what was agreed to.
		if src, _ := app.PrimarySource(); src.TargetRevision != s.targetRev {
			return decline(fmt.Sprintf("target revision changed: %s → %s",
				orDash(s.targetRev), orDash(src.TargetRevision)))
		}

		// 3. Auto-sync turned on while waiting. The controller now owns this
		//    application's syncing, and a scheduled sync would race it.
		if on, _, _ := app.AutoSync(); on && !s.autoSync {
			return decline("auto-sync was enabled while this was waiting")
		}

		// 4. The window. Re-asked of the server rather than recomputed, because
		//    a window edited in the meantime is exactly the case this guards,
		//    and the server's answer is the one that decides.
		w, err := client.SyncWindows(c, app)
		if err != nil {
			return scheduleRunMsg{id: s.id, state: scheduleFailed, reason: err.Error()}
		}
		if !w.CanSync {
			return decline("the sync window is still closed")
		}

		if _, err := client.Sync(c, s.app.Name, s.opts); err != nil {
			return scheduleRunMsg{id: s.id, state: scheduleFailed, reason: err.Error()}
		}
		return scheduleRunMsg{id: s.id, state: scheduleDone}
	}
}

// cancelSchedule removes a pending schedule.
func (m *Model) cancelSchedule(id int) {
	for i := range m.schedules {
		if m.schedules[i].id != id {
			continue
		}
		if m.schedules[i].state != scheduleWaiting {
			// A sync already in flight cannot be recalled, and pretending
			// otherwise would be the worst kind of lie here.
			m.setToast("this sync is already running")
			return
		}
		m.schedules[i].state = scheduleCancelled
		m.schedules[i].reason = "cancelled"
		m.schedules[i].ranAt = time.Now()
		m.sortSchedules()
		return
	}
}

// clearFinishedSchedules drops the rows that are done with.
func (m *Model) clearFinishedSchedules() int {
	kept := m.schedules[:0]
	n := 0
	for _, s := range m.schedules {
		if !s.state.finished() {
			kept = append(kept, s)
			continue
		}
		n++
	}
	m.schedules = kept
	return n
}

// errScheduleGone is returned when a run reports for a schedule that is no
// longer in the list, which happens if it was cleared mid-flight.
var errScheduleGone = errors.New("schedule no longer exists")

// applyScheduleResult records an outcome.
func (m *Model) applyScheduleResult(msg scheduleRunMsg) error {
	for i := range m.schedules {
		if m.schedules[i].id != msg.id {
			continue
		}
		m.schedules[i].state = msg.state
		m.schedules[i].reason = msg.reason
		m.schedules[i].ranAt = time.Now()
		m.sortSchedules()
		return nil
	}
	return errScheduleGone
}
