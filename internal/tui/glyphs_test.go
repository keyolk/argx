package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
)

// allIconSets is what every property in this file is checked against: a glyph
// set that renders correctly in isolation but breaks a column is worse than no
// icons at all.
var allIconSets = []struct {
	env string
	set iconSet
}{
	{"unicode", iconsUnicode},
	{"nerd", iconsNerd},
	{"ascii", iconsASCII},
}

func TestIconSetSelection(t *testing.T) {
	tests := []struct {
		name       string
		icons      string // ARGX_ICONS
		ascii      string // ARGX_ASCII
		configured string // the config file's `icons:`
		want       iconSet
	}{
		{"nothing set", "", "", "", iconsUnicode},
		{"env nerd", "nerd", "", "", iconsNerd},
		{"env nerdfont", "nerdfont", "", "", iconsNerd},
		{"env nf", "nf", "", "", iconsNerd},
		{"env is case-insensitive", "NERD", "", "", iconsNerd},
		{"env ascii", "ascii", "", "", iconsASCII},
		{"env plain", "plain", "", "", iconsASCII},
		{"env unicode", "unicode", "", "", iconsUnicode},

		{"config nerd", "", "", "nerd", iconsNerd},
		{"config ascii", "", "", "ascii", iconsASCII},
		{"config tolerates spacing", "", "", "  Nerd  ", iconsNerd},

		// One session on a terminal without the font must be able to opt out
		// without editing a file every other session shares.
		{"env beats config", "ascii", "", "nerd", iconsASCII},

		// ARGX_ASCII shipped first and is what a dotfile already sets.
		{"legacy ascii switch", "", "1", "", iconsASCII},
		{"explicit env beats the legacy switch", "nerd", "1", "", iconsNerd},
		{"config beats the legacy switch", "", "1", "nerd", iconsNerd},

		// A typo should leave argx legible rather than refusing to start.
		{"unknown env falls through", "emoji", "", "", iconsUnicode},
		{"unknown env falls through to config", "emoji", "", "nerd", iconsNerd},
		{"unknown config falls back", "", "", "emoji", iconsUnicode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ARGX_ICONS", tt.icons)
			t.Setenv("ARGX_ASCII", tt.ascii)
			if got := resolveIconSet(tt.configured); got != tt.want {
				t.Errorf("ARGX_ICONS=%q ARGX_ASCII=%q icons=%q → %v, want %v",
					tt.icons, tt.ascii, tt.configured, got, tt.want)
			}
		})
	}
}

// Every glyph must be exactly one cell. A two-cell icon pushes the column it
// sits in, and the whole render path is built on columns starting where the
// header says they do.
func TestEveryGlyphIsSingleWidth(t *testing.T) {
	for _, tc := range allIconSets {
		t.Setenv("ARGX_ICONS", tc.env)
		g := newGlyphs()

		single := map[string]string{
			"marked": g.marked, "unmarked": g.unmarked, "cursor": g.cursor,
			"editable": g.editable, "noHealth": g.noHealth,
			"synced": g.synced, "outOfSync": g.outOfSync,
			"healthy": g.healthy, "progressing": g.progressing,
			"degraded": g.degraded, "missing": g.missing,
			"suspended": g.suspended, "unknown": g.unknown,
			"revision": g.revision, "branchRef": g.branchRef, "tagRef": g.tagRef,
			"cluster": g.cluster, "namespace": g.namespace, "project": g.project,
			"server": g.server, "clock": g.clock, "person": g.person,
			"tabResources": g.tabResources, "tabHistory": g.tabHistory,
			"tabDetails": g.tabDetails, "kindDefault": g.kindDefault,
		}
		for name, s := range single {
			if s == "" {
				continue
			}
			if w := lipgloss.Width(s); w != 1 {
				t.Errorf("%s: %s is %d cells (%q), want 1", tc.env, name, w, s)
			}
		}
		for kind, s := range g.kinds {
			if w := lipgloss.Width(s); w != 1 {
				t.Errorf("%s: kind %s is %d cells (%q), want 1", tc.env, kind, w, s)
			}
		}
	}
}

// The ASCII set must contain nothing outside 7-bit ASCII — that is its entire
// purpose, and a single stray box-drawing character defeats it.
func TestASCIISetIsPureASCII(t *testing.T) {
	t.Setenv("ARGX_ICONS", "ascii")
	g := newGlyphs()

	for name, s := range map[string]string{
		"branch": g.branch, "corner": g.corner, "pipe": g.pipe, "blank": g.blank,
		"marked": g.marked, "unmarked": g.unmarked, "cursor": g.cursor,
		"editable": g.editable, "sep": g.sep, "tabSep": g.tabSep,
		"noHealth": g.noHealth, "synced": g.synced, "outOfSync": g.outOfSync,
		"healthy": g.healthy, "degraded": g.degraded,
	} {
		for _, r := range s {
			if r > 127 {
				t.Errorf("the ascii set's %s contains %q (U+%04X)", name, r, r)
			}
		}
	}
	if len(g.kinds) != 0 {
		t.Errorf("the ascii set should carry no kind icons, got %d", len(g.kinds))
	}
}

// Status must stay readable without color in every set. The Unicode and ASCII
// sets carry letters; the Nerd Font set carries shapes — either way, two
// different statuses must never render identically.
func TestStatusGlyphsAreDistinct(t *testing.T) {
	for _, tc := range allIconSets {
		t.Setenv("ARGX_ICONS", tc.env)
		g := newGlyphs()

		seen := map[string]string{}
		for _, st := range []string{"Healthy", "Progressing", "Degraded", "Missing", "Suspended", "Unknown"} {
			got := g.healthGlyph(st)
			if prev, dup := seen[got]; dup {
				t.Errorf("%s: %s and %s both render as %q — color is not enough on its own",
					tc.env, prev, st, got)
			}
			seen[got] = st
		}

		syncSeen := map[string]string{}
		for _, st := range []string{"Synced", "OutOfSync", "Unknown"} {
			got := g.syncGlyph(st)
			if prev, dup := syncSeen[got]; dup {
				t.Errorf("%s: sync %s and %s both render as %q", tc.env, prev, st, got)
			}
			syncSeen[got] = st
		}
	}
}

// A kind with no icon must not silently render as an empty cell, which would
// shift every name after it.
func TestKindIconAlwaysFillsItsCell(t *testing.T) {
	t.Setenv("ARGX_ICONS", "nerd")
	g := newGlyphs()

	for _, kind := range []string{"Pod", "Deployment", "SomeUnknownCRD", ""} {
		if got := g.kindIcon(kind); lipgloss.Width(got) != 1 {
			t.Errorf("kind %q → %q (%d cells), want exactly one", kind, got, lipgloss.Width(got))
		}
	}

	// Sets without kind icons must return nothing at all, so callers do not pad
	// a cell for an icon that is not there.
	for _, env := range []string{"unicode", "ascii"} {
		t.Setenv("ARGX_ICONS", env)
		if got := newGlyphs().kindIcon("Pod"); got != "" {
			t.Errorf("%s should carry no kind icon, got %q", env, got)
		}
	}
}

// prefix is where icon spacing is decided; a set with no icon must add nothing,
// or every column grows a leading space it did not budget for.
func TestPrefixAddsNothingWithoutAnIcon(t *testing.T) {
	g := newGlyphs()
	if got := g.prefix(""); got != "" {
		t.Errorf("prefix(\"\") = %q, want empty", got)
	}
	if got := g.prefix("x"); got != "x " {
		t.Errorf("prefix(%q) = %q, want a trailing space", "x", got)
	}
}

// The tab label already carries the number that selects it; a second numeral
// beside it reads as a typo.
func TestTabIconsDoNotDuplicateTheNumber(t *testing.T) {
	for _, env := range []string{"unicode", "ascii"} {
		t.Setenv("ARGX_ICONS", env)
		g := newGlyphs()
		for _, tb := range allTabs {
			if got := g.tabIcon(tb); got != "" {
				t.Errorf("%s: tab %v carries an icon %q — the label's number is the identifier",
					env, tb, got)
			}
		}
	}

	t.Setenv("ARGX_ICONS", "nerd")
	g := newGlyphs()
	for _, tb := range allTabs {
		if g.tabIcon(tb) == "" {
			t.Errorf("the nerd set should carry an icon for tab %v", tb)
		}
	}
}

// A branch and a tag look different, so a target revision says which it is
// without a network round trip.
func TestRefIconDistinguishesTagsFromBranches(t *testing.T) {
	t.Setenv("ARGX_ICONS", "nerd")
	g := newGlyphs()

	for _, rev := range []string{"v1.2.3", "1.2.3", "v2", "0.1.0-rc1"} {
		if got := g.refIcon(rev); got != g.tagRef {
			t.Errorf("%q should read as a tag", rev)
		}
	}
	for _, rev := range []string{"main", "master", "feature/tls", "release-1.2", "HEAD"} {
		if got := g.refIcon(rev); got != g.branchRef {
			t.Errorf("%q should read as a branch", rev)
		}
	}

	// Sets without ref icons return nothing rather than a wrong glyph.
	t.Setenv("ARGX_ICONS", "unicode")
	if got := newGlyphs().refIcon("main"); got != "" {
		t.Errorf("the unicode set should carry no ref icon, got %q", got)
	}
}

// ---- the property that matters: icons must not break the layout ----

// Every screen must fit the terminal in every icon set. Icons cost width, and a
// column that budgeted for a name but rendered an icon beside it overruns the
// row — which the terminal then wraps, shifting every column below.
func TestEveryIconSetFitsEveryScreen(t *testing.T) {
	for _, tc := range allIconSets {
		for _, size := range [][2]int{{168, 30}, {130, 24}, {100, 24}, {80, 24}, {60, 14}} {
			w, h := size[0], size[1]

			t.Setenv("ARGX_ICONS", tc.env)
			t.Setenv("NO_COLOR", "")
			t.Setenv("CLICOLOR_FORCE", "1")

			m := fixtureModel(t, w, h)
			m.gl = newGlyphs()
			m.st = newStyles()
			m.st.initContexts(len(m.fleet.Names()))
			fixtureTree(t, m)

			screens := []struct {
				name  string
				setup func()
			}{
				{"apps", func() { m.screen = screenApps }},
				{"resources", func() { m.screen, m.tab = screenApp, tabResources }},
				{"history", func() { m.screen, m.tab = screenApp, tabHistory }},
				{"details", func() { m.screen, m.tab = screenApp, tabDetails }},
			}
			for _, s := range screens {
				s.setup()
				out := m.View()

				if got := strings.Count(out, "\n") + 1; got != h {
					t.Errorf("%s %dx%d %s rendered %d lines, want %d",
						tc.env, w, h, s.name, got, h)
				}
				for i, line := range strings.Split(out, "\n") {
					if got := lipglossWidth(line); got > w {
						t.Errorf("%s %dx%d %s line %d is %d cells, want at most %d:\n%q",
							tc.env, w, h, s.name, i, got, w, line)
					}
				}
			}
		}
	}
}

// Columns must start at the same cell on every row and on the header, in every
// icon set. An icon that widened a cell without widening its column budget is
// exactly how the alignment broke before.
func TestColumnsStayAlignedAcrossIconSets(t *testing.T) {
	for _, tc := range allIconSets {
		for _, w := range []int{168, 130, 100, 80} {
			t.Setenv("ARGX_ICONS", tc.env)
			t.Setenv("NO_COLOR", "1")

			m := fleetModel(t, map[string][]string{
				"sb-prod": {"addons-kube-audit-rest-dataplatform-apne1-airflow-local-dev", "api"},
				"dl-prod": {"worker"},
			})
			m.gl = newGlyphs()
			m.st = newStyles()
			m.st.initContexts(len(m.fleet.Names()))
			for i := range m.apps {
				m.apps[i].Spec.Destination = argocd.Destination{
					Name: "prod-apne2-cluster-oqmz", Namespace: "kube-audit-rest",
				}
				m.apps[i].Status.Sync.Revision = "0123456789abcdef0123456789abcdef01234567"
				m.apps[i].Spec.Source = &argocd.Source{TargetRevision: "release-1.2.3"}
			}
			m.applyAppFilter()
			m.Update(tea.WindowSizeMsg{Width: w, Height: 20})

			nameW, _, _, ctxW := m.appColumns()
			wantCtx := 3 + 3 + nameW + 1

			lines := strings.Split(m.View(), "\n")
			for r := 0; r < len(m.appRows); r++ {
				line := lines[2+r]
				if ctxW > 0 {
					ctxName := m.apps[m.appRows[r]].Context
					i := strings.Index(line, ctxName)
					if i < 0 {
						t.Errorf("%s w=%d row %d: the context is missing:\n%q", tc.env, w, r, line)
						continue
					}
					// The icon sits inside the cell, before the name, so the
					// name starts one prefix later than the column does.
					start := lipglossWidth(line[:i]) - lipglossWidth(m.gl.prefix(m.gl.server))
					if start != wantCtx {
						t.Errorf("%s w=%d row %d: context cell starts at %d, want %d:\n%q",
							tc.env, w, r, start, wantCtx, line)
					}
				}
			}
		}
	}
}

// The tree's connectors must line up in every set: a kind icon that is one cell
// in one set and absent in another must still leave the names at a consistent
// indent for its own set.
func TestTreeIndentIsConsistentWithinASet(t *testing.T) {
	for _, tc := range allIconSets {
		t.Setenv("ARGX_ICONS", tc.env)
		t.Setenv("NO_COLOR", "1")

		m := fixtureModel(t, 120, 24)
		m.gl = newGlyphs()
		m.st = newStyles()
		m.st.initContexts(len(m.fleet.Names()))
		fixtureTree(t, m)
		m.screen, m.tab = screenApp, tabResources

		// The two sibling pods sit at the same depth and must start together.
		var podStarts []int
		for _, line := range strings.Split(m.View(), "\n") {
			i := strings.Index(line, "web-6d9f-")
			if i < 0 {
				continue
			}
			podStarts = append(podStarts, lipglossWidth(line[:i]))
		}
		if len(podStarts) != 2 {
			t.Fatalf("%s: expected both sibling pods on screen, found %d", tc.env, len(podStarts))
		}
		if podStarts[0] != podStarts[1] {
			t.Errorf("%s: sibling pods start at %v — the tree indent is inconsistent",
				tc.env, podStarts)
		}
	}
}
