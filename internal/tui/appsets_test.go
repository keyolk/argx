package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

// appset builds an ApplicationSet for the tests.
func appset(ctx, name, project string, gens ...argocd.AppSetGenerator) argocd.ApplicationSet {
	var s argocd.ApplicationSet
	s.Context = ctx
	s.Metadata.Name = name
	s.Metadata.Namespace = "argocd"
	s.Spec.Generators = gens
	s.Spec.Template.Spec.Project = project
	return s
}

func gitGen() argocd.AppSetGenerator {
	return argocd.AppSetGenerator{Git: &argocd.AppSetGitGenerator{RepoURL: "git@example.com:org/repo"}}
}
func clusterGen() argocd.AppSetGenerator {
	return argocd.AppSetGenerator{Clusters: &argocd.AppSetClusterGenerator{}}
}
func mergeGen(inner ...argocd.AppSetGenerator) argocd.AppSetGenerator {
	return argocd.AppSetGenerator{Merge: &argocd.AppSetNestedGenerator{Generators: inner}}
}

// setModel puts a model on the ApplicationSet list with the given sets.
func setModel(t *testing.T, sets ...argocd.ApplicationSet) *Model {
	t.Helper()
	m := fleetModel(t, map[string][]string{"sb-prod": {"web"}, "dl-prod": {"api"}})
	m.Update(appSetsMsg{sets: sets})
	m.screen = screenAppSets
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	return m
}

// The generator kind is what distinguishes one set from another at a glance,
// and a nested one is more useful named by its children than by the word
// "matrix".
func TestGeneratorKindsNameNestedChildren(t *testing.T) {
	tests := []struct {
		gens []argocd.AppSetGenerator
		want string
	}{
		{[]argocd.AppSetGenerator{gitGen()}, "git"},
		{[]argocd.AppSetGenerator{clusterGen()}, "clusters"},
		{[]argocd.AppSetGenerator{mergeGen(clusterGen(), gitGen())}, "merge(clusters+git)"},
		{[]argocd.AppSetGenerator{
			{Matrix: &argocd.AppSetNestedGenerator{Generators: []argocd.AppSetGenerator{clusterGen(), gitGen()}}},
		}, "matrix(clusters×git)"},
		{[]argocd.AppSetGenerator{{}}, "?"},
	}
	for _, tt := range tests {
		s := appset("sb-prod", "x", "default", tt.gens...)
		if got := strings.Join(s.GeneratorKinds(), ","); got != tt.want {
			t.Errorf("GeneratorKinds() = %q, want %q", got, tt.want)
		}
	}
}

// A condition is present whether or not it holds; only Status "True" means it
// applies. Reading the type alone would report every set as failing.
func TestDegradedRequiresATrueCondition(t *testing.T) {
	s := appset("sb-prod", "x", "default", gitGen())

	s.Status.Conditions = []argocd.AppSetCondition{
		{Type: "ErrorOccurred", Status: "False", Message: "no error"},
	}
	if s.Degraded() {
		t.Error("a condition with Status False must not count as degraded")
	}

	s.Status.Conditions = []argocd.AppSetCondition{
		{Type: "ErrorOccurred", Status: "True", Message: "template failed"},
	}
	if !s.Degraded() {
		t.Error("a true error condition must mark the set degraded")
	}
	c, ok := s.ErrorCondition()
	if !ok || c.Message != "template failed" {
		t.Errorf("ErrorCondition() = %+v, want the failing one", c)
	}
}

// A broken generator is the one thing this list exists to surface — the
// applications it would have produced do not exist, so nothing in the
// application list can be red.
func TestBrokenGeneratorIsVisibleAndFilterable(t *testing.T) {
	broken := appset("sb-prod", "broken-set", "default", gitGen())
	broken.Status.Conditions = []argocd.AppSetCondition{
		{Type: "ErrorOccurred", Status: "True", Message: "cannot render template"},
	}
	m := setModel(t, appset("sb-prod", "fine-set", "default", gitGen()), broken)

	out := m.View()
	if !strings.Contains(out, "broken-set") {
		t.Errorf("the broken set is missing from the list:\n%s", out)
	}
	if !strings.Contains(out, "with errors") {
		t.Errorf("the status line should count the broken sets:\n%s", out)
	}

	m.appsetFilter = "status:error"
	m.applySetFilter()
	if len(m.appsetRows) != 1 {
		t.Fatalf("status:error matched %d sets, want just the broken one", len(m.appsetRows))
	}
	if got := m.currentSet(); got == nil || got.Name() != "broken-set" {
		t.Errorf("status:error selected the wrong set")
	}

	m.appsetFilter = "status:ok"
	m.applySetFilter()
	if len(m.appsetRows) != 1 || m.currentSet().Name() != "fine-set" {
		t.Errorf("status:ok should select the healthy set")
	}
}

// gen: is the axis specific to this list, and the reason to slice it.
func TestFilterByGenerator(t *testing.T) {
	m := setModel(t,
		appset("sb-prod", "from-git", "default", gitGen()),
		appset("sb-prod", "from-clusters", "default", clusterGen()),
		appset("sb-prod", "from-both", "default", mergeGen(clusterGen(), gitGen())),
	)

	for _, tt := range []struct {
		q    string
		want int
	}{
		{"gen:git", 2},      // the git one and the merge that contains it
		{"gen:clusters", 2}, // likewise
		{"gen:merge", 1},
		{"generator:git", 2},
		{"-gen:git", 1},
	} {
		m.appsetFilter = tt.q
		m.applySetFilter()
		if len(m.appsetRows) != tt.want {
			t.Errorf("%q matched %d, want %d", tt.q, len(m.appsetRows), tt.want)
		}
	}
}

func TestSetFilterFields(t *testing.T) {
	a := appset("sb-prod", "alpha", "platform", gitGen())
	a.Spec.Template.Spec.Destination = argocd.Destination{Namespace: "web"}
	a.Metadata.Labels = map[string]string{"example.com/team": "infra"}
	b := appset("dl-prod", "beta", "default", clusterGen())

	m := setModel(t, a, b)
	for _, tt := range []struct {
		q    string
		want string
	}{
		{"ctx:sb", "alpha"},
		{"proj:platform", "alpha"},
		{"ns:web", "alpha"},
		{"label:team=infra", "alpha"},
		{"l:team", "alpha"},
		{"beta", "beta"},
		{"ctx:dl", "beta"},
	} {
		m.appsetFilter = tt.q
		m.applySetFilter()
		if len(m.appsetRows) != 1 {
			t.Errorf("%q matched %d rows, want 1", tt.q, len(m.appsetRows))
			continue
		}
		if got := m.currentSet().Name(); got != tt.want {
			t.Errorf("%q selected %q, want %q", tt.q, got, tt.want)
		}
	}
}

// Two servers can host a set with the same name; a shared key would let a
// cursor restore land on the wrong one.
func TestSetKeysAreFleetUnique(t *testing.T) {
	a := appset("sb-prod", "same", "default", gitGen())
	b := appset("dl-prod", "same", "default", gitGen())
	if a.Key() == b.Key() {
		t.Fatalf("both sets share the key %q", a.Key())
	}
}

// Membership is not always recoverable, and saying nothing would be worse than
// saying so: an unfiltered list reads as "it generated everything".
func TestDrillInReportsUnknownMembership(t *testing.T) {
	s := appset("sb-prod", "orphan-set", "default", gitGen())
	m := setModel(t, s)

	before := len(m.appRows)
	m.showGeneratedApps(&s)

	if m.screen != screenAppSets {
		t.Errorf("with no membership to show, the view should stay put, got %v", m.screen)
	}
	if len(m.appRows) != before {
		t.Error("the application list was filtered on a guess")
	}
	if !strings.Contains(m.toast, "does not record") {
		t.Errorf("the reader should be told why: toast = %q", m.toast)
	}
}

// When the tracking label is present, that is the reliable answer.
func TestDrillInUsesTheTrackingLabel(t *testing.T) {
	m := setModel(t, appset("sb-prod", "labelled-set", "default", gitGen()))
	m.apps[0].Metadata.Labels = map[string]string{appsetTrackingLabel: "labelled-set"}

	s := m.appsets[0]
	m.showGeneratedApps(&s)

	if m.screen != screenApps {
		t.Fatalf("screen = %v, want the application list", m.screen)
	}
	if len(m.appRows) != 1 {
		t.Fatalf("matched %d applications, want the labelled one", len(m.appRows))
	}
	if !strings.Contains(m.toast, "generated by") {
		t.Errorf("toast = %q, want it to say how many", m.toast)
	}
}

// S toggles between the two lists — they are peers, so getting back is the same
// key that got you there.
func TestToggleBetweenLists(t *testing.T) {
	m := setModel(t, appset("sb-prod", "x", "default", gitGen()))
	m.screen = screenApps
	m.appsetsLoaded = true

	press(t, m, "S")
	if m.screen != screenAppSets {
		t.Fatalf("S did not reach the set list, screen = %v", m.screen)
	}
	press(t, m, "S")
	if m.screen != screenApps {
		t.Errorf("S did not return to the applications, screen = %v", m.screen)
	}
}

// The list is loaded on first visit, not at startup: most sessions never open
// it, and it is one request per server.
func TestSetListLoadsLazily(t *testing.T) {
	m := fleetModel(t, map[string][]string{"sb-prod": {"web"}})
	if m.appsetsLoaded {
		t.Fatal("the set list should not be fetched before it is opened")
	}
	cmd := press(t, m, "S")
	if cmd == nil {
		t.Error("the first visit should issue a fetch")
	}
	m.Update(appSetsMsg{sets: []argocd.ApplicationSet{appset("sb-prod", "x", "default", gitGen())}})
	if !m.appsetsLoaded {
		t.Error("the response should mark the list loaded")
	}

	m.screen = screenApps
	if cmd := press(t, m, "S"); cmd != nil {
		t.Error("a second visit should not refetch")
	}
}

func TestSetListNavigates(t *testing.T) {
	m := setModel(t,
		appset("sb-prod", "a", "default", gitGen()),
		appset("sb-prod", "b", "default", gitGen()),
		appset("sb-prod", "c", "default", gitGen()),
	)

	press(t, m, "j")
	if m.appsetCur != 1 {
		t.Errorf("j moved to %d, want 1", m.appsetCur)
	}
	press(t, m, "G")
	if m.appsetCur != 2 {
		t.Errorf("G moved to %d, want the last row", m.appsetCur)
	}
	press(t, m, "g")
	if m.appsetCur != 0 {
		t.Errorf("g moved to %d, want 0", m.appsetCur)
	}
	press(t, m, "k", "k")
	if m.appsetCur != 0 {
		t.Errorf("k past the top moved to %d, want 0", m.appsetCur)
	}
}

// The cursor stays on the same set across a filter change when it survived.
func TestSetCursorSurvivesFiltering(t *testing.T) {
	m := setModel(t,
		appset("sb-prod", "alpha", "default", gitGen()),
		appset("sb-prod", "beta", "default", gitGen()),
		appset("sb-prod", "gamma", "default", gitGen()),
	)
	m.appsetCur = 2 // gamma

	m.appsetFilter = "a"
	m.applySetFilter()

	if got := m.currentSet(); got == nil || got.Name() != "gamma" {
		t.Error("the cursor moved off gamma on a filter that still matches it")
	}
}

// Every width, every icon set — the set list is a table like any other.
func TestSetListFitsTheTerminal(t *testing.T) {
	for _, env := range []string{"unicode", "nerd", "ascii"} {
		for _, size := range [][2]int{{150, 24}, {120, 20}, {100, 20}, {80, 24}, {60, 14}} {
			w, h := size[0], size[1]
			t.Setenv("ARGX_ICONS", env)
			t.Setenv("NO_COLOR", "1")

			m := setModel(t,
				appset("sb-prod", "applicationset-addon-aws-load-balancer-controller-v2", "default",
					mergeGen(clusterGen(), gitGen())),
				appset("dl-prod", "short", "a-fairly-long-project-name", gitGen()),
			)
			m.gl = newGlyphs()
			m.st = newStyles()
			m.st.initContexts(len(m.fleet.Names()))
			m.Update(tea.WindowSizeMsg{Width: w, Height: h})

			out := m.View()
			if got := strings.Count(out, "\n") + 1; got != h {
				t.Errorf("%s %dx%d rendered %d lines, want %d", env, w, h, got, h)
			}
			for i, line := range strings.Split(out, "\n") {
				if got := lipglossWidth(line); got > w {
					t.Errorf("%s %dx%d line %d is %d cells, want at most %d:\n%q",
						env, w, h, i, got, w, line)
				}
			}
		}
	}
}
