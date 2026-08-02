// Package rm implements `git work rm <repo>`: a scoped, single-repo version
// of `done` that tears down one repo's worktree/branches, drops it from the
// work folder's state, and refreshes the README/CLAUDE managed blocks -
// leaving the rest of the work folder (and any other repos) intact.
package rm

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/state"
	"github.com/co-native/git-work/internal/teardown"
	"github.com/co-native/git-work/internal/template"
	"github.com/co-native/git-work/internal/tui"
)

type options struct {
	repo           string
	deleteLocal    bool
	deleteRemote   bool
	force          bool
	noFetch        bool
	nonInteractive bool
}

// Cmd describes `git work rm` for the help system. main.go builds its index
// row from Short and answers `git work help rm` from the same value that
// `git work rm -h` prints.
var Cmd = &cli.Command{
	Name:     "rm",
	Short:    "remove one repo from the current work folder",
	Args:     "<repo>",
	Synopsis: []string{"git work rm <repo> [options]"},
	Long: "Removes one repo from the work folder you are in: the same safety checks and\n" +
		"teardown as done, scoped to <repo>, then the README/CLAUDE managed blocks are\n" +
		"re-rendered from the repos that remain. <repo> must already be listed in\n" +
		".git-work.yaml. Everything else - other repos, TICKET.md, the state file - is\n" +
		"left alone, and rm never removes the folder: removing the last repo leaves an\n" +
		"accurate zero-repo work folder.",
	Examples: []string{
		"git work rm alpha --delete-local --delete-remote",
		"git work rm alpha --non-interactive --force",
	},
	Flags: []cli.Flag{
		{Long: "delete-local", Desc: "delete the repo's local branch (prompted for interactively)"},
		{Long: "delete-remote", Desc: "delete the repo's remote branch (prompted for interactively)"},
		{Long: "force", Desc: "override safety-check problems (uncommitted, unpushed, unmerged)"},
		{Long: "no-fetch", Desc: "skip fetching before the merged check"},
		{Long: "non-interactive", Desc: "skip the prompts and take the flags as given"},
	},
}

// newFlagSet registers rm's flags against o. It is separate from parseFlags so
// the conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.deleteLocal, "delete-local", false, "delete the local branch")
	fs.BoolVar(&o.deleteRemote, "delete-remote", false, "delete the remote branch")
	fs.BoolVar(&o.force, "force", false, "override safety-check problems")
	fs.BoolVar(&o.noFetch, "no-fetch", false, "skip fetching before the merged check")
	fs.BoolVar(&o.nonInteractive, "non-interactive", false, "skip the TUI; use flags")
	return fs
}

// parseFlags parses `git work rm` arguments. <repo> is mandatory and must come
// first: a leading-dash first argument means the operator named no repo.
//
// Help is handled by Run before this is reached, so -h/--help never appears
// here.
func parseFlags(args []string) (*options, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, cli.Usagef("give a <repo> to remove")
	}
	o := &options{repo: args[0]}
	fs := newFlagSet(o)
	if err := fs.Parse(args[1:]); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	if fs.NArg() > 0 {
		return nil, cli.Usagef("unexpected argument %q", fs.Arg(0))
	}
	return o, nil
}

// Run executes `git work rm`; it prints any error to stderr and returns the
// process exit code.
func Run(args []string) int {
	if cli.Wanted(args) {
		return cli.Help(Cmd)
	}
	if err := run(args); err != nil {
		return cli.Fail(err, Cmd)
	}
	return cli.OK
}

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
	l, err := cfg.Layout()
	if err != nil {
		return err
	}

	var r state.Repo
	found := false
	for _, existing := range st.Repos {
		if existing.Name == o.repo {
			r, found = existing, true
			break
		}
	}
	if !found {
		// Naming a repo the folder does not hold is a malformed invocation,
		// not work that failed: the operator picked the wrong name.
		return cli.Usagef("%s: not part of %s", o.repo, workDir)
	}

	// Interactive: ask what to do, exactly like `done`.
	if !o.nonInteractive {
		if !tui.IsInteractive() {
			return tui.ErrNoTTY
		}
		if o.deleteLocal, err = tui.Confirm("Delete local branch?", false); err != nil {
			return err
		}
		if o.deleteRemote, err = tui.Confirm("Delete remote branch?", false); err != nil {
			return err
		}
	}

	to := teardown.Options{
		DeleteLocal:  o.deleteLocal,
		DeleteRemote: o.deleteRemote,
		Force:        o.force,
		NoFetch:      o.noFetch,
	}

	problems := teardown.CheckRepo(to, l, r, workDir)
	if len(problems) > 0 {
		if !o.force {
			return fmt.Errorf("aborting (most of these can be overridden with --force):\n  %s", strings.Join(problems, "\n  "))
		}
		fmt.Fprintf(os.Stderr, "git-work: proceeding despite %d problem(s) (--force)\n", len(problems))
	}

	// TeardownRepo drops r from st and saves only on full success; on
	// failure st (and the on-disk state) is untouched, so stop here without
	// re-aggregating the managed blocks.
	failures := teardown.TeardownRepo(to, l, st, r, workDir)
	if len(failures) > 0 {
		return fmt.Errorf("rm failed:\n  %s", strings.Join(failures, "\n  "))
	}

	// Re-aggregate README.md + CLAUDE.md managed blocks from the remaining
	// repos (duplicates add.go's aggregation - the template.* funcs are
	// exported for exactly this reuse).
	repos := make([]string, len(st.Repos))
	refs := make([]template.RepoRef, len(st.Repos))
	for i, rr := range st.Repos {
		repos[i] = rr.Name
		refs[i] = template.RepoRef{Name: rr.Name, Branch: rr.Branch}
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
			Title:       st.Title,
			Branch:      st.Branch,
			Repos:       refs,
		})); err != nil {
		return err
	}

	fmt.Printf("Removed %s from %s\n", o.repo, workDir)
	return nil
}
