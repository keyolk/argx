package tui

// The filter prompt: text editing, cursor movement, and completion.
//
// It is its own file because it is a text editor, not a list handler — the two
// share only the key dispatch that routes to them.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/argx/internal/argocd"
)

func (m *Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The resource filter is a parsed struct rather than a plain string, so it
	// is edited through its raw text and re-parsed on every keystroke.

	// The arrows split by axis, which is the only division that lets one
	// gesture do both jobs: left and right move the text cursor inside the
	// query, up and down move the cursor through the rows the query matched.
	// Typing to narrow and then arrowing to the row you wanted is the whole
	// point of an incremental filter, and fixing a typo mid-query should not
	// mean deleting back to it.
	switch msg.String() {
	case "up", "down", "pgup", "pgdown", "ctrl+n", "ctrl+p":
		return m.moveWhileFiltering(msg)
	case "tab", "shift+tab":
		// Tab completes the word under the cursor. On the application list
		// only: the resource filter's fields are a fixed handful documented in
		// the prompt hint, and there is nothing data-driven to offer.
		if m.screen == screenApps {
			return m.completeFilter()
		}
	}

	// Enter closes the prompt and keeps the query, so the list keeps its
	// filter and the full keymap comes back.
	target := m.filterTarget()
	r := []rune(*target)
	m.clampFilterCursor(len(r))

	switch msg.String() {
	case "esc":
		r = nil
		m.filtering = false
	case "enter":
		m.filtering = false

	case "left", "ctrl+b":
		if m.filterCur > 0 {
			m.filterCur--
		}
	case "right", "ctrl+f":
		if m.filterCur < len(r) {
			m.filterCur++
		}
	case "home", "ctrl+a":
		m.filterCur = 0
	case "end", "ctrl+e":
		m.filterCur = len(r)
	case "alt+left", "alt+b":
		m.filterCur = wordLeft(r, m.filterCur)
	case "alt+right", "alt+f":
		m.filterCur = wordRight(r, m.filterCur)

	case "backspace":
		if m.filterCur > 0 {
			r = append(r[:m.filterCur-1], r[m.filterCur:]...)
			m.filterCur--
		}
	case "delete", "ctrl+d":
		if m.filterCur < len(r) {
			r = append(r[:m.filterCur], r[m.filterCur+1:]...)
		}
	case "ctrl+u":
		// Clear the query but stay in the prompt: retyping from scratch is the
		// common case after a search that found nothing.
		r, m.filterCur = nil, 0
	case "ctrl+k":
		r = r[:m.filterCur]
	case "ctrl+w":
		start := wordLeft(r, m.filterCur)
		r = append(r[:start], r[m.filterCur:]...)
		m.filterCur = start

	default:
		if len(msg.Runes) > 0 {
			// Insert at the cursor, not at the end: the whole reason for a
			// movable cursor is to edit what is already typed.
			ins := msg.Runes
			out := make([]rune, 0, len(r)+len(ins))
			out = append(out, r[:m.filterCur]...)
			out = append(out, ins...)
			out = append(out, r[m.filterCur:]...)
			r = out
			m.filterCur += len(ins)
		}
	}

	*target = string(r)
	m.clampFilterCursor(len([]rune(*target)))
	// Any edit invalidates the offered list: it described the word as it was.
	m.completionHint = nil
	m.reapplyFilter()
	return m, nil
}

// completeFilter completes the filter word under the cursor.
//
// One press advances as far as is unambiguous and, when that adds nothing,
// shows the choices — the same two-stage behavior as a shell, which is what
// makes a single key useful both for finishing a word and for asking what the
// options are.
func (m *Model) completeFilter() (tea.Model, tea.Cmd) {
	if m.completions == nil {
		return m, nil
	}
	target := m.filterTarget()
	cands, start, end := m.completions.complete(*target, m.filterCur)
	if len(cands) == 0 {
		m.completionHint = nil
		return m, nil
	}

	r := []rune(*target)
	word := string(r[start:end])

	insert := cands[0]
	if len(cands) > 1 {
		insert = commonPrefix(cands)
		if insert == "" || strings.EqualFold(insert, word) {
			// Nothing more is unambiguous, so show what is available rather
			// than silently doing nothing.
			m.completionHint = cands
			return m, nil
		}
	}
	m.completionHint = nil

	out := make([]rune, 0, len(r)+len(insert))
	out = append(out, r[:start]...)
	out = append(out, []rune(insert)...)
	out = append(out, r[end:]...)
	*target = string(out)
	m.filterCur = start + len([]rune(insert))
	m.reapplyFilter()
	return m, nil
}

// clampFilterCursor keeps the text cursor inside the query.
//
// Called on every edit rather than trusted, because the query is also set from
// outside the prompt — entering a screen, clearing a filter — and a cursor left
// past the end would slice out of range.
func (m *Model) clampFilterCursor(n int) {
	if m.filterCur > n {
		m.filterCur = n
	}
	if m.filterCur < 0 {
		m.filterCur = 0
	}
}

// wordLeft is the start of the word before i.
func wordLeft(r []rune, i int) int {
	for i > 0 && r[i-1] == ' ' {
		i--
	}
	for i > 0 && r[i-1] != ' ' {
		i--
	}
	return i
}

// wordRight is the position after the word at i.
func wordRight(r []rune, i int) int {
	for i < len(r) && r[i] == ' ' {
		i++
	}
	for i < len(r) && r[i] != ' ' {
		i++
	}
	return i
}

// filterTarget is the query the current screen filters by.
//
// The resource filter is a parsed struct rather than a plain string, so it is
// edited through its raw text and re-parsed by reapplyFilter.
func (m *Model) filterTarget() *string {
	if m.screen == screenApp && m.tab == tabResources {
		return &m.treeFilt.raw
	}
	switch m.screen {
	case screenAppSets:
		return &m.appsetFilter
	case screenDiff, screenManifest, screenLogs, screenEvents:
		return &m.pagerFilt
	}
	return &m.appFilter.raw
}

// reapplyFilter recomputes the visible rows after the query changed.
func (m *Model) reapplyFilter() {
	switch {
	case m.screen == screenApp && m.tab == tabResources:
		m.treeFilt = parseResourceFilter(m.treeFilt.raw)
		m.applyTreeFilter()
	case m.screen == screenApps:
		m.appFilter = parseAppFilter(m.appFilter.raw)
		m.applyAppFilter()
	case m.screen == screenAppSets:
		m.applySetFilter()
	default:
		m.pagerTop = 0
	}
}

// moveWhileFiltering moves the cursor without leaving the filter prompt.
//
// Only the arrow keys and the control-key pairs are routed here: j and k are
// letters, and a filter that could not contain them would be useless.
func (m *Model) moveWhileFiltering(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	half := m.bodyHeight() / 2
	step := 0
	switch msg.String() {
	case "down", "ctrl+n":
		step = 1
	case "up", "ctrl+p":
		step = -1
	case "pgdown":
		step = half
	case "pgup":
		step = -half
	}

	switch {
	case m.screen == screenApps:
		m.moveApp(step)
	case m.screen == screenAppSets:
		m.appsetCur += step
		m.clampSetScroll()
	case m.screen == screenApp && m.tab == tabResources:
		m.moveTree(step)
	case m.screen == screenApp && m.tab == tabHistory:
		m.histCur += step
		m.clampScroll()
	case m.screen == screenApp && m.tab == tabDetails:
		m.moveDetail(step)
	default:
		m.pagerTop += step
		if max := len(m.pagerLines()) - m.bodyHeight(); m.pagerTop > max {
			m.pagerTop = max
		}
		if m.pagerTop < 0 {
			m.pagerTop = 0
		}
	}
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

	case overlayContainer:
		return m.handleContainerKey(msg)

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
