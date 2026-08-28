// Package argocd is a thin REST client for the Argo CD API server.
//
// Only the endpoints argx needs are modeled, and every response type keeps just
// the fields the TUI renders. Going through REST rather than shelling out to
// the argocd binary means argx controls its own timeouts, can fetch several
// applications concurrently, and does not inherit CLI version drift.
package argocd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keyolk/argx/internal/config"
)

// Client talks to one Argo CD API server.
type Client struct {
	ctx  config.Context
	http *http.Client
}

// New builds a client for the given context.
func New(ctx config.Context) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if ctx.Insecure {
		// Deliberate: the user already opted into --insecure for this server in
		// their argocd config, and refusing here would just push them back to
		// the CLI.
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		ctx:  ctx,
		http: &http.Client{Transport: tr, Timeout: 60 * time.Second},
	}
}

// Context exposes the connection settings, mainly so callers can build web UI
// URLs without holding a second copy of the config.
func (c *Client) Context() config.Context { return c.ctx }

// APIError carries the HTTP status so callers can distinguish auth failure from
// a missing application.
type APIError struct {
	Status int
	Msg    string
	Path   string
	// Server is carried so the 401 message can name the server to log in to;
	// an expired token is the most common failure and "run argocd login" is
	// useless without it.
	Server string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return fmt.Sprintf("session expired — run `argocd login %s` (%s)", e.Server, e.Msg)
	case http.StatusForbidden:
		return fmt.Sprintf("forbidden: %s", e.Msg)
	case http.StatusNotFound:
		return fmt.Sprintf("not found: %s (%s)", e.Path, e.Msg)
	default:
		return fmt.Sprintf("%s: HTTP %d: %s", e.Path, e.Status, e.Msg)
	}
}

// Unauthorized reports whether the session needs a fresh `argocd login`.
func (e *APIError) Unauthorized() bool { return e.Status == http.StatusUnauthorized }

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	u := c.ctx.BaseURL() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.ctx.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := readBounded(resp.Body, maxResponse)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Msg: apiMessage(raw), Path: path, Server: c.ctx.Server}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// maxResponse bounds a single response body.
//
// The limit exists so a runaway response cannot exhaust memory, not to trim
// anything argx expects — a real fleet's `applications` payload is large:
// a control plane with a few thousand applications returns well past 64 MiB,
// because every Application carries its full spec and status. A limit that
// silently truncated at 64 MiB produced a corrupt JSON document and an
// "unexpected end of JSON input" that read as a parse bug rather than a size
// one.
const maxResponse = 512 << 20

// readBounded reads up to limit bytes and reports truncation as an error.
//
// io.LimitReader alone cannot: it returns a short body with a nil error, which
// downstream JSON decoding then reports as a malformed document. Distinguishing
// the two is the difference between "the server sent something odd" and "argx
// cut the response off".
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	// One byte past the limit, so a body that exactly fills it is still
	// distinguishable from one that overran.
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response exceeds %d MiB — refusing to parse a truncated body",
			limit>>20)
	}
	return b, nil
}

// apiMessage pulls the human-readable part out of an Argo CD error body, which
// is `{"error":"...","message":"..."}` on the gRPC-gateway paths and plain text
// elsewhere.
func apiMessage(b []byte) string {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &e) == nil {
		if e.Message != "" {
			return e.Message
		}
		if e.Error != "" {
			return e.Error
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		return "(empty response)"
	}
	return s
}

// ListApplications fetches every application visible to the session.
//
// The projects filter is applied server-side; an empty slice means no filter.
func (c *Client) ListApplications(ctx context.Context, projects []string) ([]Application, error) {
	q := url.Values{}
	for _, p := range projects {
		q.Add("projects", p)
	}
	var out struct {
		Items []Application `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/applications", q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// GetApplication fetches one application including its resource tree status.
func (c *Client) GetApplication(ctx context.Context, name, appNamespace string) (*Application, error) {
	q := url.Values{}
	if appNamespace != "" {
		q.Set("appNamespace", appNamespace)
	}
	var app Application
	if err := c.do(ctx, http.MethodGet, "/api/v1/applications/"+url.PathEscape(name), q, nil, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ResourceTree fetches the live resource tree — the nodes shown in the Argo CD
// UI's application view, including their health and parent links.
func (c *Client) ResourceTree(ctx context.Context, name, appNamespace string) (*Tree, error) {
	q := url.Values{}
	if appNamespace != "" {
		q.Set("appNamespace", appNamespace)
	}
	var t Tree
	p := "/api/v1/applications/" + url.PathEscape(name) + "/resource-tree"
	if err := c.do(ctx, http.MethodGet, p, q, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ManagedResources fetches the desired-vs-live diff payload for an application.
// Each entry carries the normalized live and target manifests, which is what
// the diff view renders.
func (c *Client) ManagedResources(ctx context.Context, name, appNamespace string) ([]ResourceDiff, error) {
	q := url.Values{}
	if appNamespace != "" {
		q.Set("appNamespace", appNamespace)
	}
	var out struct {
		Items []ResourceDiff `json:"items"`
	}
	p := "/api/v1/applications/" + url.PathEscape(name) + "/managed-resources"
	if err := c.do(ctx, http.MethodGet, p, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ResourceManifest fetches the live manifest of a single resource in the tree.
func (c *Client) ResourceManifest(ctx context.Context, app string, r ResourceRef) (string, error) {
	q := url.Values{}
	q.Set("namespace", r.Namespace)
	q.Set("resourceName", r.Name)
	q.Set("version", r.Version)
	q.Set("kind", r.Kind)
	q.Set("group", r.Group)
	if r.AppNamespace != "" {
		q.Set("appNamespace", r.AppNamespace)
	}
	var out struct {
		Manifest string `json:"manifest"`
	}
	p := "/api/v1/applications/" + url.PathEscape(app) + "/resource"
	if err := c.do(ctx, http.MethodGet, p, q, nil, &out); err != nil {
		return "", err
	}
	return out.Manifest, nil
}

// PodLogs fetches a bounded tail of a pod's logs.
//
// The endpoint streams newline-delimited JSON even for a bounded request, so the
// response is decoded incrementally rather than unmarshalled as one document.
func (c *Client) PodLogs(ctx context.Context, app string, r ResourceRef, container string, tail int) (string, error) {
	q := url.Values{}
	q.Set("namespace", r.Namespace)
	q.Set("podName", r.Name)
	q.Set("tailLines", fmt.Sprint(tail))
	q.Set("follow", "false")
	if container != "" {
		q.Set("container", container)
	}
	if r.AppNamespace != "" {
		q.Set("appNamespace", r.AppNamespace)
	}

	u := c.ctx.BaseURL() + "/api/v1/applications/" + url.PathEscape(app) + "/logs?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.ctx.Token)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch logs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", &APIError{Status: resp.StatusCode, Msg: apiMessage(raw), Path: "logs", Server: c.ctx.Server}
	}

	var sb strings.Builder
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponse))
	for {
		var line struct {
			Result struct {
				Content   string `json:"content"`
				TimeStamp string `json:"timeStamp"`
				Last      bool   `json:"last"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := dec.Decode(&line); err != nil {
			if err == io.EOF {
				break
			}
			// A partial stream is still worth showing; surface what arrived
			// rather than discarding it for a trailing decode error.
			break
		}
		if line.Error != nil && line.Error.Message != "" {
			return sb.String(), fmt.Errorf("logs: %s", line.Error.Message)
		}
		if line.Result.Last && line.Result.Content == "" {
			break
		}
		sb.WriteString(line.Result.Content)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
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

// ProjectSyncWindows fetches every window defined on a project, including those
// that do not apply to any particular application.
func (c *Client) ProjectSyncWindows(ctx context.Context, project string) ([]SyncWindow, error) {
	var out struct {
		Windows []SyncWindow `json:"windows"`
	}
	p := "/api/v1/projects/" + url.PathEscape(project) + "/syncwindows"
	if err := c.do(ctx, http.MethodGet, p, nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Windows, nil
}
