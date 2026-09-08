package tui

// The application list and the resource tree — the two dense tables argx
// renders — plus their column sizing.

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
)

func (m *Model) renderApps() string {
	h := m.bodyHeight()
	if len(m.appRows) == 0 {
		return m.emptyBody(h, m.emptyAppsText())
	}

	// Column widths are computed from the terminal width every frame, so a
	// resize re-lays out rather than truncating against a stale width.
	c := m.appColumns()
	// One clock reading for the whole frame, so two rows of the same age can
	// never render as different ones.
	now := time.Now()

	lines := make([]string, 0, h)
	// The header is assembled from the same widths as the rows, and dropped
	// columns take their labels with them — a label left behind when its column
	// is gone makes the header wider than the rows, and lipgloss then pads every
	// line to that width.
	head := padRight("  ST NAME", 3+3+c.name)
	if c.ctx > 0 {
		head += " " + padRight("CONTEXT", c.ctx)
	}
	if c.proj > 0 {
		head += " " + padRight("PROJECT", c.proj)
	}
	if c.dst > 0 {
		head += " " + padRight("DESTINATION", c.dst)
	}
	if c.synced > 0 {
		head += " " + padRight("SYNCED", c.synced)
	}
	head += " REVISION"
	lines = append(lines, m.st.header.Render(truncate(head, m.width)))

	for r := m.appTop; r < len(m.appRows) && len(lines) < h; r++ {
		a := &m.apps[m.appRows[r]]
		cur := r == m.appCur

		// A row inside an in-progress range is drawn as marked before it is
		// marked, so the reader sees what v will take rather than committing
		// blind. It is dimmed to keep the distinction: pending, not done.
		mark := " "
		switch {
		case m.appMarks[a.Key()]:
			mark = m.st.mark.Render(m.gl.marked)
		case inVisualRange(m, r):
			mark = m.st.dim.Render(m.gl.marked)
		}
		cursor := " "
		if cur {
			cursor = m.st.accent.Render(m.gl.cursor)
		}

		sync := m.st.syncStyle(a.Status.Sync.Status).Render(m.gl.syncGlyph(a.Status.Sync.Status))
		health := m.st.healthStyle(a.Status.Health.Status).Render(m.gl.healthGlyph(a.Status.Health.Status))

		nameStyle := m.st.dim
		switch {
		case cur:
			nameStyle = m.st.selected
		case a.Degraded():
			nameStyle = m.st.err
		default:
			nameStyle = lipgloss.NewStyle()
		}
		name := nameStyle.Render(padRight(truncate(a.Name(), c.name), c.name))

		rev := m.revisionCell(a)

		dst := m.gl.prefix(m.gl.cluster) + a.Spec.Destination.Cluster()
		if ns := a.Spec.Destination.Namespace; ns != "" {
			dst += "/" + ns
		}

		line := cursor + mark + " " + sync + health + " " + name
		if c.ctx > 0 {
			// The server is colored, not just named: at a glance the reader
			// should see that a run of rows belongs to one Argo CD without
			// reading the column.
			ctxLabel := m.gl.prefix(m.gl.server) + a.Context
			line += " " + m.ctxStyle(a.Context).Render(
				padRight(truncate(ctxLabel, c.ctx), c.ctx))
		}
		if c.proj > 0 {
			proj := m.gl.prefix(m.gl.project) + a.Spec.Project
			line += " " + m.st.dim.Render(padRight(truncate(proj, c.proj), c.proj))
		}
		if c.dst > 0 {
			line += " " + m.st.dim.Render(padRight(truncate(dst, c.dst), c.dst))
		}
		if c.synced > 0 {
			// Right-aligned, so the magnitudes line up: a column of ages is
			// read by comparing them, and "4d" under "13h" only compares if
			// the units share an edge.
			line += " " + m.syncedCell(a, now, c.synced)
		}
		// The revision is truncated to its own column rather than left to the
		// row-level cut: a cell that overruns pushes the row past the terminal
		// and the wrap that follows breaks every column's alignment.
		// Already styled by revisionCell, so it is truncated but not re-styled:
		// wrapping a string that carries its own escape sequences nests them
		// and loses the inner colors after the first reset.
		line += " " + truncate(rev, revCol)
		lines = append(lines, truncate(line, m.width))
	}
	return padBody(lines, h)
}

// revisionCell renders what an application is deployed at.
//
// Three shapes, because Argo CD reports three different things there:
//
//	32b6f40 ⎇ main     a git SHA and the branch or tag it tracks
//	1.21.1             a chart version — the target repeats it, so it is dropped
//	(2 sources)        a multi-source app, whose sync revision is empty
//
// The old rendering printed a commit icon with nothing after it for the
// multi-source case, and printed "1.21.1 🏷 1.21.1" for charts.
func (m *Model) revisionCell(a *argocd.Application) string {
	src, nsrc := a.PrimarySource()
	synced := a.Status.Sync.Revision

	if nsrc > 1 {
		// Multi-source applications carry their revisions per source, not in
		// the single field, so there is nothing to abbreviate here.
		if synced == "" {
			return m.st.dim.Render(fmt.Sprintf("(%d sources)", nsrc))
		}
		return m.gl.prefix(m.gl.revision) + shortRev(synced) +
			m.st.dim.Render(fmt.Sprintf(" (%d srcs)", nsrc))
	}

	if synced == "" {
		return m.st.dim.Render("—")
	}
	cell := m.gl.prefix(m.gl.revision) + shortRev(synced)

	target := src.TargetRevision
	switch {
	case target == "", target == "HEAD":
		// HEAD says nothing the synced revision does not already say.
	case target == synced:
		// A chart version is both the target and what is deployed; printing it
		// twice is noise.
	default:
		marker := " @"
		if m.gl.branchRef != "" {
			marker = " " + m.gl.refIcon(target) + " "
		}
		cell += marker + truncate(target, 14)
	}
	return cell
}

// Minimum useful widths for the application list's columns. Below the sum of
// these the extra columns are dropped rather than squeezed: a 6-cell project
// column shows nothing but an ellipsis, which costs width and tells the reader
// less than an empty column would.
const (
	minNameCol = 24
	minProjCol = 12
	minDstCol  = 16
	// maxCtxCol bounds the server column so one long context name cannot
	// squeeze out everything else.
	maxCtxCol = 22
	// maxNameCol and maxProjCol cap columns whose content stops growing.
	// Measured over a real fleet: names reach p99 = 52, projects p99 = 12.
	maxNameCol = 52
	maxProjCol = 14
	// revCol is the width reserved for the revision, the last column.
	//
	// It holds what shortRev actually produces, not what a bare SHA needs: a
	// 7-char SHA plus " @" and a target revision. Branch names run long, so
	// this is sized for a 7-char SHA and a 21-cell target — past that the
	// target is truncated inside its own cell, which is a readable ellipsis
	// rather than a row that overruns the terminal.
	//
	// Budgeting 13 for it — the original value — made every row overflow by the
	// difference, and the row-level truncate then cut the revision off, which
	// read as broken alignment.
	revCol = 30
	// syncedCol is the width of the age column. It is sized to the widest thing
	// the column ever holds, which is not an age: humanSince tops out at five
	// cells ("999mo"), but a sync in flight renders the word "syncing". A
	// column narrower than its own contents does not truncate them — padLeft
	// returns an over-long cell unchanged — it pushes the revision one cell
	// right on that row alone, which reads as the alignment being broken by
	// whichever application happens to be syncing.
	syncedCol = 7
)

// appCols is the width of each column in the application list. Zero means the
// column is dropped at this terminal width.
//
// A struct rather than four unnamed ints: the call site assigns them
// positionally, and adding a fifth column to a positional return is how a width
// silently lands in the wrong column.
type appCols struct {
	name   int
	proj   int
	dst    int
	ctx    int
	synced int
}

// appColumns splits the width between name, project, and destination, keeping
// the name column dominant because it is what people scan.
func (m *Model) appColumns() (c appCols) {
	// 3 for cursor+mark+space, 3 for status letters + space, revCol for the
	// revision, and one separator space before each of ctx/proj/dst/synced/rev.
	avail := m.width - 3 - 3 - revCol - 1
	ctx := 0

	if m.multiServer() {
		// The context column is sized to the longest context name and taken off
		// the top, because it is the column that must never be truncated: a
		// server name cut to "argocd." tells the reader nothing, and it is the
		// one field that decides which cluster an action reaches.
		for _, n := range m.fleet.Names() {
			if w := lipgloss.Width(n); w > ctx {
				ctx = w
			}
		}
		// The icon and its space are part of the cell's content, so the column
		// has to be that much wider or the name it decorates gets truncated.
		ctx += lipgloss.Width(m.gl.prefix(m.gl.server))
		if ctx > maxCtxCol {
			ctx = maxCtxCol
		}
		avail -= ctx + 1
	}
	c.ctx = ctx

	if avail < minNameCol+minProjCol+minDstCol+syncedCol+3 {
		// Narrow terminal — a 60-column tmux split lands here. Give the whole
		// budget to the name, keeping the context column: which server a row
		// belongs to outranks its project on a narrow screen. The age goes with
		// the rest; at this width the name is already fighting for cells, and
		// seven of them spent on an age the DETAILS tab spells out in full is
		// a worse trade than a readable name.
		if avail < 12 {
			avail = 12
		}
		c.name = avail
		return c
	}
	// Three separator spaces, before PROJECT, DESTINATION, and SYNCED.
	avail -= 3
	// The age is taken off the top rather than shared out. It is a fixed width
	// that never grows, and it is the column that answers "has anything
	// touched this lately" — the question the reader came with when the list is
	// long enough that they cannot open every row.
	c.synced = syncedCol
	avail -= syncedCol

	// The split is sized from what these fields actually hold, measured across
	// a real fleet of ~3000 applications:
	//
	//   project      p99 = 12    (almost always "default")
	//   name         p90 = 36, p99 = 52
	//   destination  p75 = 84, p99 = 104  — cluster/namespace, far the longest
	//
	// So project is capped rather than given a percentage: a proportional share
	// spent 20% of the row on a column whose contents are seven characters,
	// and starved the destination that needed it.
	c.proj = maxProjCol
	if c.proj > avail/5 {
		c.proj = avail / 5
	}
	rest := avail - c.proj

	c.name = rest * 40 / 100
	if c.name > maxNameCol {
		// Past a point extra name width is wasted; give it back to the
		// destination, which is the column that stays truncated longest.
		c.name = maxNameCol
	}
	c.dst = rest - c.name
	return c
}

// syncedCell renders how long ago the application last synced, right-aligned in
// w cells.
//
// The age is what the column carries rather than a timestamp: a list is read by
// comparing rows, and "13h" against "21d" compares at a glance where two
// timestamps do not. The exact moment and the person who asked for it are in
// DETAILS, one keypress away.
//
// It is colored by staleness, not by sync status — that is what the two glyphs
// at the start of the row already say. What this column adds is drift: an
// application nothing has synced in weeks is worth noticing even when it is
// green, because it means what is running was decided a long time ago.
func (m *Model) syncedCell(a *argocd.Application, now time.Time, w int) string {
	when, _, ok := a.LastSync()
	if !ok {
		return m.st.dim.Render(padLeft("—", w))
	}

	// A sync in flight has no age worth reporting — it is happening now — and
	// saying "0s" invites the reader to refresh until it changes.
	if op := a.Status.OperationState; op != nil && op.Running() {
		return m.st.warn.Render(padLeft("syncing", w))
	}

	d := now.Sub(when)
	text := padLeft(humanSince(d), w)
	switch {
	case d >= staleAge:
		return m.st.warn.Render(text)
	case d < freshAge:
		return m.st.success.Render(text)
	default:
		return m.st.dim.Render(text)
	}
}

// Thresholds for the age column's coloring.
//
// An hour is what "just now" means for a deployment: long enough to cover a
// sync the reader kicked off before switching windows, short enough that it
// still means something happened this session.
//
// Thirty days is where an application stops being one nobody has needed to
// change and becomes one nobody has looked at. Below it the age is dim, because
// a healthy application syncing on its own schedule is not news.
const (
	freshAge = time.Hour
	staleAge = 30 * 24 * time.Hour
)

func (m *Model) emptyAppsText() string {
	if m.loading {
		return "loading applications…"
	}
	if !m.appFilter.empty() {
		return fmt.Sprintf("no applications match %q", m.appFilter.String())
	}
	if len(m.fleetErrs) > 0 {
		// Every server failed. Saying "no applications" here would report an
		// outage as an empty result.
		return "no server answered — see the error above"
	}
	return "no applications visible to this session"
}
