package tui

// Pairing resources whose names carry a content hash.
//
// A ConfigMap or Secret named for its own content — `app-config-a1b2c3d4` —
// gets a new name every time the content changes. That is deliberate: it stops
// two deployments from sharing a mutable object mid-rollout. But it means the
// managed-resources response describes an *edit* as two unrelated resources,
// one being created and one being pruned, and the diff view faithfully shows
// two full manifests where the reader wanted the handful of lines that changed.
//
// So the pair is put back together: matched by the name they share once the
// hash is removed, then diffed against each other with both names rewritten to
// the base so the name itself does not read as the change.
//
// This mirrors argodiff's `--smart-hash` (pkg/diff/smart_hash.go), including
// its default pattern and its default kinds, because the two tools looking at
// one cluster and disagreeing about what changed is worse than either rule
// being slightly wrong. Two things are deliberately *not* mirrored:
//
//   - argodiff picks the partner by overwriting a map entry while ranging over
//     a map, so with two candidates the pairing is whatever Go's randomized
//     iteration order lands on and differs between runs. Here the candidates
//     are sorted and the first is taken, which is arbitrary but at least the
//     same arbitrary answer every time.
//   - argodiff rewrites the name with a regex over the rendered text, which
//     touches the first `name:` line in the document and silently does nothing
//     when that is not the metadata one. argx holds these as JSON, so the field
//     is edited where it lives.
//
// What is *not* solved, in either tool: a Deployment mounting the old name
// still shows up as changed, because its manifest genuinely does still name the
// old ConfigMap. Collapsing that too would mean rewriting references inside
// unrelated resources, which is a much larger claim about what the reader is
// looking at than "these two are the same object".

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/keyolk/argx/internal/argocd"
)

// hashSuffix matches a name ending in a content hash.
//
// The pattern is argodiff's, verbatim. The second alternative is a superset of
// the first, so it reads as `[a-z0-9]{6,10}` — the hex branch is kept only so
// the two tools can be compared by eye. Anchored at both ends, and the base
// group is greedy, so `a-b-c-1a2b3c` bases on `a-b-c` rather than `a`.
var hashSuffix = regexp.MustCompile(`^(.+)-([a-f0-9]{6,10}|[a-z0-9]{6,10})$`)

// hashedKinds are the kinds whose names are hashed by the tools that generate
// them. Gating on kind is what keeps the pattern from pairing, say, two
// ReplicaSets that happen to end in ten lowercase characters.
var hashedKinds = map[string]bool{
	"configmap":      true,
	"secret":         true,
	"externalsecret": true,
}

// hashPair is one resource seen under two names.
type hashPair struct {
	// live is the entry being pruned, desired the one being created.
	live, desired argocd.ResourceDiff
	// base is the name they share with the hash removed; it is what the diff
	// is headed with.
	base string
}

// baseName strips a content hash from a name, reporting whether there was one.
func baseName(kind, name string) (string, bool) {
	if !hashedKinds[strings.ToLower(kind)] {
		return name, false
	}
	m := hashSuffix.FindStringSubmatch(name)
	if len(m) < 2 {
		return name, false
	}
	return m[1], true
}

// pairHashed matches created resources against pruned ones by their base name.
//
// It returns the pairs and the set of keys they consumed, so the caller can
// skip those entries in its own pass — a paired resource must not also appear
// as a create and a prune.
func pairHashed(items []argocd.ResourceDiff) ([]hashPair, map[string]bool) {
	// Only entries that exist on one side can be a rotation: a name present in
	// both states is the same object under the same name, whatever it ends in.
	created := map[string][]int{}
	pruned := map[string][]int{}
	for i, it := range items {
		live, desired, ok := diffPair(it)
		if !ok || (live != "" && desired != "") {
			continue
		}
		base, hashed := baseName(it.Kind, it.Name)
		if !hashed {
			continue
		}
		k := diffKey(it.Group, it.Kind, it.Namespace, base)
		switch {
		case live == "" && desired != "":
			created[k] = append(created[k], i)
		case live != "" && desired == "":
			pruned[k] = append(pruned[k], i)
		}
	}

	// Sorted, so a base with several candidates on either side pairs the same
	// way every run. Which one it picks is arbitrary; that it picks the same
	// one twice in a row is not.
	bases := make([]string, 0, len(created))
	for k := range created {
		if len(pruned[k]) > 0 {
			bases = append(bases, k)
		}
	}
	sort.Strings(bases)

	var pairs []hashPair
	consumed := map[string]bool{}
	for _, k := range bases {
		c, p := pickOne(items, created[k]), pickOne(items, pruned[k])
		base, _ := baseName(items[c].Kind, items[c].Name)
		pairs = append(pairs, hashPair{live: items[p], desired: items[c], base: base})
		consumed[itemKey(items[c])] = true
		consumed[itemKey(items[p])] = true
	}
	return pairs, consumed
}

// pickOne is the first candidate by name, so the choice does not depend on the
// order the server happened to list them in.
func pickOne(items []argocd.ResourceDiff, idx []int) int {
	best := idx[0]
	for _, i := range idx[1:] {
		if items[i].Name < items[best].Name {
			best = i
		}
	}
	return best
}

// itemKey identifies an entry of the managed-resources response.
func itemKey(it argocd.ResourceDiff) string {
	return diffKey(it.Group, it.Kind, it.Namespace, it.Name)
}

// sides is the two documents of a pair, with both names rewritten to the base
// so the rotation itself does not render as a change.
func (p hashPair) sides() (live, desired string) {
	l, _, _ := diffPair(p.live)
	_, d, _ := diffPair(p.desired)
	return renameTo(l, p.base), renameTo(d, p.base)
}

// renameTo rewrites metadata.name in a JSON manifest.
//
// Structural rather than textual: a manifest holds other `name` fields — every
// container, every volume, every port — and a rule about which line comes first
// is a rule that breaks the day a document is serialized differently.
func renameTo(doc, name string) string {
	if doc == "" || name == "" {
		return doc
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		return doc
	}
	meta, ok := v["metadata"].(map[string]any)
	if !ok {
		return doc
	}
	if _, ok := meta["name"].(string); !ok {
		return doc
	}
	meta["name"] = name
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return doc
	}
	return string(b)
}

// header is the line the paired diff is introduced with.
//
// Both real names are named. The reader is looking at a resource that does not
// exist under the base name on either side, and a header that showed only the
// base would be describing an object that is nowhere in the cluster.
func (p hashPair) header() string {
	return "=== " + groupKind(p.desired.Group, p.desired.Kind) + " " +
		p.desired.Namespace + "/" + p.base + "  (hash rotated: " +
		p.live.Name + " → " + p.desired.Name + ")"
}
