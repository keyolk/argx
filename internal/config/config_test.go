package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig lays down a config in an isolated HOME so the test never reads
// the developer's real argocd session.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("ARGOCD_CONFIG", "")
	dir := filepath.Join(home, ".config", "argocd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const sample = `contexts:
- name: prod.example.com
  server: prod.example.com
  user: prod.example.com
- name: dev.example.com
  server: dev.example.com
  user: dev.example.com
current-context: prod.example.com
servers:
- grpc-web-root-path: ""
  server: prod.example.com
- grpc-web-root-path: argocd
  insecure: true
  plain-text: true
  server: dev.example.com
users:
- auth-token: token-prod
  name: prod.example.com
- name: dev.example.com
`

func TestLoadJoinsContextsServersAndUsers(t *testing.T) {
	writeConfig(t, sample)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Current, "prod.example.com"; got != want {
		t.Errorf("current = %q, want %q", got, want)
	}
	if got, want := len(cfg.Contexts), 2; got != want {
		t.Fatalf("contexts = %d, want %d", got, want)
	}

	prod := cfg.Contexts[0]
	if prod.Token != "token-prod" {
		t.Errorf("prod token = %q, want token-prod", prod.Token)
	}
	if prod.Insecure {
		t.Error("prod should not be insecure")
	}

	dev := cfg.Contexts[1]
	if !dev.Insecure || !dev.PlainText {
		t.Errorf("dev insecure=%v plainText=%v, want both true", dev.Insecure, dev.PlainText)
	}
}

func TestBaseURLAndAppURL(t *testing.T) {
	tests := []struct {
		name    string
		ctx     Context
		baseURL string
		appURL  string
	}{
		{
			name:    "https root",
			ctx:     Context{Server: "argocd.example.com"},
			baseURL: "https://argocd.example.com",
			appURL:  "https://argocd.example.com/applications/my-app",
		},
		{
			name:    "plain text with sub-path",
			ctx:     Context{Server: "dev:8080", PlainText: true, GRPCWebRootPath: "/argocd/"},
			baseURL: "http://dev:8080/argocd",
			appURL:  "http://dev:8080/argocd/applications/my-app",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ctx.BaseURL(); got != tt.baseURL {
				t.Errorf("BaseURL() = %q, want %q", got, tt.baseURL)
			}
			if got := tt.ctx.AppURL("my-app"); got != tt.appURL {
				t.Errorf("AppURL() = %q, want %q", got, tt.appURL)
			}
		})
	}
}

// A context that exists but was never logged in must report that, rather than
// producing an anonymous request that fails later with a confusing 401.
func TestLookupReportsMissingToken(t *testing.T) {
	writeConfig(t, sample)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Lookup("dev.example.com"); err == nil {
		t.Fatal("expected an error for a context with no auth token")
	}
}

func TestLookupUnknownContextListsAvailable(t *testing.T) {
	writeConfig(t, sample)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = cfg.Lookup("nope")
	if err == nil {
		t.Fatal("expected an error for an unknown context")
	}
	if got := err.Error(); !contains(got, "prod.example.com") {
		t.Errorf("error %q should list available contexts", got)
	}
}

func TestLoadMissingFileSuggestsLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("ARGOCD_CONFIG", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when no config exists")
	}
	if !contains(err.Error(), "argocd login") {
		t.Errorf("error %q should suggest `argocd login`", err)
	}
}

func TestBrowserCommandPrecedence(t *testing.T) {
	t.Setenv("BROWSER", "env-browser")
	cfg := &Config{Browser: "cfg-browser"}
	if got := cfg.BrowserCommand(); got != "cfg-browser" {
		t.Errorf("config should win: got %q", got)
	}
	cfg.Browser = ""
	if got := cfg.BrowserCommand(); got != "env-browser" {
		t.Errorf("$BROWSER should be used: got %q", got)
	}
	t.Setenv("BROWSER", "")
	if got := cfg.BrowserCommand(); got == "" {
		t.Error("expected a platform default")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
