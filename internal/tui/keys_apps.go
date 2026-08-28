package tui

// Key handlers for the application list, the resource tree, the pager views,
// and the modal overlays. The tab-level dispatch lives in update.go.

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleAppsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.moveApp(1)
	case "k", "up":
		m.moveApp(-1)
	case "ctrl+d", "pgdown":
		m.moveApp(m.bodyHeight() / 2)
	case "ctrl+u", "pgup":
		m.moveApp(-m.bodyHeight() / 2)
	case "g", "home":
		m.appCur, m.appTop = 0, 0
	case "G", "end":
		m.appCur = len(m.appRows) - 1
		m.clampScroll()

	case " ":
		// Space marks and advances, so marking a run of adjacent apps is one
		// key repeated rather than an alternation of space and j.
		if a := m.currentApp(); a != nil {
			k := a.Key()
			if m.appMarks[k] {
				delete(m.appMarks, k)
			} else {
				m.appMarks[k] = true
			}
			m.moveApp(1)
		}
	case "a":
		// Toggle-all over the *filtered* rows: with a filter active, "all"
		// meaning every app on the server would mark things the user cannot see.
		if m.allFilteredAppsMarked() {
			for _, i := range m.appRows {
				delete(m.appMarks, m.apps[i].Key())
			}
			m.setToast("cleared marks")
		} else {
			for _, i := range m.appRows {
				m.appMarks[m.apps[i].Key()] = true
			}
			// No toast: the status line already carries the mark count, and
			// saying it twice is how a status line stops being read.
		}
	case "ctrl+\\":
		// Not bound — listed here only as a reminder that terminal-reserved
		// keys stay unbound.
		return m, nil

	case "enter", "l", "right":
		if a := m.currentApp(); a != nil {
			m.push(screenApp)
			m.tab = tabResources
			m.treeCur, m.treeTop, m.treeFilt = 0, 0, resourceFilter{}
			m.histCur, m.histTop, m.detailCur = 0, 0, 0
			m.tree, m.treeRows = nil, nil
			m.treeMarks = map[string]bool{}
			m.windows, m.projectWindows = nil, nil
			m.windowCur, m.windowTop = 0, 0
			// The windows load alongside the tree so DETAILS and the status
			// line can say whether a sync is blocked without the reader going
			// looking. It is one small request, and finding out by pressing `s`
			// and being rejected is worse.
			return m, tea.Batch(m.loadTreeCmd(*a), m.loadWindowsCmd(*a))
		}
	case "d":
		if a := m.currentApp(); a != nil {
			m.push(screenDiff)
			m.pager, m.pagerTitle = nil, "diff · "+a.Name()
			return m, m.loadAppDiffCmd(*a)
		}
	case "e":
		if a := m.currentApp(); a != nil {
			m.push(screenEvents)
			m.pager, m.pagerTitle = nil, "events · "+a.Name()
			return m, m.loadEventsCmd(*a)
		}
	case "o":
		return m, m.openMarkedApps()
	case "r":
		targets := m.markedApps()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading, m.loadWhat = true, "refresh"
		return m, m.refreshCmd(targets, false)
	case "R":
		targets := m.markedApps()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading, m.loadWhat = true, "hard refresh"
		return m, m.refreshCmd(targets, true)
	case "s":
		return m.openSyncModal()
	case "ctrl+r":
		return m, m.loadAppsCmd()
	case "E":
		// The status line names the unreachable servers; this shows why.
		if len(m.fleetErrs) > 0 {
			m.showError(fleetErrorText(m.fleetErrs))
		}
	case "A":
		m.autoRefresh = !m.autoRefresh
		if m.autoRefresh {
			m.setToast("auto-refresh on (15s)")
			return m, tickCmd()
		}
		m.setToast("auto-refresh off")
	}
	return m, nil
}

func (m *Model) handleTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.moveTree(1)
	case "k", "up":
		m.moveTree(-1)
	case "ctrl+d", "pgdown":
		m.moveTree(m.bodyHeight() / 2)
	case "ctrl+u", "pgup":
		m.moveTree(-m.bodyHeight() / 2)
	case "g", "home":
		m.treeCur, m.treeTop = 0, 0
	case "G", "end":
		m.treeCur = len(m.treeRows) - 1
		m.clampScroll()

	case " ":
		if n := m.currentNode(); n != nil {
			if m.treeMarks[n.UID] {
				delete(m.treeMarks, n.UID)
			} else {
				m.treeMarks[n.UID] = true
			}
			m.moveTree(1)
		}
	case "a":
		if m.allFilteredNodesMarked() {
			for _, i := range m.treeRows {
				delete(m.treeMarks, m.tree[i].Node.UID)
			}
			m.setToast("cleared marks")
		} else {
			for _, i := range m.treeRows {
				m.treeMarks[m.tree[i].Node.UID] = true
			}
		}

	case "enter":
		if n := m.currentNode(); n != nil && m.app != nil {
			m.push(screenManifest)
			m.pager, m.pagerTitle = nil, "manifest · "+n.Name
			return m, m.loadManifestCmd(*m.app, *n)
		}
	case "d":
		if m.app == nil {
			return m, nil
		}
		nodes := m.markedNodes()
		if len(nodes) == 0 {
			return m, nil
		}
		m.push(screenDiff)
		m.pager, m.pagerTitle = nil, "diff"
		return m, m.loadResourceDiffCmd(*m.app, nodes)
	case "L", "l":
		if n := m.currentNode(); n != nil && m.app != nil {
			if !n.IsPod() {
				m.setToast("logs are only available for pods")
				return m, nil
			}
			m.push(screenLogs)
			m.pager, m.pagerTitle = nil, "logs · "+n.Name
			return m, m.loadLogsCmd(*m.app, *n)
		}
	case "s":
		return m.openTreeSyncModal()
	}
	return m, nil
}

func (m *Model) handlePagerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	maxTop := len(m.pagerLines()) - m.bodyHeight()
	if maxTop < 0 {
		maxTop = 0
	}
	switch msg.String() {
	case "j", "down":
		m.pagerTop++
	case "k", "up":
		m.pagerTop--
	case "ctrl+d", "pgdown":
		m.pagerTop += m.bodyHeight() / 2
	case "ctrl+u", "pgup":
		m.pagerTop -= m.bodyHeight() / 2
	case "g", "home":
		m.pagerTop = 0
	case "G", "end":
		m.pagerTop = maxTop
	case "h", "left":
		m.pop()
		return m, nil
	}
	if m.pagerTop > maxTop {
		m.pagerTop = maxTop
	}
	if m.pagerTop < 0 {
		m.pagerTop = 0
	}
	return m, nil
}
