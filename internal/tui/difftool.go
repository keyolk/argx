package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/argx/internal/argocd"
	"github.com/keyolk/argx/internal/config"
)

// Handing the diff to an external tool.
//
// argx's own diff is deliberately plain — it exists so the common case needs no
// setup. But people have a diff tool they already read fluently, and asking
// them to give it up inside one TUI is asking them to read worse. `D` writes
// the two documents to files and runs their tool on them.
//
// The tool gets the *documents*, not argx's rendering of them. A tool that
// computes its own diff can do things argx's cannot — word-level highlighting,
// syntax awareness, folding, its own navigation — and handing it a finished
// patch would throw all of that away.

// diffSides is the pair a diff was computed from.
type diffSides struct {
	// name identifies what is being compared, used for the temp files so the
	// tool's own headers say something recognisable.
	name string
	// live is the cluster's state, desired is what git says. Either may be
	// empty: a resource that exists only in git has no live side, and one
	// only in the cluster has no desired side.
	live, desired string
}

// collectSides gathers the documents behind a diff.
//
// Resources are concatenated with a header apiece, matching what the unified
// view shows, so the external tool sees the same comparison the reader just
// asked for rather than one resource out of several.
func collectSides(items []argocd.ResourceDiff, want map[string]bool, appName string, smartHash bool) *diffSides {
	var live, desired strings.Builder
	n := 0

	// The pairing the unified view applied, applied here too: handing the tool
	// a rotated ConfigMap as two whole documents would be a different
	// comparison from the one on screen.
	var paired map[string]bool
	if smartHash {
		var pairs []hashPair
		pairs, paired = pairHashed(items)
		for _, p := range pairs {
			if want != nil && !want[itemKey(p.desired)] && !want[itemKey(p.live)] {
				continue
			}
			l, d := p.sides()
			if l == d {
				continue
			}
			head := fmt.Sprintf("# %s %s/%s  (%s -> %s)\n",
				groupKind(p.desired.Group, p.desired.Kind), p.desired.Namespace,
				p.base, p.live.Name, p.desired.Name)
			live.WriteString(head)
			desired.WriteString(head)
			live.WriteString(l)
			live.WriteString("\n\n")
			desired.WriteString(d)
			desired.WriteString("\n\n")
			n++
		}
	}

	for _, it := range items {
		if want != nil && !want[diffKey(it.Group, it.Kind, it.Namespace, it.Name)] {
			continue
		}
		if paired[itemKey(it)] {
			continue
		}
		// The same pair the unified view diffs, so the external tool is handed
		// the comparison the reader was already looking at rather than a
		// second, differently-normalized one.
		l, d, ok := diffPair(it)
		if !ok || l == d {
			continue
		}
		head := fmt.Sprintf("# %s %s/%s\n",
			groupKind(it.Group, it.Kind), it.Namespace, it.Name)
		// The header goes on both sides even when one is empty, so the two
		// files stay aligned and a tool that diffs them line by line does not
		// report the header itself as a change.
		live.WriteString(head)
		desired.WriteString(head)
		if l != "" {
			live.WriteString(l)
			live.WriteString("\n")
		}
		if d != "" {
			desired.WriteString(d)
			desired.WriteString("\n")
		}
		live.WriteString("\n")
		desired.WriteString("\n")
		n++
	}
	if n == 0 {
		return nil
	}
	return &diffSides{name: appName, live: live.String(), desired: desired.String()}
}

// diffToolRunner hands the terminal to an external diff tool.
//
// It implements tea.ExecCommand for the same reason the shell does: a tool like
// nvim -d or delta expects the terminal to itself, and rendering it inside the
// TUI would mean writing a terminal emulator.
type diffToolRunner struct {
	argv []string
	dir  string
	// files are removed when the tool exits. A tool that forks and returns
	// immediately would lose them, which is why the config documents that the
	// command must block.
	files []string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// Bubble Tea hands over the real terminal's streams; the tool gets them
// directly, since a diff tool that cannot see a terminal will not use one.
func (d *diffToolRunner) SetStdin(r io.Reader)  { d.stdin = r }
func (d *diffToolRunner) SetStdout(w io.Writer) { d.stdout = w }
func (d *diffToolRunner) SetStderr(w io.Writer) { d.stderr = w }

// Run executes the tool and waits for it.
func (d *diffToolRunner) Run() error {
	defer func() {
		for _, f := range d.files {
			_ = os.Remove(f)
		}
		if d.dir != "" {
			_ = os.Remove(d.dir)
		}
	}()

	cmd := exec.Command(d.argv[0], d.argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = d.stdin, d.stdout, d.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", d.argv[0], err)
	}
	return nil
}

// diffToolCmd writes the two sides to files and runs the configured tool.
func (m *Model) diffToolCmd() tea.Cmd {
	argv := m.cfg.DiffToolCommand()
	if len(argv) == 0 {
		m.setToast("no diff tool configured — set diff_tool in " + config.FilePath())
		return nil
	}
	if m.pagerSides == nil {
		m.setToast("nothing to compare")
		return nil
	}

	dir, err := os.MkdirTemp("", "argx-diff-")
	if err != nil {
		m.showError(fmt.Errorf("create a temp directory: %w", err))
		return nil
	}

	// Named for what they are, not tmp1/tmp2: the tool puts these names in its
	// own headers and window titles, and "live" beside "desired" is the whole
	// orientation the reader needs.
	base := sanitizeFileName(m.pagerSides.name)
	livePath := filepath.Join(dir, base+".live.yaml")
	wantPath := filepath.Join(dir, base+".desired.yaml")

	for path, content := range map[string]string{
		livePath: m.pagerSides.live,
		wantPath: m.pagerSides.desired,
	} {
		// 0600: a manifest can carry a Secret's data, and a world-readable
		// copy in /tmp outlives nothing but is readable by everyone while it
		// is there.
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			m.showError(fmt.Errorf("write %s: %w", path, err))
			return nil
		}
	}

	full := expandDiffArgv(argv, livePath, wantPath)
	runner := &diffToolRunner{
		argv:  full,
		dir:   dir,
		files: []string{livePath, wantPath},
	}
	return tea.Exec(runner, func(err error) tea.Msg {
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "diff tool closed"}
	})
}

// expandDiffArgv substitutes the two paths into the command.
//
// {live} and {desired} are replaced where they appear; a command that names
// neither gets both appended, in that order, which is what every diff tool's
// own CLI expects and means the common case is one word of config.
func expandDiffArgv(argv []string, live, desired string) []string {
	out := make([]string, 0, len(argv)+2)
	used := false
	for _, a := range argv {
		if strings.Contains(a, "{live}") || strings.Contains(a, "{desired}") {
			used = true
		}
		a = strings.ReplaceAll(a, "{live}", live)
		a = strings.ReplaceAll(a, "{desired}", desired)
		out = append(out, a)
	}
	if !used {
		out = append(out, live, desired)
	}
	return out
}

// sanitizeFileName keeps a name usable as a path segment.
//
// An application name comes from a cluster and can contain anything. Separators
// become dashes, and a name that reduces to dots is replaced outright: the
// files go into a directory argx just created, so a traversal has nowhere to go
// — but a file literally named ".." is one the cleanup would then try to
// remove, and a rule that is obviously safe beats one that needs an argument.
func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if strings.Trim(out, ".") == "" {
		return "argx"
	}
	return out
}
