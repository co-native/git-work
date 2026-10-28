package layout

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Layout resolves on-disk paths for primary repos and work folders.
type Layout struct {
	ReposRoot string
	WorkRoot  string
}

// WorktreesDirName is the reserved directory at a work folder's root holding
// linked worktrees: git-work's transient integrate checkout and the subagent
// worktrees that follow the documented convention.
const WorktreesDirName = ".worktrees"

func (l Layout) RepoDir(repo string) string { return filepath.Join(l.ReposRoot, repo) }
func (l Layout) WorkDir(name string) string { return filepath.Join(l.WorkRoot, name) }
func (l Layout) Worktree(work, repo string) string {
	return filepath.Join(l.WorkRoot, work, repo)
}

// WorktreesDir returns <work>/.worktrees.
func (l Layout) WorktreesDir(work string) string {
	return filepath.Join(work, WorktreesDirName)
}

// IntegrateWorktree returns git-work's transient integrate checkout for a
// repo: <work>/.worktrees/integrate-<repo>.
func (l Layout) IntegrateWorktree(work, repo string) string {
	return filepath.Join(work, WorktreesDirName, "integrate-"+repo)
}

// AgentWorktree returns a subagent worktree following the documented
// convention: <work>/.worktrees/agent-<agent>/<repo>.
func (l Layout) AgentWorktree(work, agent, repo string) string {
	return filepath.Join(work, WorktreesDirName, "agent-"+agent, repo)
}

// RepoMain resolves the primary clone directory of repo (e.g.
// <ReposRoot>/<repo>/main). The directory is named after the upstream
// default branch, so it is discovered from the parent .git pointer file -
// the on-disk source of truth - rather than assumed.
func (l Layout) RepoMain(repo string) (string, error) {
	repoDir := l.RepoDir(repo)
	dir, err := PrimaryDir(repoDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(repoDir, dir), nil
}

// WritePrimaryPointer writes repoDir's .git pointer file naming primary, the
// primary clone directory relative to repoDir, in the form PrimaryDir reads.
func WritePrimaryPointer(repoDir, primary string) error {
	return os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ./"+primary+"/.git\n"), 0o644)
}

// PrimaryDir returns the primary clone directory name recorded in repoDir's
// .git pointer file ("gitdir: ./<dir>/.git"), relative to repoDir.
func PrimaryDir(repoDir string) (string, error) {
	ptr := filepath.Join(repoDir, ".git")
	data, err := os.ReadFile(ptr)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s: missing .git pointer file (expected \"gitdir: ./<dir>/.git\"): not a git-work primary clone", repoDir)
		}
		return "", fmt.Errorf("read .git pointer file: %w", err)
	}
	line := strings.TrimSpace(string(data))
	malformed := func(reason string) error {
		return fmt.Errorf("%s: malformed .git pointer file (%s): expected \"gitdir: ./<dir>/.git\", got %q", ptr, reason, line)
	}
	target, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", malformed("no gitdir prefix")
	}
	target = strings.TrimSpace(target)
	if filepath.IsAbs(target) {
		return "", malformed("absolute path")
	}
	dir, ok := strings.CutSuffix(strings.TrimPrefix(target, "./"), "/.git")
	if !ok {
		return "", malformed("does not point at a <dir>/.git")
	}
	for _, part := range strings.Split(dir, "/") {
		switch part {
		case "", ".", "..":
			return "", malformed("path not within the repo directory")
		}
	}
	return dir, nil
}

// ListRepos returns the names of primary repos (directories directly under ReposRoot), sorted.
func (l Layout) ListRepos() ([]string, error) {
	entries, err := os.ReadDir(l.ReposRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var repos []string
	for _, e := range entries {
		if e.IsDir() {
			repos = append(repos, e.Name())
		}
	}
	sort.Strings(repos)
	return repos, nil
}
