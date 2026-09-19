// Package workfolder loads what a command run from inside a work folder needs:
// the state file found upward from the working directory, the config, and the
// layout the config resolves to.
package workfolder

import (
	"os"

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
	return &Context{State: st, Dir: dir, Config: cfg, Layout: l}, nil
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
