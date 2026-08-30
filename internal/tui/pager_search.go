package tui

import (
	"fmt"
	"strings"
)

// Searching a manifest or a diff.
//
// A plain grep over a rendered manifest answers the wrong question. A line
// reading `image: nginx:1.25` tells you nothing about *which* container it
// belongs to, and the surrounding lines that would have told you are exactly
// what the grep removed. The search here keeps both: it labels each hit with
// the path that reaches it, and shows the lines around it.

// pagerHit is one search result.
type pagerHit struct {
	// line is the index into the unfiltered content.
	line int
	// path is the JSON path that reaches this line, e.g.
	// `spec.containers[1].image`. Empty when the content is not structured,
	// which is the case for logs and events.
	path string
}

// searchResult is the rendered view of a search: the lines to show, and where
// each hit landed within them.
type searchResult struct {
	lines []string
	// hitRows are indices into lines, so the cursor can step between matches
	// rather than through the context around them.
	hitRows []int
}

// jsonPaths computes the JSON path of every line in an indented JSON document.
//
// It reads the indentation rather than parsing, because the parsed value has
// lost the line numbers by the time it exists — and the rendering the reader is
// looking at is the indented text, so the text is what the paths must describe.
//
// Lines that are not part of a structure (a diff's `===` header, a `@@` marker)
// get an empty path, which is honest: they have no place in the document.
func jsonPaths(lines []string) []string {
	paths := make([]string, len(lines))

	// stack holds one entry per open brace or bracket: the key that opened it,
	// and the running index if it is an array.
	type frame struct {
		key     string
		isArray bool
		index   int
	}
	var stack []frame

	render := func(leaf string) string {
		var b strings.Builder
		for _, f := range stack {
			// An array frame contributes both the key that opened it and the
			// index within it: `containers` plus `[0]`. Writing only the index
			// loses the segment that says *which* list, which is most of what
			// the path is for.
			if f.key != "" {
				if b.Len() > 0 {
					b.WriteByte('.')
				}
				b.WriteString(f.key)
			}
			if f.isArray && f.index >= 0 {
				fmt.Fprintf(&b, "[%d]", f.index)
			}
		}
		if leaf != "" {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(leaf)
		}
		return b.String()
	}

	for i, raw := range lines {
		// A diff line carries its marker in column one; the structure below it
		// is the same either way.
		body := raw
		if len(body) > 0 && (body[0] == '+' || body[0] == '-' || body[0] == ' ') {
			body = body[1:]
		}
		trimmed := strings.TrimSpace(body)
		if trimmed == "" {
			continue
		}
		// A diff header or hunk marker is not part of the document.
		if strings.HasPrefix(trimmed, "===") || strings.HasPrefix(trimmed, "@@") {
			stack = stack[:0]
			continue
		}

		key, isOpen, closes := parseJSONLine(trimmed)

		// A close comes before the path is rendered: the line `}` belongs to
		// the frame it closes, not to the one inside it.
		for n := 0; n < closes; n++ {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}

		// An array element advances its frame's counter. The element itself is
		// whatever follows on the line, so the index is bumped before the path
		// is rendered.
		if len(stack) > 0 && stack[len(stack)-1].isArray && key == "" && !closes2(trimmed) {
			stack[len(stack)-1].index++
		}

		paths[i] = render(key)

		if isOpen {
			stack = append(stack, frame{
				key:     key,
				isArray: strings.HasSuffix(trimmed, "["),
				index:   -1,
			})
		}
	}
	return paths
}

// closes2 reports whether a line only closes a structure, so it does not count
// as a new array element.
func closes2(s string) bool {
	return s == "}" || s == "]" || s == "}," || s == "],"
}

// parseJSONLine pulls a line's key apart from its structure.
//
// Returns the key (empty for an array element or a bare brace), whether the
// line opens a new object or array, and how many it closes.
func parseJSONLine(s string) (key string, opens bool, closes int) {
	for _, c := range s {
		if c == '}' || c == ']' {
			closes++
			continue
		}
		break
	}
	// A line that only closes has no key.
	if closes > 0 && closes2(s) {
		return "", false, closes
	}

	if strings.HasPrefix(s, `"`) {
		if end := strings.Index(s[1:], `"`); end >= 0 {
			rest := s[end+2:]
			if strings.HasPrefix(rest, ":") {
				key = s[1 : end+1]
				s = strings.TrimSpace(rest[1:])
			}
		}
	}
	opens = strings.HasSuffix(s, "{") || strings.HasSuffix(s, "[")
	return key, opens, closes
}

// noiseKeys are the fields a manifest carries for Kubernetes' own bookkeeping.
//
// They are hidden by default because they are not what anyone opened a manifest
// to read, and because of their size: measured on a real pod, managedFields
// alone is 39% of the document — enough to bury every real match in a search.
//
// This is a display filter, not a claim that the fields are unimportant.
// Toggling them back on is one key, and the status line says when they are
// hidden so an absent field is never a mystery.
var noiseKeys = map[string]bool{
	"managedFields": true,
	// Written by apply, and a duplicate of the object it sits on.
	"kubectl.kubernetes.io/last-applied-configuration": true,
}

// hideNoise removes the bookkeeping fields from an indented JSON document,
// reporting how many lines it dropped.
//
// It works on the rendered text rather than the parsed value for the same
// reason jsonPaths does: the text is what the reader is looking at, and
// re-marshalling would reorder and reformat it.
func hideNoise(lines []string) (out []string, hidden int) {
	out = make([]string, 0, len(lines))

	// skipIndent is the indentation of the line that opened the hidden block,
	// or -1 when nothing is being skipped. Indentation rather than brace depth:
	// a diff shows a deleted block's opens and closes on the same side, so the
	// depth never balances and a depth-based skip ends on its first line.
	skipIndent := -1

	for _, raw := range lines {
		body, indent := diffBody(raw)
		t := strings.TrimSpace(body)

		if skipIndent >= 0 {
			// The block continues while its lines are indented further than
			// the key that opened it. A blank line inside it is part of it.
			if t == "" || indent > skipIndent {
				hidden++
				continue
			}
			// The closing bracket sits at the opening key's own indentation, so
			// it would otherwise survive as an orphan `],` with nothing above
			// it. It is the last line of the block being hidden.
			if indent == skipIndent && isCloser(t) {
				hidden++
				skipIndent = -1
				continue
			}
			skipIndent = -1
		}

		if t != "" {
			if key, _, _ := parseJSONLine(t); noiseKeys[key] {
				hidden++
				// A value that opens a block starts a skip; a single-line entry
				// closes on its own line and does not.
				if strings.HasSuffix(t, "{") || strings.HasSuffix(t, "[") {
					skipIndent = indent
				}
				continue
			}
		}

		out = append(out, raw)
	}
	return out, hidden
}

// isCloser reports whether a line is only a closing bracket, with or without
// the comma that follows it in a list.
func isCloser(t string) bool {
	switch t {
	case "}", "]", "},", "],":
		return true
	}
	return false
}

// diffBody strips a diff line's marker and reports the content's indentation.
//
// The marker occupies column one, so the indentation that follows is what
// describes the structure — measuring from the raw line would make every
// deleted line look one column deeper than its added counterpart.
func diffBody(raw string) (body string, indent int) {
	body = raw
	if len(body) > 0 && (body[0] == '+' || body[0] == '-' || body[0] == ' ') {
		body = body[1:]
	}
	for i, c := range body {
		if c != ' ' && c != '\t' {
			return body, i
		}
	}
	return body, len(body)
}

// searchPager runs the pager's search.
//
// With no query the content is returned whole. With one, each matching line is
// labelled with its path and surrounded by context — the two things a grep
// throws away and which are, for a manifest, most of the answer.
func (m *Model) searchPager() searchResult {
	src := m.pager
	if m.screen == screenHelp {
		src = m.helpLines()
	}
	// Structured content only: a log line beginning with "managedFields" is
	// text, not a field, and dropping it would be a wrong answer.
	if !m.showNoise && (m.screen == screenDiff || m.screen == screenManifest) {
		src, _ = hideNoise(src)
	}
	q := strings.TrimSpace(m.pagerFilt)
	if q == "" {
		return searchResult{lines: src}
	}

	// Logs and events are not structured, so paths would be noise; the context
	// is still worth keeping.
	structured := m.screen == screenDiff || m.screen == screenManifest
	var paths []string
	if structured {
		paths = jsonPaths(src)
	}

	lower := strings.ToLower(q)
	var hits []pagerHit
	for i, l := range src {
		if !strings.Contains(strings.ToLower(l), lower) {
			continue
		}
		h := pagerHit{line: i}
		if structured && i < len(paths) {
			h.path = paths[i]
		}
		hits = append(hits, h)
	}

	if len(hits) == 0 {
		return searchResult{}
	}
	return m.renderHits(src, hits, structured)
}

// pagerContext is how many lines are shown either side of a match.
//
// Two: enough to see the key a value belongs to and the one after it, without
// the result becoming the whole document again.
const pagerContext = 2

// renderHits assembles the search view.
//
// Overlapping context windows are merged rather than repeated: with two matches
// three lines apart, showing both windows in full would print the lines between
// them twice, and the second copy would sit under a label describing the first.
func (m *Model) renderHits(src []string, hits []pagerHit, structured bool) searchResult {
	var out searchResult
	// emitted is the highest source line already on screen, so nothing is
	// printed twice and a label never sits above lines it does not describe.
	emitted := -1

	for _, h := range hits {
		start := h.line - pagerContext
		if start < 0 {
			start = 0
		}
		end := h.line + pagerContext
		if end >= len(src) {
			end = len(src) - 1
		}

		if emitted >= 0 && start > emitted+1 {
			out.lines = append(out.lines, m.st.dim.Render("⋯"))
		}

		// The path heads every match, because it is the answer to "where is
		// this": a line reading `image: nginx:1.25` means nothing without it.
		// Emitted even when the hit's own line was already shown as another
		// match's context — that reader still needs to know which container.
		if structured && h.path != "" {
			out.lines = append(out.lines, m.st.accent.Render(h.path))
		}

		if start <= emitted {
			start = emitted + 1
		}
		for i := start; i <= end; i++ {
			if i == h.line {
				out.hitRows = append(out.hitRows, len(out.lines))
			}
			out.lines = append(out.lines, src[i])
		}
		if end > emitted {
			emitted = end
		}
	}
	return out
}

// pagerLines is what the pager renders.
func (m *Model) pagerLines() []string { return m.searchPager().lines }

// pagerHitRows are the rows the match-stepping keys jump between.
func (m *Model) pagerHitRows() []int { return m.searchPager().hitRows }

// noiseHidden is how many lines the display filter is currently removing, for
// the status line — an absent field should never be a mystery.
func (m *Model) noiseHidden() int {
	if m.showNoise || (m.screen != screenDiff && m.screen != screenManifest) {
		return 0
	}
	_, n := hideNoise(m.pager)
	return n
}
