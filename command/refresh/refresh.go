package refresh

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/provider"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
	"github.com/co-native/git-work/internal/template"
)

type options struct {
	noPull bool
}

// Cmd describes `git work refresh` for the help system. main.go builds its
// index row from Short and answers `git work help refresh` from the same value
// that `git work refresh -h` prints.
var Cmd = &cli.Command{
	Name:     "refresh",
	Short:    "pull the ticket repos' primaries, re-render generated files",
	Synopsis: []string{"git work refresh [options]"},
	Long: "Run from inside a work folder. Fast-forward-pulls each ticket repo's default\n" +
		"branch in its primary clone - explicitly, so a primary left on another branch\n" +
		"still freshens it, and a failed pull is a warning rather than an abort - then\n" +
		"re-renders the managed blocks of TICKET.md, README.md and CLAUDE.md from\n" +
		"current state, re-fetching the provider's title and description (the existing\n" +
		"title is kept if that fetch fails). Ticketless folders skip both.",
	Examples: []string{"git work refresh --no-pull"},
	Flags: []cli.Flag{
		{Long: "no-pull", Desc: "skip pulling the ticket repos' primary clones"},
	},
}

// newFlagSet registers refresh's flags against o. It is separate from
// parseFlags so the conformance test can compare the registered flags with
// Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.noPull, "no-pull", false, "skip pulling the ticket repos' primary clones")
	return fs
}

// parseFlags parses `git work refresh` arguments.
//
// Help is handled by Run before this is reached, so -h/--help never appears
// here.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	fs := newFlagSet(o)
	if err := fs.Parse(args); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	return o, nil
}

// Run executes `git work refresh`; it prints any error to stderr and
// returns the process exit code.
func Run(args []string) int {
	if cli.Wanted(args) {
		return cli.Help(Cmd)
	}
	if err := run(args); err != nil {
		return cli.Fail(err, Cmd)
	}
	return cli.OK
}

// run pulls the ticket repos' primaries (unless --no-pull) and re-renders
// the managed blocks of the current work folder's files.
func run(args []string) error {
	o, err := parseFlags(args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	st, workDir, err := state.FindUp(cwd)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	repos := make([]string, len(st.Repos))
	refs := make([]template.RepoRef, len(st.Repos))
	for i, r := range st.Repos {
		repos[i] = r.Name
		refs[i] = template.RepoRef{Name: r.Name, Branch: r.Branch}
	}

	l, err := cfg.Layout()
	if err != nil {
		return err
	}
	if !o.noPull {
		pullPrimaries(os.Stdout, os.Stderr, l, repos)
	}

	// Re-fetch provider details for TICKET.md. Ticketless work folders have
	// no provider and no TICKET.md, so both steps are skipped.
	title, description := st.Title, ""
	if !st.NoTicket {
		if err := provider.Validate(cfg.Providers); err != nil {
			return err
		}
		match, err := provider.Resolve(cfg.Providers, st.TicketID)
		if err != nil {
			return err
		}
		if match.Provider != nil {
			if d, err := provider.Fetch(match, st.TicketID); err != nil {
				fmt.Fprintf(os.Stderr, "git-work: %v (keeping existing title)\n", err)
			} else {
				if d.Title != "" {
					title = d.Title
				}
				description = d.Description
			}
		}
		if err := template.WriteManaged(filepath.Join(workDir, "TICKET.md"),
			template.Ticket(st.TicketID, title, description)); err != nil {
			return err
		}
	}

	if err := template.WriteManaged(filepath.Join(workDir, "README.md"),
		template.Readme(st.DisplayName(), st.Branch, repos)); err != nil {
		return err
	}
	if err := template.WriteManaged(filepath.Join(workDir, "CLAUDE.md"),
		template.AggregateClaude(template.ClaudeInput{
			ReposRoot:   l.ReposRoot,
			OverlayPath: config.OverlayPath(),
			TicketID:    st.TicketID,
			Title:       title,
			Branch:      st.Branch,
			Repos:       refs,
		})); err != nil {
		return err
	}
	fmt.Printf("Refreshed generated files in %s\n", workDir)
	return nil
}

// pullPrimaries ff-only-pulls each named repo's default branch in its
// primary clone (explicitly, so a primary left on another branch still
// freshens the default branch), printing per-repo results to w. A failed
// pull (diverged branch, offline, layout problems) is a warning on errw -
// it never aborts the refresh.
func pullPrimaries(w, errw io.Writer, l layout.Layout, names []string) {
	for _, name := range names {
		main, err := l.RepoMain(name)
		if err != nil {
			fmt.Fprintf(errw, "git-work: pull %s: %v (skipping)\n", name, err)
			continue
		}
		def, err := repo.DefaultBranchName(main)
		if err != nil {
			fmt.Fprintf(errw, "git-work: pull %s: %v (skipping)\n", name, err)
			continue
		}
		res, err := repo.PullBranchFFOnly(main, def)
		if err != nil {
			fmt.Fprintf(errw, "git-work: pull %s: %v (skipping)\n", name, err)
			continue
		}
		fmt.Fprintf(w, "Pulled %s: %s\n", name, res)
	}
}
