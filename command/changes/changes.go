// Package changes implements `git work changes`: list a work branch's
// un-integrated commits.
package changes

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
	"github.com/co-native/git-work/internal/workfolder"
)

type options struct {
	repo string
	all  bool
}

// Cmd describes `git work changes` for the help system. main.go builds its
// index row from Short and answers `git work help changes` from the same value
// that `git work changes -h` prints.
var Cmd = &cli.Command{
	Name:  "changes",
	Short: "list a work branch's un-integrated commits",
	Args:  "<repo> | --all",
	Synopsis: []string{
		"git work changes <repo>",
		"git work changes -a|--all",
	},
	Long: "Lists the commits whose patches are on each in-scope work branch but not in\n" +
		"that repo's local default branch. The report uses the same patch-equivalence\n" +
		"rule as `integrate`, so commits already integrated by a squash or cherry-pick\n" +
		"are not listed. It does not fetch or change refs; run `git work refresh` first\n" +
		"when the local default branch must include remote changes. Scope is mandatory.",
	Flags: []cli.Flag{
		{Short: "a", Long: "all", Desc: "list changes for every repo in the work folder"},
	},
}

// newFlagSet registers changes' flags against o. It is separate from
// parseFlags so the conformance test can compare the registered flags with
// Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := cli.NewFlagSet("changes")
	fs.BoolVar(&o.all, "all", false, "list changes for every repo in the work folder")
	fs.BoolVar(&o.all, "a", false, "shorthand for --all")
	return fs
}

// parseFlags parses `git work changes` arguments: the shared `<repo> | --all`
// scope. Help is handled by Run before this is reached, so -h/--help never
// appears here.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	var err error
	if o.repo, err = cli.ParseScope(newFlagSet(o), &o.all, args); err != nil {
		return nil, err
	}
	return o, nil
}

// Run executes `git work changes`; it prints any error to stderr and returns
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
	wf, err := workfolder.Load()
	if err != nil {
		return err
	}
	return changes(o, wf.Layout, wf.State, wf.Dir)
}

// changes lists commits for each in-scope repo independently. It does not
// fetch, change refs, or inspect uncommitted changes. Problems are collected
// so --all reports every repo that needs attention.
func changes(o *options, l layout.Layout, st *state.State, workDir string) error {
	scope, err := workfolder.Scope(st, o.repo, o.all)
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
			problem("%s: checkout is missing", r.Name)
			continue
		}
		if msg, err := repo.WorktreeBranchProblem(wt, r.Branch); err != nil {
			problem("%s: could not check the worktree branch: %v", r.Name, err)
			continue
		} else if msg != "" {
			problem("%s: %s", r.Name, msg)
			continue
		}

		main, err := workfolder.RepoDirFor(l, st, workDir, r.Name)
		if err != nil {
			problem("%s: %v", r.Name, err)
			continue
		}
		defBranch, err := repo.DefaultBranchFor(main, r.DefaultBranch)
		if err != nil {
			problem("%s: could not determine the default branch: %v", r.Name, err)
			continue
		}
		exists, err := repo.LocalBranchExists(main, r.Branch)
		if err != nil {
			problem("%s: could not check branch %s: %v", r.Name, r.Branch, err)
			continue
		}
		if !exists {
			problem("%s: work branch %s does not exist locally", r.Name, r.Branch)
			continue
		}
		commits, err := repo.CommitsNotIn(main, r.Branch, defBranch)
		if err != nil {
			problem("%s: could not list commits: %v", r.Name, err)
			continue
		}
		report(r.Name, r.Branch, defBranch, commits)
	}

	if len(problems) > 0 {
		return fmt.Errorf("changes finished with %d problem(s):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

func report(name, branch, defBranch string, commits []repo.Commit) {
	if len(commits) == 0 {
		fmt.Printf("%s: no un-integrated commits on %s not in %s\n", name, branch, defBranch)
		return
	}
	word := "commits"
	if len(commits) == 1 {
		word = "commit"
	}
	fmt.Printf("%s: %d un-integrated %s on %s not in %s\n", name, len(commits), word, branch, defBranch)
	repo.PrintCommits(os.Stdout, commits)
}
