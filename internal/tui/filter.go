package tui

import (
	"strings"

	"github.com/keyolk/argx/internal/argocd"
)

// resourceFilter is the RESOURCES tab's filter. Unlike the application list's
// single substring match, a resource tree is searched along three independent
// axes — a Deployment named "web" and a Pod named "web" are different questions
// — so each field is matched separately.
//
// The query syntax is a small set of prefixes, with anything unprefixed falling
// back to the name:
//
//	web            name contains "web"
//	kind:pod       kind is pod (prefix match, case-insensitive)
//	k:deploy       same, abbreviated
//	status:degraded  health status
//	s:degraded     same, abbreviated
//	ns:prod        namespace
//	label:app=web  a label key and value
//	l:app          a label key, any value
//	kind:pod web   both — terms are ANDed
//	!kind:pod      not a pod — a `!` prefix negates any field, name included
//	!web           name does not contain "web"
//
// Labels are only available for the kinds Argo CD reports networking for —
// Pods, Services, Ingresses. A label term therefore excludes every other kind
// rather than matching it vacuously, which is the reading that makes
// `l:app=web` mean what it looks like.
//
// Prefixes are matched case-insensitively and the whole query is lowercased,
// because Kubernetes kinds are conventionally capitalized and nobody types
// "kind:StatefulSet".
type resourceFilter struct {
	// raw is what the user typed, kept verbatim so the filter line echoes it
	// back unchanged.
	raw string

	name   []strTerm
	kind   []strTerm
	status []strTerm
	ns     []strTerm
	labels []labelTerm
}

// strTerm is one field requirement: a value to compare against, and whether
// the comparison is negated. Every non-label field in both the resource and
// application filters shares this shape, so a `!` prefix means the same thing
// wherever it appears.
type strTerm struct {
	value  string
	negate bool
}

// containsMatch reports whether t is satisfied by a haystack, honoring
// negation: a positive term needs the substring present, a negated one needs
// it absent.
func (t strTerm) containsMatch(haystack string) bool {
	return strings.Contains(haystack, t.value) != t.negate
}

// prefixMatch is containsMatch for fields matched by prefix rather than
// substring — kind and status, where a substring match would make
// "kind:set" wrongly find "StatefulSet".
func (t strTerm) prefixMatch(haystack string) bool {
	return strings.HasPrefix(haystack, t.value) != t.negate
}

// splitNegate strips a leading `!` and reports whether it was there. The `!`
// only counts as negation when something follows it — a bare "!" is a name
// search for a literal exclamation mark, which is rare but not nothing.
func splitNegate(s string) (string, bool) {
	if strings.HasPrefix(s, "!") && len(s) > 1 {
		return s[1:], true
	}
	return s, false
}

// parseResourceFilter splits a query into its per-field terms.
func parseResourceFilter(q string) resourceFilter {
	f := resourceFilter{raw: q}
	for _, raw := range strings.Fields(strings.ToLower(q)) {
		term, negate := splitNegate(raw)
		field, value, ok := strings.Cut(term, ":")
		if !ok || value == "" {
			// An unprefixed term, or a trailing "kind:" the user is still
			// typing: treat it as a name search rather than dropping it, so the
			// list narrows as they type instead of jumping when the colon lands.
			f.name = append(f.name, strTerm{strings.TrimSuffix(term, ":"), negate})
			continue
		}

		switch field {
		case "label", "l", "labels":
			k, v, hasValue := strings.Cut(value, "=")
			f.labels = append(f.labels, labelTerm{
				key: k, value: v, negate: negate, hasValue: hasValue,
			})
		case "kind", "k":
			f.kind = append(f.kind, strTerm{value, negate})
		case "status", "health", "s", "h":
			f.status = append(f.status, strTerm{value, negate})
		case "ns", "namespace", "n":
			f.ns = append(f.ns, strTerm{value, negate})
		case "name":
			f.name = append(f.name, strTerm{value, negate})
		default:
			// An unknown prefix is far more likely to be a name containing a
			// colon than a typo'd field, so search the whole term by name.
			f.name = append(f.name, strTerm{term, negate})
		}
	}
	return f
}

// empty reports whether the filter matches everything.
func (f resourceFilter) empty() bool { return strings.TrimSpace(f.raw) == "" }

// String is what the status line echoes.
func (f resourceFilter) String() string { return f.raw }

// match reports whether a node satisfies every term.
//
// Terms within a field are ANDed like terms across fields: typing more always
// narrows. An OR would make "kind:pod kind:service" mean something different
// from every other search in the app.
func (f resourceFilter) match(n argocd.Node) bool {
	if f.empty() {
		return true
	}
	for _, t := range f.name {
		if !t.containsMatch(strings.ToLower(n.Name)) {
			return false
		}
	}
	for _, t := range f.kind {
		// Prefix rather than substring: "kind:set" should not match
		// "StatefulSet" when the user meant a kind starting with "set". The
		// group is checked too, so "kind:apps" finds everything in apps/, and
		// negation excludes a resource matching on either.
		kind, group := strings.ToLower(n.Kind), strings.ToLower(n.Group)
		hit := strings.HasPrefix(kind, t.value) || strings.HasPrefix(group, t.value)
		if hit == t.negate {
			return false
		}
	}
	for _, t := range f.status {
		// A resource Argo CD reports no health for answers to "status:none",
		// which is the only way to find the kinds it does not health-check.
		h := strings.ToLower(n.HealthStatus())
		if h == "" {
			h = "none"
		}
		if !t.prefixMatch(h) {
			return false
		}
	}
	for _, t := range f.ns {
		if !t.containsMatch(strings.ToLower(n.Namespace)) {
			return false
		}
	}
	for _, t := range f.labels {
		if t.match(n.Labels()) != !t.negate {
			return false
		}
	}
	return true
}

// resourceFilterHint is shown under the filter prompt so the field prefixes are
// discoverable without opening help.
const resourceFilterHint = "name · kind:pod · status:degraded · ns:prod · label:app=web · !kind:pod"
