package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

// Scheduled syncs.
//
// Syncing an application whose sync window is closed does not simply fail: Argo
// CD records a failed operation on it, which is noise in the one place someone
// looks when something is wrong. The alternative people fall back on is
// remembering to come back at 15:00, which is worse.
//
// So argx waits. A scheduled sync sits until the window opens, re-checks that
// the sync is still the one that was asked for, and issues it.
//
// It lives only as long as the TUI does. There is no daemon, no state file, and
// nothing that outlives the process — a sync that fires while nobody is
// watching is not something to build by accident. Quitting says plainly what
// will be dropped.

// scheduleState is where a scheduled sync is in its life.
type scheduleState int

const (
	// scheduleWaiting is the normal state: the window has not opened yet.
	scheduleWaiting scheduleState = iota
	// scheduleRunning is the moment the sync request is in flight.
	scheduleRunning
	// scheduleSyncing is after Argo CD accepted the request and while the sync
	// itself runs. Accepting a request and completing a sync are different
	// events, and a row that stopped at the first would report success for a
	// sync that went on to fail.
	scheduleSyncing
	scheduleDone
	scheduleFailed
	// scheduleCancelled covers both the user cancelling and argx declining to
	// run — a spec that changed, a window that moved. The reason says which.
	scheduleCancelled
)

func (s scheduleState) String() string {
	switch s {
	case scheduleRunning:
		return "running"
	case scheduleSyncing:
		return "syncing"
	case scheduleDone:
		return "synced"
	case scheduleFailed:
		return "failed"
	case scheduleCancelled:
		return "cancelled"
	default:
		return "waiting"
	}
}

// finished reports whether this state is terminal.
func (s scheduleState) finished() bool {
	return s == scheduleDone || s == scheduleFailed || s == scheduleCancelled
}

// scheduled is one pending sync.
type scheduled struct {
	// id is stable for the session, so a row can be cancelled while others come
	// and go.
	id int

	app     argocd.AppRef
	appName string
	context string
	opts    argocd.SyncOptions

	// at is when the window opens. Zero means "as soon as possible", which
	// happens when the window was already open at schedule time.
	at time.Time
	// window is the one being waited for, for display.
	window argocd.SyncWindow

	// revision, targetRev and autoSync are what the application looked like when
	// the sync was scheduled. A sync is what you asked for at the moment you
	// asked for it; if the target moved in the meantime, deploying the new one
	// is not what was agreed to. See schedule_run.go.
	revision  string
	targetRev string
	autoSync  bool

	state  scheduleState
	reason string
	// ranAt records when it finished, so the list says what happened and when.
	ranAt time.Time
	// startedAt is when Argo CD accepted the sync. The operation that follows
	// is identified by it: an operation that started before argx asked belongs
	// to somebody else, and reporting its outcome as this schedule's would
	// attribute a stranger's failure to a sync argx ran.
	startedAt time.Time
}

// due reports whether this schedule's time has come.
func (s scheduled) due(now time.Time) bool {
	return s.state == scheduleWaiting && (s.at.IsZero() || !s.at.After(now))
}

// remaining is how long until the window opens.
func (s scheduled) remaining(now time.Time) time.Duration {
	if s.at.IsZero() {
		return 0
	}
	return s.at.Sub(now)
}

// scheduleTickInterval is how often pending schedules are examined.
//
// Ten seconds: a sync window is measured in hours, so being up to ten seconds
// late costs nothing, and waking every second for hours costs a laptop fan.
const scheduleTickInterval = 10 * time.Second

// scheduleTickMsg drives the scheduler.
type scheduleTickMsg time.Time

// scheduleTickCmd schedules the next examination.
func scheduleTickCmd() tea.Cmd {
	return tea.Tick(scheduleTickInterval, func(t time.Time) tea.Msg {
		return scheduleTickMsg(t)
	})
}

// scheduledMsg carries the computed schedules back to the UI thread.
type scheduledMsg struct {
	items []scheduled
	// failed names the applications whose windows argx could not read. A window
	// argx cannot parse is not one it can wait for, and scheduling into it would
	// produce exactly the rejected sync this feature exists to avoid.
	failed []string
}

// scheduleSyncsCmd works out when each application's windows next allow a sync.
//
// The windows are fetched here rather than reused from the window view, because
// scheduling happens from the application list too, where they are not loaded —
// and because the per-application payload drops the time zone, so the project's
// copy has to be consulted to read the schedule in the right zone.
func (m *Model) scheduleSyncsCmd(apps []argocd.Application, opt argocd.SyncOptions) tea.Cmd {
	fleet := m.fleet
	ctx := m.ctx
	// Ids are assigned on the UI thread so two concurrent schedulings cannot
	// hand out the same one.
	base := m.scheduleID
	m.scheduleID += len(apps)

	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		var (
			items  []scheduled
			failed []string
		)
		now := time.Now()

		for i := range apps {
			a := apps[i]
			at, win, err := nextSyncableForApp(c, fleet, &a, now)
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", a.Name(), err))
				continue
			}

			src, _ := a.PrimarySource()
			autoOn, _, _ := a.AutoSync()

			o := opt
			o.AppNamespace = a.AppNamespace()

			items = append(items, scheduled{
				id:        base + i + 1,
				app:       a.Ref(),
				appName:   a.Name(),
				context:   a.Context,
				opts:      o,
				at:        at,
				window:    win,
				revision:  a.Status.Sync.Revision,
				targetRev: src.TargetRevision,
				autoSync:  autoOn,
				state:     scheduleWaiting,
			})
		}
		return scheduledMsg{items: items, failed: failed}
	}
}

// nextSyncableForApp asks the server for an application's windows and computes
// when they next allow a sync.
func nextSyncableForApp(ctx context.Context, fleet *argocd.Fleet, a *argocd.Application, now time.Time) (time.Time, argocd.SyncWindow, error) {
	client, err := fleet.ClientFor(a)
	if err != nil {
		return time.Time{}, argocd.SyncWindow{}, err
	}

	w, err := client.SyncWindows(ctx, a)
	if err != nil {
		return time.Time{}, argocd.SyncWindow{}, err
	}
	if w == nil || len(w.AssignedWindows) == 0 {
		// No window governs this application, so nothing is waiting for.
		return time.Time{}, argocd.SyncWindow{}, nil
	}
	// The server already answered the only question it answers. Trust it for
	// "now" and compute only the "when" it does not provide.
	if w.CanSync {
		return time.Time{}, argocd.SyncWindow{}, nil
	}

	// The per-application payload omits the time zone, and a schedule read in
	// the wrong zone is hours off, so the project's copies supply it.
	full, err := windowDetail(ctx, client, a.Spec.Project, w.AssignedWindows)
	if err != nil {
		return time.Time{}, argocd.SyncWindow{}, err
	}
	return argocd.NextSyncableAt(full, now, true)
}

// windowDetail fills in the fields the per-application payload drops.
//
// A window that the project's list does not carry is an error rather than a
// window used as-is: these are edited by automation, and reading an
// Asia/Seoul schedule as UTC would schedule a sync nine hours from the truth.
func windowDetail(ctx context.Context, client *argocd.Client, project string, assigned []argocd.SyncWindow) ([]argocd.SyncWindow, error) {
	pw, err := client.ProjectSyncWindows(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("cannot read project %s: %w", project, err)
	}

	key := func(w argocd.SyncWindow) string {
		return w.Kind + "|" + w.Schedule + "|" + w.Duration
	}
	detail := make(map[string]argocd.SyncWindow, len(pw))
	for _, w := range pw {
		detail[key(w)] = w
	}

	out := make([]argocd.SyncWindow, 0, len(assigned))
	for _, w := range assigned {
		full, ok := detail[key(w)]
		if !ok {
			return nil, fmt.Errorf("window %s %q is not in project %s — it was probably edited just now",
				w.Kind, w.Schedule, project)
		}
		out = append(out, full)
	}
	return out, nil
}

// addSchedules records freshly computed schedules and starts the ticker if it
// is not already running.
func (m *Model) addSchedules(msg scheduledMsg) tea.Cmd {
	wasIdle := m.pendingSchedules() == 0
	m.schedules = append(m.schedules, msg.items...)
	m.sortSchedules()

	switch {
	case len(msg.items) == 0 && len(msg.failed) > 0:
		m.setToast(fmt.Sprintf("could not schedule: %s", msg.failed[0]))
	case len(msg.failed) > 0:
		m.setToast(fmt.Sprintf("scheduled %d, could not schedule %d (%s)",
			len(msg.items), len(msg.failed), msg.failed[0]))
	case len(msg.items) > 0:
		m.setToast(fmt.Sprintf("scheduled %d sync(s) — W to review", len(msg.items)))
	}

	// The ticker runs only while something is pending, so an argx with no
	// schedules still idles at zero frames. Starting a second one would double
	// the wakeups for no benefit.
	if wasIdle && m.pendingSchedules() > 0 {
		return scheduleTickCmd()
	}
	return nil
}

// pendingSchedules is how many syncs are waiting or in flight, which the status
// line shows and the exit path warns about.
func (m *Model) pendingSchedules() int {
	n := 0
	for _, s := range m.schedules {
		if !s.state.finished() {
			n++
		}
	}
	return n
}

// sortSchedules puts the soonest first, with finished ones at the end.
func (m *Model) sortSchedules() {
	sort.SliceStable(m.schedules, func(i, j int) bool {
		a, b := m.schedules[i], m.schedules[j]
		ai, bi := !a.state.finished(), !b.state.finished()
		if ai != bi {
			return ai
		}
		if !ai {
			// Finished: most recent first, since that is what gets read.
			return a.ranAt.After(b.ranAt)
		}
		return a.at.Before(b.at)
	})
}

// formatWait renders a duration as something a human reads at a glance.
func formatWait(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		h := int(d.Hours())
		mins := int(d.Minutes()) - h*60
		if mins == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, mins)
	}
}

// when describes a schedule's firing time for a one-line summary.
func (s scheduled) when(now time.Time) string {
	if s.at.IsZero() {
		return "as soon as possible"
	}
	return fmt.Sprintf("%s (in %s)",
		s.at.Local().Format("01-02 15:04"), formatWait(s.remaining(now)))
}
