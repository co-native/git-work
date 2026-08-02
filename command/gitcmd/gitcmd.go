// Package gitcmd implements `git work git`: run one git command in each
// in-scope repo's worktree, passing its output straight through.
package gitcmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/state"
)

type options struct {
	repo string
	all  bool
	args []string // the git command, verbatim, from after the --
}

// Cmd describes `git work git` for the help system. main.go builds its index
// row from Short and answers `git work help git` from the same value that
// `git work git -h` prints.
var Cmd = &cli.Command{
	Name:  "git",
	Short: "run a git command in each repo's worktree",
	Args:  "<repo> | --all -- <args>",
	Synopsis: []string{
		"git work git <repo> -- <git args>",
		"git work git -a|--all -- <git args>",
	},
	Long: "Runs one git command in each in-scope repo's worktree. Everything after the\n" +
		"first `--` is passed to git verbatim and is never inspected, so aliases and\n" +
		"flags behave as they do in a single repo; the `--` is required. Output is not\n" +
		"captured or reformatted - git keeps its color, formatting and pager - and only\n" +
		"a `=== <repo> ===` header is printed per repo. The command acts on whatever the\n" +
		"worktree has checked out, drift included.",
	Examples: []string{
		"git work git -a -- st",
		"git work git -a -- log --oneline -5",
		"git work git api -- diff --stat",
	},
	Flags: []cli.Flag{
		{Short: "a", Long: "all", Desc: "run in every repo in the work folder"},
	},
}

// newFlagSet registers git's own flags against o - only those before the `--`;
// everything after it is git's. It is separate from parseFlags so the
// conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("git", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.all, "all", false, "run in every repo in the work folder")
	fs.BoolVar(&o.all, "a", false, "shorthand for --all")
	return fs
}

// parseFlags parses `git work git` arguments. It splits at the FIRST `--`:
// git-work's own flags and the optional <repo> come before it, the git command
// after. The split is what makes the command safe - push/integrate's approach of
// partitioning every leading-dash token into flags would swallow the git
// command's own flags (`--oneline`, `-5`), so everything after `--` is opaque
// and never inspected. Requiring `--` rather than accepting a bare git command
// keeps that unambiguous: `git work git -a log --oneline` has no reading where
// `--oneline` is not ambiguous.
//
// Help is handled by Run before this is reached, so -h/--help never appears in
// the pre-`--` half; cli.Wanted stops scanning at `--`, so a -h *after* it is
// git's and arrives here as an ordinary element of o.args.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	own := args
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep >= 0 {
		own, o.args = args[:sep], args[sep+1:]
	}

	fs := newFlagSet(o)

	// Within the pre-`--` half every token is git-work's own, so the
	// positional may sit anywhere on the line as it can for push and integrate.
	var flagArgs, positional []string
	for _, a := range own {
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
		} else {
			positional = append(positional, a)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	// The separator is checked before the scope rules on purpose. Without a
	// `--` there is no way to tell a repo name from a git command, so
	// `git work git -a status` would otherwise report "give a <repo> or
	// --all, not both" - technically true, and useless, when what the
	// operator meant was `status` as the command. A bare `git work git` falls
	// in here too: it has no separator either, and the rendered usage block
	// cli.Fail prints alongside shows the shape.
	if sep < 0 {
		return nil, cli.Usagef("give the git command after `--`, e.g. `git work git -a -- status`")
	}
	if len(o.args) == 0 {
		return nil, cli.Usagef("nothing after `--`; give a git command, e.g. `git work git -a -- status`")
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

// Run executes `git work git`; it prints any error to stderr and returns the
// process exit code.
func Run(args []string) int {
	// cli.Wanted stops at the first `--`, so `git work git demo -- log -h`
	// still forwards -h to git rather than printing git-work's own help.
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
	return runAll(o, os.Stdin, os.Stdout, os.Stderr, st, workDir)
}

// runAll runs the git command once per in-scope repo, in that repo's worktree.
//
// The child's streams are the ones handed in, not pipes we read: when they are
// the real os.Stdout/os.Stderr, os/exec passes the file descriptors straight to
// git, so it still sees a TTY and keeps its color, formatting, and pager
// exactly as it would in a single repo. That is the whole point of the command,
// and it is why output is never reformatted - only a header per repo is printed
// so the blocks are attributable. Tests substitute buffers, which os/exec pipes
// instead.
//
// This command deliberately does NOT apply the worktree/state branch check the
// other commands share: it runs on whatever the worktree has checked out, which
// is exactly what makes `git work git -a -- status` useful for *investigating*
// drift.
//
// Failures follow done's model - collected per repo, the rest keep going,
// nonzero exit at the end. Note that a nonzero exit is reported for any git
// command that exits nonzero, including ones where that is a normal answer
// (`git diff --quiet` on a dirty tree); git-work cannot tell those apart, so it
// reports the code and lets the operator judge.
func runAll(o *options, in io.Reader, w, errw io.Writer, st *state.State, workDir string) error {
	scope, err := scopeRepos(o, st)
	if err != nil {
		return err
	}
	var failures []string
	for i, r := range scope {
		if i > 0 {
			fmt.Fprintf(w, "\n")
		}
		wt := filepath.Join(workDir, r.Name)
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			fmt.Fprintf(errw, "git-work: %s: worktree missing; skipping\n", r.Name)
			continue
		}
		fmt.Fprintf(w, "=== %s ===\n", r.Name)
		cmd := exec.Command("git", o.args...)
		cmd.Dir = wt
		cmd.Stdin = in
		cmd.Stdout = w
		cmd.Stderr = errw
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				failures = append(failures, fmt.Sprintf("%s: git %s exited %d", r.Name, o.args[0], ee.ExitCode()))
			} else {
				failures = append(failures, fmt.Sprintf("%s: %v", r.Name, err))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d repo(s) exited nonzero:\n  %s", len(failures), strings.Join(failures, "\n  "))
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
	// not a failed run: the usage block that comes with it is the answer.
	return nil, cli.Usagef("repo %q is not part of %s", o.repo, st.DisplayName())
}
