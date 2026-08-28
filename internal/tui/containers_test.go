package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
)

func container(name, image string) argocd.Container {
	return argocd.Container{Name: name, Image: image}
}

func initContainer(name, image string) argocd.Container {
	return argocd.Container{Name: name, Image: image, Init: true}
}

// podModel puts a model on the resource tree with one pod under the cursor.
func podModel(t *testing.T) *Model {
	t.Helper()
	m := appModel(t, nil)
	tree := &argocd.Tree{Nodes: []argocd.Node{
		{ResourceRef: argocd.ResourceRef{
			UID: "p1", Kind: "Pod", Name: "web-abc12", Namespace: "web",
		}},
	}}
	m.Update(treeMsg{id: m.treeID, app: m.app, rows: tree.Flatten("argocd", "test")})
	m.screen, m.tab = screenApp, tabResources
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	return m
}

// One container is not a choice, so it is not offered as one — the modal closes
// and the action runs.
func TestSingleContainerSkipsThePicker(t *testing.T) {
	m := podModel(t)
	press(t, m, "l")
	if m.overlay != overlayContainer {
		t.Fatalf("l should open the picker while it loads, overlay = %v", m.overlay)
	}

	m.Update(containersMsg{containers: []argocd.Container{container("app", "app:1")}})

	if m.overlay != overlayNone {
		t.Errorf("a single container should not be presented as a choice, overlay = %v", m.overlay)
	}
	if m.screen != screenLogs {
		t.Errorf("screen = %v, want the logs view", m.screen)
	}
}

// A sidecar is exactly where the modal earns its place: reading the wrong
// container's logs is a silent wrong answer, not an error.
func TestMultipleContainersAsk(t *testing.T) {
	m := podModel(t)
	press(t, m, "l")
	m.Update(containersMsg{containers: []argocd.Container{
		container("fluent-bit", "registry.example.com/org/fluent-bit:3.2.3"),
		container("app", "registry.example.com/org/app:1.0.15-distroless"),
	}})

	if m.overlay != overlayContainer {
		t.Fatalf("two containers should be presented as a choice, overlay = %v", m.overlay)
	}
	out := m.View()
	for _, want := range []string{"fluent-bit", "app", "fluent-bit:3.2.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("the picker is missing %q:\n%s", want, out)
		}
	}
	// The registry path is 40 characters of noise before the name that matters.
	if strings.Contains(out, "registry.example.com") {
		t.Errorf("the full registry path should be trimmed:\n%s", out)
	}
}

// The number shown beside each container selects it, which beats arrowing for
// the two-container case this modal exists for.
func TestDigitSelectsAContainer(t *testing.T) {
	m := podModel(t)
	press(t, m, "l")
	m.Update(containersMsg{containers: []argocd.Container{
		container("fluent-bit", "fluent-bit:3"),
		container("app", "app:1"),
	}})

	press(t, m, "2")
	if m.overlay != overlayNone {
		t.Fatalf("2 should select the second container, overlay = %v", m.overlay)
	}
	if !strings.Contains(m.pagerTitle, "app") {
		t.Errorf("the logs title is %q, want the chosen container", m.pagerTitle)
	}

	// A digit past the end must do nothing rather than select something else.
	m2 := podModel(t)
	press(t, m2, "l")
	m2.Update(containersMsg{containers: []argocd.Container{
		container("a", "a:1"), container("b", "b:1"),
	}})
	press(t, m2, "9")
	if m2.overlay != overlayContainer {
		t.Error("a digit past the end should not select anything")
	}
}

// Init containers have logs — a pod stuck in Init is exactly when someone looks
// for them — but nothing to exec into once they have finished.
func TestInitContainersAreListedButNotExecutable(t *testing.T) {
	m := podModel(t)
	press(t, m, "l")
	m.Update(containersMsg{containers: []argocd.Container{
		initContainer("wait-for-db", "busybox:1"),
		container("app", "app:1"),
	}})

	out := m.View()
	if !strings.Contains(out, "wait-for-db") {
		t.Errorf("an init container should be listed for logs:\n%s", out)
	}
	if !strings.Contains(out, "init") {
		t.Errorf("an init container should be labelled as one:\n%s", out)
	}

	// Logs from it are fine.
	press(t, m, "1")
	if m.screen != screenLogs {
		t.Errorf("logs from an init container should work, screen = %v", m.screen)
	}

	// A shell in it is not.
	m2 := podModel(t)
	press(t, m2, "e")
	m2.Update(containersMsg{containers: []argocd.Container{
		initContainer("wait-for-db", "busybox:1"),
		container("app", "app:1"),
	}})
	cmd := press(t, m2, "1")
	if cmd != nil {
		t.Error("exec into a finished init container should not be attempted")
	}
	if !strings.Contains(m2.toast, "init container") {
		t.Errorf("the reason should be given: toast = %q", m2.toast)
	}
}

// Logs and exec are pod-only, and saying so beats a server error.
func TestNonPodsAreRejectedEarly(t *testing.T) {
	m := appModel(t, nil)
	tree := &argocd.Tree{Nodes: []argocd.Node{
		{ResourceRef: argocd.ResourceRef{
			UID: "s1", Kind: "Service", Name: "web", Namespace: "web",
		}},
	}}
	m.Update(treeMsg{id: m.treeID, app: m.app, rows: tree.Flatten("argocd", "test")})
	m.screen, m.tab = screenApp, tabResources

	for _, k := range []string{"l", "e"} {
		press(t, m, k)
		if m.overlay != overlayNone {
			t.Errorf("%q on a Service opened %v", k, m.overlay)
			m.overlay = overlayNone
		}
		if !strings.Contains(m.toast, "only available for pods") {
			t.Errorf("%q should say why: toast = %q", k, m.toast)
		}
	}
}

// A failed fetch is shown in the modal rather than leaving it spinning.
func TestContainerFetchFailureIsShown(t *testing.T) {
	m := podModel(t)
	press(t, m, "l")
	m.Update(containersMsg{err: errString("forbidden")})

	if m.overlay != overlayContainer {
		t.Fatalf("the modal should stay open to show the error, overlay = %v", m.overlay)
	}
	if !strings.Contains(m.View(), "forbidden") {
		t.Errorf("the error is missing:\n%s", m.View())
	}
}

// Esc closes the picker without acting.
func TestPickerEscapes(t *testing.T) {
	m := podModel(t)
	press(t, m, "l")
	m.Update(containersMsg{containers: []argocd.Container{
		container("a", "a:1"), container("b", "b:1"),
	}})

	press(t, m, "esc")
	if m.overlay != overlayNone {
		t.Errorf("esc should close the picker, overlay = %v", m.overlay)
	}
	if m.screen == screenLogs {
		t.Error("esc should not have opened the logs")
	}
}

// A registry path is mostly account id and region; the name and tag are what
// identify the image.
func TestShortImage(t *testing.T) {
	tests := []struct{ in, want string }{
		{"602401143452.dkr.ecr.ap-northeast-2.amazonaws.com/org/fluent-bit:3.2.3", "fluent-bit:3.2.3"},
		{"nginx:1.25", "nginx:1.25"},
		{"nginx", "nginx"},
		{"registry.example.com/team/app@sha256:abc123", "app:digest"},
		// A port in the registry host must not be read as the tag.
		{"registry:5000/team/app", "app"},
	}
	for _, tt := range tests {
		if got := shortImage(tt.in); got != tt.want {
			t.Errorf("shortImage(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The picker is a modal like any other: it must fit, in every icon set.
func TestPickerFitsTheTerminal(t *testing.T) {
	for _, env := range []string{"unicode", "nerd", "ascii"} {
		for _, size := range [][2]int{{140, 24}, {100, 20}, {80, 24}, {60, 14}} {
			w, h := size[0], size[1]
			t.Setenv("ARGX_ICONS", env)
			t.Setenv("NO_COLOR", "1")

			m := podModel(t)
			m.gl = newGlyphs()
			m.st = newStyles()
			m.st.initContexts(len(m.fleet.Names()))
			press(t, m, "l")
			m.Update(containersMsg{containers: []argocd.Container{
				container("a-container-with-a-fairly-long-name", "602401143452.dkr.ecr.ap-northeast-2.amazonaws.com/org/some-long-image-name:1.2.3"),
				initContainer("wait-for-everything-to-be-ready", "busybox:1.36"),
				container("app", "app:1"),
			}})
			m.Update(tea.WindowSizeMsg{Width: w, Height: h})

			out := m.View()
			if got := strings.Count(out, "\n") + 1; got != h {
				t.Errorf("%s %dx%d rendered %d lines, want %d", env, w, h, got, h)
			}
			for i, line := range strings.Split(out, "\n") {
				if got := lipglossWidth(line); got > w {
					t.Errorf("%s %dx%d line %d is %d cells, want at most %d:\n%q",
						env, w, h, i, got, w, line)
				}
			}
		}
	}
}
