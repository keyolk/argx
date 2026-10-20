package tui

// The status line, the footer hints, and the modal overlays — the frame around
// whatever body is being rendered.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/render"
	"github.com/keyolk/tuikit/toast"
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
			// Who last moved this application, on every tab. It is the fact a
			// reader most often wants while doing something else — deciding
			// whether to sync, reading a diff — and DETAILS is the wrong place
			// to have to go for it mid-action.
			if when, who, ok := a.LastSync(); ok {
				parts = append(parts, m.st.dim.Render(fmt.Sprintf(
					"synced %s ago by %s", humanSince(time.Since(when)), who)))
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
		// A reader on this screen is usually here because syncing is blocked,
		// which is the one moment "you can wait for it instead" is worth
		// knowing. Not a footer hint: no key here does it, the sync options do.
		parts = append(parts, m.st.dim.Render(fmt.Sprintf(
			"to schedule a sync: %s, then %s in the sync options",
			m.keyHint(stackDetails, aDetailsSync), m.keyHint(stackSyncOpts, aOptWait))))

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
	// Toasts are cleared by their own timers (see Update), so View only draws
	// them: a sticky error first, then the newest status.
	parts = append(parts, m.toastParts()...)

	// Each part carries its own style already; the separator is styled on its
	// own rather than wrapping the joined string, which would nest escape
	// sequences and lose the inner colors after the first reset.
	return truncate(strings.Join(parts, m.st.dim.Render(m.gl.sep)), m.width)
}

// toastParts renders the queued toasts for the status line, each with its
// level's marker so severity survives NO_COLOR.
func (m *Model) toastParts() []string {
	st := toast.Styles{
		Info: m.st.info, Success: m.st.success, Warn: m.st.warn, Error: m.st.err, Progress: m.st.info,
		ASCII: m.gl.set == iconsASCII,
	}
	var parts []string
	for _, t := range m.toasts.Items() {
		parts = append(parts, toast.RenderItem(t, m.width, st))
	}
	return parts
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

// renderFooter is the always-visible hint line, derived from the binding
// table for the keys that are live right now.
//
// The narrowing keeps argx's old priorities as data: each binding's Footer
// number says how long it survives a narrow terminal, and quit, help and back
// are pinned — the exit is how you leave, help is the only place the keys that
// did not fit are written down, and esc is the one exit that keeps the
// reader's place. The pending-schedule hint leads because schedules run only
// in this process and are otherwise invisible from wherever the reader is.
//
// A filter prompt and a range in progress are modes, so the hints change with
// them: the prompt's stack has no list keys (almost every key is text there),
// and a range shows only the keys that end it (see actionVisible).
func (m *Model) renderFooter() string {
	line := m.keymap().Footer(m.keyStack(), m.width, m.keyOpts(), m.footerStyle())
	return m.st.footer.Render(line)
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
	return m.spliceOverlay(frame, m.overlayBox())
}

// overlayBox is the modal's own text. It is separate from renderOverlay so the
// mouse handler measures the same box View draws instead of recomputing it.
func (m *Model) overlayBox() string {
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
		lines = append(lines, "", m.confirmChoices(), m.modalHint(stackConfirm, w))
		box = m.st.modal.Render(strings.Join(
			clampModalBody(lines, w, m.modalContentHeight()), "\n"))

	case overlayContainer:
		box = m.renderContainerPicker()

	case overlayRevPicker:
		box = m.renderRevPicker()

	case overlayPalette:
		box = m.renderPalette()

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
			row(0, check(m.syncOpts.prune)+" "+m.st.info.Render(m.keyHint(stackSyncOpts, aOptPrune))+" prune  "+
				m.st.dim.Render("(delete resources not in git)")),
			row(1, check(m.syncOpts.dryRun)+" "+m.st.info.Render(m.keyHint(stackSyncOpts, aOptDryRun))+" dry-run  "+
				m.st.dim.Render("(compute only, apply nothing)")),
			row(2, check(m.syncOpts.schedule)+" "+m.st.info.Render(m.keyHint(stackSyncOpts, aOptWait))+" wait for the sync window  "+
				m.st.dim.Render("(while argx runs)")),
			"",
			m.modalHint(stackSyncOpts, m.modalContentWidth(64)),
		}
		if m.syncBlocked() && !m.syncOpts.schedule {
			// Syncing now would be rejected and recorded as a failed operation.
			// Saying so here is the only place it changes the decision.
			lines = append(lines[:len(lines)-2],
				m.st.err.Render("A sync window is blocking this — syncing now will be refused."),
				"",
				m.modalHint(stackSyncOpts, m.modalContentWidth(64)))
		}
		box = m.st.modal.Render(strings.Join(
			clampModalBody(lines, m.modalContentWidth(64), m.modalContentHeight()), "\n"))
	}

	return box
}

// spliceOverlay centers box over a dimmed copy of frame.
//
// The frame is kept rather than replaced with blank space so the reader still
// sees what the modal is about — which list, which rows are marked. It is
// stripped and dimmed first so the box stands out; render.Center keeps the
// result exactly the terminal's size and replaces a wide character straddling
// either seam with a space instead of shifting the line.
func (m *Model) spliceOverlay(frame, box string) string {
	return render.Center(render.Dim(frame), box, m.width, m.height)
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
				style.Render(render.PadRight(truncate(it.name, nameW-lipgloss.Width(icon)), nameW-lipgloss.Width(icon)))+" "+
				m.st.dim.Render(it.kind))
		}
		if n := len(p.rows); n > h {
			lines = append(lines, m.st.dim.Render(fmt.Sprintf("… %d refs", n)))
		}
	}

	lines = append(lines, "",
		m.st.dim.Render("type to filter  ·  ")+m.modalHint(stackRevPicker, w-18))
	return m.st.modal.Render(strings.Join(
		clampModalBody(lines, w, m.modalContentHeight()), "\n"))
}
