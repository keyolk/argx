package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// Semantic color tokens. Render paths reference these, never a raw hex literal,
// so theming and NO_COLOR are handled in one place.
var (
	cAccent  = lipgloss.Color("#A78BFA") // selection, titles
	cInfo    = lipgloss.Color("#38BDF8") // identifiers, revisions, paths
	cSuccess = lipgloss.Color("#22C55E") // Synced, Healthy, added lines
	cWarn    = lipgloss.Color("#F59E0B") // OutOfSync, Progressing, modified
	cError   = lipgloss.Color("#EF4444") // Degraded, Missing, removed lines
	cDim     = lipgloss.Color("#9CA3AF") // secondary metadata
	cBorder  = lipgloss.Color("#4B5563")
	cMark    = lipgloss.Color("#F472B6") // multi-select marks
)

// ctxPalette colors the server column when a session spans several Argo CDs.
//
// The colors are assigned by position in the fleet rather than hashed from the
// name: a hash gives a stable color per server but no control over which
// servers end up looking alike, and two prod servers rendering in near-identical
// blues is the failure that matters here. Position also keeps the colors stable
// for a given invocation, which is what the reader actually relies on.
//
// The palette deliberately excludes the status colors — a server tinted the
// same green as "Healthy" would read as a status.
var ctxPalette = []lipgloss.Color{
	lipgloss.Color("#38BDF8"), // cyan
	lipgloss.Color("#A78BFA"), // purple
	lipgloss.Color("#F472B6"), // pink
	lipgloss.Color("#FB923C"), // orange
	lipgloss.Color("#2DD4BF"), // teal
	lipgloss.Color("#C084FC"), // violet
}

// styles holds every style used in the render path, built once against the
// terminal's detected color profile.
type styles struct {
	title    lipgloss.Style
	subtitle lipgloss.Style
	dim      lipgloss.Style
	accent   lipgloss.Style
	info     lipgloss.Style
	success  lipgloss.Style
	warn     lipgloss.Style
	err      lipgloss.Style
	selected lipgloss.Style
	header   lipgloss.Style
	footer   lipgloss.Style
	filter   lipgloss.Style
	// cursorCell is the text cursor in the filter prompt: reverse video over
	// the character it sits on, so its position is unambiguous.
	cursorCell lipgloss.Style
	mark       lipgloss.Style
	modal      lipgloss.Style
	modalErr   lipgloss.Style
	diffAdd    lipgloss.Style
	diffDel    lipgloss.Style
	diffHunk   lipgloss.Style

	// ctx holds one style per fleet position; see ctxPalette.
	ctx []lipgloss.Style
	// renderer is kept so context styles can be built once the fleet is known.
	renderer *lipgloss.Renderer
}

// initContexts builds one style per context, in fleet order.
func (s *styles) initContexts(n int) {
	s.ctx = make([]lipgloss.Style, n)
	for i := 0; i < n; i++ {
		s.ctx[i] = s.renderer.NewStyle().Foreground(ctxPalette[i%len(ctxPalette)])
	}
}

func newStyles() *styles {
	// Rendering to stderr keeps profile detection working when stdout is
	// redirected, and matches where Bubble Tea writes.
	r := lipgloss.NewRenderer(os.Stderr)
	s := &styles{
		renderer:   r,
		title:      r.NewStyle().Bold(true).Foreground(cAccent),
		subtitle:   r.NewStyle().Foreground(cDim),
		dim:        r.NewStyle().Foreground(cDim),
		accent:     r.NewStyle().Foreground(cAccent),
		info:       r.NewStyle().Foreground(cInfo),
		success:    r.NewStyle().Foreground(cSuccess),
		warn:       r.NewStyle().Foreground(cWarn),
		err:        r.NewStyle().Foreground(cError),
		selected:   r.NewStyle().Bold(true).Foreground(cAccent),
		header:     r.NewStyle().Bold(true).Foreground(cDim),
		footer:     r.NewStyle().Foreground(cDim),
		filter:     r.NewStyle().Foreground(cInfo),
		cursorCell: r.NewStyle().Reverse(true),
		mark:       r.NewStyle().Bold(true).Foreground(cMark),
		diffAdd:    r.NewStyle().Foreground(cSuccess),
		diffDel:    r.NewStyle().Foreground(cError),
		diffHunk:   r.NewStyle().Foreground(cInfo),
	}
	s.modal = r.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 2)
	s.modalErr = r.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cError).
		Padding(0, 2)
	return s
}

// kindStyle colors a resource kind's icon by what the kind is for, so a tree
// separates workloads from networking from configuration at a glance.
//
// The grouping is the same one the tree sorts by, and it is deliberately coarse:
// a distinct color per kind would be a lookup table nobody can hold in their
// head, and the icon already carries the specific identity.
func (s *styles) kindStyle(kind string) lipgloss.Style {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Rollout", "Job", "CronJob", "ReplicaSet", "Pod":
		return s.info
	case "Service", "Ingress", "IngressClass", "VirtualService", "Gateway",
		"DestinationRule", "NetworkPolicy", "Endpoints", "EndpointSlice":
		return s.accent
	case "Secret", "ServiceAccount", "Role", "RoleBinding", "ClusterRole",
		"ClusterRoleBinding", "Certificate", "Issuer", "ClusterIssuer":
		return s.warn
	default:
		return s.dim
	}
}

// syncStyle maps an Argo CD sync status to its semantic style. Callers pair the
// style with the status text itself, never color alone.
func (s *styles) syncStyle(status string) lipgloss.Style {
	switch status {
	case "Synced":
		return s.success
	case "OutOfSync":
		return s.warn
	default:
		return s.dim
	}
}

// healthStyle maps an Argo CD health status to its semantic style.
func (s *styles) healthStyle(status string) lipgloss.Style {
	switch status {
	case "Healthy":
		return s.success
	case "Progressing", "Suspended":
		return s.warn
	case "Degraded", "Missing":
		return s.err
	default:
		return s.dim
	}
}
