package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

// The context view: which Argo CD servers this session is talking to, and as
// whom.
//
// A fleet session holds several credentials at once. They are not
// interchangeable — one may be a read-only SSO identity, another an admin API
// key generated eleven months ago — and until something is refused, which one
// is in use is invisible. Finding out by pressing `s` and receiving a 403 is a
// worse way to learn it than a line that says so.
//
// Three sources, answering three different questions:
//
//   - The argx config says where the credential comes from: a `pass` entry, a
//     command, the Argo CD CLI's own session. That is what you edit when it is
//     wrong, and it is the only one of the three that names a place.
//   - The token says what it claims to be, read locally. It works for a token
//     the server rejects, which is exactly when the question gets asked.
//   - The server says who it thinks the caller is and what they may do. It is
//     the authority: RBAC maps SSO groups onto permissions, and a token's
//     claims do not decide the outcome — the server's policy does.

// contextRow is one server.
type contextRow struct {
	name   string
	server string
	// source is where the credential comes from, e.g. "pass:infra/argocd" or
	// "argocd login".
	source string

	// claims is what the token says about itself. Read locally, so it is
	// present even when the server refuses the token.
	claims    argocd.TokenClaims
	claimsErr error

	// user is who the server says this is. Absent when the server could not be
	// reached or refused the credential.
	user     *argocd.UserInfo
	perms    []contextPerm
	err      error
	insecure bool
}

// contextPerm is one RBAC answer.
type contextPerm struct {
	label   string
	allowed bool
}

// permChecks are the actions argx itself performs, in the order the reader
// meets them.
//
// Not every Argo CD resource: a permission argx never exercises is noise, and
// the question this view answers is "what will argx be able to do here".
var permChecks = []struct {
	label, resource, action string
}{
	{"read apps", "applications", "get"},
	{"sync", "applications", "sync"},
	{"edit spec", "applications", "update"},
	{"rollback", "applications", "override"},
	{"logs", "logs", "get"},
	{"exec", "exec", "create"},
	{"projects", "projects", "get"},
}

// contextsMsg carries the loaded rows.
type contextsMsg struct {
	rows []contextRow
}

// loadContextsCmd asks every server who argx is and what it may do.
//
// Concurrent across servers, because one unreachable Argo CD should not hold up
// the answer for the others — the same reason the fleet list works that way.
func (m *Model) loadContextsCmd() tea.Cmd {
	fleet := m.fleet
	parent := m.ctx
	m.loading, m.loadWhat = true, "contexts"

	return func() tea.Msg {
		clients := fleet.Clients()
		rows := make([]contextRow, len(clients))

		var wg sync.WaitGroup
		for i, cl := range clients {
			wg.Add(1)
			go func(i int, cl *argocd.Client) {
				defer wg.Done()
				cfg := cl.Context()

				row := contextRow{
					name:     cfg.Name,
					server:   cfg.Server,
					source:   cfg.TokenSource(),
					insecure: cfg.Insecure,
				}
				row.claims, row.claimsErr = argocd.ParseTokenClaims(cfg.Token)

				c, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()

				u, err := cl.WhoAmI(c)
				if err != nil {
					// A server that will not say who we are cannot be asked
					// what we may do either, and the local claims still stand.
					row.err = err
					rows[i] = row
					return
				}
				row.user = u

				// The permission checks are independent, so they run together.
				perms := make([]contextPerm, len(permChecks))
				var pw sync.WaitGroup
				for j, ck := range permChecks {
					pw.Add(1)
					go func(j int, label, res, act string) {
						defer pw.Done()
						ok, err := cl.CanI(c, res, act, "*/*")
						perms[j] = contextPerm{label: label, allowed: err == nil && ok}
					}(j, ck.label, ck.resource, ck.action)
				}
				pw.Wait()
				row.perms = perms
				rows[i] = row
			}(i, cl)
		}
		wg.Wait()
		return contextsMsg{rows: rows}
	}
}

// identity is the one-line answer to "who is this".
//
// An SSO email beats a subject id, which is an opaque base64 string nobody
// recognises; the server's username beats both, because that is the name RBAC
// rules are written against.
func (r contextRow) identity() string {
	switch {
	case r.claims.Email != "":
		return r.claims.Email
	case r.user != nil && r.user.Username != "":
		return r.user.Username
	case r.claims.Account() != "":
		return r.claims.Account()
	}
	return "unknown"
}

// authKind describes the shape of the credential in a few words.
func (r contextRow) authKind() string {
	switch {
	case r.claims.Email != "" || !r.claims.Local():
		return "SSO"
	case r.claims.APIKey():
		return "API key"
	case r.claims.Subject != "":
		return "local login"
	}
	return "unknown"
}

// issuer names who minted the credential, in a form worth reading.
//
// "argocd" is the server's own name for a local account and says nothing the
// kind does not; an OIDC issuer is a URL, and its host is the part that
// identifies the provider.
func (r contextRow) issuer() string {
	iss := r.claims.Issuer
	if iss == "" || iss == "argocd" {
		return ""
	}
	iss = strings.TrimPrefix(strings.TrimPrefix(iss, "https://"), "http://")
	if i := strings.IndexByte(iss, '/'); i > 0 {
		iss = iss[:i]
	}
	return iss
}

// age describes the credential's lifetime — when it expires, or how old it is
// when it never does.
//
// A token with no expiry is not a problem to flag, but an API key minted
// eleven months ago is worth being able to see; the fleet has three.
func (r contextRow) age(now time.Time) (text string, warn bool) {
	c := r.claims
	switch {
	case c.Expired(now):
		return "EXPIRED " + humanSince(now.Sub(c.ExpiresAt)) + " ago", true
	case !c.ExpiresAt.IsZero():
		d := c.ExpiresAt.Sub(now)
		// An hour is enough warning to renew before something fails halfway.
		return "expires in " + humanSince(d), d < time.Hour
	case !c.IssuedAt.IsZero():
		return "no expiry · issued " + humanSince(now.Sub(c.IssuedAt)) + " ago", false
	}
	return "", false
}

// humanSince renders a duration in the largest unit that stays honest.
func humanSince(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
}

// denied lists the actions this credential may not perform.
//
// Rendered as what is missing rather than what is granted: a session that can
// do everything needs no list, and a session that cannot sync needs exactly
// that one word.
func (r contextRow) denied() []string {
	var out []string
	for _, p := range r.perms {
		if !p.allowed {
			out = append(out, p.label)
		}
	}
	return out
}

// sortContextRows keeps fleet order but floats the broken ones, since a server
// argx cannot authenticate to is the reason anybody opened this view.
func sortContextRows(rows []contextRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		bi := rows[i].err != nil
		bj := rows[j].err != nil
		return bi && !bj
	})
}
