package tui

// Cell-width-aware text helpers shared by every render path. Alignment is
// always computed in display cells — len() and rune counts both misplace
// columns after a CJK or emoji name.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// emptyBody is the placeholder shown when a view has nothing to list.
//
// The text is wrapped rather than truncated: these messages explain why the
// view is empty, and a sentence cut off at the terminal edge explains nothing.
// They are the one place in the render path where content is worth more than
// one line.
func (m *Model) emptyBody(h int, text string) string {
	lines := make([]string, 0, h)
	lines = append(lines, "")
	for _, l := range strings.Split(wrapText(text, m.width-4), "\n") {
		if len(lines) >= h {
			break
		}
		lines = append(lines, "  "+m.st.dim.Render(truncate(l, m.width-2)))
	}
	return padBody(lines, h)
}

// padBody pads a body to exactly h lines so the status and footer stay pinned
// to the bottom regardless of content length.
func padBody(lines []string, h int) string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// truncate cuts a string to w display cells.
//
// Rows are assembled from styled cells, so this is routinely handed a string
// that already contains ANSI sequences. A rune-by-rune walk gets that wrong
// three ways at once: it counts escape bytes as width (so the result is far
// shorter than asked for), it can cut a sequence in half (sending a broken
// control code to the terminal), and it drops the trailing reset (bleeding the
// color into every row below). ansi.Truncate is aware of all three, and of
// wide characters and graphemes besides.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// padRight pads to w display cells.
func padRight(s string, w int) string {
	d := w - lipgloss.Width(s)
	if d <= 0 {
		return s
	}
	return s + strings.Repeat(" ", d)
}

// shortRev abbreviates a git SHA, leaving non-SHA revisions (chart versions,
// tags) intact.
func shortRev(rev string) string {
	if len(rev) == 40 && isHex(rev) {
		return rev[:7]
	}
	return truncate(rev, 20)
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// wrapText hard-wraps at w cells for modal bodies.
//
// A word longer than the line is broken rather than left to overhang: error
// bodies carry things with no spaces in them — a URL, a base64 token, a
// serialized manifest — and a single unbreakable word pushed the whole modal
// off screen, which is how the error nobody could read got reported as broken
// alignment.
func wrapText(s string, w int) string {
	if w < 20 {
		w = 20
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		flush := func() {
			out = append(out, line)
			line = ""
		}
		for _, word := range strings.Fields(para) {
			// A word that cannot fit on a line of its own is split across as
			// many lines as it needs.
			for lipgloss.Width(word) > w {
				if line != "" {
					flush()
				}
				head, tail := splitCells(word, w)
				out = append(out, head)
				word = tail
			}
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
				line += " " + word
			default:
				flush()
				line = word
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// modalChrome is what a modal's border and padding cost: one border column and
// two padding columns on each side.
const modalChrome = 6

// modalContentWidth is the widest a modal body may be at the current terminal
// size, and never wider than max.
//
// Every modal goes through this rather than computing its own width: the
// border and padding are easy to forget, and a modal that forgets them renders
// wider than the terminal, which pushes the frame apart.
func (m *Model) modalContentWidth(max int) int {
	w := m.width - modalChrome - 4 // 4 = a two-column margin on each side
	if w > max {
		w = max
	}
	if w < 20 {
		w = 20
	}
	return w
}

// modalContentHeight is the tallest a modal body may be, leaving room for its
// border and a margin above and below.
func (m *Model) modalContentHeight() int {
	h := m.height - 4
	if h < 3 {
		h = 3
	}
	return h
}

// clampModalBody truncates each line to w and the whole body to h lines,
// dropping from the middle and marking what it dropped.
//
// A modal taller than the terminal is not merely ugly: lipgloss places it
// centered, so the overflow lands outside the frame and the lines that matter
// scroll off with no way to reach them.
//
// The last `keep` lines are preserved because they carry the modal's controls —
// "any key to dismiss", "y confirm · n cancel". Cutting from the end drops
// exactly the line that says how to close the thing the reader is stuck in.
func clampModalBody(lines []string, w, h int) []string {
	for i := range lines {
		lines[i] = truncate(lines[i], w)
	}
	if len(lines) <= h {
		return lines
	}

	// Two control lines and the blank above them.
	const keep = 3
	if h <= keep+1 {
		// Too short for both halves; the controls win — a body with no visible
		// exit is worse than a body with no visible content.
		return lines[len(lines)-h:]
	}

	head := h - keep - 1
	out := make([]string, 0, h)
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("… %d more lines", len(lines)-head-keep))
	out = append(out, lines[len(lines)-keep:]...)
	return out
}

// splitCells cuts a string at w display cells, returning both halves.
//
// Unlike truncate it loses nothing — the remainder is returned for the next
// line — and it steps by rune so a wide character is never split down the
// middle into two half-width cells.
func splitCells(s string, w int) (head, tail string) {
	used := 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w {
			return s[:i], s[i:]
		}
		used += rw
	}
	return s, ""
}
