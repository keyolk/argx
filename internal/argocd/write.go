package argocd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Mutating calls. Every one of these changes an application's spec or triggers
// an operation against a live cluster; the TUI gates each behind an explicit
// confirmation, and nothing here confirms on its own.

// patch applies a merge patch to the application.
//
// A merge patch rather than PUT /spec: PUT replaces the whole spec with what
// argx modeled, so any field argx does not know about — ignoreDifferences,
// info, revisionHistoryLimit, per-source settings — would be silently dropped.
// A merge patch touches only the keys it names.
func (c *Client) patch(ctx context.Context, name, appNamespace string, patch map[string]any) (*Application, error) {
	b, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("encode patch: %w", err)
	}
	body := struct {
		Name         string `json:"name"`
		AppNamespace string `json:"appNamespace,omitempty"`
		Patch        string `json:"patch"`
		PatchType    string `json:"patchType"`
	}{Name: name, AppNamespace: appNamespace, Patch: string(b), PatchType: "merge"}

	var app Application
	p := "/api/v1/applications/" + url.PathEscape(name)
	if err := c.do(ctx, http.MethodPatch, p, nil, body, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// SetTargetRevision points the application's source at a different revision.
//
// Only single-source applications are supported: a merge patch on `source`
// has no way to address one element of the `sources` array, and guessing an
// index would rewrite the wrong source.
func (c *Client) SetTargetRevision(ctx context.Context, app *Application, rev string) (*Application, error) {
	if !app.SingleSource() {
		return nil, fmt.Errorf("%s has multiple sources — edit its target revision in the Argo CD UI", app.Name())
	}
	return c.patch(ctx, app.Name(), app.AppNamespace(), map[string]any{
		"spec": map[string]any{
			"source": map[string]any{"targetRevision": rev},
		},
	})
}

// SetAutoSync turns automated sync on or off, carrying the prune and self-heal
// flags when turning it on.
//
// Turning it off sends an explicit null rather than omitting the key: a merge
// patch treats a missing key as "leave alone", so omitting it would silently do
// nothing and report success.
func (c *Client) SetAutoSync(ctx context.Context, app *Application, on, prune, selfHeal bool) (*Application, error) {
	var automated any
	if on {
		automated = map[string]any{"prune": prune, "selfHeal": selfHeal}
	}
	return c.patch(ctx, app.Name(), app.AppNamespace(), map[string]any{
		"spec": map[string]any{
			"syncPolicy": map[string]any{"automated": automated},
		},
	})
}

// Rollback re-syncs the application to a previous history entry.
//
// Argo CD refuses a rollback while automated sync is enabled — the controller
// would immediately sync forward again — so the caller must turn auto-sync off
// first. The server's own error says as much, and it is surfaced verbatim.
func (c *Client) Rollback(ctx context.Context, app *Application, id int64, prune, dryRun bool) (*Application, error) {
	body := struct {
		Name         string `json:"name"`
		AppNamespace string `json:"appNamespace,omitempty"`
		ID           int64  `json:"id"`
		Prune        bool   `json:"prune"`
		DryRun       bool   `json:"dryRun"`
	}{Name: app.Name(), AppNamespace: app.AppNamespace(), ID: id, Prune: prune, DryRun: dryRun}

	var out Application
	p := "/api/v1/applications/" + url.PathEscape(app.Name()) + "/rollback"
	if err := c.do(ctx, http.MethodPost, p, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TerminateOperation stops the sync currently in progress.
//
// The stopped sync leaves the application partially applied — that is inherent
// to terminating, not a defect, and the TUI says so before asking.
func (c *Client) TerminateOperation(ctx context.Context, app *Application) error {
	q := url.Values{}
	if ns := app.AppNamespace(); ns != "" {
		q.Set("appNamespace", ns)
	}
	p := "/api/v1/applications/" + url.PathEscape(app.Name()) + "/operation"
	return c.do(ctx, http.MethodDelete, p, q, nil, nil)
}

// RevisionMetadata is the commit behind a revision, used to label history
// entries and candidate revisions with something a human recognizes.
type RevisionMetadata struct {
	Author  string   `json:"author"`
	Date    string   `json:"date"`
	Message string   `json:"message"`
	Tags    []string `json:"tags"`
}

// RevisionMetadata fetches the commit details for a revision.
func (c *Client) RevisionMetadata(ctx context.Context, app *Application, revision string) (*RevisionMetadata, error) {
	q := url.Values{}
	if ns := app.AppNamespace(); ns != "" {
		q.Set("appNamespace", ns)
	}
	var md RevisionMetadata
	p := "/api/v1/applications/" + url.PathEscape(app.Name()) +
		"/revisions/" + url.PathEscape(revision) + "/metadata"
	if err := c.do(ctx, http.MethodGet, p, q, nil, &md); err != nil {
		return nil, err
	}
	return &md, nil
}

// RepoRefs lists the branches and tags of a repository, so a target revision
// can be picked from what actually exists rather than typed from memory.
type RepoRefs struct {
	Branches []string `json:"branches"`
	Tags     []string `json:"tags"`
}

// RepoRefs fetches the branches and tags Argo CD can see for a repository.
//
// The project is passed through because Argo CD scopes repository access by
// project; omitting it fails for a repo the session can only reach through the
// application's project.
//
// The repo must already be registered with Argo CD — argx does not add
// credentials, so an unregistered repo returns the server's own error.
func (c *Client) RepoRefs(ctx context.Context, repoURL, project string) (*RepoRefs, error) {
	q := url.Values{}
	if project != "" {
		q.Set("appProject", project)
	}
	var refs RepoRefs
	p := "/api/v1/repositories/" + url.PathEscape(repoURL) + "/refs"
	if err := c.do(ctx, http.MethodGet, p, q, nil, &refs); err != nil {
		return nil, err
	}
	return &refs, nil
}
