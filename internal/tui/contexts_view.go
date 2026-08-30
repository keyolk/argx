package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The context list, and the detail panel behind `enter`.

const (
	ctxNameCol = 26
	ctxKindCol = 12
)

// renderContexts draws the context view.
func (m *Model) renderContexts() string {
	h := m.bodyHeight()

	if !m.ctxLoaded {
		return m.emptyBody(h, "asking each server who argx is…")
	}
	if len(m.ctxRows) == 0 {
		return m.emptyBody(h, "no contexts configured")
	}
	if m.ctxDetail {
		return m.renderContextDetail(h)
	}

	now := time.Now()
	lines := make([]string, 0, h)
	lines = append(lines, m.st.header.Render(truncate(
		"  SERVER                     AUTH         IDENTITY", m.width)))

	for r := m.ctxTop; r < len(m.ctxRows) && len(lines) < h; r++ {
		row := m.ctxRows[r]

		cursor := " "
		if r == m.ctxCur {
			cursor = m.st.accent.Render(m.gl.cursor)
		}

		nameStyle := m.ctxStyle(row.name)
		if r == m.ctxCur {
			nameStyle = m.st.selected
		}

		kindStyle := m.st.dim
		switch {
		case row.err != nil:
			kindStyle = m.st.err
		case row.authKind() == "SSO":
			kindStyle = m.st.info
		}

		id := row.identity()
		if row.err != nil {
			id = m.st.err.Render("the server refused or could not be reached")
		}

		line := cursor + " " +
			nameStyle.Render(padRight(truncate(row.name, ctxNameCol), ctxNameCol)) + " " +
			kindStyle.Render(padRight(row.authKind(), ctxKindCol)) + " " + id
		lines = append(lines, truncate(line, m.width))

		// The second line carries what needs saying about this credential, and
		// only what needs saying: a healthy admin session gets none.
		if note := m.contextNote(row, now); note != "" && len(lines) < h {
			lines = append(lines, truncate(strings.Repeat(" ", 4)+note, m.width))
		}
	}
	return padBody(lines, h)
}

// contextNote is the warning line: expiry, denied actions, TLS, errors.
func (m *Model) contextNote(row contextRow, now time.Time) string {
	var parts []string

	if row.err != nil {
		parts = append(parts, m.st.err.Render(trimErr(row.err)))
	}
	if text, warn := row.age(now); text != "" {
		style := m.st.dim
		if warn {
			style = m.st.err
		}
		parts = append(parts, style.Render(text))
	}
	if d := row.denied(); len(d) > 0 && row.err == nil {
		// What is missing, not what is granted: a session that can do
		// everything needs no list, and one that cannot sync needs that word.
		parts = append(parts, m.st.warn.Render("cannot "+strings.Join(d, ", ")))
	}
	if row.insecure {
		parts = append(parts, m.st.warn.Render("TLS verification off"))
	}
	return strings.Join(parts, m.st.dim.Render("  ·  "))
}

// renderContextDetail draws everything known about one context.
func (m *Model) renderContextDetail(h int) string {
	row := m.currentContext()
	if row == nil {
		return m.emptyBody(h, "no context selected")
	}
	now := time.Now()

	label := func(k, v string) string {
		return "  " + m.st.dim.Render(padRight(k, 18)) + v
	}

	lines := []string{
		m.st.accent.Render(row.name),
		"",
		m.st.header.Render("CONNECTION"),
		label("server", row.server),
	}
	// Only when there is one: a labelled blank reads as missing data rather
	// than as a field that does not apply.
	if u := m.contextURL(row.name); u != "" {
		lines = append(lines, label("url", u))
	}
	if row.insecure {
		lines = append(lines, label("tls", m.st.warn.Render("verification disabled")))
	}

	lines = append(lines, "", m.st.header.Render("CREDENTIAL"))
	// Where it comes from is the one thing here that names a place to edit.
	src := row.source
	if src == "" {
		src = m.st.dim.Render("(none — argx has no credential for this server)")
	}
	lines = append(lines, label("source", src))

	if row.claimsErr != nil {
		lines = append(lines, label("token", m.st.warn.Render(
			"not a readable JWT: "+trimErr(row.claimsErr))))
	} else {
		c := row.claims
		lines = append(lines, label("kind", row.authKind()))
		if c.Subject != "" {
			lines = append(lines, label("subject", c.Subject))
		}
		if iss := row.issuer(); iss != "" {
			lines = append(lines, label("issuer", iss))
		}
		if c.Email != "" {
			lines = append(lines, label("email", c.Email))
		}
		if c.Name != "" {
			lines = append(lines, label("name", c.Name))
		}
		if c.ID != "" {
			// Argo CD lists an account's API keys by id, so this is the one
			// value that connects a token in hand to an entry in that list —
			// the closest thing an API key has to a name.
			lines = append(lines, label("token id", c.ID))
		}
		if !c.IssuedAt.IsZero() {
			lines = append(lines, label("issued",
				c.IssuedAt.Local().Format("2006-01-02 15:04")+
					m.st.dim.Render("  ("+humanSince(now.Sub(c.IssuedAt))+" ago)")))
		}
		switch {
		case c.Expired(now):
			lines = append(lines, label("expires", m.st.err.Render(
				c.ExpiresAt.Local().Format("2006-01-02 15:04")+"  EXPIRED")))
		case !c.ExpiresAt.IsZero():
			lines = append(lines, label("expires",
				c.ExpiresAt.Local().Format("2006-01-02 15:04")+
					m.st.dim.Render("  (in "+humanSince(c.ExpiresAt.Sub(now))+")")))
		default:
			// Saying so beats a blank row: an API key with no expiry is a
			// deliberate choice, not missing data.
			lines = append(lines, label("expires", m.st.dim.Render("never")))
		}
	}

	lines = append(lines, "", m.st.header.Render("THE SERVER'S VIEW"))
	switch {
	case row.err != nil:
		lines = append(lines, label("status", m.st.err.Render(trimErr(row.err))))
	case row.user == nil:
		lines = append(lines, label("status", m.st.dim.Render("not asked")))
	default:
		u := row.user
		if u.LoggedIn {
			lines = append(lines, label("status", m.st.success.Render("authenticated")))
		} else {
			// A 200 that says loggedIn:false is an anonymous session, which
			// looks like success until something is refused.
			lines = append(lines, label("status", m.st.err.Render("not logged in")))
		}
		lines = append(lines, label("username", u.Username))
		if u.Iss != "" && u.Iss != "argocd" {
			lines = append(lines, label("issuer", u.Iss))
		}
		if len(u.Groups) > 0 {
			// Argo CD's RBAC maps groups onto permissions, so these are what
			// decide the answers below.
			lines = append(lines, label("groups", strings.Join(u.Groups, ", ")))
		} else if !row.claims.Local() {
			lines = append(lines, label("groups", m.st.warn.Render(
				"none — an SSO session with no groups usually has no permissions")))
		}
	}

	if len(row.perms) > 0 {
		lines = append(lines, "", m.st.header.Render("WHAT ARGX MAY DO HERE"))
		for _, p := range row.perms {
			mark, style := m.gl.no, m.st.err
			if p.allowed {
				mark, style = m.gl.yes, m.st.success
			}
			lines = append(lines, "  "+style.Render(mark)+" "+p.label)
		}
	}

	// Clamp before slicing, so scrolling past the end cannot show a blank
	// panel with content still above it.
	if max := len(lines) - h; m.pagerTop > max {
		m.pagerTop = max
	}
	if m.pagerTop < 0 {
		m.pagerTop = 0
	}

	out := make([]string, 0, h)
	for i := m.pagerTop; i < len(lines) && len(out) < h; i++ {
		out = append(out, truncate(lines[i], m.width))
	}
	// A panel that is taller than the screen has to say so, or the last
	// permission silently does not exist as far as the reader can tell.
	if len(lines) > h {
		out[len(out)-1] = truncate(m.st.dim.Render(fmt.Sprintf(
			"… %d more line(s) — j/k scrolls", len(lines)-m.pagerTop-h+1)), m.width)
	}
	return padBody(out, h)
}

// trimErr shortens an error for a single line.
func trimErr(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}

// contextURL is a server's web UI address.
func (m *Model) contextURL(name string) string {
	c, err := m.fleet.Client(name)
	if err != nil {
		return ""
	}
	return c.Context().BaseURL()
}

// currentContext is the row under the cursor.
func (m *Model) currentContext() *contextRow {
	if m.ctxCur < 0 || m.ctxCur >= len(m.ctxRows) {
		return nil
	}
	return &m.ctxRows[m.ctxCur]
}

// handleContextsKey handles the context view.
func (m *Model) handleContextsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ctxDetail {
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
		case "o":
			if r := m.currentContext(); r != nil {
				return m, m.openBrowserCmd([]string{m.contextURL(r.name)})
			}
		case "enter", "esc", "h", "left":
			m.ctxDetail = false
			m.pagerTop = 0
		}
		if m.pagerTop < 0 {
			m.pagerTop = 0
		}
		return m, nil
	}

	switch msg.String() {
	case "j", "down":
		m.ctxCur++
	case "k", "up":
		m.ctxCur--
	case "g", "home":
		m.ctxCur = 0
	case "G", "end":
		m.ctxCur = len(m.ctxRows) - 1
	case "enter", "l", "right":
		if m.currentContext() != nil {
			m.ctxDetail = true
			m.pagerTop = 0
		}
		return m, nil
	case "o":
		if r := m.currentContext(); r != nil {
			return m, m.openBrowserCmd([]string{m.contextURL(r.name)})
		}
	case "r", "ctrl+r":
		return m, m.loadContextsCmd()
	case "h", "left", "esc":
		m.pop()
		return m, nil
	}

	m.clampContextCursor()
	return m, nil
}

// clampContextCursor keeps the cursor and viewport inside the list.
func (m *Model) clampContextCursor() {
	if m.ctxCur >= len(m.ctxRows) {
		m.ctxCur = len(m.ctxRows) - 1
	}
	if m.ctxCur < 0 {
		m.ctxCur = 0
	}
	h := m.bodyHeight()
	if m.ctxCur < m.ctxTop {
		m.ctxTop = m.ctxCur
	}
	if m.ctxCur >= m.ctxTop+h {
		m.ctxTop = m.ctxCur - h + 1
	}
	if m.ctxTop < 0 {
		m.ctxTop = 0
	}
}

// contextsSummary is the status line: how many servers, and what is wrong.
func (m *Model) contextsSummary() []string {
	var parts []string
	bad, limited, expiring := 0, 0, 0
	now := time.Now()
	for _, r := range m.ctxRows {
		if r.err != nil {
			bad++
			continue
		}
		if len(r.denied()) > 0 {
			limited++
		}
		if _, warn := r.age(now); warn {
			expiring++
		}
	}
	parts = append(parts, m.st.dim.Render(fmt.Sprintf("%d server(s)", len(m.ctxRows))))
	if bad > 0 {
		parts = append(parts, m.st.err.Render(fmt.Sprintf("%d unusable", bad)))
	}
	if expiring > 0 {
		parts = append(parts, m.st.err.Render(fmt.Sprintf("%d expiring", expiring)))
	}
	if limited > 0 {
		parts = append(parts, m.st.warn.Render(fmt.Sprintf("%d limited", limited)))
	}
	return parts
}

var _ = lipgloss.NewStyle
