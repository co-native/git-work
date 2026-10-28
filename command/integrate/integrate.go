package integrate

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
	"github.com/co-native/git-work/internal/workfolder"
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
	fs := cli.NewFlagSet("integrate")
	fs.BoolVar(&o.all, "all", false, "integrate every repo in the work folder")
	fs.BoolVar(&o.all, "a", false, "shorthand for --all")
	fs.BoolVar(&o.multi, "multi", false, "allow integrating a repo with 2+ un-integrated commits")
	fs.BoolVar(&o.push, "push", false, "push the default branch to origin after integrating")
	fs.BoolVar(&o.noFetch, "no-fetch", false, "skip fetching before pre-flight")
	fs.BoolVar(&o.dryRun, "dry-run", false, "run the full pre-flight but skip cherry-picks and pushes")
	fs.BoolVar(&o.dryRun, "n", false, "shorthand for --dry-run")
	return fs
}

// parseFlags parses `git work integrate` arguments: the shared `<repo> | --all`
// scope (see cli.ParseScope) plus integrate's own flags.
//
// Help is handled by Run before this is reached, so -h/--help never appears
// here.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	var err error
	if o.repo, err = cli.ParseScope(newFlagSet(o), &o.all, args); err != nil {
		return nil, err
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
	repoDir      string // refs, push target
	transferDir  string // where commits are applied (temp worktree in clone mode)
	mode         layout.Mode
	tempPath     string
	resumed      bool // a clean temp worktree from a prior run was removed
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
	wf, err := workfolder.Load()
	if err != nil {
		return err
	}
	if err := wf.Config.Validate(); err != nil {
		return err
	}
	return integrate(o, wf.Config, wf.Layout, wf.State, wf.Dir)
}

// integrate runs the pre-flight over the whole in-scope set, then (only if it all
// passes) the cherry-pick transfer. Split out from run so tests drive it with
// a constructed config/layout/state instead of the global config.
func integrate(o *options, cfg *config.Config, l layout.Layout, st *state.State, workDir string) error {
	scope, err := workfolder.Scope(st, o.repo, o.all)
	if err != nil {
		return err
	}

	// Pre-flight: fully atomic. Collect violations across the whole set; a
	// single violation aborts before any cherry-pick happens. Repos with
	// nothing to integrate (missing branch/worktree) are skipped, not violations.
	var targets []target
	var violations []string
	for _, r := range scope {
		t, violation := preflight(o, cfg, l, st, r, workDir)
		if violation != "" {
			violations = append(violations, violation)
		}
		if t != nil {
			targets = append(targets, *t)
		}
	}

	if o.dryRun {
		for _, t := range targets {
			if t.report("would integrate") && o.push {
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
		if !t.report("integrating") {
			// A resumed conflict resolution already advanced the local default
			// branch, so --push still has something to publish.
			if t.resumed && o.push {
				if err := repo.PushBranch(t.repoDir, t.defBranch); err != nil {
					return fmt.Errorf("%s: push %s: %v", t.r.Name, t.defBranch, err)
				}
				fmt.Printf("%s: pushed %s to origin\n", t.r.Name, t.defBranch)
			}
			continue
		}
		integrated, err := t.transfer()
		if err != nil {
			return err
		}
		if err := t.cleanup(); err != nil {
			return err
		}
		if integrated == 0 {
			fmt.Printf("%s: already up to date\n", t.r.Name)
			continue
		}
		fmt.Printf("%s: %d integrated onto %s\n", t.r.Name, integrated, t.defBranch)
		if o.push {
			if err := repo.PushBranch(t.repoDir, t.defBranch); err != nil {
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

// preflight runs one repo's pre-flight. It returns the target when the repo
// is ready to transfer, and a violation when something forbids the run. A
// repo with nothing to integrate yields neither, with a note on stderr. Both
// can be set at once: a repo that trips the --multi guard under --dry-run
// keeps its target so the listing still shows the commits, while the
// violation keeps the transfer from ever reaching it.
func preflight(o *options, cfg *config.Config, l layout.Layout, st *state.State, r state.Repo, workDir string) (*target, string) {
	violate := func(format string, a ...any) (*target, string) {
		return nil, fmt.Sprintf(format, a...)
	}
	skip := func(format string, a ...any) (*target, string) {
		fmt.Fprintf(os.Stderr, "git-work: "+format+"\n", a...)
		return nil, ""
	}

	// A PR-only repo is a standing policy, not an error state, so how it
	// is reported depends on how the repo entered scope. Under --all,
	// declining it is the appropriate thing to do and the run still
	// succeeds; naming it means asking for something policy forbids, and
	// answering that with a silent exit 0 would be the worst outcome.
	// Both paths run before any git work, and both are shared with
	// --dry-run, so -n predicts the real run's exit code.
	if cfg.Integration(r.Name) == config.IntegrationPR {
		if o.all {
			return skip("%s: integrates through a pull request; skipping", r.Name)
		}
		return violate("%s: integrates through a pull request (integration: pr); "+
			"push the branch and open one, or change the route with "+
			"`git work config set repos.%s.integration local`", r.Name, r.Name)
	}

	main, err := workfolder.RepoDirFor(l, st, workDir, r.Name)
	if err != nil {
		return violate("%s: %v", r.Name, err)
	}

	// A gone worktree or a branch that no longer exists locally means
	// there is nothing to integrate: skip with a note rather than erroring.
	wt := filepath.Join(workDir, r.Name)
	if _, err := os.Stat(wt); os.IsNotExist(err) {
		return skip("%s: worktree already removed; nothing to integrate", r.Name)
	}

	// The cherry-picks take their commits from r.Branch, so a worktree on
	// a different branch means the pre-flight would measure one branch
	// while the operator is working on another. A pre-flight violation,
	// not a skip: the whole run aborts rather than integrating the wrong branch.
	if msg, err := repo.WorktreeBranchProblem(wt, r.Branch); err != nil {
		return violate("%s: could not check the worktree's branch: %v", r.Name, err)
	} else if msg != "" {
		return violate("%s: %s", r.Name, msg)
	}

	mode := st.Mode.OrDefault()
	tempPath := ""
	resumed := false
	completed := false
	if mode == layout.ModeClone {
		tempPath = l.IntegrateWorktree(workDir, r.Name)
		if _, statErr := os.Stat(tempPath); statErr == nil {
			if o.dryRun {
				return violate("%s: an integrate worktree already exists at %s; resolve or abort it before a dry run", r.Name, tempPath)
			}
			resume, rerr := repo.PrepareIntegrateWorktree(main, tempPath)
			if rerr != nil {
				return violate("%s: %v", r.Name, rerr)
			}
			resumed = resume.Resumed
			completed = resume.Completed
		}
	}

	if !o.noFetch {
		if err := repo.Fetch(main); err != nil {
			fmt.Fprintf(os.Stderr, "git-work: %s: fetch failed: %v (continuing with local refs)\n", r.Name, err)
		}
	}

	exists, err := repo.LocalBranchExists(main, r.Branch)
	if err != nil {
		return violate("%s: could not check branch %s: %v", r.Name, r.Branch, err)
	}
	if !exists {
		return skip("%s: branch %s does not exist locally; nothing to integrate", r.Name, r.Branch)
	}

	defBranch, err := repo.DefaultBranchFor(main, r.DefaultBranch)
	if err != nil {
		return violate("%s: could not determine default branch: %v", r.Name, err)
	}

	if mode != layout.ModeClone {
		// The primary must be clean and sitting on its default branch so the
		// cherry-picks integrate where the pre-flight measured them. Clone mode
		// does the transfer in a fresh temp worktree instead, so the feature
		// checkout's state does not matter.
		if dirty, err := repo.HasUncommittedChanges(main); err != nil {
			return violate("%s: could not check for uncommitted changes: %v", r.Name, err)
		} else if dirty {
			return violate("%s: primary clone %s has uncommitted changes", r.Name, main)
		}
		cur, err := repo.CurrentBranch(main)
		if err != nil {
			return violate("%s: could not determine checked-out branch: %v", r.Name, err)
		}
		if cur != defBranch {
			return violate("%s: primary clone %s is on %s, not the default branch %s", r.Name, main, cur, defBranch)
		}
	}

	// Fast-forward the local default branch to origin - a safe, always
	// desirable mutation; a non-fast-forward is a violation. A resumed run
	// already has the resolved cherry-picks on the local default, so it is
	// deliberately ahead of origin and must not be pulled back.
	if !completed {
		if _, err := repo.PullBranchFFOnly(main, defBranch); err != nil {
			return violate("%s: could not fast-forward %s to origin/%s: %v", r.Name, defBranch, defBranch, err)
		}
	}

	// Measure against the LOCAL default branch (the cherry-pick target),
	// not origin/main: integrating without --push leaves local main ahead, and
	// measuring against origin would re-list already-integrated commits.
	unintegrated, err := repo.CommitsNotIn(main, r.Branch, defBranch)
	if err != nil {
		return violate("%s: could not compute un-integrated commits: %v", r.Name, err)
	}
	if completed {
		// The operator finished the conflict resolution; the resolved patches
		// deliberately no longer match the original commits, so patch
		// equivalence cannot see them. The marker and moved HEAD are the proof.
		unintegrated = nil
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
			return violate("%s: could not check whether %s can fast-forward to %s: %v", r.Name, defBranch, r.Branch, err)
		}
	}
	t := &target{r: r, repoDir: main, mode: mode, tempPath: tempPath, resumed: resumed, defBranch: defBranch, unintegrated: unintegrated, ff: ff}

	// The --multi guard applies to both mechanisms. A fast-forward carries
	// no per-commit risk, but the guard is about intent - integrating commits
	// you had forgotten about is the same surprise either way - so the
	// rule stays one rule.
	if len(unintegrated) >= 2 && !o.multi {
		violation := fmt.Sprintf("%s: %d un-integrated commits; pass --multi to integrate more than one", r.Name, len(unintegrated))
		// A dry run still reports what's out there: the commits are
		// computed, so keep the target for the listing. Violations stay
		// non-empty, so the transfer can never reach this repo.
		if o.dryRun {
			return t, violation
		}
		return nil, violation
	}
	return t, ""
}

// report prints the repo's summary line under verb ("would integrate",
// "integrating") and lists the commits, or says the repo is already up to
// date. It reports whether there is anything to transfer.
func (t target) report(verb string) bool {
	if len(t.unintegrated) == 0 {
		fmt.Printf("%s: already up to date\n", t.r.Name)
		return false
	}
	fmt.Printf("%s: %s %d onto %s (%s)\n", t.r.Name, verb, len(t.unintegrated), t.defBranch, mechanism(t.ff))
	repo.PrintCommits(os.Stdout, t.unintegrated)
	return true
}

// transfer moves the repo's un-integrated commits onto its default branch by
// the mechanism pre-flight chose and returns how many landed. A cherry-pick
// that conflicts is left in progress and returned as an error naming the
// way out; the caller halts before touching any remaining repo.
func (t target) transfer() (int, error) {
	transferDir := t.repoDir
	if t.mode == layout.ModeClone {
		if err := repo.AddWorktree(t.repoDir, t.tempPath, t.defBranch, "", false); err != nil {
			return 0, fmt.Errorf("%s: create integrate worktree %s: %v", t.r.Name, t.tempPath, err)
		}
		transferDir = t.tempPath
		base, err := repo.RevParse(t.repoDir, t.defBranch)
		if err != nil {
			return 0, fmt.Errorf("%s: record integrate base: %v", t.r.Name, err)
		}
		if err := repo.WriteIntegrateMarker(t.tempPath, base); err != nil {
			return 0, fmt.Errorf("%s: write integrate marker: %v", t.r.Name, err)
		}
	}
	if t.ff {
		// A pointer move, so it is atomic and cannot conflict - pre-flight
		// already established the ancestry --ff-only requires, leaving no
		// half-transferred state for this path to report.
		if err := repo.FastForwardTo(transferDir, t.r.Branch); err != nil {
			return 0, fmt.Errorf("%s: fast-forward %s to %s in %s: %v",
				t.r.Name, t.defBranch, t.r.Branch, transferDir, err)
		}
		return len(t.unintegrated), nil
	}
	integrated := 0
	for _, c := range t.unintegrated {
		status, err := repo.CherryPick(transferDir, c.SHA)
		if err != nil {
			return integrated, fmt.Errorf("%s: cherry-pick %s: %v", t.r.Name, c.Short(), err)
		}
		switch status {
		case repo.CherryPickOK:
			integrated++
		case repo.CherryPickEmpty:
			// Patch already present; the pick was auto-skipped internally.
		case repo.CherryPickConflict:
			return integrated, fmt.Errorf("%s: cherry-pick of %s conflicted in %s; the pick is in progress. "+
				"Resolve it there, or run `git cherry-pick --abort` in %s to restore %s. "+
				"Halted before integrating any remaining repos.",
				t.r.Name, c.Short(), transferDir, transferDir, t.defBranch)
		}
	}
	return integrated, nil
}

// cleanup removes the clone-mode transfer worktree after a successful
// transfer. A conflict leaves it in place for the operator to resolve.
func (t target) cleanup() error {
	if t.mode != layout.ModeClone || t.tempPath == "" {
		return nil
	}
	if err := repo.RemoveWorktree(t.repoDir, t.tempPath, false); err != nil {
		return fmt.Errorf("%s: remove integrate worktree %s: %v", t.r.Name, t.tempPath, err)
	}
	return nil
}
