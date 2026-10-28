package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/keyolk/argx/internal/argocd"
	"github.com/keyolk/argx/internal/config"
)

// writeArgocdConfig lays down an Argo CD CLI config in the HOME newTestModel
// isolated, as `argocd login` would have left it.
func writeArgocdConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".config", "argocd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// After a login, the running client picks up the new session without a
// restart: that is the point of logging in from inside argx.
func TestReloadTokenHandsTheNewSessionToTheClient(t *testing.T) {
	m := newTestModel(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("ARGOCD_CONFIG", "")
	t.Setenv("ARGX_CONFIG", "")
	writeArgocdConfig(t, `contexts:
- name: test
  server: argocd.test
  user: test
users:
- name: test
  auth-token: fresh-token
current-context: test
`)

	if err := m.reloadToken("test"); err != nil {
		t.Fatal(err)
	}
	cl, _ := m.fleet.Client("test")
	if got := cl.Context().Token; got != "fresh-token" {
		t.Errorf("client token = %q, want fresh-token", got)
	}
}

// A context whose token comes from pass is not logged in: the CLI session would
// change and argx would go on using the pass token, so the login would look
// like it did nothing.
func TestLoginRefusesAContextWithAConfiguredSource(t *testing.T) {
	m := newTestModel(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("ARGOCD_CONFIG", "")
	t.Setenv("ARGX_CONFIG", "")
	writeArgocdConfig(t, `contexts:
- name: test
  server: argocd.test
  user: test
current-context: test
`)
	dir := filepath.Join(os.Getenv("HOME"), ".config", "argx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("contexts:\n  test:\n    token: pass:some/token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c, _ := cfg.Lookup("test")
	m.fleet = argocd.NewFleet([]config.Context{c})

	cmd := m.loginCmd(ctxRow("test", nil))
	if _, ok := cmd().(toastMsg); !ok {
		t.Fatal("a pass-sourced context should be refused with a note, not logged in")
	}
}

// L belongs to the context view without taking the keys that move through it.
func TestLoginKeyLeavesNavigationAlone(t *testing.T) {
	m := ctxModel(t, ctxRow("a", nil), ctxRow("test", nil))
	m.Update(key("j"))
	if m.ctxCur != 1 {
		t.Fatalf("j should still move the cursor, got %d", m.ctxCur)
	}
	_, cmd := m.Update(key("L"))
	if cmd == nil {
		t.Fatal("L should start a login for the row under the cursor")
	}
	// Calling the command only returns Bubble Tea's request to hand over the
	// terminal; nothing is executed until the program loop receives it. A
	// refusal or a lookup failure would come back as a toast or an error.
	switch msg := cmd().(type) {
	case toastMsg, errMsg:
		t.Errorf("L on a CLI-session context should hand over the terminal, got %#v", msg)
	}
	if m.ctxCur != 1 || m.screen != screenContexts {
		t.Error("L should neither move the cursor nor leave the view")
	}
}
