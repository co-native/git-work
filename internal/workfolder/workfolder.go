// Package workfolder loads what a command run from inside a work folder needs:
// the state file found upward from the working directory, the config, and the
// layout the config resolves to.
package workfolder

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/state"
)

// Context is the work folder a command runs inside, with the config and
// layout it resolves paths through.
type Context struct {
	State  *state.State
	Dir    string // the work folder's directory
	Config *config.Config
	Layout layout.Layout
}

// Load finds the work folder from the working directory and loads the config.
func Load() (*Context, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	st, dir, err := state.FindUp(cwd)
	if err != nil {
		return nil, err
	}
	cfg, l, err := config.LoadLayout()
	if err != nil {
		return nil, err
	}
	if err := RefuseInsideWorktrees(cwd, dir); err != nil {
		return nil, err
	}
	return &Context{State: st, Dir: dir, Config: cfg, Layout: l}, nil
}

// RefuseInsideWorktrees rejects a folder-scoped command invoked from inside a
// linked worktree under <work>/.worktrees. Those are subagent or integrate
// worktrees; every such command resolves the main checkout from state, so
// running one here would act on a different tree than the caller is looking
// at. Plain git is the right tool inside them.
func RefuseInsideWorktrees(cwd, workDir string) error {
	rel, err := filepath.Rel(workDir, cwd)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	first := strings.Split(rel, string(filepath.Separator))[0]
	if first != layout.WorktreesDirName {
		return nil
	}
	return cli.Usagef("refusing to run from inside %s: git-work commands act on the main checkout, not this linked worktree; use plain git here, or run the command from %s", cwd, workDir)
}

// Mode resolves the work folder's checkout mode. State files written before
// modes existed load as worktree.
func (c *Context) Mode() layout.Mode {
	return c.State.Mode.OrDefault()
}

// RepoDir returns the directory holding a repo in this work folder: the
// primary clone in worktree mode, or the full clone at <work>/<repo> in clone
// mode. It is the single resolver commands use instead of assuming a primary.
func (c *Context) RepoDir(name string) (string, error) {
	if c.Mode() == layout.ModeClone {
		return filepath.Join(c.Dir, name), nil
	}
	return c.Layout.RepoMain(name)
}

// WorktreesDir returns <work>/.worktrees.
func (c *Context) WorktreesDir() string {
	return c.Layout.WorktreesDir(c.Dir)
}

// IntegrateInProgress reports whether clone-mode integrate has a temp worktree
// for repo, i.e. a conflict is waiting to be resolved or aborted.
func IntegrateInProgress(l layout.Layout, workDir, name string) bool {
	_, err := os.Stat(l.IntegrateWorktree(workDir, name))
	return err == nil
}

// RepoDirFor is Context.RepoDir for callers that already hold a state, layout
// and work directory rather than a Context (the shared teardown path and the
// test-driven command helpers).
func RepoDirFor(l layout.Layout, st *state.State, workDir, name string) (string, error) {
	if st.Mode.OrDefault() == layout.ModeClone {
		return filepath.Join(workDir, name), nil
	}
	return l.RepoMain(name)
}

// Scope resolves the repos a `<repo> | --all` command acts on: every repo in
// the work folder with all, else the single named one. Naming a repo the
// folder does not hold is a malformed invocation, not work that failed -
// nothing was attempted - so it is a usage error.
func Scope(st *state.State, name string, all bool) ([]state.Repo, error) {
	if all {
		return st.Repos, nil
	}
	r, ok := st.Repo(name)
	if !ok {
		return nil, cli.Usagef("repo %q is not part of %s", name, st.DisplayName())
	}
	return []state.Repo{r}, nil
}
