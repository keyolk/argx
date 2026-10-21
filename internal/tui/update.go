package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/prefs"
	"github.com/keyolk/tuikit/selection"
	"github.com/keyolk/tuikit/toast"
)

// Update dispatches by message type. No I/O happens here — every side effect
// returns a tea.Cmd.
//
// It also returns the expiry ticks of any toasts the message queued. Doing that
// here rather than at each setToast keeps the dozens of call sites unchanged,
// and expiring on a timer (rather than View comparing against time.Since) is
// what makes a toast disappear while argx is idle.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	// Persist after the handler, whatever it changed: one place, so no handler
	// can forget. Debounced, so holding j does not write per keypress.
	if save := m.persistUI(); save != nil {
		cmd = tea.Batch(cmd, save)
	}
	if len(m.toastCmds) > 0 {
		cmd = tea.Batch(append([]tea.Cmd{cmd}, m.toastCmds...)...)
		m.toastCmds = nil
	}
	return model, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case toast.Expired:
		m.toasts = toast.Update(m.toasts, msg)
		return m, nil

	case prefs.SaveTick, prefs.Saved:
		cmd, _ := m.handlePrefsMsg(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tooSmall = msg.Width < minWidth || msg.Height < minHeight
		m.clampScroll()
		return m, nil

	case appsMsg:
		if msg.id != m.appsID {
			// Superseded by a newer request. Dropping it also keeps the
			// loading indicator up until the request that matters answers.
			return m, nil
		}
		m.appsInFlight = false
		m.loading = false
		if msg.err != nil {
			m.showError(msg.err)
			return m, nil
		}
		// Capture the cursor's key while appRows still indexes the old slice.
		prev := m.currentAppKey()
		m.apps = msg.apps
		m.fleetErrs = msg.errs
		// Completions are rebuilt from what actually arrived, so the prompt
		// never offers a label key nothing carries.
		m.completions = buildCompletions(msg.apps)
		m.pruneAppMarks()
		m.applyAppFilterKeeping(prev)
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
		prev := m.currentNodeUID() // before treeRows stops matching m.tree
		m.app = msg.app
		m.tree = msg.rows
		m.pruneTreeMarks()
		m.applyTreeFilterKeeping(prev)
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
		m.pagerSides = msg.sides
		m.diffItems, m.diffWant = msg.items, msg.want
		pairs, _ := pairHashed(msg.items)
		m.diffHasPairs = len(pairs) > 0
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
		if !m.autoRefresh || msg.gen != m.tickGen {
			return m, nil // no redraw, no work: argx idles at 0 fps
		}
		if m.appsInFlight {
			// The previous load has not answered yet; keep the cadence but do
			// not pile a second request onto a slow fleet.
			return m, tickCmd(m.tickGen)
		}
		return m, tea.Batch(m.loadAppsCmd(), tickCmd(m.tickGen))

	case contextsMsg:
		m.loading = false
		m.ctxRows = msg.rows
		sortContextRows(m.ctxRows)
		m.ctxLoaded = true
		m.clampContextCursor()
		return m, nil

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
			text := "a cleared scheduled sync finished: " + msg.state.String()
			if msg.state == scheduleFailed {
				m.setToastErr(text)
			} else {
				m.setToast(text)
			}
			return m, nil
		}
		m.clampScheduleCursor()
		if msg.state == scheduleFailed {
			m.setToastErr("scheduled sync failed: " + msg.reason)
		}
		return m, nil

	case tea.MouseMsg:
		// The column split first: a gutter press is a resize, never a
		// selection or a click.
		if m.splitMouse(msg) {
			return m, nil
		}
		return m.selectionMouse(msg)
	case selection.AutoScrollMsg:
		return m.selectionMouse(msg)

	case selection.CopiedMsg:
		m.copied(msg)
		return m, nil

	case tea.KeyMsg:
		// A key between two clicks breaks the pair: click, move away and back
		// by keyboard, click again must not count as a double click.
		m.clicks = mouse.Clicks{}
		// A key is the "next action" that ends a selection, as in the mod
		// engine; cleared here, not in handleKey, for the same reason.
		m.textSel.Clear()
		// ctrl+c is the terminal's interrupt and quits from anywhere: inside a
		// modal, mid-search, during a confirmation. Routing it through the
		// normal key path would let an overlay or the filter prompt swallow it,
		// and a program you cannot interrupt is one people kill from another
		// window.
		if msg.Type == tea.KeyCtrlC {
			return m, m.quitCmd()
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A key the reader pressed after an error was on screen is the reader
	// having seen it. Cleared before the handler runs, so the handler's own
	// toast survives.
	m.toasts = toast.OnKey(m.toasts)
	// Under a Korean input source the shortcut keys arrive as jamo (`q` -> `ㅂ`)
	// and do nothing until the input source is switched back. Rewrite them to
	// the Latin key at the same physical position -- but not while the filter
	// prompt or the palette owns keys, where the jamo IS the query.
	//
	// The other overlays are all shortcuts (y/n, j/k, space), so they normalize.
	if !m.filtering && m.overlay != overlayPalette {
		msg = keys.NormalizeCJK(msg)
	}
	b, ok := m.keymap().ResolveRaw(m.keyStack(), msg, m.keyOpts())
	action := ""
	if ok {
		action = b.Action
	}

	// Overlays capture keys first: a modal that can be dismissed by keys that
	// also act on the screen behind it is how people sync the wrong thing.
	if m.overlay != overlayNone {
		return m.handleOverlayKey(action, msg)
	}
	if m.filtering {
		return m.handleFilterKey(action, msg)
	}
	if !ok {
		return m, nil
	}
	return m.runAction(action)
}

// runAction runs a screen-level action: from a key, or from the palette.
func (m *Model) runAction(action string) (tea.Model, tea.Cmd) {
	switch action {
	case aQuit:
		// Quit outright, from wherever the reader is. Esc is the key that
		// unwinds a screen; q meaning "back" on some screens and "quit" on one
		// made the same key do two things depending on where you were.
		//
		// The one thing that stops it is pending scheduled syncs: they run only
		// in this process, so quitting silently stops work the reader asked for.
		// Ctrl+C still quits immediately (see update) — an interrupt that asks a
		// question is not an interrupt.
		if n := m.pendingSchedules(); n > 0 {
			m.armQuitConfirm(n)
			return m, nil
		}
		return m, m.quitCmd()
	case aAppSetsToggle:
		// The two lists are peers, not a stack: S toggles between them from
		// either side, so getting back is the same key that got you there.
		switch m.screen {
		case screenApps:
			m.screen = screenAppSets
			if !m.appsetsLoaded {
				return m, m.loadAppSetsCmd()
			}
		case screenAppSets:
			m.screen = screenApps
		}
		return m, nil
	case aContexts:
		// Which credential is in play is a property of the session, not of a
		// screen, so it is reachable from all of them and toggles back the same
		// way. Loaded on entry: it is two requests per server and most sessions
		// never ask.
		if m.screen == screenContexts {
			m.pop()
			m.ctxDetail = false
			return m, nil
		}
		m.push(screenContexts)
		m.ctxDetail = false
		m.pagerTop = 0
		if !m.ctxLoaded {
			return m, m.loadContextsCmd()
		}
		return m, nil
	case aSchedules:
		// From every screen: a schedule belongs to the session, not to whatever
		// the reader happens to be looking at.
		if m.screen == screenSchedule {
			m.pop()
			return m, nil
		}
		m.push(screenSchedule)
		m.clampScheduleCursor()
		return m, nil
	case aHelp:
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
	case aPalette:
		m.openPalette()
		return m, nil
	case aVisualCancel:
		m.visualFrom = -1
		m.setToast("range cancelled")
		return m, nil
	case aBack:
		// A range in progress is the innermost level of all (normally caught by
		// the visual context; kept here for a palette-run back).
		if m.visualFrom >= 0 {
			m.visualFrom = -1
			m.setToast("range cancelled")
			return m, nil
		}
		// Esc unwinds one level, and a level is not always a screen: the
		// context view has a detail panel layered over its list.
		if m.screen == screenContexts && m.ctxDetail {
			m.ctxDetail = false
			m.pagerTop = 0
			return m, nil
		}
		// A standing filter is a level too, in front of the screen it narrows.
		// The marks are deliberately left alone: dropping a selection built
		// across several filters because the reader wanted to see the whole
		// list again is the surprise in the expensive direction.
		if m.clearStandingFilter() {
			return m, nil
		}
		m.pop()
		return m, nil
	case aFilter:
		m.filtering = true
		// Open at the end of whatever query is already there, so reopening a
		// filter continues it rather than overwriting from the front.
		m.filterCur = len([]rune(*m.filterTarget()))
		return m, nil
	}

	switch m.screen {
	case screenApps:
		return m.handleAppsKey(action)
	case screenAppSets:
		return m.handleAppSetsKey(action)
	case screenApp:
		return m.handleAppKey(action)
	case screenWindows:
		return m.handleWindowsKey(action)
	case screenSchedule:
		return m.handleScheduleKey(action)
	case screenContexts:
		return m.handleContextsKey(action)
	case screenDiff, screenManifest, screenLogs, screenEvents, screenHelp:
		return m.handlePagerKey(action)
	}
	return m, nil
}

// handleAppKey handles the keys shared by all three tabs, then delegates.
func (m *Model) handleAppKey(action string) (tea.Model, tea.Cmd) {
	switch action {
	case aTabNext:
		return m, m.cycleTab(1)
	case aTabPrev:
		return m, m.cycleTab(-1)
	case aTabResources:
		return m, m.selectTab(tabResources)
	case aTabHistory:
		return m, m.selectTab(tabHistory)
	case aTabDetails:
		return m, m.selectTab(tabDetails)
	case aAppBrowser:
		// Every tab opens the same application in the browser: there is no
		// per-tab URL, and a key that works on two tabs out of three is worse
		// than one that always works.
		if m.app != nil {
			return m, m.openBrowserCmd([]string{m.appURL(m.app)})
		}
		return m, nil
	case aAppReload:
		if m.app != nil {
			return m, m.loadTreeCmd(*m.app)
		}
		return m, nil
	case aAppWindows:
		// Available from every tab: whether a sync is allowed to run is a
		// property of the application, not of the lens you happen to be using.
		if m.app != nil {
			m.push(screenWindows)
			return m, m.loadWindowsCmd(*m.app)
		}
		return m, nil
	case aAppParent:
		// h/← no longer leaves the application view — only Esc does that now,
		// so a resource name that happens to contain "h" or a stray left-arrow
		// while browsing the tree can never bounce the reader back out to the
		// list. Inside the graph it still moves toward the root, since j/k
		// already walk the tree in DFS order, which reaches every node but
		// never "the parent of this one" directly — that is the one direction
		// the graph draws a visible line toward. At a root there is nowhere
		// left to go, so the key does nothing rather than falling through to
		// an exit nobody asked it to have.
		if m.tab == tabResources && m.treeGraphActive() {
			if r, ok := m.graphParentRow(); ok {
				m.treeCur = r
				m.clampScroll()
			}
		}
		return m, nil
	}

	switch m.tab {
	case tabResources:
		return m.handleTreeKey(action)
	case tabHistory:
		return m.handleHistoryKey(action)
	case tabDetails:
		return m.handleDetailsKey(action)
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
	m.lastTab = t
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

func (m *Model) handleHistoryKey(action string) (tea.Model, tea.Cmd) {
	rows := m.histRows()
	switch action {
	case aNavDown:
		m.histCur++
	case aNavUp:
		m.histCur--
	case aNavPgDown:
		m.histCur += m.bodyHeight() / 2
	case aNavPgUp:
		m.histCur -= m.bodyHeight() / 2
	case aNavTop:
		m.histCur, m.histTop = 0, 0
	case aNavBottom:
		m.histCur = len(rows) - 1
	case aHistRollback:
		// → drills in the way it does on every other list; what it reaches here
		// is the rollback prompt, which asks before it does anything.
		return m, m.armRollback()
	case aHistDiff:
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

func (m *Model) handleDetailsKey(action string) (tea.Model, tea.Cmd) {
	switch action {
	case aNavDown:
		m.moveDetail(1)
	case aNavUp:
		m.moveDetail(-1)
	case aNavTop:
		m.detailCur = 0
		m.moveDetail(1)
		m.moveDetail(-1)
	case aNavBottom:
		m.detailCur = len(m.detailRows()) - 1
		m.moveDetail(-1)
		m.moveDetail(1)
	case aDetailsEdit:
		return m.handleDetailEnter()
	case aDetailsEvents:
		if m.app != nil {
			m.push(screenEvents)
			m.pager, m.pagerTitle = nil, "events · "+m.app.Name()
			return m, m.loadEventsCmd(*m.app)
		}
	case aDetailsSync:
		return m.openTreeSyncModal()
	}
	m.clampScroll()
	return m, nil
}
