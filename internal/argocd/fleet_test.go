package argocd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyolk/argx/internal/config"
)

// fakeServer is one stand-in Argo CD. It records the paths it received so the
// tests can prove which server an action actually reached — the property the
// fleet exists to guarantee.
type fakeServer struct {
	*httptest.Server
	name  string
	paths []string
	apps  []string
	fail  int // when non-zero, every request answers with this status
}

func newFakeServer(t *testing.T, name string, apps ...string) *fakeServer {
	t.Helper()
	f := &fakeServer{name: name, apps: apps}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.paths = append(f.paths, r.URL.Path)
		if f.fail != 0 {
			w.WriteHeader(f.fail)
			json.NewEncoder(w).Encode(map[string]string{"message": name + " is unhappy"})
			return
		}
		items := make([]map[string]any, 0, len(f.apps))
		for _, a := range f.apps {
			items = append(items, map[string]any{
				"metadata": map[string]any{"name": a, "namespace": "argocd"},
				"spec":     map[string]any{"project": "default"},
				"status": map[string]any{
					"sync":   map[string]any{"status": "Synced"},
					"health": map[string]any{"status": "Healthy"},
				},
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	t.Cleanup(f.Close)
	return f
}

// ctx builds the config context that points at this fake server.
func (f *fakeServer) ctx() config.Context {
	return config.Context{
		Name:      f.name,
		Server:    strings.TrimPrefix(f.URL, "http://"),
		Token:     "fake",
		PlainText: true,
	}
}

func TestFleetMergesApplicationsFromEveryServer(t *testing.T) {
	a := newFakeServer(t, "alpha", "web", "api")
	b := newFakeServer(t, "beta", "worker")

	f := NewFleet([]config.Context{a.ctx(), b.ctx()})
	apps, errs := f.ListApplications(context.Background(), nil)

	if len(errs) != 0 {
		t.Fatalf("unexpected failures: %v", errs)
	}
	if len(apps) != 3 {
		t.Fatalf("apps = %d, want 3", len(apps))
	}
	// Sorted by name, so the merged list reads as one list rather than as two
	// concatenated ones.
	want := []string{"api", "web", "worker"}
	for i, w := range want {
		if apps[i].Name() != w {
			t.Errorf("apps[%d] = %q, want %q", i, apps[i].Name(), w)
		}
	}
}

// Every application must carry the server it came from, or no later action can
// route itself correctly.
func TestFleetStampsOrigin(t *testing.T) {
	a := newFakeServer(t, "alpha", "web")
	b := newFakeServer(t, "beta", "worker")

	f := NewFleet([]config.Context{a.ctx(), b.ctx()})
	apps, _ := f.ListApplications(context.Background(), nil)

	byName := map[string]string{}
	for _, app := range apps {
		byName[app.Name()] = app.Context
	}
	if byName["web"] != "alpha" || byName["worker"] != "beta" {
		t.Errorf("origins = %v, want web→alpha and worker→beta", byName)
	}
}

// The same application name on two servers must stay two rows, with distinct
// keys — a shared key is how a mark on one silently selects the other.
func TestFleetKeepsSameNameFromDifferentServersDistinct(t *testing.T) {
	a := newFakeServer(t, "alpha", "web")
	b := newFakeServer(t, "beta", "web")

	f := NewFleet([]config.Context{a.ctx(), b.ctx()})
	apps, _ := f.ListApplications(context.Background(), nil)

	if len(apps) != 2 {
		t.Fatalf("apps = %d, want both copies of web", len(apps))
	}
	if apps[0].Key() == apps[1].Key() {
		t.Fatalf("both rows share the key %q — a mark on one would select the other", apps[0].Key())
	}
	// Ties on name break by context, so the two copies are adjacent and in a
	// stable order rather than shuffling between refreshes.
	if apps[0].Context != "alpha" || apps[1].Context != "beta" {
		t.Errorf("order = %q,%q, want alpha,beta", apps[0].Context, apps[1].Context)
	}
}

// One server being down must not hide the servers that answered.
func TestFleetReturnsPartialResultsWhenAServerFails(t *testing.T) {
	a := newFakeServer(t, "alpha", "web")
	b := newFakeServer(t, "beta", "worker")
	b.fail = http.StatusUnauthorized

	f := NewFleet([]config.Context{a.ctx(), b.ctx()})
	apps, errs := f.ListApplications(context.Background(), nil)

	if len(apps) != 1 || apps[0].Name() != "web" {
		t.Fatalf("apps = %v, want the reachable server's application", apps)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one", errs)
	}
	if errs[0].Context != "beta" {
		t.Errorf("the failure names %q, want beta", errs[0].Context)
	}
	if !strings.Contains(errs[0].Error(), "beta") {
		t.Errorf("the error text should name the server: %q", errs[0].Error())
	}
}

// An action must resolve back to the server its application came from.
func TestClientForRoutesToTheOwningServer(t *testing.T) {
	a := newFakeServer(t, "alpha", "web")
	b := newFakeServer(t, "beta", "worker")

	f := NewFleet([]config.Context{a.ctx(), b.ctx()})
	apps, _ := f.ListApplications(context.Background(), nil)

	for i := range apps {
		app := &apps[i]
		c, err := f.ClientFor(app)
		if err != nil {
			t.Fatalf("ClientFor(%s): %v", app.Name(), err)
		}
		if got := c.Context().Name; got != app.Context {
			t.Errorf("%s routed to %q, want %q", app.Name(), got, app.Context)
		}
	}
}

// A stamped context that is not in the fleet must fail loudly. Falling back to
// the first client would send the action to a server the user never chose.
func TestClientForRefusesAnUnknownContext(t *testing.T) {
	a := newFakeServer(t, "alpha", "web")
	f := NewFleet([]config.Context{a.ctx()})

	var stray Application
	stray.Context = "somewhere-else"
	stray.Metadata.Name = "web"

	if _, err := f.ClientFor(&stray); err == nil {
		t.Fatal("an application from an unknown context must not resolve to a client")
	}

	var unstamped Application
	unstamped.Metadata.Name = "web"
	if _, err := f.ClientFor(&unstamped); err == nil {
		t.Fatal("an application with no recorded context must not resolve to a client")
	}
}

// Each application's URL must point at its own server.
func TestURLUsesTheOwningServer(t *testing.T) {
	a := newFakeServer(t, "alpha", "web")
	b := newFakeServer(t, "beta", "worker")

	f := NewFleet([]config.Context{a.ctx(), b.ctx()})
	apps, _ := f.ListApplications(context.Background(), nil)

	for i := range apps {
		app := &apps[i]
		url, err := f.URL(app)
		if err != nil {
			t.Fatalf("URL(%s): %v", app.Name(), err)
		}
		want := a.URL
		if app.Context == "beta" {
			want = b.URL
		}
		if !strings.HasPrefix(url, want) {
			t.Errorf("%s → %s, want a URL on %s", app.Name(), url, want)
		}
	}
}

// A confirmation prompt groups targets by server; the grouping must follow
// fleet order so the same set always renders the same way.
func TestByContextGroupsInFleetOrder(t *testing.T) {
	a := newFakeServer(t, "alpha", "web")
	b := newFakeServer(t, "beta", "worker")
	f := NewFleet([]config.Context{a.ctx(), b.ctx()})

	apps := []Application{
		{Context: "beta"}, {Context: "alpha"}, {Context: "beta"},
	}
	order, groups := f.ByContext(apps)

	if len(order) != 2 || order[0] != "alpha" || order[1] != "beta" {
		t.Fatalf("order = %v, want fleet order alpha,beta", order)
	}
	if len(groups["beta"]) != 2 || len(groups["alpha"]) != 1 {
		t.Errorf("group sizes = beta:%d alpha:%d, want 2 and 1",
			len(groups["beta"]), len(groups["alpha"]))
	}
}

func TestContextsDeduplicates(t *testing.T) {
	apps := []Application{{Context: "a"}, {Context: "b"}, {Context: "a"}}
	got := Contexts(apps)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Contexts() = %v, want [a b] in first-seen order", got)
	}
}

func TestSingleReportsFleetSize(t *testing.T) {
	a := newFakeServer(t, "alpha")
	b := newFakeServer(t, "beta")

	if !NewFleet([]config.Context{a.ctx()}).Single() {
		t.Error("a one-server fleet should report Single")
	}
	if NewFleet([]config.Context{a.ctx(), b.ctx()}).Single() {
		t.Error("a two-server fleet should not report Single")
	}
}

// A response larger than the read limit must be reported as too large, never
// parsed as a truncated document.
//
// This is a real failure, not a hypothetical: a control plane with a few
// thousand applications returns well past 64 MiB for `applications` — every
// Application carries its full spec and status — and the earlier 64 MiB
// LimitReader silently cut the body, producing "unexpected end of JSON input"
// that read as a parse bug rather than a size one.
func TestOversizedResponseIsReportedNotTruncated(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A valid JSON prefix, then more than the limit of filler. A truncating
		// reader yields a body that parses as malformed; a bounded one refuses.
		w.Write([]byte(`{"items":[{"metadata":{"name":"`))
		chunk := strings.Repeat("x", 1<<20)
		for written := 0; written <= int(maxResponse); written += len(chunk) {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(big.Close)

	c := New(config.Context{
		Name:      "big",
		Server:    strings.TrimPrefix(big.URL, "http://"),
		Token:     "t",
		PlainText: true,
	})
	_, err := c.ListApplications(context.Background(), nil)
	if err == nil {
		t.Fatal("an oversized response must be an error, not a silently truncated parse")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("the error should say the response was too large, got: %v", err)
	}
}

// A body that exactly fills the limit is not truncated and must still parse.
func TestResponseAtTheLimitStillParses(t *testing.T) {
	// readBounded reads limit+1 to tell "full" from "overran"; prove the
	// boundary itself is accepted rather than rejected off by one.
	body := []byte(`{"items":[]}`)
	got, err := readBounded(strings.NewReader(string(body)), int64(len(body)))
	if err != nil {
		t.Fatalf("a body exactly at the limit was rejected: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("readBounded returned %q, want the whole body", got)
	}
}
