package integrate

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
	repo    string
	all     bool
	multi   bool
	push    bool
	noFetch bool
	dryRun  bool
}

// Cmd describes `git work integrate` for the help system. main.go builds its index
// row from Short and answers `git work help integrate` from the same value that
// `git work integrate -h` prints.
var Cmd = &cli.Command{
	Name:  "integrate",
	Short: "move a work branch's un-integrated commits onto its default branch",
	Args:  "<repo> | --all",
	Synopsis: []string{
		"git work integrate <repo> [options]",
		"git work integrate -a|--all [options]",
	},
	Long: "Moves a work branch's un-integrated commits onto its repo's local default branch -\n" +
		"by fast-forward when the default branch is an ancestor of the work branch, so\n" +
		"the commits keep their SHAs, and by cherry-pick otherwise. Pre-flight is atomic\n" +
		"across the whole scope: nothing is transferred at all if any repo is dirty, is\n" +
		"off its default branch, or has 2+ un-integrated commits without --multi (which gates\n" +
		"both mechanisms). Scope is mandatory.\n\n" +
		"A repo whose integration route is `pr` is not integrated locally: naming it is an\n" +
		"error, and --all skips it with a note. Set the route per repo or as a house rule\n" +
		"(`git work config set repos.<name>.integration local|pr`).",
	Flags: []cli.Flag{
		{Short: "a", Long: "all", Desc: "integrate every repo in the work folder"},
		{Long: "multi", Desc: "allow integrating a repo with 2+ un-integrated commits"},
		{Long: "push", Desc: "push the default branch to origin after integrating"},
		{Long: "no-fetch", Desc: "skip fetching before pre-flight"},
		{Short: "n", Long: "dry-run", Desc: "run the full pre-flight but skip cherry-picks and pushes"},
	},
}

// newFlagSet registers integrate's flags against o. It is separate from parseFlags
// so the conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("integrate", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.all, "all", false, "integrate every repo in the work folder")
	fs.BoolVar(&o.all, "a", false, "shorthand for --all")
	fs.BoolVar(&o.multi, "multi", false, "allow integrating a repo with 2+ un-integrated commits")
	fs.BoolVar(&o.push, "push", false, "push the default branch to origin after integrating")
	fs.BoolVar(&o.noFetch, "no-fetch", false, "skip fetching before pre-flight")
	fs.BoolVar(&o.dryRun, "dry-run", false, "run the full pre-flight but skip cherry-picks and pushes")
	fs.BoolVar(&o.dryRun, "n", false, "shorthand for --dry-run")
	return fs
}

// parseFlags parses `git work integrate` arguments. Every flag is boolean, so the
// single positional <repo> may appear anywhere on the line; leading-dash
// tokens are partitioned out as flags (this deliberately does NOT reject a
// leading `-` first arg, so `integrate --all` parses).
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

// Run executes `git work integrate`; it prints any error to stderr and returns the
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

// target is one in-scope repo that passed pre-flight, carrying the values the
// transfer phase reuses so nothing is recomputed between the two phases.
type target struct {
	r            state.Repo
	main         string
	defBranch    string
	unintegrated []repo.Commit
	ff           bool // transfer by fast-forward rather than cherry-pick
}

// mechanism names how a repo's commits will be transferred, for the report.
// Both modes print it: which one runs changes whether the commits keep their
// SHAs, which is what the operator needs to know.
func mechanism(ff bool) string {
	if ff {
		return "fast-forward"
	}
	return "cherry-pick"
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
	if err := cfg.Validate(); err != nil {
		return err
	}
	l, err := cfg.Layout()
	if err != nil {
		return err
	}
	return integrate(o, cfg, l, st, workDir)
}

// integrate runs the pre-flight over the whole in-scope set, then (only if it all
// passes) the cherry-pick transfer. Split out from run so tests drive it with
// a constructed config/layout/state instead of the global config.
func integrate(o *options, cfg *config.Config, l layout.Layout, st *state.State, workDir string) error {
	// Resolve the in-scope repos.
	var scope []state.Repo
	if o.all {
		scope = st.Repos
	} else {
		for _, r := range st.Repos {
			if r.Name == o.repo {
				scope = append(scope, r)
				break
			}
		}
		if len(scope) == 0 {
			// Naming a repo the work folder does not hold is a malformed
			// invocation, not a failed integration: nothing was attempted.
			return cli.Usagef("repo %q is not part of %s", o.repo, st.DisplayName())
		}
	}

	// Pre-flight: fully atomic. Collect violations across the whole set; a
	// single violation aborts before any cherry-pick happens. Repos with
	// nothing to integrate (missing branch/worktree) are skipped, not violations.
	var targets []target
	var violations []string
	violate := func(format string, a ...any) {
		violations = append(violations, fmt.Sprintf(format, a...))
	}
	for _, r := range scope {
		// A PR-only repo is a standing policy, not an error state, so how it
		// is reported depends on how the repo entered scope. Under --all,
		// declining it is the appropriate thing to do and the run still
		// succeeds; naming it means asking for something policy forbids, and
		// answering that with a silent exit 0 would be the worst outcome.
		// Both paths run before any git work, and both are shared with
		// --dry-run, so -n predicts the real run's exit code.
		if cfg.Integration(r.Name) == config.IntegrationPR {
			if o.all {
				fmt.Fprintf(os.Stderr, "git-work: %s: integrates through a pull request; skipping\n", r.Name)
				continue
			}
			violate("%s: integrates through a pull request (integration: pr); "+
				"push the branch and open one, or change the route with "+
				"`git work config set repos.%s.integration local`", r.Name, r.Name)
			continue
		}

		main, err := l.RepoMain(r.Name)
		if err != nil {
			violate("%s: %v", r.Name, err)
			continue
		}

		// A gone worktree or a branch that no longer exists locally means
		// there is nothing to integrate: skip with a note rather than erroring.
		wt := filepath.Join(workDir, r.Name)
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "git-work: %s: worktree already removed; nothing to integrate\n", r.Name)
			continue
		}

		// The cherry-picks take their commits from r.Branch, so a worktree on
		// a different branch means the pre-flight would measure one branch
		// while the operator is working on another. A pre-flight violation,
		// not a skip: the whole run aborts rather than integrating the wrong branch.
		if msg, err := repo.WorktreeBranchProblem(wt, r.Branch); err != nil {
			violate("%s: could not check the worktree's branch: %v", r.Name, err)
			continue
		} else if msg != "" {
			violate("%s: %s", r.Name, msg)
			continue
		}

		if !o.noFetch {
			if err := repo.Fetch(main); err != nil {
				fmt.Fprintf(os.Stderr, "git-work: %s: fetch failed: %v (continuing with local refs)\n", r.Name, err)
			}
		}

		exists, err := repo.LocalBranchExists(main, r.Branch)
		if err != nil {
			violate("%s: could not check branch %s: %v", r.Name, r.Branch, err)
			continue
		}
		if !exists {
			fmt.Fprintf(os.Stderr, "git-work: %s: branch %s does not exist locally; nothing to integrate\n", r.Name, r.Branch)
			continue
		}

		defBranch, err := repo.DefaultBranchName(main)
		if err != nil {
			violate("%s: could not determine default branch: %v", r.Name, err)
			continue
		}

		// The primary must be clean and sitting on its default branch so the
		// cherry-picks integrate where the pre-flight measured them.
		if dirty, err := repo.HasUncommittedChanges(main); err != nil {
			violate("%s: could not check for uncommitted changes: %v", r.Name, err)
			continue
		} else if dirty {
			violate("%s: primary clone %s has uncommitted changes", r.Name, main)
			continue
		}
		cur, err := repo.CurrentBranch(main)
		if err != nil {
			violate("%s: could not determine checked-out branch: %v", r.Name, err)
			continue
		}
		if cur != defBranch {
			violate("%s: primary clone %s is on %s, not the default branch %s", r.Name, main, cur, defBranch)
			continue
		}

		// Fast-forward the local default branch to origin - a safe, always
		// desirable mutation; a non-fast-forward is a violation.
		if _, err := repo.PullBranchFFOnly(main, defBranch); err != nil {
			violate("%s: could not fast-forward %s to origin/%s: %v", r.Name, defBranch, defBranch, err)
			continue
		}

		// Measure against the LOCAL default branch (the cherry-pick target),
		// not origin/main: integrating without --push leaves local main ahead, and
		// measuring against origin would re-list already-integrated commits.
		unintegrated, err := repo.CommitsNotIn(main, r.Branch, defBranch)
		if err != nil {
			violate("%s: could not compute un-integrated commits: %v", r.Name, err)
			continue
		}
		// Prefer a fast-forward: possible exactly when the default branch is
		// an ancestor of the work branch, i.e. nothing integrated on the default
		// branch since the work branch diverged. The check comes AFTER the
		// pre-flight fast-forwarded the default branch to origin, so it asks
		// about the same tip a real transfer would move.
		//
		// It is worth preferring because a cherry-pick rewrites the commits:
		// an already-pushed work branch ends up holding different objects
		// than the default branch, which is exactly the case `done`'s
		// patch-equivalence fallback exists to paper over. A fast-forward
		// keeps the SHAs, so the two genuinely match.
		//
		// Computed before the --multi guard so a dry run of a repo that trips
		// the guard still reports which mechanism it would have used.
		ff := false
		if len(unintegrated) > 0 {
			if ff, err = repo.IsMergedInto(main, defBranch, r.Branch); err != nil {
				violate("%s: could not check whether %s can fast-forward to %s: %v", r.Name, defBranch, r.Branch, err)
				continue
			}
		}

		// The --multi guard applies to both mechanisms. A fast-forward carries
		// no per-commit risk, but the guard is about intent - integrating commits
		// you had forgotten about is the same surprise either way - so the
		// rule stays one rule.
		if len(unintegrated) >= 2 && !o.multi {
			violate("%s: %d un-integrated commits; pass --multi to integrate more than one", r.Name, len(unintegrated))
			// A dry run still reports what's out there: the commits are
			// computed, so keep the target for the listing. Violations stay
			// non-empty, so the transfer can never reach this repo.
			if o.dryRun {
				targets = append(targets, target{r: r, main: main, defBranch: defBranch, unintegrated: unintegrated, ff: ff})
			}
			continue
		}
		targets = append(targets, target{r: r, main: main, defBranch: defBranch, unintegrated: unintegrated, ff: ff})
	}

	if o.dryRun {
		for _, t := range targets {
			if len(t.unintegrated) == 0 {
				fmt.Printf("%s: already up to date\n", t.r.Name)
				continue
			}
			fmt.Printf("%s: would integrate %d onto %s (%s)\n", t.r.Name, len(t.unintegrated), t.defBranch, mechanism(t.ff))
			printCommits(t.unintegrated)
			if o.push {
				fmt.Printf("%s: would push %s to origin\n", t.r.Name, t.defBranch)
			}
		}
	}

	if len(violations) > 0 {
		return fmt.Errorf("aborting; nothing integrated:\n  %s", strings.Join(violations, "\n  "))
	}
	if o.dryRun {
		return nil
	}

	// Transfer: only now, with the whole set proven safe, cherry-pick.
	for _, t := range targets {
		if len(t.unintegrated) == 0 {
			fmt.Printf("%s: already up to date\n", t.r.Name)
			continue
		}
		fmt.Printf("%s: integrating %d onto %s (%s)\n", t.r.Name, len(t.unintegrated), t.defBranch, mechanism(t.ff))
		printCommits(t.unintegrated)
		integrated := 0
		if t.ff {
			// A pointer move, so it is atomic and cannot conflict - pre-flight
			// already established the ancestry --ff-only requires, leaving no
			// half-transferred state for this path to report.
			if err := repo.FastForwardTo(t.main, t.r.Branch); err != nil {
				return fmt.Errorf("%s: fast-forward %s to %s in %s: %v",
					t.r.Name, t.defBranch, t.r.Branch, t.main, err)
			}
			integrated = len(t.unintegrated)
		} else {
			for _, c := range t.unintegrated {
				status, err := repo.CherryPick(t.main, c.SHA)
				if err != nil {
					return fmt.Errorf("%s: cherry-pick %s: %v", t.r.Name, shortSHA(c.SHA), err)
				}
				switch status {
				case repo.CherryPickOK:
					integrated++
				case repo.CherryPickEmpty:
					// Patch already present; the pick was auto-skipped internally.
				case repo.CherryPickConflict:
					return fmt.Errorf("%s: cherry-pick of %s conflicted in %s; the pick is in progress. "+
						"Resolve it there, or run `git cherry-pick --abort` in %s to restore %s. "+
						"Halted before integrating any remaining repos.",
						t.r.Name, shortSHA(c.SHA), t.main, t.main, t.defBranch)
				}
			}
		}
		if integrated == 0 {
			fmt.Printf("%s: already up to date\n", t.r.Name)
			continue
		}
		fmt.Printf("%s: %d integrated onto %s\n", t.r.Name, integrated, t.defBranch)
		if o.push {
			if err := repo.PushBranch(t.main, t.defBranch); err != nil {
				return fmt.Errorf("%s: push %s: %v", t.r.Name, t.defBranch, err)
			}
			fmt.Printf("%s: pushed %s to origin\n", t.r.Name, t.defBranch)
		} else {
			fmt.Fprintf(os.Stderr, "git-work: %s: warning: local %s is ahead of origin/%s; "+
				"push it before `git work done` will see %s as merged\n",
				t.r.Name, t.defBranch, t.defBranch, t.r.Branch)
		}
	}
	return nil
}

// printCommits lists commits one per line, indented under the repo's
// would-integrate/integrating summary line.
func printCommits(commits []repo.Commit) {
	for _, c := range commits {
		fmt.Printf("  %s %s\n", shortSHA(c.SHA), c.Subject)
	}
}

// shortSHA abbreviates a commit id for reporting, leaving short ids untouched.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
