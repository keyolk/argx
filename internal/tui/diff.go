package tui

// A line-level diff over manifests. A full Myers implementation is not needed:
// manifests are small and mostly line-aligned, so an LCS over lines produces
// readable output and keeps argx free of a diff dependency.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/keyolk/argx/internal/argocd"
)

// ---- rendering helpers that belong with the fetch, not the view ----

func diffKey(group, kind, ns, name string) string {
	return group + "/" + kind + "/" + ns + "/" + name
}

// diffPair is the two documents Argo CD itself compares for one resource, and
// whether the entry belongs in a diff at all.
//
// Getting this pair right is the whole correctness of the diff view. Argo CD's
// own `diff` field is not populated on the managed-resources endpoint, so the
// text has to be computed here — but from the same two sides the server used,
// which are normalizedLiveState and *predictedLiveState*.
//
// predictedLiveState is not targetState. targetState is the raw desired
// manifest as it came out of git; predictedLiveState is what the controller
// produced by running that manifest through the diff config —
// spec.ignoreDifferences, the resource overrides, the built-in normalizers, and
// a merge against the live object — in StateDiffs(). Comparing a *normalized*
// live side against an *un-normalized* desired side is comparing two documents
// that were never meant to match: every field ignoreDifferences was written to
// silence, and every default the API server filled in, comes back as drift.
// That is the bug this pair exists to prevent, and it is why the UI asks the
// endpoint for exactly these two fields and never fetches targetState at all
// (resource-details.tsx: fields: ['items.normalizedLiveState',
// 'items.predictedLiveState', ...]).
//
// Hooks are dropped for the same reason the UI drops them: a Job that ran once
// during a sync is not drift, and listing it buries the changes that are.
func diffPair(it argocd.ResourceDiff) (live, desired string, ok bool) {
	if it.Hook {
		return "", "", false
	}
	// The endpoint sends the JSON literal "null", not an empty string, for a
	// side that does not exist — handleResourceCreateOrDeleteDiff writes
	// []byte("null") for a resource being created or pruned. Treating that as a
	// document would print "null" as the whole manifest and lose the "will be
	// created" label, so it is normalized to empty here.
	live = prettyJSON(nullToEmpty(it.NormalizedLiveState))
	desired = prettyJSON(nullToEmpty(it.PredictedLiveState))
	// Falling back keeps argx working against a server that answered without
	// the normalized fields — an older API, or a projection that did not ask
	// for them. The comparison is then the un-normalized one, which is worse
	// but is still the honest reading of what arrived.
	if live == "" && desired == "" {
		live = prettyJSON(nullToEmpty(it.LiveState))
		desired = prettyJSON(nullToEmpty(it.TargetState))
	}
	return live, desired, true
}

// nullToEmpty maps the JSON literal null onto the empty document.
func nullToEmpty(s string) string {
	if strings.TrimSpace(s) == "null" {
		return ""
	}
	return s
}

// renderDiff turns managed-resources entries into unified-diff text.
func renderDiff(items []argocd.ResourceDiff, want map[string]bool) []string {
	var out []string
	changed := 0
	for _, it := range items {
		if want != nil && !want[diffKey(it.Group, it.Kind, it.Namespace, it.Name)] {
			continue
		}
		live, target, ok := diffPair(it)
		if !ok || live == target {
			continue
		}
		changed++
		head := fmt.Sprintf("=== %s %s/%s", groupKind(it.Group, it.Kind), it.Namespace, it.Name)
		switch {
		case live == "":
			head += "  (will be created)"
		case target == "":
			head += "  (not in desired state — prune candidate)"
		}
		out = append(out, head)
		out = append(out, unifiedDiff(live, target)...)
		out = append(out, "")
	}
	if changed == 0 {
		return []string{"(no differences)"}
	}
	return out
}

func groupKind(group, kind string) string {
	if group == "" {
		return kind
	}
	return kind + "." + group
}

// prettyJSON re-indents a JSON manifest so the diff compares formatting-stable
// text. Input that is not JSON (an already-YAML manifest, or empty) is returned
// unchanged.
func prettyJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(b)
}

// unifiedDiff is a compact line diff with context.
//
// A full Myers implementation is not needed here: manifests are small and
// mostly line-aligned, so an LCS over lines produces readable output and keeps
// argx free of a diff dependency.
func unifiedDiff(a, b string) []string {
	al := splitLines(a)
	bl := splitLines(b)
	ops := lcsOps(al, bl)

	const ctxLines = 3
	// Mark which ops to keep: every change plus ctxLines of surrounding
	// context, so an unchanged 2000-line manifest does not scroll past.
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		lo, hi := i-ctxLines, i+ctxLines
		if lo < 0 {
			lo = 0
		}
		if hi >= len(ops) {
			hi = len(ops) - 1
		}
		for j := lo; j <= hi; j++ {
			keep[j] = true
		}
	}

	var out []string
	gap := false
	for i, op := range ops {
		if !keep[i] {
			gap = true
			continue
		}
		// The marker is emitted for a leading gap too, not only for gaps
		// between kept runs: without it a diff that starts mid-manifest reads
		// as if it started at line 1.
		if gap {
			out = append(out, "@@ ...")
		}
		gap = false
		out = append(out, string(op.kind)+op.text)
	}
	if gap {
		out = append(out, "@@ ...")
	}
	return out
}

type diffOp struct {
	kind byte // ' ', '+', '-'
	text string
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// lcsOps computes a line-level diff via longest common subsequence.
//
// The DP table is O(n*m); manifests large enough for that to hurt are truncated
// to a size where it does not, because a 20k-line diff is not something a human
// reads in a TUI anyway.
func lcsOps(a, b []string) []diffOp {
	const maxLines = 4000
	truncated := false
	if len(a) > maxLines {
		a, truncated = a[:maxLines], true
	}
	if len(b) > maxLines {
		b, truncated = b[:maxLines], true
	}

	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	if truncated {
		ops = append(ops, diffOp{' ', fmt.Sprintf("... (diff truncated at %d lines)", maxLines)})
	}
	return ops
}
