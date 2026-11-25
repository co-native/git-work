// Package teardown holds the per-repo safety checks and worktree/branch
// removal shared by `git work done` and `git work rm`. It depends on
// repo/layout/state only - no tui, no flag parsing (prompting stays in the
// commands) - to keep the command->internal layering and avoid import cycles.
package teardown

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

// Options selects which branches a teardown deletes and how safety checks
// behave. Force overrides safety-check problems (and force-deletes worktrees).
// NoFetch skips the fetch before the containment check, so the check reads the
// origin refs as of the last fetch.
//
// Mode and the two resolvers make the shared path mode-aware without importing
// state/config here: RepoDir returns a primary or a clone, and RemoteURL is the
// fallback used to delete a remote branch after the clone is already gone.
type Options struct {
	DeleteLocal, DeleteRemote, Force, NoFetch bool
	Mode                                      layout.Mode
	RepoDir                                   func(name string) (string, error)
	RemoteURL                                 func(name string) (string, error)
}

func repoDir(o Options, l layout.Layout, name string) (string, error) {
	if o.RepoDir != nil {
		return o.RepoDir(name)
	}
	return l.RepoMain(name)
}

// Gate decides whether a run proceeds past the problems its checks found: an
// error naming them when force is unset, otherwise a note on stderr that the
// run continues despite them. The wording says "most", because the
// default-branch refusal is the one problem --force never overrides.
func Gate(problems []string, force bool) error {
	if len(problems) == 0 {
		return nil
	}
	if !force {
		return fmt.Errorf("aborting (most of these can be overridden with --force):\n  %s", strings.Join(problems, "\n  "))
	}
	fmt.Fprintf(os.Stderr, "git-work: proceeding despite %d problem(s) (--force)\n", len(problems))
	return nil
}

// CheckRepo runs one repo's safety checks (uncommitted / drift / contained)
// and returns this repo's problems. An error while running a check is a
// warning plus a counted problem (conservative), never a mid-check abort, so a
// partially torn-down folder can still be processed. --force overrides
// problems. A repo whose worktree is already gone is skipped.
//
// Containment is the gate on every path, with or without delete flags: the
// work branch's commits must be on the remote default branch, by ancestry or
// patch equivalence, so squash merges count. A branch that is not contained is
// a problem - teardown must neither forget unmerged work nor delete the only
// refs that hold it. The same verdict labels a squash merge on a delete run.
// The default-branch refusal, reported here for delete flags, is the one
// problem --force does not override.
//
// The repo is fetched first (unless NoFetch), so a merge that landed on origin
// since the last fetch is visible without a separate refresh. In worktree mode
// a no-flag run leaves the local branch in the primary after removing the
// checkout, so a warning names it. In clone mode the branch goes with the
// clone. Progress notes/warnings print to stderr.
func CheckRepo(o Options, l layout.Layout, r state.Repo, workDir string) []string {
	var problems []string
	addProblem := func(format string, a ...any) {
		problems = append(problems, fmt.Sprintf(format, a...))
	}
	warnProblem := func(format string, a ...any) {
		p := fmt.Sprintf(format, a...)
		fmt.Fprintf(os.Stderr, "git-work: warning: %s\n", p)
		problems = append(problems, p)
	}

	wt := filepath.Join(workDir, r.Name)
	if _, err := os.Stat(wt); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "git-work: %s: worktree already removed; skipping checks\n", r.Name)
		return problems
	}
	if dirty, err := repo.HasUncommittedChanges(wt); err != nil {
		warnProblem("%s: could not check for uncommitted changes: %v", r.Name, err)
	} else if dirty {
		addProblem("%s: uncommitted changes", r.Name)
	}

	// Teardown is where drift is most dangerous: containment and the deletions
	// both target r.Branch, while the worktree may be on another branch, so a
	// disagreement means checking one branch and then deleting a different one
	// - locally and on the remote. Report it before the checks it invalidates.
	// --force still overrides, like every other problem here.
	if msg, err := repo.WorktreeBranchProblem(wt, r.Branch); err != nil {
		warnProblem("%s: could not check the worktree's branch: %v", r.Name, err)
	} else if msg != "" {
		addProblem("%s: %s", r.Name, msg)
	}

	main, err := repoDir(o, l, r.Name)
	if err != nil {
		warnProblem("%s: %v", r.Name, err)
		return problems
	}
	if !o.NoFetch {
		if err := repo.Fetch(main); err != nil {
			warnProblem("%s: fetch failed: %v", r.Name, err)
		}
	}

	// Surface the default-branch refusal here too, after the fetch has had a
	// chance to freshen origin/HEAD. TeardownRepo enforces it regardless. This
	// is so the operator learns before any repo is torn down rather than
	// discovering it as a mid-run failure. Worded to say --force will not help,
	// since unlike every other problem here it will not.
	if o.DeleteLocal || o.DeleteRemote {
		if isDefault, derr := repo.IsDefaultBranchFor(main, r.DefaultBranch, r.Branch); derr != nil {
			warnProblem("%s: cannot determine the default branch: %v", r.Name, derr)
		} else if isDefault {
			addProblem("%s: %s is the repo's default branch and will not be deleted (--force does not override this)", r.Name, r.Branch)
		}
	}

	exists, err := repo.LocalBranchExists(main, r.Branch)
	if err != nil {
		warnProblem("%s: could not check branch %s: %v", r.Name, r.Branch, err)
		return problems
	}
	if !exists {
		return problems // nothing to verify: the branch is already gone
	}
	target, err := repo.RemoteDefaultRef(main)
	if err != nil {
		def, derr := repo.DefaultBranchFor(main, r.DefaultBranch)
		if derr != nil {
			warnProblem("%s: could not determine default branch: %v", r.Name, err)
			return problems
		}
		target = def
	}

	// Ancestry (IsMergedInto) misses squash merges (the squashed commit is a
	// new object), so a "not merged" result falls back to patch equivalence
	// (IsPatchEquivalent). The verdict gates every path and labels a squash
	// merge on a delete run.
	contained, squashMerged := false, false
	merged, err := repo.IsMergedInto(main, r.Branch, target)
	switch {
	case err != nil:
		warnProblem("%s: could not check if %s is merged into %s: %v", r.Name, r.Branch, target, err)
		return problems
	case merged:
		contained = true
	default:
		eq, eqErr := repo.IsPatchEquivalent(main, r.Branch, target)
		if eqErr != nil {
			warnProblem("%s: could not check if %s is squash-merged into %s: %v", r.Name, r.Branch, target, eqErr)
			return problems
		}
		contained, squashMerged = eq, eq
	}
	if !contained {
		addProblem("%s: branch %s not merged into %s", r.Name, r.Branch, target)
		return problems
	}

	if o.DeleteLocal || o.DeleteRemote {
		if squashMerged {
			fmt.Fprintf(os.Stderr, "git-work: %s: branch %s squash-merged into %s\n", r.Name, r.Branch, target)
		}
		return problems
	}
	if o.Mode.OrDefault() == layout.ModeWorktree {
		fmt.Fprintf(os.Stderr, "git-work: %s: local branch %s left in place in the primary\n", r.Name, r.Branch)
	}
	return problems
}

// TeardownRepo removes r's worktree then (per flags) its local/remote branches
// - strictly worktree-then-branch. Missing worktrees and already-deleted
// branches are skipped with a note so teardown is idempotent. On full success
// for this repo (zero failures) it drops r from st and persists with
// st.Save(workDir), so state stays honest even when other repos block or a
// later run resumes. Returns this repo's failures. Progress notes print to
// stderr.
func TeardownRepo(o Options, l layout.Layout, st *state.State, r state.Repo, workDir string) []string {
	var failures []string
	fail := func(format string, a ...any) {
		failures = append(failures, fmt.Sprintf(format, a...))
	}
	main, err := repoDir(o, l, r.Name)
	if err != nil {
		fail("%s: %v", r.Name, err)
		return failures
	}
	// A work branch is never a repo's default branch, and this is where that
	// would cost the most - the deletions below remove it locally and on
	// origin. Checked before the worktree is touched so a refusal leaves the
	// repo exactly as it was, and deliberately not overridable by Force:
	// --force exists for judgment calls about unmerged work, and deleting a
	// repo's default branch is not a judgment call. `git work git <repo> --
	// branch -D <name>` is the honest bypass. Only gated on the delete flags:
	// removing a worktree that sits on the default branch harms nothing, since
	// the branch itself survives in the primary.
	if o.DeleteLocal || o.DeleteRemote {
		isDefault, derr := repo.IsDefaultBranchFor(main, r.DefaultBranch, r.Branch)
		switch {
		case derr != nil:
			fail("%s: cannot determine the default branch (%v); refusing to delete %s", r.Name, derr, r.Branch)
			return failures
		case isDefault:
			fail("%s: %s is the repo's default branch; refusing to delete it (--force does not override this)", r.Name, r.Branch)
			return failures
		}
	}
	wt := filepath.Join(workDir, r.Name)
	if o.Mode == layout.ModeClone {
		_, statErr := os.Stat(wt)
		cloneExists := statErr == nil
		if o.DeleteRemote {
			var url string
			if !cloneExists {
				if o.RemoteURL == nil {
					fail("%s: clone is gone and no URL is configured to delete remote branch %s", r.Name, r.Branch)
					return failures
				}
				url, err = o.RemoteURL(r.Name)
				if err != nil {
					fail("%s: %v", r.Name, err)
					return failures
				}
			}
			exists := false
			if cloneExists {
				exists, err = repo.RemoteBranchExists(main, r.Branch)
			} else {
				exists, err = repo.RemoteBranchExistsAt(url, r.Branch)
			}
			switch {
			case err != nil:
				fail("%s: check remote branch %s: %v", r.Name, r.Branch, err)
				return failures
			case !exists:
				fmt.Fprintf(os.Stderr, "git-work: %s: remote branch %s already deleted; skipping\n", r.Name, r.Branch)
			case cloneExists:
				if err := repo.DeleteRemoteBranch(main, r.Branch); err != nil {
					fail("%s: delete remote branch %s: %v", r.Name, r.Branch, err)
					return failures // keep the clone so a re-run can retry
				}
			default:
				if err := repo.DeleteRemoteBranchURL(url, r.Branch); err != nil {
					fail("%s: delete remote branch %s: %v", r.Name, r.Branch, err)
					return failures
				}
			}
		}
		if o.DeleteLocal {
			fmt.Fprintf(os.Stderr, "git-work: %s: local branch %s is removed with the clone\n", r.Name, r.Branch)
		}
		if cloneExists {
			if err := os.RemoveAll(wt); err != nil {
				fail("%s: remove clone: %v", r.Name, err)
				return failures
			}
		} else {
			fmt.Fprintf(os.Stderr, "git-work: %s: clone already removed\n", r.Name)
		}
		st.RemoveRepo(r.Name)
		if err := st.Save(workDir); err != nil {
			fail("%s: save state: %v", r.Name, err)
		}
		return failures
	}

	if _, err := os.Stat(wt); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "git-work: %s: worktree already removed; pruning stale registration\n", r.Name)
		if err := repo.PruneWorktrees(main); err != nil {
			fail("%s: prune worktrees: %v", r.Name, err)
			return failures // worktree may still be registered; leave branches alone
		}
	} else if err := repo.RemoveWorktree(main, wt, o.Force); err != nil {
		fail("%s: remove worktree: %v", r.Name, err)
		return failures // keep worktree-then-branch order: leave branches alone
	}
	if o.DeleteLocal {
		exists, err := repo.LocalBranchExists(main, r.Branch)
		switch {
		case err != nil:
			fail("%s: check local branch %s: %v", r.Name, r.Branch, err)
		case !exists:
			fmt.Fprintf(os.Stderr, "git-work: %s: local branch %s already deleted; skipping\n", r.Name, r.Branch)
		default:
			if err := repo.DeleteLocalBranch(main, r.Branch, o.Force); err != nil {
				// `git branch -d` refuses a squash-merged branch, as the
				// ancestry check does. The containment check
				// accepts patch equivalence, so deletion must too:
				// re-verify and force-delete.
				if !o.Force && squashMergedIntoDefault(main, r.Branch) {
					fmt.Fprintf(os.Stderr, "git-work: %s: branch %s squash-merged; force-deleting\n", r.Name, r.Branch)
					err = repo.DeleteLocalBranch(main, r.Branch, true)
				}
				if err != nil {
					fail("%s: delete local branch %s: %v", r.Name, r.Branch, err)
				}
			}
		}
	}
	if o.DeleteRemote {
		exists, err := repo.RemoteBranchExists(main, r.Branch)
		switch {
		case err != nil:
			fail("%s: check remote branch %s: %v", r.Name, r.Branch, err)
		case !exists:
			fmt.Fprintf(os.Stderr, "git-work: %s: remote branch %s already deleted; skipping\n", r.Name, r.Branch)
		default:
			if err := repo.DeleteRemoteBranch(main, r.Branch); err != nil {
				fail("%s: delete remote branch %s: %v", r.Name, r.Branch, err)
			}
		}
	}

	// Only a fully successful teardown drops the repo from state. A partial or
	// blocked one leaves it listed so a re-run resumes precisely it.
	if len(failures) == 0 {
		st.RemoveRepo(r.Name)
		if err := st.Save(workDir); err != nil {
			fail("%s: save state: %v", r.Name, err)
		}
	}
	return failures
}

// squashMergedIntoDefault reports whether branch is patch-equivalent to the
// remote default branch - the squash-merge case where `git branch -d`
// refuses although the containment check passed. Errors read as "not
// squash-merged". The caller then reports the original delete failure.
func squashMergedIntoDefault(main, branch string) bool {
	target, err := repo.RemoteDefaultRef(main)
	if err != nil {
		return false
	}
	eq, err := repo.IsPatchEquivalent(main, branch, target)
	return err == nil && eq
}
