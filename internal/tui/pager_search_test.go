package tui

import (
	"strings"
	"testing"
)

// A rendered pod manifest, trimmed to what the tests need.
var manifestLines = strings.Split(`{
  "apiVersion": "v1",
  "kind": "Pod",
  "metadata": {
    "name": "web-abc12",
    "managedFields": [
      {
        "manager": "kubelet",
        "fieldsV1": {
          "f:spec": {
            "f:containers": {
              "f:image": {}
            }
          }
        }
      }
    ],
    "namespace": "web"
  },
  "spec": {
    "containers": [
      {
        "name": "fluent-bit",
        "image": "registry/fluent-bit:3.2.3",
        "resources": {
          "limits": {
            "cpu": "100m",
            "memory": "100Mi"
          }
        }
      },
      {
        "name": "app",
        "image": "registry/app:1.0.15",
        "resources": {
          "limits": {
            "memory": "512Mi"
          }
        }
      }
    ]
  }
}`, "\n")

// pagerModel puts a model on the manifest view with the given content.
func pagerModel(t *testing.T, lines []string) *Model {
	t.Helper()
	m := appModel(t, nil)
	m.screen = screenManifest
	m.pager = lines
	m.pagerTitle = "manifest"
	return m
}

// A line reading `"image": "..."` says nothing about which container it belongs
// to, and the lines that would have said are exactly what a grep removes.
func TestSearchLabelsMatchesWithTheirPath(t *testing.T) {
	m := pagerModel(t, manifestLines)
	m.pagerFilt = "image"
	res := m.searchPager()

	flat := stripANSI(strings.Join(res.lines, "\n"))
	for _, want := range []string{
		"spec.containers[0].image",
		"spec.containers[1].image",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the path %q is missing:\n%s", want, flat)
		}
	}
	// The index is what distinguishes the two, so a path without it is a
	// wrong answer rather than a vague one.
	if strings.Contains(flat, "spec[0]") {
		t.Errorf("a path lost the key that opened its array:\n%s", flat)
	}
}

// Each match is shown with the lines around it — the context is half of what
// makes the path meaningful.
func TestSearchShowsContext(t *testing.T) {
	m := pagerModel(t, manifestLines)
	m.pagerFilt = "512Mi"
	res := m.searchPager()

	flat := stripANSI(strings.Join(res.lines, "\n"))
	if !strings.Contains(flat, "512Mi") {
		t.Fatalf("the match itself is missing:\n%s", flat)
	}
	if !strings.Contains(flat, "limits") {
		t.Errorf("the surrounding lines are missing:\n%s", flat)
	}
	if !strings.Contains(flat, "spec.containers[1].resources.limits.memory") {
		t.Errorf("the path is wrong or missing:\n%s", flat)
	}
}

// Overlapping windows are merged. Printing both in full would show the lines
// between them twice, and the second copy would sit under a label describing
// the first — which is how a search starts lying.
func TestOverlappingContextIsNotRepeated(t *testing.T) {
	m := pagerModel(t, manifestLines)
	// Two adjacent matches: `"name"` and `"image"` are one line apart.
	m.pagerFilt = "fluent-bit"
	res := m.searchPager()

	flat := stripANSI(strings.Join(res.lines, "\n"))
	// The image line must appear once, not once per nearby match.
	if n := strings.Count(flat, "registry/fluent-bit:3.2.3"); n > 1 {
		t.Errorf("a line was shown %d times:\n%s", n, flat)
	}
}

// Every hit is labelled, including one whose line was already on screen as
// another match's context.
func TestEveryMatchIsLabelled(t *testing.T) {
	m := pagerModel(t, manifestLines)
	m.pagerFilt = "memory"
	res := m.searchPager()

	flat := stripANSI(strings.Join(res.lines, "\n"))
	labels := 0
	for _, l := range strings.Split(flat, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "spec.containers") &&
			strings.Contains(l, "memory") {
			labels++
		}
	}
	if labels != len(res.hitRows) {
		t.Errorf("%d matches but %d labels:\n%s", len(res.hitRows), labels, flat)
	}
}

// hitRows point at the matches so n and N step between them rather than
// through the context around them.
func TestHitRowsPointAtMatches(t *testing.T) {
	m := pagerModel(t, manifestLines)
	m.pagerFilt = "image"
	res := m.searchPager()

	if len(res.hitRows) == 0 {
		t.Fatal("no hits recorded")
	}
	for _, r := range res.hitRows {
		if r < 0 || r >= len(res.lines) {
			t.Fatalf("hit row %d is outside the rendered lines (%d)", r, len(res.lines))
		}
		if !strings.Contains(strings.ToLower(stripANSI(res.lines[r])), "image") {
			t.Errorf("hit row %d does not point at a match: %q", r, stripANSI(res.lines[r]))
		}
	}
}

// ---- noise ----

// managedFields is 39% of a real pod manifest; leaving it in buries every real
// match in a search.
func TestManagedFieldsAreHiddenByDefault(t *testing.T) {
	m := pagerModel(t, manifestLines)

	shown := stripANSI(strings.Join(m.searchPager().lines, "\n"))
	if strings.Contains(shown, "managedFields") {
		t.Errorf("managedFields should be hidden by default:\n%s", shown)
	}
	if strings.Contains(shown, "f:containers") {
		t.Errorf("the contents of managedFields should be hidden too:\n%s", shown)
	}
	// Everything after it must survive — a block skip that ran long would eat
	// the rest of the document.
	for _, want := range []string{`"namespace": "web"`, `"name": "app"`} {
		if !strings.Contains(shown, want) {
			t.Errorf("the skip consumed too much — %q is missing:\n%s", want, shown)
		}
	}
}

// Hidden is not gone: the count says how much, and M shows it.
func TestNoiseToggleRevealsAndCounts(t *testing.T) {
	m := pagerModel(t, manifestLines)

	if n := m.noiseHidden(); n == 0 {
		t.Fatal("the hidden-line count should be reported")
	}
	if !strings.Contains(m.View(), "lines hidden") {
		t.Errorf("the status line should say lines are hidden:\n%s", m.View())
	}

	press(t, m, "M")
	if !m.showNoise {
		t.Fatal("M should reveal the bookkeeping fields")
	}
	shown := stripANSI(strings.Join(m.searchPager().lines, "\n"))
	if !strings.Contains(shown, "managedFields") {
		t.Errorf("M did not reveal managedFields:\n%s", shown)
	}
	if m.noiseHidden() != 0 {
		t.Error("nothing is hidden once the toggle is on")
	}

	press(t, m, "M")
	if m.showNoise {
		t.Error("M should toggle back")
	}
}

// A search runs over what is displayed: a match inside hidden noise would be a
// result the reader cannot see.
func TestSearchDoesNotMatchHiddenNoise(t *testing.T) {
	m := pagerModel(t, manifestLines)
	m.pagerFilt = "kubelet" // only appears inside managedFields

	if n := len(m.searchPager().hitRows); n != 0 {
		t.Errorf("%d matches found inside hidden content", n)
	}

	press(t, m, "M")
	if n := len(m.searchPager().hitRows); n == 0 {
		t.Error("the match should be findable once the content is shown")
	}
}

// Logs are text, not a document: a log line beginning with "managedFields" is
// content, and dropping it would be a wrong answer.
func TestNoiseFilterIsStructuredOnly(t *testing.T) {
	m := pagerModel(t, []string{
		`managedFields: something a program logged`,
		`another line`,
	})
	m.screen = screenLogs

	shown := strings.Join(m.searchPager().lines, "\n")
	if !strings.Contains(shown, "managedFields") {
		t.Errorf("a log line was filtered as though it were a manifest field:\n%s", shown)
	}
	if m.noiseHidden() != 0 {
		t.Error("logs have nothing to hide")
	}
}

// ---- paths ----

func TestJSONPaths(t *testing.T) {
	paths := jsonPaths(manifestLines)

	want := map[string]string{
		`"kind": "Pod"`:                  "kind",
		`"name": "fluent-bit"`:           "spec.containers[0].name",
		`"image": "registry/app:1.0.15"`: "spec.containers[1].image",
		`"memory": "512Mi"`:              "spec.containers[1].resources.limits.memory",
		`"cpu": "100m"`:                  "spec.containers[0].resources.limits.cpu",
	}
	for line, wantPath := range want {
		found := false
		for i, l := range manifestLines {
			if !strings.Contains(l, line) {
				continue
			}
			found = true
			if paths[i] != wantPath {
				t.Errorf("%s\n  path = %q, want %q", line, paths[i], wantPath)
			}
			break
		}
		if !found {
			t.Errorf("the fixture no longer contains %s", line)
		}
	}
}

// A diff's markers and headers are not part of the document, and a diff line's
// leading +/- must not shift the path.
func TestJSONPathsHandleDiffMarkers(t *testing.T) {
	lines := []string{
		"=== Deployment prod/web",
		` {`,
		`   "spec": {`,
		`     "replicas": 2,`,
		`-    "image": "old",`,
		`+    "image": "new",`,
		`   }`,
		` }`,
		"@@ ...",
	}
	paths := jsonPaths(lines)

	if paths[0] != "" || paths[8] != "" {
		t.Errorf("a diff header or hunk marker was given a path: %q, %q", paths[0], paths[8])
	}
	for _, i := range []int{4, 5} {
		if paths[i] != "spec.image" {
			t.Errorf("line %d (%q) has path %q, want spec.image", i, lines[i], paths[i])
		}
	}
	if paths[3] != "spec.replicas" {
		t.Errorf("path = %q, want spec.replicas", paths[3])
	}
}

// Nothing about the search may exceed the terminal.
func TestSearchOutputFitsTheTerminal(t *testing.T) {
	for _, w := range []int{140, 120, 100, 80, 60} {
		m := pagerModel(t, manifestLines)
		m.width, m.height = w, 20
		m.pagerFilt = "image"

		for i, line := range strings.Split(m.View(), "\n") {
			if got := lipglossWidth(line); got > w {
				t.Errorf("w=%d line %d is %d cells:\n%q", w, i, got, line)
			}
		}
	}
}
