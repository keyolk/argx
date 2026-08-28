// Package cli wires argx's commands.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/keyolk/argx/internal/argocd"
	"github.com/keyolk/argx/internal/config"
	"github.com/keyolk/argx/internal/tui"
)

var (
	// flagContexts is repeatable: argx is normally pointed at several Argo CD
	// servers at once, and a single --context is the special case.
	flagContexts []string
	flagJSON     bool
)

// Execute runs the root command.
func Execute(version string) error {
	root := &cobra.Command{
		Use:   "argx",
		Short: "A terminal UI for Argo CD",
		Long: "argx browses Argo CD applications, their resource trees, diffs, and logs.\n\n" +
			"It reuses whatever `argocd login` already established — no separate\n" +
			"credentials and no argocd binary at runtime.",
		Version: version,
		// Both are silenced because main prints the error itself; leaving them
		// on makes cobra print it a second time, prefixed differently.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runTUI,
	}
	root.PersistentFlags().StringSliceVar(&flagContexts, "context", nil,
		"argocd context to use; repeatable (default: every logged-in context)")

	root.AddCommand(listCmd(), contextsCmd())
	return root.Execute()
}

// signalContext cancels on SIGINT/SIGTERM so in-flight API calls stop when the
// user quits rather than holding the process open.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func newFleet(ctx context.Context) (*argocd.Fleet, *config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	ctxs, err := cfg.Resolve(flagContexts)
	if err != nil {
		return nil, nil, err
	}
	// Tokens are fetched here, once, before anything is drawn: a `pass` entry
	// may prompt a GPG agent, and a prompt appearing behind the alternate
	// screen is a hang with no explanation.
	ctxs, err = config.ResolveTokens(ctx, ctxs)
	if err != nil {
		return nil, nil, err
	}
	return argocd.NewFleet(ctxs), cfg, nil
}

func runTUI(cmd *cobra.Command, _ []string) error {
	ctx, cancel := signalContext()
	defer cancel()

	fleet, cfg, err := newFleet(ctx)
	if err != nil {
		return err
	}

	m := tui.New(ctx, fleet, cfg)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))

	// A panic inside the program would otherwise leave the terminal in raw mode
	// and on the alternate screen; kill the program so its restore runs before
	// the panic propagates.
	defer func() {
		if r := recover(); r != nil {
			p.Kill()
			panic(r)
		}
	}()

	_, err = p.Run()
	return err
}

// listCmd is the --no-tui path: a plain listing for scripts, pipes, and screen
// readers.
func listCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List applications without the TUI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := signalContext()
			defer cancel()

			fleet, _, err := newFleet(ctx)
			if err != nil {
				return err
			}

			apps, ferrs := fleet.ListApplications(ctx, nil)
			// Per-server failures go to stderr so a partial result still pipes
			// cleanly, but is never silently passed off as the whole fleet.
			for _, e := range ferrs {
				fmt.Fprintln(os.Stderr, "argx: "+e.Error())
			}
			if len(apps) == 0 && len(ferrs) > 0 {
				return fmt.Errorf("no server answered")
			}

			if flagJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(apps)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CONTEXT\tNAME\tPROJECT\tSYNC\tHEALTH\tDESTINATION\tURL")
			for i := range apps {
				a := &apps[i]
				dst := a.Spec.Destination.Cluster()
				if ns := a.Spec.Destination.Namespace; ns != "" {
					dst += "/" + ns
				}
				url, _ := fleet.URL(a)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					a.Context, a.Name(), a.Spec.Project,
					a.Status.Sync.Status, a.Status.Health.Status, dst, url)
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&flagJSON, "json", false, "emit raw JSON")
	return c
}

func contextsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "contexts",
		Aliases: []string{"ctx"},
		Short:   "List argocd contexts argx can use",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CURRENT\tNAME\tSERVER\tCREDENTIAL")
			for _, c := range cfg.Contexts {
				cur := ""
				if c.Name == cfg.Current {
					cur = "*"
				}
				// The source is shown, not the token: knowing a credential
				// comes from `pass` rather than a stale login is the thing
				// worth seeing, and no secret command runs to render it.
				auth := c.TokenSource()
				if auth == "" {
					auth = "none — run `argocd login`"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cur, c.Name, c.Server, auth)
			}
			fmt.Fprintln(w)
			return w.Flush()
		},
	}
}
