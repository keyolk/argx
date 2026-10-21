package tui

// Key handlers for the application list, the resource tree, the pager views,
// and the modal overlays. The tab-level dispatch lives in update.go.

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleAppsKey(action string) (tea.Model, tea.Cmd) {
	// The mark vocabulary is the same on both lists, so it is handled in one
	// place; anything it does not claim falls through to the keys below.
	if cmd, handled := m.handleMarkKey(action, m.appScope(), &m.appCur); handled {
		return m, cmd
	}
	switch action {
	case aNavDown:
		m.moveApp(1)
	case aNavUp:
		m.moveApp(-1)
	case aNavPgDown:
		m.moveApp(m.bodyHeight() / 2)
	case aNavPgUp:
		m.moveApp(-m.bodyHeight() / 2)
	case aNavTop:
		m.appCur, m.appTop = 0, 0
	case aNavBottom:
		m.appCur = len(m.appRows) - 1
		m.clampScroll()

	case aAppsOpen:
		if a := m.currentApp(); a != nil {
			m.push(screenApp)
			m.tab = tabResources
			m.treeCur, m.treeTop, m.graphTop, m.treeFilt = 0, 0, 0, resourceFilter{}
			m.histCur, m.histTop, m.detailCur = 0, 0, 0
			m.tree, m.treeRows = nil, nil
			m.treeMarks = map[string]bool{}
			m.windows, m.projectWindows = nil, nil
			m.windowCur, m.windowTop = 0, 0
			// The windows load alongside the tree so DETAILS and the status
			// line can say whether a sync is blocked without the reader going
			// looking. It is one small request, and finding out by pressing `s`
			// and being rejected is worse.
			cmd := tea.Batch(m.loadTreeCmd(*a), m.loadWindowsCmd(*a))
			// Open on the tab last used; the tree loads either way, since the
			// status line and the resource count need it.
			if m.lastTab != tabResources {
				m.selectTab(m.lastTab)
			}
			return m, cmd
		}
	case aAppsDiff:
		if a := m.currentApp(); a != nil {
			m.push(screenDiff)
			m.pager, m.pagerTitle = nil, "diff · "+a.Name()
			return m, m.loadAppDiffCmd(*a)
		}
	case aAppsEvents:
		if a := m.currentApp(); a != nil {
			m.push(screenEvents)
			m.pager, m.pagerTitle = nil, "events · "+a.Name()
			return m, m.loadEventsCmd(*a)
		}
	case aAppsBrowser:
		return m, m.openMarkedApps()
	case aAppsRefresh:
		targets := m.markedApps()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading, m.loadWhat = true, "refresh"
		return m, m.refreshCmd(targets, false)
	case aAppsHardRefresh:
		targets := m.markedApps()
		if len(targets) == 0 {
			return m, nil
		}
		m.loading, m.loadWhat = true, "hard refresh"
		return m, m.refreshCmd(targets, true)
	case aAppsSync:
		return m.openSyncModal()
	case aAppsReload:
		return m, m.loadAppsCmd()
	case aFleetErrors:
		// The status line names the unreachable servers; this shows why.
		if len(m.fleetErrs) > 0 {
			m.showError(fleetErrorText(m.fleetErrs))
		}
	case aAppsFollow:
		// Follow. This was A until the mark vocabulary claimed that letter for
		// "clear the visible marks", at which point the mark handler — which
		// runs first, because both lists share it — swallowed the key and
		// auto-refresh became unreachable while the footer and the help still
		// named it. One letter, two owners, is a key that does whichever thing
		// the dispatch order happens to pick.
		m.autoRefresh = !m.autoRefresh
		// Every toggle retires the running chain, so turning it back on starts
		// exactly one.
		m.tickGen++
		if m.autoRefresh {
			m.setToast("auto-refresh on (15s)")
			return m, tickCmd(m.tickGen)
		}
		m.setToast("auto-refresh off")
	}
	return m, nil
}

func (m *Model) handleTreeKey(action string) (tea.Model, tea.Cmd) {
	if cmd, handled := m.handleMarkKey(action, m.treeScope(), &m.treeCur); handled {
		return m, cmd
	}
	switch action {
	case aNavDown:
		m.moveTree(1)
	case aNavUp:
		m.moveTree(-1)
	case aNavPgDown:
		m.moveTree(m.bodyHeight() / 2)
	case aNavPgUp:
		m.moveTree(-m.bodyHeight() / 2)
	case aNavTop:
		m.treeCur, m.treeTop, m.graphTop = 0, 0, 0
	case aNavBottom:
		m.treeCur = len(m.treeRows) - 1
		m.clampScroll()

	case aTreeGraph:
		// Rendering only: the cursor keeps walking treeRows in the same DFS
		// order either way, so toggling mid-browse does not lose the reader's
		// place the way switching screens would.
		m.treeGraph = !m.treeGraph
		if m.treeGraph {
			if !m.treeFilt.empty() || m.markedOnly {
				m.setToast("graph view — clear the filter to see it (list view while filtered)")
			} else {
				m.setToast("graph view")
			}
		} else {
			m.setToast("list view")
		}
		m.clampScroll()

	case aTreeManifest:
		// `l` would be the third key here, and on this list it is already
		// logs — so `right` carries the "drill in" half of the pair on its own
		// rather than the tree being the one screen where → does nothing.
		if n := m.currentNode(); n != nil && m.app != nil {
			m.push(screenManifest)
			m.pager, m.pagerTitle = nil, "manifest · "+n.Name
			return m, m.loadManifestCmd(*m.app, *n)
		}
	case aTreeDiff:
		// The marked resources, or the one under the cursor.
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
	case aTreeAppDiff:
		// The whole application, regardless of what is marked. "What is
		// different about this application" is the question you arrive at the
		// resource tree with, and it had no key: d narrowed to a selection, and
		// leaving the view to ask from the list lost your place in it.
		if m.app == nil {
			return m, nil
		}
		m.push(screenDiff)
		m.pager, m.pagerTitle = nil, "diff · "+m.app.Name()
		return m, m.loadAppDiffCmd(*m.app)
	case aTreeLogs:
		if n := m.currentNode(); n != nil {
			return m, m.openContainerPicker(*n, containerForLogs)
		}
	case aTreeShell:
		// A shell in the container. It goes through Argo CD rather than
		// kubectl, so the session inherits its RBAC and lands in its audit log.
		if n := m.currentNode(); n != nil {
			return m, m.openContainerPicker(*n, containerForExec)
		}
	case aTreeSync:
		return m.openTreeSyncModal()
	}
	return m, nil
}

func (m *Model) handlePagerKey(action string) (tea.Model, tea.Cmd) {
	// Stepping between matches rather than through the context around them is
	// what makes a search over a long manifest usable.
	switch action {
	case aSplitGrow, aSplitShrink, aSplitReset:
		return m.handleSplitKey(action)
	case aPagerNext:
		return m, m.jumpToHit(1)
	case aPagerPrev:
		return m, m.jumpToHit(-1)
	case aDiffSxS:
		// Side by side. Only in the diff view: a manifest or a log has no two
		// sides to lay out, and a key that silently does nothing on three
		// screens out of four is a key people stop trusting.
		if m.screen != screenDiff {
			return m, nil
		}
		m.sxs = !m.sxs
		m.pagerTop = 0
		switch {
		case !m.sxs:
			m.setToast("unified diff")
		case m.sxsAvailable():
			m.setToast("side by side")
		default:
			// Saying why beats appearing to ignore the key.
			m.setToast(fmt.Sprintf(
				"side by side needs %d columns; this terminal has %d — showing unified",
				sxsMinWidth, m.width))
		}
		return m, nil
	case aDiffTool:
		// The external tool. Only where there is something to hand it: `D` on a
		// log view would launch a diff of nothing.
		if m.screen != screenDiff {
			return m, nil
		}
		return m, m.diffToolCmd()
	case aDiffPairing:
		// Pairing off. A pairing is a *claim* — that these two differently-named
		// objects are one resource — inferred from a name pattern, so the
		// reader has to be able to see the two documents it was made from. Only
		// in the diff view: it is the only place the claim is made.
		if m.screen != screenDiff {
			return m, nil
		}
		m.smartHash = !m.smartHash
		m.pagerTop = 0
		if m.smartHash {
			m.setToast("pairing hash-suffixed resources")
		} else {
			m.setToast("showing hash-suffixed resources as created and pruned")
		}
		m.rerenderDiff()
		return m, nil
	case aPagerNoise:
		// The bookkeeping fields, back on. They are hidden by default because
		// they bury everything else, not because they never matter.
		m.showNoise = !m.showNoise
		m.pagerTop = 0
		if m.showNoise {
			m.setToast("showing managedFields and other bookkeeping")
		} else {
			m.setToast("hiding managedFields and other bookkeeping")
		}
		return m, nil
	}

	maxTop := len(m.pagerLines()) - m.bodyHeight()
	if maxTop < 0 {
		maxTop = 0
	}
	switch action {
	case aNavDown:
		m.pagerTop++
	case aNavUp:
		m.pagerTop--
	case aNavPgDown:
		m.pagerTop += m.bodyHeight() / 2
	case aNavPgUp:
		m.pagerTop -= m.bodyHeight() / 2
	case aNavTop:
		m.pagerTop = 0
	case aNavBottom:
		m.pagerTop = maxTop
	case aPagerLeave:
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

// jumpToHit moves the viewport to the next or previous match.
//
// The match is placed a couple of lines down rather than at the very top, so
// the context above it — which is half the reason it is shown — stays visible.
func (m *Model) jumpToHit(d int) tea.Cmd {
	rows := m.pagerHitRows()
	if len(rows) == 0 {
		return nil
	}

	// Which match the viewport is currently showing.
	cur := -1
	for i, r := range rows {
		if r >= m.pagerTop {
			cur = i
			break
		}
	}
	switch {
	case cur < 0:
		cur = len(rows) - 1
	case d > 0 && rows[cur] > m.pagerTop:
		// The next match is already below the top of the screen, so stepping
		// forward means going to it rather than past it.
		d = 0
	}

	next := cur + d
	if next < 0 {
		next = len(rows) - 1
	}
	if next >= len(rows) {
		next = 0
	}

	m.pagerTop = rows[next] - 2
	if m.pagerTop < 0 {
		m.pagerTop = 0
	}
	if max := len(m.pagerLines()) - m.bodyHeight(); m.pagerTop > max && max > 0 {
		m.pagerTop = max
	}
	return nil
}
