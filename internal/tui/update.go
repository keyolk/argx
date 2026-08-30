package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Update dispatches by message type. No I/O happens here — every side effect
// returns a tea.Cmd.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tooSmall = msg.Width < minWidth || msg.Height < minHeight
		m.clampScroll()
		return m, nil

	case appsMsg:
		m.loading = false
		if msg.err != nil {
			m.showError(msg.err)
			return m, nil
		}
		m.apps = msg.apps
		m.fleetErrs = msg.errs
		// Completions are rebuilt from what actually arrived, so the prompt
		// never offers a label key nothing carries.
		m.completions = buildCompletions(msg.apps)
		m.pruneAppMarks()
		m.applyAppFilter()
		// A server that failed is reported, but not as a modal that has to be
		// dismissed on every refresh: the status line carries it, and the
		// details are one keypress away. A modal here would make auto-refresh
		// unusable against a fleet with one flaky server.
		if len(msg.errs) > 0 && len(msg.apps) == 0 {
			// Nothing answered — that is worth interrupting for.
			m.showError(fleetErrorText(msg.errs))
		}
		return m, nil

	case treeMsg:
		if msg.id != m.treeID {
			return m, nil // stale: the user moved on before this landed
		}
		m.loading = false
		if msg.err != nil {
			m.showError(msg.err)
			return m, nil
		}
		m.app = msg.app
		m.tree = msg.rows
		m.pruneTreeMarks()
		m.applyTreeFilter()
		return m, nil

	case pagerMsg:
		if msg.id != m.reqID {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.showError(msg.err)
			return m, nil
		}
		m.pager = msg.lines
		m.pagerTitle = msg.title
		m.pagerTop = 0
		return m, nil

	case actionMsg:
		m.loading = false
		if msg.err != nil {
			m.showError(fmt.Errorf("%s\n\n%w", msg.text, msg.err))
			return m, nil
		}
		m.setToast(msg.text)
		// A sync or refresh changes what the list shows, so reload it rather
		// than leaving stale status on screen next to a "synced" toast.
		return m, m.loadAppsCmd()

	case specMsg:
		m.loading = false
		if msg.app != nil {
			// Update from the server's response, not from what argx asked for:
			// the server may normalize or reject part of a patch, and showing
			// the request back would report a change that did not happen.
			m.app = msg.app
			m.pruneTreeMarks()
		}
		if msg.err != nil {
			m.showError(msg.err)
			return m, nil
		}
		m.setToast(msg.text)
		// The spec changed, so the list's copy of this app is stale.
		return m, m.loadAppsCmd()

	case refsMsg:
		m.revPicker.loading = false
		if msg.err != nil {
			m.revPicker.err = msg.err.Error()
			return m, nil
		}
		m.revPicker.items = msg.items
		m.applyRevFilter()
		return m, nil

	case appSetsMsg:
		m.loading = false
		m.appsets = msg.sets
		m.appsetsLoaded = true
		m.fleetErrs = msg.errs
		m.applySetFilter()
		return m, nil

	case windowsMsg:
		if msg.id != m.windowID {
			return m, nil // stale: the user moved on before this landed
		}
		m.loading = false
		if msg.err != nil {
			m.showError(msg.err)
			return m, nil
		}
		m.windows, m.projectWindows = msg.windows, msg.project
		m.windowCur, m.windowTop = 0, 0
		return m, nil

	case containersMsg:
		p := &m.picker
		p.loading = false
		if msg.err != nil {
			p.err = msg.err.Error()
			return m, nil
		}
		p.containers = msg.containers
		switch len(p.containers) {
		case 0:
			p.err = "this pod reports no containers"
		case 1:
			// One container is not a choice, so it is not offered as one: the
			// modal closes and the action runs. This is why the containers are
			// fetched before the modal decides whether to stay open.
			p.cur = 0
			return m.chooseContainer()
		}
		return m, nil

	case execMsg:
		if msg.err != nil {
			m.showError(msg.err)
			return m, nil
		}
		// The tree may have changed while the shell was open — a restart, a new
		// pod — so it is reloaded rather than left as it was.
		if m.app != nil {
			return m, m.loadTreeCmd(*m.app)
		}
		return m, nil

	case toastMsg:
		m.setToast(msg.text)
		return m, nil

	case errMsg:
		m.loading = false
		m.showError(msg.err)
		return m, nil

	case tickMsg:
		if !m.autoRefresh {
			return m, nil // no redraw, no work: argx idles at 0 fps
		}
		return m, tea.Batch(m.loadAppsCmd(), tickCmd())

	case scheduledMsg:
		return m, m.addSchedules(msg)

	case scheduleTickMsg:
		// The ticker stops as soon as nothing is pending, so a session that has
		// run all its schedules goes back to zero frames.
		cmd := m.runDueSchedules(time.Time(msg))
		if m.pendingSchedules() == 0 {
			return m, cmd
		}
		return m, tea.Batch(cmd, scheduleTickCmd())

	case scheduleRunMsg:
		if err := m.applyScheduleResult(msg); err != nil {
			// The row was cleared while its sync was in flight. The sync still
			// happened, so saying nothing would be the wrong kind of quiet.
			m.setToast("a cleared scheduled sync finished: " + msg.state.String())
			return m, nil
		}
		m.clampScheduleCursor()
		if msg.state == scheduleFailed {
			m.setToast("scheduled sync failed: " + msg.reason)
		}
		return m, nil

	case tea.KeyMsg:
		// ctrl+c is the terminal's interrupt and quits from anywhere: inside a
		// modal, mid-search, during a confirmation. Routing it through the
		// normal key path would let an overlay or the filter prompt swallow it,
		// and a program you cannot interrupt is one people kill from another
		// window.
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Overlays capture keys first: a modal that can be dismissed by keys that
	// also act on the screen behind it is how people sync the wrong thing.
	if m.overlay != overlayNone {
		return m.handleOverlayKey(msg)
	}
	if m.filtering {
		// Tab keys are not filter text. Without this a `]` typed while the
		// filter prompt is open lands in the query instead of switching tabs,
		// and the reader is stuck on a tab with no way out but Esc.
		switch msg.String() {
		case "]", "[", "tab", "shift+tab":
			if m.screen == screenApp {
				m.filtering = false
				d := 1
				if msg.String() == "[" || msg.String() == "shift+tab" {
					d = -1
				}
				return m, m.cycleTab(d)
			}
		}
		return m.handleFilterKey(msg)
	}

	switch msg.String() {
	case "q":
		// Quit outright, from wherever the reader is. q meaning "back" on some
		// screens and "quit" on one made the same key do two things depending
		// on where you were, which is the kind of thing you find out by
		// pressing it. Esc is the key that unwinds a screen.
		//
		// Reached only when no overlay is open and the filter prompt is closed,
		// since q is an ordinary letter and a search that cannot contain it
		// would be broken.
		//
		// The one thing that stops it is pending scheduled syncs: they exist
		// only in this process, so quitting silently cancels work the reader
		// asked for and would have no way of noticing. Ctrl+C still quits
		// immediately — an interrupt that asks a question is not an interrupt.
		if n := m.pendingSchedules(); n > 0 {
			m.armQuitConfirm(n)
			return m, nil
		}
		return m, tea.Quit
	case "S":
		// The two lists are peers, not a stack: S toggles between them from
		// either side, so getting back is the same key that got you there.
		switch m.screen {
		case screenApps:
			m.screen = screenAppSets
			if !m.appsetsLoaded {
				return m, m.loadAppSetsCmd()
			}
			return m, nil
		case screenAppSets:
			m.screen = screenApps
			return m, nil
		}
	case "W":
		// Available from every screen: a schedule belongs to the session, not
		// to whatever the reader happens to be looking at, and a key that only
		// works in one place is one people cannot find when they need it.
		if m.screen == screenSchedule {
			m.pop()
			return m, nil
		}
		m.push(screenSchedule)
		m.clampScheduleCursor()
		return m, nil
	case "?":
		if m.screen == screenHelp {
			m.pop()
			m.pagerTop = 0
			return m, nil
		}
		m.push(screenHelp)
		// Help borrows the pager's scroll state, so start it at the top rather
		// than wherever the diff underneath was scrolled to.
		m.pagerTop = 0
		return m, nil
	case "esc":
		m.pop()
		return m, nil
	case "/":
		m.filtering = true
		// Open at the end of whatever query is already there, so reopening a
		// filter continues it rather than overwriting from the front.
		m.filterCur = len([]rune(*m.filterTarget()))
		return m, nil
	}

	switch m.screen {
	case screenApps:
		return m.handleAppsKey(msg)
	case screenAppSets:
		return m.handleAppSetsKey(msg)
	case screenApp:
		return m.handleAppKey(msg)
	case screenWindows:
		return m.handleWindowsKey(msg)
	case screenSchedule:
		return m.handleScheduleKey(msg)
	case screenDiff, screenManifest, screenLogs, screenEvents, screenHelp:
		return m.handlePagerKey(msg)
	}
	return m, nil
}

// handleAppKey handles the keys shared by all three tabs, then delegates.
func (m *Model) handleAppKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "]", "tab":
		return m, m.cycleTab(1)
	case "[", "shift+tab":
		return m, m.cycleTab(-1)
	case "1":
		return m, m.selectTab(tabResources)
	case "2":
		return m, m.selectTab(tabHistory)
	case "3":
		return m, m.selectTab(tabDetails)
	case "o":
		// Every tab opens the same application in the browser: there is no
		// per-tab URL, and a key that works on two tabs out of three is worse
		// than one that always works.
		if m.app != nil {
			return m, m.openBrowserCmd([]string{m.appURL(m.app)})
		}
		return m, nil
	case "r", "ctrl+r":
		if m.app != nil {
			return m, m.loadTreeCmd(*m.app)
		}
		return m, nil
	case "w":
		// Available from every tab: whether a sync is allowed to run is a
		// property of the application, not of the lens you happen to be using.
		if m.app != nil {
			m.push(screenWindows)
			return m, m.loadWindowsCmd(*m.app)
		}
		return m, nil
	case "h", "left":
		m.pop()
		return m, nil
	}

	switch m.tab {
	case tabResources:
		return m.handleTreeKey(msg)
	case tabHistory:
		return m.handleHistoryKey(msg)
	case tabDetails:
		return m.handleDetailsKey(msg)
	}
	return m, nil
}

// cycleTab moves to the next or previous tab.
func (m *Model) cycleTab(d int) tea.Cmd {
	i := int(m.tab) + d
	if i < 0 {
		i = len(allTabs) - 1
	}
	if i >= len(allTabs) {
		i = 0
	}
	return m.selectTab(allTabs[i])
}

// selectTab switches tabs, loading the tab's data if it is not there yet.
//
// Each tab keeps its own cursor, so returning to a tab lands where it was left
// rather than at the top.
func (m *Model) selectTab(t tab) tea.Cmd {
	if m.tab == t {
		return nil
	}
	m.tab = t
	// A filter typed on one tab does not carry to another: the queries mean
	// different things, and a stale filter that hides every row reads as an
	// empty tab.
	m.filtering = false
	m.filterCur = 0
	if t == tabDetails {
		// Section headings are not selectable, and row 0 is one, so nudge the
		// cursor onto the first real row rather than opening on a heading.
		m.moveDetail(1)
		m.moveDetail(-1)
	}
	if t == tabResources && len(m.tree) == 0 && m.app != nil && !m.loading {
		return m.loadTreeCmd(*m.app)
	}
	return nil
}

func (m *Model) handleHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.histRows()
	switch msg.String() {
	case "j", "down":
		m.histCur++
	case "k", "up":
		m.histCur--
	case "ctrl+d", "pgdown":
		m.histCur += m.bodyHeight() / 2
	case "ctrl+u", "pgup":
		m.histCur -= m.bodyHeight() / 2
	case "g", "home":
		m.histCur, m.histTop = 0, 0
	case "G", "end":
		m.histCur = len(rows) - 1
	case "enter", "b":
		return m, m.armRollback()
	case "d":
		// Diff a past deployment against what is live by looking at the app
		// diff; a per-revision diff would need the manifests for that revision,
		// which the managed-resources endpoint does not carry.
		if m.app != nil {
			m.push(screenDiff)
			m.pager, m.pagerTitle = nil, "diff · "+m.app.Name()
			return m, m.loadAppDiffCmd(*m.app)
		}
	}
	m.clampScroll()
	return m, nil
}

func (m *Model) handleDetailsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.moveDetail(1)
	case "k", "up":
		m.moveDetail(-1)
	case "g", "home":
		m.detailCur = 0
		m.moveDetail(1)
		m.moveDetail(-1)
	case "G", "end":
		m.detailCur = len(m.detailRows()) - 1
		m.moveDetail(-1)
		m.moveDetail(1)
	case "enter":
		return m.handleDetailEnter()
	case "e":
		if m.app != nil {
			m.push(screenEvents)
			m.pager, m.pagerTitle = nil, "events · "+m.app.Name()
			return m, m.loadEventsCmd(*m.app)
		}
	case "s":
		return m.openTreeSyncModal()
	}
	m.clampScroll()
	return m, nil
}
