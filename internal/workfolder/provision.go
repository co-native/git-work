package workfolder

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

// AvailableRepos returns the repo names a work folder can select in mode: the
// local primaries for worktree mode, and the union of local primaries and
// config entries for clone mode, where a URL is enough.
func AvailableRepos(cfg *config.Config, l layout.Layout, mode layout.Mode) ([]string, error) {
	local, err := l.ListRepos()
	if err != nil {
		return nil, err
	}
	if mode != layout.ModeClone {
		return local, nil
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
	return names, nil
}

// RepoURL resolves a repo's clone URL: the config registry first, then an
// existing primary's origin. Neither is a hard error naming both remedies.
func RepoURL(cfg *config.Config, l layout.Layout, name string) (string, error) {
	if url := cfg.RepoURL(name); url != "" {
		return url, nil
	}
	if main, err := l.RepoMain(name); err == nil {
		if url, err := repo.RemoteURL(main); err == nil {
			return url, nil
		}
	}
	return "", fmt.Errorf("repo %q has no clone URL: set `repos.%s.url` in the config, or run `git work repo clone <url> %s`", name, name, name)
}

// PlannedRepo is one repo ready to materialize into a work folder, in either
// checkout mode. It carries the source-specific fields so new and add share
// one creation and rollback path.
type PlannedRepo struct {
	Name          string
	Choice        repo.BranchChoice
	Mode          layout.Mode
	MainDir       string // worktree mode: the primary clone
	URL           string // clone mode: the clone URL
	DefaultBranch string // clone mode: the recorded default branch
	LFSFetchAll   bool   // clone mode: fetch every LFS object, not just the branch
	Warn          func(msg string)
}

// Materialize creates the repo's checkout under workDir and returns the state
// record for it. A clone-mode failure removes the partial clone.
func (p PlannedRepo) Materialize(workDir string) (state.Repo, error) {
	path := filepath.Join(workDir, p.Name)
	rec := state.Repo{Name: p.Name, BranchSource: p.Choice.Source, Branch: p.Choice.Branch}
	if p.Mode != layout.ModeClone {
		if err := p.Choice.Checkout(p.MainDir, path); err != nil {
			return state.Repo{}, err
		}
		return rec, nil
	}
	if err := repo.CloneCheckout(p.URL, path); err != nil {
		return state.Repo{}, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(path)
		}
	}()
	if err := repo.SwitchToBranch(path, p.Choice); err != nil {
		return state.Repo{}, err
	}
	def, err := repo.DefaultBranchFor(path, p.DefaultBranch)
	if err != nil {
		return state.Repo{}, err
	}
	rec.DefaultBranch = def
	if warning, err := repo.PrepareLFS(path, p.LFSFetchAll); err != nil {
		return state.Repo{}, err
	} else if warning != "" && p.Warn != nil {
		p.Warn(fmt.Sprintf("%s: %s", p.Name, warning))
	}
	success = true
	return rec, nil
}

// Remove undoes Materialize for rollback. It is force-remove for a worktree,
// since the caller has already decided the whole folder is being abandoned.
func (p PlannedRepo) Remove(workDir string) error {
	path := filepath.Join(workDir, p.Name)
	if p.Mode == layout.ModeClone {
		return os.RemoveAll(path)
	}
	return repo.RemoveWorktree(p.MainDir, path, true)
}
