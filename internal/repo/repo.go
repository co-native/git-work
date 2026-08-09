package repo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// RemoteDefaultRef returns the remote default branch ref (e.g. "origin/main")
// from origin/HEAD. There is no fallback: an unset origin/HEAD is an error
// the caller surfaces as a warning.
func RemoteDefaultRef(mainDir string) (string, error) {
	out, err := git(mainDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("origin/HEAD not set (run `git remote set-head origin --auto`): %w", err)
	}
	return strings.TrimSpace(out), nil
}

// IsDefaultBranch reports whether branch is the repo's default branch.
//
// origin/HEAD is the sound source and is tried first: it is independent of what
// the primary has checked out, which matters because a primary left on another
// branch is exactly the drifted layout in which a worktree can hold the default
// branch at all. DefaultBranchName is deliberately not used here - its fallback
// names the primary's checked-out branch, which in that drifted case is the
// wrong answer in the one situation the guard exists for.
//
// A repo with no origin/HEAD (local-only, or a remote that never set it) has no
// upstream default to read, so the primary's own branch stands in: a primary
// sits on its default branch by design. That keeps "cannot tell" from becoming
// "cannot delete anything" for repos that simply have no remote.
func IsDefaultBranch(mainDir, branch string) (bool, error) {
	if ref, err := RemoteDefaultRef(mainDir); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(ref), "origin/") == branch, nil
	}
	cur, err := CurrentBranch(mainDir)
	if err != nil {
		return false, fmt.Errorf("determine default branch: %w", err)
	}
	return cur == branch, nil
}

// DefaultBranchProblem reports why branch must not serve as a work branch for
// the repo at mainDir, or "" when it may. Callers prefix it with the repo name.
//
// A work branch is never a repo's default branch, because every teardown safety
// check is trivially satisfied by it: the default branch is merged into
// origin/HEAD by definition, has an upstream with nothing unpushed, and agrees
// with a worktree sitting on it. Nothing else stands between
// `done --delete-local --delete-remote` and deleting it locally and on origin.
func DefaultBranchProblem(mainDir, branch string) (string, error) {
	isDefault, err := IsDefaultBranch(mainDir, branch)
	if err != nil || !isDefault {
		return "", err
	}
	return fmt.Sprintf("%s is the repo's default branch and cannot be a work branch; create one first (`git -C <worktree> switch -c <name>`), or let `git work add` create it", branch), nil
}

// Clone sets up the primary-clone layout at repoDir:
//
//	repoDir/<default>  = real clone, named after the upstream default branch
//	repoDir/.git       = "gitdir: ./<default>/.git"
//	core.worktree set to ".."
//
// extraOpts are passed to `git clone` before the url.
func Clone(url, repoDir string, extraOpts []string) (err error) {
	if _, serr := os.Stat(repoDir); serr == nil {
		return fmt.Errorf("target already exists: %s", repoDir)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}
	// This function created repoDir, so any failure from here on removes it
	// again: a poisoned target (empty dir, or a clone stranded at the temp
	// name) would make every retry fail with "target already exists".
	defer func() {
		if err != nil {
			os.RemoveAll(repoDir)
		}
	}()
	// Clone into a temp name first: the primary dir is named after the
	// upstream default branch, which is only known once the clone exists.
	tmp := filepath.Join(repoDir, ".clone-tmp")
	args := append([]string{"clone"}, extraOpts...)
	args = append(args, url, tmp)
	cmd := exec.Command("git", args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	name, err := DefaultBranchName(tmp)
	if err != nil {
		return err
	}
	primary := filepath.Join(repoDir, name)
	if err := os.MkdirAll(filepath.Dir(primary), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, primary); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ./"+name+"/.git\n"), 0o644); err != nil {
		return err
	}
	if _, err := git(primary, "config", "core.worktree", ".."); err != nil {
		return err
	}
	return nil
}

// DefaultBranchName returns the default branch name of a clone: the upstream
// default from origin/HEAD (which `git clone` sets), falling back to the
// local HEAD when the remote has no HEAD (e.g. an empty or local-only repo).
func DefaultBranchName(dir string) (string, error) {
	if ref, err := RemoteDefaultRef(dir); err == nil {
		return strings.TrimPrefix(ref, "origin/"), nil
	}
	out, err := git(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("determine default branch: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// CurrentBranch returns the branch checked out in dir (the symbolic HEAD);
// a detached HEAD is an error.
func CurrentBranch(dir string) (string, error) {
	out, err := git(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("determine checked-out branch (detached HEAD?): %w", err)
	}
	return strings.TrimSpace(out), nil
}

// PullResult classifies a successful PullFFOnly.
type PullResult int

const (
	PullUpToDate      PullResult = iota // already at the upstream tip
	PullFastForwarded                   // fast-forwarded to the upstream tip
)

// String returns the per-repo report label callers print for a pull result.
func (r PullResult) String() string {
	if r == PullFastForwarded {
		return "fast-forwarded"
	}
	return "already up to date"
}

// PullFFOnly fast-forwards the current branch in dir from its upstream; a
// pull that would need a merge or rebase (a diverged branch) fails instead
// of touching the tree. On success the result says whether HEAD moved.
func PullFFOnly(dir string) (PullResult, error) {
	before, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return PullUpToDate, err
	}
	if _, err := git(dir, "pull", "--ff-only"); err != nil {
		return PullUpToDate, err
	}
	after, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return PullUpToDate, err
	}
	if strings.TrimSpace(after) == strings.TrimSpace(before) {
		return PullUpToDate, nil
	}
	return PullFastForwarded, nil
}

// PullBranchFFOnly fast-forwards branch in dir from origin. When branch is
// the checked-out branch this is a plain ff-only pull (working tree
// included); when the clone sits on another branch (or a detached HEAD) the
// branch ref is fast-forwarded via `git fetch origin <branch>:<branch>`
// instead, which never touches the working tree - so pulling a primary's
// default branch keeps freshening the rebase/merge target even when the
// primary was left on a different branch. A non-forced fetch refspec
// refuses non-fast-forward updates, matching --ff-only.
func PullBranchFFOnly(dir, branch string) (PullResult, error) {
	if cur, err := CurrentBranch(dir); err == nil && cur == branch {
		return PullFFOnly(dir)
	}
	ref := "refs/heads/" + branch
	before, err := git(dir, "rev-parse", ref)
	if err != nil {
		return PullUpToDate, err
	}
	if _, err := git(dir, "fetch", "origin", branch+":"+branch); err != nil {
		return PullUpToDate, err
	}
	after, err := git(dir, "rev-parse", ref)
	if err != nil {
		return PullUpToDate, err
	}
	if strings.TrimSpace(after) == strings.TrimSpace(before) {
		return PullUpToDate, nil
	}
	return PullFastForwarded, nil
}

// Checkout checks out branch in dir.
func Checkout(dir, branch string) error {
	_, err := git(dir, "checkout", branch)
	return err
}

// Fetch runs `git fetch --all` in a clone/worktree dir.
func Fetch(dir string) error {
	_, err := git(dir, "fetch", "--all", "--prune")
	return err
}

// AddWorktree adds a worktree at path. If create is true a new branch `branch`
// is created (optionally from startPoint, default current HEAD); otherwise the
// existing branch is checked out.
func AddWorktree(mainDir, path, branch, startPoint string, create bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	args := []string{"worktree", "add"}
	if create {
		args = append(args, "-b", branch, path)
		if startPoint != "" {
			args = append(args, startPoint)
		}
	} else {
		args = append(args, path, branch)
	}
	_, err := git(mainDir, args...)
	return err
}

// Worktree is one entry of `git worktree list`: the checkout path and its
// branch (short name; empty when the HEAD is detached).
type Worktree struct {
	Path   string
	Branch string
}

// ListWorktrees returns the repo's worktrees from
// `git worktree list --porcelain`. The main worktree (the primary clone
// itself) is always listed first, followed by the linked worktrees.
func ListWorktrees(mainDir string) ([]Worktree, error) {
	out, err := git(mainDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			wts = append(wts, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
		case strings.HasPrefix(line, "branch ") && len(wts) > 0:
			wts[len(wts)-1].Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	return wts, nil
}

// CommonGitDir returns the repo's common git directory as an absolute path -
// for a linked worktree that is the primary clone's .git directory.
func CommonGitDir(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p), nil
}

// CoreWorktree returns the repo's core.worktree config value, or "" when
// unset (`git config --get` exits 1 for a missing key; any other failure is
// a real error).
func CoreWorktree(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "config", "--get", "core.worktree")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("git config --get core.worktree: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// SetCoreWorktree sets the repo's core.worktree config value (the managed
// primary-clone layout uses ".." so git works from the repo's parent dir too).
func SetCoreWorktree(dir, value string) error {
	_, err := git(dir, "config", "core.worktree", value)
	return err
}

// RemoveWorktree removes a worktree (force when needed) and prunes metadata.
func RemoveWorktree(mainDir, path string, force bool) error {
	args := []string{"worktree", "remove", path}
	if force {
		args = append(args, "--force")
	}
	if _, err := git(mainDir, args...); err != nil {
		return err
	}
	_, err := git(mainDir, "worktree", "prune")
	return err
}

// HasUncommittedChanges reports whether the worktree has staged/unstaged changes.
func HasUncommittedChanges(dir string) (bool, error) {
	out, err := git(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// HasUnpushedCommits reports whether the current branch has commits not on its
// upstream. A branch with no upstream is treated as having unpushed work.
func HasUnpushedCommits(dir string) (bool, error) {
	if _, err := git(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err != nil {
		return true, nil // no upstream configured
	}
	out, err := git(dir, "rev-list", "@{upstream}..HEAD", "--count")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "0", nil
}

// IsMergedInto reports whether branch is an ancestor of target in mainDir.
// merge-base exits 1 for "not an ancestor"; any other failure (unknown ref,
// corrupt repo) is returned as an error instead of reading as "not merged".
func IsMergedInto(mainDir, branch, target string) (bool, error) {
	cmd := exec.Command("git", "-C", mainDir, "merge-base", "--is-ancestor", branch, target)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %v: %s",
			branch, target, err, strings.TrimSpace(errb.String()))
	}
	return true, nil
}

// IsPatchEquivalent reports whether target already contains the changes of
// branch since their merge-base - the merged-without-ancestry cases the
// ancestry check (IsMergedInto) misses. A branch with no commits since the
// merge-base is trivially contained and reports true.
//
// It recognizes two merge shapes:
//
//  1. Per-commit: `git cherry` marks each commit in base..branch "-" when
//     target already has a patch-equivalent commit and "+" when it does not.
//     When none are "+", every commit was carried into target individually -
//     integrated one by one, rebased, cherry-picked, or fast-forwarded - so the
//     branch is contained. This survives target advancing past the merge with
//     follow-up commits (patch-ids of the original commits still match), but
//     it does NOT catch a squash merge: the branch's individual commits have
//     no counterpart to the single squashed commit.
//
//  2. Squash: the whole branch collapsed into one commit whose diff equals the
//     branch's combined change. Squashing branch's changes into a temporary
//     dangling commit (never referenced; gc collects it) and asking `git
//     cherry` whether target has a patch-equivalent commit catches this, but
//     misses shape 1 when the merge left more than one commit or target moved
//     on afterward. The two are complementary, so both are tried.
//
// A final backstop (mergeAbsorbedInto) catches the MIX shape neither cherry
// path recognizes - some commits squashed together, others applied
// individually - via an in-memory three-way merge that absorbs cleanly into
// target's tree.
func IsPatchEquivalent(mainDir, branch, target string) (bool, error) {
	base, err := git(mainDir, "merge-base", branch, target)
	if err != nil {
		return false, err
	}
	base = strings.TrimSpace(base)
	tip, err := git(mainDir, "rev-parse", branch+"^{commit}")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(tip) == base {
		return true, nil // no commits since merge-base
	}

	// Shape 1: every commit in base..branch is patch-present in target.
	perCommit, err := git(mainDir, "cherry", target, branch, base)
	if err != nil {
		return false, err
	}
	if cherryFullyMerged(perCommit) {
		return true, nil
	}

	tree, err := git(mainDir, "rev-parse", branch+"^{tree}")
	if err != nil {
		return false, err
	}
	baseTree, err := git(mainDir, "rev-parse", base+"^{tree}")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(tree) == strings.TrimSpace(baseTree) {
		// Net-zero branch: its commits sum to an empty diff (e.g. a change
		// followed by its revert). An empty probe commit would confuse
		// `git cherry` (it cannot patch-id an empty diff and reports it as
		// unmerged), but a branch with no effective changes is trivially
		// contained.
		return true, nil
	}
	squashed, err := git(mainDir, "-c", "user.name=git-work", "-c", "user.email=git-work@localhost",
		"commit-tree", strings.TrimSpace(tree), "-p", base, "-m", "git-work patch-equivalence probe")
	if err != nil {
		return false, err
	}
	// cherry prints one line for the probe commit: "- <sha>" when target has
	// a patch-equivalent commit, "+ <sha>" when it does not.
	out, err := git(mainDir, "cherry", target, strings.TrimSpace(squashed), base)
	if err != nil {
		return false, err
	}
	if strings.HasPrefix(strings.TrimSpace(out), "-") {
		return true, nil
	}

	// Final backstop: an in-memory three-way merge that absorbs cleanly into
	// target's tree means the branch's content is already fully present even
	// as a MIX of squashed and individually-applied commits - a shape neither
	// cherry path recognizes (PROJ-3 api).
	return mergeAbsorbedInto(mainDir, branch, target)
}

// cherryUnmerged parses `git cherry` output, returning the SHAs of commits
// marked "+" (no patch-equivalent commit in the upstream), oldest first, and
// whether any commit line was seen at all.
func cherryUnmerged(cherryOut string) (plus []string, sawCommit bool) {
	for _, ln := range strings.Split(cherryOut, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		sawCommit = true
		if strings.HasPrefix(ln, "+") {
			plus = append(plus, strings.TrimSpace(strings.TrimPrefix(ln, "+")))
		}
	}
	return plus, sawCommit
}

// cherryFullyMerged reports whether `git cherry` output shows every listed
// commit already patch-present in the upstream: at least one commit line and
// none marked "+" (unmerged). Empty output (no commits listed) reports false
// so the caller falls through to the squash probe rather than treating an
// unexpected empty result as merged.
func cherryFullyMerged(cherryOut string) bool {
	plus, sawCommit := cherryUnmerged(cherryOut)
	return sawCommit && len(plus) == 0
}

// mergeAbsorbedInto reports whether merging branch into target contributes
// nothing: an in-memory three-way merge (git merge-tree --write-tree, git
// >=2.38) whose result tree equals target's tree. merge-tree exits 1 on
// conflict, which the git() helper turns into an error, so this uses the
// exec.ExitError code-1 pattern from IsMergedInto but never bubbles: a
// conflict, a too-old git without --write-tree, or any other failure all
// report (false, nil) so the caller falls back to the other signals. A
// bubbled error would reach done's warnProblem and add a spurious problem to
// a genuinely-unmerged branch.
func mergeAbsorbedInto(mainDir, branch, target string) (bool, error) {
	cmd := exec.Command("git", "-C", mainDir, "merge-tree", "--write-tree", target, branch)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	targetTree, err := git(mainDir, "rev-parse", target+"^{tree}")
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(out.String()) == strings.TrimSpace(targetTree), nil
}

// A Commit is one commit identified for integrating: its full SHA plus the
// subject line callers display when reporting what will (or would) move.
type Commit struct {
	SHA     string
	Subject string
}

// CommitsNotIn returns the commits on branch not yet patch-present in
// target - the "+" lines of `git cherry target branch base`, base =
// merge-base(branch, target) - oldest first. An empty slice means target
// already has all of branch's content.
//
// Deliberately named for what it computes rather than for a caller: with
// target = the default branch it yields un-integrated commits, but `push`
// also calls it with target = origin/<default> to count unpushed ones.
func CommitsNotIn(mainDir, branch, target string) ([]Commit, error) {
	base, err := git(mainDir, "merge-base", branch, target)
	if err != nil {
		return nil, err
	}
	out, err := git(mainDir, "cherry", target, branch, strings.TrimSpace(base))
	if err != nil {
		return nil, err
	}
	plus, _ := cherryUnmerged(out)
	commits := make([]Commit, 0, len(plus))
	for _, sha := range plus {
		subject, err := git(mainDir, "show", "-s", "--format=%s", sha)
		if err != nil {
			return nil, err
		}
		commits = append(commits, Commit{SHA: sha, Subject: strings.TrimSpace(subject)})
	}
	return commits, nil
}

// CommitsAhead returns the commits on branch that are not on target - plain
// `git rev-list` ancestry, oldest first. Unlike CommitsNotIn it does no
// patch-equivalence filtering: a rebased or amended branch holds the same
// patches under new SHAs, and the ref still needs a push to move, so those
// commits must count. CommitsNotIn answers "is the content in target";
// CommitsAhead answers "does target's ref need updating".
func CommitsAhead(mainDir, branch, target string) ([]Commit, error) {
	out, err := git(mainDir, "rev-list", "--reverse", target+".."+branch)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, sha := range strings.Fields(out) {
		subject, err := git(mainDir, "show", "-s", "--format=%s", sha)
		if err != nil {
			return nil, err
		}
		commits = append(commits, Commit{SHA: sha, Subject: strings.TrimSpace(subject)})
	}
	return commits, nil
}

// CherryPickStatus classifies CherryPick's outcome.
type CherryPickStatus int

const (
	CherryPickOK       CherryPickStatus = iota // applied a new commit
	CherryPickEmpty                            // patch already present; nothing to apply
	CherryPickConflict                         // left in progress for manual resolution
)

// CherryPick applies commit sha onto the branch currently checked out in dir.
// A clean apply is CherryPickOK. Conflict detection mirrors rebaseInProgress:
// it stats `git rev-parse --git-path CHERRY_PICK_HEAD` (a single-commit pick
// leaves no sequencer dir). On failure with CHERRY_PICK_HEAD present, a clean
// worktree means the pick was empty - the patch is already present - so
// CherryPick clears the in-progress state with `git cherry-pick --skip` and
// reports CherryPickEmpty; a dirty worktree is a real conflict left in
// progress (CherryPickConflict). Any other failure is an error. After a
// CherryPickOK or CherryPickEmpty return the repo is NOT mid-sequence; only
// CherryPickConflict leaves it in progress.
func CherryPick(dir, sha string) (CherryPickStatus, error) {
	_, err := git(dir, "cherry-pick", sha)
	if err == nil {
		return CherryPickOK, nil
	}
	inProgress, perr := cherryPickInProgress(dir)
	if perr != nil {
		return CherryPickOK, perr
	}
	if !inProgress {
		return CherryPickOK, err
	}
	dirty, derr := HasUncommittedChanges(dir)
	if derr != nil {
		return CherryPickOK, derr
	}
	if dirty {
		return CherryPickConflict, nil
	}
	// Empty pick (patch already present): clear the in-progress state so a
	// sequential caller can proceed to the next commit.
	if _, serr := git(dir, "cherry-pick", "--skip"); serr != nil {
		return CherryPickOK, serr
	}
	return CherryPickEmpty, nil
}

// cherryPickInProgress reports whether dir has a cherry-pick in progress
// (CHERRY_PICK_HEAD present). Mirrors rebaseInProgress by stating the git-path
// directly rather than looking for a sequencer dir a single-commit pick never
// creates.
func cherryPickInProgress(dir string) (bool, error) {
	out, err := git(dir, "rev-parse", "--git-path", "CHERRY_PICK_HEAD")
	if err != nil {
		return false, err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if _, err := os.Stat(p); err == nil {
		return true, nil
	}
	return false, nil
}

// PushBranch runs `git push origin <branch>` from mainDir (mirrors
// DeleteRemoteBranch). No --force: fast-forward safety is the remote's to
// enforce.
func PushBranch(mainDir, branch string) error {
	_, err := git(mainDir, "push", "origin", branch)
	return err
}

// PushBranchUpstream runs `git push -u origin <branch>` from dir (a worktree),
// so a first push establishes @{upstream} regardless of the operator's
// push.autoSetupRemote setting. forceWithLease adds --force-with-lease - never
// bare --force: a lease rejection (the remote moved beyond our remote-tracking
// refs) surfaces as an error for the caller to report.
func PushBranchUpstream(dir, branch string, forceWithLease bool) error {
	args := []string{"push", "-u"}
	if forceWithLease {
		args = append(args, "--force-with-lease")
	}
	args = append(args, "origin", branch)
	_, err := git(dir, args...)
	return err
}

// FastForwardTo advances the branch checked out in dir to branch, which must
// be a descendant of it. `--ff-only` means git refuses rather than creating a
// merge commit when that does not hold, so a caller that checked ancestry
// first can treat a failure as a real error.
//
// Unlike a cherry-pick this creates no new objects: the commits keep their
// identities, so a work branch already pushed to origin matches the default
// branch exactly afterwards, and `done`'s merged check passes by plain
// ancestry instead of falling back to patch equivalence.
func FastForwardTo(dir, branch string) error {
	_, err := git(dir, "merge", "--ff-only", branch)
	return err
}

// RebaseStatus classifies the outcome of RebaseOnto.
type RebaseStatus int

const (
	RebaseOK       RebaseStatus = iota // rebased (or already up to date)
	RebaseDirty                        // uncommitted changes; rebase not attempted
	RebaseConflict                     // conflicts; rebase left in progress for --continue/--abort
)

// RebaseOnto rebases the branch checked out in dir onto target. A dirty
// worktree reports RebaseDirty without touching the repo. On conflict the
// rebase is left in progress (RebaseConflict) for normal
// `git rebase --continue/--abort` resolution. Any other git failure is an
// error.
func RebaseOnto(dir, target string) (RebaseStatus, error) {
	dirty, err := HasUncommittedChanges(dir)
	if err != nil {
		return RebaseOK, err
	}
	if dirty {
		return RebaseDirty, nil
	}
	if _, rerr := git(dir, "rebase", target); rerr != nil {
		inProgress, err := rebaseInProgress(dir)
		if err != nil {
			return RebaseOK, err
		}
		if inProgress {
			return RebaseConflict, nil
		}
		return RebaseOK, rerr
	}
	return RebaseOK, nil
}

// rebaseInProgress reports whether dir has a rebase in progress.
func rebaseInProgress(dir string) (bool, error) {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		exists, err := gitPathExists(dir, name)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

// gitPathExists reports whether a path inside dir's git dir exists, resolving
// it with `git rev-parse --git-path` so linked worktrees (whose git dir is not
// dir/.git) answer for themselves.
func gitPathExists(dir, name string) (bool, error) {
	out, err := git(dir, "rev-parse", "--git-path", name)
	if err != nil {
		return false, err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	_, statErr := os.Stat(p)
	return statErr == nil, nil
}

// HeadState classifies what a worktree's HEAD points at.
type HeadState int

const (
	HeadOnBranch    HeadState = iota // HEAD is a symbolic ref to a branch
	HeadDetached                     // HEAD is a raw commit and nothing is running
	HeadInOperation                  // HEAD is detached because git is mid-operation
)

// Head describes what a worktree's HEAD points at.
type Head struct {
	State     HeadState
	Branch    string // the branch, when State is HeadOnBranch
	Operation string // e.g. "rebase", when State is HeadInOperation
	Remedy    string // how to finish or undo Operation
}

// detachingOps are the git operations that leave HEAD detached while they run,
// paired with the way out. A conflicted merge, cherry-pick, or revert keeps the
// branch checked out, so those never look like a detached HEAD and are not
// probed here.
var detachingOps = []struct{ path, name, remedy string }{
	{"rebase-merge", "rebase", "`git rebase --continue` or `git rebase --abort`"},
	{"rebase-apply", "rebase", "`git rebase --continue` or `git rebase --abort`"},
	{"BISECT_LOG", "bisect", "`git bisect reset`"},
}

// ReadHead reports what dir's HEAD points at. A detached HEAD is classified
// rather than returned as an error (CurrentBranch's behavior): `git rebase`
// and `git bisect` both detach, and git-work's own `rebase` deliberately
// leaves conflicted rebases in progress, so "mid-operation" has to be
// distinguishable from "checked out a raw commit" for callers to say anything
// useful.
func ReadHead(dir string) (Head, error) {
	out, err := git(dir, "symbolic-ref", "--short", "HEAD")
	if err == nil {
		return Head{State: HeadOnBranch, Branch: strings.TrimSpace(out)}, nil
	}
	// symbolic-ref also fails when dir is not a repository at all; probing
	// the git dir below surfaces that as a real error rather than letting it
	// masquerade as a detached HEAD.
	for _, op := range detachingOps {
		exists, perr := gitPathExists(dir, op.path)
		if perr != nil {
			return Head{}, perr
		}
		if exists {
			return Head{State: HeadInOperation, Operation: op.name, Remedy: op.remedy}, nil
		}
	}
	return Head{State: HeadDetached}, nil
}

// WorktreeBranchProblem reports why the worktree at wt is not sitting on want -
// the branch the work folder's state file records for it - or "" when it is.
//
// State is written once, at `new`/`add`/`adopt` time, and nothing re-syncs it
// when a branch is switched by hand, so the two can drift. Commands that act on
// the recorded branch (push, integrate) and commands that act on the checked-out one
// (rebase) would then operate on different branches, and `done` would delete a
// branch other than the one it safety-checked. Every such command refuses
// instead, sharing this wording so the remedy reads the same everywhere.
func WorktreeBranchProblem(wt, want string) (string, error) {
	h, err := ReadHead(wt)
	if err != nil {
		return "", err
	}
	switch h.State {
	case HeadOnBranch:
		if h.Branch == want {
			return "", nil
		}
		return fmt.Sprintf("state records branch %s but the worktree is on %s; run `git work adopt ./%s` to record the new branch, or check %s back out",
			want, h.Branch, filepath.Base(wt), want), nil
	case HeadInOperation:
		return fmt.Sprintf("a %s is in progress in %s; finish it with %s first", h.Operation, wt, h.Remedy), nil
	default:
		return fmt.Sprintf("the worktree is on a detached HEAD, not branch %s; check %s back out", want, want), nil
	}
}

// RepairWorktrees repairs worktree administrative files and back-pointers
// after the primary clone (or a worktree) moved (`git worktree repair`).
// Pass the current paths of moved worktrees so their .git back-pointers are
// rewritten; with no paths only the primary-side links are repaired.
func RepairWorktrees(mainDir string, worktreePaths ...string) error {
	args := append([]string{"worktree", "repair"}, worktreePaths...)
	_, err := git(mainDir, args...)
	return err
}

// BranchesMatching returns local branch names containing ticketID delimited
// by non-alphanumeric boundaries (or the string edges): "PROJ-123" matches
// "feature/PROJ-123" and "PROJ-123-fix" but not "PROJ-1234" or "XPROJ-123".
func BranchesMatching(mainDir, ticketID string) ([]string, error) {
	re, err := regexp.Compile(`(?i)(^|[^a-zA-Z0-9])` + regexp.QuoteMeta(ticketID) + `([^a-zA-Z0-9]|$)`)
	if err != nil {
		return nil, err
	}
	out, err := git(mainDir, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		b := strings.TrimSpace(line)
		if b != "" && re.MatchString(b) {
			matches = append(matches, b)
		}
	}
	return matches, nil
}

// DeleteLocalBranch deletes a local branch (force when not merged).
func DeleteLocalBranch(mainDir, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := git(mainDir, "branch", flag, branch)
	return err
}

// DeleteRemoteBranch deletes a branch on origin.
func DeleteRemoteBranch(mainDir, branch string) error {
	_, err := git(mainDir, "push", "origin", "--delete", branch)
	return err
}

// refExists reports whether a fully-qualified ref exists. show-ref exits 1
// for "no such ref"; any other failure is a real error.
func refExists(mainDir, ref string) (bool, error) {
	cmd := exec.Command("git", "-C", mainDir, "show-ref", "--verify", "--quiet", ref)
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("git show-ref %s: %v", ref, err)
	}
	return true, nil
}

// LocalBranchExists reports whether a local branch exists.
func LocalBranchExists(mainDir, branch string) (bool, error) {
	return refExists(mainDir, "refs/heads/"+branch)
}

// RemoteBranchExists reports whether origin/<branch> exists locally (accurate
// after a fetch --prune).
func RemoteBranchExists(mainDir, branch string) (bool, error) {
	return refExists(mainDir, "refs/remotes/origin/"+branch)
}

// PruneWorktrees drops stale worktree registrations (e.g. after a worktree
// directory was deleted out from under git).
func PruneWorktrees(mainDir string) error {
	_, err := git(mainDir, "worktree", "prune")
	return err
}
