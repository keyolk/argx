package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
	"github.com/keyolk/argx/internal/config"
)

// fleetModel builds a model spanning several Argo CD servers. The applications
// are stamped as the fleet would stamp them on arrival.
func fleetModel(t *testing.T, apps map[string][]string) *Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NO_COLOR", "1")

	// Fleet order is the order the contexts are given, and the tests depend on
	// it for column and grouping assertions, so build it deterministically.
	var names []string
	for _, n := range []string{"sb-prod", "sb-kb", "dl-prod"} {
		if _, ok := apps[n]; ok {
			names = append(names, n)
		}
	}

	ctxs := make([]config.Context, 0, len(names))
	for _, n := range names {
		ctxs = append(ctxs, config.Context{Name: n, Server: n + ".example.com", Token: "t"})
	}
	fleet := argocd.NewFleet(ctxs)

	m := New(context.Background(), fleet, &config.Config{})
	for _, n := range names {
		for _, appName := range apps[n] {
			var a argocd.Application
			a.Context = n
			a.Metadata.Name = appName
			a.Metadata.Namespace = "argocd"
			a.Spec.Project = "default"
			a.Spec.Destination = argocd.Destination{Name: "cluster-" + n, Namespace: "ns"}
			a.Status.Sync.Status = "Synced"
			a.Status.Health.Status = "Healthy"
			m.apps = append(m.apps, a)
		}
	}
	m.applyAppFilter()
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 30})
	return m
}

// ---- marks must not cross servers ----

// Two servers can host an application with the same name. A mark on one must
// never select the other — this is the single most dangerous confusion a
// merged list can produce.
func TestMarksDoNotLeakAcrossServers(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"web"},
	})
	if len(m.apps) != 2 {
		t.Fatalf("expected both copies of web, got %d", len(m.apps))
	}

	press(t, m, " ") // mark the first row
	if len(m.appMarks) != 1 {
		t.Fatalf("marks = %d, want 1", len(m.appMarks))
	}

	targets := m.markedApps()
	if len(targets) != 1 {
		t.Fatalf("markedApps() returned %d applications — a mark selected both copies", len(targets))
	}
	if targets[0].Context != m.apps[0].Context {
		t.Errorf("the mark selected %q, want %q", targets[0].Context, m.apps[0].Context)
	}
}

// A mark survives a refresh only if the same application on the same server is
// still present.
func TestMarkPruningIsPerServer(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"web"},
	})
	for _, a := range m.apps {
		m.appMarks[a.Key()] = true
	}

	// dl-prod's copy disappears; sb-prod's remains.
	var kept argocd.Application
	kept.Context = "sb-prod"
	kept.Metadata.Name = "web"
	m.Update(appsMsg{apps: []argocd.Application{kept}})

	if len(m.appMarks) != 1 {
		t.Fatalf("marks = %d, want only the surviving one", len(m.appMarks))
	}
	if !m.appMarks["sb-prod/web"] {
		t.Errorf("the surviving mark is %v, want sb-prod/web", m.appMarks)
	}
}

// ---- routing ----

// Each application's browser URL must point at its own server.
func TestBrowserURLsFollowTheOwningServer(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"web"},
	})

	for i := range m.apps {
		a := &m.apps[i]
		got := m.appURL(a)
		if !strings.Contains(got, a.Context+".example.com") {
			t.Errorf("%s/%s → %s, want its own server", a.Context, a.Name(), got)
		}
	}
}

// An application whose context is not in the fleet must not resolve to a
// client: falling back to the first server would act on a cluster the user
// never chose.
func TestUnknownContextDoesNotResolve(t *testing.T) {
	m := fleetModel(t, map[string][]string{"sb-prod": {"web"}})

	var stray argocd.Application
	stray.Context = "nowhere"
	stray.Metadata.Name = "web"

	if _, err := m.client(&stray); err == nil {
		t.Fatal("an unknown context must not resolve to a client")
	}
	if got := m.appURL(&stray); got != "" {
		t.Errorf("appURL for an unknown context = %q, want empty", got)
	}
}

// ---- context filter ----

func TestContextFilterNarrowsToOneServer(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web", "api"},
		"dl-prod": {"web"},
	})

	for _, q := range []string{"ctx:dl", "context:dl-prod", "c:dl"} {
		m.appFilter = parseAppFilter(q)
		m.applyAppFilter()
		if len(m.appRows) != 1 {
			t.Fatalf("%q matched %d rows, want 1", q, len(m.appRows))
		}
		if got := m.apps[m.appRows[0]].Context; got != "dl-prod" {
			t.Errorf("%q selected %q, want dl-prod", q, got)
		}
	}
}

// The context term ANDs with the rest, like every other filter in argx.
func TestContextFilterCombinesWithOtherTerms(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web", "api"},
		"dl-prod": {"web"},
	})

	m.appFilter = parseAppFilter("ctx:sb api")
	m.applyAppFilter()
	if len(m.appRows) != 1 {
		t.Fatalf("matched %d rows, want just sb-prod/api", len(m.appRows))
	}
	got := m.apps[m.appRows[0]]
	if got.Context != "sb-prod" || got.Name() != "api" {
		t.Errorf("selected %s/%s, want sb-prod/api", got.Context, got.Name())
	}
}

// "mark all" is scoped to the filter, so ctx: plus `a` marks exactly one
// server — the safe way to act on a whole Argo CD.
func TestMarkAllRespectsTheContextFilter(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web", "api"},
		"dl-prod": {"web"},
	})
	m.appFilter = parseAppFilter("ctx:sb-prod")
	m.applyAppFilter()

	press(t, m, "a")
	if len(m.appMarks) != 2 {
		t.Fatalf("marks = %d, want the two sb-prod applications", len(m.appMarks))
	}
	for _, a := range m.markedApps() {
		if a.Context != "sb-prod" {
			t.Errorf("marked %s/%s — the filter should have excluded it", a.Context, a.Name())
		}
	}
}

// ---- cross-server confirmation ----

// A flat list of names hides that some are on a different Argo CD. A sync that
// spans servers must say so before it is confirmed.
func TestCrossServerSyncPromptGroupsByServer(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"worker"},
	})
	press(t, m, "a") // mark everything
	press(t, m, "s") // sync options
	press(t, m, "enter")

	if m.overlay != overlayConfirm {
		t.Fatalf("overlay = %v, want the confirmation", m.overlay)
	}
	body := strings.Join(m.confirm.body, "\n")
	if !strings.Contains(body, "spans 2 Argo CD servers") {
		t.Errorf("the prompt must say the action crosses servers:\n%s", body)
	}
	for _, want := range []string{"sb-prod", "dl-prod", "web", "worker"} {
		if !strings.Contains(body, want) {
			t.Errorf("the prompt is missing %q:\n%s", want, body)
		}
	}
}

// A single-server action keeps the plain list — the grouping is a warning, and
// warning on every sync would train people to ignore it.
func TestSingleServerSyncPromptStaysFlat(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web", "api"},
		"dl-prod": {"worker"},
	})
	m.appFilter = parseAppFilter("ctx:sb-prod")
	m.applyAppFilter()
	press(t, m, "a", "s", "enter")

	body := strings.Join(m.confirm.body, "\n")
	if strings.Contains(body, "spans") {
		t.Errorf("a single-server sync should not carry the cross-server warning:\n%s", body)
	}
	if !strings.Contains(body, "web") || !strings.Contains(body, "api") {
		t.Errorf("the prompt should still list its targets:\n%s", body)
	}
}

// ---- partial failure ----

// A server that failed must be named, and the applications that did arrive must
// still be shown.
func TestPartialFailureIsReportedNotFatal(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"worker"},
	})

	var kept argocd.Application
	kept.Context = "sb-prod"
	kept.Metadata.Name = "web"
	m.Update(appsMsg{
		apps: []argocd.Application{kept},
		errs: []argocd.FleetError{{Context: "dl-prod", Err: errString("session expired")}},
	})

	if m.overlay != overlayNone {
		t.Error("a partial failure must not block the view with a modal — auto-refresh would become unusable")
	}
	out := m.View()
	if !strings.Contains(out, "unreachable") || !strings.Contains(out, "dl-prod") {
		t.Errorf("the status line must name the unreachable server:\n%s", out)
	}
	if !strings.Contains(out, "web") {
		t.Errorf("the applications that did arrive must still be listed:\n%s", out)
	}
}

// When nothing answered, that is worth interrupting for: an empty list would
// otherwise read as "you have no applications".
func TestTotalFailureSurfaces(t *testing.T) {
	m := fleetModel(t, map[string][]string{"sb-prod": {"web"}, "dl-prod": {"worker"}})

	m.Update(appsMsg{errs: []argocd.FleetError{
		{Context: "sb-prod", Err: errString("session expired")},
		{Context: "dl-prod", Err: errString("connection refused")},
	}})

	if m.overlay != overlayError {
		t.Fatalf("overlay = %v, want the error modal", m.overlay)
	}
	for _, want := range []string{"sb-prod", "dl-prod", "session expired", "connection refused"} {
		if !strings.Contains(m.errMsg, want) {
			t.Errorf("the error is missing %q:\n%s", want, m.errMsg)
		}
	}
}

// The failure details must stay reachable after the status line mentions them.
func TestFailureDetailsAreReachable(t *testing.T) {
	m := fleetModel(t, map[string][]string{"sb-prod": {"web"}, "dl-prod": {"worker"}})

	var kept argocd.Application
	kept.Context = "sb-prod"
	kept.Metadata.Name = "web"
	m.Update(appsMsg{
		apps: []argocd.Application{kept},
		errs: []argocd.FleetError{{Context: "dl-prod", Err: errString("session expired")}},
	})

	press(t, m, "E")
	if m.overlay != overlayError {
		t.Fatalf("E should show the failures, overlay = %v", m.overlay)
	}
	if !strings.Contains(m.errMsg, "session expired") {
		t.Errorf("the failure reason is lost: %q", m.errMsg)
	}
}

// ---- rendering ----

// The server column must be present, and never truncated: a name cut to
// "argocd." tells the reader nothing, and it is the field that decides which
// cluster an action reaches.
func TestContextColumnIsShownInFull(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"worker"},
	})

	out := m.View()
	if !strings.Contains(out, "CONTEXT") {
		t.Errorf("the list should have a CONTEXT column:\n%s", out)
	}
	for _, n := range []string{"sb-prod", "dl-prod"} {
		if !strings.Contains(out, n) {
			t.Errorf("the context %q is missing or truncated:\n%s", n, out)
		}
	}
}

// One server means no context column: spending width on a constant helps
// nobody.
func TestContextColumnIsAbsentForOneServer(t *testing.T) {
	m := fleetModel(t, map[string][]string{"sb-prod": {"web"}})

	if _, _, _, ctxW := m.appColumns(); ctxW != 0 {
		t.Errorf("a single-server session reserved %d cells for the context column", ctxW)
	}
	if strings.Contains(m.View(), "CONTEXT") {
		t.Errorf("a single-server session should not show a CONTEXT column:\n%s", m.View())
	}
}

// On a narrow terminal the context column survives while project and
// destination are dropped: which server a row belongs to outranks its project.
func TestNarrowLayoutKeepsTheContextColumn(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"worker"},
	})
	m.Update(tea.WindowSizeMsg{Width: 72, Height: 20})

	name, proj, dst, ctxW := m.appColumns()
	if ctxW == 0 {
		t.Error("the context column must survive a narrow terminal")
	}
	if proj != 0 || dst != 0 {
		t.Errorf("project/destination should be dropped first, got %d/%d", proj, dst)
	}
	if name < 12 {
		t.Errorf("the name column should stay usable, got %d", name)
	}
}

// Multi-server rows must still fit every supported width.
func TestFleetRowsFitTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{130, 30}, {100, 24}, {80, 24}, {60, 14}} {
		w, h := size[0], size[1]
		m := fleetModel(t, map[string][]string{
			"sb-prod": {"web", "api"},
			"sb-kb":   {"knowledge-base"},
			"dl-prod": {"worker"},
		})
		m.Update(tea.WindowSizeMsg{Width: w, Height: h})

		for i, line := range strings.Split(m.View(), "\n") {
			if got := lipglossWidth(line); got > w {
				t.Errorf("%dx%d line %d is %d cells, want at most %d:\n%q",
					w, h, i, got, w, line)
			}
		}
	}
}

// The header reports the fleet rather than a single server name, and says so
// when part of it is unreachable.
func TestHeaderReportsFleetHealth(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"worker"},
	})
	if !strings.Contains(m.renderHeader(), "2 servers") {
		t.Errorf("the header should report the fleet size: %q", m.renderHeader())
	}

	m.fleetErrs = []argocd.FleetError{{Context: "dl-prod", Err: errString("down")}}
	if got := m.renderHeader(); !strings.Contains(got, "1/2 servers") {
		t.Errorf("the header should report how many answered: %q", got)
	}
}

// Inside an application, the header must name the server the spec edits will
// land on.
func TestApplicationHeaderNamesItsServer(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"web"},
	})
	m.app = &m.apps[1] // the dl-prod copy
	m.screen = screenApp

	if got := m.renderHeader(); !strings.Contains(got, "dl-prod") {
		t.Errorf("the application header must name its server: %q", got)
	}
}

// Servers are told apart by color as well as by name, and each gets a distinct
// one — two servers rendering alike defeats the point.
func TestServersGetDistinctColors(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"sb-kb":   {"kb"},
		"dl-prod": {"worker"},
	})

	seen := map[string]bool{}
	for _, n := range m.fleet.Names() {
		key := fmt.Sprintf("%v", m.ctxStyle(n).GetForeground())
		if seen[key] {
			t.Errorf("%q reuses a color already assigned to another server", n)
		}
		seen[key] = true
	}

	// A context outside the fleet must not be tinted as though it were one.
	if m.ctxStyle("nowhere").GetForeground() != m.st.dim.GetForeground() {
		t.Error("an unknown context should fall back to the dim style")
	}
}

// Every column must start at the same cell on every row, and on the header.
// This is the failure the width budget exists to prevent: a row that overruns
// the terminal wraps, and the wrap shifts every column below it.
func TestColumnsLineUpWithTheHeader(t *testing.T) {
	for _, w := range []int{168, 130, 110, 100, 90, 80, 72} {
		m := fleetModel(t, map[string][]string{
			"sb-prod": {"addons-kube-audit-rest-dataplatform-apne1-airflow-local-dev", "api"},
			"sb-kb":   {"knowledge-base"},
			"dl-prod": {"addons-kube-audit-rest-apne2-prod-cluster-xyzw"},
		})
		// Real-shaped values: long destinations and a revision with a target.
		for i := range m.apps {
			m.apps[i].Spec.Destination = argocd.Destination{
				Name:      "prod-apne2-cluster-oqmz",
				Namespace: "kube-audit-rest",
			}
			m.apps[i].Status.Sync.Revision = "0123456789abcdef0123456789abcdef01234567"
			m.apps[i].Spec.Source = &argocd.Source{TargetRevision: "release-1.2.3-hotfix"}
		}
		m.applyAppFilter()
		m.Update(tea.WindowSizeMsg{Width: w, Height: 20})

		nameW, projW, dstW, ctxW := m.appColumns()
		// Where each column starts: cursor+mark+space, status letters+space.
		wantCtx := 3 + 3 + nameW + 1
		wantProj := wantCtx + ctxW + 1
		wantDst := wantProj + projW + 1

		lines := strings.Split(m.View(), "\n")
		header := lines[1]

		if ctxW > 0 {
			if got := lipglossWidth(header[:strings.Index(header, "CONTEXT")]); got != wantCtx {
				t.Errorf("w=%d: CONTEXT header starts at %d, want %d", w, got, wantCtx)
			}
		}
		if projW > 0 {
			if got := lipglossWidth(header[:strings.Index(header, "PROJECT")]); got != wantProj {
				t.Errorf("w=%d: PROJECT header starts at %d, want %d", w, got, wantProj)
			}
		}
		if dstW > 0 {
			if got := lipglossWidth(header[:strings.Index(header, "DESTINATION")]); got != wantDst {
				t.Errorf("w=%d: DESTINATION header starts at %d, want %d", w, got, wantDst)
			}
		}

		// And every data row must agree with those positions.
		for r := 0; r < len(m.appRows); r++ {
			line := lines[2+r]
			if ctxW > 0 {
				i := strings.Index(line, m.apps[m.appRows[r]].Context)
				if i < 0 {
					t.Errorf("w=%d row %d: the context is missing from %q", w, r, line)
					continue
				}
				if got := lipglossWidth(line[:i]); got != wantCtx {
					t.Errorf("w=%d row %d: context starts at %d, want %d:\n%q",
						w, r, got, wantCtx, line)
				}
			}
			if got := lipglossWidth(line); got > w {
				t.Errorf("w=%d row %d: %d cells — the row overflows and the terminal will wrap it:\n%q",
					w, r, got, line)
			}
		}
	}
}

// The revision is the last column and must stay inside its own budget: a cell
// that overruns pushes the row past the terminal.
func TestRevisionStaysInItsColumn(t *testing.T) {
	m := fleetModel(t, map[string][]string{"sb-prod": {"web"}, "dl-prod": {"api"}})
	for i := range m.apps {
		m.apps[i].Status.Sync.Revision = "0123456789abcdef0123456789abcdef01234567"
		m.apps[i].Spec.Source = &argocd.Source{
			TargetRevision: "a-very-long-branch-name-that-will-not-fit",
		}
	}
	m.applyAppFilter()
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 12})

	for r, line := range strings.Split(m.View(), "\n")[2 : 2+len(m.appRows)] {
		if got := lipglossWidth(line); got > 140 {
			t.Errorf("row %d overflows at %d cells:\n%q", r, got, line)
		}
	}
}

// Column widths are sized from what the fields actually hold, measured across a
// real fleet of ~3000 applications: project p99 = 12, name p99 = 52,
// destination p75 = 84. A proportional split spent a fifth of the row on a
// column whose contents are the word "default", and starved the destination.
func TestColumnBudgetMatchesRealContent(t *testing.T) {
	m := fleetModel(t, map[string][]string{
		"sb-prod": {"web"},
		"dl-prod": {"api"},
	})
	m.Update(tea.WindowSizeMsg{Width: 168, Height: 24})

	name, proj, dst, _ := m.appColumns()
	if proj > maxProjCol {
		t.Errorf("project column is %d cells; it never needs more than %d", proj, maxProjCol)
	}
	if name > maxNameCol {
		t.Errorf("name column is %d cells; it never needs more than %d", name, maxNameCol)
	}
	// Destination is the longest field in practice, so on a wide terminal it
	// must get the largest share of what is left.
	if dst <= name {
		t.Errorf("destination got %d cells and name got %d — destination is the longer field", dst, name)
	}
	// Destination cannot always fit — a p75 value is 84 cells and a 168-column
	// terminal has no room for that alongside name, context, and revision. What
	// it must get is the largest remaining share, which the check above asserts,
	// and enough to carry a cluster name plus the start of a namespace.
	if dst < 48 {
		t.Errorf("destination column is %d cells — too narrow to show a cluster and namespace", dst)
	}
}

// The revision is the last column, and its budget must hold what shortRev
// actually produces: a 7-char SHA plus " @" and a target revision.
func TestRevisionBudgetHoldsAShaPlusTarget(t *testing.T) {
	rev := shortRev("0123456789abcdef0123456789abcdef01234567") + " @release-1.2.3-hotfix"
	if lipglossWidth(rev) > revCol {
		t.Errorf("a SHA plus a target revision is %d cells but revCol is %d — every row would overflow",
			lipglossWidth(rev), revCol)
	}
}

// The sync status must be visible on every row. It vanished once because the
// Nerd Font set used codepoints from a range the terminal's font did not carry,
// so the glyph rendered as nothing at all and only the color was left.
func TestSyncStatusIsVisibleInEveryIconSet(t *testing.T) {
	for _, env := range []string{"unicode", "nerd", "ascii"} {
		t.Setenv("ARGX_ICONS", env)
		t.Setenv("NO_COLOR", "1")

		m := fleetModel(t, map[string][]string{"sb-prod": {"synced", "drifted"}})
		m.gl = newGlyphs()
		m.st = newStyles()
		m.st.initContexts(len(m.fleet.Names()))
		m.apps[0].Status.Sync.Status = "Synced"
		m.apps[1].Status.Sync.Status = "OutOfSync"
		m.applyAppFilter()

		out := m.View()
		syncGlyph := m.gl.syncGlyph("Synced")
		driftGlyph := m.gl.syncGlyph("OutOfSync")

		if syncGlyph == "" || driftGlyph == "" {
			t.Fatalf("%s: a sync status rendered as an empty string", env)
		}
		if !strings.Contains(out, syncGlyph) {
			t.Errorf("%s: the Synced glyph %q is missing from the list:\n%s", env, syncGlyph, out)
		}
		if !strings.Contains(out, driftGlyph) {
			t.Errorf("%s: the OutOfSync glyph %q is missing:\n%s", env, driftGlyph, out)
		}
	}
}

// Every Nerd Font glyph must come from the Material Design range that the
// Nerd Font patch reliably carries.
//
// The Font Awesome range (U+F000–U+F2FF) is patched inconsistently: several
// fonts carry the Material range but not that one, and a missing glyph renders
// as nothing — which is how the sync column went blank while its color stayed.
func TestNerdGlyphsAvoidThePatchyRange(t *testing.T) {
	t.Setenv("ARGX_ICONS", "nerd")
	g := newGlyphs()

	inspect := map[string]string{
		"marked": g.marked, "unmarked": g.unmarked, "cursor": g.cursor,
		"editable": g.editable, "noHealth": g.noHealth,
		"synced": g.synced, "outOfSync": g.outOfSync, "healthy": g.healthy,
		"progressing": g.progressing, "degraded": g.degraded,
		"missing": g.missing, "suspended": g.suspended, "unknown": g.unknown,
		"revision": g.revision, "branchRef": g.branchRef, "tagRef": g.tagRef,
		"cluster": g.cluster, "namespace": g.namespace, "project": g.project,
		"server": g.server, "clock": g.clock, "person": g.person,
		"tabResources": g.tabResources, "tabHistory": g.tabHistory,
		"tabDetails": g.tabDetails, "kindDefault": g.kindDefault,
	}
	for kind, s := range g.kinds {
		inspect["kind:"+kind] = s
	}

	for name, s := range inspect {
		for _, r := range s {
			if r >= 0xF000 && r <= 0xF2FF {
				t.Errorf("%s uses U+%04X, in the patchily-supported Font Awesome range — "+
					"use the Material Design range (U+F0000+) instead", name, r)
			}
		}
	}
}

// The revision cell says three different things depending on what Argo CD
// reports, and each has to read correctly on its own.
func TestRevisionCellShapes(t *testing.T) {
	for _, env := range []string{"unicode", "nerd"} {
		t.Setenv("ARGX_ICONS", env)
		t.Setenv("NO_COLOR", "1")

		m := fleetModel(t, map[string][]string{"sb-prod": {"x"}})
		m.gl = newGlyphs()
		m.st = newStyles()
		m.st.initContexts(1)

		tests := []struct {
			name     string
			mutate   func(*argocd.Application)
			contains []string
			excludes []string
		}{
			{
				name: "a SHA and its branch",
				mutate: func(a *argocd.Application) {
					a.Status.Sync.Revision = "0123456789abcdef0123456789abcdef01234567"
					a.Spec.Source = &argocd.Source{TargetRevision: "main"}
				},
				contains: []string{"0123456", "main"},
			},
			{
				// A chart version is both the target and what is deployed;
				// printing it twice is noise.
				name: "a chart version that equals its target",
				mutate: func(a *argocd.Application) {
					a.Status.Sync.Revision = "1.21.1"
					a.Spec.Source = &argocd.Source{TargetRevision: "1.21.1"}
				},
				contains: []string{"1.21.1"},
				excludes: []string{"1.21.1 "},
			},
			{
				// HEAD says nothing the synced revision does not already say.
				name: "HEAD is not repeated",
				mutate: func(a *argocd.Application) {
					a.Status.Sync.Revision = "0123456789abcdef0123456789abcdef01234567"
					a.Spec.Source = &argocd.Source{TargetRevision: "HEAD"}
				},
				contains: []string{"0123456"},
				excludes: []string{"HEAD"},
			},
			{
				// The old rendering printed a commit icon with nothing after it.
				name: "a multi-source app with no single revision",
				mutate: func(a *argocd.Application) {
					a.Status.Sync.Revision = ""
					a.Spec.Source = nil
					a.Spec.Sources = []argocd.Source{{RepoURL: "a"}, {RepoURL: "b"}}
				},
				contains: []string{"2 sources"},
			},
			{
				name: "nothing deployed yet",
				mutate: func(a *argocd.Application) {
					a.Status.Sync.Revision = ""
					a.Spec.Source = &argocd.Source{TargetRevision: "main"}
				},
				contains: []string{"—"},
			},
		}

		for _, tt := range tests {
			t.Run(env+"/"+tt.name, func(t *testing.T) {
				a := m.apps[0]
				tt.mutate(&a)
				got := m.revisionCell(&a)

				for _, w := range tt.contains {
					if !strings.Contains(got, w) {
						t.Errorf("cell %q is missing %q", got, w)
					}
				}
				for _, w := range tt.excludes {
					if strings.Contains(got, w) {
						t.Errorf("cell %q should not contain %q", got, w)
					}
				}
				// Whatever the shape, it must never be only an icon.
				plain := strings.TrimSpace(stripANSI(got))
				for _, r := range []string{m.gl.revision, m.gl.branchRef, m.gl.tagRef} {
					if r != "" {
						plain = strings.ReplaceAll(plain, r, "")
					}
				}
				if strings.TrimSpace(plain) == "" {
					t.Errorf("cell %q renders as an icon with no text beside it", got)
				}
			})
		}
	}
}

// stripANSI removes escape sequences so a cell's text can be inspected.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && !(s[i] >= '@' && s[i] <= '~') {
				i++
			}
			i++ // the final byte
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
