package tui

// The command palette (:). It lists the actions the keys would run on the
// screen behind it — keys.Entries over the same table dispatch uses — so it
// can never offer something a key would not do, and it reaches the actions
// whose only key is an uppercase letter a Korean input source cannot type.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/render"
)

// paletteState is the open palette.
type paletteState struct {
	query string
	cur   int
	// all is the screen's actions, captured on open: the stack behind the
	// palette does not change while it is open.
	all []keys.Entry
}

// paletteRows is how many matches the palette shows at once.
const paletteRows = 12

// openPalette captures the actions of the screen in front.
func (m *Model) openPalette() {
	stack := m.screenStack()
	if m.visualFrom >= 0 {
		stack = append([]keys.Ctx{ctxVisual}, stack...)
	}
	var all []keys.Entry
	for _, e := range m.keymap().Entries(stack, m.keyOpts()) {
		if e.Action == aPalette {
			continue // running the palette from the palette is a no-op
		}
		all = append(all, e)
	}
	m.palette = paletteState{all: all}
	m.overlay = overlayPalette
}

// paletteMatches is the entries the query keeps, best first.
func (m *Model) paletteMatches() []keys.Entry {
	return keys.Filter(m.palette.all, m.palette.query)
}

// handlePaletteKey edits the query, moves, and runs.
func (m *Model) handlePaletteKey(action string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.palette
	matches := m.paletteMatches()
	switch action {
	case aPalClose:
		m.overlay = overlayNone
		return m, nil
	case aPalDown:
		p.cur++
	case aPalUp:
		p.cur--
	case aPalBackspace:
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			p.cur = 0
		}
	case aPalClear:
		p.query, p.cur = "", 0
	case aPalRun:
		if p.cur < 0 || p.cur >= len(matches) {
			return m, nil
		}
		a := matches[p.cur].Action
		m.overlay = overlayNone
		return m.runAction(a)
	case "":
		// Text, verbatim: a jamo is part of the query here, not a shortcut.
		if len(msg.Runes) > 0 && !msg.Alt {
			p.query += string(msg.Runes)
			p.cur = 0
		}
	}
	if n := len(m.paletteMatches()); p.cur >= n {
		p.cur = n - 1
	}
	if p.cur < 0 {
		p.cur = 0
	}
	return m, nil
}

// renderPalette draws the palette box.
func (m *Model) renderPalette() string {
	p := &m.palette
	w := m.modalContentWidth(72)
	names := m.keyNames()
	matches := m.paletteMatches()

	lines := []string{
		m.st.accent.Render("Commands"),
		m.st.filter.Render(": " + p.query + "▏"),
		"",
	}
	if len(matches) == 0 {
		lines = append(lines, m.st.dim.Render(fmt.Sprintf("no command matches %q", p.query)))
	}
	top := 0
	if p.cur >= paletteRows {
		top = p.cur - paletteRows + 1
	}
	keyW := 0
	for _, e := range matches {
		keyW = max(keyW, lipgloss.Width(names.Join(e.Keys)))
	}
	keyW = min(keyW, w/3)
	for i := top; i < len(matches) && i < top+paletteRows; i++ {
		e := matches[i]
		cursor, style := " ", lipgloss.NewStyle()
		if i == p.cur {
			cursor, style = m.st.accent.Render(m.gl.cursor), m.st.selected
		}
		k := render.PadRight(truncate(names.Join(e.Keys), keyW), keyW)
		lines = append(lines, truncate(cursor+" "+m.st.info.Render(k)+"  "+
			style.Render(e.Help)+"  "+m.st.dim.Render(e.Group), w))
	}
	if n := len(matches) - (top + paletteRows); n > 0 {
		lines = append(lines, m.st.dim.Render(fmt.Sprintf("↓ %d more", n)))
	}
	lines = append(lines, "", m.modalHint(stackPalette, w))
	return m.st.modal.Render(strings.Join(clampModalBody(lines, w, m.modalContentHeight()), "\n"))
}
