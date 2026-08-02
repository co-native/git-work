// Package teardown holds the per-repo safety checks and worktree/branch
// removal shared by `git work done` and `git work rm`. It depends on
// repo/layout/state only - no tui, no flag parsing (prompting stays in the
// commands) - to keep the command->internal layering and avoid import cycles.
package teardown

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

// Options selects which branches a teardown deletes and how safety checks
// behave. Force overrides safety-check problems (and force-deletes worktrees);
// NoFetch skips the pre-merged-check fetch.
type Options struct{ DeleteLocal, DeleteRemote, Force, NoFetch bool }

// CheckRepo runs one repo's safety checks (uncommitted / unpushed /
// merged-or-contained) and returns this repo's problems. An error while
// RUNNING a check is a warning plus a counted problem (conservative), never a
// mid-check abort, so a partially torn-down folder can still be processed;
// --force overrides problems. A repo whose worktree is already gone is skipped.
//
// The unpushed check treats a branch with no upstream as unpushed. When BOTH
// DeleteLocal and DeleteRemote are set and the branch is verified contained in
// the remote default branch (ancestry or patch equivalence - the same verdict
// the merged check uses, computed once), the unpushed problem is suppressed and
// a note is emitted instead: the never-pushed commits are already merged. With
// only one delete flag, no delete flag, or when containment cannot be verified,
// the unpushed problem fires as before. Progress notes/warnings print to stderr.
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

	// Teardown is where drift is most dangerous: the unpushed check below
	// measures the WORKTREE's HEAD while the deletions below target r.Branch,
	// so a disagreement means safety-checking one branch and then deleting a
	// different one - locally and on the remote. Report it before the checks
	// it invalidates. --force still overrides, like every other problem here.
	if msg, err := repo.WorktreeBranchProblem(wt, r.Branch); err != nil {
		warnProblem("%s: could not check the worktree's branch: %v", r.Name, err)
	} else if msg != "" {
		addProblem("%s: %s", r.Name, msg)
	}

	// The unpushed check treats a branch with no upstream as unpushed, so
	// a branch that was squash-merged and never pushed trips it. When both
	// delete flags are set and the branch is verified contained in the
	// remote default branch (the same containment the merged check below
	// establishes), that unpushed work is already merged and the problem
	// is suppressed in favor of a note. Compute the result now but report
	// it only once containment is known: reportUnpushed fires the problem
	// (or a check-error problem) in every path where containment is not
	// verified.
	unpushed, unpushedErr := repo.HasUnpushedCommits(wt)
	reportUnpushed := func() {
		if unpushedErr != nil {
			warnProblem("%s: could not check for unpushed commits: %v", r.Name, unpushedErr)
		} else if unpushed {
			addProblem("%s: unpushed commits", r.Name)
		}
	}

	if !o.DeleteLocal && !o.DeleteRemote {
		reportUnpushed()
		return problems
	}

	main, err := l.RepoMain(r.Name)
	if err != nil {
		warnProblem("%s: %v", r.Name, err)
		reportUnpushed()
		return problems
	}
	if !o.NoFetch {
		if err := repo.Fetch(main); err != nil {
			warnProblem("%s: fetch failed: %v", r.Name, err)
		}
	}
	// Surface the default-branch refusal here too, after the fetch has had a
	// chance to freshen origin/HEAD. TeardownRepo enforces it regardless; this
	// is so the operator learns before any repo is torn down rather than
	// discovering it as a mid-run failure. Worded to say --force will not help,
	// since unlike every other problem here it genuinely will not.
	if isDefault, derr := repo.IsDefaultBranch(main, r.Branch); derr != nil {
		warnProblem("%s: cannot determine the default branch: %v", r.Name, derr)
	} else if isDefault {
		addProblem("%s: %s is the repo's default branch and will not be deleted (--force does not override this)", r.Name, r.Branch)
	}
	exists, err := repo.LocalBranchExists(main, r.Branch)
	if err != nil {
		warnProblem("%s: could not check branch %s: %v", r.Name, r.Branch, err)
		reportUnpushed()
		return problems
	}
	if !exists {
		reportUnpushed()
		return problems // nothing to delete in this repo, nothing to verify
	}
	target, err := repo.RemoteDefaultRef(main)
	if err != nil {
		warnProblem("%s: could not determine default branch: %v", r.Name, err)
		reportUnpushed()
		return problems
	}

	// Verify containment once and share the verdict between the unpushed
	// suppression and the merged check. Ancestry (IsMergedInto) misses
	// squash merges (the squashed commit is a new object), so a "not
	// merged" ancestry result falls back to patch equivalence
	// (IsPatchEquivalent) - a branch whose combined changes target already
	// contains is merged all the same. IsPatchEquivalent runs at most once
	// and its result feeds both checks below.
	contained, squashMerged := false, false
	merged, err := repo.IsMergedInto(main, r.Branch, target)
	switch {
	case err != nil:
		warnProblem("%s: could not check if %s is merged into %s: %v", r.Name, r.Branch, target, err)
		reportUnpushed()
		return problems
	case merged:
		contained = true
	default:
		eq, eqErr := repo.IsPatchEquivalent(main, r.Branch, target)
		if eqErr != nil {
			warnProblem("%s: could not check if %s is squash-merged into %s: %v", r.Name, r.Branch, target, eqErr)
			reportUnpushed()
			return problems
		}
		contained, squashMerged = eq, eq
	}

	// Unpushed suppression: only when both delete flags are set and the
	// branch is verified contained. A check error always reports (via
	// reportUnpushed); only a genuine "unpushed commits" problem is
	// swapped for a note.
	if o.DeleteLocal && o.DeleteRemote && contained && unpushedErr == nil && unpushed {
		fmt.Fprintf(os.Stderr, "git-work: %s: branch %s contained in %s; unpushed commits already merged, not blocking teardown\n", r.Name, r.Branch, target)
	} else {
		reportUnpushed()
	}

	// Merged check: squash-merged branches are labeled (and force-deleted
	// at teardown since `git branch -d` refuses them); ancestry-merged
	// branches pass silently; anything else is genuinely unmerged.
	switch {
	case squashMerged:
		fmt.Fprintf(os.Stderr, "git-work: %s: branch %s squash-merged into %s\n", r.Name, r.Branch, target)
	case contained:
		// merged by ancestry; nothing to report
	default:
		addProblem("%s: branch %s not merged into %s", r.Name, r.Branch, target)
	}
	return problems
}

// TeardownRepo removes r's worktree then (per flags) its local/remote branches
// - strictly worktree-then-branch. Missing worktrees and already-deleted
// branches are skipped with a note so teardown is idempotent. On FULL success
// for this repo (zero failures) it drops r from st and persists with
// st.Save(workDir), so state stays honest even when other repos block or a
// later run resumes. Returns this repo's failures. Progress notes print to
// stderr.
func TeardownRepo(o Options, l layout.Layout, st *state.State, r state.Repo, workDir string) []string {
	var failures []string
	fail := func(format string, a ...any) {
		failures = append(failures, fmt.Sprintf(format, a...))
	}
	main, err := l.RepoMain(r.Name)
	if err != nil {
		fail("%s: %v", r.Name, err)
		return failures
	}
	// A work branch is never a repo's default branch, and this is where that
	// would cost the most - the deletions below remove it locally and on
	// origin. Checked before the worktree is touched so a refusal leaves the
	// repo exactly as it was, and deliberately NOT overridable by Force:
	// --force exists for judgment calls about unmerged work, and deleting a
	// repo's default branch is not a judgment call. `git work git <repo> --
	// branch -D <name>` is the honest bypass. Only gated on the delete flags:
	// removing a worktree that sits on the default branch harms nothing, since
	// the branch itself survives in the primary.
	if o.DeleteLocal || o.DeleteRemote {
		isDefault, derr := repo.IsDefaultBranch(main, r.Branch)
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
				// `git branch -d` refuses a squash-merged branch just
				// like the ancestry check does. The merged check
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

	// Only a fully successful teardown drops the repo from state; a partial
	// or blocked one leaves it listed so a re-run resumes precisely it.
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
// refuses although the merged check passed. Errors read as "not
// squash-merged"; the caller then reports the original delete failure.
func squashMergedIntoDefault(main, branch string) bool {
	target, err := repo.RemoteDefaultRef(main)
	if err != nil {
		return false
	}
	eq, err := repo.IsPatchEquivalent(main, branch, target)
	return err == nil && eq
}
