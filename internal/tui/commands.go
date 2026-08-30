package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

// Every network call happens here, in a tea.Cmd, and reports back as a message.
// Update and View never touch I/O.

type appsMsg struct {
	apps []argocd.Application
	// errs are per-server failures. A fleet list does not fail as a whole when
	// one server is down: the others still have answers, and hiding them
	// because a third is unreachable helps nobody.
	errs []argocd.FleetError
	err  error
}

type treeMsg struct {
	id   uint64
	app  *argocd.Application
	rows []argocd.TreeRow
	err  error
}

type pagerMsg struct {
	id    uint64
	title string
	lines []string
	err   error
	// sides carries the two documents a diff was computed from, so an external
	// diff tool can be handed the originals rather than argx's rendering of
	// them. Nil for anything that is not a diff.
	sides *diffSides
}

type actionMsg struct {
	text string
	err  error
}

type toastMsg struct{ text string }

type errMsg struct{ err error }

// tickMsg drives the optional auto-refresh. It is a data tick, not a redraw
// tick: Update only issues work when auto-refresh is on, so an idle argx
// renders zero frames.
type tickMsg time.Time

func (m *Model) loadAppsCmd() tea.Cmd {
	m.loading, m.loadWhat = true, "applications"
	fleet := m.fleet
	ctx := m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// The fleet queries every server concurrently and returns whatever
		// answered, already sorted and stamped with its origin.
		apps, errs := fleet.ListApplications(c, nil)
		return appsMsg{apps: apps, errs: errs}
	}
}

func (m *Model) loadTreeCmd(app argocd.Application) tea.Cmd {
	m.treeID++
	id := m.treeID
	m.loading, m.loadWhat = true, "resource tree"
	client, err := m.client(&app)
	if err != nil {
		return func() tea.Msg { return treeMsg{id: id, err: err} }
	}
	ctx := m.ctx
	ctxName := app.Context
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// Re-fetch the app alongside the tree: the list may be minutes stale,
		// and the detail header must not disagree with the tree it sits above.
		fresh, err := client.GetApplication(c, app.Name(), app.AppNamespace())
		if err != nil {
			return treeMsg{id: id, err: err}
		}
		fresh.Context = ctxName
		t, err := client.ResourceTree(c, app.Name(), app.AppNamespace())
		if err != nil {
			return treeMsg{id: id, err: err}
		}
		return treeMsg{id: id, app: fresh, rows: t.Flatten(app.AppNamespace(), ctxName)}
	}
}

// loadAppDiffCmd renders the whole application's desired-vs-live difference.
func (m *Model) loadAppDiffCmd(app argocd.Application) tea.Cmd {
	m.reqID++
	id := m.reqID
	m.loading, m.loadWhat = true, "diff"
	client, err := m.client(&app)
	if err != nil {
		return func() tea.Msg { return pagerMsg{id: id, err: err} }
	}
	ctx := m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		items, err := client.ManagedResources(c, app.Name(), app.AppNamespace())
		if err != nil {
			return pagerMsg{id: id, err: err}
		}
		return pagerMsg{
			id:    id,
			title: "diff · " + app.Name(),
			lines: renderDiff(items, nil),
			sides: collectSides(items, nil, app.Name()),
		}
	}
}

// loadResourceDiffCmd narrows the diff to specific resources, which is what the
// tree's `d` does when rows are marked.
func (m *Model) loadResourceDiffCmd(app argocd.Application, nodes []argocd.Node) tea.Cmd {
	m.reqID++
	id := m.reqID
	m.loading, m.loadWhat = true, "diff"
	client, err := m.client(&app)
	if err != nil {
		return func() tea.Msg { return pagerMsg{id: id, err: err} }
	}
	ctx := m.ctx

	want := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		want[diffKey(n.Group, n.Kind, n.Namespace, n.Name)] = true
	}
	title := fmt.Sprintf("diff · %s · %d resource(s)", app.Name(), len(nodes))

	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		items, err := client.ManagedResources(c, app.Name(), app.AppNamespace())
		if err != nil {
			return pagerMsg{id: id, err: err}
		}
		return pagerMsg{
			id: id, title: title,
			lines: renderDiff(items, want),
			sides: collectSides(items, want, app.Name()),
		}
	}
}

func (m *Model) loadManifestCmd(app argocd.Application, n argocd.Node) tea.Cmd {
	m.reqID++
	id := m.reqID
	m.loading, m.loadWhat = true, "manifest"
	client, err := m.client(&app)
	if err != nil {
		return func() tea.Msg { return pagerMsg{id: id, err: err} }
	}
	ctx := m.ctx
	ref := n.ResourceRef
	ref.AppNamespace = app.AppNamespace()
	title := fmt.Sprintf("manifest · %s/%s", n.GroupKind(), n.Name)
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		s, err := client.ResourceManifest(c, app.Name(), ref)
		if err != nil {
			return pagerMsg{id: id, err: err}
		}
		return pagerMsg{id: id, title: title, lines: strings.Split(prettyJSON(s), "\n")}
	}
}

func (m *Model) loadLogsCmd(app argocd.Application, n argocd.Node, container string) tea.Cmd {
	m.reqID++
	id := m.reqID
	m.loading, m.loadWhat = true, "logs"
	client, err := m.client(&app)
	if err != nil {
		return func() tea.Msg { return pagerMsg{id: id, err: err} }
	}
	ctx := m.ctx
	ref := n.ResourceRef
	ref.AppNamespace = app.AppNamespace()
	title := fmt.Sprintf("logs · %s (last %d lines)", n.Name, logTailLines)
	if container != "" {
		title = fmt.Sprintf("logs · %s · %s (last %d lines)", n.Name, container, logTailLines)
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		// An empty container name lets the server pick the pod's default, which
		// is right for a single-container pod; the caller resolves the choice
		// for the rest.
		out, err := client.PodLogs(c, app.Name(), ref, container, logTailLines)
		if err != nil && out == "" {
			return pagerMsg{id: id, err: err}
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if err != nil {
			lines = append(lines, "", "-- log stream ended: "+err.Error())
		}
		return pagerMsg{id: id, title: title, lines: lines}
	}
}

func (m *Model) loadEventsCmd(app argocd.Application) tea.Cmd {
	m.reqID++
	id := m.reqID
	m.loading, m.loadWhat = true, "events"
	client, err := m.client(&app)
	if err != nil {
		return func() tea.Msg { return pagerMsg{id: id, err: err} }
	}
	ctx := m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		evs, err := client.Events(c, app.Name(), app.AppNamespace())
		if err != nil {
			return pagerMsg{id: id, err: err}
		}
		sort.Slice(evs, func(i, j int) bool {
			return evs[i].LastTimestamp.After(evs[j].LastTimestamp)
		})
		lines := make([]string, 0, len(evs)+1)
		if len(evs) == 0 {
			lines = append(lines, "(no events)")
		}
		for _, e := range evs {
			lines = append(lines, fmt.Sprintf("%s  %-8s %-24s %s/%s: %s",
				e.LastTimestamp.Local().Format("01-02 15:04:05"),
				e.Type, e.Reason,
				e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Message))
		}
		return pagerMsg{id: id, title: "events · " + app.Name(), lines: lines}
	}
}

// refreshCmd re-reconciles a set of applications. Refreshing is safe — it only
// re-reads the source and recomputes status — so it needs no confirmation.
func (m *Model) refreshCmd(apps []argocd.Application, hard bool) tea.Cmd {
	fleet := m.fleet
	ctx := m.ctx
	kind := "refreshed"
	if hard {
		kind = "hard-refreshed"
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		var errs []error
		ok := 0
		for _, a := range apps {
			// Resolved per application, not once for the batch: a marked set
			// can span servers, and one client for all of them would refresh
			// the wrong ones.
			client, err := fleet.ClientFor(&a)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if _, err := client.Refresh(c, a.Name(), a.AppNamespace(), hard); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", qualify(&a, fleet), err))
				continue
			}
			ok++
		}
		if len(errs) > 0 {
			return actionMsg{
				text: fmt.Sprintf("%s %d of %d", kind, ok, len(apps)),
				err:  errors.Join(errs...),
			}
		}
		return actionMsg{text: fmt.Sprintf("%s %d application(s)", kind, ok)}
	}
}

// qualify names an application, prefixing its server when the session spans
// more than one — an error saying only "web failed" is useless when three
// servers each have a `web`.
func qualify(a *argocd.Application, fleet *argocd.Fleet) string {
	if fleet.Single() {
		return a.Name()
	}
	return a.Context + "/" + a.Name()
}

// syncCmd syncs a set of applications.
//
// Sequential rather than a tea.Batch of per-app commands: those finish in
// arbitrary order and each would overwrite the status line, so a failure would
// be silently replaced by a later success and the user told everything worked.
func (m *Model) syncCmd(apps []argocd.Application, opt argocd.SyncOptions) tea.Cmd {
	fleet := m.fleet
	ctx := m.ctx
	verb := "synced"
	if opt.DryRun {
		verb = "dry-run synced"
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 300*time.Second)
		defer cancel()
		var errs []error
		ok := 0
		for _, a := range apps {
			client, err := fleet.ClientFor(&a)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			o := opt
			o.AppNamespace = a.AppNamespace()
			if _, err := client.Sync(c, a.Name(), o); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", qualify(&a, fleet), err))
				continue
			}
			ok++
		}
		if len(errs) > 0 {
			return actionMsg{
				text: fmt.Sprintf("%s %d of %d", verb, ok, len(apps)),
				err:  errors.Join(errs...),
			}
		}
		return actionMsg{text: fmt.Sprintf("%s %d application(s)", verb, ok)}
	}
}

// openBrowserCmd opens application URLs in the configured browser.
//
// Sequential for the same reason as syncCmd: one result for the batch, so a
// partial failure is reported as a partial failure.
func (m *Model) openBrowserCmd(urls []string) tea.Cmd {
	opener := m.cfg.BrowserCommand()
	ctx := m.ctx
	return func() tea.Msg {
		var errs []error
		opened := 0
		for _, u := range urls {
			if u == "" {
				continue
			}
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := exec.CommandContext(c, opener, u).Run()
			cancel()
			if err != nil {
				errs = append(errs, fmt.Errorf("open %s: %w", u, err))
				continue
			}
			opened++
		}
		switch {
		case len(errs) > 0:
			return actionMsg{
				text: fmt.Sprintf("opened %d of %d", opened, len(urls)),
				err:  errors.Join(errs...),
			}
		case opened == 1:
			return toastMsg{text: "opened in browser"}
		default:
			return toastMsg{text: fmt.Sprintf("opened %d in browser", opened)}
		}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(15*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// fleetErrorText renders per-server failures as one error body.
func fleetErrorText(errs []argocd.FleetError) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d server(s) did not answer:\n", len(errs)))
	for _, e := range errs {
		b.WriteString("\n  " + e.Context + "\n    " + e.Err.Error() + "\n")
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

// appSetsMsg carries the fleet's ApplicationSets.
type appSetsMsg struct {
	sets []argocd.ApplicationSet
	errs []argocd.FleetError
}

// specMsgPager reuses the pager for a rendered spec.
func (m *Model) loadAppSetsCmd() tea.Cmd {
	m.loading, m.loadWhat = true, "application sets"
	fleet := m.fleet
	ctx := m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		sets, errs := fleet.ListApplicationSets(c, nil)
		return appSetsMsg{sets: sets, errs: errs}
	}
}

// loadSetSpecCmd renders an ApplicationSet's spec into the pager.
//
// The stored copy is re-fetched rather than formatted from the list: the list
// response omits nothing today, but a spec is what someone reads to reproduce a
// generator, and reading a possibly-stale copy for that is the wrong tradeoff.
func (m *Model) loadSetSpecCmd(set argocd.ApplicationSet) tea.Cmd {
	m.reqID++
	id := m.reqID
	m.loading, m.loadWhat = true, "spec"

	client, err := m.fleet.ClientForSet(&set)
	if err != nil {
		return func() tea.Msg { return pagerMsg{id: id, err: err} }
	}
	ctx := m.ctx
	title := "spec · " + set.Name()

	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		fresh, err := client.GetApplicationSet(c, set.Name(), set.Namespace())
		if err != nil {
			return pagerMsg{id: id, err: err}
		}
		b, err := json.MarshalIndent(fresh, "", "  ")
		if err != nil {
			return pagerMsg{id: id, err: err}
		}
		return pagerMsg{id: id, title: title, lines: strings.Split(string(b), "\n")}
	}
}
