package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Side-by-side diff.
//
// A unified diff answers "what changed"; a side-by-side answers "changed from
// what to what", which is the question anyone comparing a live manifest to a
// desired one is actually asking. Reading `- replicas: 3` and `+ replicas: 5`
// eleven lines apart is work the layout can do instead.
//
// It is built from the rendered unified diff rather than from a second pass
// over the manifests. That keeps one pipeline: the search, the JSON-path
// labels, and the noise filter all run once, and side-by-side is a way of
// laying out their result rather than a parallel path that could disagree with
// what the reader was just looking at.

// sxsMinWidth is the narrowest terminal that gets two columns.
//
// Below it each side would be under 40 cells, where a manifest line is mostly
// truncation and the layout costs more than it explains. The view falls back to
// unified rather than refusing, so a narrow split is degraded, not broken.
const sxsMinWidth = 100

// sxsRow is one output line: a left cell, a right cell, and what happened.
type sxsRow struct {
	left, right string
	// kind is ' ' unchanged, '-' removed, '+' added, '~' replaced (a removal
	// paired with an addition), or 'h' for a header or hunk marker, which spans
	// the full width.
	kind byte
}

// sideBySide turns unified diff lines into paired rows.
//
// Removals and additions that sit together are one row, which is what makes the
// change readable — the pairing is the whole point. Runs of unequal length pair
// as far as they go and leave the remainder against a blank cell.
func sideBySide(lines []string) []sxsRow {
	var out []sxsRow
	var dels, adds []string

	// flush pairs whatever removals and additions have accumulated.
	flush := func() {
		n := len(dels)
		if len(adds) > n {
			n = len(adds)
		}
		for i := 0; i < n; i++ {
			var l, r string
			kind := byte('~')
			if i < len(dels) {
				l = dels[i]
			} else {
				kind = '+'
			}
			if i < len(adds) {
				r = adds[i]
			} else {
				kind = '-'
			}
			out = append(out, sxsRow{left: l, right: r, kind: kind})
		}
		dels, adds = nil, nil
	}

	for _, raw := range lines {
		switch {
		case strings.HasPrefix(raw, "-"):
			dels = append(dels, raw[1:])
			continue
		case strings.HasPrefix(raw, "+"):
			adds = append(adds, raw[1:])
			continue
		}
		// Anything else ends the run: context, a header, a hunk marker, or the
		// blank line between resources.
		flush()
		switch {
		case strings.HasPrefix(raw, "==="), strings.HasPrefix(raw, "@@"):
			out = append(out, sxsRow{left: raw, kind: 'h'})
		case strings.HasPrefix(raw, " "):
			out = append(out, sxsRow{left: raw[1:], right: raw[1:], kind: ' '})
		default:
			// A line with no diff prefix — the "(no differences)" notice, or a
			// blank separator.
			out = append(out, sxsRow{left: raw, right: raw, kind: ' '})
		}
	}
	flush()
	return out
}

// renderSideBySide draws the paired rows into two columns.
func (m *Model) renderSideBySide(rows []sxsRow, top, h int) []string {
	// The gutter is a single vertical rule. Two columns of text with nothing
	// between them read as one wrapped column.
	const gutter = " │ "
	col := (m.width - lipgloss.Width(gutter)) / 2
	if col < 1 {
		col = 1
	}

	out := make([]string, 0, h)
	for i := top; i < len(rows) && len(out) < h; i++ {
		r := rows[i]

		if r.kind == 'h' {
			// Headers and hunk markers span both columns: they describe the
			// comparison rather than either side of it.
			out = append(out, m.st.diffHunk.Render(truncate(r.left, m.width)))
			continue
		}

		lStyle, rStyle := lipgloss.NewStyle(), lipgloss.NewStyle()
		switch r.kind {
		case '-':
			lStyle = m.st.diffDel
		case '+':
			rStyle = m.st.diffAdd
		case '~':
			lStyle, rStyle = m.st.diffDel, m.st.diffAdd
		default:
			lStyle, rStyle = m.st.dim, m.st.dim
		}

		// A cell that is empty on one side is filled rather than left blank, so
		// the reader can see at a glance that the line exists on only one side
		// — an empty cell and a cell of spaces look identical otherwise.
		left := padRight(truncate(r.left, col), col)
		right := truncate(r.right, col)
		if r.left == "" && (r.kind == '+' || r.kind == '~') {
			left = m.st.dim.Render(padRight(strings.Repeat("·", min(col, 3)), col))
			lStyle = lipgloss.NewStyle()
		}
		if r.right == "" && (r.kind == '-' || r.kind == '~') {
			right = m.st.dim.Render(strings.Repeat("·", min(col, 3)))
			rStyle = lipgloss.NewStyle()
		}

		out = append(out, lStyle.Render(left)+m.st.dim.Render(gutter)+rStyle.Render(right))
	}
	return out
}

// sxsAvailable reports whether the terminal is wide enough for two columns.
func (m *Model) sxsAvailable() bool { return m.width >= sxsMinWidth }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
