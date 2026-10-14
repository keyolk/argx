package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keyolk/tuikit/keys"
)

// One table, no key with two owners. F ("follow") was named in the footer and
// help while the mark handler, which ran first, swallowed the key — this test
// is what turns the next such collision into a CI failure.
//
// Unreachable (an uppercase letter a Korean input source cannot type) is
// judged per action below rather than per key: G also answers to end, Y to y.
func TestKeymapHasNoConflicts(t *testing.T) {
	for _, c := range baseKeymap.Conflicts(allStacks()...) {
		if c.Kind == keys.Unreachable {
			continue
		}
		t.Error(c)
	}
}

// Every action must be reachable under a Korean input source: by another key
// of its own binding, or — for uppercase-only actions like S, C, F — from the
// palette, which is opened with `:` and typed into, so it needs no uppercase.
func TestEveryActionIsReachableUnderAKoreanLayout(t *testing.T) {
	paletteHas := map[string]bool{}
	for _, st := range allStacks() {
		for _, e := range baseKeymap.Entries(st, keys.Opts{}) {
			paletteHas[e.Action] = true
		}
	}
	if !keys.ReachableUnderCJK(baseKeymap.Bindings()[0].Keys[0]) {
		t.Fatal("setup: quit must be typable")
	}
	for _, b := range baseKeymap.Bindings() {
		if slices.ContainsFunc(b.Keys, keys.ReachableUnderCJK) {
			continue
		}
		// The palette only lists screen actions; modal keys are not in it.
		if !paletteHas[b.Action] {
			t.Errorf("%s in %s: keys %v are all uppercase and the palette does not offer it",
				b.Action, b.Ctx, b.Keys)
		}
	}
}

// Every key help lists for an action runs that action somewhere — help is
// generated from the table, and this proves the table and the dispatcher are
// the same thing.
func TestEveryHelpEntryIsReachable(t *testing.T) {
	m := newTestModel(t, "alpha")
	secs := baseKeymap.Help(m.keyOpts(), allStacks()...)
	if len(secs) == 0 {
		t.Fatal("help is empty")
	}
	for _, sec := range secs {
		for _, e := range sec.Entries {
			for _, k := range e.Keys {
				found := false
				for _, st := range allStacks() {
					if b, ok := baseKeymap.ResolveRaw(st, key(k), keys.Opts{}); ok && b.Action == e.Action {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("help says %q runs %s, but no stack routes it there", k, e.Action)
				}
			}
		}
	}
}

// Every action in the table is handled: pressing its key on a screen where it
// is live changes something, or at least is not the same as an unbound key.
// A cheap check that catches an action constant added to the table and to no
// switch.
func TestEveryScreenActionIsDispatched(t *testing.T) {
	handled := map[string]bool{}
	src := ""
	for _, f := range []string{"update.go", "keys_apps.go", "marks.go", "appsets.go",
		"windows.go", "schedule_view.go", "contexts_view.go", "filter_input.go",
		"details.go", "containers.go", "palette.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src += string(b)
	}
	for _, b := range baseKeymap.Bindings() {
		if handled[b.Action] {
			continue
		}
		name := actionConstName(b.Action)
		if name == "" {
			t.Errorf("no constant for action %q", b.Action)
			continue
		}
		if !strings.Contains(src, "case "+name) && !strings.Contains(src, ", "+name+":") &&
			!strings.Contains(src, ", "+name+",") {
			t.Errorf("action %s (%s) is bound but no handler has a case for it", b.Action, name)
		}
		handled[b.Action] = true
	}
}

// actionConstName finds the Go constant naming action, by reading keys.go.
func actionConstName(action string) string {
	b, err := os.ReadFile("keys.go")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[1] == "=" && f[2] == `"`+action+`"` {
			return f[0]
		}
	}
	return ""
}

// The user's keys.yaml rebinds by action name and is validated against the
// same stacks the conflict test uses.
func TestKeysYamlOverridesAndRejectsConflicts(t *testing.T) {
	// Built first: newTestModel points XDG_CONFIG_HOME at its own temp dir.
	m := newTestModel(t, "alpha")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := keys.ConfigPath("argx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("apps.follow: [f]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	km, err := loadKeymap()
	if err != nil {
		t.Fatalf("a valid override was rejected: %v", err)
	}
	m.km = km
	m.handleKey(key("f"))
	if !m.autoRefresh {
		t.Error("f should follow after rebinding apps.follow to it")
	}
	if !strings.Contains(stripANSI(strings.Join(m.helpLines(), "\n")), "follow") {
		t.Error("help should still describe follow")
	}

	// d is the diff key on the list: claiming it for follow is a Duplicate.
	if err := os.WriteFile(path, []byte("apps.follow: [d]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadKeymap(); err == nil {
		t.Error("an override that gives d two owners must be rejected")
	}
	// j on the list hiding pinned help is a Shadowed conflict.
	if err := os.WriteFile(path, []byte("help: [j]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadKeymap(); err == nil {
		t.Error("an override that hides pinned help must be rejected")
	}
}

// The palette offers the screen's actions and runs the chosen one.
func TestPaletteRunsAnAction(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.handleKey(key(":"))
	if m.overlay != overlayPalette {
		t.Fatal(": should open the palette")
	}
	for _, r := range "appset" {
		m.handleKey(key(string(r)))
	}
	if got := m.paletteMatches(); len(got) == 0 || got[0].Action != aAppSetsToggle {
		t.Fatalf("palette matches = %v, want the appset toggle first", got)
	}
	m.handleKey(key("enter"))
	if m.overlay != overlayNone || m.screen != screenAppSets {
		t.Errorf("enter should run it: overlay=%v screen=%v", m.overlay, m.screen)
	}
}

// The palette is a text field: a jamo typed into it is query text, not the
// shortcut at the same physical position.
func TestPaletteKeepsHangulVerbatim(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.handleKey(key(":"))
	m.handleKey(key("ㅂ")) // physical q
	if m.palette.query != "ㅂ" {
		t.Errorf("query = %q, want the jamo verbatim", m.palette.query)
	}
	if m.overlay != overlayPalette {
		t.Error("ㅂ must not quit or close the palette")
	}
}

// The palette never offers what the screen's keys would not run.
func TestPaletteOffersOnlyTheScreensActions(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.handleKey(key(":"))
	for _, e := range m.palette.all {
		if e.Action == aDiffTool || e.Action == aHistRollback {
			t.Errorf("the application list palette offers %s", e.Action)
		}
	}
}
