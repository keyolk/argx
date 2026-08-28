package tui

// Key handlers for the application list, the resource tree, the pager views,
// and the modal overlays. The tab-level dispatch lives in update.go.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
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
			return m, m.loadTreeCmd(*a)
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

func (m *Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The resource filter is a parsed struct rather than a plain string, so it
	// is edited through its raw text and re-parsed on every keystroke.
	if m.screen == screenApp && m.tab == tabResources {
		return m.handleTreeFilterKey(msg)
	}

	target := &m.appFilter
	switch m.screen {
	case screenDiff, screenManifest, screenLogs, screenEvents:
		target = &m.pagerFilt
	}

	switch msg.String() {
	case "esc":
		*target = ""
		m.filtering = false
	case "enter":
		m.filtering = false
	case "backspace":
		if *target != "" {
			r := []rune(*target)
			*target = string(r[:len(r)-1])
		}
	default:
		if len(msg.Runes) > 0 {
			*target += string(msg.Runes)
		}
	}

	switch m.screen {
	case screenApps:
		m.applyAppFilter()
	default:
		m.pagerTop = 0
	}
	return m, nil
}

// handleTreeFilterKey edits the RESOURCES tab's field-aware filter.
func (m *Model) handleTreeFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	raw := m.treeFilt.raw
	switch msg.String() {
	case "esc":
		raw = ""
		m.filtering = false
	case "enter":
		m.filtering = false
	case "backspace":
		if raw != "" {
			r := []rune(raw)
			raw = string(r[:len(r)-1])
		}
	default:
		if len(msg.Runes) > 0 {
			raw += string(msg.Runes)
		}
	}
	m.treeFilt = parseResourceFilter(raw)
	m.applyTreeFilter()
	return m, nil
}

func (m *Model) handleOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayError:
		// Any key dismisses an error; requiring a specific one just traps
		// people who are already annoyed.
		m.overlay = overlayNone
		m.errMsg = ""
		return m, nil

	case overlayConfirm:
		switch msg.String() {
		case "y", "Y", "enter":
			m.overlay = overlayNone
			if m.confirm.action != nil {
				cmd := m.confirm.action()
				m.confirm = confirmState{}
				m.loading = true
				return m, cmd
			}
		case "n", "N", "esc", "q":
			m.overlay = overlayNone
			m.confirm = confirmState{}
		}
		return m, nil

	case overlayRevPicker:
		return m.handleRevPickerKey(msg)

	case overlaySyncOpts:
		switch msg.String() {
		case "p":
			m.syncOpts.prune = !m.syncOpts.prune
		case "d":
			m.syncOpts.dryRun = !m.syncOpts.dryRun
		case "esc", "q", "n":
			m.overlay = overlayNone
		case "enter", "y":
			m.overlay = overlayNone
			return m, m.armSyncConfirm()
		}
		return m, nil
	}
	return m, nil
}

// openSyncModal starts the sync flow from the application list.
func (m *Model) openSyncModal() (tea.Model, tea.Cmd) {
	targets := m.markedApps()
	if len(targets) == 0 {
		return m, nil
	}
	m.syncOpts = syncOptState{targets: targets}
	m.overlay = overlaySyncOpts
	return m, nil
}

// openTreeSyncModal syncs only the marked resources of the focused app.
func (m *Model) openTreeSyncModal() (tea.Model, tea.Cmd) {
	if m.app == nil {
		return m, nil
	}
	nodes := m.markedNodes()
	if len(nodes) == 0 {
		return m, nil
	}
	m.syncOpts = syncOptState{targets: []argocd.Application{*m.app}}
	m.overlay = overlaySyncOpts
	return m, nil
}

// armSyncConfirm builds the final confirmation. Sync mutates a cluster, so it
// always passes through an explicit y/n even when the user marked one app.
func (m *Model) armSyncConfirm() tea.Cmd {
	opt := argocd.SyncOptions{Prune: m.syncOpts.prune, DryRun: m.syncOpts.dryRun}

	// A tree-scoped sync carries the marked resources; a list-scoped one syncs
	// whole applications.
	var scope string
	if m.screen == screenApp && m.tab == tabResources && m.app != nil {
		nodes := m.markedNodes()
		for _, n := range nodes {
			ref := n.ResourceRef
			opt.Resources = append(opt.Resources, ref)
		}
		scope = fmt.Sprintf("%d resource(s) of %s", len(nodes), m.app.Name())
	} else {
		scope = fmt.Sprintf("%d application(s)", len(m.syncOpts.targets))
		if n := len(argocd.Contexts(m.syncOpts.targets)); n > 1 {
			scope += fmt.Sprintf(" across %d servers", n)
		}
	}

	body := []string{scope, ""}
	body = append(body, m.targetLines(m.syncOpts.targets)...)

	var flags []string
	if opt.Prune {
		flags = append(flags, "PRUNE (deletes resources not in git)")
	}
	if opt.DryRun {
		flags = append(flags, "dry-run (no changes applied)")
	}
	if len(flags) > 0 {
		body = append(body, "", "options: "+strings.Join(flags, ", "))
	}

	targets := m.syncOpts.targets
	m.confirm = confirmState{
		title:  "Sync?",
		body:   body,
		action: func() tea.Cmd { return m.syncCmd(targets, opt) },
	}
	m.overlay = overlayConfirm
	return nil
}

// targetLines renders an action's targets, grouped by server when the set spans
// more than one.
//
// A flat list of ten application names hides that three of them are on a
// different Argo CD; grouping makes a cross-server action impossible to confirm
// by accident.
func (m *Model) targetLines(apps []argocd.Application) []string {
	const maxRows = 12

	if len(argocd.Contexts(apps)) <= 1 {
		var out []string
		for i, a := range apps {
			if i >= maxRows {
				out = append(out, fmt.Sprintf("  … and %d more", len(apps)-maxRows))
				break
			}
			out = append(out, "  "+a.Name()+"  →  "+a.Spec.Destination.Cluster())
		}
		return out
	}

	order, groups := m.fleet.ByContext(apps)
	out := []string{
		m.st.warn.Render(fmt.Sprintf("This spans %d Argo CD servers.", len(order))),
		"",
	}
	shown := 0
	for _, ctxName := range order {
		g := groups[ctxName]
		out = append(out, m.ctxStyle(ctxName).Render(ctxName)+
			m.st.dim.Render(fmt.Sprintf("  (%d)", len(g))))
		for _, a := range g {
			if shown >= maxRows {
				out = append(out, fmt.Sprintf("  … and %d more", len(apps)-shown))
				return out
			}
			out = append(out, "    "+a.Name()+"  →  "+a.Spec.Destination.Cluster())
			shown++
		}
	}
	return out
}

// openMarkedApps opens every marked application in the browser.
func (m *Model) openMarkedApps() tea.Cmd {
	targets := m.markedApps()
	if len(targets) == 0 {
		return nil
	}
	// A stray `a` before `o` would otherwise fire a hundred browser tabs at
	// once; make the user acknowledge anything past a handful.
	urls := make([]string, 0, len(targets))
	for i := range targets {
		if u := m.appURL(&targets[i]); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) > 5 {
		m.confirm = confirmState{
			title:  "Open in browser?",
			body:   []string{fmt.Sprintf("This opens %d browser tabs.", len(urls))},
			action: func() tea.Cmd { return m.openBrowserCmd(urls) },
		}
		m.overlay = overlayConfirm
		return nil
	}
	return m.openBrowserCmd(urls)
}
