package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// argx's own config, layered over the Argo CD CLI's.
//
// The CLI's config stays the source of truth for server addresses and for any
// session established by `argocd login`. This file only adds what the CLI has
// no notion of: which servers argx opens by default, and where to fetch a token
// from when it should not live in a file on disk.

// File is the argx configuration.
type File struct {
	// Contexts overrides or augments Argo CD CLI contexts, keyed by context
	// name. A context named here that the CLI does not know is still usable as
	// long as it carries a server address.
	Contexts map[string]FileContext `yaml:"contexts"`

	// TokenSources are named command templates a context can reference by name,
	// so the same secret manager is described once rather than at every context.
	// The built-in sources are available without declaring anything; an entry
	// here with a built-in's name replaces it.
	TokenSources map[string]TokenSource `yaml:"token_sources"`

	// Default lists the contexts argx opens when none are given on the command
	// line. Empty means every context that resolves to a token.
	Default []string `yaml:"default"`

	// Browser overrides the URL opener; see Config.BrowserCommand.
	Browser string `yaml:"browser"`

	// DiffTool is an external diff command, given the two documents as file
	// paths. Both spellings work:
	//
	//	diff_tool: nvim -d                       split on spaces
	//	diff_tool: [difft, --display, inline]    an explicit list
	//
	// The paths are appended unless the command names {live} and {desired},
	// which is what every diff tool's own CLI expects. The command must block
	// until the reader is done with it: argx removes the files when it exits,
	// so a command that forks and returns leaves the tool reading nothing.
	DiffTool StringList `yaml:"diff_tool"`

	// Icons selects the glyph repertoire: "unicode" (the default), "nerd" for
	// a Nerd Font terminal, or "ascii". ARGX_ICONS overrides it, so a session
	// on a terminal without the font can opt out without editing the file.
	Icons string `yaml:"icons"`
}

// TokenSource is a command template for fetching a secret.
//
// Argv is expanded with the context's argument before running: every occurrence
// of "{}" in an element is replaced with it. A source with no "{}" simply
// ignores the argument, which is what a fixed command wants.
type TokenSource struct {
	Argv []string `yaml:"argv"`

	// Line selects which line of stdout carries the secret, 1-based. Zero means
	// the first line — the convention for `pass` and most others. Negative
	// counts from the end, so -1 is the last line.
	Line int `yaml:"line"`
}

// expand substitutes arg for each "{}" placeholder.
func (s TokenSource) expand(arg string) []string {
	out := make([]string, len(s.Argv))
	for i, a := range s.Argv {
		out[i] = strings.ReplaceAll(a, "{}", arg)
	}
	return out
}

// argocdSource is the sentinel naming the Argo CD CLI's own session — the
// token `argocd login` wrote into ~/.config/argocd/config. It is not a command,
// so it is handled in resolution rather than being a TokenSource.
//
// It exists so the CLI session can be named explicitly. Without it, a context
// that declares any token source loses the fallback entirely, and there is no
// way to say "prefer pass, but fall back to whatever I logged in with" — which
// is exactly what a fleet with one occasionally-rotated server wants.
const argocdSource = "argocd"

// builtinSources are the secret managers argx knows how to call without being
// told. They are ordinary TokenSource values with nothing special about them —
// a user's own entry with the same name replaces one outright, and any manager
// not listed here works the same way through a declared source.
var builtinSources = map[string]TokenSource{
	"pass":      {Argv: []string{"pass", "show", "{}"}},
	"op":        {Argv: []string{"op", "read", "{}"}},
	"gopass":    {Argv: []string{"gopass", "show", "-o", "{}"}},
	"bw":        {Argv: []string{"bw", "get", "password", "{}"}},
	"doppler":   {Argv: []string{"doppler", "secrets", "get", "{}", "--plain"}},
	"vault":     {Argv: []string{"vault", "kv", "get", "-field=token", "{}"}},
	"keychain":  {Argv: []string{"security", "find-generic-password", "-w", "-s", "{}"}},
	"env":       {Argv: []string{"sh", "-c", `printf '%s' "${{}}"`}},
	"file":      {Argv: []string{"cat", "{}"}},
	"aws-ssm":   {Argv: []string{"aws", "ssm", "get-parameter", "--name", "{}", "--with-decryption", "--query", "Parameter.Value", "--output", "text"}},
	"gcloud-sm": {Argv: []string{"gcloud", "secrets", "versions", "access", "latest", "--secret", "{}"}},
}

// FileContext is one server's argx-side settings.
type FileContext struct {
	// Server is the bare host[:port], needed only for a context the Argo CD CLI
	// does not already define.
	Server string `yaml:"server"`

	// Token is the credential, described one of three ways:
	//
	//	token: {value: eyJhbG...}          a literal (avoid — plaintext on disk)
	//	token: {pass: path/to/entry}       a named source and its argument
	//	token: {argv: [cmd, arg, ...]}     an explicit command
	//
	// The short forms `token: <source>:<arg>` and `token: <literal>` are also
	// accepted; see TokenSpec.UnmarshalYAML.
	Token TokenSpec `yaml:"token"`

	Insecure  bool `yaml:"insecure"`
	PlainText bool `yaml:"plain_text"`
}

// StringList is a command accepted either as a list or as one string split on
// spaces, so the common case stays a single line.
type StringList []string

func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*s = strings.Fields(value.Value)
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*s = list
	return nil
}

// TokenSpec describes where one context's credential comes from.
type TokenSpec struct {
	// Value is a literal token.
	Value string
	// Source names a token source — a built-in or one declared in the config.
	Source string
	// Arg is what the source's "{}" placeholder expands to.
	Arg string
	// Argv is an explicit command, used when no named source fits.
	Argv []string
	// Line overrides the source's line selection.
	Line int
}

// empty reports whether no credential is described.
func (t TokenSpec) empty() bool {
	return t.Value == "" && t.Source == "" && len(t.Argv) == 0
}

// String describes the source for display, without fetching anything.
func (t TokenSpec) String() string {
	switch {
	case t.Value != "":
		return "argx config"
	case t.Source != "":
		if t.Arg == "" {
			return t.Source
		}
		return t.Source + ":" + t.Arg
	case len(t.Argv) > 0:
		return "cmd:" + t.Argv[0]
	}
	return ""
}

// UnmarshalYAML accepts three spellings, so the common case stays one line
// while the general case remains expressible:
//
//	token: pass:infra/argocd/token              a source and its argument
//	token: eyJhbGciOi...                        a bare literal
//	token:                                      the explicit mapping
//	  argv: [vault, kv, get, -field=t, path]
//	  line: 2
//
// A bare scalar is read as `source:arg` when its prefix names a known source,
// and as a literal otherwise. That rule is stated rather than guessed at: a
// literal Argo CD token is a JWT, which contains dots and dashes but no colon
// before its first segment, so the two forms do not collide in practice.
func (t *TokenSpec) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		s := value.Value
		if s == "" {
			return nil
		}
		// A bare source name takes no argument — `token: argocd` and a source
		// whose command has no placeholder.
		if isSourceName(s) {
			t.Source = s
			return nil
		}
		if source, arg, ok := strings.Cut(s, ":"); ok && isSourceName(source) {
			t.Source, t.Arg = source, arg
			return nil
		}
		t.Value = s
		return nil
	}

	// The mapping form. Any key that is not one of the reserved names is taken
	// as `source: arg`, which is what makes `pass: path/to/entry` work without
	// pass being special-cased anywhere in the resolution path.
	var raw map[string]yaml.Node
	if err := value.Decode(&raw); err != nil {
		return err
	}
	for k, v := range raw {
		switch k {
		case "value", "literal":
			if err := v.Decode(&t.Value); err != nil {
				return err
			}
		case "argv", "cmd", "command":
			if err := v.Decode(&t.Argv); err != nil {
				return err
			}
		case "line":
			if err := v.Decode(&t.Line); err != nil {
				return err
			}
		case "source":
			if err := v.Decode(&t.Source); err != nil {
				return err
			}
		case "arg":
			if err := v.Decode(&t.Arg); err != nil {
				return err
			}
		default:
			t.Source = k
			if err := v.Decode(&t.Arg); err != nil {
				return err
			}
		}
	}
	return nil
}

// isSourceName reports whether a scalar's prefix names a built-in source.
//
// Only built-ins are recognized here because unmarshalling happens before the
// file's own token_sources are known. A user-declared source is still usable in
// the scalar form once it is declared — resolution consults the merged set —
// but a name argx cannot possibly know yet is read as a literal, which is the
// safer default: a mistyped source name fails loudly at connect time rather
// than being silently swallowed as a command.
func isSourceName(s string) bool {
	if s == argocdSource {
		return true
	}
	_, ok := builtinSources[s]
	return ok
}

// FilePath returns the argx config location.
func FilePath() string {
	if p := os.Getenv("ARGX_CONFIG"); p != "" {
		return p
	}
	if h := os.Getenv("XDG_CONFIG_HOME"); h != "" {
		return filepath.Join(h, "argx", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "argx", "config.yaml")
}

// LoadFile reads the argx config. A missing file is not an error: argx works
// with nothing but the Argo CD CLI's own config.
func LoadFile() (*File, error) {
	p := FilePath()
	if p == "" {
		return &File{}, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &f, nil
}

// source resolves a source name against the config's own declarations first,
// then the built-ins, so a user can override a built-in that calls their secret
// manager differently.
func (f *File) source(name string) (TokenSource, bool) {
	if f != nil {
		if s, ok := f.TokenSources[name]; ok {
			return s, true
		}
	}
	s, ok := builtinSources[name]
	return s, ok
}

// SourceNames lists every source available, for error messages.
func (f *File) SourceNames() []string {
	seen := map[string]bool{}
	var out []string
	if f != nil {
		for n := range f.TokenSources {
			seen[n] = true
			out = append(out, n)
		}
	}
	for n := range builtinSources {
		if !seen[n] {
			out = append(out, n)
		}
	}
	if !seen[argocdSource] {
		out = append(out, argocdSource)
	}
	sort.Strings(out)
	return out
}

// tokenTimeout bounds a secret command. A GPG agent prompting for a passphrase
// is the common slow case, so the budget is generous — but not unbounded, or a
// wedged agent hangs argx before it draws anything.
const tokenTimeout = 60 * time.Second

// resolveToken fetches the credential a spec describes.
//
// A literal short-circuits; everything else becomes a command. There is no
// per-manager code path: `pass`, `op`, and a hand-written `argv` all run
// through the same executor, which is what keeps adding a manager a matter of
// naming its command rather than editing argx.
func (f *File) resolveToken(ctx context.Context, name string, t TokenSpec) (string, error) {
	if t.Value != "" {
		return t.Value, nil
	}

	argv := t.Argv
	line := t.Line
	if t.Source != "" {
		if t.Source == argocdSource {
			// Handled by the caller, which is the only place that holds the
			// session token; reaching here means the caller did not check.
			return "", fmt.Errorf("context %q: the %q source is resolved from the argocd CLI config, not a command",
				name, argocdSource)
		}
		src, ok := f.source(t.Source)
		if !ok {
			return "", fmt.Errorf("context %q: unknown token source %q (have: %s)",
				name, t.Source, strings.Join(f.SourceNames(), ", "))
		}
		argv = src.expand(t.Arg)
		if line == 0 {
			line = src.Line
		}
	}
	if len(argv) == 0 {
		return "", nil
	}
	return runTokenCmd(ctx, name, argv, line)
}

// usesArgocdSession reports whether this spec defers to the Argo CD CLI's own
// session rather than fetching a secret.
func (t TokenSpec) usesArgocdSession() bool { return t.Source == argocdSource }

// runTokenCmd executes a secret command and returns the selected output line.
//
// Trailing whitespace is stripped: an editor-added newline inside a secret
// would otherwise travel into an Authorization header and fail with an error
// that says nothing about why.
func runTokenCmd(ctx context.Context, name string, argv []string, line int) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("context %q: empty token command", name)
	}
	c, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()

	cmd := exec.CommandContext(c, argv[0], argv[1:]...)
	// stderr is inherited so a GPG passphrase prompt reaches the terminal;
	// swallowing it turns "unlock your key" into an unexplained hang.
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		if c.Err() != nil {
			return "", fmt.Errorf("context %q: %s timed out after %s",
				name, argv[0], tokenTimeout)
		}
		return "", fmt.Errorf("context %q: %s: %w", name, strings.Join(argv, " "), err)
	}

	got, err := selectLine(string(out), line)
	if err != nil {
		return "", fmt.Errorf("context %q: %s: %w", name, argv[0], err)
	}
	return got, nil
}

// selectLine picks one line of command output.
//
// Line 0 and 1 both mean the first line, the convention for `pass` and most
// other managers, which put the secret on line one and metadata below it.
// Negative indices count from the end.
func selectLine(out string, line int) (string, error) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	i := 0
	switch {
	case line > 0:
		i = line - 1
	case line < 0:
		i = len(lines) + line
	}
	if i < 0 || i >= len(lines) {
		return "", fmt.Errorf("output has %d lines, cannot take line %d", len(lines), line)
	}

	s := strings.TrimSpace(lines[i])
	if s == "" {
		return "", fmt.Errorf("produced no token")
	}
	return s, nil
}
