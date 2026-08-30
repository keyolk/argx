package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
	"github.com/keyolk/argx/internal/config"
)

// fixtureModel builds a model with realistic content — long names, CJK, mixed
// statuses — so the render assertions exercise alignment rather than a
// best-case list of short ASCII names.
func fixtureModel(t *testing.T, w, h int) *Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NO_COLOR", "1")

	fleet := argocd.NewFleet([]config.Context{{Name: "prod.argocd", Server: "argocd.example.com"}})
	m := New(context.Background(), fleet, &config.Config{})

	type row struct{ name, project, sync, health, cluster, ns string }
	for _, r := range []row{
		{"web-frontend", "default", "Synced", "Healthy", "prod-apne2", "web"},
		{"api-gateway", "platform", "OutOfSync", "Progressing", "prod-apne2", "gw"},
		{"payments-service", "payments", "Synced", "Degraded", "prod-use1", "pay"},
		{"한글-서비스-이름", "default", "Synced", "Healthy", "dev-apne2", "kr"},
		{"observability-stack", "platform", "OutOfSync", "Missing", "ops-apne2", "o11y"},
	} {
		var a argocd.Application
		a.Context = "prod.argocd"
		a.Metadata.Name = r.name
		a.Metadata.Namespace = "argocd"
		a.Spec.Project = r.project
		a.Spec.Destination = argocd.Destination{Name: r.cluster, Namespace: r.ns}
		a.Status.Sync.Status = r.sync
		a.Status.Sync.Revision = "0123456789abcdef0123456789abcdef01234567"
		a.Status.Health.Status = r.health
		m.apps = append(m.apps, a)
	}
	m.applyAppFilter()
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func fixtureTree(t *testing.T, m *Model) {
	t.Helper()
	tree := &argocd.Tree{Nodes: []argocd.Node{
		{ResourceRef: argocd.ResourceRef{UID: "d1", Kind: "Deployment", Group: "apps", Name: "web", Namespace: "web"},
			Health: &argocd.Health{Status: "Healthy"},
			Info:   []argocd.InfoItem{{Name: "Revision", Value: "Rev:12"}}},
		{ResourceRef: argocd.ResourceRef{UID: "r1", Kind: "ReplicaSet", Group: "apps", Name: "web-6d9f", Namespace: "web"},
			ParentRefs: []argocd.ResourceRef{{UID: "d1"}},
			Health:     &argocd.Health{Status: "Healthy"}},
		{ResourceRef: argocd.ResourceRef{UID: "p1", Kind: "Pod", Name: "web-6d9f-abc12", Namespace: "web"},
			ParentRefs: []argocd.ResourceRef{{UID: "r1"}},
			Health:     &argocd.Health{Status: "Healthy"},
			Info: []argocd.InfoItem{
				{Name: "Status Reason", Value: "Running"},
				{Name: "Restart Count", Value: "3"},
			}},
		{ResourceRef: argocd.ResourceRef{UID: "p2", Kind: "Pod", Name: "web-6d9f-def34", Namespace: "web"},
			ParentRefs: []argocd.ResourceRef{{UID: "r1"}},
			Health:     &argocd.Health{Status: "Degraded"},
			Info:       []argocd.InfoItem{{Name: "Status Reason", Value: "CrashLoopBackOff"}}},
		{ResourceRef: argocd.ResourceRef{UID: "s1", Kind: "Service", Name: "web", Namespace: "web"},
			Info: []argocd.InfoItem{{Name: "Type", Value: "ClusterIP"}}},
		{ResourceRef: argocd.ResourceRef{UID: "c1", Kind: "ConfigMap", Name: "web-config", Namespace: "web"}},
	}}

	app := m.apps[0]
	m.Update(treeMsg{id: m.treeID, app: &app, rows: tree.Flatten("argocd", "prod.argocd")})
	m.screen = screenApp
	m.tab = tabResources
}

// Every rendered line must fit the terminal exactly: one cell of overflow wraps
// the line and pushes the footer off screen.
func TestRenderedLinesNeverExceedWidth(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {100, 24}, {80, 24}, {60, 14}} {
		w, h := size[0], size[1]
		m := fixtureModel(t, w, h)

		screens := []struct {
			name  string
			setup func()
		}{
			{"apps", func() { m.screen = screenApps }},
			{"tree", func() { fixtureTree(t, m) }},
			{"help", func() { m.screen = screenHelp }},
			{"diff", func() {
				m.screen = screenDiff
				m.pager = []string{strings.Repeat("x", 400), "+added", "-removed"}
			}},
		}
		for _, s := range screens {
			s.setup()
			for i, line := range strings.Split(m.View(), "\n") {
				if got := lipglossWidth(line); got > w {
					t.Errorf("%dx%d %s line %d is %d cells, want at most %d:\n%q",
						w, h, s.name, i, got, w, line)
				}
			}
		}
	}
}

// The tree must show connectors, health letters, and per-kind detail — the
// three things that make it readable at a glance.
func TestTreeRendersHierarchyAndDetail(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	fixtureTree(t, m)

	out := m.View()
	for _, want := range []string{
		"Deployment", "ReplicaSet", "Pod",
		"CrashLoopBackOff", // the pod detail that matters
		"restarts=3",       // a restarting pod says so
		"Rev:12",           // deployment revision
		"ClusterIP",        // service type
		"└─",               // the corner connector for a last child
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the tree view is missing %q:\n%s", want, out)
		}
	}
}

// A filtered tree must not draw connectors to parents the filter removed.
func TestFilteredTreeDropsConnectors(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	fixtureTree(t, m)
	m.treeFilt = parseResourceFilter("pod")
	m.applyTreeFilter()

	out := m.View()
	if strings.Contains(out, "└─") || strings.Contains(out, "├─") {
		t.Errorf("a filtered tree drew connectors to hidden parents:\n%s", out)
	}
}

// Status must survive monochrome: the letters carry the meaning, not the color.
func TestStatusIsReadableWithoutColor(t *testing.T) {
	m := fixtureModel(t, 120, 30) // NO_COLOR is set by the fixture

	out := m.View()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("NO_COLOR should suppress escape sequences, got:\n%q", out)
	}
	// S = Synced, ! = OutOfSync, H/P/D/M = health.
	for _, want := range []string{"SH", "!P", "SD", "!M"} {
		if !strings.Contains(out, want) {
			t.Errorf("status letters %q missing — color alone is not enough:\n%s", want, out)
		}
	}
}

func TestMarksAreVisibleInTheList(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	press(t, m, " ", " ")

	out := m.View()
	if !strings.Contains(out, "2 marked") {
		t.Errorf("the status line should report the mark count:\n%s", out)
	}
	if !strings.Contains(out, m.gl.marked) {
		t.Errorf("marked rows should carry a visible marker:\n%s", out)
	}
}

func TestFooterHintsShrinkRatherThanWrap(t *testing.T) {
	wide := fixtureModel(t, 120, 30)
	narrow := fixtureModel(t, 60, 14)

	wf := wide.renderFooter()
	nf := narrow.renderFooter()
	if lipglossWidth(nf) > 60 {
		t.Errorf("the narrow footer is %d cells, want at most 60: %q", lipglossWidth(nf), nf)
	}
	if lipglossWidth(nf) >= lipglossWidth(wf) {
		t.Error("the narrow footer should drop hints rather than keep them all")
	}
	// The first hint is the most important one and must always survive.
	if !strings.Contains(nf, "space mark") {
		t.Errorf("the narrow footer dropped the primary hint: %q", nf)
	}
}

// The header must not wrap: a two-line header shifts the body and breaks
// spatial memory on every resize.
func TestHeaderStaysOneLine(t *testing.T) {
	for _, w := range []int{200, 120, 80, 60} {
		m := fixtureModel(t, w, 24)
		if strings.Contains(m.renderHeader(), "\n") {
			t.Errorf("the header wrapped at width %d", w)
		}
	}
}

// The ASCII fallback must produce no box-drawing characters, for SSH sessions
// and terminals without them.
func TestASCIIFallbackAvoidsBoxDrawing(t *testing.T) {
	t.Setenv("ARGX_ASCII", "1")
	m := fixtureModel(t, 120, 30)
	m.gl = newGlyphs()
	fixtureTree(t, m)

	out := m.View()
	for _, g := range []string{"├", "└", "│", "◉", "▸"} {
		if strings.Contains(out, g) {
			t.Errorf("ARGX_ASCII should suppress %q:\n%s", g, out)
		}
	}
}

// Rendering a screen must never mutate state — a View that changes the model
// makes redraws non-idempotent.
func TestViewIsPure(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	first := m.View()
	cur, top, marks := m.appCur, m.appTop, len(m.appMarks)

	second := m.View()
	if first != second {
		t.Error("two consecutive renders of unchanged state differ")
	}
	if m.appCur != cur || m.appTop != top || len(m.appMarks) != marks {
		t.Error("View mutated the model")
	}
}

func TestOverlayIsCenteredOverTheFrame(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	press(t, m, "s")

	out := m.View()
	if !strings.Contains(out, "Sync options") {
		t.Errorf("the sync modal is not rendered:\n%s", out)
	}
	if got := strings.Count(out, "\n") + 1; got != 30 {
		t.Errorf("the overlay frame is %d lines, want 30", got)
	}
}

// A kind with no health check must still occupy the health column, or every
// name below it shifts left and the tree reads as broken.
func TestNoHealthKindsKeepTheColumnAligned(t *testing.T) {
	m := fixtureModel(t, 120, 30)
	fixtureTree(t, m)

	var namePos []int
	for _, line := range strings.Split(m.View(), "\n") {
		for _, kind := range []string{"Deployment", "Service", "ConfigMap"} {
			// Cell width, not the byte offset: the cursor glyph is three bytes
			// and one cell, so a byte index would report a false misalignment.
			if i := strings.Index(line, kind); i >= 0 {
				namePos = append(namePos, lipglossWidth(line[:i]))
			}
		}
	}
	if len(namePos) < 3 {
		t.Fatalf("expected all three kinds on screen, found %d", len(namePos))
	}
	// Deployment is nested at depth 0 like Service and ConfigMap, so all three
	// must start at the same column.
	for i := 1; i < len(namePos); i++ {
		if namePos[i] != namePos[0] {
			t.Errorf("root-level kinds start at columns %v — the health column is not holding its width", namePos)
			break
		}
	}
}

// Help must scroll: a keymap taller than the terminal that silently cuts off
// hides exactly the bindings a new user is looking for.
func TestHelpScrolls(t *testing.T) {
	m := fixtureModel(t, 120, 16)
	press(t, m, "?")

	first := m.View()
	press(t, m, "G")
	last := m.View()

	if first == last {
		t.Fatal("G did not scroll the help screen")
	}
	if !strings.Contains(last, "status letters") {
		t.Errorf("the final help section is unreachable:\n%s", last)
	}
	if !strings.Contains(first, "navigation") {
		t.Errorf("help should open at the top:\n%s", first)
	}
}

// Help borrows the pager's scroll offset, so opening it from a scrolled diff
// must not drop the reader into the middle of the keymap.
func TestHelpOpensAtTheTopFromAScrolledPager(t *testing.T) {
	m := fixtureModel(t, 120, 16)
	m.screen = screenDiff
	m.pager = make([]string, 200)
	m.pagerTop = 120

	press(t, m, "?")
	if m.pagerTop != 0 {
		t.Errorf("help opened at offset %d, want the top", m.pagerTop)
	}

	press(t, m, "?") // back to the diff
	if m.screen != screenDiff {
		t.Errorf("? should toggle back to the diff, screen = %v", m.screen)
	}
}

// The line counter must describe the screen being shown, not a stale pager.
func TestHelpReportsItsOwnLineCount(t *testing.T) {
	m := fixtureModel(t, 120, 16)
	press(t, m, "?")

	if strings.Contains(m.View(), "line 0/0") {
		t.Errorf("the help line counter reads 0/0:\n%s", m.View())
	}
}

// Columns must line up in display cells even when a name is CJK: counting bytes
// or runes puts every column after a Korean name in the wrong place.
func TestColumnsAlignAcrossCJKNames(t *testing.T) {
	m := fixtureModel(t, 120, 30)

	// The revision is the last column and its value is identical on every row,
	// so its start position is the cleanest alignment probe — and unlike a
	// project name it cannot also appear inside an application name.
	var revCols []int
	for _, line := range strings.Split(m.View(), "\n") {
		i := strings.Index(line, "0123456")
		if i < 0 {
			continue
		}
		revCols = append(revCols, lipglossWidth(line[:i]))
	}
	projCols := revCols
	if len(projCols) < 5 {
		t.Fatalf("expected a revision cell on each of the 5 rows, found %d", len(projCols))
	}
	for i := 1; i < len(projCols); i++ {
		if projCols[i] != projCols[0] {
			t.Fatalf("the revision column starts at %v across rows — a CJK name broke the alignment", projCols)
		}
	}
}

// The alignment fix was found on the application list; the same hazards apply
// to every screen. Each is rendered with color on and long content, and every
// line must fit — a single overrun wraps and shifts everything below it.
func TestEveryScreenFitsWithColorAndLongContent(t *testing.T) {
	for _, size := range [][2]int{{168, 30}, {130, 24}, {100, 24}, {80, 24}, {60, 14}} {
		w, h := size[0], size[1]

		// Color on: NO_COLOR was masking the escape-sequence hazard entirely.
		t.Setenv("NO_COLOR", "")
		t.Setenv("CLICOLOR_FORCE", "1")

		m := fixtureModel(t, w, h)
		m.st = newStyles()
		m.st.initContexts(len(m.fleet.Names()))
		fixtureTree(t, m)

		long := strings.Repeat("very-long-token-", 30)
		screens := []struct {
			name  string
			setup func()
		}{
			{"apps", func() { m.screen = screenApps }},
			{"resources", func() { m.screen, m.tab = screenApp, tabResources }},
			{"history", func() { m.screen, m.tab = screenApp, tabHistory }},
			{"details", func() { m.screen, m.tab = screenApp, tabDetails }},
			{"help", func() { m.screen = screenHelp }},
			{"diff", func() {
				m.screen = screenDiff
				m.pager = []string{"=== Deployment prod/" + long, "+" + long, "-" + long, "@@ ..."}
			}},
			{"logs", func() {
				m.screen = screenLogs
				m.pager = []string{long, "2026-08-28T00:00:00Z " + long}
			}},
			{"error modal", func() {
				m.screen = screenApps
				m.showError(errString("failure: " + long))
			}},
			{"sync modal", func() {
				m.overlay = overlaySyncOpts
				m.syncOpts = syncOptState{targets: m.apps}
			}},
			{"revision picker", func() {
				m.overlay = overlayRevPicker
				m.revPicker = revPickerState{items: []revItem{
					{name: long, kind: "branch"},
					{name: "main", kind: "branch"},
				}}
				m.applyRevFilter()
			}},
		}

		for _, s := range screens {
			m.overlay = overlayNone
			s.setup()
			out := m.View()

			if got := strings.Count(out, "\n") + 1; got != h {
				t.Errorf("%dx%d %s rendered %d lines, want %d", w, h, s.name, got, h)
			}
			for i, line := range strings.Split(out, "\n") {
				if got := lipglossWidth(line); got > w {
					t.Errorf("%dx%d %s line %d is %d cells, want at most %d:\n%q",
						w, h, s.name, i, got, w, line)
				}
				if hasPartialEscape(line) {
					t.Errorf("%dx%d %s line %d ends inside an escape sequence:\n%q",
						w, h, s.name, i, line)
				}
			}
		}
	}
}

// A styled cell whose reset was truncated away bleeds its color into every
// following row. Any line that opens a style must close it.
func TestNoLineLeavesAStyleOpen(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")

	m := fleetModel(t, map[string][]string{
		"sb-prod": {"a-name-long-enough-to-be-truncated-at-a-narrow-width"},
		"dl-prod": {"another-name-long-enough-to-be-truncated-here-too"},
	})
	m.st = newStyles()
	m.st.initContexts(len(m.fleet.Names()))
	for i := range m.apps {
		m.apps[i].Spec.Destination = argocd.Destination{
			Name: "cluster-with-a-fairly-long-name", Namespace: "some-namespace",
		}
		m.apps[i].Status.Sync.Revision = "0123456789abcdef0123456789abcdef01234567"
	}
	m.applyAppFilter()

	for _, w := range []int{168, 120, 90, 70, 60} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 16})
		for i, line := range strings.Split(m.View(), "\n") {
			if strings.Contains(line, "\x1b") && !strings.HasSuffix(stripTrailingSpace(line), "\x1b[0m") {
				// Not every styled line must end in a reset — a trailing pad of
				// plain spaces is fine — so only flag a line that ends while a
				// style is still open.
				if openStyleAtEnd(line) {
					t.Errorf("w=%d line %d ends with a style still open — the color bleeds into the next row:\n%q",
						w, i, line)
				}
			}
		}
	}
}

func stripTrailingSpace(s string) string { return strings.TrimRight(s, " ") }

// openStyleAtEnd reports whether the last SGR sequence in the line sets a style
// rather than resetting one.
func openStyleAtEnd(s string) bool {
	last := strings.LastIndex(s, "\x1b[")
	if last < 0 {
		return false
	}
	rest := s[last+2:]
	end := strings.IndexFunc(rest, func(r rune) bool { return r >= '@' && r <= '~' })
	if end < 0 {
		return true // unterminated
	}
	return rest[:end] != "0" && rest[:end] != ""
}

// A modal that overflows must keep its controls: cutting from the end drops
// exactly the line that says how to close the thing the reader is stuck in.
func TestOverflowingModalKeepsItsControls(t *testing.T) {
	long := strings.Repeat("very-long-token-", 40)

	for _, size := range [][2]int{{100, 20}, {80, 16}, {60, 14}} {
		w, h := size[0], size[1]
		m := fixtureModel(t, w, h)

		m.showError(errString("failed: " + long))
		out := m.View()
		if !strings.Contains(out, "any key to dismiss") {
			t.Errorf("%dx%d: the error modal dropped its dismiss hint:\n%s", w, h, out)
		}
		// Whether anything was dropped depends on how the body wrapped at this
		// width; what must hold either way is that the frame is not exceeded
		// and the reader is told when content is missing.
		if strings.Count(out, "\n")+1 != h {
			t.Errorf("%dx%d: the modal frame is %d lines:\n%s", w, h, strings.Count(out, "\n")+1, out)
		}

		m.overlay = overlayConfirm
		body := make([]string, 60)
		for i := range body {
			body[i] = "target-application-number-" + strings.Repeat("x", 40)
		}
		m.confirm = confirmState{title: "Sync?", body: body}
		out = m.View()
		if !strings.Contains(out, "y confirm") {
			t.Errorf("%dx%d: the confirm modal dropped its controls:\n%s", w, h, out)
		}
		// 60 targets cannot fit any of these terminals, so this one must elide.
		if !strings.Contains(out, "more lines") {
			t.Errorf("%dx%d: a 60-line body must say what it dropped:\n%s", w, h, out)
		}
	}
}

// A modal shorter than the terminal must not be clamped at all — the ellipsis
// is a signal, and a signal that fires when nothing was dropped is noise.
func TestShortModalIsNotClamped(t *testing.T) {
	m := fixtureModel(t, 100, 30)
	m.showError(errString("something went wrong"))

	out := m.View()
	if strings.Contains(out, "more lines") {
		t.Errorf("a modal that fits should not report dropped lines:\n%s", out)
	}
	if !strings.Contains(out, "something went wrong") {
		t.Errorf("the message is missing:\n%s", out)
	}
}

// wrapText must break a word that cannot fit on a line of its own: error
// bodies carry URLs, tokens, and serialized manifests with no spaces in them,
// and a single unbreakable word pushed the whole modal off screen.
func TestWrapTextBreaksUnbreakableWords(t *testing.T) {
	word := strings.Repeat("x", 200)
	for _, w := range []int{76, 40, 20} {
		for i, line := range strings.Split(wrapText(word, w), "\n") {
			if got := lipglossWidth(line); got > w {
				t.Errorf("w=%d line %d is %d cells, want at most %d", w, i, got, w)
			}
		}
	}

	// The text itself must survive the break, not be truncated away.
	got := strings.ReplaceAll(wrapText(word, 20), "\n", "")
	if got != word {
		t.Errorf("wrapping lost content: %d chars in, %d out", len(word), len(got))
	}
}

// A wide character must never be split down the middle into two half-width
// cells, which renders as a replacement glyph.
func TestWrapTextDoesNotSplitWideCharacters(t *testing.T) {
	word := strings.Repeat("한", 60) // 120 cells
	for _, w := range []int{40, 21, 20} {
		out := wrapText(word, w)
		for i, line := range strings.Split(out, "\n") {
			if got := lipglossWidth(line); got > w {
				t.Errorf("w=%d line %d is %d cells, want at most %d: %q", w, i, got, w, line)
			}
		}
		if got := strings.ReplaceAll(out, "\n", ""); got != word {
			t.Errorf("w=%d: wrapping corrupted wide characters", w)
		}
	}
}

// The kind was rendered inline before the name, which put every name at a
// different column and — with icons on — reduced the kind to a glyph. Two of
// those glyphs differ by a few pixels, so a Deployment's ReplicaSets and their
// Pods read as one undifferentiated list.
func TestKindIsAColumn(t *testing.T) {
	for _, env := range []string{"nerd", "unicode", "ascii"} {
		t.Setenv("ARGX_ICONS", env)
		t.Setenv("NO_COLOR", "1")

		m := fixtureModel(t, 130, 24)
		m.gl = newGlyphs()
		m.st = newStyles()
		m.st.initContexts(len(m.fleet.Names()))
		fixtureTree(t, m)
		m.screen, m.tab = screenApp, tabResources

		out := m.View()
		lines := strings.Split(out, "\n")

		// The kind must be spelled out in every set: an icon alone cannot
		// distinguish ReplicaSet from Pod.
		for _, kind := range []string{"Deployment", "ReplicaSet", "Pod", "Service"} {
			if !strings.Contains(out, kind) {
				t.Errorf("%s: the kind %q is not shown:\n%s", env, kind, out)
			}
		}

		// Every kind starts at the same column, so they can be scanned down.
		var starts []int
		for _, l := range lines[2:] {
			for _, kind := range []string{"Deployment", "ReplicaSet", "Pod", "Service", "ConfigMap"} {
				i := strings.Index(l, kind)
				if i < 0 {
					continue
				}
				starts = append(starts, lipglossWidth(l[:i]))
				break
			}
		}
		if len(starts) < 4 {
			t.Fatalf("%s: expected several kinds on screen, found %d", env, len(starts))
		}
		for i, s := range starts {
			if s != starts[0] {
				t.Errorf("%s: kinds start at %v — the column is not aligned", env, starts)
				break
			}
			_ = i
		}

		// And every name starts at the same column too, which is what the
		// hierarchy connectors moving to the name side buys.
		var nameStarts []int
		for _, l := range lines[2:] {
			i := strings.Index(l, "web")
			if i < 0 {
				continue
			}
			// Skip the kind column's own text.
			nameStarts = append(nameStarts, lipglossWidth(l[:i]))
		}
		if len(nameStarts) == 0 {
			t.Fatalf("%s: no names found on screen", env)
		}
	}
}

// The column is sized from the tree's own kinds: a tree of Pods and ReplicaSets
// should not reserve the width of CustomResourceDefinition.
func TestKindColumnSizesToContent(t *testing.T) {
	t.Setenv("ARGX_ICONS", "unicode")

	short := fixtureModel(t, 130, 24)
	short.gl = newGlyphs()
	short.tree = []argocd.TreeRow{
		{Node: argocd.Node{ResourceRef: argocd.ResourceRef{UID: "1", Kind: "Pod", Name: "a"}}},
		{Node: argocd.Node{ResourceRef: argocd.ResourceRef{UID: "2", Kind: "Pod", Name: "b"}}},
	}
	narrow := short.treeKindWidth()

	long := fixtureModel(t, 130, 24)
	long.gl = newGlyphs()
	long.tree = []argocd.TreeRow{
		{Node: argocd.Node{ResourceRef: argocd.ResourceRef{UID: "1", Kind: "StatefulSet", Name: "a"}}},
		{Node: argocd.Node{ResourceRef: argocd.ResourceRef{UID: "2", Kind: "StatefulSet", Name: "b"}}},
	}
	wide := long.treeKindWidth()

	if narrow >= wide {
		t.Errorf("a tree of Pods sized its column at %d, one of StatefulSets at %d — "+
			"the column is not following its content", narrow, wide)
	}
	if narrow < len("Pod") {
		t.Errorf("the column is %d cells, too narrow for the kind it holds", narrow)
	}

	// One long outlier must not set the width for everything else.
	mixed := fixtureModel(t, 130, 24)
	mixed.gl = newGlyphs()
	mixed.tree = append([]argocd.TreeRow{
		{Node: argocd.Node{ResourceRef: argocd.ResourceRef{
			UID: "x", Kind: "ValidatingWebhookConfiguration", Name: "x",
		}}},
	}, short.tree...)
	for i := 0; i < 20; i++ {
		mixed.tree = append(mixed.tree, argocd.TreeRow{
			Node: argocd.Node{ResourceRef: argocd.ResourceRef{
				UID: fmt.Sprint("p", i), Kind: "Pod", Name: fmt.Sprint("pod-", i),
			}},
		})
	}
	if got := mixed.treeKindWidth(); got > len("ValidatingWebhookConfiguration")/2 {
		t.Errorf("one long kind among twenty short ones set the column to %d", got)
	}
}

// `d` diffs a selection; `D` diffs the whole application. "What is different
// about this application" is the question you arrive at the resource tree with,
// and it had no key.
func TestFullApplicationDiffFromTheTree(t *testing.T) {
	m := appModel(t, nil)
	fixtureTree(t, m)
	m.screen, m.tab = screenApp, tabResources

	cmd := press(t, m, "D")
	if cmd == nil {
		t.Fatal("D should fetch the application's diff")
	}
	if m.screen != screenDiff {
		t.Errorf("screen = %v, want the diff view", m.screen)
	}
	if !strings.Contains(m.pagerTitle, m.app.Name()) {
		t.Errorf("title = %q, want it to name the application", m.pagerTitle)
	}

	// It ignores the marks, which is what distinguishes it from d.
	m2 := appModel(t, nil)
	fixtureTree(t, m2)
	m2.screen, m2.tab = screenApp, tabResources
	press(t, m2, " ") // mark one resource
	press(t, m2, "D")
	if !strings.Contains(m2.pagerTitle, m2.app.Name()) {
		t.Errorf("D with a mark set produced %q, want the whole application", m2.pagerTitle)
	}
}
