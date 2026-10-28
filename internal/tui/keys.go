package tui

// keys.go is argx's one binding table. Dispatch, the footer, the ? help and
// the : palette are all derived from it, so none of them can drift from what
// a key actually does.
//
// Before this table each screen had its own `switch msg.String()` and the
// footer and help were hand-written beside them. That is how F ("follow") came
// to be named in the footer and the help while the mark handler — which ran
// first, because both lists share it — swallowed the key: one letter, two
// owners, and the dispatch order picked one. The mark keys are now declared in
// the same context as each list's own keys, so a second owner is a Duplicate
// conflict that TestKeymapHasNoConflicts fails on.

import (
	"fmt"

	"github.com/keyolk/tuikit/keys"
)

// Contexts. A stack of them is active at a time, innermost first; see keyStack.
//
// Context names carry no dots: keys.yaml addresses a binding as
// "<context>.<action>", and a dotted context would make that ambiguous.
const (
	ctxGlobal         keys.Ctx = "global"
	ctxApps           keys.Ctx = "apps"
	ctxAppSets        keys.Ctx = "appsets"
	ctxApp            keys.Ctx = "app" // keys every tab of the application view shares
	ctxTree           keys.Ctx = "tree"
	ctxHistory        keys.Ctx = "history"
	ctxDetails        keys.Ctx = "details"
	ctxPager          keys.Ctx = "pager"
	ctxDiff           keys.Ctx = "diff" // inner to pager, on the diff view only
	ctxWindows        keys.Ctx = "windows"
	ctxSchedule       keys.Ctx = "schedule"
	ctxContexts       keys.Ctx = "contexts"
	ctxCtxDetail      keys.Ctx = "ctxdetail"
	ctxVisual         keys.Ctx = "visual"
	ctxFilter         keys.Ctx = "filter"
	ctxFilterMarks    keys.Ctx = "filtermarks"
	ctxFilterComplete keys.Ctx = "filtercomplete"
	ctxFilterTabs     keys.Ctx = "filtertabs"
	ctxConfirm        keys.Ctx = "confirm"
	ctxSyncOpts       keys.Ctx = "syncopts"
	ctxRevPicker      keys.Ctx = "revpicker"
	ctxContainer      keys.Ctx = "container"
	ctxCommands       keys.Ctx = "palette"
)

// Action names. Constants rather than literals so a handler and the table
// cannot disagree by a typo: an unhandled action would otherwise be a key that
// silently does nothing.
const (
	aQuit      = "quit"
	aHelp      = "help"
	aBack      = "back"
	aFilter    = "filter"
	aPalette   = "palette"
	aSchedules = "schedules"
	aContexts  = "contexts"

	aNavDown   = "nav.down"
	aNavUp     = "nav.up"
	aNavPgDown = "nav.pgdown"
	aNavPgUp   = "nav.pgup"
	aNavTop    = "nav.top"
	aNavBottom = "nav.bottom"

	aMarkToggle     = "mark.toggle"
	aMarkAll        = "mark.all"
	aMarkClear      = "mark.clear"
	aMarkInvert     = "mark.invert"
	aMarkAdd        = "mark.add"
	aMarkRange      = "mark.range"
	aMarkOnly       = "mark.only"
	aMarkExtendDown = "mark.extenddown"
	aMarkExtendUp   = "mark.extendup"
	aVisualCancel   = "visual.cancel"

	aAppSetsToggle = "appsets.toggle"
	aFleetErrors   = "fleet.errors"

	aAppsOpen        = "apps.open"
	aAppsSync        = "apps.sync"
	aAppsDiff        = "apps.diff"
	aAppsEvents      = "apps.events"
	aAppsBrowser     = "apps.browser"
	aAppsRefresh     = "apps.refresh"
	aAppsHardRefresh = "apps.hardrefresh"
	aAppsReload      = "apps.reload"
	aAppsFollow      = "apps.follow"

	aSetsOpen    = "appsets.open"
	aSetsSpec    = "appsets.spec"
	aSetsBrowser = "appsets.browser"
	aSetsReload  = "appsets.reload"

	aTabNext      = "tab.next"
	aTabPrev      = "tab.prev"
	aTabResources = "tab.resources"
	aTabHistory   = "tab.history"
	aTabDetails   = "tab.details"
	aAppBrowser   = "app.browser"
	aAppReload    = "app.reload"
	aAppWindows   = "app.windows"
	aAppParent    = "app.parent"

	aTreeGraph    = "tree.graph"
	aTreeManifest = "tree.manifest"
	aTreeDiff     = "tree.diff"
	aTreeAppDiff  = "tree.appdiff"
	aTreeLogs     = "tree.logs"
	aTreeShell    = "tree.shell"
	aTreeSync     = "tree.sync"

	aHistRollback = "history.rollback"
	aHistDiff     = "history.diff"

	aDetailsEdit   = "details.edit"
	aDetailsEvents = "details.events"
	aDetailsSync   = "details.sync"

	aPagerNext  = "pager.next"
	aPagerPrev  = "pager.prev"
	aPagerNoise = "pager.noise"
	aPagerLeave = "pager.leave"

	aDiffSxS = "diff.sidebyside"
	// The kit's split actions (split.ActionGrow…), spelled out so the
	// dispatch test can find them; TestSplitActionNamesAreTheKits keeps them equal.
	aSplitGrow   = "split.grow"
	aSplitShrink = "split.shrink"
	aSplitReset  = "split.reset"
	aDiffTool    = "diff.tool"
	aDiffPairing = "diff.pairing"

	aWinBrowser    = "windows.browser"
	aWinAppBrowser = "windows.appbrowser"
	aWinReload     = "windows.reload"
	aWinLeave      = "windows.leave"

	aSchedCancel  = "schedule.cancel"
	aSchedClear   = "schedule.clear"
	aSchedBrowser = "schedule.browser"
	aSchedLeave   = "schedule.leave"

	aCtxOpen    = "contexts.open"
	aCtxBrowser = "contexts.browser"
	aCtxReload  = "contexts.reload"
	aCtxLogin   = "contexts.login"
	aCtxLeave   = "contexts.leave"
	aCtxClose   = "contexts.close"

	aFilterDown       = "filter.down"
	aFilterUp         = "filter.up"
	aFilterPgDown     = "filter.pgdown"
	aFilterPgUp       = "filter.pgup"
	aFilterKeep       = "filter.keep"
	aFilterCancel     = "filter.cancel"
	aFilterLeft       = "filter.left"
	aFilterRight      = "filter.right"
	aFilterHome       = "filter.home"
	aFilterEnd        = "filter.end"
	aFilterWordLeft   = "filter.wordleft"
	aFilterWordRight  = "filter.wordright"
	aFilterBackspace  = "filter.backspace"
	aFilterDelete     = "filter.delete"
	aFilterClear      = "filter.clear"
	aFilterKillEnd    = "filter.killend"
	aFilterKillWord   = "filter.killword"
	aFilterMark       = "filter.mark"
	aFilterUnmark     = "filter.unmark"
	aFilterExtendDown = "filter.extenddown"
	aFilterExtendUp   = "filter.extendup"
	aFilterComplete   = "filter.complete"

	aConfirmToggle = "confirm.toggle"
	aConfirmEnter  = "confirm.enter"
	aConfirmYes    = "confirm.yes"
	aConfirmNo     = "confirm.no"

	aOptDown     = "syncopts.down"
	aOptUp       = "syncopts.up"
	aOptTop      = "syncopts.top"
	aOptBottom   = "syncopts.bottom"
	aOptToggle   = "syncopts.toggle"
	aOptPrune    = "syncopts.prune"
	aOptDryRun   = "syncopts.dryrun"
	aOptWait     = "syncopts.wait"
	aOptCancel   = "syncopts.cancel"
	aOptContinue = "syncopts.continue"

	aRevCancel    = "revpicker.cancel"
	aRevDown      = "revpicker.down"
	aRevUp        = "revpicker.up"
	aRevPgDown    = "revpicker.pgdown"
	aRevPgUp      = "revpicker.pgup"
	aRevBackspace = "revpicker.backspace"
	aRevChoose    = "revpicker.choose"

	aPickCancel = "container.cancel"
	aPickDown   = "container.down"
	aPickUp     = "container.up"
	aPickTop    = "container.top"
	aPickBottom = "container.bottom"
	aPickChoose = "container.choose"

	aPalClose     = "palette.close"
	aPalRun       = "palette.run"
	aPalDown      = "palette.down"
	aPalUp        = "palette.up"
	aPalBackspace = "palette.backspace"
	aPalClear     = "palette.clear"
)

// navBindings are the list motions, declared once per context that has them.
//
// Declared per context rather than once in an outer "nav" context so that a
// screen key reusing j or g is a Duplicate the conflict test reports, instead
// of an inner binding silently winning. footer 0 keeps them out of the footer;
// label is the footer's word for them ("move" on a list, "scroll" on a page).
func navBindings(ctx keys.Ctx, footer int, label string) []keys.Binding {
	g := "navigation"
	return []keys.Binding{
		{Action: aNavDown, Keys: []string{"j", "down"}, Ctx: ctx, Group: g, Help: "down", Footer: footer, FooterGroup: label},
		{Action: aNavUp, Keys: []string{"k", "up"}, Ctx: ctx, Group: g, Help: "up", Footer: footer, FooterGroup: label},
		{Action: aNavPgDown, Keys: []string{"ctrl+d", "pgdown"}, Ctx: ctx, Group: g, Help: "half a page down"},
		{Action: aNavPgUp, Keys: []string{"ctrl+u", "pgup"}, Ctx: ctx, Group: g, Help: "half a page up"},
		{Action: aNavTop, Keys: []string{"g", "home"}, Ctx: ctx, Group: g, Help: "top"},
		{Action: aNavBottom, Keys: []string{"G", "end"}, Ctx: ctx, Group: g, Help: "bottom"},
	}
}

// markBindings are the multi-select vocabulary both marking lists share.
//
// They lead the footer on those lists because every destructive action there
// takes its targets from the selection; "space mark" alone taught marking one
// row and nothing else.
func markBindings(ctx keys.Ctx) []keys.Binding {
	g := "multi-select"
	return []keys.Binding{
		{Action: aMarkToggle, Keys: []string{" "}, Ctx: ctx, Group: g, Help: "toggle this row and advance", Footer: 2, FooterGroup: "mark"},
		{Action: aMarkAll, Keys: []string{"a"}, Ctx: ctx, Group: g, Help: "mark every visible row (never unmarks)", Footer: 2, FooterGroup: "mark"},
		{Action: aMarkClear, Keys: []string{"A"}, LatinOnly: true, Ctx: ctx, Group: g, Help: "clear the visible marks; again for those the filter hides", Footer: 2, FooterGroup: "mark"},
		{Action: aMarkInvert, Keys: []string{"i"}, Ctx: ctx, Group: g, Help: "invert the visible rows"},
		{Action: aMarkAdd, Keys: []string{"+"}, Ctx: ctx, Group: g, Help: "add the visible rows, keeping what is marked"},
		{Action: aMarkRange, Keys: []string{"v"}, Ctx: ctx, Group: g, Help: "range select — move, then v again", Short: "range",
			Labels: map[string]string{"active": "take the range"}, Footer: 4},
		{Action: aMarkOnly, Keys: []string{"m"}, Ctx: ctx, Group: g, Help: "show only what is marked"},
		{Action: aMarkExtendDown, Keys: []string{"J", "shift+down"}, Ctx: ctx, Group: g, Help: "extend the selection down", Footer: 3, FooterGroup: "extend"},
		{Action: aMarkExtendUp, Keys: []string{"K", "shift+up"}, Ctx: ctx, Group: g, Help: "extend the selection up", Footer: 3, FooterGroup: "extend"},
	}
}

// tabBindings switch the application view's tabs. The filter prompt carries
// them too (see ctxFilterTabs), out of the footer there.
func tabBindings(ctx keys.Ctx, footer int) []keys.Binding {
	g := "application view"
	return []keys.Binding{
		{Action: aTabPrev, Keys: []string{"[", "shift+tab"}, Ctx: ctx, Group: g, Help: "previous tab", Footer: footer, FooterGroup: "tabs"},
		{Action: aTabNext, Keys: []string{"]", "tab"}, Ctx: ctx, Group: g, Help: "next tab", Footer: footer, FooterGroup: "tabs"},
	}
}

// syncLabels names the alternative on the sync key when a window is blocking:
// "s sync" on an application argx already knows cannot sync is an invitation
// to press it and have Argo CD record a failed operation.
var syncLabels = map[string]string{"blocked": "sync (w waits)"}

// Uppercase-only actions (A, C, F, S, D, N, M, H, L) are LatinOnly: a Korean
// input source cannot type them, and each stays reachable from the : palette,
// which is typed into. TestLatinOnlyActionsAreInThePalette holds that line.
func bindings() []keys.Binding {
	var bs []keys.Binding
	add := func(b ...keys.Binding) { bs = append(bs, b...) }

	// ---- global ----
	//
	// quit, help and back are pinned: a footer that drops "q quit" to make room
	// for "d diff" has cut the wrong thing, help is the only place the keys that
	// did not fit are written down, and esc is the one exit that keeps the
	// reader's place.
	add(
		keys.Binding{Action: aQuit, Keys: []string{"q", "ctrl+c"}, Ctx: ctxGlobal, Group: "general",
			Help: "quit, from anywhere (q asks first if syncs are scheduled)", Short: "quit", Pinned: true, Footer: 30},
		keys.Binding{Action: aHelp, Keys: []string{"?"}, Ctx: ctxGlobal, Group: "general",
			Help: "this help", Short: "help", Pinned: true, Footer: 29},
		keys.Binding{Action: aBack, Keys: []string{"esc"}, Ctx: ctxGlobal, Group: "general",
			Help: "back one screen — a standing filter or range is a level of its own", Short: "back",
			Labels: map[string]string{"detail": "back to the list"}, Pinned: true, Footer: 28},
		keys.Binding{Action: aFilter, Keys: []string{"/"}, Ctx: ctxGlobal, Group: "general",
			Help: "filter (esc clears, enter keeps)", Short: "filter",
			Labels: map[string]string{"search": "search"}, Footer: 12},
		keys.Binding{Action: aPalette, Keys: []string{":"}, Ctx: ctxGlobal, Group: "general",
			Help: "command palette — every action here, by name", Short: "commands", Footer: 16},
		keys.Binding{Action: aSchedules, Keys: []string{"W"}, Ctx: ctxGlobal, Group: "general",
			Help: "the scheduled syncs", Short: "scheduled", Footer: 1},
		keys.Binding{Action: aContexts, Keys: []string{"C"}, LatinOnly: true, Ctx: ctxGlobal, Group: "general",
			Help: "the servers this session is on, and as whom", Short: "contexts", Footer: 11},
	)

	// ---- application list ----
	add(markBindings(ctxApps)...)
	add(navBindings(ctxApps, 0, "move")...)
	g := "applications"
	add(
		keys.Binding{Action: aAppsOpen, Keys: []string{"enter", "l", "right"}, Ctx: ctxApps, Group: g, Help: "open the application", Short: "open", Footer: 5},
		keys.Binding{Action: aAppsSync, Keys: []string{"s"}, Ctx: ctxApps, Group: g, Help: "sync (options, then confirm)", Short: "sync", Footer: 6},
		keys.Binding{Action: aAppsDiff, Keys: []string{"d"}, Ctx: ctxApps, Group: g, Help: "diff (desired vs live)", Short: "diff", Footer: 7},
		keys.Binding{Action: aAppsEvents, Keys: []string{"e"}, Ctx: ctxApps, Group: g, Help: "events"},
		keys.Binding{Action: aAppsBrowser, Keys: []string{"o"}, Ctx: ctxApps, Group: g, Help: "open in browser", Short: "browser", Footer: 8},
		keys.Binding{Action: aAppsRefresh, Keys: []string{"r"}, Ctx: ctxApps, Group: g, Help: "refresh"},
		keys.Binding{Action: aAppsHardRefresh, Keys: []string{"R"}, Ctx: ctxApps, Group: g, Help: "hard refresh"},
		keys.Binding{Action: aAppsReload, Keys: []string{"ctrl+r"}, Ctx: ctxApps, Group: g, Help: "reload the list"},
		keys.Binding{Action: aAppsFollow, Keys: []string{"F"}, LatinOnly: true, Ctx: ctxApps, Group: g, Help: "follow — 15s auto-refresh",
			Labels: map[string]string{"on": "stop following"}},
		keys.Binding{Action: aFleetErrors, Keys: []string{"E"}, Ctx: ctxApps, Group: g, Help: "why a server is unreachable"},
		keys.Binding{Action: aAppSetsToggle, Keys: []string{"S"}, LatinOnly: true, Ctx: ctxApps, Group: g, Help: "the application sets that generate them", Short: "appsets", Footer: 10},
	)

	// ---- application set list ----
	add(navBindings(ctxAppSets, 0, "move")...)
	g = "application sets"
	add(
		keys.Binding{Action: aSetsOpen, Keys: []string{"enter", "l", "right"}, Ctx: ctxAppSets, Group: g, Help: "the applications this set generated", Short: "apps", Footer: 1},
		keys.Binding{Action: aSetsSpec, Keys: []string{"y"}, Ctx: ctxAppSets, Group: g, Help: "its full spec", Short: "spec", Footer: 2},
		keys.Binding{Action: aSetsBrowser, Keys: []string{"o"}, Ctx: ctxAppSets, Group: g, Help: "open in browser", Short: "browser", Footer: 3},
		keys.Binding{Action: aSetsReload, Keys: []string{"r", "ctrl+r"}, Ctx: ctxAppSets, Group: g, Help: "reload"},
		keys.Binding{Action: aFleetErrors, Keys: []string{"E"}, Ctx: ctxAppSets, Group: g, Help: "why a server is unreachable"},
		keys.Binding{Action: aAppSetsToggle, Keys: []string{"S"}, LatinOnly: true, Ctx: ctxAppSets, Group: g, Help: "back to the applications", Short: "applications", Footer: 4},
	)

	// ---- application view, every tab ----
	add(tabBindings(ctxApp, 13)...)
	g = "application view"
	add(
		keys.Binding{Action: aTabResources, Keys: []string{"1"}, Ctx: ctxApp, Group: g, Help: "RESOURCES tab"},
		keys.Binding{Action: aTabHistory, Keys: []string{"2"}, Ctx: ctxApp, Group: g, Help: "HISTORY tab"},
		keys.Binding{Action: aTabDetails, Keys: []string{"3"}, Ctx: ctxApp, Group: g, Help: "DETAILS tab"},
		keys.Binding{Action: aAppWindows, Keys: []string{"w"}, Ctx: ctxApp, Group: g, Help: "sync windows", Short: "windows", Footer: 14},
		keys.Binding{Action: aAppBrowser, Keys: []string{"o"}, Ctx: ctxApp, Group: g, Help: "open the application in browser", Short: "browser", Footer: 15},
		keys.Binding{Action: aAppReload, Keys: []string{"r", "ctrl+r"}, Ctx: ctxApp, Group: g, Help: "reload"},
		// h/← never leaves the application view — only esc does — so a stray
		// left-arrow while browsing cannot bounce the reader out to the list.
		keys.Binding{Action: aAppParent, Keys: []string{"h", "left"}, Ctx: ctxApp, Group: g, Help: "graph view: move to the parent", Short: "parent", Footer: 1},
	)

	// ---- RESOURCES tab ----
	add(markBindings(ctxTree)...)
	add(navBindings(ctxTree, 0, "move")...)
	g = "RESOURCES tab"
	add(
		// `l` is logs here, so → carries the "drill in" half of the pair alone.
		keys.Binding{Action: aTreeManifest, Keys: []string{"enter", "right"}, Ctx: ctxTree, Group: g, Help: "live manifest", Short: "manifest", Footer: 5},
		keys.Binding{Action: aTreeDiff, Keys: []string{"d"}, Ctx: ctxTree, Group: g, Help: "diff of the marked resources", Short: "diff", Footer: 7},
		keys.Binding{Action: aTreeAppDiff, Keys: []string{"D"}, LatinOnly: true, Ctx: ctxTree, Group: g, Help: "diff of the whole application", Short: "app diff", Footer: 8},
		keys.Binding{Action: aTreeLogs, Keys: []string{"l", "L"}, Ctx: ctxTree, Group: g, Help: "pod logs", Short: "logs", Footer: 9},
		keys.Binding{Action: aTreeShell, Keys: []string{"e"}, Ctx: ctxTree, Group: g, Help: "a shell in the container, through Argo CD", Short: "shell", Footer: 10},
		keys.Binding{Action: aTreeGraph, Keys: []string{"t"}, Ctx: ctxTree, Group: g, Help: "graph view, Argo CD UI style (list while filtered)", Short: "graph",
			Labels: map[string]string{"on": "list"}, Footer: 11},
		keys.Binding{Action: aTreeSync, Keys: []string{"s"}, Ctx: ctxTree, Group: g, Help: "sync the marked resources", Short: "sync", Labels: syncLabels, Footer: 6},
	)

	// ---- HISTORY tab ----
	add(navBindings(ctxHistory, 0, "move")...)
	g = "HISTORY tab"
	add(
		keys.Binding{Action: aHistRollback, Keys: []string{"enter", "b", "right"}, Ctx: ctxHistory, Group: g, Help: "roll back to this deployment", Short: "rollback", Footer: 1},
		keys.Binding{Action: aHistDiff, Keys: []string{"d"}, Ctx: ctxHistory, Group: g, Help: "diff against live", Short: "diff", Footer: 3},
	)

	// ---- DETAILS tab ----
	add(navBindings(ctxDetails, 0, "move")...)
	g = "DETAILS tab"
	add(
		keys.Binding{Action: aDetailsEdit, Keys: []string{"enter", "right"}, Ctx: ctxDetails, Group: g, Help: "change the marked row under the cursor", Short: "edit", Footer: 1},
		keys.Binding{Action: aDetailsSync, Keys: []string{"s"}, Ctx: ctxDetails, Group: g, Help: "sync", Short: "sync", Labels: syncLabels, Footer: 3},
		keys.Binding{Action: aDetailsEvents, Keys: []string{"e"}, Ctx: ctxDetails, Group: g, Help: "events", Footer: 5},
	)

	// ---- pagers: diff, manifest, logs, events, help ----
	add(navBindings(ctxPager, 1, "scroll")...)
	g = "manifests and diffs"
	add(
		keys.Binding{Action: aPagerNext, Keys: []string{"n"}, Ctx: ctxPager, Group: g, Help: "next match", Footer: 5, FooterGroup: "match"},
		keys.Binding{Action: aPagerPrev, Keys: []string{"N"}, LatinOnly: true, Ctx: ctxPager, Group: g, Help: "previous match", Footer: 5, FooterGroup: "match"},
		keys.Binding{Action: aPagerNoise, Keys: []string{"M"}, LatinOnly: true, Ctx: ctxPager, Group: g, Help: "show managedFields and other bookkeeping", Short: "noise", Footer: 6},
		keys.Binding{Action: aPagerLeave, Keys: []string{"h", "left"}, Ctx: ctxPager, Group: g, Help: "back"},
	)
	g = "diffs"
	add(
		keys.Binding{Action: aDiffSxS, Keys: []string{"s"}, Ctx: ctxDiff, Group: g, Help: "side by side (needs 100 columns)", Short: "side-by-side",
			Labels: map[string]string{"on": "unified"}, Footer: 2},
		keys.Binding{Action: aDiffTool, Keys: []string{"D"}, LatinOnly: true, Ctx: ctxDiff, Group: g, Help: "hand the two documents to your diff tool", Short: "diff tool", Footer: 3},
		keys.Binding{Action: aDiffPairing, Keys: []string{"H"}, LatinOnly: true, Ctx: ctxDiff, Group: g, Help: "pair resources whose names carry a content hash", Short: "hash pairing", Footer: 7},
	)
	add(sxsBindings()...)

	// ---- sync windows ----
	add(navBindings(ctxWindows, 1, "move")...)
	g = "sync windows"
	add(
		keys.Binding{Action: aWinBrowser, Keys: []string{"o"}, Ctx: ctxWindows, Group: g, Help: "open the project in browser — where windows are edited", Short: "project", Footer: 3},
		keys.Binding{Action: aWinAppBrowser, Keys: []string{"O"}, Ctx: ctxWindows, Group: g, Help: "open the application in browser", Short: "app", Footer: 4},
		keys.Binding{Action: aWinReload, Keys: []string{"r", "ctrl+r"}, Ctx: ctxWindows, Group: g, Help: "reload", Footer: 5},
		keys.Binding{Action: aWinLeave, Keys: []string{"h", "left"}, Ctx: ctxWindows, Group: g, Help: "back"},
	)

	// ---- scheduled syncs ----
	add(navBindings(ctxSchedule, 1, "move")...)
	g = "scheduled syncs"
	add(
		keys.Binding{Action: aSchedCancel, Keys: []string{"x", "d"}, Ctx: ctxSchedule, Group: g, Help: "cancel the one under the cursor", Short: "cancel", Footer: 2},
		keys.Binding{Action: aSchedClear, Keys: []string{"c"}, Ctx: ctxSchedule, Group: g, Help: "clear the finished rows", Short: "clear finished", Footer: 3},
		keys.Binding{Action: aSchedBrowser, Keys: []string{"o"}, Ctx: ctxSchedule, Group: g, Help: "open in browser", Short: "browser", Footer: 4},
		keys.Binding{Action: aSchedLeave, Keys: []string{"h", "left"}, Ctx: ctxSchedule, Group: g, Help: "back"},
	)

	// ---- contexts ----
	add(navBindings(ctxContexts, 1, "move")...)
	g = "contexts"
	add(
		keys.Binding{Action: aCtxOpen, Keys: []string{"enter", "l", "right"}, Ctx: ctxContexts, Group: g, Help: "the full credential: source, claims, permissions", Short: "details", Footer: 2},
		keys.Binding{Action: aCtxBrowser, Keys: []string{"o"}, Ctx: ctxContexts, Group: g, Help: "open the server in browser", Short: "browser", Footer: 3},
		keys.Binding{Action: aCtxReload, Keys: []string{"r", "ctrl+r"}, Ctx: ctxContexts, Group: g, Help: "re-ask every server", Short: "reload", Footer: 4},
		keys.Binding{Action: aCtxLogin, Keys: []string{"L"}, LatinOnly: true, Ctx: ctxContexts, Group: g, Help: "log in again through SSO — only a context on the argocd CLI session", Short: "login", Footer: 5},
		keys.Binding{Action: aCtxLeave, Keys: []string{"h", "left"}, Ctx: ctxContexts, Group: g, Help: "back"},
	)
	add(navBindings(ctxCtxDetail, 1, "scroll")...)
	add(
		keys.Binding{Action: aCtxBrowser, Keys: []string{"o"}, Ctx: ctxCtxDetail, Group: g, Help: "open the server in browser", Short: "browser", Footer: 2},
		keys.Binding{Action: aCtxLogin, Keys: []string{"L"}, LatinOnly: true, Ctx: ctxCtxDetail, Group: g, Help: "log in again through SSO", Short: "login", Footer: 3},
		keys.Binding{Action: aCtxClose, Keys: []string{"enter", "h", "left"}, Ctx: ctxCtxDetail, Group: g, Help: "back to the list"},
	)

	// ---- a range select in progress ----
	add(keys.Binding{Action: aVisualCancel, Keys: []string{"esc"}, Ctx: ctxVisual, Group: "multi-select",
		Help: "cancel the range", Short: "cancel", Footer: 5,
		// The range is the innermost level: esc abandons it, not the screen.
		Shadows: []string{aBack}})

	// ---- the filter prompt ----
	//
	// Every unbound printable key is query text here, which is why j and k are
	// not motions and the arrows split by axis: ←/→ edit, ↑/↓ move the rows.
	g = "filter prompt"
	add(
		keys.Binding{Action: aFilterUp, Keys: []string{"up", "ctrl+p"}, Ctx: ctxFilter, Group: g, Help: "previous matching row", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aFilterDown, Keys: []string{"down", "ctrl+n"}, Ctx: ctxFilter, Group: g, Help: "next matching row", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aFilterPgDown, Keys: []string{"pgdown"}, Ctx: ctxFilter, Group: g, Help: "half a page down"},
		keys.Binding{Action: aFilterPgUp, Keys: []string{"pgup"}, Ctx: ctxFilter, Group: g, Help: "half a page up"},
		keys.Binding{Action: aFilterKeep, Keys: []string{"enter"}, Ctx: ctxFilter, Group: g, Help: "close the prompt, keep the query", Short: "keep", Footer: 5},
		keys.Binding{Action: aFilterCancel, Keys: []string{"esc"}, Ctx: ctxFilter, Group: g, Help: "clear the query (cancels a range first)", Short: "clear", Footer: 6,
			// The prompt's way out, pinned like back is outside it.
			Pinned: true},
		keys.Binding{Action: aFilterLeft, Keys: []string{"left", "ctrl+b"}, Ctx: ctxFilter, Group: g, Help: "text cursor left"},
		keys.Binding{Action: aFilterRight, Keys: []string{"right", "ctrl+f"}, Ctx: ctxFilter, Group: g, Help: "text cursor right"},
		keys.Binding{Action: aFilterHome, Keys: []string{"ctrl+a", "home"}, Ctx: ctxFilter, Group: g, Help: "start of line"},
		keys.Binding{Action: aFilterEnd, Keys: []string{"ctrl+e", "end"}, Ctx: ctxFilter, Group: g, Help: "end of line"},
		keys.Binding{Action: aFilterWordLeft, Keys: []string{"alt+b", "alt+left"}, Ctx: ctxFilter, Group: g, Help: "word left"},
		keys.Binding{Action: aFilterWordRight, Keys: []string{"alt+f", "alt+right"}, Ctx: ctxFilter, Group: g, Help: "word right"},
		keys.Binding{Action: aFilterBackspace, Keys: []string{"backspace"}, Ctx: ctxFilter, Group: g, Help: "delete back"},
		keys.Binding{Action: aFilterDelete, Keys: []string{"delete", "ctrl+d"}, Ctx: ctxFilter, Group: g, Help: "delete forward"},
		keys.Binding{Action: aFilterClear, Keys: []string{"ctrl+u"}, Ctx: ctxFilter, Group: g, Help: "clear the query, stay in the prompt"},
		keys.Binding{Action: aFilterKillEnd, Keys: []string{"ctrl+k"}, Ctx: ctxFilter, Group: g, Help: "delete to the end"},
		keys.Binding{Action: aFilterKillWord, Keys: []string{"ctrl+w"}, Ctx: ctxFilter, Group: g, Help: "delete a word"},
		// On the two lists that mark, ← and → mark instead: an exact inverse
		// pair, so a run one row too far is undone by the other arrow. ctrl+b /
		// ctrl+f still move the text cursor there.
		keys.Binding{Action: aFilterMark, Keys: []string{"right"}, Ctx: ctxFilterMarks, Group: g, Help: "mark this row and move down", Short: "mark", Footer: 2},
		keys.Binding{Action: aFilterUnmark, Keys: []string{"left"}, Ctx: ctxFilterMarks, Group: g, Help: "move up and unmark", Short: "unmark", Footer: 3},
		keys.Binding{Action: aFilterExtendUp, Keys: []string{"shift+up"}, Ctx: ctxFilterMarks, Group: g, Help: "extend the selection up", Footer: 4, FooterGroup: "extend"},
		keys.Binding{Action: aFilterExtendDown, Keys: []string{"shift+down"}, Ctx: ctxFilterMarks, Group: g, Help: "extend the selection down", Footer: 4, FooterGroup: "extend"},
		keys.Binding{Action: aFilterComplete, Keys: []string{"tab", "shift+tab"}, Ctx: ctxFilterComplete, Group: g, Help: "complete the word under the cursor", Short: "complete", Footer: 7},
	)
	add(tabBindings(ctxFilterTabs, 0)...)

	// ---- modals ----
	g = "confirmation"
	add(
		keys.Binding{Action: aConfirmToggle, Keys: []string{"h", "l", "left", "right", "tab"}, Ctx: ctxConfirm, Group: g, Help: "move between No and Yes", Short: "move", Footer: 1},
		keys.Binding{Action: aConfirmEnter, Keys: []string{"enter"}, Ctx: ctxConfirm, Group: g, Help: "take the choice under the cursor (No to start)", Short: "take it", Footer: 2},
		keys.Binding{Action: aConfirmYes, Keys: []string{"y", "Y"}, Ctx: ctxConfirm, Group: g, Help: "yes, outright", Footer: 3, FooterGroup: "outright"},
		keys.Binding{Action: aConfirmNo, Keys: []string{"n", "N", "esc"}, Ctx: ctxConfirm, Group: g, Help: "no, outright", Footer: 3, FooterGroup: "outright"},
	)
	g = "sync options"
	add(
		keys.Binding{Action: aOptDown, Keys: []string{"j", "down"}, Ctx: ctxSyncOpts, Group: g, Help: "next option", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aOptUp, Keys: []string{"k", "up"}, Ctx: ctxSyncOpts, Group: g, Help: "previous option", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aOptTop, Keys: []string{"g", "home"}, Ctx: ctxSyncOpts, Group: g, Help: "first option"},
		keys.Binding{Action: aOptBottom, Keys: []string{"G", "end"}, Ctx: ctxSyncOpts, Group: g, Help: "last option"},
		keys.Binding{Action: aOptToggle, Keys: []string{" ", "h", "l", "left", "right"}, Ctx: ctxSyncOpts, Group: g, Help: "toggle the option under the cursor", Short: "toggle", Footer: 2},
		keys.Binding{Action: aOptPrune, Keys: []string{"p"}, Ctx: ctxSyncOpts, Group: g, Help: "prune (delete resources not in git)"},
		keys.Binding{Action: aOptDryRun, Keys: []string{"d"}, Ctx: ctxSyncOpts, Group: g, Help: "dry-run (compute only, apply nothing)"},
		keys.Binding{Action: aOptWait, Keys: []string{"w"}, Ctx: ctxSyncOpts, Group: g, Help: "wait for the sync window instead of syncing now"},
		keys.Binding{Action: aOptContinue, Keys: []string{"enter", "y"}, Ctx: ctxSyncOpts, Group: g, Help: "continue to the confirmation", Short: "continue", Footer: 3},
		keys.Binding{Action: aOptCancel, Keys: []string{"esc", "n"}, Ctx: ctxSyncOpts, Group: g, Help: "cancel", Footer: 4},
	)
	g = "revision picker"
	add(
		keys.Binding{Action: aRevUp, Keys: []string{"up", "ctrl+p"}, Ctx: ctxRevPicker, Group: g, Help: "previous ref", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aRevDown, Keys: []string{"down", "ctrl+n"}, Ctx: ctxRevPicker, Group: g, Help: "next ref", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aRevPgDown, Keys: []string{"pgdown"}, Ctx: ctxRevPicker, Group: g, Help: "half a page down"},
		keys.Binding{Action: aRevPgUp, Keys: []string{"pgup"}, Ctx: ctxRevPicker, Group: g, Help: "half a page up"},
		keys.Binding{Action: aRevBackspace, Keys: []string{"backspace"}, Ctx: ctxRevPicker, Group: g, Help: "delete back"},
		keys.Binding{Action: aRevChoose, Keys: []string{"enter"}, Ctx: ctxRevPicker, Group: g, Help: "select this revision", Short: "select", Footer: 2},
		keys.Binding{Action: aRevCancel, Keys: []string{"esc"}, Ctx: ctxRevPicker, Group: g, Help: "cancel", Footer: 3},
	)
	g = "container picker"
	add(
		keys.Binding{Action: aPickDown, Keys: []string{"j", "down", "ctrl+n"}, Ctx: ctxContainer, Group: g, Help: "next container", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aPickUp, Keys: []string{"k", "up", "ctrl+p"}, Ctx: ctxContainer, Group: g, Help: "previous container", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aPickTop, Keys: []string{"g", "home"}, Ctx: ctxContainer, Group: g, Help: "first container"},
		keys.Binding{Action: aPickBottom, Keys: []string{"G", "end"}, Ctx: ctxContainer, Group: g, Help: "last container"},
		keys.Binding{Action: aPickChoose, Keys: []string{"enter", " "}, Ctx: ctxContainer, Group: g, Help: "choose (or press its number)", Short: "choose", Footer: 2},
		keys.Binding{Action: aPickCancel, Keys: []string{"esc"}, Ctx: ctxContainer, Group: g, Help: "cancel", Footer: 3},
	)
	g = "command palette"
	add(
		keys.Binding{Action: aPalUp, Keys: []string{"up", "ctrl+p"}, Ctx: ctxCommands, Group: g, Help: "previous command", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aPalDown, Keys: []string{"down", "ctrl+n"}, Ctx: ctxCommands, Group: g, Help: "next command", Footer: 1, FooterGroup: "move"},
		keys.Binding{Action: aPalRun, Keys: []string{"enter"}, Ctx: ctxCommands, Group: g, Help: "run the command", Short: "run", Footer: 2},
		keys.Binding{Action: aPalBackspace, Keys: []string{"backspace"}, Ctx: ctxCommands, Group: g, Help: "delete back"},
		keys.Binding{Action: aPalClear, Keys: []string{"ctrl+u"}, Ctx: ctxCommands, Group: g, Help: "clear the query"},
		keys.Binding{Action: aPalClose, Keys: []string{"esc"}, Ctx: ctxCommands, Group: g, Help: "close", Footer: 3},
	)
	return bs
}

// baseKeymap is the table before the user's keys.yaml.
var baseKeymap = keys.New(bindings()...)

// keymap is the table in effect: the user's overrides when they loaded, the
// defaults otherwise (and in tests that build a Model literal).
func (m *Model) keymap() keys.Keymap { return m.km.Or(baseKeymap) }

// Stacks, innermost first. allStacks lists every combination that can be
// active, for the conflict check, override validation and the help page.
var (
	stackApps      = []keys.Ctx{ctxApps, ctxGlobal}
	stackAppSets   = []keys.Ctx{ctxAppSets, ctxGlobal}
	stackTree      = []keys.Ctx{ctxTree, ctxApp, ctxGlobal}
	stackHistory   = []keys.Ctx{ctxHistory, ctxApp, ctxGlobal}
	stackDetails   = []keys.Ctx{ctxDetails, ctxApp, ctxGlobal}
	stackPager     = []keys.Ctx{ctxPager, ctxGlobal}
	stackDiff      = []keys.Ctx{ctxDiff, ctxPager, ctxGlobal}
	stackWindows   = []keys.Ctx{ctxWindows, ctxGlobal}
	stackSchedule  = []keys.Ctx{ctxSchedule, ctxGlobal}
	stackContexts  = []keys.Ctx{ctxContexts, ctxGlobal}
	stackCtxDetail = []keys.Ctx{ctxCtxDetail, ctxGlobal}
	stackVisApps   = []keys.Ctx{ctxVisual, ctxApps, ctxGlobal}
	stackVisTree   = []keys.Ctx{ctxVisual, ctxTree, ctxApp, ctxGlobal}

	stackFilterApps  = []keys.Ctx{ctxFilterMarks, ctxFilterComplete, ctxFilter}
	stackFilterTree  = []keys.Ctx{ctxFilterMarks, ctxFilterTabs, ctxFilter}
	stackFilterTabs  = []keys.Ctx{ctxFilterTabs, ctxFilter}
	stackFilterPlain = []keys.Ctx{ctxFilter}

	stackConfirm   = []keys.Ctx{ctxConfirm}
	stackSyncOpts  = []keys.Ctx{ctxSyncOpts}
	stackRevPicker = []keys.Ctx{ctxRevPicker}
	stackContainer = []keys.Ctx{ctxContainer}
	stackPalette   = []keys.Ctx{ctxCommands}
)

func allStacks() [][]keys.Ctx {
	return [][]keys.Ctx{
		stackApps, stackAppSets, stackTree, stackHistory, stackDetails,
		stackDiff, stackPager, stackWindows, stackSchedule, stackContexts, stackCtxDetail,
		stackVisApps, stackVisTree,
		stackFilterApps, stackFilterTree, stackFilterTabs, stackFilterPlain,
		stackConfirm, stackSyncOpts, stackRevPicker, stackContainer, stackPalette,
	}
}

// screenStack is the stack of the screen in front, ignoring any modal, prompt
// or range: what the palette offers and what the help page leads with.
func (m *Model) screenStack() []keys.Ctx { return m.stackFor(m.screen) }

// stackFor is screenStack for a given screen, with the current tab and panel.
func (m *Model) stackFor(sc screen) []keys.Ctx {
	switch sc {
	case screenApps:
		return stackApps
	case screenAppSets:
		return stackAppSets
	case screenApp:
		switch m.tab {
		case tabHistory:
			return stackHistory
		case tabDetails:
			return stackDetails
		}
		return stackTree
	case screenDiff:
		return stackDiff
	case screenWindows:
		return stackWindows
	case screenSchedule:
		return stackSchedule
	case screenContexts:
		if m.ctxDetail {
			return stackCtxDetail
		}
		return stackContexts
	}
	return stackPager
}

// keyStack is the stack the next key resolves against.
func (m *Model) keyStack() []keys.Ctx {
	switch m.overlay {
	case overlayConfirm:
		return stackConfirm
	case overlaySyncOpts:
		return stackSyncOpts
	case overlayRevPicker:
		return stackRevPicker
	case overlayContainer:
		return stackContainer
	case overlayPalette:
		return stackPalette
	case overlayError:
		return nil // any key dismisses
	}
	if m.filtering {
		_, _, markable := m.markableList()
		switch {
		case m.screen == screenApps:
			return stackFilterApps
		case m.screen == screenApp && markable:
			return stackFilterTree
		case m.screen == screenApp:
			return stackFilterTabs
		}
		return stackFilterPlain
	}
	s := m.screenStack()
	if m.visualFrom >= 0 {
		return append([]keys.Ctx{ctxVisual}, s...)
	}
	return s
}

// keyOpts is the model's view of its actions, rebuilt per call so it always
// reads the model being handled (see keys.Opts).
func (m *Model) keyOpts() keys.Opts {
	return keys.Opts{Enabled: m.actionEnabled, Visible: m.actionVisible, State: m.actionState, Label: m.actionLabel}
}

// actionLabel carries live values into the footer. The pending-sync count
// leads it: "W 3 scheduled" says both that something is waiting and how much.
func (m *Model) actionLabel(a string) (string, bool) {
	if a == aSchedules {
		if n := m.pendingSchedules(); n > 0 {
			return fmt.Sprintf("%d scheduled", n), true
		}
	}
	return "", false
}

// actionEnabled gates dispatch and the palette. A disabled key falls through
// to outer contexts, so this is used only where nothing outer claims the key.
func (m *Model) actionEnabled(a string) bool {
	switch a {
	case aAppParent:
		return m.tab == tabResources && m.treeGraphActive()
	case aFleetErrors:
		return len(m.fleetErrs) > 0
	}
	return true
}

// actionVisible narrows the footer to the hints that matter right now.
func (m *Model) actionVisible(a string) bool {
	// A range in progress is a mode: the keys that end it are the ones worth
	// naming while it is on, plus the way out.
	if m.visualFrom >= 0 && !m.filtering && m.overlay == overlayNone {
		switch a {
		case aMarkRange, aVisualCancel, aQuit, aHelp:
			return true
		}
		return false
	}
	switch a {
	case aBack:
		// Nothing to go back to at the root, unless a filter is standing.
		switch m.screen {
		case screenApps, screenAppSets:
			return m.markedOnly || m.appFilter.String() != "" && m.screen == screenApps ||
				m.screen == screenAppSets && m.appsetFilter != ""
		}
		return true
	case aHelp:
		return m.screen != screenHelp
	case aSchedules:
		// Pending syncs exist only in this process and are otherwise invisible
		// from wherever the reader is, so the way back to them leads the
		// footer — and is absent when there is nothing to go back to.
		return m.screen != screenSchedule && m.pendingSchedules() > 0
	case aContexts:
		return m.screen == screenApps
	case aSplitGrow, aSplitShrink, aSplitReset:
		return m.sxsActive()
	case aFilter:
		switch m.screen {
		case screenApps, screenAppSets, screenDiff, screenManifest, screenLogs, screenEvents:
			return true
		case screenApp:
			return m.tab == tabResources
		}
		return false
	}
	return true
}

// actionState picks a binding's Labels entry.
func (m *Model) actionState(a string) string {
	on := func(b bool) string {
		if b {
			return "on"
		}
		return ""
	}
	switch a {
	case aMarkRange:
		if m.visualFrom >= 0 {
			return "active"
		}
	case aTreeSync, aDetailsSync:
		if m.syncBlocked() {
			return "blocked"
		}
	case aTreeGraph:
		return on(m.treeGraph)
	case aDiffSxS:
		return on(m.sxs)
	case aAppsFollow:
		return on(m.autoRefresh)
	case aBack:
		if m.screen == screenContexts && m.ctxDetail {
			return "detail"
		}
	case aFilter:
		switch m.screen {
		case screenDiff, screenManifest, screenLogs, screenEvents, screenHelp:
			return "search"
		}
	}
	return ""
}

// keyNames is how keys are spelled on screen: words, except the arrows, which
// read better as arrows wherever the icon set can draw them.
func (m *Model) keyNames() keys.Names {
	n := keys.Names{}
	if m.gl.set == iconsASCII {
		return n
	}
	for k, v := range map[string]string{"up": "↑", "down": "↓", "left": "←", "right": "→",
		"shift+up": "⇧↑", "shift+down": "⇧↓"} {
		n = n.With(k, v)
	}
	return n
}

// keyHint names the key that runs action in stack right now, for the few
// places prose names a key ("then w in the sync options"). keys.yaml
// overrides and inner-context shadows are already applied by KeyFor, so the
// prose says what the key does.
func (m *Model) keyHint(stack []keys.Ctx, action string) string {
	if k, ok := m.keymap().KeyFor(stack, action, m.keyOpts()); ok {
		return m.keyNames().Name(k)
	}
	return "?"
}

// footerStyle renders hints plain; renderFooter wraps the line in the footer
// style, as the hand-written footer did.
func (m *Model) footerStyle() keys.FooterStyle {
	return keys.FooterStyle{Names: m.keyNames()}
}

// modalHint is a modal's key line, from the same table its keys resolve in.
func (m *Model) modalHint(stack []keys.Ctx, w int) string {
	st := keys.FooterStyle{Names: m.keyNames(), Separator: "  ·  ", GroupSeparator: "/"}
	return m.st.dim.Render(m.keymap().Footer(stack, w, m.keyOpts(), st))
}

// helpStyle maps the help page onto argx's styles.
func (m *Model) helpStyle() keys.HelpStyle {
	return keys.HelpStyle{
		Title: m.st.accent,
		Key:   m.st.info,
		Desc:  m.st.dim,
		Muted: m.st.dim,
		Names: m.keyNames(),
	}
}

// loadKeymap applies the user's keys.yaml ($XDG_CONFIG_HOME/argx/keys.yaml)
// to the defaults, checked against every stack argx can activate: an override
// that gives a key two owners or hides quit/help/back is refused whole.
func loadKeymap() (keys.Keymap, error) {
	return baseKeymap.LoadOverrides(keys.ConfigPath("argx"), allStacks()...)
}
