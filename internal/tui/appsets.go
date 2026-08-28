package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
)

// The ApplicationSet list answers a question the application list cannot: what
// generates all of these, and is any generator broken.
//
// A broken generator is invisible from the application side — the applications
// it would have produced simply do not exist, so there is no row to be red. The
// only place that failure surfaces is the ApplicationSet's own conditions.

// setRows is the filtered view of the loaded ApplicationSets.
func (m *Model) setRows() []int { return m.appsetRows }

// currentSet is the ApplicationSet under the cursor.
func (m *Model) currentSet() *argocd.ApplicationSet {
	if m.appsetCur < 0 || m.appsetCur >= len(m.appsetRows) {
		return nil
	}
	i := m.appsetRows[m.appsetCur]
	if i < 0 || i >= len(m.appsets) {
		return nil
	}
	return &m.appsets[i]
}

// applySetFilter recomputes the visible rows.
func (m *Model) applySetFilter() {
	prev := ""
	if s := m.currentSet(); s != nil {
		prev = s.Key()
	}
	m.appsetRows = m.appsetRows[:0]
	for i := range m.appsets {
		if matchSet(&m.appsets[i], m.appsetFilter) {
			m.appsetRows = append(m.appsetRows, i)
		}
	}
	m.appsetCur = 0
	if prev != "" {
		for r, i := range m.appsetRows {
			if m.appsets[i].Key() == prev {
				m.appsetCur = r
				break
			}
		}
	}
	m.clampSetScroll()
}

// matchSet filters the ApplicationSet list.
//
// The same field prefixes as the application list where they mean the same
// thing, plus `gen:` for the generator kind — which is the axis specific to
// this view, and the one people come here to slice by.
func matchSet(s *argocd.ApplicationSet, q string) bool {
	if strings.TrimSpace(q) == "" {
		return true
	}
	gens := strings.ToLower(strings.Join(s.GeneratorKinds(), " "))
	src, _ := s.Spec.Template.PrimarySource()

	hay := strings.ToLower(strings.Join([]string{
		s.Name(), s.Context, s.Project(), s.Namespace(),
		gens, src.RepoURL, src.Path, src.TargetRevision,
		s.Spec.Template.Spec.Destination.Namespace,
	}, " "))

	for _, term := range strings.Fields(strings.ToLower(q)) {
		negate := false
		if strings.HasPrefix(term, "-") && len(term) > 1 {
			negate, term = true, term[1:]
		}
		ok := setMatchTerm(s, gens, hay, term)
		if ok == negate {
			return false
		}
	}
	return true
}

func setMatchTerm(s *argocd.ApplicationSet, gens, hay, term string) bool {
	field, value, hasField := strings.Cut(term, ":")
	if !hasField || value == "" {
		return strings.Contains(hay, strings.TrimSuffix(term, ":"))
	}
	switch field {
	case "gen", "generator":
		return strings.Contains(gens, value)
	case "ctx", "context", "c":
		return strings.Contains(strings.ToLower(s.Context), value)
	case "proj", "project", "p":
		return strings.Contains(strings.ToLower(s.Project()), value)
	case "ns", "namespace", "n":
		return strings.Contains(strings.ToLower(s.Spec.Template.Spec.Destination.Namespace), value)
	case "label", "l", "labels":
		k, v, hasValue := strings.Cut(value, "=")
		t := labelTerm{key: k, value: v, hasValue: hasValue}
		return t.match(s.Metadata.Labels)
	case "status":
		// `status:error` is how you find the broken generators, which is the
		// main reason to open this list at all.
		if strings.HasPrefix("error", value) || strings.HasPrefix("degraded", value) {
			return s.Degraded()
		}
		if strings.HasPrefix("ok", value) || strings.HasPrefix("healthy", value) {
			return !s.Degraded()
		}
		return false
	default:
		return strings.Contains(hay, term)
	}
}

const appsetFilterHint = "name · gen:git · status:error · ctx: · proj: · ns: · label:"

// renderAppSets draws the ApplicationSet list.
func (m *Model) renderAppSets() string {
	h := m.bodyHeight()
	if len(m.appsetRows) == 0 {
		txt := "loading application sets…"
		if !m.loading {
			txt = "no application sets"
			if strings.TrimSpace(m.appsetFilter) != "" {
				txt = fmt.Sprintf("no application sets match %q", m.appsetFilter)
			}
			if len(m.fleetErrs) > 0 {
				txt = "no server answered — press E for the reason"
			}
		}
		return m.emptyBody(h, txt)
	}

	nameW, ctxW, genW, projW := m.setColumns()

	lines := make([]string, 0, h)
	head := padRight("   NAME", 3+nameW)
	if ctxW > 0 {
		head += " " + padRight("CONTEXT", ctxW)
	}
	head += " " + padRight("GENERATORS", genW)
	if projW > 0 {
		head += " " + padRight("PROJECT", projW)
	}
	head += " APPS"
	lines = append(lines, m.st.header.Render(truncate(head, m.width)))

	for r := m.appsetTop; r < len(m.appsetRows) && len(lines) < h; r++ {
		s := &m.appsets[m.appsetRows[r]]
		cur := r == m.appsetCur

		cursor := " "
		if cur {
			cursor = m.st.accent.Render(m.gl.cursor)
		}
		// A broken generator is the one thing this list exists to surface, so
		// it gets the status cell rather than being buried in a detail view.
		state := m.st.success.Render(m.gl.synced)
		if s.Degraded() {
			state = m.st.err.Render(m.gl.degraded)
		}

		nameStyle := lipgloss.NewStyle()
		switch {
		case cur:
			nameStyle = m.st.selected
		case s.Degraded():
			nameStyle = m.st.err
		}

		line := cursor + state + " " +
			nameStyle.Render(padRight(truncate(s.Name(), nameW), nameW))
		if ctxW > 0 {
			label := m.gl.prefix(m.gl.server) + s.Context
			line += " " + m.ctxStyle(s.Context).Render(padRight(truncate(label, ctxW), ctxW))
		}
		line += " " + m.st.info.Render(padRight(
			truncate(strings.Join(s.GeneratorKinds(), ","), genW), genW))
		if projW > 0 {
			proj := m.gl.prefix(m.gl.project) + s.Project()
			line += " " + m.st.dim.Render(padRight(truncate(proj, projW), projW))
		}
		line += " " + m.st.dim.Render(fmt.Sprint(len(s.Status.ApplicationStatus)))
		lines = append(lines, truncate(line, m.width))
	}
	return padBody(lines, h)
}

// setColumns splits the width. Generators is given a fixed, generous share
// because a nested one — `merge(clusters+git)` — is long and is the column the
// reader is scanning.
func (m *Model) setColumns() (name, ctx, gen, proj int) {
	avail := m.width - 3 - 6 // cursor+status+space, and the APPS count

	if m.multiServer() {
		for _, n := range m.fleet.Names() {
			if w := lipgloss.Width(n); w > ctx {
				ctx = w
			}
		}
		ctx += lipgloss.Width(m.gl.prefix(m.gl.server))
		if ctx > maxCtxCol {
			ctx = maxCtxCol
		}
		avail -= ctx + 1
	}

	gen = 28
	if gen > avail/3 {
		gen = avail / 3
	}
	avail -= gen + 1

	if avail < minNameCol+minProjCol+1 {
		if avail < 12 {
			avail = 12
		}
		return avail, ctx, gen, 0
	}
	proj = maxProjCol
	if proj > avail/4 {
		proj = avail / 4
	}
	name = avail - proj - 1
	if name > maxNameCol {
		name = maxNameCol
	}
	return name, ctx, gen, proj
}

// clampSetScroll keeps the cursor in range and the viewport following it.
func (m *Model) clampSetScroll() {
	h := m.bodyHeight()
	if m.appsetCur >= len(m.appsetRows) {
		m.appsetCur = len(m.appsetRows) - 1
	}
	if m.appsetCur < 0 {
		m.appsetCur = 0
	}
	if m.appsetCur < m.appsetTop {
		m.appsetTop = m.appsetCur
	}
	if m.appsetCur >= m.appsetTop+h {
		m.appsetTop = m.appsetCur - h + 1
	}
	if m.appsetTop < 0 {
		m.appsetTop = 0
	}
}

// handleAppSetsKey drives the ApplicationSet list.
func (m *Model) handleAppSetsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.appsetCur++
	case "k", "up":
		m.appsetCur--
	case "ctrl+d", "pgdown":
		m.appsetCur += m.bodyHeight() / 2
	case "ctrl+u", "pgup":
		m.appsetCur -= m.bodyHeight() / 2
	case "g", "home":
		m.appsetCur, m.appsetTop = 0, 0
	case "G", "end":
		m.appsetCur = len(m.appsetRows) - 1

	case "enter", "l", "right":
		// Drilling in means seeing the applications this set generated, which
		// is the application list with a filter it could not have expressed:
		// membership is recorded on the applications, not derivable from a name.
		if s := m.currentSet(); s != nil {
			return m.showGeneratedApps(s)
		}
	case "y":
		if s := m.currentSet(); s != nil {
			m.push(screenManifest)
			m.pager, m.pagerTitle = nil, "spec · "+s.Name()
			return m, m.loadSetSpecCmd(*s)
		}
	case "o":
		if s := m.currentSet(); s != nil {
			u, err := m.fleet.SetURL(s)
			if err != nil {
				m.showError(err)
				return m, nil
			}
			return m, m.openBrowserCmd([]string{u})
		}
	case "r", "ctrl+r":
		return m, m.loadAppSetsCmd()
	case "E":
		if len(m.fleetErrs) > 0 {
			m.showError(fleetErrorText(m.fleetErrs))
		}
	}
	m.clampSetScroll()
	return m, nil
}

// showGeneratedApps switches to the application list, filtered to what this
// ApplicationSet produced.
//
// Membership is not always recoverable. Argo CD records it in two places and
// neither is guaranteed: the controller's tracking label is only present if the
// template does not override metadata.labels, and `status.applicationStatus` is
// only populated when a progressive-sync strategy is configured. Measured
// against a real fleet of 89 sets and 2976 applications, both were empty
// throughout — the templates set their own labels, and no set uses a rollout
// strategy.
//
// So this tries each source in turn and, when none answers, says so rather than
// showing an unfiltered list that would read as "it generated everything".
func (m *Model) showGeneratedApps(s *argocd.ApplicationSet) (tea.Model, tea.Cmd) {
	if q, ok := m.generatedByQuery(s); ok {
		m.screen = screenApps
		m.appFilter = parseAppFilter(q)
		m.applyAppFilter()
		m.setToast(fmt.Sprintf("%d application(s) generated by %s", len(m.appRows), s.Name()))
		return m, nil
	}
	m.setToast(s.Name() + ": Argo CD does not record which applications it generated — " +
		"the template overrides labels and no progressive-sync strategy is set")
	return m, nil
}

// generatedByQuery builds a filter that selects an ApplicationSet's
// applications, reporting whether membership could be established at all.
func (m *Model) generatedByQuery(s *argocd.ApplicationSet) (string, bool) {
	// The controller's tracking label, when the template left it in place.
	for i := range m.apps {
		if m.apps[i].Metadata.Labels[appsetTrackingLabel] == s.Name() {
			return "label:" + appsetTrackingLabel + "=" + s.Name(), true
		}
	}
	// The status list, populated only under a progressive-sync strategy. One
	// name is enough to filter by when that is all there is, since the list is
	// an OR that argx's AND-only filter cannot express.
	if len(s.Status.ApplicationStatus) == 1 {
		return s.Status.ApplicationStatus[0].Application, true
	}
	return "", false
}

// appsetTrackingLabel is the label Argo CD's ApplicationSet controller puts on
// every application it generates.
const appsetTrackingLabel = "argocd.argoproj.io/application-set-name"
