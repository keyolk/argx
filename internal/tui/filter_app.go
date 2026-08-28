package tui

import (
	"sort"
	"strings"

	"github.com/keyolk/argx/internal/argocd"
)

// appFilterQuery is the application list's filter.
//
// Like the resource filter it searches several axes separately, because a
// project called `web` and an application called `web` are different questions.
// Labels get their own syntax rather than being folded into the free-text
// haystack: a label value like `prod` appears in half the cluster names too, so
// an unprefixed match cannot express "applications whose env label is prod".
//
//	web                    name, project, destination, or status
//	kind:...               (not applicable here; see the resource filter)
//	ctx:prod   c:prod      the Argo CD server
//	label:env=prod         a label key and value
//	l:env                  a label key, any value
//	-l:env                 applications *without* the label
//	proj:platform  p:...   the AppProject
//	ns:web                 destination namespace
//	cluster:apne2          destination cluster
//	sync:outofsync         sync status
//	health:degraded        health status
type appFilterQuery struct {
	raw string

	free    []string
	ctx     []string
	project []string
	ns      []string
	cluster []string
	sync    []string
	health  []string

	// labels are key/value requirements. An empty value means "the key is
	// present, whatever it holds".
	labels []labelTerm
}

// labelTerm is one label requirement.
type labelTerm struct {
	key   string
	value string
	// negate inverts the match, so `-l:env` finds applications with no env
	// label — the way to find the ones a labelling convention missed.
	negate bool
	// hasValue distinguishes `l:env=` (an empty value) from `l:env` (any).
	hasValue bool
}

// parseAppFilter splits a query into its per-field terms.
func parseAppFilter(q string) appFilterQuery {
	f := appFilterQuery{raw: q}
	for _, term := range strings.Fields(strings.ToLower(q)) {
		negate := false
		if strings.HasPrefix(term, "-") && len(term) > 1 {
			negate, term = true, term[1:]
		}

		field, value, ok := strings.Cut(term, ":")
		if !ok || value == "" {
			// A trailing `label:` the user is still typing is treated as free
			// text rather than as a term that matches nothing, so the list
			// narrows as they type instead of emptying and refilling.
			f.free = append(f.free, strings.TrimSuffix(term, ":"))
			continue
		}

		switch field {
		case "label", "l", "labels":
			k, v, hasValue := strings.Cut(value, "=")
			f.labels = append(f.labels, labelTerm{
				key: k, value: v, negate: negate, hasValue: hasValue,
			})
		case "ctx", "context", "c":
			f.ctx = append(f.ctx, value)
		case "proj", "project", "p":
			f.project = append(f.project, value)
		case "ns", "namespace", "n":
			f.ns = append(f.ns, value)
		case "cluster", "dest", "destination":
			f.cluster = append(f.cluster, value)
		case "sync":
			f.sync = append(f.sync, value)
		case "health":
			f.health = append(f.health, value)
		default:
			// An unknown prefix is far likelier to be a name containing a colon
			// than a typo'd field.
			f.free = append(f.free, term)
		}
	}
	return f
}

func (f appFilterQuery) empty() bool    { return strings.TrimSpace(f.raw) == "" }
func (f appFilterQuery) String() string { return f.raw }

// match reports whether an application satisfies every term.
func (f appFilterQuery) match(a *argocd.Application) bool {
	if f.empty() {
		return true
	}

	if len(f.free) > 0 {
		// An unprefixed term searches everything the row shows, so what is on
		// screen is what can be found: typing a revision you can see and
		// getting no result is the kind of gap that makes a search untrusted.
		src, _ := a.PrimarySource()
		hay := strings.ToLower(strings.Join([]string{
			a.Name(),
			a.Context,
			a.Spec.Project,
			a.Spec.Destination.Namespace,
			a.Spec.Destination.Cluster(),
			a.Status.Sync.Status,
			a.Status.Health.Status,
			a.Status.Sync.Revision,
			src.TargetRevision,
			src.RepoURL,
			src.Path,
			src.Chart,
		}, " "))
		for _, t := range f.free {
			if !strings.Contains(hay, t) {
				return false
			}
		}
	}

	for _, t := range f.ctx {
		if !strings.Contains(strings.ToLower(a.Context), t) {
			return false
		}
	}
	for _, t := range f.project {
		if !strings.Contains(strings.ToLower(a.Spec.Project), t) {
			return false
		}
	}
	for _, t := range f.ns {
		if !strings.Contains(strings.ToLower(a.Spec.Destination.Namespace), t) {
			return false
		}
	}
	for _, t := range f.cluster {
		if !strings.Contains(strings.ToLower(a.Spec.Destination.Cluster()), t) {
			return false
		}
	}
	for _, t := range f.sync {
		if !strings.HasPrefix(strings.ToLower(a.Status.Sync.Status), t) {
			return false
		}
	}
	for _, t := range f.health {
		if !strings.HasPrefix(strings.ToLower(a.Status.Health.Status), t) {
			return false
		}
	}

	for _, t := range f.labels {
		if t.match(a.Metadata.Labels) != !t.negate {
			return false
		}
	}
	return true
}

// match reports whether a label set satisfies this term, ignoring negation —
// the caller applies that.
//
// The key is matched as a suffix as well as in full, because real label keys
// carry a domain prefix (`example.com/env`) that nobody wants to type. The
// suffix must start at a path separator so `l:env` cannot match `l:tenv`.
func (t labelTerm) match(labels map[string]string) bool {
	for k, v := range labels {
		lk := strings.ToLower(k)
		if lk != t.key && !strings.HasSuffix(lk, "/"+t.key) {
			continue
		}
		if !t.hasValue {
			return true
		}
		if strings.Contains(strings.ToLower(v), t.value) {
			return true
		}
	}
	return false
}

// appFilterHint is shown under the filter prompt so the fields are
// discoverable without opening help.
const appFilterHint = "name · label:env=prod · ctx: · proj: · ns: · cluster: · sync: · health:"

// ---- completion ----

// completionSource is what the prompt can complete against: the field names,
// and the label keys and values the loaded applications actually carry.
//
// Completions come from the data rather than a fixed list, because a label key
// is only useful if something is labelled with it — offering `l:team=` when no
// application has a team label wastes the reader's time.
type completionSource struct {
	labelKeys   []string
	labelValues map[string][]string
	contexts    []string
	projects    []string
	clusters    []string
	namespaces  []string
}

// buildCompletions indexes what the loaded applications can be filtered by.
func buildCompletions(apps []argocd.Application) *completionSource {
	c := &completionSource{labelValues: map[string][]string{}}

	keys := map[string]bool{}
	values := map[string]map[string]bool{}
	seen := func(set map[string]bool, v string) {
		if v != "" {
			set[v] = true
		}
	}
	ctxs, projs, clusters, nss := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}

	for i := range apps {
		a := &apps[i]
		seen(ctxs, a.Context)
		seen(projs, a.Spec.Project)
		seen(clusters, a.Spec.Destination.Cluster())
		seen(nss, a.Spec.Destination.Namespace)

		for k, v := range a.Metadata.Labels {
			// The bare suffix is what a reader types, so that is what is
			// offered; the full key is offered too for the rare collision.
			short := k
			if i := strings.LastIndex(k, "/"); i >= 0 {
				short = k[i+1:]
			}
			keys[short] = true
			if short != k {
				keys[k] = true
			}
			for _, kk := range []string{short, k} {
				if values[kk] == nil {
					values[kk] = map[string]bool{}
				}
				seen(values[kk], v)
			}
		}
	}

	c.labelKeys = sortedKeys(keys)
	for k, vs := range values {
		c.labelValues[k] = sortedKeys(vs)
	}
	c.contexts = sortedKeys(ctxs)
	c.projects = sortedKeys(projs)
	c.clusters = sortedKeys(clusters)
	c.namespaces = sortedKeys(nss)
	return c
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fieldPrefixes are the field names the prompt completes, in the order they are
// offered. The short aliases are deliberately absent: offering both `label:`
// and `l:` doubles the list without adding a capability.
var fieldPrefixes = []string{
	"label:", "ctx:", "proj:", "ns:", "cluster:", "sync:", "health:",
}

// syncValues and healthValues are fixed by Argo CD, so they are listed rather
// than harvested — a status nothing currently has is still worth offering.
var (
	syncValues   = []string{"synced", "outofsync", "unknown"}
	healthValues = []string{"healthy", "progressing", "degraded", "missing", "suspended", "unknown"}
)

// complete returns the candidates for the word the cursor sits in, and the
// range of that word, so the caller can replace it.
//
// Completion works on the word under the cursor rather than on the whole query:
// a filter is several terms, and only the one being typed should change.
func (c *completionSource) complete(query string, cursor int) (cands []string, start, end int) {
	r := []rune(query)
	if cursor > len(r) {
		cursor = len(r)
	}
	start = wordLeft(r, cursor)
	end = cursor
	word := strings.ToLower(string(r[start:end]))

	// A negation prefix is carried through untouched so `-l:env` completes on
	// the field after it.
	neg := ""
	if strings.HasPrefix(word, "-") {
		neg, word = "-", word[1:]
	}

	field, value, ok := strings.Cut(word, ":")
	if !ok {
		// Still typing the field name — or a bare word, which could become
		// one. Offer the fields that match what is typed so far.
		for _, p := range fieldPrefixes {
			if strings.HasPrefix(p, word) {
				cands = append(cands, neg+p)
			}
		}
		return cands, start, end
	}

	switch field {
	case "label", "l", "labels":
		key, val, hasVal := strings.Cut(value, "=")
		if !hasVal {
			for _, k := range c.labelKeys {
				if strings.HasPrefix(strings.ToLower(k), key) {
					// The `=` is appended because a key alone is a valid but
					// rarely-wanted filter, and typing it is the next thing
					// the reader does.
					cands = append(cands, neg+"label:"+k+"=")
				}
			}
			return cands, start, end
		}
		for _, v := range c.labelValues[key] {
			if strings.HasPrefix(strings.ToLower(v), val) {
				cands = append(cands, neg+"label:"+key+"="+v)
			}
		}
	case "ctx", "context", "c":
		cands = prefixed(neg+"ctx:", c.contexts, value)
	case "proj", "project", "p":
		cands = prefixed(neg+"proj:", c.projects, value)
	case "ns", "namespace", "n":
		cands = prefixed(neg+"ns:", c.namespaces, value)
	case "cluster", "dest", "destination":
		cands = prefixed(neg+"cluster:", c.clusters, value)
	case "sync":
		cands = prefixed(neg+"sync:", syncValues, value)
	case "health":
		cands = prefixed(neg+"health:", healthValues, value)
	}
	return cands, start, end
}

// prefixed builds candidates from the values matching what is typed.
func prefixed(prefix string, values []string, typed string) []string {
	var out []string
	for _, v := range values {
		if strings.HasPrefix(strings.ToLower(v), typed) {
			out = append(out, prefix+v)
		}
	}
	return out
}

// commonPrefix is the longest prefix every candidate shares, which is what a
// first Tab press inserts — the same behavior as a shell, where one press
// advances as far as is unambiguous and a second shows the choices.
func commonPrefix(cands []string) string {
	if len(cands) == 0 {
		return ""
	}
	p := cands[0]
	for _, c := range cands[1:] {
		for !strings.HasPrefix(strings.ToLower(c), strings.ToLower(p)) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}
