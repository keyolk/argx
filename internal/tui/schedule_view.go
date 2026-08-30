package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The scheduled-sync list.
//
// One row per sync, soonest first, with finished ones falling to the bottom
// rather than disappearing: a sync that declined to run is the thing the reader
// most needs to see, and a row that vanishes takes its reason with it.

// renderSchedule draws the scheduled-sync view.
func (m *Model) renderSchedule() string {
	h := m.bodyHeight()

	if len(m.schedules) == 0 {
		return m.emptyBody(h, "no scheduled syncs — press s on an application whose "+
			"sync window is closed, then choose to schedule")
	}

	now := time.Now()
	lines := make([]string, 0, h)
	lines = append(lines, m.st.header.Render(truncate(
		"  STATE      WHEN               APPLICATION", m.width)))

	for r := m.scheduleTop; r < len(m.schedules) && len(lines) < h; r++ {
		s := m.schedules[r]

		cursor := " "
		if r == m.scheduleCur {
			cursor = m.st.accent.Render(m.gl.cursor)
		}

		var stateStyle lipgloss.Style
		switch s.state {
		case scheduleDone:
			stateStyle = m.st.success
		case scheduleFailed:
			stateStyle = m.st.err
		case scheduleCancelled:
			stateStyle = m.st.warn
		case scheduleRunning:
			stateStyle = m.st.info
		default:
			stateStyle = m.st.dim
		}

		when := s.when(now)
		if s.state.finished() {
			// A finished row's firing time is history; what it did and when is
			// the useful fact.
			when = s.ranAt.Local().Format("01-02 15:04")
		}

		nameStyle := lipgloss.NewStyle()
		if r == m.scheduleCur {
			nameStyle = m.st.selected
		}
		name := s.appName
		if m.multiServer() {
			name = m.ctxStyle(s.context).Render(s.context) + m.st.dim.Render("/") + name
		}

		line := cursor + " " + stateStyle.Render(padRight(s.state.String(), 10)) + " " +
			m.st.dim.Render(padRight(truncate(when, 18), 18)) + " " +
			nameStyle.Render(name)
		lines = append(lines, truncate(line, m.width))

		// The second line is why, and it only exists when there is a why: a
		// blank continuation on every row would halve the number visible.
		if detail := m.scheduleDetail(s); detail != "" && len(lines) < h {
			lines = append(lines, truncate("             "+m.st.dim.Render(detail), m.width))
		}
	}
	return padBody(lines, h)
}

// scheduleDetail is the second line: what is being waited for, or what happened.
func (m *Model) scheduleDetail(s scheduled) string {
	if s.reason != "" {
		return s.reason
	}
	if s.state != scheduleWaiting || s.window.Schedule == "" {
		return ""
	}
	// Naming the window is what makes the wait legible: "waiting" alone leaves
	// the reader to guess whether argx is stuck or the window is simply hours
	// away.
	return fmt.Sprintf("waiting for %s window %q (%s %s)",
		s.window.Kind, s.window.Schedule, s.window.Duration, s.window.Zone())
}

// handleScheduleKey handles the scheduled-sync list.
func (m *Model) handleScheduleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.scheduleCur++
	case "k", "up":
		m.scheduleCur--
	case "g", "home":
		m.scheduleCur = 0
	case "G", "end":
		m.scheduleCur = len(m.schedules) - 1
	case "ctrl+d", "pgdown":
		m.scheduleCur += m.bodyHeight() / 2
	case "ctrl+u", "pgup":
		m.scheduleCur -= m.bodyHeight() / 2
	case "x", "d":
		// Cancelling is destructive only in the sense that the sync will not
		// happen, which is recoverable by scheduling it again — so no
		// confirmation, unlike the sync itself.
		if s := m.currentSchedule(); s != nil {
			m.cancelSchedule(s.id)
		}
	case "c":
		if n := m.clearFinishedSchedules(); n > 0 {
			m.setToast(fmt.Sprintf("cleared %d finished", n))
			m.clampScheduleCursor()
		}
	case "o":
		if s := m.currentSchedule(); s != nil {
			if u := m.scheduleURL(*s); u != "" {
				return m, m.openBrowserCmd([]string{u})
			}
		}
	case "h", "left", "esc":
		m.pop()
		return m, nil
	}

	m.clampScheduleCursor()
	return m, nil
}

// currentSchedule is the row under the cursor.
func (m *Model) currentSchedule() *scheduled {
	if m.scheduleCur < 0 || m.scheduleCur >= len(m.schedules) {
		return nil
	}
	return &m.schedules[m.scheduleCur]
}

// scheduleURL is the application's page on its own server.
//
// Built from the ref rather than a loaded Application, because a schedule
// outlives whatever the reader is looking at.
func (m *Model) scheduleURL(s scheduled) string {
	c, err := m.fleet.Client(s.context)
	if err != nil {
		return ""
	}
	return c.Context().AppURL(s.app.Name)
}

// clampScheduleCursor keeps the cursor and viewport inside the list.
func (m *Model) clampScheduleCursor() {
	if m.scheduleCur >= len(m.schedules) {
		m.scheduleCur = len(m.schedules) - 1
	}
	if m.scheduleCur < 0 {
		m.scheduleCur = 0
	}
	h := m.bodyHeight()
	if m.scheduleCur < m.scheduleTop {
		m.scheduleTop = m.scheduleCur
	}
	if m.scheduleCur >= m.scheduleTop+h {
		m.scheduleTop = m.scheduleCur - h + 1
	}
	if m.scheduleTop < 0 {
		m.scheduleTop = 0
	}
}

// armQuitConfirm asks before dropping pending scheduled syncs.
//
// Nothing else in argx blocks quitting, and this only does because the
// schedules live in this process: there is no daemon to pick them up, so
// quitting is what cancels them.
func (m *Model) armQuitConfirm(n int) {
	now := time.Now()
	body := []string{
		fmt.Sprintf("%d scheduled sync(s) have not run yet.", n),
		m.st.dim.Render("They exist only in this session and will not run after argx exits."),
		"",
	}
	shown := 0
	for _, s := range m.schedules {
		if s.state.finished() {
			continue
		}
		if shown >= 8 {
			body = append(body, fmt.Sprintf("  … and %d more", n-shown))
			break
		}
		body = append(body, "  "+s.appName+"  →  "+s.when(now))
		shown++
	}
	m.confirm = confirmState{
		title:  "Quit and drop them?",
		body:   body,
		action: func() tea.Cmd { return tea.Quit },
	}
	m.overlay = overlayConfirm
}
