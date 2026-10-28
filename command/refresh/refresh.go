package refresh

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/provider"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/template"
	"github.com/co-native/git-work/internal/workfolder"
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
		"re-renders the managed blocks of TICKET.md, README.md and the instructions\n" +
		"file (AGENTS.md, or CLAUDE.md per `config instructions_file`) from\n" +
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
	fs := cli.NewFlagSet("refresh")
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
	wf, err := workfolder.Load()
	if err != nil {
		return err
	}
	st, workDir, cfg, l := wf.State, wf.Dir, wf.Config, wf.Layout

	if !o.noPull {
		var names []string
		for _, r := range st.Repos {
			if wf.Mode() == layout.ModeClone && workfolder.IntegrateInProgress(l, wf.Dir, r.Name) {
				fmt.Fprintf(os.Stderr, "git-work: pull %s: an integrate conflict is waiting in %s; resolve or abort it first (skipping)\n", r.Name, l.IntegrateWorktree(wf.Dir, r.Name))
				continue
			}
			names = append(names, r.Name)
		}
		pullRepos(os.Stdout, os.Stderr, wf.RepoDir, func(name string) string {
			r, _ := st.Repo(name)
			return r.DefaultBranch
		}, names)
	}

	// Re-fetch provider details for TICKET.md. Ticketless work folders have
	// no provider and no TICKET.md, so both steps are skipped.
	ticket := &template.TicketDetails{Title: st.Title}
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
					ticket.Title = d.Title
				}
				ticket.Description = d.Description
			}
		}
	}
	if err := template.WriteWorkFolder(workDir, template.WorkFolderOptions{Mode: wf.Mode(), ReposRoot: l.ReposRoot, File: cfg.Instructions()}, st, ticket); err != nil {
		return err
	}
	fmt.Printf("Refreshed generated files in %s\n", workDir)
	return nil
}

// pullPrimaries is the worktree-mode wrapper kept for the refresh tests: it
// resolves the primary of each named repo and pulls its default branch.
func pullPrimaries(w, errw io.Writer, l layout.Layout, names []string) {
	pullRepos(w, errw, l.RepoMain, func(string) string { return "" }, names)
}

// pullRepos ff-only-pulls each named repo's default branch in the directory
// the resolver returns (a primary or a clone), printing per-repo results to w.
// A failed pull (diverged branch, offline, layout problems) is a warning on
// errw - it never aborts the refresh.
func pullRepos(w, errw io.Writer, dirOf func(string) (string, error), recordedOf func(string) string, names []string) {
	for _, name := range names {
		dir, err := dirOf(name)
		if err == nil {
			var res repo.PullResult
			if recorded := recordedOf(name); recorded != "" {
				res, err = repo.PullBranchFFOnly(dir, recorded)
			} else {
				res, err = repo.PullDefaultBranch(dir)
			}
			if err == nil {
				fmt.Fprintf(w, "Pulled %s: %s\n", name, res)
				continue
			}
		}
		fmt.Fprintf(errw, "git-work: pull %s: %v (skipping)\n", name, err)
	}
}
