package argocd

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// ApplicationSet is the generator that produces applications.
//
// A fleet has far fewer ApplicationSets than Applications — a few dozen against
// a few thousand — and each one explains a whole family of them. Listing them is
// how you answer "what generates all these?", which the application list alone
// cannot.
type ApplicationSet struct {
	// Context is the argx context this came from; stamped on arrival, like
	// Application.Context, so every later action resolves the right server.
	Context string `json:"-"`

	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Labels            map[string]string `json:"labels"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
	} `json:"metadata"`

	Spec struct {
		GoTemplate bool              `json:"goTemplate"`
		Generators []AppSetGenerator `json:"generators"`
		Template   AppSetTemplate    `json:"template"`
		SyncPolicy *AppSetSyncPolicy `json:"syncPolicy"`
		Strategy   *AppSetStrategy   `json:"strategy"`
	} `json:"spec"`

	Status struct {
		Conditions        []AppSetCondition         `json:"conditions"`
		ApplicationStatus []AppSetApplicationStatus `json:"applicationStatus"`
	} `json:"status"`
}

// Name is the ApplicationSet's name.
func (a *ApplicationSet) Name() string { return a.Metadata.Name }

// Namespace is where the ApplicationSet CR lives.
func (a *ApplicationSet) Namespace() string { return a.Metadata.Namespace }

// Key is the fleet-unique key, for the same reason Application.Key exists: two
// servers can host an ApplicationSet with the same name.
func (a *ApplicationSet) Key() string { return a.Context + "/" + a.Metadata.Name }

// Project is the project its generated applications land in.
func (a *ApplicationSet) Project() string { return a.Spec.Template.Spec.Project }

// AppSetTemplate is the Application each generated entry is rendered from.
type AppSetTemplate struct {
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Project     string      `json:"project"`
		Source      *Source     `json:"source"`
		Sources     []Source    `json:"sources"`
		Destination Destination `json:"destination"`
		SyncPolicy  *SyncPolicy `json:"syncPolicy"`
	} `json:"spec"`
}

// PrimarySource is the template's source, with the same multi-source rule as an
// Application's.
func (t AppSetTemplate) PrimarySource() (Source, int) {
	if len(t.Spec.Sources) > 0 {
		return t.Spec.Sources[0], len(t.Spec.Sources)
	}
	if t.Spec.Source != nil {
		return *t.Spec.Source, 1
	}
	return Source{}, 0
}

// AppSetGenerator is one generator entry.
//
// Only the discriminating fields are modeled: what argx needs is the generator's
// *kind* and enough of its configuration to say what it draws from, not a
// faithful reproduction of every generator's schema.
type AppSetGenerator struct {
	List     *AppSetListGenerator    `json:"list"`
	Clusters *AppSetClusterGenerator `json:"clusters"`
	Git      *AppSetGitGenerator     `json:"git"`
	Matrix   *AppSetNestedGenerator  `json:"matrix"`
	Merge    *AppSetNestedGenerator  `json:"merge"`
	SCM      map[string]any          `json:"scmProvider"`
	PR       map[string]any          `json:"pullRequest"`
	Plugin   map[string]any          `json:"plugin"`
	// Selector narrows what a generator produces; present on any of them.
	Selector map[string]any `json:"selector"`
}

type AppSetListGenerator struct {
	Elements []map[string]any `json:"elements"`
}

type AppSetClusterGenerator struct {
	Selector map[string]any    `json:"selector"`
	Values   map[string]string `json:"values"`
}

type AppSetGitGenerator struct {
	RepoURL     string           `json:"repoURL"`
	Revision    string           `json:"revision"`
	Directories []map[string]any `json:"directories"`
	Files       []map[string]any `json:"files"`
}

// AppSetNestedGenerator is a matrix or merge, which combine other generators.
type AppSetNestedGenerator struct {
	Generators []AppSetGenerator `json:"generators"`
}

type AppSetSyncPolicy struct {
	// PreserveResourcesOnDeletion keeps generated applications when the
	// ApplicationSet is deleted.
	PreserveResourcesOnDeletion bool `json:"preserveResourcesOnDeletion"`
	// ApplicationsSync is "create-only", "create-update", or "create-delete";
	// empty means the controller fully manages its applications.
	ApplicationsSync string `json:"applicationsSync"`
}

type AppSetStrategy struct {
	Type string `json:"type"`
}

// AppSetCondition is an ApplicationSet condition.
//
// Unlike an Application's, it carries a Status — a condition is present whether
// or not it holds, and only Status "True" means it applies. Reading the type
// alone would report every ApplicationSet as failing.
type AppSetCondition struct {
	Type     string    `json:"type"`
	Status   string    `json:"status"`
	Reason   string    `json:"reason"`
	Message  string    `json:"message"`
	LastTime time.Time `json:"lastTransitionTime"`
}

// AppSetApplicationStatus is the ApplicationSet's own view of one application
// it generated, which is distinct from that application's sync and health.
type AppSetApplicationStatus struct {
	Application string    `json:"application"`
	Status      string    `json:"status"`
	Message     string    `json:"message"`
	Step        string    `json:"step"`
	LastTime    time.Time `json:"lastTransitionTime"`
}

// GeneratorKinds names the generators in order, e.g. ["git", "clusters"].
//
// The kind is what distinguishes one ApplicationSet from another at a glance —
// a git generator walks a repository, a cluster generator fans out over
// registered clusters — so it is what the list column shows.
func (a *ApplicationSet) GeneratorKinds() []string {
	return generatorKinds(a.Spec.Generators)
}

func generatorKinds(gens []AppSetGenerator) []string {
	var out []string
	for _, g := range gens {
		switch {
		case g.Matrix != nil:
			// A matrix is its children crossed together; naming them is more
			// use than the word "matrix" on its own.
			out = append(out, "matrix("+strings.Join(generatorKinds(g.Matrix.Generators), "×")+")")
		case g.Merge != nil:
			out = append(out, "merge("+strings.Join(generatorKinds(g.Merge.Generators), "+")+")")
		case g.Git != nil:
			out = append(out, "git")
		case g.Clusters != nil:
			out = append(out, "clusters")
		case g.List != nil:
			out = append(out, "list")
		case g.SCM != nil:
			out = append(out, "scm")
		case g.PR != nil:
			out = append(out, "pullRequest")
		case g.Plugin != nil:
			out = append(out, "plugin")
		default:
			out = append(out, "?")
		}
	}
	return out
}

// Degraded reports whether the ApplicationSet is in a state worth looking at.
//
// An ApplicationSet's conditions are where a generator failure surfaces — a
// repository it cannot read, a template that will not render — and that failure
// is invisible in the application list, because the applications it would have
// generated simply do not exist.
func (a *ApplicationSet) Degraded() bool {
	for _, c := range a.Status.Conditions {
		if c.Status == "True" && (c.Type == "ErrorOccurred" || strings.Contains(strings.ToLower(c.Type), "error")) {
			return true
		}
	}
	return false
}

// ErrorCondition is the first error condition, for display.
func (a *ApplicationSet) ErrorCondition() (AppSetCondition, bool) {
	for _, c := range a.Status.Conditions {
		if c.Status == "True" && (c.Type == "ErrorOccurred" || strings.Contains(strings.ToLower(c.Type), "error")) {
			return c, true
		}
	}
	return AppSetCondition{}, false
}

// ListApplicationSets fetches the ApplicationSets on one server.
func (c *Client) ListApplicationSets(ctx context.Context, projects []string) ([]ApplicationSet, error) {
	q := url.Values{}
	for _, p := range projects {
		q.Add("projects", p)
	}
	var out struct {
		Items []ApplicationSet `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/applicationsets", q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// GetApplicationSet fetches one ApplicationSet.
func (c *Client) GetApplicationSet(ctx context.Context, name, namespace string) (*ApplicationSet, error) {
	q := url.Values{}
	if namespace != "" {
		q.Set("appsetNamespace", namespace)
	}
	var out ApplicationSet
	p := "/api/v1/applicationsets/" + url.PathEscape(name)
	if err := c.do(ctx, http.MethodGet, p, q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListApplicationSets fetches from every server concurrently, exactly as the
// application list does, so a fleet reads as one list rather than several.
func (f *Fleet) ListApplicationSets(ctx context.Context, projects []string) ([]ApplicationSet, []FleetError) {
	type result struct {
		sets []ApplicationSet
		err  *FleetError
	}
	results := make([]result, len(f.clients))

	var wg sync.WaitGroup
	for i, c := range f.clients {
		wg.Add(1)
		go func(i int, c *Client) {
			defer wg.Done()
			name := c.Context().Name
			sets, err := c.ListApplicationSets(ctx, projects)
			if err != nil {
				results[i] = result{err: &FleetError{Context: name, Err: err}}
				return
			}
			for j := range sets {
				sets[j].Context = name
			}
			results[i] = result{sets: sets}
		}(i, c)
	}
	wg.Wait()

	var all []ApplicationSet
	var errs []FleetError
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, *r.err)
			continue
		}
		all = append(all, r.sets...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Name() != all[j].Name() {
			return all[i].Name() < all[j].Name()
		}
		return all[i].Context < all[j].Context
	})
	return all, errs
}

// ClientForSet resolves the client that owns an ApplicationSet.
func (f *Fleet) ClientForSet(s *ApplicationSet) (*Client, error) {
	if s.Context == "" {
		return nil, fmt.Errorf("application set %q has no recorded context", s.Name())
	}
	return f.Client(s.Context)
}

// SetURL is the web UI address of an ApplicationSet.
func (f *Fleet) SetURL(s *ApplicationSet) (string, error) {
	c, err := f.ClientForSet(s)
	if err != nil {
		return "", err
	}
	return c.Context().BaseURL() + "/applicationsets/" + s.Name(), nil
}
