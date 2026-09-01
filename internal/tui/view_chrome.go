package tui

// The status line, the footer hints, and the modal overlays — the frame around
// whatever body is being rendered.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderStatus is the one-line state readout above the footer: counts, marks,
// filter, and the transient toast.
func (m *Model) renderStatus() string {
	var parts []string

	switch m.screen {
	case screenApps:
		parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d/%d apps", len(m.appRows), len(m.apps))))
		if n := len(m.fleetErrs); n > 0 {
			// Named, not counted: "1 server failed" leaves the reader wondering
			// whether the application they came for is missing or simply on the
			// server that did not answer.
			names := make([]string, 0, n)
			for _, e := range m.fleetErrs {
				names = append(names, e.Context)
			}
			parts = append(parts,
				m.st.err.Render("unreachable: "+strings.Join(names, ", ")+" (E)"))
		}
		parts = append(parts, m.markParts(m.appScope())...)
		if n := m.pendingSchedules(); n > 0 {
			// Carried on the root screen because a scheduled sync is invisible
			// otherwise, and it disappears when argx exits — the reader needs a
			// standing reminder that something is waiting on this process.
			parts = append(parts, m.st.warn.Render(fmt.Sprintf("%d scheduled (W)", n)))
		}
		if !m.appFilter.empty() || m.filtering {
			parts = append(parts, m.renderFilter(m.appFilter.String()))
			if m.filtering {
				// The candidate list replaces the field hint when there is one:
				// the reader just asked what the options are, and showing both
				// buries the answer.
				if len(m.completionHint) > 0 {
					parts = append(parts, m.st.accent.Render(
						joinCandidates(m.completionHint, m.width/2)))
				} else {
					parts = append(parts, m.st.dim.Render(appFilterHint))
				}
			}
		}
	case screenAppSets:
		broken := 0
		for _, i := range m.appsetRows {
			if m.appsets[i].Degraded() {
				broken++
			}
		}
		parts = append(parts, m.st.dim.Render(
			fmt.Sprintf("%d/%d sets", len(m.appsetRows), len(m.appsets))))
		if broken > 0 {
			parts = append(parts, m.st.err.Render(fmt.Sprintf("%d with errors", broken)))
		}
		if n := len(m.fleetErrs); n > 0 {
			names := make([]string, 0, n)
			for _, e := range m.fleetErrs {
				names = append(names, e.Context)
			}
			parts = append(parts, m.st.err.Render("unreachable: "+strings.Join(names, ", ")+" (E)"))
		}
		if strings.TrimSpace(m.appsetFilter) != "" || m.filtering {
			parts = append(parts, m.renderFilter(m.appsetFilter))
			if m.filtering {
				parts = append(parts, m.st.dim.Render(appsetFilterHint))
			}
		}

	case screenApp:
		switch m.tab {
		case tabResources:
			parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d/%d resources", len(m.treeRows), len(m.tree))))
			parts = append(parts, m.markParts(m.treeScope())...)
		case tabHistory:
			parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d deployments", len(m.histRows()))))
		case tabDetails:
			// The marker is named by showing it, not by spelling it: the glyph
			// differs per icon set, and a hardcoded "*" would disagree with the
			// rows it describes.
			parts = append(parts, m.st.dim.Render(
				m.st.mark.Render(m.gl.editable)+m.st.dim.Render(" rows are editable")))
		}
		if m.app != nil {
			a := m.app
			parts = append(parts,
				m.st.syncStyle(a.Status.Sync.Status).Render(a.Status.Sync.Status),
				m.st.healthStyle(a.Status.Health.Status).Render(a.Status.Health.Status))
			// A blocked sync is worth carrying on every tab: pressing `s` and
			// getting a rejection is a worse way to find out.
			if m.windows != nil && !m.windows.CanSync {
				parts = append(parts, m.st.err.Render("sync window: BLOCKED"))
			}
			if on, prune, selfHeal := a.AutoSync(); on {
				s := "auto"
				if prune {
					s += "+prune"
				}
				if selfHeal {
					s += "+heal"
				}
				parts = append(parts, m.st.dim.Render(s))
			}
		}
		if m.tab == tabResources && (!m.treeFilt.empty() || m.filtering) {
			parts = append(parts, m.renderFilter(m.treeFilt.String()))
			if m.filtering {
				parts = append(parts, m.st.dim.Render(resourceFilterHint))
			}
		}
	case screenContexts:
		parts = append(parts, m.contextsSummary()...)

	case screenSchedule:
		waiting, syncing, done, declined, failed := 0, 0, 0, 0, 0
		for _, sc := range m.schedules {
			switch sc.state {
			case scheduleDone:
				done++
			case scheduleFailed:
				failed++
			case scheduleCancelled:
				declined++
			case scheduleRunning, scheduleSyncing:
				syncing++
			default:
				waiting++
			}
		}
		parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d waiting", waiting)))
		if syncing > 0 {
			parts = append(parts, m.st.info.Render(fmt.Sprintf("%d syncing", syncing)))
		}
		if done > 0 {
			parts = append(parts, m.st.success.Render(fmt.Sprintf("%d synced", done)))
		}
		if declined > 0 {
			// "declined" rather than "cancelled": most of these are argx
			// refusing to deploy something that changed, not the user changing
			// their mind, and the reason line says which.
			parts = append(parts, m.st.warn.Render(fmt.Sprintf("%d declined", declined)))
		}
		if failed > 0 {
			parts = append(parts, m.st.err.Render(fmt.Sprintf("%d failed", failed)))
		}

	case screenWindows:
		rows := m.windowRows()
		open := 0
		for _, r := range rows {
			if r.active {
				open++
			}
		}
		// The project's total is carried alongside, because "2 windows" reads
		// very differently when the project has two than when it has thirty.
		summary := fmt.Sprintf("%d window(s) apply", len(rows))
		if n := len(m.projectWindows); n > len(rows) {
			summary += fmt.Sprintf(" of %d on the project", n)
		}
		if open > 0 {
			summary += fmt.Sprintf(" · %d open", open)
		}
		parts = append(parts, m.st.dim.Render(summary))
		if m.windows != nil {
			if m.windows.CanSync {
				parts = append(parts, m.st.success.Render("syncing allowed now"))
			} else {
				parts = append(parts, m.st.err.Render("syncing BLOCKED now"))
			}
		}
		if m.app != nil {
			parts = append(parts, m.st.dim.Render(m.app.Name()))
		}

	default:
		res := m.searchPager()
		total := len(res.lines)
		if n := len(res.hitRows); n > 0 {
			// The match count is what says whether the search found anything;
			// a line count of a filtered view says almost nothing.
			parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d match(es)", n)))
		}
		parts = append(parts, m.st.dim.Render(fmt.Sprintf("line %d/%d", min(m.pagerTop+1, total), total)))
		if m.screen == screenDiff && m.sxs {
			// Whether the two columns are actually in effect: the layout falls
			// back on a narrow terminal, and a reader who pressed the key
			// should not have to work out from the shape of the text whether
			// it took.
			if m.sxsAvailable() {
				parts = append(parts, m.st.info.Render("side by side"))
			} else {
				parts = append(parts, m.st.warn.Render(fmt.Sprintf(
					"side by side needs %d columns (s)", sxsMinWidth)))
			}
		}
		if m.screen == screenDiff && !m.smartHash && m.hashPairsAvailable() {
			// Only when it would actually change what is on screen: a standing
			// note about a mode that makes no difference here is noise.
			parts = append(parts, m.st.warn.Render("hash pairing off (H)"))
		}
		if n := m.noiseHidden(); n > 0 {
			// An absent field should never be a mystery: the count says how
			// much is hidden, and M is how to see it.
			parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d lines hidden (M)", n)))
		}
		if m.pagerFilt != "" || m.filtering {
			parts = append(parts, m.renderFilter(m.pagerFilt))
		}
	}

	if m.loading {
		parts = append(parts, m.st.warn.Render("… "+m.loadWhat))
	}
	// The toast lives for a few seconds; it is checked here rather than cleared
	// on a timer so no tick is needed just to expire it.
	if m.toast != "" && time.Since(m.toastAt) < 5*time.Second {
		parts = append(parts, m.st.success.Render(m.toast))
	}

	// Each part carries its own style already; the separator is styled on its
	// own rather than wrapping the joined string, which would nest escape
	// sequences and lose the inner colors after the first reset.
	return truncate(strings.Join(parts, m.st.dim.Render(m.gl.sep)), m.width)
}

// markParts is the selection's readout: how many, how many the filter is
// hiding, and whether a range is being drawn.
//
// The hidden count is the one that has to be said out loud. Marks the filter
// does not show are still targets of the next sync, and a reader who cannot see
// them has no way to know the selection is wider than the screen.
func (m *Model) markParts(scope markScope) []string {
	var parts []string

	if n := len(scope.marks); n > 0 {
		label := fmt.Sprintf("%d marked", n)
		if h := scope.hiddenCount(); h > 0 {
			label += fmt.Sprintf(" (%d not shown)", h)
			parts = append(parts, m.st.warn.Render(label))
		} else {
			parts = append(parts, m.st.mark.Render(label))
		}
	}
	if m.markedOnly {
		// Otherwise a list narrowed to its own marks reads as a list that lost
		// most of its rows.
		parts = append(parts, m.st.info.Render("marked only (m)"))
	}
	if m.visualFrom >= 0 {
		from, to, _ := m.visualRange(m.listCursor())
		parts = append(parts, m.st.accent.Render(
			fmt.Sprintf("range: %d rows — v to take, esc to cancel", to-from+1)))
	}
	return parts
}

// joinCandidates renders a completion list, bounded so it cannot push the rest
// of the status line off screen.
func joinCandidates(cands []string, budget int) string {
	if budget < 20 {
		budget = 20
	}
	var b strings.Builder
	for i, c := range cands {
		next := c
		if i > 0 {
			next = "  " + c
		}
		if lipgloss.Width(b.String())+lipgloss.Width(next) > budget {
			return b.String() + fmt.Sprintf("  +%d more", len(cands)-i)
		}
		b.WriteString(next)
	}
	return b.String()
}

// renderFilter draws the query with the text cursor at its actual position.
//
// The cursor is a reverse-video cell over the character it sits on rather than
// a bar appended to the end: a bar that always trailed the query said nothing
// about where an edit would land, which is the whole point of being able to
// move it.
func (m *Model) renderFilter(q string) string {
	if !m.filtering {
		return m.st.filter.Render("/" + q)
	}

	r := []rune(q)
	i := m.filterCur
	if i > len(r) {
		i = len(r)
	}
	if i < 0 {
		i = 0
	}

	// At the end of the query the cursor covers a space, so it stays visible
	// rather than vanishing past the last character.
	under := " "
	after := ""
	if i < len(r) {
		under = string(r[i])
		after = string(r[i+1:])
	}
	return m.st.filter.Render("/"+string(r[:i])) +
		m.st.cursorCell.Render(under) +
		m.st.filter.Render(after)
}

// syncHint labels the sync key, saying so when a window is blocking.
//
// "s sync" on an application argx already knows cannot sync is an invitation to
// press it and have Argo CD record a failed operation. Naming the alternative
// costs four cells and is only shown when it applies.
func (m *Model) syncHint() string {
	if m.syncBlocked() {
		return "s sync (w waits)"
	}
	return "s sync"
}

// renderFooter is the always-visible hint line, per screen.
func (m *Model) renderFooter() string {
	var hints []string
	switch m.screen {
	case screenApps:
		// "space mark" alone taught marking one row and nothing else, so the
		// mark keys sat undiscovered behind a key nobody had reason to press.
		// They lead now, because every destructive action here takes its
		// targets from the selection.
		hints = []string{"space/a/A mark", "J/K extend", "v range", "enter open",
			"s sync", "d diff", "o browser", "W schedules", "S appsets",
			"C contexts", "/ filter", "? help", "q quit"}
	case screenApp:
		switch m.tab {
		case tabHistory:
			hints = []string{"enter rollback", "[ ] tabs", "d diff", "w windows", "o browser", "esc back", "q quit"}
		case tabDetails:
			hints = []string{"enter edit", "[ ] tabs", m.syncHint(), "w windows", "e events", "esc back", "q quit"}
		default:
			hints = []string{"space/a/A mark", "J/K extend", "v range",
				"enter manifest", "d diff", "D app diff", "l logs", "e shell",
				m.syncHint(), "w windows", "esc back", "q quit"}
		}
	case screenAppSets:
		hints = []string{"enter apps", "y spec", "o browser", "S applications", "/ filter", "? help", "q quit"}
	case screenWindows:
		// A reader on this screen is here because syncing is blocked, which is
		// the one moment "you can wait for it instead" is worth knowing.
		hints = []string{"j/k move", "s+w schedule a sync", "o project", "O app", "r reload", "esc back", "q quit"}
	case screenSchedule:
		hints = []string{"j/k move", "x cancel", "c clear finished", "o browser", "esc back", "q quit"}
	case screenContexts:
		if m.ctxDetail {
			hints = []string{"j/k scroll", "o browser", "esc back to the list", "q quit"}
		} else {
			hints = []string{"j/k move", "enter details", "o browser", "r reload", "esc back", "q quit"}
		}
	case screenHelp:
		hints = []string{"esc back", "q quit"}
	default:
		hints = []string{"j/k scroll", "/ search", "n/N match", "M noise", "esc back", "q quit"}
		if m.screen == screenDiff {
			// The diff view has two more, and they are the ones nobody guesses.
			hints = []string{"j/k scroll", "s side-by-side", "D diff tool",
				"/ search", "n/N match", "M noise", "H hash pairing",
				"esc back", "q quit"}
		}
	}

	// Pending syncs lead, on every screen. They exist only in this process and
	// are otherwise invisible from wherever the reader happens to be, so the way
	// back to them has to survive the narrowing below — which drops from the
	// right — rather than being the first thing to go.
	if n := m.pendingSchedules(); n > 0 && m.screen != screenSchedule {
		hints = append([]string{fmt.Sprintf("W %d scheduled", n)}, hints...)
	}
	// An open filter prompt replaces the hints entirely, for the same reason:
	// it is a mode, and almost every key named above is text while it is on.
	// The three that are not are the ones nobody would guess — the mark keys
	// have to be modified here, since space and J are characters — so this is
	// the only place they get written down at the moment they apply.
	if m.filtering {
		hints = []string{"↑↓ move", "enter keep", "esc clear"}
		if _, _, ok := m.markableList(); ok {
			// alt is what the whole vocabulary wears in here, so it is said once
			// rather than on every key: naming alt+a, alt+A and alt+i separately
			// costs three times the width to say the same thing.
			hints = []string{"↑↓ move", "→ mark", "← unmark", "shift+↑↓ extend",
				"enter keep the query", "esc clear"}
		}
	}
	// A range in progress replaces the hints entirely. It is a mode, and the
	// two keys that end it are the only ones worth naming while it is on.
	if m.visualFrom >= 0 {
		hints = []string{"move to extend", "v take the range", "esc cancel"}
	}

	// Drop hints from the right until the line fits, so a narrow terminal keeps
	// the most important ones rather than wrapping.
	//
	// Except the last ones. Every list ends in "q quit", and most end in
	// "? help" before it; a footer that drops either to make room for "d diff"
	// has cut the wrong thing. The exit is how you leave, and the help is the
	// only place the keys that did not fit are written down — dropping it
	// exactly when the footer is too small to list them is the worst moment.
	// "esc back" is an exit too, and on a screen the reader drilled into it is
	// the *only* one that does not lose their place — so it is kept for the
	// same reason, rather than being the first casualty of a new hint.
	keep := 0
	for keep < len(hints) {
		switch hints[len(hints)-1-keep] {
		case "q quit", "? help", "esc back":
			keep++
			continue
		}
		break
	}
	if keep == 0 {
		keep = 1
	}
	tail := strings.Join(hints[len(hints)-keep:], "  ")
	body := hints[:len(hints)-keep]
	for len(body) > 0 {
		s := strings.Join(append(append([]string{}, body...), tail), "  ")
		if lipgloss.Width(s) <= m.width {
			break
		}
		body = body[:len(body)-1]
	}
	hints = append(body, tail)
	return m.st.footer.Render(truncate(strings.Join(hints, "  "), m.width))
}

// confirmChoices draws the yes/no pair with the cursor on one of them.
//
// The selected side is marked with a caret as well as colored, because the
// whole point of the cursor is to say which way enter will go and a reader on a
// monochrome terminal needs that answer too.
func (m *Model) confirmChoices() string {
	no, yes := "  No", "  Yes"
	if m.confirm.yes {
		yes = m.st.warn.Render("› Yes")
		no = m.st.dim.Render(no)
	} else {
		no = m.st.selected.Render("› No")
		yes = m.st.dim.Render(yes)
	}
	return no + "     " + yes
}

// renderOverlay draws a modal centered over the frame.
func (m *Model) renderOverlay(frame string) string {
	var box string
	switch m.overlay {
	case overlayError:
		w := m.modalContentWidth(76)
		lines := []string{m.st.err.Render("error"), ""}
		lines = append(lines, strings.Split(wrapText(m.errMsg, w), "\n")...)
		lines = append(lines, "", m.st.dim.Render("any key to dismiss"))
		box = m.st.modalErr.Render(strings.Join(
			clampModalBody(lines, w, m.modalContentHeight()), "\n"))

	case overlayConfirm:
		w := m.modalContentWidth(76)
		lines := []string{m.st.warn.Render(m.confirm.title), ""}
		lines = append(lines, m.confirm.body...)
		lines = append(lines, "", m.confirmChoices(),
			m.st.dim.Render("h/l move · enter take it · y/n outright"))
		box = m.st.modal.Render(strings.Join(
			clampModalBody(lines, w, m.modalContentHeight()), "\n"))

	case overlayContainer:
		box = m.renderContainerPicker()

	case overlayRevPicker:
		box = m.renderRevPicker()

	case overlaySyncOpts:
		check := func(on bool) string {
			if on {
				return m.st.success.Render("[x]")
			}
			return m.st.dim.Render("[ ]")
		}
		// The cursor is a caret in the left margin rather than a highlight on
		// the row: the row already carries a checkbox whose own state is what
		// the color is saying, and two colored things a cell apart is how a
		// reader stops being able to tell which one they are looking at.
		row := func(i int, s string) string {
			if m.syncOpts.cur == i {
				return m.st.accent.Render("›") + " " + s
			}
			return "  " + s
		}
		scope := fmt.Sprintf("%d application(s)", len(m.syncOpts.targets))
		if m.screen == screenApp && m.tab == tabResources && m.app != nil {
			scope = fmt.Sprintf("%d resource(s) of %s", len(m.markedNodes()), m.app.Name())
		}
		lines := []string{
			m.st.accent.Render("Sync options"),
			m.st.dim.Render(scope),
			"",
			row(0, check(m.syncOpts.prune)+" "+m.st.info.Render("p")+" prune  "+
				m.st.dim.Render("(delete resources not in git)")),
			row(1, check(m.syncOpts.dryRun)+" "+m.st.info.Render("d")+" dry-run  "+
				m.st.dim.Render("(compute only, apply nothing)")),
			row(2, check(m.syncOpts.schedule)+" "+m.st.info.Render("w")+" wait for the sync window  "+
				m.st.dim.Render("(while argx runs)")),
			"",
			m.st.dim.Render("j/k move   ·   space toggle   ·   enter continue   ·   esc cancel"),
		}
		if m.syncBlocked() && !m.syncOpts.schedule {
			// Syncing now would be rejected and recorded as a failed operation.
			// Saying so here is the only place it changes the decision.
			lines = append(lines[:len(lines)-2],
				m.st.err.Render("A sync window is blocking this — syncing now will be refused."),
				"",
				m.st.dim.Render("j/k move   ·   space toggle   ·   enter continue   ·   esc cancel"))
		}
		box = m.st.modal.Render(strings.Join(
			clampModalBody(lines, m.modalContentWidth(64), m.modalContentHeight()), "\n"))
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// renderRevPicker draws the branch/tag chooser.
func (m *Model) renderRevPicker() string {
	p := &m.revPicker
	w := m.modalContentWidth(64)

	lines := []string{
		m.st.accent.Render("Target revision"),
		m.st.filter.Render("> " + p.filter + "▏"),
		"",
	}

	switch {
	case p.loading:
		lines = append(lines, m.st.dim.Render("loading branches and tags…"))
	case p.err != "":
		lines = append(lines, m.st.err.Render(wrapText(p.err, w)))
	case len(p.rows) == 0:
		lines = append(lines, m.st.dim.Render(fmt.Sprintf("no ref matches %q", p.filter)))
	default:
		h := m.revPickerHeight()
		// The kind column is sized to the widest kind present and reserved
		// before the name gets its share: a branch and a tag can share a name,
		// and truncating the label away would hide which one is being picked.
		kindW := 0
		for _, i := range p.rows {
			if k := lipgloss.Width(p.items[i].kind); k > kindW {
				kindW = k
			}
		}
		nameW := w - kindW - 3
		if nameW < 8 {
			nameW = 8
		}
		for r := p.top; r < len(p.rows) && r < p.top+h; r++ {
			it := p.items[p.rows[r]]
			cursor := " "
			style := lipgloss.NewStyle()
			if r == p.cur {
				cursor = m.st.accent.Render(m.gl.cursor)
				style = m.st.selected
			}
			icon := ""
			switch it.kind {
			case "tag":
				icon = m.gl.prefix(m.gl.tagRef)
			case "branch", "default branch":
				icon = m.gl.prefix(m.gl.branchRef)
			}
			lines = append(lines, cursor+" "+icon+
				style.Render(padRight(truncate(it.name, nameW-lipgloss.Width(icon)), nameW-lipgloss.Width(icon)))+" "+
				m.st.dim.Render(it.kind))
		}
		if n := len(p.rows); n > h {
			lines = append(lines, m.st.dim.Render(fmt.Sprintf("… %d refs", n)))
		}
	}

	lines = append(lines, "",
		m.st.dim.Render("type to filter  ·  ↑↓ move  ·  enter select  ·  esc cancel"))
	return m.st.modal.Render(strings.Join(
		clampModalBody(lines, w, m.modalContentHeight()), "\n"))
}
