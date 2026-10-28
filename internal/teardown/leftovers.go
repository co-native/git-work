package teardown

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/co-native/git-work/internal/layout"
)

// Leftovers returns the entries directly under <work>/.worktrees, sorted.
// They are git-work's integrate checkout or a subagent's linked worktree.
func Leftovers(workDir string) ([]string, error) {
	root := filepath.Join(workDir, layout.WorktreesDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// RepoLeftovers returns the entries under .worktrees that belong to repo: the
// integrate checkout for that repo and any agent worktree for it, as
// .worktrees-relative paths. It does not inspect other repos.
func RepoLeftovers(workDir, repo string) ([]string, error) {
	top, err := Leftovers(workDir)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(workDir, layout.WorktreesDirName)
	var out []string
	for _, name := range top {
		if name == "integrate-"+repo {
			out = append(out, name)
			continue
		}
		if !strings.HasPrefix(name, "agent-") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, repo)); err == nil {
			out = append(out, filepath.Join(name, repo))
		}
	}
	sort.Strings(out)
	return out, nil
}

// RemoveLeftovers deletes the given .worktrees-relative paths and prunes any
// agent-* directory left empty. It never removes the .worktrees root itself;
// the caller decides whether the folder goes too.
func RemoveLeftovers(workDir string, rels []string) error {
	root := filepath.Join(workDir, layout.WorktreesDirName)
	for _, rel := range rels {
		if err := os.RemoveAll(filepath.Join(root, rel)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "agent-") {
			continue
		}
		remaining, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			if err := os.Remove(filepath.Join(root, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
