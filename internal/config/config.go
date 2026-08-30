// Package config reads the Argo CD CLI's own config file so that argx needs no
// separate login: whatever `argocd login` already established is what argx uses.
package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Context pairs a server with the user credential to present to it. The Argo CD
// config models these as three separate lists keyed by name; Context is the
// joined view that the rest of argx works with.
type Context struct {
	Name string
	// Server is the bare host[:port], no scheme — matching what the Argo CD
	// config stores.
	Server string
	// Token is a bearer JWT. Empty means the context has no credential yet:
	// either it was never logged in, or its token comes from a secret source
	// that has not been resolved. Callers must treat an empty token as an auth
	// error rather than an anonymous request.
	Token string

	// spec describes where to fetch this context's token when it does not come
	// from the Argo CD CLI's config. It is resolved on demand rather than at
	// load, so listing contexts never triggers a passphrase prompt.
	spec TokenSpec
	// file is the argx config the spec's source names resolve against.
	file *File
	// Insecure skips TLS verification for this server.
	Insecure bool
	// GRPCWebRootPath is carried through only so argx can reconstruct the web
	// UI URL for servers hosted under a sub-path.
	GRPCWebRootPath string
	// PlainText means the server speaks http, not https.
	PlainText bool
}

// BaseURL is the origin for both API calls and web UI links.
func (c Context) BaseURL() string {
	scheme := "https"
	if c.PlainText {
		scheme = "http"
	}
	root := strings.Trim(c.GRPCWebRootPath, "/")
	if root != "" {
		return fmt.Sprintf("%s://%s/%s", scheme, c.Server, root)
	}
	return fmt.Sprintf("%s://%s", scheme, c.Server)
}

// AppURL is the web UI address for one application, which is what `o` opens.
func (c Context) AppURL(name string) string {
	return c.BaseURL() + "/applications/" + name
}

// Config is the resolved view of ~/.config/argocd/config, layered with argx's
// own config.
type Config struct {
	Contexts []Context
	Current  string

	// Browser overrides the URL opener; see BrowserCommand.
	Browser string

	// Icons is the configured glyph repertoire; see File.Icons.
	Icons string

	// DiffTool is the external diff command; see DiffToolCommand.
	DiffTool []string

	// file is argx's own config, consulted for token sources and defaults.
	file *File
}

// raw mirrors the on-disk Argo CD CLI config. Only the fields argx needs are
// declared; yaml.v3 ignores the rest.
type raw struct {
	Contexts []struct {
		Name   string `yaml:"name"`
		Server string `yaml:"server"`
		User   string `yaml:"user"`
	} `yaml:"contexts"`
	Servers []struct {
		Server          string `yaml:"server"`
		Insecure        bool   `yaml:"insecure"`
		GRPCWebRootPath string `yaml:"grpc-web-root-path"`
		PlainText       bool   `yaml:"plain-text"`
	} `yaml:"servers"`
	Users []struct {
		Name      string `yaml:"name"`
		AuthToken string `yaml:"auth-token"`
	} `yaml:"users"`
	CurrentContext string `yaml:"current-context"`
}

// Path returns the Argo CD CLI config location, honoring the same environment
// variables the CLI itself does so a non-default ARGOCD_CONFIG keeps working.
func Path() string {
	if p := os.Getenv("ARGOCD_CONFIG"); p != "" {
		return p
	}
	if h := os.Getenv("XDG_CONFIG_HOME"); h != "" {
		return filepath.Join(h, "argocd", "config")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "argocd", "config")
}

// Load reads and joins the Argo CD CLI config.
//
// A context whose user has no auth-token is still returned: reporting "not
// logged in" against a context the user can see beats hiding it and answering
// "no such context".
func Load() (*Config, error) {
	p := Path()
	if p == "" {
		return nil, fmt.Errorf("cannot locate argocd config: no home directory")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no argocd config at %s — run `argocd login <server>` first", p)
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var r raw
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}

	tokens := make(map[string]string, len(r.Users))
	for _, u := range r.Users {
		tokens[u.Name] = u.AuthToken
	}
	type srv struct {
		insecure  bool
		rootPath  string
		plainText bool
	}
	servers := make(map[string]srv, len(r.Servers))
	for _, s := range r.Servers {
		servers[s.Server] = srv{s.Insecure, s.GRPCWebRootPath, s.PlainText}
	}

	cfg := &Config{Current: r.CurrentContext, Browser: os.Getenv("ARGX_BROWSER")}
	for _, c := range r.Contexts {
		s := servers[c.Server]
		cfg.Contexts = append(cfg.Contexts, Context{
			Name:            c.Name,
			Server:          c.Server,
			Token:           tokens[c.User],
			Insecure:        s.insecure,
			GRPCWebRootPath: s.rootPath,
			PlainText:       s.plainText,
		})
	}

	file, err := LoadFile()
	if err != nil {
		return nil, err
	}
	cfg.merge(file)
	return cfg, nil
}

// merge layers argx's own config over the Argo CD CLI's.
//
// A context the CLI already knows keeps its server address and picks up a token
// source; a context only argx knows is added, provided it names a server. The
// CLI's config stays authoritative for addresses because that is what
// `argocd login` maintains.
func (c *Config) merge(f *File) {
	c.file = f
	if f == nil {
		f = &File{}
		c.file = f
	}
	// Every context carries the file, not just those with an entry: an error
	// message that lists the available sources needs it, and a nil check at
	// each use site would be one more thing to forget.
	for i := range c.Contexts {
		c.Contexts[i].file = f
	}
	if f.Browser != "" && c.Browser == "" {
		c.Browser = f.Browser
	}
	c.Icons = f.Icons
	if len(f.DiffTool) > 0 && len(c.DiffTool) == 0 {
		c.DiffTool = f.DiffTool
	}

	known := make(map[string]int, len(c.Contexts))
	for i, ctx := range c.Contexts {
		known[ctx.Name] = i
	}

	// Config-only contexts are appended in name order: a map has no order, and
	// fleet position decides both column colors and group ordering, which must
	// not shuffle between runs.
	var extra []string
	for name := range f.Contexts {
		if _, ok := known[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)

	for name, fc := range f.Contexts {
		i, ok := known[name]
		if !ok {
			continue
		}
		c.Contexts[i].spec = fc.Token
		c.Contexts[i].file = f
		if fc.Insecure {
			c.Contexts[i].Insecure = true
		}
		if fc.PlainText {
			c.Contexts[i].PlainText = true
		}
	}

	for _, name := range extra {
		fc := f.Contexts[name]
		if fc.Server == "" {
			// Without an address there is nothing to connect to, and guessing
			// one from the context name would be worse than ignoring the entry.
			continue
		}
		c.Contexts = append(c.Contexts, Context{
			Name:      name,
			Server:    fc.Server,
			Insecure:  fc.Insecure,
			PlainText: fc.PlainText,
			spec:      fc.Token,
			file:      f,
		})
	}
}

// HasToken reports whether this context can produce a credential, without
// fetching one. A context backed by a secret manager counts, so listing
// contexts never triggers a passphrase prompt just to render a column.
func (c Context) HasToken() bool {
	if c.spec.usesArgocdSession() {
		// Explicitly deferring to the CLI session means there must be one.
		return c.Token != ""
	}
	return c.Token != "" || !c.spec.empty()
}

// TokenSource describes where the credential comes from, for display.
func (c Context) TokenSource() string {
	if c.spec.usesArgocdSession() || c.spec.empty() {
		if c.Token != "" {
			return "argocd login"
		}
		return ""
	}
	return c.spec.String()
}

// Resolved returns the context with its token fetched.
//
// Fetching happens here rather than at load so that `argx contexts` and a
// mistyped --context never run a secret command; by the time this is called the
// caller has committed to connecting.
//
// A configured source wins over any `argocd login` session: having named a
// source, the user means that to be the credential. Naming the `argocd` source
// says the opposite — use the session — which is how a context opts back into
// the CLI's own credential while its neighbours use a secret manager.
func (c Context) Resolved(ctx context.Context) (Context, error) {
	if !c.spec.empty() && !c.spec.usesArgocdSession() {
		tok, err := c.file.resolveToken(ctx, c.Name, c.spec)
		if err != nil {
			return c, err
		}
		if tok != "" {
			c.Token = tok
			return c, nil
		}
		// A source that produced nothing falls through to the CLI session
		// rather than failing outright: an empty result is usually a secret
		// manager that is not unlocked, and a working session is a better
		// answer than an error.
	}
	if c.Token == "" {
		return c, fmt.Errorf("context %q has no credential — run `argocd login %s`, or give it a token source in %s",
			c.Name, c.Server, FilePath())
	}
	return c, nil
}

// ResolveTokens fetches the tokens for a set of contexts.
//
// Sequential, not concurrent: each `pass show` may prompt a GPG agent, and
// several prompts racing for the terminal at once is unusable.
func ResolveTokens(ctx context.Context, ctxs []Context) ([]Context, error) {
	out := make([]Context, 0, len(ctxs))
	for _, c := range ctxs {
		r, err := c.Resolved(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Lookup finds a context by name. An empty name means the current context.
func (c *Config) Lookup(name string) (Context, error) {
	if name == "" {
		name = c.Current
	}
	if name == "" {
		return Context{}, fmt.Errorf("no argocd context selected — run `argocd login <server>` or pass --context")
	}
	for _, ctx := range c.Contexts {
		if ctx.Name == name {
			if !ctx.HasToken() {
				return ctx, fmt.Errorf("context %q has no credential — run `argocd login %s`, or give it a token source in %s",
					name, ctx.Server, FilePath())
			}
			return ctx, nil
		}
	}
	return Context{}, fmt.Errorf("no such argocd context: %q (have: %s)", name, strings.Join(c.Names(), ", "))
}

// Resolve picks the contexts argx should connect to.
//
// An empty selection means every context that has a token: with several Argo CD
// servers the useful default is all of them, and a context the user never
// logged into is not a failure to report — it is simply not part of this
// session. An explicit selection is strict, because a name the user typed and
// argx silently dropped is worse than an error.
func (c *Config) Resolve(names []string) ([]Context, error) {
	if len(names) > 0 {
		out := make([]Context, 0, len(names))
		for _, n := range names {
			ctx, err := c.Lookup(n)
			if err != nil {
				return nil, err
			}
			out = append(out, ctx)
		}
		return out, nil
	}

	// An explicit default list in argx's config takes precedence over "every
	// context with a credential": having written the list, the user means it.
	if c.file != nil && len(c.file.Default) > 0 {
		return c.Resolve(c.file.Default)
	}

	var out []Context
	for _, ctx := range c.Contexts {
		if ctx.HasToken() {
			out = append(out, ctx)
		}
	}
	if len(out) == 0 {
		if len(c.Contexts) == 0 {
			return nil, fmt.Errorf("no argocd contexts configured — run `argocd login <server>`")
		}
		return nil, fmt.Errorf("no argocd context has a credential — run `argocd login <server>` (have: %s)",
			strings.Join(c.Names(), ", "))
	}

	// The current context leads, so a single-server habit still opens on the
	// server the argocd CLI would have used.
	if c.Current != "" {
		for i, ctx := range out {
			if ctx.Name == c.Current {
				out[0], out[i] = out[i], out[0]
				break
			}
		}
	}
	return out, nil
}

// Names lists context names in file order.
func (c *Config) Names() []string {
	out := make([]string, 0, len(c.Contexts))
	for _, ctx := range c.Contexts {
		out = append(out, ctx.Name)
	}
	return out
}

// BrowserCommand resolves the URL opener: ARGX_BROWSER > $BROWSER > the
// platform default.
//
// $BROWSER's fuller conventions — %s placeholders and colon-separated fallback
// lists — are deliberately not interpreted: such a value is handed to exec as a
// literal command name and fails visibly, which beats guessing at a syntax
// nobody agrees on.
// DiffToolCommand is the external diff command, as argv.
//
// ARGX_DIFF_TOOL overrides the config, so a session can try a tool without
// editing the file — the same escape hatch $BROWSER gives the opener. It is
// split on spaces, which covers `delta --side-by-side` and stops short of
// quoting rules; a command that needs those belongs in the config as a list.
//
// There is no default. A diff tool is a personal choice and guessing wrong
// means launching something unexpected that owns the terminal until it exits;
// argx's own diff is the default, and it is already there.
func (c *Config) DiffToolCommand() []string {
	if env := strings.TrimSpace(os.Getenv("ARGX_DIFF_TOOL")); env != "" {
		return strings.Fields(env)
	}
	if c != nil && len(c.DiffTool) > 0 {
		return c.DiffTool
	}
	return nil
}

func (c *Config) BrowserCommand() string {
	if c != nil && c.Browser != "" {
		return c.Browser
	}
	if b := os.Getenv("BROWSER"); b != "" {
		return b
	}
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}
