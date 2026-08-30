package tui

import (
	"fmt"
	"sort"
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

// windowRow is one line of the view.
type windowRow struct {
	w argocd.SyncWindow
	// active is true when the window is open right now.
	active bool
	// detailed is true when the project's spec supplied the selectors and time
	// zone. The per-application payload carries only kind, schedule, duration
	// and manualSync, and the two calls are separate — a window edited between
	// them is present in one and not the other, which is not hypothetical:
	// these are edited by automation.
	detailed bool
}

// windowRows are the windows that govern the focused application.
//
// Only those: a project's other windows govern other applications, and listing
// them here made the reader work out which lines were about the thing they were
// looking at. The server decides membership — its selectors are glob patterns
// over name, cluster, and namespace, and reimplementing that matching to filter
// a fuller list would be a second, divergent answer to a question the server
// already answers.
//
// The project's spec is still consulted, but only to recover what the
// per-application payload drops — the selectors and the time zone — so a window
// would otherwise render with no indication of what it covers, in a zone that
// is not its own.
func (m *Model) windowRows() []windowRow {
	if m.windows == nil {
		return nil
	}

	// The same window appears in both payloads with different fields, so it is
	// keyed on what both carry.
	key := func(w argocd.SyncWindow) string {
		return w.Kind + "|" + w.Schedule + "|" + w.Duration
	}

	detailed := make(map[string]argocd.SyncWindow, len(m.projectWindows))
	for _, w := range m.projectWindows {
		detailed[key(w)] = w
	}
	active := make(map[string]bool, len(m.windows.ActiveWindows))
	for _, w := range m.windows.ActiveWindows {
		active[key(w)] = true
	}

	rows := make([]windowRow, 0, len(m.windows.AssignedWindows))
	for _, w := range m.windows.AssignedWindows {
		k := key(w)
		full, matched := detailed[k]
		if matched {
			w = full
		}
		rows = append(rows, windowRow{w: w, active: active[k], detailed: matched})
	}

	// Open windows lead: what is in effect right now is what the reader came
	// for, and a schedule that will not matter for hours is context.
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].active && !rows[j].active
	})
	return rows
}

// renderWindows draws the sync-window view.
func (m *Model) renderWindows() string {
	h := m.bodyHeight()
	rows := m.windowRows()

	if len(rows) == 0 {
		txt := "loading sync windows…"
		if !m.loading {
			// No window governing this application is the common case and a
			// meaningful answer, not an empty screen. The distinction matters:
			// the project may well have windows, just none that match this app.
			txt = "no sync window applies to this application — its syncing is never blocked by a schedule"
			if n := len(m.projectWindows); n > 0 {
				txt += fmt.Sprintf(" (the project has %d, none matching)", n)
			}
		}
		return m.emptyBody(h, txt)
	}

	lines := make([]string, 0, h)
	lines = append(lines, m.st.header.Render(truncate(
		"   KIND   SCHEDULE          DURATION  ZONE            MATCHED BY", m.width)))

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

		schedStyle := lipgloss.NewStyle()
		if r == m.windowCur {
			schedStyle = m.st.selected
		}

		line := cursor + state + kind + " " +
			schedStyle.Render(padRight(row.w.Schedule, 17)) + " " +
			m.st.dim.Render(padRight(row.w.Duration, 9)) + " " +
			m.st.dim.Render(padRight(truncate(m.windowZoneCell(row), 15), 15)) + " " +
			m.st.dim.Render(m.windowScopeCell(row))
		lines = append(lines, truncate(line, m.width))
	}
	return padBody(lines, h)
}

// windowZoneCell renders the time zone, or marks it unknown.
//
// The per-application payload omits the zone, so defaulting to UTC there would
// report a schedule in the wrong zone — an Asia/Seoul window read as UTC is off
// by nine hours, which is the difference between "open now" and "opens
// tonight".
func (m *Model) windowZoneCell(row windowRow) string {
	if !row.detailed {
		return "?"
	}
	return row.w.Zone()
}

// windowScopeCell renders what a window covers, or says the detail is missing.
//
// Silence is not an option here: an empty selector set legitimately means "the
// whole project", so rendering the same thing for "we could not find this
// window's definition" would state the opposite of the truth.
func (m *Model) windowScopeCell(row windowRow) string {
	if !row.detailed {
		return m.st.warn.Render("(selectors unavailable — the project's list changed)")
	}
	return windowScope(row.w)
}

// windowScope renders the selectors that matched this application.
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
		// Straight to the project's windows tab, which is where they are
		// defined and edited — not to the project overview.
		if m.app != nil {
			return m, m.openBrowserCmd([]string{m.projectWindowsURL(m.app)})
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
