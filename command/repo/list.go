package repo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	gitrepo "github.com/co-native/git-work/internal/repo"
)

// ListCmd describes `git work repo list`.
var ListCmd = &cli.Command{
	Name:     "repo list",
	Short:    "list configured and managed repos, worktrees, and drift",
	Synopsis: []string{"git work repo list"},
	Long: "Lists every configured repo and every local primary in one table, with its\n" +
		"default branch, configured clone URL, worktree count, and status. A config\n" +
		"entry with no local primary, or a primary with no config URL, is a row rather\n" +
		"than an omission. Layout drift - a missing or malformed pointer file, a\n" +
		"primary folder not named after the default branch, a primary checked out\n" +
		"elsewhere, core.worktree not \"..\" - is reported, never fixed; run\n" +
		"`git work repo adopt <repo>` to normalize it.",
}

// repoStatus describes one configured or managed repo: its resolved primary
// folder, default branch, URL state, linked worktrees, and layout drift.
type repoStatus struct {
	Name          string
	PrimaryDir    string             // primary folder name; "" when undetermined
	DefaultBranch string             // default branch (upstream, local HEAD fallback); "" when undetermined
	ConfigURL     string             // repos.<name>.url, or ""
	OriginURL     string             // the primary's origin URL, or ""
	ConfigOnly    bool               // a config entry with no local primary
	Worktrees     []gitrepo.Worktree // linked worktrees (the primary itself excluded)
	Drift         []string           // layout drift findings; empty when conforming
}

// runList executes `git work repo list`.
func runList(args []string) error {
	if len(args) > 0 {
		return cli.Usagef("repo list takes no arguments")
	}
	cfg, l, err := config.LoadLayout()
	if err != nil {
		return err
	}
	statuses, err := listRepos(cfg, l)
	if err != nil {
		return err
	}
	writeList(os.Stdout, statuses)
	return nil
}

// listRepos builds one sorted status per configured repo and per local primary.
func listRepos(cfg *config.Config, l layout.Layout) ([]repoStatus, error) {
	local, err := l.ListRepos()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(local)+len(cfg.Repos))
	names := make([]string, 0, len(local)+len(cfg.Repos))
	for _, name := range local {
		seen[name] = true
		names = append(names, name)
	}
	for name := range cfg.Repos {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	statuses := make([]repoStatus, 0, len(names))
	for _, name := range names {
		if _, err := os.Stat(l.RepoDir(name)); os.IsNotExist(err) {
			statuses = append(statuses, repoStatus{
				Name:          name,
				DefaultBranch: cfg.RepoDefaultBranch(name),
				ConfigURL:     cfg.RepoURL(name),
				ConfigOnly:    true,
			})
			continue
		}
		statuses = append(statuses, inspectRepo(cfg, l, name))
	}
	return statuses, nil
}

// inspectRepo gathers one repo's status. Layout problems - a missing or
// malformed parent .git pointer, a primary folder not named after the
// default branch, a primary checked out on a non-default branch,
// core.worktree not pointing at the parent - are reported as drift findings
// rather than aborting the listing; `repo adopt` is the normalizer for
// them.
func inspectRepo(cfg *config.Config, l layout.Layout, name string) repoStatus {
	st := repoStatus{Name: name, ConfigURL: cfg.RepoURL(name)}
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

	if url, err := gitrepo.RemoteURL(mainDir); err == nil {
		st.OriginURL = url
	}

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

// statusText summarizes the row: the first issue that needs an operator's
// attention, or "ok".
func (st repoStatus) statusText() string {
	var notes []string
	if st.ConfigOnly {
		notes = append(notes, "configured, not cloned")
	} else {
		if st.ConfigURL == "" {
			notes = append(notes, "no config url")
		} else if st.OriginURL != "" && st.ConfigURL != st.OriginURL {
			notes = append(notes, "config url differs from origin")
		}
	}
	if len(st.Drift) > 0 {
		notes = append(notes, "drift")
	}
	if len(notes) == 0 {
		return "ok"
	}
	return strings.Join(notes, ", ")
}

// writeList renders the unified table, then worktree and drift detail lines.
func writeList(w io.Writer, statuses []repoStatus) {
	if len(statuses) == 0 {
		fmt.Fprintln(w, "no configured repos")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDEFAULT\tURL\tWORKTREES\tSTATUS")
	for _, st := range statuses {
		def := st.DefaultBranch
		if def == "" {
			def = "-"
		}
		url := st.ConfigURL
		if url == "" {
			url = "-"
		}
		wts := "-"
		if len(st.Worktrees) > 0 {
			wts = fmt.Sprintf("%d", len(st.Worktrees))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", st.Name, def, url, wts, st.statusText())
	}
	tw.Flush()

	for _, st := range statuses {
		for _, wt := range st.Worktrees {
			branch := wt.Branch
			if branch == "" {
				branch = "detached"
			}
			fmt.Fprintf(w, "  %s worktree %s (%s)\n", st.Name, wt.Path, branch)
		}
		for _, d := range st.Drift {
			fmt.Fprintf(w, "  %s drift: %s\n", st.Name, d)
		}
	}
}
