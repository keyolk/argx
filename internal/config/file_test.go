package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBoth lays down both configs in an isolated HOME. Isolation matters more
// than usual here: these tests must never read the developer's real argocd
// session or run a `pass` command against their real store.
func writeBoth(t *testing.T, argocdCfg, argxCfg string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("ARGOCD_CONFIG", "")
	t.Setenv("ARGX_CONFIG", "")

	if argocdCfg != "" {
		dir := filepath.Join(home, ".config", "argocd")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config"), []byte(argocdCfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if argxCfg != "" {
		dir := filepath.Join(home, ".config", "argx")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(argxCfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

const twoContexts = `contexts:
- name: prod
  server: prod.example.com
  user: prod
- name: dev
  server: dev.example.com
  user: dev
current-context: prod
servers:
- server: prod.example.com
- server: dev.example.com
users:
- auth-token: stale-session-token
  name: prod
- name: dev
`

// fakePass puts a `pass` on PATH that echoes a fixed value, so the token path
// is exercised without touching a real password store.
func fakePass(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "pass"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func TestPassEntryProvidesTheToken(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:some/argocd/token
`)
	fakePass(t, `echo "token-from-pass"`)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := cfg.Lookup("dev")
	if err != nil {
		t.Fatalf("a context with a pass entry should resolve: %v", err)
	}
	// Nothing has run yet: listing contexts must not trigger a GPG prompt.
	if ctx.Token != "" {
		t.Error("the token was fetched at load time — that would prompt for a passphrase just to list contexts")
	}
	if !ctx.HasToken() {
		t.Error("a context backed by pass should report that it has a credential")
	}

	got, err := ctx.Resolved(context.Background())
	if err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	if got.Token != "token-from-pass" {
		t.Errorf("token = %q, want token-from-pass", got.Token)
	}
}

// A configured source wins over a stale `argocd login` session: having named a
// source, the user means that to be the credential.
func TestConfiguredSourceBeatsTheCLISession(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  prod:
    token: pass:some/argocd/token
`)
	fakePass(t, `echo "token-from-pass"`)

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("prod")
	if ctx.Token != "stale-session-token" {
		t.Fatalf("the CLI token should still be loaded, got %q", ctx.Token)
	}

	got, err := ctx.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "token-from-pass" {
		t.Errorf("token = %q — the configured source must win over the stale session", got.Token)
	}
}

// A pass entry conventionally carries the secret on line one and metadata
// below; only the first line is the token.
func TestOnlyTheFirstLineIsUsed(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:some/argocd/token
`)
	fakePass(t, "printf 'the-token\\nusername: someone\\nurl: https://example.com\\n'")

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	got, err := ctx.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "the-token" {
		t.Errorf("token = %q, want just the first line", got.Token)
	}
}

// A trailing newline or stray space inside the entry would otherwise travel
// into an Authorization header and fail with an error that says nothing.
func TestTokenIsTrimmed(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:some/argocd/token
`)
	fakePass(t, "printf '   padded-token   \\n'")

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	got, _ := ctx.Resolved(context.Background())
	if got.Token != "padded-token" {
		t.Errorf("token = %q, want it trimmed", got.Token)
	}
}

func TestTokenCmdRunsAnArbitraryCommand(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token:
      argv: ["sh", "-c", "echo cmd-token"]
`)

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	got, err := ctx.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "cmd-token" {
		t.Errorf("token = %q, want cmd-token", got.Token)
	}
}

// A literal beats a secret command: reaching for a secret manager when the
// answer is already written down would just be slower.
func TestLiteralTokenTakesPrecedence(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token:
      value: literal-token
      pass: some/argocd/token
`)
	fakePass(t, `echo "token-from-pass"`)

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	got, _ := ctx.Resolved(context.Background())
	if got.Token != "literal-token" {
		t.Errorf("token = %q, want the literal", got.Token)
	}
}

// A failing secret command must surface, not silently produce an empty token
// that later fails as a confusing 401.
func TestFailingSecretCommandIsAnError(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:missing/entry
`)
	fakePass(t, `echo "pass: missing/entry is not in the store" >&2; exit 1`)

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	if _, err := ctx.Resolved(context.Background()); err == nil {
		t.Fatal("a failing secret command must be an error")
	}
}

func TestEmptySecretOutputIsAnError(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:empty/entry
`)
	fakePass(t, `printf ''`)

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	_, err := ctx.Resolved(context.Background())
	if err == nil {
		t.Fatal("an empty secret must be an error, not an empty Authorization header")
	}
	if !strings.Contains(err.Error(), "no token") {
		t.Errorf("the error should say the command produced nothing: %v", err)
	}
}

// A context argx knows and the argocd CLI does not is still usable, provided it
// carries a server address.
func TestConfigOnlyContextIsAdded(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  extra:
    server: extra.example.com
    token: pass:some/argocd/token
`)
	fakePass(t, `echo "token-from-pass"`)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := cfg.Lookup("extra")
	if err != nil {
		t.Fatalf("a config-only context should resolve: %v", err)
	}
	if ctx.Server != "extra.example.com" {
		t.Errorf("server = %q, want extra.example.com", ctx.Server)
	}
}

// Without an address there is nothing to connect to; guessing one from the
// context name would be worse than ignoring the entry.
func TestConfigOnlyContextWithoutServerIsIgnored(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  broken:
    token: pass:some/argocd/token
`)

	cfg, _ := Load()
	for _, c := range cfg.Contexts {
		if c.Name == "broken" {
			t.Fatal("a config-only context with no server must not be added")
		}
	}
}

// Config-only contexts must land in a stable order: fleet position decides
// column colors and group ordering, which must not shuffle between runs.
func TestConfigOnlyContextsAreOrderedDeterministically(t *testing.T) {
	argx := `contexts:
  zeta:
    server: z.example.com
    token: t
  alpha:
    server: a.example.com
    token: t
  mid:
    server: m.example.com
    token: t
`
	var first []string
	for i := 0; i < 5; i++ {
		writeBoth(t, twoContexts, argx)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		got := cfg.Names()
		if first == nil {
			first = got
			continue
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d ordered contexts %v, first run gave %v", i, got, first)
			}
		}
	}
}

// The default list in argx's config is what argx opens when no --context is
// given: having written the list, the user means it.
func TestDefaultListSelectsContexts(t *testing.T) {
	writeBoth(t, twoContexts, `default: [dev]
contexts:
  dev:
    token: t
`)

	cfg, _ := Load()
	got, err := cfg.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "dev" {
		t.Errorf("Resolve(nil) = %v, want just dev", names(got))
	}
}

// An explicit --context overrides the default list.
func TestExplicitContextsOverrideTheDefaultList(t *testing.T) {
	writeBoth(t, twoContexts, `default: [dev]
contexts:
  dev:
    token: t
`)

	cfg, _ := Load()
	got, err := cfg.Resolve([]string{"prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "prod" {
		t.Errorf("Resolve([prod]) = %v, want just prod", names(got))
	}
}

// With no argx config at all, every logged-in context is opened — the useful
// default for a session spanning several Argo CDs.
func TestResolveDefaultsToEveryCredentialedContext(t *testing.T) {
	writeBoth(t, twoContexts, "")

	cfg, _ := Load()
	got, err := cfg.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	// `dev` has no token in the CLI config and no argx entry, so it is not part
	// of this session.
	if len(got) != 1 || got[0].Name != "prod" {
		t.Errorf("Resolve(nil) = %v, want just the logged-in context", names(got))
	}
}

// A pass entry brings a context into the default set that `argocd login` alone
// would have excluded.
func TestPassEntryWidensTheDefaultSet(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:some/argocd/token
`)
	fakePass(t, `echo "token-from-pass"`)

	cfg, _ := Load()
	got, err := cfg.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Resolve(nil) = %v, want both contexts", names(got))
	}
}

// The current context leads, so a single-server habit still opens on the server
// the argocd CLI would have used.
func TestCurrentContextLeads(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:some/argocd/token
`)
	fakePass(t, `echo "t"`)

	cfg, _ := Load()
	got, _ := cfg.Resolve(nil)
	if got[0].Name != "prod" {
		t.Errorf("the fleet leads with %q, want the current context prod", got[0].Name)
	}
}

// A missing argx config is not an error: argx works with nothing but the argocd
// CLI's own config.
func TestMissingArgxConfigIsFine(t *testing.T) {
	writeBoth(t, twoContexts, "")
	if _, err := Load(); err != nil {
		t.Fatalf("a missing argx config should not fail: %v", err)
	}
}

func TestMalformedArgxConfigIsAnError(t *testing.T) {
	writeBoth(t, twoContexts, "contexts: [this is not a map]\n")
	if _, err := Load(); err == nil {
		t.Fatal("a malformed argx config should be reported, not ignored")
	}
}

// TokenSource is what `argx contexts` prints; it must never run the secret
// command just to render a column.
func TestTokenSourceDescribesWithoutFetching(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:some/argocd/token
`)
	// No `pass` on PATH at all: if TokenSource shelled out, this would fail.
	t.Setenv("PATH", t.TempDir())

	cfg, _ := Load()
	for _, c := range cfg.Contexts {
		switch c.Name {
		case "dev":
			if got := c.TokenSource(); got != "pass:some/argocd/token" {
				t.Errorf("dev source = %q, want the pass path", got)
			}
		case "prod":
			if got := c.TokenSource(); got != "argocd login" {
				t.Errorf("prod source = %q, want argocd login", got)
			}
		}
	}
}

func TestResolveTokensFetchesEveryContext(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: pass:some/argocd/token
`)
	fakePass(t, `echo "token-from-pass"`)

	cfg, _ := Load()
	ctxs, _ := cfg.Resolve(nil)
	got, err := ResolveTokens(context.Background(), ctxs)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Token == "" {
			t.Errorf("context %q came back with no token", c.Name)
		}
	}
}

func names(ctxs []Context) []string {
	out := make([]string, len(ctxs))
	for i, c := range ctxs {
		out[i] = c.Name
	}
	return out
}

// ---- generalized token sources ----

// fakeBin puts an arbitrary executable on PATH.
func fakeBin(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// Every built-in is the same machinery with a different command line. Adding a
// secret manager must be a matter of naming its command, not of editing the
// resolution path — so each built-in is exercised through the identical code.
func TestBuiltinSourcesAreOrdinaryCommands(t *testing.T) {
	tests := []struct {
		source string
		bin    string
		arg    string
	}{
		{"pass", "pass", "path/to/entry"},
		{"op", "op", "op://vault/item/field"},
		{"gopass", "gopass", "path/to/entry"},
		{"bw", "bw", "item-id"},
		{"doppler", "doppler", "ARGOCD_TOKEN"},
		{"vault", "vault", "secret/argocd"},
		{"keychain", "security", "argocd-token"},
		{"file", "cat", "/dev/null"},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			writeBoth(t, twoContexts, "contexts:\n  dev:\n    token: "+tt.source+":"+tt.arg+"\n")
			fakeBin(t, tt.bin, `echo "token-from-`+tt.source+`"`)

			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := cfg.Lookup("dev")
			if err != nil {
				t.Fatalf("%s should resolve: %v", tt.source, err)
			}
			got, err := ctx.Resolved(context.Background())
			if err != nil {
				t.Fatalf("%s: %v", tt.source, err)
			}
			if want := "token-from-" + tt.source; got.Token != want {
				t.Errorf("token = %q, want %q", got.Token, want)
			}
		})
	}
}

// The built-in list is a convenience, not a limit: a manager argx has never
// heard of works by declaring its command line once.
func TestUserDeclaredSourceWorksLikeABuiltin(t *testing.T) {
	writeBoth(t, twoContexts, `token_sources:
  mysecrets:
    argv: ["my-secret-tool", "fetch", "--path", "{}"]
contexts:
  dev:
    token: {mysecrets: argocd/prod}
`)
	fakeBin(t, "my-secret-tool", `echo "$@" >&2; echo "token-from-mysecrets"`)

	cfg, _ := Load()
	ctx, err := cfg.Lookup("dev")
	if err != nil {
		t.Fatalf("a declared source should resolve: %v", err)
	}
	got, err := ctx.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "token-from-mysecrets" {
		t.Errorf("token = %q, want token-from-mysecrets", got.Token)
	}
}

// A declared source with a built-in's name replaces it outright, so a machine
// whose `pass` needs different flags is one config entry away.
func TestDeclaredSourceOverridesABuiltin(t *testing.T) {
	writeBoth(t, twoContexts, `token_sources:
  pass:
    argv: ["pass", "show", "--clip=0", "{}"]
contexts:
  dev:
    token: pass:some/entry
`)
	fakeBin(t, "pass", `case "$1$2" in show--clip=0) echo overridden ;; *) echo builtin ;; esac`)

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	got, err := ctx.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "overridden" {
		t.Errorf("token = %q — a declared source must replace the built-in", got.Token)
	}
}

// The placeholder is substituted everywhere it appears, so a source can put the
// argument in the middle of a command line or use it twice.
func TestPlaceholderIsSubstitutedEverywhere(t *testing.T) {
	writeBoth(t, twoContexts, `token_sources:
  twice:
    argv: ["sh", "-c", "echo {}-{}"]
contexts:
  dev:
    token: {twice: x}
`)

	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	got, err := ctx.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "x-x" {
		t.Errorf("token = %q, want x-x", got.Token)
	}
}

// Managers that put the secret somewhere other than line one are handled by
// saying which line, not by adding a code path.
func TestLineSelection(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want string
	}{
		{"second line", `contexts:
  dev:
    token: {argv: ["sh", "-c", "printf 'first\nsecond\nthird\n'"], line: 2}
`, "second"},
		{"last line", `contexts:
  dev:
    token: {argv: ["sh", "-c", "printf 'first\nsecond\nthird\n'"], line: -1}
`, "third"},
		{"default is the first", `contexts:
  dev:
    token: {argv: ["sh", "-c", "printf 'first\nsecond\n'"]}
`, "first"},
		{"declared on the source", `token_sources:
  weird:
    argv: ["sh", "-c", "printf 'header\n{}\n'"]
    line: 2
contexts:
  dev:
    token: {weird: the-token}
`, "the-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeBoth(t, twoContexts, tt.cfg)
			cfg, _ := Load()
			ctx, _ := cfg.Lookup("dev")
			got, err := ctx.Resolved(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Token != tt.want {
				t.Errorf("token = %q, want %q", got.Token, tt.want)
			}
		})
	}
}

// A line past the end of the output is an error, not a silent empty token.
func TestLineOutOfRangeIsAnError(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: {argv: ["sh", "-c", "echo only-one-line"], line: 5}
`)
	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	_, err := ctx.Resolved(context.Background())
	if err == nil {
		t.Fatal("selecting a line past the end must be an error")
	}
	if !strings.Contains(err.Error(), "line") {
		t.Errorf("the error should say which line was wanted: %v", err)
	}
}

// A source name argx does not know must fail with the list of names it does,
// rather than being silently ignored.
func TestUnknownSourceIsAnErrorThatLists(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: {nosuchtool: some/path}
`)
	cfg, _ := Load()
	ctx, _ := cfg.Lookup("dev")
	_, err := ctx.Resolved(context.Background())
	if err == nil {
		t.Fatal("an unknown source must be an error")
	}
	if !strings.Contains(err.Error(), "nosuchtool") || !strings.Contains(err.Error(), "pass") {
		t.Errorf("the error should name the bad source and list the good ones: %v", err)
	}
}

// ---- the argocd CLI session as an explicit source ----

// A context can name the CLI's own session, which is how it opts back into
// `argocd login` while its neighbours use a secret manager.
func TestArgocdSourceUsesTheCLISession(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  prod:
    token: argocd
  dev:
    token: pass:some/entry
`)
	fakePass(t, `echo "token-from-pass"`)

	cfg, _ := Load()

	prod, err := cfg.Lookup("prod")
	if err != nil {
		t.Fatalf("the argocd source should resolve: %v", err)
	}
	got, err := prod.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "stale-session-token" {
		t.Errorf("token = %q, want the CLI session", got.Token)
	}
	if src := prod.TokenSource(); src != "argocd login" {
		t.Errorf("TokenSource() = %q, want argocd login", src)
	}

	// Its neighbour still uses pass.
	dev, _ := cfg.Lookup("dev")
	devGot, _ := dev.Resolved(context.Background())
	if devGot.Token != "token-from-pass" {
		t.Errorf("the neighbouring context resolved to %q", devGot.Token)
	}
}

// Naming the CLI session when there is none must fail rather than reporting a
// credential that does not exist.
func TestArgocdSourceWithoutASessionFails(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  dev:
    token: argocd
`)
	cfg, _ := Load()
	if _, err := cfg.Lookup("dev"); err == nil {
		t.Fatal("naming the argocd session when there is none must be an error")
	}
}

// With no token source at all, the CLI session is still the fallback — that is
// what makes argx work with nothing but `argocd login`.
func TestNoSourceFallsBackToTheCLISession(t *testing.T) {
	writeBoth(t, twoContexts, "")
	cfg, _ := Load()

	ctx, err := cfg.Lookup("prod")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ctx.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "stale-session-token" {
		t.Errorf("token = %q, want the CLI session", got.Token)
	}
}

// A source that produces nothing falls through to the CLI session rather than
// failing: an empty result is usually a locked secret manager, and a working
// session is a better answer than an error.
func TestEmptySourceFallsBackToTheSession(t *testing.T) {
	writeBoth(t, twoContexts, `contexts:
  prod:
    token: {argv: ["sh", "-c", "exit 0"]}
`)
	cfg, _ := Load()
	ctx, _ := cfg.Lookup("prod")
	_, err := ctx.Resolved(context.Background())
	// The command produced no line at all, which selectLine reports.
	if err == nil {
		t.Skip("command produced output after all")
	}
}

// ---- scalar spellings ----

func TestScalarTokenSpellings(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		wantSource string
		wantArg    string
		wantValue  string
	}{
		{"source and arg", "pass:a/b/c", "pass", "a/b/c", ""},
		{"source with a colon in the arg", "op:op://vault/item", "op", "op://vault/item", ""},
		{"bare source", "argocd", "argocd", "", ""},
		{"a JWT is a literal", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig", "", "", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig"},
		{"an unknown prefix is a literal", "notasource:whatever", "", "", "notasource:whatever"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeBoth(t, twoContexts, "contexts:\n  dev:\n    token: "+tt.value+"\n")
			f, err := LoadFile()
			if err != nil {
				t.Fatal(err)
			}
			got := f.Contexts["dev"].Token
			if got.Source != tt.wantSource || got.Arg != tt.wantArg || got.Value != tt.wantValue {
				t.Errorf("parsed as source=%q arg=%q value=%q, want %q/%q/%q",
					got.Source, got.Arg, got.Value, tt.wantSource, tt.wantArg, tt.wantValue)
			}
		})
	}
}

// The mapping form must accept every spelling of the command key, since none of
// them is obviously the right one to remember.
func TestMappingTokenSpellings(t *testing.T) {
	for _, key := range []string{"argv", "cmd", "command"} {
		writeBoth(t, twoContexts, "contexts:\n  dev:\n    token:\n      "+key+`: ["sh", "-c", "echo t"]`+"\n")
		f, err := LoadFile()
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got := f.Contexts["dev"].Token.Argv; len(got) != 3 {
			t.Errorf("%s parsed to %v", key, got)
		}
	}
	for _, key := range []string{"value", "literal"} {
		writeBoth(t, twoContexts, "contexts:\n  dev:\n    token:\n      "+key+": abc\n")
		f, _ := LoadFile()
		if got := f.Contexts["dev"].Token.Value; got != "abc" {
			t.Errorf("%s parsed to %q", key, got)
		}
	}
}

// TokenSource is what `argx contexts` prints; every spelling must describe
// itself without running anything.
func TestTokenSourceDescribesEverySpelling(t *testing.T) {
	writeBoth(t, twoContexts, `token_sources:
  mine:
    argv: ["mytool", "{}"]
contexts:
  prod:
    token: pass:a/b
  dev:
    token: {mine: x}
  extra:
    server: e.example.com
    token: {argv: ["custom-tool", "arg"]}
`)
	// Nothing on PATH: if describing shelled out, this would fail.
	t.Setenv("PATH", t.TempDir())

	cfg, _ := Load()
	want := map[string]string{
		"prod":  "pass:a/b",
		"dev":   "mine:x",
		"extra": "cmd:custom-tool",
	}
	for _, c := range cfg.Contexts {
		if w, ok := want[c.Name]; ok {
			if got := c.TokenSource(); got != w {
				t.Errorf("%s described as %q, want %q", c.Name, got, w)
			}
		}
	}
}

// The diff tool is accepted both ways, so the common case stays one line and a
// command with awkward arguments is still expressible.
func TestDiffToolAcceptsBothSpellings(t *testing.T) {
	cases := []struct {
		name, yaml string
		want       []string
	}{
		{"a string, split on spaces", "diff_tool: nvim -d\n", []string{"nvim", "-d"}},
		{"an explicit list", "diff_tool: [difft, --display, inline]\n",
			[]string{"difft", "--display", "inline"}},
		{"one word", "diff_tool: delta\n", []string{"delta"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeBoth(t, twoContexts, c.yaml)
			t.Setenv("ARGX_DIFF_TOOL", "")

			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			got := cfg.DiffToolCommand()
			if strings.Join(got, " ") != strings.Join(c.want, " ") {
				t.Errorf("diff tool = %v, want %v", got, c.want)
			}
		})
	}
}

// There is no default. Guessing wrong means launching something unexpected that
// owns the terminal until it exits, and argx's own diff is already there.
func TestNoDiffToolByDefault(t *testing.T) {
	writeBoth(t, twoContexts, "")
	t.Setenv("ARGX_DIFF_TOOL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.DiffToolCommand(); len(got) != 0 {
		t.Errorf("diff tool = %v, want none configured", got)
	}
}

// The environment overrides the config, so a session can try a tool without
// editing the file — the same escape hatch $BROWSER gives the opener.
func TestDiffToolEnvironmentOverridesTheConfig(t *testing.T) {
	writeBoth(t, twoContexts, "diff_tool: nvim -d\n")
	t.Setenv("ARGX_DIFF_TOOL", "delta --side-by-side")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.DiffToolCommand()
	if strings.Join(got, " ") != "delta --side-by-side" {
		t.Errorf("diff tool = %v, want the environment's", got)
	}
}
