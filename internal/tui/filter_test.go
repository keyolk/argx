package tui

import (
	"testing"

	"github.com/keyolk/argx/internal/argocd"
)

func fnode(kind, name, ns, health string) argocd.Node {
	n := argocd.Node{ResourceRef: argocd.ResourceRef{Kind: kind, Name: name, Namespace: ns}}
	if health != "" {
		n.Health = &argocd.Health{Status: health}
	}
	return n
}

func TestFilterMatchesByField(t *testing.T) {
	pod := fnode("Pod", "web-abc12", "prod", "Degraded")
	dep := fnode("Deployment", "web", "prod", "Healthy")
	svc := fnode("Service", "api", "staging", "")

	tests := []struct {
		query string
		pod   bool
		dep   bool
		svc   bool
	}{
		{"", true, true, true},
		{"web", true, true, false},
		{"kind:pod", true, false, false},
		{"k:deploy", false, true, false},
		{"status:degraded", true, false, false},
		{"s:healthy", false, true, false},
		{"ns:prod", true, true, false},
		{"n:staging", false, false, true},
		// Terms across fields are ANDed.
		{"kind:pod status:degraded", true, false, false},
		{"kind:pod status:healthy", false, false, false},
		{"web kind:deployment", false, true, false},
		// A kind Argo CD does not health-check answers to status:none — the
		// only way to find ConfigMaps, Secrets, and most CRDs by status.
		{"status:none", false, false, true},
	}
	for _, tt := range tests {
		f := parseResourceFilter(tt.query)
		if got := f.match(pod); got != tt.pod {
			t.Errorf("%q vs Pod = %v, want %v", tt.query, got, tt.pod)
		}
		if got := f.match(dep); got != tt.dep {
			t.Errorf("%q vs Deployment = %v, want %v", tt.query, got, tt.dep)
		}
		if got := f.match(svc); got != tt.svc {
			t.Errorf("%q vs Service = %v, want %v", tt.query, got, tt.svc)
		}
	}
}

// Kinds are conventionally capitalized; nobody types "kind:StatefulSet".
func TestFilterIsCaseInsensitive(t *testing.T) {
	n := fnode("StatefulSet", "Postgres-0", "Prod", "Healthy")
	for _, q := range []string{"kind:statefulset", "KIND:STATEFULSET", "postgres", "ns:prod", "status:HEALTHY"} {
		if !parseResourceFilter(q).match(n) {
			t.Errorf("%q should match regardless of case", q)
		}
	}
}

// A kind term is a prefix match, so "kind:set" must not find "StatefulSet" —
// substring matching there makes kind filtering useless on a real tree.
func TestFilterKindIsPrefixNotSubstring(t *testing.T) {
	n := fnode("StatefulSet", "db", "prod", "Healthy")
	if parseResourceFilter("kind:set").match(n) {
		t.Error("kind:set should not match StatefulSet")
	}
	if !parseResourceFilter("kind:stateful").match(n) {
		t.Error("kind:stateful should match StatefulSet")
	}
}

// The API group is searchable through the kind field, so "kind:apps" finds
// everything in apps/.
func TestFilterKindMatchesAPIGroup(t *testing.T) {
	n := argocd.Node{ResourceRef: argocd.ResourceRef{Kind: "Deployment", Group: "apps", Name: "web"}}
	if !parseResourceFilter("kind:apps").match(n) {
		t.Error("kind: should also match the API group")
	}
}

// While the user is mid-word the list must narrow, not jump: a trailing colon
// is a term still being typed, not a request to match nothing.
func TestFilterHandlesPartialPrefix(t *testing.T) {
	n := fnode("Pod", "kind-checker", "prod", "Healthy")
	f := parseResourceFilter("kind:")
	if !f.match(n) {
		t.Error("a bare `kind:` should not filter everything out")
	}
	// An unknown prefix is far likelier to be a name with a colon in it.
	if !parseResourceFilter("kind-check").match(n) {
		t.Error("an unprefixed term should search the name")
	}
}

func TestFilterEmptyAndString(t *testing.T) {
	if !parseResourceFilter("   ").empty() {
		t.Error("whitespace should count as an empty filter")
	}
	f := parseResourceFilter("kind:Pod web")
	if got := f.String(); got != "kind:Pod web" {
		t.Errorf("String() = %q — the filter line must echo what was typed", got)
	}
	if f.empty() {
		t.Error("a filter with terms is not empty")
	}
}

// The tree view suppresses indentation while a filter is active; that decision
// keys on empty(), so it has to be right for a filter that parses to no terms.
func TestFilterWithOnlySeparatorsIsNotEmpty(t *testing.T) {
	f := parseResourceFilter("::")
	if f.empty() {
		t.Error("a non-blank query is not empty even if it parses oddly")
	}
}
