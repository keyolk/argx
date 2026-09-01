package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
)

func TestRenderDiffReportsNoDifferences(t *testing.T) {
	same := `{"spec":{"replicas":2}}`
	items := []argocd.ResourceDiff{{
		Kind: "Deployment", Name: "web", Namespace: "prod",
		NormalizedLiveState: same, PredictedLiveState: same,
	}}
	got := strings.Join(renderDiff(items, nil, false), "\n")
	if !strings.Contains(got, "no differences") {
		t.Errorf("identical states should report no differences, got:\n%s", got)
	}
}

// The diff must compare the two sides Argo CD compared — normalizedLiveState
// against predictedLiveState — not the raw documents either came from.
//
// This is the test that catches the whole class of false drift: here the raw
// live and raw target disagree on every field, and the server's own normalized
// pair agrees. Argo CD calls this application Synced, and so must argx.
func TestRenderDiffComparesTheNormalizedPair(t *testing.T) {
	items := []argocd.ResourceDiff{{
		Kind: "Deployment", Name: "web",
		LiveState:           `{"spec":{"replicas":9,"paused":false}}`,
		TargetState:         `{"spec":{"replicas":1}}`,
		NormalizedLiveState: `{"spec":{"replicas":2}}`,
		PredictedLiveState:  `{"spec":{"replicas":2}}`,
	}}
	got := strings.Join(renderDiff(items, nil, false), "\n")
	if !strings.Contains(got, "no differences") {
		t.Errorf("the server's normalized pair should win over the raw documents, got:\n%s", got)
	}
}

// targetState is the desired manifest *before* ignoreDifferences and the
// normalizers ran, so a field the application asked Argo CD to ignore still
// differs there. Diffing against it is how argx would report drift on a field
// somebody deliberately silenced — the fields must not be mixed across sides.
func TestRenderDiffIgnoresTargetStateWhenPredictedIsPresent(t *testing.T) {
	items := []argocd.ResourceDiff{{
		Kind: "Deployment", Name: "web",
		NormalizedLiveState: `{"spec":{"replicas":4}}`,
		// What git says, and what an ignoreDifferences on replicas exists to
		// stop argx from shouting about.
		TargetState:        `{"spec":{"replicas":1}}`,
		PredictedLiveState: `{"spec":{"replicas":4}}`,
	}}
	got := strings.Join(renderDiff(items, nil, false), "\n")
	if !strings.Contains(got, "no differences") {
		t.Errorf("an ignored field must not show as drift, got:\n%s", got)
	}
}

// A server that answered without the normalized fields still gets a diff: the
// un-normalized comparison is worse, but it is the honest reading of what
// arrived, and showing nothing would look like "no differences".
func TestRenderDiffFallsBackToRawStates(t *testing.T) {
	items := []argocd.ResourceDiff{{
		Kind: "Deployment", Name: "web",
		LiveState:   `{"spec":{"replicas":2}}`,
		TargetState: `{"spec":{"replicas":5}}`,
	}}
	got := strings.Join(renderDiff(items, nil, false), "\n")
	if strings.Contains(got, "no differences") {
		t.Errorf("without the normalized pair the raw one should still diff, got:\n%s", got)
	}
}

// Hooks are not drift. A Job that ran once during a sync is reported by the
// endpoint like any other managed resource, and listing it buries the changes
// that matter — the Argo CD UI drops it for the same reason.
func TestRenderDiffSkipsHooks(t *testing.T) {
	items := []argocd.ResourceDiff{{
		Kind: "Job", Name: "db-migrate", Hook: true,
		NormalizedLiveState: `{"spec":{"completions":1}}`,
		PredictedLiveState:  `{"spec":{"completions":2}}`,
	}}
	got := strings.Join(renderDiff(items, nil, false), "\n")
	if !strings.Contains(got, "no differences") {
		t.Errorf("a hook is not drift, got:\n%s", got)
	}
}

// The endpoint writes the JSON literal null — not an empty string — for the
// side that does not exist. Read as a document it prints "null" as the whole
// manifest and loses the label that says what is about to happen.
func TestRenderDiffTreatsJSONNullAsAMissingSide(t *testing.T) {
	created := []argocd.ResourceDiff{{
		Kind: "ConfigMap", Name: "new",
		NormalizedLiveState: "null",
		PredictedLiveState:  `{"data":{"a":"1"}}`,
	}}
	got := strings.Join(renderDiff(created, nil, false), "\n")
	if !strings.Contains(got, "will be created") {
		t.Errorf("a null live side means the resource is being created, got:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(strings.TrimLeft(line, "-+")) == "null" {
			t.Errorf("the null literal leaked into the diff as a document: %q", line)
		}
	}

	pruned := []argocd.ResourceDiff{{
		Kind: "ConfigMap", Name: "old",
		NormalizedLiveState: `{"data":{"a":"1"}}`,
		PredictedLiveState:  "null",
	}}
	if got := strings.Join(renderDiff(pruned, nil, false), "\n"); !strings.Contains(got, "prune candidate") {
		t.Errorf("a null desired side means the resource is a prune candidate, got:\n%s", got)
	}
}

func TestRenderDiffShowsAddedAndRemovedLines(t *testing.T) {
	items := []argocd.ResourceDiff{{
		Kind: "Deployment", Name: "web", Namespace: "prod",
		NormalizedLiveState: `{"spec":{"replicas":2}}`,
		PredictedLiveState:  `{"spec":{"replicas":5}}`,
	}}
	got := strings.Join(renderDiff(items, nil, false), "\n")
	if !strings.Contains(got, "-") || !strings.Contains(got, "+") {
		t.Errorf("a changed value should produce both a - and a + line, got:\n%s", got)
	}
	if !strings.Contains(got, "Deployment") || !strings.Contains(got, "web") {
		t.Errorf("the header should identify the resource, got:\n%s", got)
	}
}

func TestRenderDiffLabelsCreatesAndPrunes(t *testing.T) {
	created := []argocd.ResourceDiff{{
		Kind: "ConfigMap", Name: "new", PredictedLiveState: `{"data":{"a":"1"}}`,
	}}
	if got := strings.Join(renderDiff(created, nil, false), "\n"); !strings.Contains(got, "will be created") {
		t.Errorf("a resource missing from the cluster should say so, got:\n%s", got)
	}

	pruned := []argocd.ResourceDiff{{
		Kind: "ConfigMap", Name: "old", NormalizedLiveState: `{"data":{"a":"1"}}`,
	}}
	if got := strings.Join(renderDiff(pruned, nil, false), "\n"); !strings.Contains(got, "prune candidate") {
		t.Errorf("a resource absent from the desired state should be flagged, got:\n%s", got)
	}
}

// A tree-scoped diff must show only the marked resources.
func TestRenderDiffHonorsResourceFilter(t *testing.T) {
	items := []argocd.ResourceDiff{
		{Kind: "Deployment", Name: "web", NormalizedLiveState: `{"a":1}`, PredictedLiveState: `{"a":2}`},
		{Kind: "Deployment", Name: "api", NormalizedLiveState: `{"a":1}`, PredictedLiveState: `{"a":3}`},
	}
	want := map[string]bool{diffKey("", "Deployment", "", "web"): true}

	got := strings.Join(renderDiff(items, want, false), "\n")
	if !strings.Contains(got, "web") {
		t.Errorf("the selected resource should appear, got:\n%s", got)
	}
	if strings.Contains(got, "api") {
		t.Errorf("an unselected resource should not appear, got:\n%s", got)
	}
}

// An unchanged 2000-line manifest must not scroll past; only changes plus a
// few lines of context are worth showing.
func TestUnifiedDiffElidesUnchangedRuns(t *testing.T) {
	var a, b []string
	for i := 0; i < 200; i++ {
		a = append(a, "same")
		b = append(b, "same")
	}
	b[100] = "different"

	out := unifiedDiff(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if len(out) > 20 {
		t.Errorf("a single change in 200 lines produced %d output lines", len(out))
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "different") {
		t.Errorf("the changed line is missing from:\n%s", joined)
	}
	if !strings.Contains(joined, "@@") {
		t.Errorf("elided runs should be marked, got:\n%s", joined)
	}
}

func TestPrettyJSONLeavesNonJSONAlone(t *testing.T) {
	yaml := "apiVersion: v1\nkind: ConfigMap"
	if got := prettyJSON(yaml); got != yaml {
		t.Errorf("non-JSON input should be returned unchanged, got %q", got)
	}
	if got := prettyJSON(""); got != "" {
		t.Errorf("empty input should stay empty, got %q", got)
	}
}

func TestPrettyJSONNormalizesFormatting(t *testing.T) {
	a := prettyJSON(`{"b":1,"a":2}`)
	b := prettyJSON(`{  "a" : 2,  "b" : 1  }`)
	if a != b {
		t.Errorf("the same object formatted differently should normalize equal:\n%s\n---\n%s", a, b)
	}
}

// A pathological manifest must not stall the UI in an O(n·m) DP table.
func TestUnifiedDiffTruncatesHugeInputs(t *testing.T) {
	var a, b []string
	for i := 0; i < 6000; i++ {
		a = append(a, "line-a")
		b = append(b, "line-b")
	}
	out := unifiedDiff(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if !strings.Contains(strings.Join(out, "\n"), "truncated") {
		t.Error("an oversized diff must say it was truncated rather than silently dropping lines")
	}
}

func TestShortRevAbbreviatesSHAsOnly(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	if got := shortRev(sha); got != "0123456" {
		t.Errorf("shortRev(sha) = %q, want the 7-char prefix", got)
	}
	if got := shortRev("1.2.3"); got != "1.2.3" {
		t.Errorf("a chart version should be left alone, got %q", got)
	}
}

// Alignment must be computed in display cells: a byte or rune count misaligns
// every column after a CJK name.
func TestTruncateAndPadUseCellWidth(t *testing.T) {
	s := "한글이름" // 4 runes, 8 cells
	if got := padRight(s, 10); lipglossWidth(got) != 10 {
		t.Errorf("padRight produced %d cells, want 10", lipglossWidth(got))
	}
	if got := truncate(s, 5); lipglossWidth(got) > 5 {
		t.Errorf("truncate produced %d cells, want at most 5", lipglossWidth(got))
	}
	if got := truncate("short", 100); got != "short" {
		t.Errorf("a string that fits should be untouched, got %q", got)
	}
	if got := truncate("anything", 0); got != "" {
		t.Errorf("a zero width should produce nothing, got %q", got)
	}
}

// lipglossWidth keeps the cell-width assertions readable without importing
// lipgloss into every test.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

// Rows are assembled from styled cells, so truncate() is routinely handed a
// string that already contains escape sequences. Cutting one mid-sequence sends
// a broken control code to the terminal, and dropping the trailing reset bleeds
// the color into every following row — which is what wrecked the column
// alignment on a wide, colored application list.
func TestTruncatePreservesStyledStrings(t *testing.T) {
	const (
		cyan  = "\x1b[38;2;56;189;248m"
		reset = "\x1b[0m"
	)
	line := "abc " + cyan + "0123456789" + reset + " tail" // 19 cells

	for _, w := range []int{20, 19, 12, 8, 5, 1} {
		got := truncate(line, w)

		if cells := lipgloss.Width(got); cells > w {
			t.Errorf("truncate(w=%d) produced %d cells", w, cells)
		}
		// Escape bytes carry no width, so a truncation that has room to spare
		// is losing visible characters to sequence bytes.
		if w <= 19 && lipgloss.Width(got) < w-1 {
			t.Errorf("truncate(w=%d) produced only %d cells (%q) — escape bytes are being counted as width",
				w, lipgloss.Width(got), got)
		}
		if strings.Contains(got, "\x1b") && !strings.Contains(got, reset) {
			t.Errorf("truncate(w=%d) = %q — a styled result must end with a reset, or the color bleeds into the next row",
				w, got)
		}
		if hasPartialEscape(got) {
			t.Errorf("truncate(w=%d) = %q — an escape sequence was cut mid-way", w, got)
		}
	}
}

// hasPartialEscape reports whether the string ends inside an unterminated CSI
// sequence: ESC [ ... with no final byte in the @-~ range.
func hasPartialEscape(s string) bool {
	i := strings.LastIndex(s, "\x1b")
	if i < 0 {
		return false
	}
	rest := s[i:]
	if len(rest) < 2 || rest[1] != '[' {
		return true // a bare ESC, or ESC followed by nothing
	}
	for _, c := range rest[2:] {
		if c >= '@' && c <= '~' {
			return false
		}
	}
	return true
}

// The whole point of the width budget is that a full row fits. A wide list with
// a long revision must not overflow into a terminal wrap.
func TestStyledRowFitsItsWidth(t *testing.T) {
	const cyan = "\x1b[38;2;56;189;248m"
	const reset = "\x1b[0m"

	// Five styled cells, as renderApps assembles a row.
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString(cyan + strings.Repeat("x", 20) + reset + " ")
	}
	row := b.String()

	for _, w := range []int{100, 80, 60, 40} {
		got := truncate(row, w)
		if cells := lipgloss.Width(got); cells > w {
			t.Errorf("a %d-cell row truncated to %d cells, want at most %d",
				lipgloss.Width(row), cells, w)
		}
	}
}
