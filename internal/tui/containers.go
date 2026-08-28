package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/argx/internal/argocd"
)

// Container selection, and the shell that follows it.
//
// A pod with one container needs no choice, so it is not offered one: the modal
// appears only when there is something to decide. A pod with a sidecar is where
// it earns its place — reading the wrong container's logs is a silent wrong
// answer, not an error.

// containerPurpose says what the picker is being opened for, since the same
// list serves both and only the action at the end differs.
type containerPurpose int

const (
	containerForLogs containerPurpose = iota
	containerForExec
)

// containerPicker is the modal's state.
type containerPicker struct {
	purpose    containerPurpose
	containers []argocd.Container
	cur        int
	// pod is what the chosen container belongs to, carried so the action does
	// not have to re-derive it from a cursor that may have moved.
	pod argocd.Node
	// err is a fetch failure, shown in place of the list.
	err string
	// loading is true while the containers are being fetched.
	loading bool
}

// openContainerPicker starts the flow for a pod.
//
// The containers are fetched first, because the decision of whether to ask
// depends on how many there are — and that is not known until the pod's
// manifest arrives.
func (m *Model) openContainerPicker(n argocd.Node, purpose containerPurpose) tea.Cmd {
	if m.app == nil {
		return nil
	}
	if !n.IsPod() {
		m.setToast("logs and exec are only available for pods")
		return nil
	}
	m.picker = containerPicker{purpose: purpose, pod: n, loading: true}
	m.overlay = overlayContainer
	return m.loadContainersCmd(*m.app, n)
}

// chooseContainer acts on the selected container.
func (m *Model) chooseContainer() (tea.Model, tea.Cmd) {
	p := &m.picker
	if p.cur < 0 || p.cur >= len(p.containers) || m.app == nil {
		return m, nil
	}
	c := p.containers[p.cur]
	m.overlay = overlayNone

	switch p.purpose {
	case containerForExec:
		if c.Init {
			// An init container that has finished has no process to attach to,
			// and the server's error for that says only "Failed to exec".
			m.setToast(c.Name + " is an init container — it has logs, but nothing to exec into")
			return m, nil
		}
		return m, m.execCmd(*m.app, p.pod, c.Name)
	default:
		m.push(screenLogs)
		m.pager, m.pagerTitle = nil, "logs · "+p.pod.Name+" · "+c.Name
		return m, m.loadLogsCmd(*m.app, p.pod, c.Name)
	}
}

// handleContainerKey drives the picker.
func (m *Model) handleContainerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.picker
	switch msg.String() {
	case "esc", "q":
		m.overlay = overlayNone
	case "j", "down", "ctrl+n":
		p.cur++
	case "k", "up", "ctrl+p":
		p.cur--
	case "g", "home":
		p.cur = 0
	case "G", "end":
		p.cur = len(p.containers) - 1
	case "enter", " ":
		return m.chooseContainer()
	default:
		// A digit selects directly, which is faster than arrowing for the two-
		// or three-container case this modal exists for.
		if len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '9' {
			if i := int(msg.Runes[0] - '1'); i < len(p.containers) {
				p.cur = i
				return m.chooseContainer()
			}
		}
	}
	if p.cur >= len(p.containers) {
		p.cur = len(p.containers) - 1
	}
	if p.cur < 0 {
		p.cur = 0
	}
	return m, nil
}

// renderContainerPicker draws the modal.
func (m *Model) renderContainerPicker() string {
	p := &m.picker
	w := m.modalContentWidth(72)

	verb := "Logs"
	if p.purpose == containerForExec {
		verb = "Shell"
	}
	lines := []string{
		m.st.accent.Render(verb + " · " + p.pod.Name),
		"",
	}

	switch {
	case p.loading:
		lines = append(lines, m.st.dim.Render("reading the pod's containers…"))
	case p.err != "":
		lines = append(lines, m.st.err.Render(wrapText(p.err, w)))
	case len(p.containers) == 0:
		lines = append(lines, m.st.dim.Render("this pod reports no containers"))
	default:
		for i, c := range p.containers {
			cursor := "  "
			style := lipgloss.NewStyle()
			if i == p.cur {
				cursor = m.st.accent.Render(m.gl.cursor) + " "
				style = m.st.selected
			}
			// The number is shown because it selects: for two containers,
			// pressing 2 beats arrowing to it.
			label := fmt.Sprintf("%d %s", i+1, c.Name)
			row := cursor + style.Render(label)
			if c.Init {
				// An init container's logs are often the answer for a pod stuck
				// in Init, so it is listed — but it cannot be exec'd into once
				// it has finished, and the label says which kind it is.
				row += m.st.warn.Render("  init")
			}
			if c.Image != "" {
				// Two containers named `app` and `sidecar` say little; their
				// images say what they are.
				row += "  " + m.st.dim.Render(truncate(shortImage(c.Image), w-lipgloss.Width(row)-2))
			}
			lines = append(lines, truncate(row, w))
		}
	}

	lines = append(lines, "",
		m.st.dim.Render("1-9 or enter select  ·  j/k move  ·  esc cancel"))
	return m.st.modal.Render(strings.Join(
		clampModalBody(lines, w, m.modalContentHeight()), "\n"))
}

// shortImage trims a registry path to what identifies the image.
//
// A full ECR path is 60 characters of account id and region before the name
// that matters; the tag is kept because it is often the thing being checked.
func shortImage(image string) string {
	name, tag, hasTag := strings.Cut(image, "@")
	if !hasTag {
		if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
			name, tag, hasTag = image[:i], image[i+1:], true
		}
	} else {
		// A digest is not worth 71 characters of screen; the fact that it is
		// pinned is what matters.
		tag = "digest"
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if hasTag && tag != "" {
		return name + ":" + tag
	}
	return name
}
