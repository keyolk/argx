package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/term"

	"github.com/keyolk/argx/internal/argocd"
)

// execRunner bridges an Argo CD exec session to the real terminal.
//
// It implements tea.ExecCommand, which is how Bubble Tea hands the terminal
// over: the program leaves the alternate screen and raw mode, this runs to
// completion with the terminal to itself, and the TUI is restored afterwards.
// Rendering a shell inside the TUI instead would mean writing a terminal
// emulator.
type execRunner struct {
	ctx    context.Context
	client *argocd.Client
	req    argocd.ExecRequest

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func newExecRunner(ctx context.Context, client *argocd.Client, req argocd.ExecRequest) *execRunner {
	return &execRunner{ctx: ctx, client: client, req: req}
}

func (e *execRunner) SetStdin(r io.Reader)  { e.stdin = r }
func (e *execRunner) SetStdout(w io.Writer) { e.stdout = w }
func (e *execRunner) SetStderr(w io.Writer) { e.stderr = w }

// Run holds the terminal until the shell exits.
func (e *execRunner) Run() error {
	session, err := e.client.Exec(e.ctx, e.req)
	if err != nil {
		return err
	}
	defer session.Close()

	// Raw mode, so keystrokes reach the container rather than being line-
	// buffered by the local terminal — without it nothing is sent until Enter,
	// and Ctrl-C kills argx instead of the remote process.
	//
	// Bubble Tea has already restored the terminal by the time Run is called,
	// so this takes it back and gives it up again on the way out.
	var restore func()
	if f, ok := e.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		state, err := term.MakeRaw(int(f.Fd()))
		if err != nil {
			return fmt.Errorf("put the terminal in raw mode: %w", err)
		}
		restore = func() { _ = term.Restore(int(f.Fd()), state) }
		defer restore()
	}

	fmt.Fprintf(e.stdout, "connected to %s/%s — exit the shell to return to argx\r\n",
		e.req.Pod, e.req.Container)

	// Two directions, either of which ending ends the session: the shell
	// exiting closes the read side, and the local terminal closing ends the
	// write side.
	var once sync.Once
	done := make(chan error, 2)
	finish := func(err error) { once.Do(func() { done <- err }) }

	go func() {
		_, err := io.Copy(e.stdout, session)
		finish(err)
	}()
	go func() {
		// The container's stdin is closed when this returns, which is what
		// makes Ctrl-D end the shell.
		_, err := io.Copy(session, e.stdin)
		finish(err)
	}()

	err = <-done
	if err == io.EOF {
		return nil
	}
	return err
}
