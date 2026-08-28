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

	name   []string
	kind   []string
	status []string
	ns     []string
	labels []labelTerm
}

// parseResourceFilter splits a query into its per-field terms.
func parseResourceFilter(q string) resourceFilter {
	f := resourceFilter{raw: q}
	for _, term := range strings.Fields(strings.ToLower(q)) {
		field, value, ok := strings.Cut(term, ":")
		if !ok || value == "" {
			// An unprefixed term, or a trailing "kind:" the user is still
			// typing: treat it as a name search rather than dropping it, so the
			// list narrows as they type instead of jumping when the colon lands.
			f.name = append(f.name, strings.TrimSuffix(term, ":"))
			continue
		}
		negate := false
		if strings.HasPrefix(field, "-") && len(field) > 1 {
			negate, field = true, field[1:]
		}

		switch field {
		case "label", "l", "labels":
			k, v, hasValue := strings.Cut(value, "=")
			f.labels = append(f.labels, labelTerm{
				key: k, value: v, negate: negate, hasValue: hasValue,
			})
		case "kind", "k":
			f.kind = append(f.kind, value)
		case "status", "health", "s", "h":
			f.status = append(f.status, value)
		case "ns", "namespace", "n":
			f.ns = append(f.ns, value)
		case "name":
			f.name = append(f.name, value)
		default:
			// An unknown prefix is far more likely to be a name containing a
			// colon than a typo'd field, so search the whole term by name.
			f.name = append(f.name, term)
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
		if !strings.Contains(strings.ToLower(n.Name), t) {
			return false
		}
	}
	for _, t := range f.kind {
		// Prefix rather than substring: "kind:set" should not match
		// "StatefulSet" when the user meant a kind starting with "set". The
		// group is checked too, so "kind:apps" finds everything in apps/.
		if !strings.HasPrefix(strings.ToLower(n.Kind), t) &&
			!strings.HasPrefix(strings.ToLower(n.Group), t) {
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
		if !strings.HasPrefix(h, t) {
			return false
		}
	}
	for _, t := range f.ns {
		if !strings.Contains(strings.ToLower(n.Namespace), t) {
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
const resourceFilterHint = "name · kind:pod · status:degraded · ns:prod · label:app=web"
