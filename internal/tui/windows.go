package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
)

// The sync-window view answers two questions that the rest of argx cannot:
// whether a sync is allowed to run right now, and — when it is not — which
// window is stopping it.
//
// Windows are defined on the AppProject, so one governs every application whose
// name, cluster, or namespace its selectors match. argx shows them and does not
// edit them: a change here reaches every application in the project at once,
// which is a decision that belongs in the repository that owns the project.

// windowRow is one line of the view. The project's windows are shown alongside
// the application's own, so a window that nearly matched is visible rather than
// silently absent.
type windowRow struct {
	w argocd.SyncWindow
	// applies is true when this window governs the focused application.
	applies bool
	// active is true when it is open right now.
	active bool
}

// windowRows assembles the view's content.
func (m *Model) windowRows() []windowRow {
	if m.windows == nil {
		return nil
	}

	// The API returns the same window in more than one list, and the payloads
	// differ — the per-application form drops the selectors. Key on the fields
	// present in both so a window is not listed twice.
	key := func(w argocd.SyncWindow) string {
		return w.Kind + "|" + w.Schedule + "|" + w.Duration
	}
	applies := make(map[string]bool, len(m.windows.AssignedWindows))
	for _, w := range m.windows.AssignedWindows {
		applies[key(w)] = true
	}
	active := make(map[string]bool, len(m.windows.ActiveWindows))
	for _, w := range m.windows.ActiveWindows {
		active[key(w)] = true
	}

	// The project's list carries the selectors, so it is preferred as the
	// source; anything assigned but missing from it is appended, which happens
	// when the session cannot read the project.
	var rows []windowRow
	seen := map[string]bool{}
	for _, w := range m.projectWindows {
		k := key(w)
		seen[k] = true
		rows = append(rows, windowRow{w: w, applies: applies[k], active: active[k]})
	}
	for _, w := range m.windows.AssignedWindows {
		if k := key(w); !seen[k] {
			rows = append(rows, windowRow{w: w, applies: true, active: active[k]})
		}
	}

	// Windows that govern this application lead: the reader came here to find
	// out about their own application, and the rest is context.
	stable := make([]windowRow, 0, len(rows))
	for _, r := range rows {
		if r.applies {
			stable = append(stable, r)
		}
	}
	for _, r := range rows {
		if !r.applies {
			stable = append(stable, r)
		}
	}
	return stable
}

// renderWindows draws the sync-window view.
func (m *Model) renderWindows() string {
	h := m.bodyHeight()
	rows := m.windowRows()

	if len(rows) == 0 {
		txt := "loading sync windows…"
		if !m.loading {
			// No windows at all is the common case and a meaningful answer, not
			// an empty screen.
			txt = "no sync windows on this project — syncing is never blocked by a schedule"
		}
		return m.emptyBody(h, txt)
	}

	lines := make([]string, 0, h)
	lines = append(lines, m.st.header.Render(truncate(
		"   KIND   SCHEDULE          DURATION  ZONE            APPLIES TO", m.width)))

	for r := m.windowTop; r < len(rows) && len(lines) < h; r++ {
		row := rows[r]
		cursor := " "
		if r == m.windowCur {
			cursor = m.st.accent.Render(m.gl.cursor)
		}

		// An allow window and a deny window mean opposite things, so they are
		// colored as opposites rather than by whether they are open.
		kindStyle := m.st.success
		if row.w.Blocks() {
			kindStyle = m.st.err
		}
		kind := kindStyle.Render(padRight(row.w.Kind, 6))

		// Open right now is the fact the reader is here for.
		state := "  "
		if row.active {
			state = m.st.warn.Render(m.gl.progressing) + " "
		}

		nameStyle := lipgloss.NewStyle()
		if !row.applies {
			// A window that does not govern this application is context, not
			// news; dimming it keeps the applicable ones legible.
			nameStyle = m.st.dim
		}
		if r == m.windowCur {
			nameStyle = m.st.selected
		}

		scope := windowScope(row.w)
		if !row.applies {
			scope += m.st.dim.Render("  (does not apply)")
		}

		line := cursor + state + kind + " " +
			nameStyle.Render(padRight(row.w.Schedule, 17)) + " " +
			m.st.dim.Render(padRight(row.w.Duration, 9)) + " " +
			m.st.dim.Render(padRight(truncate(row.w.Zone(), 15), 15)) + " " +
			scope
		lines = append(lines, truncate(line, m.width))
	}
	return padBody(lines, h)
}

// windowScope renders what a window applies to.
//
// An empty selector set means the whole project, which is worth saying outright
// — a blank cell reads as missing data rather than as "everything".
func windowScope(w argocd.SyncWindow) string {
	var parts []string
	if len(w.Applications) > 0 {
		parts = append(parts, strings.Join(dedupePatterns(w.Applications), ", "))
	}
	for _, c := range w.Clusters {
		parts = append(parts, "cluster:"+c)
	}
	for _, n := range w.Namespaces {
		parts = append(parts, "ns:"+n)
	}
	if len(parts) == 0 {
		return "the whole project"
	}
	return strings.Join(parts, "  ")
}

// dedupePatterns drops the redundant half of the `foo*` / `*foo*` pairs that
// Argo CD's UI writes, which would otherwise double the width of every scope.
func dedupePatterns(pats []string) []string {
	seen := make(map[string]bool, len(pats))
	var out []string
	for _, p := range pats {
		core := strings.Trim(p, "*")
		if seen[core] {
			continue
		}
		seen[core] = true
		out = append(out, p)
	}
	return out
}

// windowSummary is the one-line verdict shown in DETAILS and in the status bar.
func (m *Model) windowSummary() (text string, blocked bool) {
	if m.windows == nil {
		return "", false
	}
	n := len(m.windows.AssignedWindows)
	if n == 0 {
		return "none", false
	}
	if !m.windows.CanSync {
		return fmt.Sprintf("%d window(s) — syncing is BLOCKED now", n), true
	}
	if len(m.windows.ActiveWindows) > 0 {
		return fmt.Sprintf("%d window(s) — %d open, syncing allowed",
			n, len(m.windows.ActiveWindows)), false
	}
	return fmt.Sprintf("%d window(s) — none open, syncing allowed", n), false
}

// handleWindowsKey drives the sync-window view.
func (m *Model) handleWindowsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.windowRows()
	switch msg.String() {
	case "j", "down":
		m.windowCur++
	case "k", "up":
		m.windowCur--
	case "ctrl+d", "pgdown":
		m.windowCur += m.bodyHeight() / 2
	case "ctrl+u", "pgup":
		m.windowCur -= m.bodyHeight() / 2
	case "g", "home":
		m.windowCur, m.windowTop = 0, 0
	case "G", "end":
		m.windowCur = len(rows) - 1
	case "r", "ctrl+r":
		if m.app != nil {
			return m, m.loadWindowsCmd(*m.app)
		}
	case "o":
		// Windows are edited in the Argo CD UI, on the project — which is where
		// `o` goes from here, rather than to the application's own page.
		if m.app != nil {
			return m, m.openBrowserCmd([]string{m.projectURL(m.app)})
		}
	case "O":
		// The application's own page, for the reader who came here to check a
		// schedule and now wants the app itself.
		if m.app != nil {
			return m, m.openBrowserCmd([]string{m.appURL(m.app)})
		}
	case "h", "left":
		m.pop()
		return m, nil
	}

	if m.windowCur >= len(rows) {
		m.windowCur = len(rows) - 1
	}
	if m.windowCur < 0 {
		m.windowCur = 0
	}
	h := m.bodyHeight()
	if m.windowCur < m.windowTop {
		m.windowTop = m.windowCur
	}
	if m.windowCur >= m.windowTop+h {
		m.windowTop = m.windowCur - h + 1
	}
	if m.windowTop < 0 {
		m.windowTop = 0
	}
	return m, nil
}
