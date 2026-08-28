package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

// ---- mutating commands ----
//
// Each of these changes an application; every caller has already passed the
// user through a confirmation. They return specMsg so the view can update from
// the server's response rather than from what argx assumed it wrote.

type specMsg struct {
	app  *argocd.Application
	text string
	err  error
}

type refsMsg struct {
	items []revItem
	err   error
}

func (m *Model) setRevisionCmd(rev string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	app := m.app
	client, err := m.client(app)
	if err != nil {
		return func() tea.Msg { return specMsg{err: err} }
	}
	ctx := m.ctx
	m.loading, m.loadWhat = true, "target revision"
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, err := client.SetTargetRevision(c, app, rev)
		if err != nil {
			return specMsg{err: err}
		}
		return specMsg{app: out, text: "target revision → " + rev}
	}
}

func (m *Model) setAutoSyncCmd(on, prune, selfHeal bool) tea.Cmd {
	if m.app == nil {
		return nil
	}
	app := m.app
	client, err := m.client(app)
	if err != nil {
		return func() tea.Msg { return specMsg{err: err} }
	}
	ctx := m.ctx
	m.loading, m.loadWhat = true, "sync policy"
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, err := client.SetAutoSync(c, app, on, prune, selfHeal)
		if err != nil {
			return specMsg{err: err}
		}
		text := "auto-sync off"
		if on {
			text = fmt.Sprintf("auto-sync on (prune=%v selfHeal=%v)", prune, selfHeal)
		}
		return specMsg{app: out, text: text}
	}
}

func (m *Model) rollbackCmd(id int64) tea.Cmd {
	if m.app == nil {
		return nil
	}
	app := m.app
	client, err := m.client(app)
	if err != nil {
		return func() tea.Msg { return specMsg{err: err} }
	}
	ctx := m.ctx
	m.loading, m.loadWhat = true, "rollback"
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 300*time.Second)
		defer cancel()
		out, err := client.Rollback(c, app, id, false, false)
		if err != nil {
			return specMsg{err: err}
		}
		return specMsg{app: out, text: fmt.Sprintf("rolled back to id %d", id)}
	}
}

func (m *Model) terminateCmd() tea.Cmd {
	if m.app == nil {
		return nil
	}
	app := m.app
	client, err := m.client(app)
	if err != nil {
		return func() tea.Msg { return specMsg{err: err} }
	}
	ctx := m.ctx
	m.loading, m.loadWhat = true, "terminate"
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := client.TerminateOperation(c, app); err != nil {
			return specMsg{err: err}
		}
		// The terminate endpoint returns no body, so re-read the application
		// rather than leaving the view showing a sync that is no longer running.
		fresh, err := client.GetApplication(c, app.Name(), app.AppNamespace())
		if err != nil {
			return specMsg{text: "sync terminated", err: err}
		}
		return specMsg{app: fresh, text: "sync terminated"}
	}
}

// loadRefsCmd fetches the repository's branches and tags for the picker.
func (m *Model) loadRefsCmd(app argocd.Application) tea.Cmd {
	client, err := m.client(&app)
	if err != nil {
		return func() tea.Msg { return refsMsg{err: err} }
	}
	ctx := m.ctx
	repo, project := app.RepoURL(), app.Spec.Project
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		refs, err := client.RepoRefs(c, repo, project)
		if err != nil {
			return refsMsg{err: err}
		}
		// Branches first: a target revision is usually a branch, and a repo
		// with hundreds of tags would otherwise bury them.
		items := make([]revItem, 0, len(refs.Branches)+len(refs.Tags)+1)
		for _, b := range refs.Branches {
			items = append(items, revItem{name: b, kind: "branch"})
		}
		for _, t := range refs.Tags {
			items = append(items, revItem{name: t, kind: "tag"})
		}
		// HEAD is what Argo CD resolves to the default branch and is a valid
		// target revision, but it is not a ref the server lists.
		items = append(items, revItem{name: "HEAD", kind: "default branch"})
		return refsMsg{items: items}
	}
}
