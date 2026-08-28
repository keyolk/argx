package tui

// Cursor movement, filtering, and multi-select bookkeeping — the state the key
// handlers in update.go manipulate, kept apart from the dispatch itself.

import (
	"strings"

	"github.com/keyolk/argx/internal/argocd"
)

// ---- scrolling and filtering ----

func (m *Model) moveApp(d int) {
	m.appCur += d
	m.clampScroll()
}

func (m *Model) moveTree(d int) {
	m.treeCur += d
	m.clampScroll()
}

// clampScroll keeps cursors in range and the viewport following them. Selection
// never shifts the layout — only the window into the list moves.
func (m *Model) clampScroll() {
	h := m.bodyHeight()

	if m.appCur >= len(m.appRows) {
		m.appCur = len(m.appRows) - 1
	}
	if m.appCur < 0 {
		m.appCur = 0
	}
	if m.appCur < m.appTop {
		m.appTop = m.appCur
	}
	if m.appCur >= m.appTop+h {
		m.appTop = m.appCur - h + 1
	}
	if m.appTop < 0 {
		m.appTop = 0
	}

	if m.treeCur >= len(m.treeRows) {
		m.treeCur = len(m.treeRows) - 1
	}
	if m.treeCur < 0 {
		m.treeCur = 0
	}
	if m.treeCur < m.treeTop {
		m.treeTop = m.treeCur
	}
	if m.treeCur >= m.treeTop+h {
		m.treeTop = m.treeCur - h + 1
	}
	if m.treeTop < 0 {
		m.treeTop = 0
	}

	if m.histCur >= len(m.histRows()) {
		m.histCur = len(m.histRows()) - 1
	}
	if m.histCur < 0 {
		m.histCur = 0
	}
	if m.histCur < m.histTop {
		m.histTop = m.histCur
	}
	if m.histCur >= m.histTop+h {
		m.histTop = m.histCur - h + 1
	}
	if m.histTop < 0 {
		m.histTop = 0
	}

	if n := len(m.detailRows()); m.detailCur >= n {
		m.detailCur = n - 1
	}
	if m.detailCur < 0 {
		m.detailCur = 0
	}
}

// histRows is the application's deploy history, newest first — the order a
// human reads it in, and the reverse of how Argo CD stores it.
func (m *Model) histRows() []argocd.RevisionHistory {
	if m.app == nil {
		return nil
	}
	h := m.app.Status.History
	out := make([]argocd.RevisionHistory, len(h))
	for i, e := range h {
		out[len(h)-1-i] = e
	}
	return out
}

// currentHistory is the deployment under the cursor in the HISTORY tab.
func (m *Model) currentHistory() *argocd.RevisionHistory {
	rows := m.histRows()
	if m.histCur < 0 || m.histCur >= len(rows) {
		return nil
	}
	return &rows[m.histCur]
}

// applyAppFilter recomputes the visible application rows.
func (m *Model) applyAppFilter() {
	prev := ""
	if a := m.currentApp(); a != nil {
		prev = a.Key()
	}
	m.appRows = m.appRows[:0]
	for i := range m.apps {
		if matchApp(&m.apps[i], m.appFilter) {
			m.appRows = append(m.appRows, i)
		}
	}
	// Keep the cursor on the same application across a filter change when it
	// survived the filter; jumping to row 0 loses the user's place.
	m.appCur = 0
	if prev != "" {
		for r, i := range m.appRows {
			if m.apps[i].Key() == prev {
				m.appCur = r
				break
			}
		}
	}
	m.clampScroll()
}

// matchApp filters the application list.
//
// Terms are matched case-insensitively against name, project, destination, and
// status, with one prefixed field: `ctx:` narrows to a server. With several
// Argo CDs in one list, "show me only staging" is the first thing anyone asks
// for, and an unprefixed substring cannot express it — a context name shares
// words with cluster and project names.
func matchApp(a *argocd.Application, q string) bool {
	if q == "" {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{
		a.Name(),
		a.Spec.Project,
		a.Spec.Destination.Namespace,
		a.Spec.Destination.Cluster(),
		a.Status.Sync.Status,
		a.Status.Health.Status,
	}, " "))
	ctxName := strings.ToLower(a.Context)

	for _, term := range strings.Fields(strings.ToLower(q)) {
		if v, ok := cutPrefix(term, "ctx:", "context:", "c:"); ok {
			if v != "" && !strings.Contains(ctxName, v) {
				return false
			}
			continue
		}
		if !strings.Contains(hay, term) {
			return false
		}
	}
	return true
}

// cutPrefix returns the value after the first matching prefix.
func cutPrefix(s string, prefixes ...string) (string, bool) {
	for _, p := range prefixes {
		if v, ok := strings.CutPrefix(s, p); ok {
			return v, true
		}
	}
	return "", false
}

func (m *Model) applyTreeFilter() {
	prev := ""
	if n := m.currentNode(); n != nil {
		prev = n.UID
	}
	m.treeRows = m.treeRows[:0]
	for i := range m.tree {
		if m.treeFilt.match(m.tree[i].Node) {
			m.treeRows = append(m.treeRows, i)
		}
	}
	m.treeCur = 0
	if prev != "" {
		for r, i := range m.treeRows {
			if m.tree[i].Node.UID == prev {
				m.treeCur = r
				break
			}
		}
	}
	m.clampScroll()
}

// pagerLines is the pager content after its filter, which acts as a grep.
//
// Help is included so its scroll bound and line count come from the same place
// as every other pager view rather than a second, drifting copy.
func (m *Model) pagerLines() []string {
	src := m.pager
	if m.screen == screenHelp {
		src = m.helpLines()
	}
	if m.pagerFilt == "" {
		return src
	}
	q := strings.ToLower(m.pagerFilt)
	out := make([]string, 0, len(src))
	for _, l := range src {
		if strings.Contains(strings.ToLower(l), q) {
			out = append(out, l)
		}
	}
	return out
}

// ---- mark bookkeeping ----

// pruneAppMarks drops marks for applications that no longer exist, so a stale
// mark cannot silently widen a later sync.
func (m *Model) pruneAppMarks() {
	if len(m.appMarks) == 0 {
		return
	}
	live := make(map[string]bool, len(m.apps))
	for i := range m.apps {
		live[m.apps[i].Key()] = true
	}
	for k := range m.appMarks {
		if !live[k] {
			delete(m.appMarks, k)
		}
	}
}

func (m *Model) pruneTreeMarks() {
	if len(m.treeMarks) == 0 {
		return
	}
	live := make(map[string]bool, len(m.tree))
	for _, r := range m.tree {
		live[r.Node.UID] = true
	}
	for uid := range m.treeMarks {
		if !live[uid] {
			delete(m.treeMarks, uid)
		}
	}
}

func (m *Model) allFilteredAppsMarked() bool {
	if len(m.appRows) == 0 {
		return false
	}
	for _, i := range m.appRows {
		if !m.appMarks[m.apps[i].Key()] {
			return false
		}
	}
	return true
}

func (m *Model) allFilteredNodesMarked() bool {
	if len(m.treeRows) == 0 {
		return false
	}
	for _, i := range m.treeRows {
		if !m.treeMarks[m.tree[i].Node.UID] {
			return false
		}
	}
	return true
}

func (m *Model) showError(err error) {
	m.errMsg = err.Error()
	m.overlay = overlayError
}
