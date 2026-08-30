package argocd

// Pod-level reads: the containers a pod has, and the logs they produce.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Container is one container in a pod.
type Container struct {
	Name string
	// Init marks an init container, which has logs but cannot be exec'd into
	// once it has finished.
	Init bool
	// Image is shown beside the name in the picker: two containers named
	// `app` and `sidecar` say little, their images say what they are.
	Image string
}

// PodContainers lists a pod's containers, in the order Kubernetes reports them.
//
// The names come from the live manifest because nothing else carries them: the
// resource tree has the images but not the names, and the logs endpoint needs a
// name. Init containers are included — they have logs, and a pod stuck in Init
// is exactly when someone goes looking for them.
func (c *Client) PodContainers(ctx context.Context, app string, r ResourceRef) ([]Container, error) {
	manifest, err := c.ResourceManifest(ctx, app, r)
	if err != nil {
		return nil, err
	}
	var pod struct {
		Spec struct {
			Containers []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
			} `json:"containers"`
			InitContainers []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
			} `json:"initContainers"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(manifest), &pod); err != nil {
		return nil, fmt.Errorf("parse pod manifest: %w", err)
	}

	out := make([]Container, 0,
		len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
	for _, cc := range pod.Spec.InitContainers {
		out = append(out, Container{Name: cc.Name, Image: cc.Image, Init: true})
	}
	for _, cc := range pod.Spec.Containers {
		out = append(out, Container{Name: cc.Name, Image: cc.Image})
	}
	return out, nil
}

// Refresh re-reads the application's source and recomputes its sync status.
// hard also invalidates the repo-server manifest cache.
func (c *Client) Refresh(ctx context.Context, name, appNamespace string, hard bool) (*Application, error) {
	q := url.Values{}
	q.Set("refresh", map[bool]string{true: "hard", false: "normal"}[hard])
	if appNamespace != "" {
		q.Set("appNamespace", appNamespace)
	}
	var app Application
	if err := c.do(ctx, http.MethodGet, "/api/v1/applications/"+url.PathEscape(name), q, nil, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// SyncOptions selects what a sync does. The zero value is a plain sync of the
// whole application at its configured target revision.
type SyncOptions struct {
	// Prune deletes resources that are no longer in the desired state.
	Prune bool
	// DryRun asks the server to compute the result without applying it.
	DryRun bool
	// Resources limits the sync to specific resources; empty syncs everything.
	Resources []ResourceRef
	// AppNamespace scopes the request for apps outside the control-plane
	// namespace.
	AppNamespace string
}

// Sync triggers a sync of the application.
func (c *Client) Sync(ctx context.Context, name string, opt SyncOptions) (*Application, error) {
	type syncResource struct {
		Group     string `json:"group"`
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	body := struct {
		Name         string         `json:"name"`
		AppNamespace string         `json:"appNamespace,omitempty"`
		Prune        bool           `json:"prune"`
		DryRun       bool           `json:"dryRun"`
		Resources    []syncResource `json:"resources,omitempty"`
	}{Name: name, AppNamespace: opt.AppNamespace, Prune: opt.Prune, DryRun: opt.DryRun}

	for _, r := range opt.Resources {
		body.Resources = append(body.Resources, syncResource{
			Group: r.Group, Kind: r.Kind, Name: r.Name, Namespace: r.Namespace,
		})
	}

	var app Application
	p := "/api/v1/applications/" + url.PathEscape(name) + "/sync"
	if err := c.do(ctx, http.MethodPost, p, nil, body, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// Events fetches the Kubernetes events Argo CD associates with an application.
func (c *Client) Events(ctx context.Context, name, appNamespace string) ([]Event, error) {
	q := url.Values{}
	if appNamespace != "" {
		q.Set("appNamespace", appNamespace)
	}
	var out struct {
		Items []Event `json:"items"`
	}
	p := "/api/v1/applications/" + url.PathEscape(name) + "/events"
	if err := c.do(ctx, http.MethodGet, p, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// SyncWindow is one allow or deny window on an AppProject.
//
// Windows are what stop a sync from running outside an agreed maintenance
// period. They are defined per project, so one window governs every application
// in it — which is why argx shows them but does not edit them.
type SyncWindow struct {
	// Kind is "allow" or "deny".
	Kind string `json:"kind"`
	// Schedule is when the window opens, in cron format.
	Schedule string `json:"schedule"`
	// Duration is how long it stays open, e.g. "1h30m".
	Duration string `json:"duration"`
	// TimeZone the schedule is interpreted in; empty means UTC.
	TimeZone string `json:"timeZone"`
	// ManualSync allows a human-triggered sync during a window that would
	// otherwise block one.
	ManualSync bool `json:"manualSync"`

	// The selectors that decide which applications a window applies to. All
	// empty means the whole project.
	Applications []string `json:"applications"`
	Clusters     []string `json:"clusters"`
	Namespaces   []string `json:"namespaces"`
}

// Zone renders the window's time zone, naming the default rather than leaving
// it blank — a schedule with no zone is read in UTC, and not saying so invites
// reading it as local time.
func (w SyncWindow) Zone() string {
	if w.TimeZone == "" {
		return "UTC"
	}
	return w.TimeZone
}

// Blocks reports whether this window prevents syncing while it is open.
func (w SyncWindow) Blocks() bool { return w.Kind == "deny" }

// AppSyncWindows is what applies to one application right now.
type AppSyncWindows struct {
	// ActiveWindows are open at this moment.
	ActiveWindows []SyncWindow `json:"activeWindows"`
	// AssignedWindows are every window that governs this application,
	// open or not.
	AssignedWindows []SyncWindow `json:"assignedWindows"`
	// CanSync is the server's own verdict, which accounts for the interaction
	// between allow and deny windows — argx reports it rather than recomputing
	// it, because the precedence rules are the server's to define.
	CanSync bool `json:"canSync"`
}

// SyncWindows fetches the windows governing one application.
func (c *Client) SyncWindows(ctx context.Context, app *Application) (*AppSyncWindows, error) {
	q := url.Values{}
	if ns := app.AppNamespace(); ns != "" {
		q.Set("appNamespace", ns)
	}
	if p := app.Spec.Project; p != "" {
		q.Set("project", p)
	}
	var out AppSyncWindows
	p := "/api/v1/applications/" + url.PathEscape(app.Name()) + "/syncwindows"
	if err := c.do(ctx, http.MethodGet, p, q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProjectSyncWindows fetches every window defined on a project.
//
// This reads the project's spec, not /projects/{name}/syncwindows — that
// endpoint returns only the windows that are *open right now*
// (server/project/project.go calls SyncWindows.Active()), so a window that has
// not opened yet is simply absent from it. argx needs the whole set: a closed
// window is exactly the one a scheduled sync is waiting for.
//
// The spec is also the only place the fields live. The per-application payload
// carries kind, schedule, duration and manualSync and nothing else — no time
// zone, no selectors (server/application/application.go, convertSyncWindows) —
// and an Asia/Seoul schedule read as UTC is nine hours off.
func (c *Client) ProjectSyncWindows(ctx context.Context, project string) ([]SyncWindow, error) {
	var out struct {
		Spec struct {
			SyncWindows []SyncWindow `json:"syncWindows"`
		} `json:"spec"`
	}
	p := "/api/v1/projects/" + url.PathEscape(project)
	if err := c.do(ctx, http.MethodGet, p, nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Spec.SyncWindows, nil
}

// UserInfo is who the server thinks argx is.
type UserInfo struct {
	LoggedIn bool     `json:"loggedIn"`
	Username string   `json:"username"`
	Iss      string   `json:"iss"`
	Groups   []string `json:"groups"`
}

// WhoAmI asks the server who this token authenticates as.
func (c *Client) WhoAmI(ctx context.Context) (*UserInfo, error) {
	var out UserInfo
	if err := c.do(ctx, http.MethodGet, "/api/v1/session/userinfo", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CanI asks whether this token may perform an action.
//
// The server answers from its own RBAC, which is the only authority on it:
// reading the policy and evaluating it here would be a second, divergent
// answer to a question the server already answers.
func (c *Client) CanI(ctx context.Context, resource, action, subresource string) (bool, error) {
	if subresource == "" {
		subresource = "*/*"
	}
	var out struct {
		Value string `json:"value"`
	}
	p := "/api/v1/account/can-i/" + url.PathEscape(resource) + "/" +
		url.PathEscape(action) + "/" + subresource
	if err := c.do(ctx, http.MethodGet, p, nil, nil, &out); err != nil {
		return false, err
	}
	return out.Value == "yes", nil
}
