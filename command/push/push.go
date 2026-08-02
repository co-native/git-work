package push

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
	repo       string
	all        bool
	force      bool
	allowEmpty bool
	dryRun     bool
	main       bool
}

// Cmd describes `git work push` for the help system. main.go builds its index
// row from Short and answers `git work help push` from the same value that
// `git work push -h` prints.
var Cmd = &cli.Command{
	Name:  "push",
	Short: "push work branches with un-integrated commits to origin",
	Args:  "<repo> | --all",
	Synopsis: []string{
		"git work push <repo> [options]",
		"git work push -a|--all [options]",
	},
	Long: "Pushes work branches to origin - the multi-repo equivalent of `cd`-ing into\n" +
		"each worktree and running `git push`. A repo is pushed when its work branch\n" +
		"has un-integrated commits (commits not on the local default branch, distinct from\n" +
		"unpushed, which measures against @{upstream}). Scope is mandatory.",
	Flags: []cli.Flag{
		{Short: "a", Long: "all", Desc: "push every repo in the work folder"},
		{Short: "f", Long: "force", Desc: "force-push (uses --force-with-lease, never bare --force)"},
		{Short: "e", Long: "allow-empty", Desc: "push branches with nothing new to push"},
		{Long: "main", Desc: "push each repo's default branch instead of its work branch"},
		{Short: "n", Long: "dry-run", Desc: "show what would be pushed without pushing"},
	},
}

// newFlagSet registers push's flags against o. It is separate from parseFlags
// so the conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.all, "all", false, "push every repo in the work folder")
	fs.BoolVar(&o.all, "a", false, "shorthand for --all")
	fs.BoolVar(&o.force, "force", false, "force-push (uses --force-with-lease)")
	fs.BoolVar(&o.force, "f", false, "shorthand for --force")
	fs.BoolVar(&o.allowEmpty, "allow-empty", false, "push branches with nothing new to push")
	fs.BoolVar(&o.allowEmpty, "e", false, "shorthand for --allow-empty")
	fs.BoolVar(&o.main, "main", false, "push each repo's default branch instead of its work branch")
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would be pushed without pushing")
	fs.BoolVar(&o.dryRun, "n", false, "shorthand for --dry-run")
	return fs
}

// parseFlags parses `git work push` arguments. Every flag is boolean, so the
// single positional <repo> may appear anywhere on the line; leading-dash
// tokens are partitioned out as flags (this deliberately does NOT reject a
// leading `-` first arg, so `push --all` parses).
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
	case o.main && o.force:
		// A default branch is shared; git-work never rewrites its history.
		// Rejecting the combination beats silently ignoring -f.
		return nil, cli.Usagef("-f/--force does not apply to --main; default branches are never force-pushed")
	}
	if len(positional) == 1 {
		o.repo = positional[0]
	}
	return o, nil
}

// Run executes `git work push`; it prints any error to stderr and returns the
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
	return push(o, l, st, workDir)
}

// push pushes each in-scope repo's work branch to origin - or, with --main,
// its default branch instead. Unlike integrate there is no cross-repo invariant, so
// failures are collected per repo (done's model) and the rest keep going; a
// nonzero exit reports them together at the end. Split out from run so tests
// drive it with a constructed layout/state.
func push(o *options, l layout.Layout, st *state.State, workDir string) error {
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
			return fmt.Errorf("repo %q is not part of %s", o.repo, st.DisplayName())
		}
	}

	var failures []string
	fail := func(format string, a ...any) {
		failures = append(failures, fmt.Sprintf(format, a...))
	}
	for _, r := range scope {
		main, err := l.RepoMain(r.Name)
		if err != nil {
			fail("%s: %v", r.Name, err)
			continue
		}
		if o.main {
			pushDefaultBranch(o, r.Name, main, fail)
		} else {
			pushWorkBranch(o, r, main, workDir, fail)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%d repo(s) failed to push:\n  %s", len(failures), strings.Join(failures, "\n  "))
	}
	return nil
}

// pushWorkBranch pushes one repo's work branch from its worktree - the default
// mode. fail records a per-repo failure without stopping the run.
func pushWorkBranch(o *options, r state.Repo, main, workDir string, fail func(string, ...any)) {
	// A gone worktree or a branch that no longer exists locally means
	// there is nothing to push: skip with a note rather than failing.
	wt := filepath.Join(workDir, r.Name)
	if _, err := os.Stat(wt); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "git-work: %s: worktree already removed; nothing to push\n", r.Name)
		return
	}
	// The push targets r.Branch, so a worktree sitting on some other branch
	// means the operator is looking at one branch while push publishes
	// another. Refuse rather than silently pushing the recorded one.
	if msg, err := repo.WorktreeBranchProblem(wt, r.Branch); err != nil {
		fail("%s: could not check the worktree's branch: %v", r.Name, err)
		return
	} else if msg != "" {
		fail("%s: %s", r.Name, msg)
		return
	}

	exists, err := repo.LocalBranchExists(main, r.Branch)
	if err != nil {
		fail("%s: could not check branch %s: %v", r.Name, r.Branch, err)
		return
	}
	if !exists {
		fmt.Fprintf(os.Stderr, "git-work: %s: branch %s does not exist locally; nothing to push\n", r.Name, r.Branch)
		return
	}

	defBranch, err := repo.DefaultBranchName(main)
	if err != nil {
		fail("%s: could not determine default branch: %v", r.Name, err)
		return
	}

	// Scope predicate: un-integrated commits, measured against the LOCAL
	// default branch - no fetch first, deliberately: --force-with-lease
	// uses the remote-tracking refs as the lease, and fetching right
	// before pushing would weaken that protection.
	unintegrated, err := repo.CommitsNotIn(main, r.Branch, defBranch)
	if err != nil {
		fail("%s: could not compute un-integrated commits: %v", r.Name, err)
		return
	}
	if len(unintegrated) == 0 && !o.allowEmpty {
		fmt.Fprintf(os.Stderr, "git-work: %s: no commits since %s; skipped (use -e/--allow-empty)\n", r.Name, defBranch)
		return
	}

	verb, forceNote := "pushing", ""
	if o.dryRun {
		verb = "would push"
	}
	if o.force {
		forceNote = " (force-with-lease)"
	}
	if len(unintegrated) == 0 {
		fmt.Printf("%s: %s empty branch %s to origin%s\n", r.Name, verb, r.Branch, forceNote)
	} else {
		fmt.Printf("%s: %s %d commit(s) to origin/%s%s\n", r.Name, verb, len(unintegrated), r.Branch, forceNote)
		printCommits(unintegrated)
	}
	if o.dryRun {
		return
	}

	if err := repo.PushBranchUpstream(wt, r.Branch, o.force); err != nil {
		msg := err.Error()
		if !o.force && (strings.Contains(msg, "[rejected]") || strings.Contains(msg, "failed to push")) {
			fail("%s: push rejected (non-fast-forward); rebase or use -f/--force: %v", r.Name, err)
		} else {
			fail("%s: push %s: %v", r.Name, r.Branch, err)
		}
		return
	}
	fmt.Printf("%s: pushed %s to origin\n", r.Name, r.Branch)
}

// pushDefaultBranch pushes one repo's local default branch from its primary
// clone - the --main mode, the equivalent of running a plain `git push` there.
// It deliberately touches neither the worktree nor the work branch: after
// `git work integrate` the commits live on the default branch, so a torn-down
// worktree or an already-integrated (and therefore "empty") work branch must not
// stop the default branch from reaching origin. parseFlags rejects -f with
// --main, so this never force-pushes.
func pushDefaultBranch(o *options, name, main string, fail func(string, ...any)) {
	defBranch, err := repo.DefaultBranchName(main)
	if err != nil {
		fail("%s: could not determine default branch: %v", name, err)
		return
	}

	// Measure against the remote-tracking ref, purely to report what is going
	// out and to skip repos with nothing to push; git itself stays the
	// authority at push time. Still no fetch first (same reasoning as the work
	// branch mode), so a stale origin/<default> costs at most a redundant push
	// or a rejection that lands in the failure list.
	hasRemote, err := repo.RemoteBranchExists(main, defBranch)
	if err != nil {
		fail("%s: could not check origin/%s: %v", name, defBranch, err)
		return
	}
	var ahead []repo.Commit
	if hasRemote {
		if ahead, err = repo.CommitsNotIn(main, defBranch, "origin/"+defBranch); err != nil {
			fail("%s: could not compute unpushed commits on %s: %v", name, defBranch, err)
			return
		}
		if len(ahead) == 0 && !o.allowEmpty {
			fmt.Fprintf(os.Stderr, "git-work: %s: %s is up to date with origin/%s; skipped (use -e/--allow-empty)\n",
				name, defBranch, defBranch)
			return
		}
	}

	verb := "pushing"
	if o.dryRun {
		verb = "would push"
	}
	switch {
	case !hasRemote:
		fmt.Printf("%s: %s new branch %s to origin\n", name, verb, defBranch)
	case len(ahead) == 0:
		fmt.Printf("%s: %s %s to origin (already up to date)\n", name, verb, defBranch)
	default:
		fmt.Printf("%s: %s %d commit(s) to origin/%s\n", name, verb, len(ahead), defBranch)
		printCommits(ahead)
	}
	if o.dryRun {
		return
	}

	if err := repo.PushBranch(main, defBranch); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "[rejected]") || strings.Contains(msg, "failed to push") {
			fail("%s: push %s rejected (non-fast-forward); pull %s in %s first: %v", name, defBranch, defBranch, main, err)
		} else {
			fail("%s: push %s: %v", name, defBranch, err)
		}
		return
	}
	fmt.Printf("%s: pushed %s to origin\n", name, defBranch)
}

// printCommits lists commits one per line, indented under the repo's
// would-push/pushing summary line.
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
