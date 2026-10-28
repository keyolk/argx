package tui

import (
	"fmt"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/config"
)

// Renewing a session from the context view.
//
// An SSO session through Dex's SAML connector carries no refresh token, so it
// simply ends, and the fix is a fresh `argocd login --sso`. Running it from
// here hands the terminal to the CLI the same way exec does, and the new token
// is read back into the running client, so nothing has to restart.
//
// One login at a time, by construction: the CLI listens on a fixed callback
// port and rewrites its whole config file when the browser returns, so two
// logins racing either fail to bind or erase each other's token.

// loginMsg reports how an `argocd login` for one context ended.
type loginMsg struct {
	name string
	err  error
}

// loginCmd renews the session of the context under the cursor.
//
// A context whose token comes from a configured source is refused rather than
// logged in: the CLI session would change, argx would keep using the
// configured token, and the login would look like it did nothing.
func (m *Model) loginCmd(r contextRow) tea.Cmd {
	cl, err := m.fleet.Client(r.name)
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	ctx := cl.Context()
	if !ctx.UsesArgocdSession() {
		return func() tea.Msg {
			return toastMsg{fmt.Sprintf("%s takes its token from %s — `argocd login` would not change it", r.name, r.source)}
		}
	}
	cmd := exec.Command("argocd", ctx.LoginArgs()...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return loginMsg{name: r.name, err: err}
	})
}

// reloadToken reads the session `argocd login` just wrote and hands it to the
// running client.
func (m *Model) reloadToken(name string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	c, err := cfg.Lookup(name)
	if err != nil {
		return err
	}
	if c.Token == "" {
		return fmt.Errorf("`argocd login` finished but %s still has no token", name)
	}
	cl, err := m.fleet.Client(name)
	if err != nil {
		return err
	}
	cl.SetToken(c.Token)
	return nil
}
