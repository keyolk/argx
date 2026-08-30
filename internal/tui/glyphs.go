package tui

import (
	"os"
	"strings"
)

// Glyphs are chosen from one of three sets, so argx renders the same layout in
// a Nerd Font terminal, a plain UTF-8 one, and an ASCII-only one.
//
// The set is a run-time choice rather than a detected one: there is no reliable
// way to ask a terminal whether its font has the Nerd Font private-use range,
// and guessing wrong renders every icon as a tofu box. Unicode is therefore the
// default — it is legible everywhere — and Nerd Font is opt-in.
//
// Every glyph in every set is one cell wide. A two-cell icon would push the
// column it sits in and break the alignment the whole render path depends on;
// the Nerd Font ranges used here are single-width by design.

// iconSet names a glyph repertoire.
type iconSet int

const (
	// iconsUnicode uses box drawing and geometric shapes: legible in any
	// UTF-8 terminal without a patched font.
	iconsUnicode iconSet = iota
	// iconsNerd uses Nerd Font icons — Kubernetes kinds, git branches, health
	// states. Requires a patched font.
	iconsNerd
	// iconsASCII avoids anything outside 7-bit ASCII, for SSH into minimal
	// images and terminals that mangle box drawing.
	iconsASCII
)

// resolveIconSet picks the repertoire: ARGX_ICONS, then the config file, then
// the default.
//
// The environment wins over the config so that one session on a terminal
// without the font can opt out without editing a file that every other session
// shares. ARGX_ASCII stays honored because it shipped first and is what a
// script in someone's dotfiles already sets.
func resolveIconSet(configured string) iconSet {
	if s, ok := parseIconSet(os.Getenv("ARGX_ICONS")); ok {
		return s
	}
	if s, ok := parseIconSet(configured); ok {
		return s
	}
	if os.Getenv("ARGX_ASCII") != "" {
		return iconsASCII
	}
	return iconsUnicode
}

// parseIconSet maps a name to a set, reporting whether it was recognized.
//
// An unrecognized value is not an error: it falls through to the next source
// and ultimately to the default, because a typo should leave argx legible
// rather than refusing to start.
func parseIconSet(name string) (iconSet, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "nerd", "nerdfont", "nf":
		return iconsNerd, true
	case "ascii", "plain":
		return iconsASCII, true
	case "unicode", "uni":
		return iconsUnicode, true
	}
	return iconsUnicode, false
}

// glyphSet is every glyph the render path uses.
//
// It is a flat struct rather than a map so that a missing glyph is a compile
// error: a map would silently render an empty string and shift a column.
type glyphSet struct {
	set iconSet

	// ---- tree connectors ----
	branch string // ├─
	corner string // └─
	pipe   string // │
	blank  string

	// ---- selection ----
	marked   string
	unmarked string
	cursor   string
	editable string // marks a row the DETAILS tab can change

	// ---- separators ----
	sep    string // between status-line parts
	tabSep string // between tab labels

	// ---- status ----
	// noHealth fills the health column for kinds Argo CD has no health check
	// for. It occupies the cell rather than leaving it blank, so names below it
	// stay aligned.
	noHealth    string
	synced      string
	outOfSync   string
	healthy     string
	progressing string
	degraded    string
	missing     string
	suspended   string
	unknown     string

	// ---- field markers ----
	// Empty in the ASCII and Unicode sets: a column identified by its header
	// does not need a per-row marker, and one on every row is clutter. They
	// earn their place only in the Nerd Font set, where they are recognizable
	// at a glance and replace nothing.
	revision  string
	branchRef string
	tagRef    string
	cluster   string
	namespace string
	project   string
	server    string
	clock     string
	person    string

	// ---- permission answers ----
	//
	// Used where the answer is the content of the line rather than a column
	// marker, so all three sets carry one: the context view's permission list
	// reads as a checklist and a blank cell there means nothing at all.
	yes string
	no  string

	// ---- tabs ----
	//
	// Empty outside the Nerd Font set: the tab label already carries the number
	// that selects it, and a second numeral beside it reads as a typo.
	tabResources string
	tabHistory   string
	tabDetails   string

	// kinds maps a Kubernetes kind to its icon. A kind with no entry falls back
	// to kindDefault.
	kinds       map[string]string
	kindDefault string
}

// newGlyphs builds the default glyph set, for callers with no config in hand.
func newGlyphs() glyphSet { return newGlyphsFor("") }

// newGlyphsFor builds the glyph set for a configured repertoire name.
func newGlyphsFor(configured string) glyphSet {
	switch resolveIconSet(configured) {
	case iconsNerd:
		return nerdGlyphs()
	case iconsASCII:
		return asciiGlyphs()
	default:
		return unicodeGlyphs()
	}
}

func unicodeGlyphs() glyphSet {
	return glyphSet{
		set:      iconsUnicode,
		branch:   "├─ ",
		corner:   "└─ ",
		pipe:     "│  ",
		blank:    "   ",
		marked:   "◉",
		unmarked: "○",
		cursor:   "▸",
		editable: "*",
		sep:      "  ·  ",
		tabSep:   " │ ",

		noHealth:    "·",
		synced:      "S",
		outOfSync:   "!",
		healthy:     "H",
		progressing: "P",
		degraded:    "D",
		missing:     "M",
		suspended:   "Z",
		unknown:     "?",

		yes: "\u2713", // check
		no:  "\u2717", // ballot X

		kindDefault: "",
	}
}

func asciiGlyphs() glyphSet {
	return glyphSet{
		set:      iconsASCII,
		branch:   "|- ",
		corner:   "`- ",
		pipe:     "|  ",
		blank:    "   ",
		marked:   "*",
		unmarked: " ",
		cursor:   ">",
		editable: "*",
		sep:      " | ",
		tabSep:   " | ",

		noHealth:    ".",
		synced:      "S",
		outOfSync:   "!",
		healthy:     "H",
		progressing: "P",
		degraded:    "D",
		missing:     "M",
		suspended:   "Z",
		unknown:     "?",

		yes: "y",
		no:  "n",

		kindDefault: "",
	}
}

// nerdGlyphs uses Nerd Font codepoints.
//
// The Kubernetes icons come from the Material Design range that Nerd Font
// embeds (nf-md-*); the rest are Font Awesome and Dev Icons (nf-fa-*, nf-dev-*).
// All are single-width.
func nerdGlyphs() glyphSet {
	return glyphSet{
		set:      iconsNerd,
		branch:   "\u251c\u2500 ",
		corner:   "\u2514\u2500 ",
		pipe:     "\u2502  ",
		blank:    "   ",
		marked:   "\U000f0e1e",     // nf-md-checkbox_marked_circle
		unmarked: "\U000f0130",     // nf-md-checkbox_blank_circle_outline
		cursor:   "\U000f0142",     // nf-md-chevron_right
		editable: "\U000f03eb",     // nf-md-pencil
		sep:      "  \U000f09de  ", // nf-md-circle_small
		tabSep:   " \u2502 ",

		// Status. Distinct silhouettes, not just distinct colors: a check, a
		// warning triangle, a heart, a refresh arrow, a cross, and a question
		// stay apart in monochrome.
		noHealth:    "\U000f09de", // circle_small — no health check for this kind
		synced:      "\U000f012c", // nf-md-check
		outOfSync:   "\U000f0026", // nf-md-alert
		healthy:     "\U000f02d1", // nf-md-heart
		progressing: "\U000f0450", // nf-md-refresh
		degraded:    "\U000f0159", // nf-md-close_circle
		missing:     "\U000f02d7", // nf-md-help_circle
		suspended:   "\U000f03e4", // nf-md-pause_circle
		unknown:     "\U000f02d6", // nf-md-help

		revision:  "\U000f0718", // nf-md-source_commit
		branchRef: "\U000f062c", // nf-md-source_branch
		tagRef:    "\U000f04f9", // nf-md-tag
		cluster:   "\U000f048b", // nf-md-server
		namespace: "\U000f0f89", // nf-md-select_group
		project:   "\U000f024b", // nf-md-folder
		server:    "\U000f015e", // nf-md-cloud_outline
		clock:     "\U000f0954", // nf-md-clock_fast
		person:    "\U000f0004", // nf-md-account

		yes: "\U000f012c", // nf-md-check
		no:  "\U000f0156", // nf-md-close

		tabResources: "\U000f0a30", // nf-md-cube_outline
		tabHistory:   "\U000f02da", // nf-md-history
		tabDetails:   "\U000f0493", // nf-md-cog

		// Kubernetes kinds. Workload controllers first, then networking, then
		// configuration and storage — the order a reader scans a tree in.
		kinds: map[string]string{
			"Pod":         "\U000f0a30", // nf-md-cube_outline
			"Deployment":  "\U000f0868", // nf-md-rocket
			"ReplicaSet":  "\U000f0a2f", // nf-md-cube_scan
			"StatefulSet": "\U000f01bc", // nf-md-database
			"DaemonSet":   "\U000f06a9", // nf-md-cog_transfer
			"Job":         "\U000f0407", // nf-md-briefcase
			"CronJob":     "\U000f0954", // nf-md-clock_fast
			"Rollout":     "\U000f0868",

			"Service":         "\U000f0337", // nf-md-lan_connect
			"Ingress":         "\U000f0484", // nf-md-router_network
			"IngressClass":    "\U000f0484",
			"VirtualService":  "\U000f0337",
			"Gateway":         "\U000f0d1c", // nf-md-gate
			"DestinationRule": "\U000f0337",
			"NetworkPolicy":   "\U000f0483", // nf-md-shield_lock
			"Endpoints":       "\U000f0337",
			"EndpointSlice":   "\U000f0337",

			"ConfigMap":      "\U000f0219", // nf-md-file_document
			"Secret":         "\U000f0306", // nf-md-key
			"ServiceAccount": "\U000f0004", // nf-md-account
			"Role":           "\U000f0bd6", // nf-md-shield_account
			"RoleBinding":    "\U000f0bd6",
			"ClusterRole":    "\U000f0bd6",

			"PersistentVolume":      "\U000f02ca", // nf-md-harddisk
			"PersistentVolumeClaim": "\U000f02ca",
			"StorageClass":          "\U000f02ca",

			"Namespace":               "\U000f0f89", // nf-md-select_group
			"HorizontalPodAutoscaler": "\U000f04c1", // nf-md-arrow_expand_vertical
			"PodDisruptionBudget":     "\U000f0483",
			"Certificate":             "\U000f0bc5", // nf-md-certificate_outline
			"Issuer":                  "\U000f0bc5",
			"ClusterIssuer":           "\U000f0bc5",
			"ServiceMonitor":          "\U000f04db", // nf-md-chart_line
			"PrometheusRule":          "\U000f04db",
			"Application":             "\U000f0a30",
		},
		kindDefault: "\U000f0b2b", // nf-md-code_braces_box
	}
}

// kindIcon is the icon for a Kubernetes kind, or empty when the set has none.
//
// The empty result matters: callers must not pad a cell for an icon that is not
// there, or the Unicode and ASCII sets grow a column of spaces.
func (g glyphSet) kindIcon(kind string) string {
	if g.kinds == nil {
		return ""
	}
	if s, ok := g.kinds[kind]; ok {
		return s
	}
	return g.kindDefault
}

// prefix renders an icon followed by a space, or nothing when the set has no
// icon for it. Every caller that puts an icon before a value goes through this,
// so the spacing is decided in one place.
func (g glyphSet) prefix(icon string) string {
	if icon == "" {
		return ""
	}
	return icon + " "
}

// hasIcons reports whether this set carries decorative field markers, which is
// what decides whether a column budgets width for one.
func (g glyphSet) hasIcons() bool { return g.set == iconsNerd }

// syncGlyph is the marker for an Argo CD sync status.
//
// In the Unicode and ASCII sets this is a letter, so the status survives
// monochrome terminals and colorblind readers; in the Nerd Font set it is an
// icon whose shape carries the same distinction.
func (g glyphSet) syncGlyph(status string) string {
	switch status {
	case "Synced":
		return g.synced
	case "OutOfSync":
		return g.outOfSync
	case "Unknown":
		return g.unknown
	default:
		if g.set == iconsNerd {
			return g.noHealth
		}
		return "-"
	}
}

// healthGlyph is the marker for an Argo CD health status.
func (g glyphSet) healthGlyph(status string) string {
	switch status {
	case "Healthy":
		return g.healthy
	case "Progressing":
		return g.progressing
	case "Degraded":
		return g.degraded
	case "Missing":
		return g.missing
	case "Suspended":
		return g.suspended
	case "Unknown":
		return g.unknown
	default:
		// A kind Argo CD has no health check for — ConfigMap, Secret, most
		// CRDs. A filler rather than a space keeps the column aligned; an empty
		// cell shifts every name after it and reads as a broken row.
		return g.noHealth
	}
}

// tabIcon is the marker shown beside a tab's label.
func (g glyphSet) tabIcon(t tab) string {
	switch t {
	case tabHistory:
		return g.tabHistory
	case tabDetails:
		return g.tabDetails
	default:
		return g.tabResources
	}
}

// refIcon distinguishes a tag from a branch by the shape of its name.
//
// Argo CD does not say which a target revision is, and asking the repository
// would be a network round trip per row. The heuristic is the one a human uses:
// a name that starts with a version number, or with "v" followed by one, is a
// tag; everything else is a branch. A wrong guess costs a slightly misleading
// icon, never a wrong action.
func (g glyphSet) refIcon(rev string) string {
	if g.tagRef == "" {
		return ""
	}
	s := strings.TrimPrefix(rev, "v")
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		return g.tagRef
	}
	return g.branchRef
}
