package argocd

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/keyolk/argx/internal/config"
)

// Fleet is a set of Argo CD servers queried as one.
//
// Every application argx holds carries the name of the server it came from, and
// each action resolves that name back to a client. Nothing in the TUI holds a
// bare client, because a single ambient "current server" is exactly how an
// action ends up pointed at the wrong cluster.
type Fleet struct {
	clients []*Client
	byName  map[string]*Client
}

// NewFleet builds a fleet over the given contexts, in the order given.
func NewFleet(ctxs []config.Context) *Fleet {
	f := &Fleet{byName: make(map[string]*Client, len(ctxs))}
	for _, c := range ctxs {
		cl := New(c)
		f.clients = append(f.clients, cl)
		f.byName[c.Name] = cl
	}
	return f
}

// Clients returns every client in fleet order.
func (f *Fleet) Clients() []*Client { return f.clients }

// Names returns the context names in fleet order.
func (f *Fleet) Names() []string {
	out := make([]string, 0, len(f.clients))
	for _, c := range f.clients {
		out = append(out, c.Context().Name)
	}
	return out
}

// Single reports whether the fleet has exactly one server, which is what the
// TUI checks before spending header width on a context column.
func (f *Fleet) Single() bool { return len(f.clients) == 1 }

// Client resolves the client for a context name.
//
// A miss is an error rather than a fallback to the first client: silently
// acting on a different server than the one an application came from is the
// worst outcome this package can produce.
func (f *Fleet) Client(ctxName string) (*Client, error) {
	if c, ok := f.byName[ctxName]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("no argocd context named %q in this session", ctxName)
}

// ClientFor resolves the client that owns an application.
func (f *Fleet) ClientFor(app *Application) (*Client, error) {
	if app.Context == "" {
		return nil, fmt.Errorf("application %q has no recorded context", app.Name())
	}
	return f.Client(app.Context)
}

// FleetError is one server's failure inside an otherwise successful fleet call.
//
// A fleet query does not fail as a whole when one server is down or its token
// expired: the other servers still have answers, and hiding them because a
// third is unreachable helps nobody. The failures come back alongside the
// results so the UI can show both.
type FleetError struct {
	Context string
	Err     error
}

func (e FleetError) Error() string { return e.Context + ": " + e.Err.Error() }

// ListApplications fetches applications from every server concurrently.
//
// Results are returned sorted by name then context, so an application present
// on two servers appears as adjacent rows rather than scattered.
func (f *Fleet) ListApplications(ctx context.Context, projects []string) ([]Application, []FleetError) {
	type result struct {
		apps []Application
		err  *FleetError
	}
	results := make([]result, len(f.clients))

	var wg sync.WaitGroup
	for i, c := range f.clients {
		wg.Add(1)
		go func(i int, c *Client) {
			defer wg.Done()
			name := c.Context().Name
			apps, err := c.ListApplications(ctx, projects)
			if err != nil {
				results[i] = result{err: &FleetError{Context: name, Err: err}}
				return
			}
			// Stamp the origin here, at the only place that knows it, so no
			// later code has to infer which server an application came from.
			for j := range apps {
				apps[j].Context = name
			}
			results[i] = result{apps: apps}
		}(i, c)
	}
	wg.Wait()

	var all []Application
	var errs []FleetError
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, *r.err)
			continue
		}
		all = append(all, r.apps...)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Name() != all[j].Name() {
			return all[i].Name() < all[j].Name()
		}
		return all[i].Context < all[j].Context
	})
	return all, errs
}

// AppRef identifies one application within the fleet: the context and the name
// together, since the same application name can exist on several servers.
type AppRef struct {
	Context      string
	Name         string
	AppNamespace string
}

// Ref is the fleet-wide identity of an application.
func (a *Application) Ref() AppRef {
	return AppRef{Context: a.Context, Name: a.Name(), AppNamespace: a.AppNamespace()}
}

// Key is the fleet-unique key for an application, used for marks and lookups.
//
// Keying on name alone would let a mark on `web` in one server silently select
// `web` in another — the exact mistake the fleet exists to prevent.
func (r AppRef) Key() string { return r.Context + "/" + r.Name }

// Key is the fleet-unique key of the application.
func (a *Application) Key() string { return a.Ref().Key() }

// URL is the web UI address of the application on its own server.
func (f *Fleet) URL(app *Application) (string, error) {
	c, err := f.ClientFor(app)
	if err != nil {
		return "", err
	}
	return c.Context().AppURL(app.Name()), nil
}

// ByContext groups applications by the server they came from, preserving fleet
// order for the groups and input order within each.
//
// This is what a confirmation prompt renders: seeing "3 apps" without seeing
// that they span two servers is how a change lands somewhere unintended.
func (f *Fleet) ByContext(apps []Application) ([]string, map[string][]Application) {
	groups := make(map[string][]Application, len(f.clients))
	for _, a := range apps {
		groups[a.Context] = append(groups[a.Context], a)
	}
	var order []string
	for _, c := range f.clients {
		if n := c.Context().Name; len(groups[n]) > 0 {
			order = append(order, n)
		}
	}
	return order, groups
}

// Contexts returns the distinct contexts represented in a set of applications.
func Contexts(apps []Application) []string {
	seen := make(map[string]bool, len(apps))
	var out []string
	for _, a := range apps {
		if !seen[a.Context] {
			seen[a.Context] = true
			out = append(out, a.Context)
		}
	}
	return out
}
