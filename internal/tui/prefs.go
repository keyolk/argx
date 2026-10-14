package tui

// UI state that survives a restart (engine principle E12): the application
// filter, the last tab, auto-refresh, and the scheduled syncs.
//
// Written to $XDG_STATE_HOME/argx/ui.json by tuikit's prefs.Saver: debounced,
// atomic (temp file + rename), off the UI thread, ordered so an older write
// never lands over a newer one, and flushed on quit before the program exits.
//
// Scheduled syncs used to live only in the process, and quitting dropped
// them. They are kept now, but they still run only while argx runs — there is
// no daemon — so a restored schedule whose window opened while argx was closed
// fires on the next start, after the same re-checks every schedule makes
// (target revision, sync status, auto-sync, the window itself).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/prefs"

	"github.com/keyolk/argx/internal/argocd"
)

const prefsApp = "argx"

// uiState is ui.json. Field names are the file's contract; add, do not rename.
type uiState struct {
	AppFilter   string          `json:"app_filter,omitempty"`
	Tab         string          `json:"tab,omitempty"`
	AutoRefresh bool            `json:"auto_refresh,omitempty"`
	Schedules   []savedSchedule `json:"schedules,omitempty"`
}

// savedSchedule is a scheduled sync as stored. A separate type because
// argocd.ResourceRef drops its context and app namespace from JSON (they are
// not part of the API payload); they are restored from the schedule's app.
type savedSchedule struct {
	ID        int                  `json:"id"`
	App       argocd.AppRef        `json:"app"`
	AppName   string               `json:"app_name"`
	Prune     bool                 `json:"prune,omitempty"`
	DryRun    bool                 `json:"dry_run,omitempty"`
	Resources []argocd.ResourceRef `json:"resources,omitempty"`
	At        time.Time            `json:"at"`
	Window    argocd.SyncWindow    `json:"window"`
	Revision  string               `json:"revision,omitempty"`
	TargetRev string               `json:"target_rev,omitempty"`
	AutoSync  bool                 `json:"auto_sync,omitempty"`
	State     string               `json:"state"`
	Reason    string               `json:"reason,omitempty"`
	RanAt     time.Time            `json:"ran_at"`
	StartedAt time.Time            `json:"started_at"`
}

func parseTab(s string) tab {
	for _, t := range allTabs {
		if t.String() == s {
			return t
		}
	}
	return tabResources
}

func parseScheduleState(s string) scheduleState {
	for st := scheduleWaiting; st <= scheduleCancelled; st++ {
		if st.String() == s {
			return st
		}
	}
	return scheduleWaiting
}

// uiState is what would be written now.
func (m *Model) uiState() uiState {
	st := uiState{
		AppFilter:   m.appFilter.raw,
		Tab:         m.lastTab.String(),
		AutoRefresh: m.autoRefresh,
	}
	for _, s := range m.schedules {
		st.Schedules = append(st.Schedules, savedSchedule{
			ID: s.id, App: s.app, AppName: s.appName,
			Prune: s.opts.Prune, DryRun: s.opts.DryRun, Resources: s.opts.Resources,
			At: s.at, Window: s.window, Revision: s.revision, TargetRev: s.targetRev,
			AutoSync: s.autoSync, State: s.state.String(), Reason: s.reason,
			RanAt: s.ranAt, StartedAt: s.startedAt,
		})
	}
	return st
}

// restoreUI applies a loaded state. It returns how many schedules are pending.
func (m *Model) restoreUI(st uiState) int {
	m.appFilter = parseAppFilter(st.AppFilter)
	m.lastTab = parseTab(st.Tab)
	m.autoRefresh = st.AutoRefresh
	pending := 0
	for _, ss := range st.Schedules {
		s := scheduled{
			id: ss.ID, app: ss.App, appName: ss.AppName, context: ss.App.Context,
			opts: argocd.SyncOptions{Prune: ss.Prune, DryRun: ss.DryRun,
				AppNamespace: ss.App.AppNamespace},
			at: ss.At, window: ss.Window, revision: ss.Revision, targetRev: ss.TargetRev,
			autoSync: ss.AutoSync, state: parseScheduleState(ss.State), reason: ss.Reason,
			ranAt: ss.RanAt, startedAt: ss.StartedAt,
		}
		for _, r := range ss.Resources {
			r.AppNamespace, r.Context = ss.App.AppNamespace, ss.App.Context
			s.opts.Resources = append(s.opts.Resources, r)
		}
		// A sync that was in flight when argx exited has an outcome argx never
		// saw. Running it again would sync twice; calling it done would be a
		// guess. Say so instead.
		if s.state == scheduleRunning || s.state == scheduleSyncing {
			s.state = scheduleFailed
			s.reason = "argx exited while this sync was in flight — check Argo CD for its result"
			if s.ranAt.IsZero() {
				s.ranAt = time.Now()
			}
		}
		if !s.state.finished() {
			pending++
		}
		m.schedules = append(m.schedules, s)
		m.scheduleID = max(m.scheduleID, s.id)
	}
	m.sortSchedules()
	return pending
}

// persistUI queues a debounced write when the state changed since the last one.
// The comparison is on the encoded bytes, so a no-op Update (most of them)
// costs one small marshal and no write.
func (m *Model) persistUI() tea.Cmd {
	data, err := json.Marshal(m.uiState())
	if err != nil || bytes.Equal(data, m.savedUI) {
		return nil
	}
	m.savedUI = data
	var cmd tea.Cmd
	m.saver, cmd = m.saver.Save(m.uiState())
	return cmd
}

// quitCmd writes the latest state and then quits (see the prefs.Saved arm).
//
// savedUI is brought up to date too, or the persistUI that Update runs after
// every handler would see the state as unsaved and queue a debounced write
// behind the final one.
func (m *Model) quitCmd() tea.Cmd {
	st := m.uiState()
	if data, err := json.Marshal(st); err == nil {
		m.savedUI = data
	}
	var cmd tea.Cmd
	m.saver, cmd = m.saver.FlushWith(st)
	return cmd
}

// handlePrefsMsg handles the saver's messages, reporting whether msg was one.
func (m *Model) handlePrefsMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case prefs.SaveTick:
		var cmd tea.Cmd
		m.saver, cmd = m.saver.Update(msg)
		return cmd, true
	case prefs.Saved:
		if msg.Final {
			// The state is on disk (or failed to be); either way, quit — a
			// write error must not trap the reader in the program.
			return tea.Quit, true
		}
		if msg.Err != nil {
			m.setToastErr(fmt.Sprintf("could not save UI state: %v", msg.Err))
		}
		return nil, true
	}
	return nil, false
}
