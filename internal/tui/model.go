// Package tui implements argx's interactive Argo CD browser.
//
// The shape is a drill-down stack (k9s/a9s style) rather than a persistent
// multi-panel: an Argo CD session has many applications but a human works on
// one at a time, and the resource tree needs the full width once you are inside
// an app.
//
//	applications ─Enter→ application view ─Enter→ manifest / rollback
//	     │                  [ RESOURCES | HISTORY | DETAILS ]
//	     ├─d→ app diff         │      │         │
//	     └─o→ web UI           │      │         └→ edit spec (revision, auto-sync)
//	                           │      └→ past deployments, rollback
//	                           └→ resource tree, diff, logs
//
// Within the application view the three tabs are lenses on the same app, cycled
// with [ / ] or picked with 1 / 2 / 3 — the app in the header never changes, so
// the reader keeps their place.
//
// Multi-select (Space) is available on the application list and the resource
// tab; actions that can operate on a set — sync, refresh, open in browser —
// apply to the marks when any exist and to the cursor row otherwise.
package tui

import (
	"context"
	"net/url"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
	"github.com/keyolk/argx/internal/config"
)

type screen int

const (
	screenApps screen = iota
	// screenAppSets lists the ApplicationSets that generate the applications.
	// A broken generator is invisible from the application side — what it would
	// have produced simply does not exist — so it needs its own list.
	screenAppSets
	// screenApp is the single-application view; which tab it shows is held
	// separately in Model.tab, so switching tabs does not disturb the screen
	// stack that Esc unwinds.
	screenApp
	screenDiff
	screenManifest
	screenLogs
	screenEvents
	// screenWindows lists the sync windows governing the focused application's
	// project — when a sync is allowed to run, and when it is blocked.
	screenWindows
	// screenSchedule lists the syncs waiting for their window to open.
	screenSchedule
	// screenContexts lists the servers this session is connected to and the
	// credential each one is using.
	screenContexts
	screenHelp
)

// tab is a lens on the focused application.
type tab int

const (
	tabResources tab = iota
	tabHistory
	tabDetails
)

func (t tab) String() string {
	switch t {
	case tabHistory:
		return "HISTORY"
	case tabDetails:
		return "DETAILS"
	default:
		return "RESOURCES"
	}
}

// allTabs is the cycle order for [ and ].
var allTabs = []tab{tabResources, tabHistory, tabDetails}

// overlay is a modal layered over the current screen.
type overlay int

const (
	overlayNone overlay = iota
	overlayConfirm
	overlayError
	overlaySyncOpts
	// overlayRevPicker chooses a target revision from the repository's actual
	// branches and tags, so a revision is picked rather than typed from memory.
	overlayRevPicker
	// overlayContainer chooses which container of a multi-container pod to read
	// logs from or exec into.
	overlayContainer
)

const (
	minWidth  = 60
	minHeight = 14

	// logTailLines bounds the log fetch. Argo CD streams NDJSON, so an
	// unbounded tail on a chatty pod would stall the fetch for a long time
	// before the user sees anything.
	logTailLines = 500
)

// Model is the Bubble Tea model.
type Model struct {
	ctx   context.Context
	fleet *argocd.Fleet
	cfg   *config.Config

	st *styles
	gl glyphSet

	width    int
	height   int
	tooSmall bool

	screen  screen
	overlay overlay
	// prev is the screen to return to when an overlay or a leaf view closes.
	prev []screen

	// ---- application list ----
	apps      []argocd.Application
	appRows   []int // indices into apps, after filtering
	appCur    int
	appTop    int
	appFilter appFilterQuery
	// appMarks keys on context+name, never name alone: two servers can host an
	// application with the same name, and a mark that selected both would sync
	// a cluster the user never looked at.
	appMarks map[string]bool
	// fleetErrs records servers that failed the last list, so a partial result
	// is reported as partial rather than passing for the whole fleet.
	fleetErrs []argocd.FleetError
	// completions indexes what the loaded applications can be filtered by, so
	// the prompt offers label keys and values that something actually carries.
	completions *completionSource

	// ---- application set list ----
	appsets      []argocd.ApplicationSet
	appsetRows   []int
	appsetCur    int
	appsetTop    int
	appsetFilter string
	// appsetsLoaded records that a fetch has happened, so an empty list is
	// distinguishable from one that was never asked for.
	appsetsLoaded bool

	// ---- the focused application ----
	app *argocd.Application
	tab tab

	// ---- RESOURCES tab ----
	tree     []argocd.TreeRow
	treeRows []int
	treeCur  int
	treeTop  int
	treeFilt resourceFilter
	// treeMarks keys on resource UID, which is stable across refreshes of the
	// same live object; a name-based key would silently re-mark a recreated pod.
	treeMarks map[string]bool

	// ---- HISTORY tab ----
	histCur int
	histTop int

	// ---- DETAILS tab ----
	detailCur int
	// windows is the sync-window state for the focused application, fetched
	// lazily: most sessions never look at it, and it is one request per app.
	windows *argocd.AppSyncWindows
	// projectWindows is every window on the project, read from its spec —
	// including those that do not apply to this application, so a window that
	// *nearly* matched is still noticeable, and including closed ones, which
	// /projects/{name}/syncwindows omits entirely.
	projectWindows []argocd.SyncWindow
	windowCur      int
	windowTop      int
	// revPicker is the branch/tag list backing overlayRevPicker.
	revPicker revPickerState
	// picker is the container chooser backing overlayContainer.
	picker containerPicker

	// ---- pager views (diff, manifest, logs, events) ----
	pager      []string
	pagerTop   int
	pagerTitle string
	pagerFilt  string
	// pagerSides is what a diff was computed from, kept so an external diff
	// tool can be handed the two documents rather than argx's rendering. Nil
	// for anything that is not a diff.
	pagerSides *diffSides

	// sxs lays the diff out in two columns instead of as a unified patch. Off
	// by default: a unified diff is what people expect from the word "diff",
	// and two columns need a wide terminal to be worth it.
	sxs bool

	// ---- multi-select ----
	//
	// visualFrom is where a range select started, or -1. The rows between it
	// and the cursor are drawn as marked before they are marked, so the reader
	// sees what v will take.
	visualFrom int
	// markedOnly narrows both lists to what is marked, which is how a
	// selection built across several filters becomes inspectable.
	markedOnly bool

	// showNoise reveals the bookkeeping fields a manifest carries — see
	// noiseKeys. Off by default: they are 39% of a real pod manifest and bury
	// everything else.
	showNoise bool

	// ---- contexts ----
	//
	// Who argx is on each server. Loaded on entering the view rather than at
	// startup: it is two requests per server, and most sessions never ask.
	ctxRows   []contextRow
	ctxCur    int
	ctxTop    int
	ctxLoaded bool
	ctxDetail bool

	// ---- scheduled syncs ----
	//
	// These live only as long as the session: there is no daemon and no state
	// file, and the exit path says what will be dropped.
	schedules   []scheduled
	scheduleID  int
	scheduleCur int
	scheduleTop int

	// ---- filter input ----
	filtering bool
	// filterCur is the text cursor's position within the query, counted in
	// runes so a multi-byte character is never split. It is clamped on every
	// edit rather than trusted, because the query is also set from outside the
	// prompt (entering a screen, clearing a filter).
	filterCur int
	// completionHint is the candidate list shown when a Tab press could not
	// narrow further. Cleared by any edit, since it described the old word.
	completionHint []string

	// ---- async state ----
	loading  bool
	loadWhat string
	// Request stamps, one sequence per kind of fetch, so a result whose target
	// changed mid-flight is dropped instead of overwriting the current view.
	//
	// Separate sequences because several fetches are issued together: a single
	// counter shared by a tea.Batch has the second command invalidate the
	// first's response, and the tree would simply never arrive.
	reqID    uint64 // pager views: diff, manifest, logs, events
	treeID   uint64
	windowID uint64

	// ---- transient feedback ----
	toast       string
	toastAt     time.Time
	errMsg      string
	confirm     confirmState
	syncOpts    syncOptState
	autoRefresh bool
}

// confirmState describes a pending yes/no prompt.
type confirmState struct {
	title string
	body  []string
	// yes is the choice under the cursor. It starts false on every prompt
	// because every one of them guards something destructive — a sync, a
	// rollback, dropping scheduled work — so the key that commits must never be
	// the one already selected. h/l and ←/→ move it; y and n still answer
	// outright without moving anything.
	yes bool
	// action runs when the user confirms. It is a tea.Cmd so the work happens
	// off the UI thread like every other side effect.
	action func() tea.Cmd
}

// syncOptState is the sync modal's toggles.
type syncOptState struct {
	prune  bool
	dryRun bool
	// schedule waits for the sync window to open instead of syncing now. Argo
	// CD records a rejected sync as a failed operation on the application, so
	// syncing into a closed window leaves noise where someone will later look
	// for a real fault.
	schedule bool
	targets  []argocd.Application
	// cur is the toggle under the cursor. The letters still work — p, d and w
	// are faster once you know them — but a modal reachable only by keys you
	// have to already know is one people back out of, and this is the screen
	// where prune is turned on.
	cur int
}

// syncOptToggles are the modal's rows, in display order, each paired with the
// field it flips. Written once so the keys, the cursor and the rendering cannot
// drift apart.
func (m *Model) syncOptToggles() []*bool {
	return []*bool{&m.syncOpts.prune, &m.syncOpts.dryRun, &m.syncOpts.schedule}
}

// revPickerState is the revision picker's list and its own filter.
type revPickerState struct {
	// items are branches then tags, each already labelled with its kind so the
	// two namespaces cannot be confused when they share a name.
	items   []revItem
	rows    []int
	cur     int
	top     int
	filter  string
	loading bool
	err     string
}

// revItem is one candidate target revision.
type revItem struct {
	name string
	// kind is "branch" or "tag"; the same name can be both, and which one Argo
	// CD resolves is not something to leave implicit.
	kind string
}

// New builds the model. The context governs every API call the TUI makes.
func New(ctx context.Context, fleet *argocd.Fleet, cfg *config.Config) *Model {
	st := newStyles()
	st.initContexts(len(fleet.Names()))
	gl := newGlyphsFor(cfg.Icons)
	return &Model{
		ctx:        ctx,
		fleet:      fleet,
		cfg:        cfg,
		st:         st,
		gl:         gl,
		screen:     screenApps,
		tab:        tabResources,
		appMarks:   map[string]bool{},
		treeMarks:  map[string]bool{},
		visualFrom: -1,
		width:      80,
		height:     24,
	}
}

// Init loads the application list.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadAppsCmd(), tea.WindowSize())
}

// ---- selection helpers ----

// markedApps returns the applications the next action should operate on: the
// marked set when non-empty, else the row under the cursor.
//
// Returning the cursor row when nothing is marked is what makes every action
// work identically whether or not the user has bothered to mark anything.
func (m *Model) markedApps() []argocd.Application {
	if len(m.appMarks) > 0 {
		var out []argocd.Application
		// Iterate the display order, not the map, so the confirmation modal
		// lists targets in the order the user sees them.
		for _, i := range m.appRows {
			if m.appMarks[m.apps[i].Key()] {
				out = append(out, m.apps[i])
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if a := m.currentApp(); a != nil {
		return []argocd.Application{*a}
	}
	return nil
}

// currentApp is the application under the cursor in the list.
//
// The row index is validated against apps as well as appRows: applyAppFilter
// reads the cursor before rebuilding the rows, so during a reload that shrinks
// the list — every server failing, or a filter that now matches nothing —
// appRows still holds indices into the previous, longer slice.
func (m *Model) currentApp() *argocd.Application {
	if m.appCur < 0 || m.appCur >= len(m.appRows) {
		return nil
	}
	i := m.appRows[m.appCur]
	if i < 0 || i >= len(m.apps) {
		return nil
	}
	return &m.apps[i]
}

// markedNodes returns the tree resources the next action should operate on,
// with the same marks-else-cursor rule as markedApps.
func (m *Model) markedNodes() []argocd.Node {
	if len(m.treeMarks) > 0 {
		var out []argocd.Node
		for _, i := range m.treeRows {
			if m.treeMarks[m.tree[i].Node.UID] {
				out = append(out, m.tree[i].Node)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if n := m.currentNode(); n != nil {
		return []argocd.Node{*n}
	}
	return nil
}

// currentNode is the resource under the cursor in the tree, with the same
// stale-index guard as currentApp.
func (m *Model) currentNode() *argocd.Node {
	if m.treeCur < 0 || m.treeCur >= len(m.treeRows) {
		return nil
	}
	i := m.treeRows[m.treeCur]
	if i < 0 || i >= len(m.tree) {
		return nil
	}
	return &m.tree[i].Node
}

// ctxStyle is the color assigned to a context, by its position in the fleet.
//
// An unknown name falls back to dim rather than to a palette entry: a context
// that is not in the fleet must not be tinted as though it were one.
func (m *Model) ctxStyle(name string) lipgloss.Style {
	for i, n := range m.fleet.Names() {
		if n == name {
			if i < len(m.st.ctx) {
				return m.st.ctx[i]
			}
			break
		}
	}
	return m.st.dim
}

// client resolves the server an application came from.
//
// Every action goes through this rather than a stored "current client": with
// several servers in one list, an ambient current server is precisely how an
// action lands on the wrong cluster.
func (m *Model) client(app *argocd.Application) (*argocd.Client, error) {
	return m.fleet.ClientFor(app)
}

// appURL is the web UI address of an application on its own server.
func (m *Model) appURL(app *argocd.Application) string {
	u, err := m.fleet.URL(app)
	if err != nil {
		return ""
	}
	return u
}

// projectWindowsURL is the web UI address of an application's sync windows.
//
// The `?tab=windows` fragment is what Argo CD's own UI links to from an
// application's SyncWindow badge (ui/src/app/applications/components/utils.tsx),
// so this lands on the windows rather than on the project overview — which is
// the difference between arriving at the thing you were reading and arriving
// one click away from it.
func (m *Model) projectWindowsURL(app *argocd.Application) string {
	c, err := m.fleet.ClientFor(app)
	if err != nil {
		return ""
	}
	return c.Context().BaseURL() + "/settings/projects/" +
		url.PathEscape(app.Spec.Project) + "?tab=windows"
}

// multiServer reports whether this session spans more than one Argo CD, which
// is what decides whether the list spends width on a context column.
func (m *Model) multiServer() bool { return !m.fleet.Single() }

// bodyHeight is the number of rows available to the list/pager body, after the
// header, the status line, and the footer — one line each.
func (m *Model) bodyHeight() int {
	h := m.height - 3
	if h < 1 {
		return 1
	}
	return h
}

// push records the current screen so Esc can return to it.
func (m *Model) push(next screen) {
	m.prev = append(m.prev, m.screen)
	m.screen = next
}

// pop returns to the previous screen, or stays put at the root.
func (m *Model) pop() {
	if len(m.prev) == 0 {
		return
	}
	m.screen = m.prev[len(m.prev)-1]
	m.prev = m.prev[:len(m.prev)-1]
}

// setToast shows a transient message in the status line.
func (m *Model) setToast(s string) {
	m.toast = s
	m.toastAt = time.Now()
}
