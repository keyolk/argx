package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/argx/internal/argocd"
)

// cm builds a ConfigMap entry of the managed-resources response. Only one side
// is populated, which is what a hash rotation actually looks like: the old name
// exists live and not in git, the new name the other way round.
func cm(name, live, desired string) argocd.ResourceDiff {
	it := argocd.ResourceDiff{Kind: "ConfigMap", Namespace: "prod", Name: name}
	if live == "" {
		it.NormalizedLiveState = "null"
	} else {
		it.NormalizedLiveState = live
	}
	if desired == "" {
		it.PredictedLiveState = "null"
	} else {
		it.PredictedLiveState = desired
	}
	return it
}

func doc(name, value string) string {
	return `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"` + name +
		`","namespace":"prod"},"data":{"level":"` + value + `"}}`
}

// The whole point: a rotated ConfigMap is one edit, not two whole manifests.
func TestHashRotationRendersAsOneEdit(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("app-config-a1b2c3", doc("app-config-a1b2c3", "info"), ""),
		cm("app-config-d4e5f6", "", doc("app-config-d4e5f6", "debug")),
	}

	got := strings.Join(renderDiff(items, nil, true), "\n")

	if strings.Contains(got, "will be created") || strings.Contains(got, "prune candidate") {
		t.Errorf("the pair should not also render as a create and a prune:\n%s", got)
	}
	if !strings.Contains(got, "hash rotated") {
		t.Errorf("the header should say what was paired:\n%s", got)
	}
	// Both real names, because the base name exists on neither side.
	if !strings.Contains(got, "app-config-a1b2c3") || !strings.Contains(got, "app-config-d4e5f6") {
		t.Errorf("both names should be named:\n%s", got)
	}
	// The content change is what is left.
	if !strings.Contains(got, "info") || !strings.Contains(got, "debug") {
		t.Errorf("the content change should be visible:\n%s", got)
	}
}

// The name must not read as the change — that is the noise being removed.
func TestPairedNamesAreNormalizedAway(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("app-config-a1b2c3", doc("app-config-a1b2c3", "info"), ""),
		cm("app-config-d4e5f6", "", doc("app-config-d4e5f6", "debug")),
	}

	got := renderDiff(items, nil, true)
	for _, line := range got {
		if !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "+") {
			continue // header and context lines may name both
		}
		if strings.Contains(line, "a1b2c3") || strings.Contains(line, "d4e5f6") {
			t.Errorf("the hash should not show as a changed line: %q", line)
		}
	}
}

// A rotation that changed nothing but the name is not a change at all.
func TestHashOnlyRotationIsOmitted(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("app-config-a1b2c3", doc("app-config-a1b2c3", "info"), ""),
		cm("app-config-d4e5f6", "", doc("app-config-d4e5f6", "info")),
	}

	got := strings.Join(renderDiff(items, nil, true), "\n")
	if !strings.Contains(got, "no differences") {
		t.Errorf("only the name changed, so there is nothing to read:\n%s", got)
	}
}

// Off, the same input is what it was before — two unrelated resources. This is
// what the toggle is for: the pairing is an inference, and the reader has to be
// able to see the documents it was made from.
func TestPairingOffRestoresTheCreateAndPrune(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("app-config-a1b2c3", doc("app-config-a1b2c3", "info"), ""),
		cm("app-config-d4e5f6", "", doc("app-config-d4e5f6", "debug")),
	}

	got := strings.Join(renderDiff(items, nil, false), "\n")
	if !strings.Contains(got, "will be created") || !strings.Contains(got, "prune candidate") {
		t.Errorf("with pairing off both halves should render on their own:\n%s", got)
	}
	if strings.Contains(got, "hash rotated") {
		t.Errorf("nothing should have been paired:\n%s", got)
	}
}

// Gated on kind. A Deployment whose name happens to end in ten lowercase
// characters is not a hashed resource, and pairing two of them would claim two
// different workloads are one.
func TestOnlyHashedKindsArePaired(t *testing.T) {
	mk := func(name, live, desired string) argocd.ResourceDiff {
		it := cm(name, live, desired)
		it.Kind = "Deployment"
		return it
	}
	items := []argocd.ResourceDiff{
		mk("web-abcdef", `{"metadata":{"name":"web-abcdef"},"spec":{"replicas":1}}`, ""),
		mk("web-ghijkl", "", `{"metadata":{"name":"web-ghijkl"},"spec":{"replicas":2}}`),
	}

	got := strings.Join(renderDiff(items, nil, true), "\n")
	if strings.Contains(got, "hash rotated") {
		t.Errorf("a Deployment is not a hash-suffixed resource:\n%s", got)
	}
	if !strings.Contains(got, "will be created") || !strings.Contains(got, "prune candidate") {
		t.Errorf("both should render on their own:\n%s", got)
	}
}

// A resource that exists under the same name on both sides is an ordinary
// change, whatever its name ends in — it was never a rotation.
func TestUnrotatedResourceIsNotPaired(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("app-config-a1b2c3",
			doc("app-config-a1b2c3", "info"),
			doc("app-config-a1b2c3", "debug")),
	}

	got := strings.Join(renderDiff(items, nil, true), "\n")
	if strings.Contains(got, "hash rotated") {
		t.Errorf("nothing rotated here:\n%s", got)
	}
	if !strings.Contains(got, "debug") {
		t.Errorf("it is still an ordinary change:\n%s", got)
	}
}

// A name with no hash is left alone even on a hashed kind.
func TestPlainNamesAreNotPaired(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("old-settings", doc("old-settings", "info"), ""),
		cm("new-settings", "", doc("new-settings", "debug")),
	}

	got := strings.Join(renderDiff(items, nil, true), "\n")
	if strings.Contains(got, "hash rotated") {
		t.Errorf("two differently named ConfigMaps are two resources:\n%s", got)
	}
}

// With several candidates for one base the partner has to be chosen the same
// way every time. argodiff picks its by overwriting a map entry while ranging
// over a map, so its answer follows Go's randomized iteration order and differs
// between runs; this must not.
//
// The choice is by name, so it is asserted by name rather than by "it did the
// same thing twice" — a stable-looking result is exactly what a randomized
// choice produces most of the time with two candidates.
func TestPairingPicksTheSamePartnerEveryTime(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("app-config-bbbbbb", doc("app-config-bbbbbb", "two"), ""),
		cm("app-config-aaaaaa", doc("app-config-aaaaaa", "one"), ""),
		cm("app-config-cccccc", "", doc("app-config-cccccc", "three")),
	}

	pairs, consumed := pairHashed(items)
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d", len(pairs))
	}
	if got := pairs[0].live.Name; got != "app-config-aaaaaa" {
		t.Errorf("partner = %q, want the first by name — the tie-break must not "+
			"depend on map order", got)
	}
	// The candidate that lost still has to be shown: silently dropping it would
	// hide a resource that really is being pruned.
	if consumed[diffKey("", "ConfigMap", "prod", "app-config-bbbbbb")] {
		t.Error("the unpaired candidate must not be consumed")
	}
	got := strings.Join(renderDiff(items, nil, true), "\n")
	if !strings.Contains(got, "prune candidate") {
		t.Errorf("the unpaired candidate should still render:\n%s", got)
	}
}

// Several bases pair independently, and the order they are emitted in is
// stable — otherwise a diff reshuffles itself between two looks at the same
// application.
func TestPairOrderIsStable(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("zeta-cfg-aaaaaa", doc("zeta-cfg-aaaaaa", "1"), ""),
		cm("alpha-cfg-aaaaaa", doc("alpha-cfg-aaaaaa", "1"), ""),
		cm("zeta-cfg-bbbbbb", "", doc("zeta-cfg-bbbbbb", "2")),
		cm("alpha-cfg-bbbbbb", "", doc("alpha-cfg-bbbbbb", "2")),
	}

	pairs, _ := pairHashed(items)
	if len(pairs) != 2 {
		t.Fatalf("want two pairs, got %d", len(pairs))
	}
	if pairs[0].base != "alpha-cfg" || pairs[1].base != "zeta-cfg" {
		t.Errorf("pairs came out as %q then %q, want them sorted",
			pairs[0].base, pairs[1].base)
	}
}

// The external diff tool must be handed the same comparison that is on screen.
func TestDiffToolSeesThePairedComparison(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("app-config-a1b2c3", doc("app-config-a1b2c3", "info"), ""),
		cm("app-config-d4e5f6", "", doc("app-config-d4e5f6", "debug")),
	}

	sides := collectSides(items, nil, "app", true)
	if sides == nil {
		t.Fatal("a rotated resource with a content change has two sides")
	}
	if !strings.Contains(sides.live, `"level": "info"`) {
		t.Errorf("the live side should be the old document:\n%s", sides.live)
	}
	if !strings.Contains(sides.desired, `"level": "debug"`) {
		t.Errorf("the desired side should be the new one:\n%s", sides.desired)
	}
	// Both documents carry the base name, so the tool does not report the name
	// as the difference.
	if strings.Contains(sides.live, "a1b2c3\"") || strings.Contains(sides.desired, "d4e5f6\"") {
		t.Errorf("the names should have been normalized:\n%s\n---\n%s", sides.live, sides.desired)
	}
}

// The rename edits metadata.name and nothing else. A manifest is full of other
// name fields — containers, volumes, ports — and argodiff's textual rule
// rewrites whichever one it meets first.
func TestRenameTouchesOnlyMetadataName(t *testing.T) {
	in := `{"metadata":{"name":"cfg-a1b2c3"},"spec":{"containers":[{"name":"cfg-a1b2c3"}]}}`
	out := renameTo(in, "cfg")

	if !strings.Contains(out, `"name": "cfg"`) {
		t.Errorf("metadata.name should have been rewritten:\n%s", out)
	}
	if !strings.Contains(out, `"name": "cfg-a1b2c3"`) {
		t.Errorf("the container name is not the resource's name and must survive:\n%s", out)
	}
}

// Anything that is not a JSON object with a string metadata.name is returned
// untouched rather than mangled.
func TestRenameLeavesUnexpectedDocumentsAlone(t *testing.T) {
	for _, in := range []string{
		"",
		"not json at all",
		`{"metadata":"a string"}`,
		`{"spec":{}}`,
		`{"metadata":{"name":42}}`,
	} {
		if got := renameTo(in, "cfg"); got != in {
			t.Errorf("renameTo(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestBaseNameRule(t *testing.T) {
	for _, tc := range []struct {
		kind, name string
		want       string
		hashed     bool
	}{
		{"ConfigMap", "app-config-a1b2c3", "app-config", true},
		{"configmap", "app-config-a1b2c3", "app-config", true},
		{"Secret", "creds-0123456789", "creds", true},
		{"ExternalSecret", "es-abcdef", "es", true},
		// The base group is greedy, so only the last segment is the hash.
		{"ConfigMap", "a-b-c-1a2b3c", "a-b-c", true},
		// Too short, too long, and no separator.
		{"ConfigMap", "app-abc", "app-abc", false},
		{"ConfigMap", "app-abcdefghijk", "app-abcdefghijk", false},
		{"ConfigMap", "appconfig", "appconfig", false},
		// Uppercase is not a hash — and is not a legal object name anyway.
		{"ConfigMap", "app-ABCDEF", "app-ABCDEF", false},
		// Gated on kind.
		{"Deployment", "web-a1b2c3", "web-a1b2c3", false},
	} {
		got, hashed := baseName(tc.kind, tc.name)
		if got != tc.want || hashed != tc.hashed {
			t.Errorf("baseName(%q, %q) = (%q, %v), want (%q, %v)",
				tc.kind, tc.name, got, hashed, tc.want, tc.hashed)
		}
	}
}

// The tree's `d` narrows the diff to marked resources; a pair must be reachable
// by marking either half, since the base name is not a row in the tree.
func TestMarkingEitherHalfShowsThePair(t *testing.T) {
	items := []argocd.ResourceDiff{
		cm("app-config-a1b2c3", doc("app-config-a1b2c3", "info"), ""),
		cm("app-config-d4e5f6", "", doc("app-config-d4e5f6", "debug")),
	}

	for _, name := range []string{"app-config-a1b2c3", "app-config-d4e5f6"} {
		want := map[string]bool{diffKey("", "ConfigMap", "prod", name): true}
		got := strings.Join(renderDiff(items, want, true), "\n")
		if !strings.Contains(got, "hash rotated") {
			t.Errorf("marking %s should reach the pair:\n%s", name, got)
		}
	}
}

// ---- the toggle ----

// H re-renders from the response already in hand. Re-asking the server would
// mean the reader is shown a different comparison than the one they pressed a
// key to re-present.
func TestHashToggleRerendersWithoutRefetching(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.push(screenDiff)
	m.diffItems = []argocd.ResourceDiff{
		cm("app-config-a1b2c3", doc("app-config-a1b2c3", "info"), ""),
		cm("app-config-d4e5f6", "", doc("app-config-d4e5f6", "debug")),
	}
	m.pager = renderDiff(m.diffItems, nil, m.smartHash)

	if !strings.Contains(strings.Join(m.pager, "\n"), "hash rotated") {
		t.Fatal("setup: pairing should be on by default")
	}

	_, cmd := m.Update(key("H"))
	if cmd != nil {
		t.Error("toggling must not issue a command — the response is already in hand")
	}
	if m.smartHash {
		t.Fatal("H should have turned pairing off")
	}
	got := strings.Join(m.pager, "\n")
	if strings.Contains(got, "hash rotated") {
		t.Errorf("the view should have re-rendered unpaired:\n%s", got)
	}
	if !strings.Contains(got, "will be created") {
		t.Errorf("both halves should be back:\n%s", got)
	}

	press(t, m, "H")
	if !m.smartHash || !strings.Contains(strings.Join(m.pager, "\n"), "hash rotated") {
		t.Error("H again should pair them back up")
	}
}

// It is a claim about a diff, so it belongs to the diff view — a key that
// silently does nothing on the screens either side of it is one people stop
// trusting.
func TestHashToggleIsOnlyInTheDiffView(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.push(screenLogs)
	m.pager = []string{"a log line"}

	press(t, m, "H")
	if !m.smartHash {
		t.Error("H outside the diff view should do nothing")
	}
}

// Off *and* relevant is the only case worth a standing note; a mode that would
// change nothing here must not be announced.
func TestStatusSaysWhenPairingIsOffAndWouldMatter(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	m.push(screenDiff)
	m.diffItems = []argocd.ResourceDiff{
		cm("app-config-a1b2c3", doc("app-config-a1b2c3", "info"), ""),
		cm("app-config-d4e5f6", "", doc("app-config-d4e5f6", "debug")),
	}
	pairs, _ := pairHashed(m.diffItems)
	m.diffHasPairs = len(pairs) > 0

	if got := m.renderStatus(); strings.Contains(got, "hash pairing off") {
		t.Errorf("nothing to say while it is on: %q", got)
	}
	m.smartHash = false
	if got := m.renderStatus(); !strings.Contains(got, "hash pairing off") {
		t.Errorf("off and relevant should be said: %q", got)
	}

	// Nothing to pair — then the note is noise.
	m.diffItems = []argocd.ResourceDiff{
		cm("plain-config", doc("plain-config", "info"), doc("plain-config", "debug")),
	}
	m.diffHasPairs = false
	if got := m.renderStatus(); strings.Contains(got, "hash pairing off") {
		t.Errorf("a mode that changes nothing here must not be announced: %q", got)
	}
}
