package repo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	gitrepo "github.com/co-native/git-work/internal/repo"
)

// ListCmd describes `git work repo list`.
var ListCmd = &cli.Command{
	Name:     "repo list",
	Short:    "list managed repos, worktrees, and layout drift",
	Synopsis: []string{"git work repo list"},
	Long: "Lists every managed repo under the repos root with its default branch, its\n" +
		"linked worktrees (path and branch), and any layout drift: a missing or\n" +
		"malformed pointer file, a primary folder not named after the default branch,\n" +
		"a primary checked out elsewhere, core.worktree not \"..\". Drift is reported,\n" +
		"never fixed - run `git work repo adopt <repo>` to normalize it.",
}

// repoStatus describes one managed repo under the repos root: its resolved
// primary folder, default branch, linked worktrees, and any layout drift.
type repoStatus struct {
	Name          string
	PrimaryDir    string             // primary folder name; "" when undetermined
	DefaultBranch string             // default branch (upstream, local HEAD fallback); "" when undetermined
	Worktrees     []gitrepo.Worktree // linked worktrees (the primary itself excluded)
	Drift         []string           // layout drift findings; empty when conforming
}

// runList executes `git work repo list`.
func runList(args []string) error {
	if len(args) > 0 {
		return cli.Usagef("repo list takes no arguments")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	l, err := cfg.Layout()
	if err != nil {
		return err
	}
	statuses, err := listRepos(l)
	if err != nil {
		return err
	}
	writeList(os.Stdout, statuses)
	return nil
}

// listRepos inspects every managed repo under the repos root, sorted by name.
func listRepos(l layout.Layout) ([]repoStatus, error) {
	names, err := l.ListRepos()
	if err != nil {
		return nil, err
	}
	statuses := make([]repoStatus, 0, len(names))
	for _, name := range names {
		statuses = append(statuses, inspectRepo(l, name))
	}
	return statuses, nil
}

// inspectRepo gathers one repo's status. Layout problems - a missing or
// malformed parent .git pointer, a primary folder not named after the
// default branch, a primary checked out on a non-default branch,
// core.worktree not pointing at the parent - are reported as drift findings
// rather than aborting the listing; `repo adopt` is the normalizer for
// them.
func inspectRepo(l layout.Layout, name string) repoStatus {
	st := repoStatus{Name: name}
	repoDir := l.RepoDir(name)

	primary, err := layout.PrimaryDir(repoDir)
	if err != nil {
		st.Drift = append(st.Drift, err.Error())
		if primary = primaryFallback(repoDir); primary == "" {
			return st
		}
	}
	st.PrimaryDir = primary
	mainDir := filepath.Join(repoDir, primary)

	if def, err := gitrepo.DefaultBranchName(mainDir); err != nil {
		st.Drift = append(st.Drift, fmt.Sprintf("default branch undetermined: %v", err))
	} else {
		st.DefaultBranch = def
		if primary != def {
			st.Drift = append(st.Drift, fmt.Sprintf("primary folder %q does not match default branch %q", primary, def))
		}
		if cur, err := gitrepo.CurrentBranch(mainDir); err != nil {
			st.Drift = append(st.Drift, fmt.Sprintf("checked-out branch undetermined: %v", err))
		} else if cur != def {
			st.Drift = append(st.Drift, fmt.Sprintf("primary has %q checked out, want the default branch %q", cur, def))
		}
	}

	if cw, err := gitrepo.CoreWorktree(mainDir); err != nil {
		st.Drift = append(st.Drift, fmt.Sprintf("core.worktree undetermined: %v", err))
	} else if cw != ".." {
		st.Drift = append(st.Drift, fmt.Sprintf("core.worktree is %q, want \"..\"", cw))
	}

	if wts, err := gitrepo.ListWorktrees(mainDir); err != nil {
		st.Drift = append(st.Drift, fmt.Sprintf("list worktrees: %v", err))
	} else if len(wts) > 1 {
		st.Worktrees = wts[1:] // the main worktree (the primary itself) is first
	}
	return st
}

// primaryFallback locates the real clone when the pointer file cannot name
// it: the unique direct subdirectory containing a .git entry. Returns ""
// when there is no such directory or more than one.
func primaryFallback(repoDir string) string {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return ""
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoDir, e.Name(), ".git")); err == nil {
			found = append(found, e.Name())
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return ""
}

// writeList renders repo statuses, one block per repo.
func writeList(w io.Writer, statuses []repoStatus) {
	if len(statuses) == 0 {
		fmt.Fprintln(w, "no managed repos")
		return
	}
	for _, st := range statuses {
		if st.DefaultBranch != "" {
			fmt.Fprintf(w, "%s (%s)\n", st.Name, st.DefaultBranch)
		} else {
			fmt.Fprintln(w, st.Name)
		}
		for _, wt := range st.Worktrees {
			branch := wt.Branch
			if branch == "" {
				branch = "detached"
			}
			fmt.Fprintf(w, "  worktree %s (%s)\n", wt.Path, branch)
		}
		for _, d := range st.Drift {
			fmt.Fprintf(w, "  drift: %s\n", d)
		}
	}
}
