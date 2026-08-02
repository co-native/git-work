// Package rebase implements `git work rebase`: pull the in-scope ticket
// repos' primary clones (ff-only), then rebase each repo's work branch onto
// its default branch.
package rebase

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

type options struct {
	repo   string
	all    bool
	noPull bool
	dryRun bool
}

// Cmd describes `git work rebase` for the help system. main.go builds its
// index row from Short and answers `git work help rebase` from the same value
// that `git work rebase -h` prints.
var Cmd = &cli.Command{
	Name:  "rebase",
	Short: "pull the repos' primaries, rebase work branches onto their defaults",
	Args:  "<repo> | --all",
	Synopsis: []string{
		"git work rebase <repo> [options]",
		"git work rebase -a|--all [options]",
	},
	Long: "Pulls each in-scope repo's primary clone ff-only, then rebases the branch\n" +
		"checked out in its worktree - not the one recorded in state - onto that repo's\n" +
		"default branch. Scope is mandatory: bare `rebase` is a usage error, so no run\n" +
		"silently rewrites history in repos you did not name. Repos are independent -\n" +
		"problems are collected and reported at the end, and a conflicted rebase is left\n" +
		"in progress for `git rebase --continue` or `git rebase --abort`.",
	Flags: []cli.Flag{
		{Short: "a", Long: "all", Desc: "rebase every repo in the work folder"},
		{Long: "no-pull", Desc: "skip pulling the primaries; rebase onto the local default branch"},
		{Short: "n", Long: "dry-run", Desc: "report what would be rebased without rebasing"},
	},
}

// newFlagSet registers rebase's flags against o. It is separate from parseFlags
// so the conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("rebase", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.all, "all", false, "rebase every repo in the work folder")
	fs.BoolVar(&o.all, "a", false, "shorthand for --all")
	fs.BoolVar(&o.noPull, "no-pull", false, "skip pulling the ticket repos' primary clones")
	fs.BoolVar(&o.dryRun, "dry-run", false, "report what would be rebased without rebasing")
	fs.BoolVar(&o.dryRun, "n", false, "shorthand for --dry-run")
	return fs
}

// parseFlags parses `git work rebase` arguments. Every flag is boolean, so the
// single positional <repo> may appear anywhere on the line; leading-dash
// tokens are partitioned out as flags (this deliberately does NOT reject a
// leading `-` first arg, so `rebase --all` parses). Bare `rebase` is a usage
// error rather than an implicit --all: scope is always explicit, the same rule
// push and integrate follow.
//
// Help is handled by Run before this is reached, so -h/--help never appears
// here.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	fs := newFlagSet(o)

	var flagArgs, positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
		} else {
			positional = append(positional, a)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	if len(positional) > 1 {
		return nil, cli.Usagef("give at most one <repo>")
	}
	switch {
	case o.all && len(positional) == 1:
		return nil, cli.Usagef("give a <repo> or --all, not both")
	case !o.all && len(positional) == 0:
		return nil, cli.Usagef("give a <repo> or --all")
	}
	if len(positional) == 1 {
		o.repo = positional[0]
	}
	return o, nil
}

// Run executes `git work rebase`; it prints any error to stderr and returns
// the process exit code.
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
	return rebaseAll(o, os.Stdout, os.Stderr, l, st, workDir)
}

// rebaseAll processes each in-scope repo independently: pull its primary
// (ff-only, unless --no-pull), then rebase the work branch checked out in its
// worktree onto the repo's default branch. One repo's problem never stops the
// others; problems are collected and reported at the end (nonzero exit). A
// dirty worktree is skipped and counted as a problem. On conflict the rebase
// is left in progress for normal `git rebase --continue/--abort` resolution
// and counted as a problem. A failed pull is only a warning - the rebase
// still runs against the local default branch.
func rebaseAll(o *options, w, errw io.Writer, l layout.Layout, st *state.State, workDir string) error {
	scope, err := scopeRepos(o, st)
	if err != nil {
		return err
	}
	var problems []string
	problem := func(format string, a ...any) {
		problems = append(problems, fmt.Sprintf(format, a...))
	}
	for _, r := range scope {
		wt := filepath.Join(workDir, r.Name)
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			fmt.Fprintf(errw, "git-work: %s: worktree missing; skipping\n", r.Name)
			continue
		}
		// Checked before the pull so a repo we are going to refuse costs no
		// network round trip. This is also where a conflicted rebase left in
		// progress by an earlier run gets named as such: HEAD is detached
		// while a rebase replays, so without this it would surface as
		// unexplained branch drift.
		if msg, err := repo.WorktreeBranchProblem(wt, r.Branch); err != nil {
			problem("%s: could not check the worktree's branch: %v", r.Name, err)
			continue
		} else if msg != "" {
			problem("%s: %s", r.Name, msg)
			continue
		}
		main, err := l.RepoMain(r.Name)
		if err != nil {
			problem("%s: %v", r.Name, err)
			continue
		}
		target, err := repo.DefaultBranchName(main)
		if err != nil {
			problem("%s: %v", r.Name, err)
			continue
		}
		// Pull the default branch explicitly: a primary left on another
		// branch would otherwise pull that branch and leave the rebase
		// target stale forever.
		if !o.noPull {
			if res, err := repo.PullBranchFFOnly(main, target); err != nil {
				fmt.Fprintf(errw, "git-work: pull %s: %v (rebasing onto the local default branch)\n", r.Name, err)
			} else {
				fmt.Fprintf(w, "Pulled %s: %s\n", r.Name, res)
			}
		}
		if o.dryRun {
			reportRepo(w, r.Name, main, wt, target, problem)
			continue
		}
		status, err := repo.RebaseOnto(wt, target)
		if err != nil {
			problem("%s: rebase onto %s: %v", r.Name, target, err)
			continue
		}
		switch status {
		case repo.RebaseDirty:
			problem("%s: uncommitted changes; rebase skipped", r.Name)
		case repo.RebaseConflict:
			problem("%s: rebase onto %s hit conflicts; left in progress - run `git rebase --continue` or `git rebase --abort` in %s", r.Name, target, wt)
		default:
			fmt.Fprintf(w, "Rebased %s onto %s\n", r.Name, target)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("rebase finished with %d problem(s):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

// scopeRepos resolves the repos a run operates on: every repo in the work
// folder with --all, or the single named one. parseFlags guarantees exactly
// one of the two is set.
func scopeRepos(o *options, st *state.State) ([]state.Repo, error) {
	if o.all {
		return st.Repos, nil
	}
	for _, r := range st.Repos {
		if r.Name == o.repo {
			return []state.Repo{r}, nil
		}
	}
	// Naming a repo the work folder does not hold is a malformed invocation,
	// not a failed rebase: nothing was attempted.
	return nil, cli.Usagef("repo %q is not part of %s", o.repo, st.DisplayName())
}

// reportRepo prints what a real run would do to one repo, touching nothing -
// the --dry-run body. The pull has already run (unless --no-pull), so the
// target is exactly as fresh as it would be in a real run; this is integrate's
// model, where -n performs the identical pre-flight so the report matches.
// A dirty worktree is reported as a problem here too, so -n predicts the real
// run's nonzero exit rather than quietly reporting a rebase that would be
// skipped.
func reportRepo(w io.Writer, name, main, wt, target string, problem func(string, ...any)) {
	dirty, err := repo.HasUncommittedChanges(wt)
	if err != nil {
		problem("%s: %v", name, err)
		return
	}
	if dirty {
		problem("%s: uncommitted changes; rebase would be skipped", name)
		return
	}
	// Read the branch from the worktree rather than from state: a real run
	// rebases whatever the worktree has checked out, so that is the honest
	// source for what -n reports. The drift guard upstream has already
	// established that the two agree.
	branch, err := repo.CurrentBranch(wt)
	if err != nil {
		problem("%s: %v", name, err)
		return
	}
	// Ask from the worktree so HEAD-relative refs resolve against it rather
	// than against the primary's checkout.
	commits, err := repo.CommitsNotIn(wt, branch, target)
	if err != nil {
		problem("%s: could not compute the commits to replay: %v", name, err)
		return
	}
	if len(commits) > 0 {
		fmt.Fprintf(w, "%s: would rebase %d commit(s) from %s onto %s\n", name, len(commits), branch, target)
		for _, c := range commits {
			fmt.Fprintf(w, "  %s %s\n", shortSHA(c.SHA), c.Subject)
		}
		return
	}
	// Nothing to replay: rebase leaves the branch sitting at the target, so
	// the only question left is whether that moves it at all.
	current, err := repo.IsMergedInto(wt, target, branch)
	if err != nil {
		problem("%s: %v", name, err)
		return
	}
	if current {
		fmt.Fprintf(w, "%s: %s is already up to date with %s\n", name, branch, target)
	} else {
		fmt.Fprintf(w, "%s: no commits to replay; %s would fast-forward to %s\n", name, branch, target)
	}
}

// shortSHA abbreviates a commit id for reporting, leaving short ids untouched.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
