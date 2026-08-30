package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
	"github.com/keyolk/argx/internal/config"
)

// A command that names neither path gets both appended, which is what every
// diff tool's own CLI expects and makes the common case one word of config.
func TestPathsAreAppendedWhenUnnamed(t *testing.T) {
	got := expandDiffArgv([]string{"nvim", "-d"}, "/tmp/a", "/tmp/b")
	want := []string{"nvim", "-d", "/tmp/a", "/tmp/b"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

// A command that names them controls where they go — some tools take flags
// after the files, or want them in the other order.
func TestNamedPlaceholdersAreSubstitutedInPlace(t *testing.T) {
	got := expandDiffArgv(
		[]string{"difft", "{desired}", "{live}", "--display", "inline"},
		"/tmp/live", "/tmp/desired")
	want := []string{"difft", "/tmp/desired", "/tmp/live", "--display", "inline"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v, want %v", got, want)
	}
	// Naming only one is still naming them: nothing is appended.
	got = expandDiffArgv([]string{"tool", "--old={live}"}, "/tmp/live", "/tmp/desired")
	if len(got) != 2 {
		t.Errorf("argv = %v, want nothing appended once a placeholder is used", got)
	}
}

// The two documents are what the tool gets, not argx's rendering of them: a
// tool that computes its own diff can do things argx's cannot, and handing it a
// finished patch would throw all of that away.
func TestSidesCarryTheDocumentsNotTheDiff(t *testing.T) {
	items := []argocd.ResourceDiff{{
		Kind: "Deployment", Namespace: "web", Name: "frontend",
		NormalizedLiveState: `{"spec":{"replicas":3}}`,
		TargetState:         `{"spec":{"replicas":5}}`,
	}}

	sides := collectSides(items, nil, "web-frontend")
	if sides == nil {
		t.Fatal("a differing resource should produce two sides")
	}
	if !strings.Contains(sides.live, `"replicas": 3`) {
		t.Errorf("the live side should be the live document:\n%s", sides.live)
	}
	if !strings.Contains(sides.desired, `"replicas": 5`) {
		t.Errorf("the desired side should be the desired document:\n%s", sides.desired)
	}
	// No diff markers anywhere: these are documents, not a patch.
	for _, s := range []string{sides.live, sides.desired} {
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "@@") {
				t.Errorf("a diff marker leaked into a document: %q", line)
			}
		}
	}
}

// The header goes on both sides even when one is empty, so a tool diffing the
// files line by line does not report the header itself as a change.
func TestHeadersAppearOnBothSides(t *testing.T) {
	items := []argocd.ResourceDiff{{
		Kind: "ConfigMap", Namespace: "kube-system", Name: "new-thing",
		TargetState: `{"data":{"a":"1"}}`,
	}}

	sides := collectSides(items, nil, "app")
	if sides == nil {
		t.Fatal("a resource that exists only in git still has two sides to compare")
	}
	head := "# ConfigMap kube-system/new-thing"
	if !strings.Contains(sides.live, head) || !strings.Contains(sides.desired, head) {
		t.Error("the header should be on both sides, or the tool reports it as a change")
	}
	if strings.Contains(sides.live, `"data"`) {
		t.Error("a resource that is not in the cluster has no live content")
	}
}

// Identical resources are left out entirely — the same rule the unified view
// follows, so the tool sees the comparison the reader asked for.
func TestUnchangedResourcesAreOmitted(t *testing.T) {
	same := `{"spec":{"replicas":3}}`
	items := []argocd.ResourceDiff{
		{Kind: "Deployment", Name: "same", NormalizedLiveState: same, TargetState: same},
	}
	if sides := collectSides(items, nil, "app"); sides != nil {
		t.Errorf("nothing differs, so there is nothing to hand a diff tool:\n%+v", sides)
	}
}

// The marked-resource filter applies here too: `d` on two marked rows should
// not open a tool showing nine.
func TestOnlyTheRequestedResourcesAreCollected(t *testing.T) {
	items := []argocd.ResourceDiff{
		{Kind: "Deployment", Namespace: "web", Name: "wanted",
			NormalizedLiveState: `{"a":1}`, TargetState: `{"a":2}`},
		{Kind: "Service", Namespace: "web", Name: "unwanted",
			NormalizedLiveState: `{"b":1}`, TargetState: `{"b":2}`},
	}
	want := map[string]bool{diffKey("", "Deployment", "web", "wanted"): true}

	sides := collectSides(items, want, "app")
	if sides == nil {
		t.Fatal("the wanted resource differs")
	}
	if strings.Contains(sides.live, "unwanted") {
		t.Errorf("an unmarked resource leaked in:\n%s", sides.live)
	}
}

// Without a configured tool the key says what to do rather than doing nothing.
func TestNoConfiguredToolSaysSo(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m.screen = screenDiff
	m.pagerSides = &diffSides{name: "app", live: "a", desired: "b"}

	if cmd := m.diffToolCmd(); cmd != nil {
		t.Error("with nothing configured there is nothing to run")
	}
	if !strings.Contains(m.toast, "diff_tool") {
		t.Errorf("toast = %q, want it to name the setting to add", m.toast)
	}
}

// Nor does it run on a view with no two sides.
func TestDiffToolNeedsSomethingToCompare(t *testing.T) {
	m := newTestModel(t)
	m.cfg = &config.Config{DiffTool: []string{"true"}}
	m.screen = screenDiff
	m.pagerSides = nil

	if cmd := m.diffToolCmd(); cmd != nil {
		t.Error("there is nothing to compare")
	}
	if !strings.Contains(m.toast, "nothing to compare") {
		t.Errorf("toast = %q, want it to say why", m.toast)
	}
}

// D is diff-only: on a log view it would launch a diff of nothing.
func TestDiffToolKeyIsDiffOnly(t *testing.T) {
	m := newTestModel(t)
	m.cfg = &config.Config{DiffTool: []string{"false"}}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	m.screen = screenLogs
	m.pagerSides = &diffSides{name: "app", live: "a", desired: "b"}

	if _, cmd := m.Update(key("D")); cmd != nil {
		t.Error("D should do nothing outside the diff view")
	}
}

// A name that is not a usable path segment is made into one — an application
// name comes from a cluster and can contain anything.
func TestFileNamesAreSanitized(t *testing.T) {
	for in, want := range map[string]string{
		"web-frontend":     "web-frontend",
		"apps/web:v1":      "apps-web-v1",
		"../../etc/passwd": "..-..-etc-passwd",
		"a b\tc":           "a-b-c",
		// A name that is nothing but dots would be a file the cleanup then
		// tries to remove by that name.
		"":     "argx",
		"..":   "argx",
		"....": "argx",
		".":    "argx",
	} {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Whatever the name, the files land inside the directory argx created — a
// traversal has nowhere to go.
func TestSanitizedNamesStayInTheDirectory(t *testing.T) {
	for _, name := range []string{"../../etc/passwd", "a/b/c", "..", "~/.ssh/id_rsa"} {
		got := sanitizeFileName(name)
		if strings.ContainsAny(got, "/\\") {
			t.Errorf("sanitizeFileName(%q) = %q, which is not one path segment", name, got)
		}
		if strings.Trim(got, ".") == "" {
			t.Errorf("sanitizeFileName(%q) = %q, which names a directory", name, got)
		}
	}
}
