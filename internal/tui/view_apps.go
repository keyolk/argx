package tui

// The application list and the resource tree — the two dense tables argx
// renders — plus their column sizing.

import (
	"fmt"

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
	nameW, projW, dstW, ctxW := m.appColumns()

	lines := make([]string, 0, h)
	// The header is assembled from the same widths as the rows, and dropped
	// columns take their labels with them — a label left behind when its column
	// is gone makes the header wider than the rows, and lipgloss then pads every
	// line to that width.
	head := padRight("  ST NAME", 3+3+nameW)
	if ctxW > 0 {
		head += " " + padRight("CONTEXT", ctxW)
	}
	if projW > 0 {
		head += " " + padRight("PROJECT", projW)
	}
	if dstW > 0 {
		head += " " + padRight("DESTINATION", dstW)
	}
	head += " REVISION"
	lines = append(lines, m.st.header.Render(truncate(head, m.width)))

	for r := m.appTop; r < len(m.appRows) && len(lines) < h; r++ {
		a := &m.apps[m.appRows[r]]
		cur := r == m.appCur

		mark := " "
		if m.appMarks[a.Key()] {
			mark = m.st.mark.Render(m.gl.marked)
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
		name := nameStyle.Render(padRight(truncate(a.Name(), nameW), nameW))

		rev := m.revisionCell(a)

		dst := m.gl.prefix(m.gl.cluster) + a.Spec.Destination.Cluster()
		if ns := a.Spec.Destination.Namespace; ns != "" {
			dst += "/" + ns
		}

		line := cursor + mark + " " + sync + health + " " + name
		if ctxW > 0 {
			// The server is colored, not just named: at a glance the reader
			// should see that a run of rows belongs to one Argo CD without
			// reading the column.
			ctxLabel := m.gl.prefix(m.gl.server) + a.Context
			line += " " + m.ctxStyle(a.Context).Render(
				padRight(truncate(ctxLabel, ctxW), ctxW))
		}
		if projW > 0 {
			proj := m.gl.prefix(m.gl.project) + a.Spec.Project
			line += " " + m.st.dim.Render(padRight(truncate(proj, projW), projW))
		}
		if dstW > 0 {
			line += " " + m.st.dim.Render(padRight(truncate(dst, dstW), dstW))
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
)

// appColumns splits the width between name, project, and destination, keeping
// the name column dominant because it is what people scan.
func (m *Model) appColumns() (name, proj, dst, ctx int) {
	// 3 for cursor+mark+space, 3 for status letters + space, revCol for the
	// revision, and one separator space before each of ctx/proj/dst/rev.
	avail := m.width - 3 - 3 - revCol - 1

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

	if avail < minNameCol+minProjCol+minDstCol+2 {
		// Narrow terminal — a 60-column tmux split lands here. Give the whole
		// budget to the name, keeping the context column: which server a row
		// belongs to outranks its project on a narrow screen.
		if avail < 12 {
			avail = 12
		}
		return avail, 0, 0, ctx
	}
	// Two separator spaces, before PROJECT and before DESTINATION.
	avail -= 2

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
	proj = maxProjCol
	if proj > avail/5 {
		proj = avail / 5
	}
	rest := avail - proj

	name = rest * 40 / 100
	if name > maxNameCol {
		// Past a point extra name width is wasted; give it back to the
		// destination, which is the column that stays truncated longest.
		name = maxNameCol
	}
	dst = rest - name
	return name, proj, dst, ctx
}

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
