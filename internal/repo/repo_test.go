package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// makeOrigin creates a bare origin with one commit on main, returns its path.
func makeOrigin(t *testing.T) string {
	t.Helper()
	return makeOriginBranch(t, "main")
}

// makeOriginBranch creates a bare origin whose default branch is branch,
// with one commit on it, and returns its path.
func makeOriginBranch(t *testing.T, branch string) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	run(t, "", "git", "init", "-q", "--bare", "-b", branch, origin)
	seed := filepath.Join(root, "seed")
	run(t, "", "git", "clone", "-q", origin, seed)
	run(t, seed, "git", "config", "user.email", "t@t.t")
	run(t, seed, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(seed, "f.txt"), []byte("hi\n"), 0o644)
	run(t, seed, "git", "add", "f.txt")
	run(t, seed, "git", "commit", "-qm", "init")
	run(t, seed, "git", "branch", "-M", branch)
	run(t, seed, "git", "push", "-q", "origin", branch)
	return origin
}

// cloneWorkdir clones origin into a fresh temp dir with a commit identity set.
func cloneWorkdir(t *testing.T, origin string) string {
	t.Helper()
	w := filepath.Join(t.TempDir(), "w")
	run(t, "", "git", "clone", "-q", origin, w)
	run(t, w, "git", "config", "user.email", "t@t.t")
	run(t, w, "git", "config", "user.name", "t")
	return w
}

// writeCommit writes content to fname in dir and commits it.
func writeCommit(t *testing.T, dir, fname, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, fname), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", fname)
	run(t, dir, "git", "commit", "-qm", msg)
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func TestCloneAndWorktree(t *testing.T) {
	origin := makeOrigin(t)
	reposRoot := t.TempDir()
	repoDir := filepath.Join(reposRoot, "myrepo")

	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	// .git pointer file exists with expected content
	ptr, err := os.ReadFile(filepath.Join(repoDir, ".git"))
	if err != nil || string(ptr) != "gitdir: ./main/.git\n" {
		t.Fatalf("pointer = %q err=%v", ptr, err)
	}
	// git resolves the same toplevel from parent and main
	main := filepath.Join(repoDir, "main")
	top := run(t, main, "git", "rev-parse", "--show-toplevel")
	if filepath.Clean(top) == "" {
		t.Fatal("empty toplevel")
	}

	// fetch should succeed
	if err := Fetch(main); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// add a worktree on a new branch
	wt := filepath.Join(t.TempDir(), "work", "myrepo")
	if err := AddWorktree(main, wt, "PROJ-1-foo", "", true); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	branch := run(t, wt, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if got := strings.TrimSpace(branch); got != "PROJ-1-foo" {
		t.Errorf("branch = %q", got)
	}

	// status checks: clean tree
	dirty, err := HasUncommittedChanges(wt)
	if err != nil || dirty {
		t.Errorf("HasUncommittedChanges = %v err=%v", dirty, err)
	}

	// remove worktree
	if err := RemoveWorktree(main, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree dir still present")
	}
}

func TestClonePrimaryNamedAfterDefaultBranch(t *testing.T) {
	origin := makeOriginBranch(t, "master")
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	ptr, err := os.ReadFile(filepath.Join(repoDir, ".git"))
	if err != nil || string(ptr) != "gitdir: ./master/.git\n" {
		t.Fatalf("pointer = %q err=%v", ptr, err)
	}
	primary := filepath.Join(repoDir, "master")
	head := run(t, primary, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if got := strings.TrimSpace(head); got != "master" {
		t.Errorf("HEAD = %q, want master", got)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "main")); !os.IsNotExist(err) {
		t.Errorf("unexpected main/ dir in a master-defaulted repo")
	}
}

// TestCloneFailureCleansUpTarget pins that a failed Clone removes the
// repoDir it created: a leftover empty dir (failed `git clone`) or a full
// clone stranded at the temp name (failed default-branch detection) would
// make every retry fail with a misleading "target already exists".
func TestCloneFailureCleansUpTarget(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "myrepo")

	// git clone itself fails: bad URL.
	err := Clone(filepath.Join(t.TempDir(), "no-such-origin"), repoDir, nil)
	if err == nil || strings.Contains(err.Error(), "target already exists") {
		t.Fatalf("Clone(bad url) err = %v, want a git clone failure", err)
	}
	if _, serr := os.Stat(repoDir); !os.IsNotExist(serr) {
		t.Fatalf("repoDir left behind after a failed clone: stat err = %v", serr)
	}

	// The clone succeeds but default-branch detection fails: --depth 1
	// --branch <tag> yields a detached HEAD with no origin/HEAD.
	origin := makeOrigin(t)
	seedTag := cloneWorkdir(t, origin)
	run(t, seedTag, "git", "tag", "v1")
	run(t, seedTag, "git", "push", "-q", "origin", "v1")
	err = Clone(origin, repoDir, []string{"--depth", "1", "--branch", "v1"})
	if err == nil || strings.Contains(err.Error(), "target already exists") {
		t.Fatalf("Clone(tag) err = %v, want a default-branch failure", err)
	}
	if _, serr := os.Stat(repoDir); !os.IsNotExist(serr) {
		t.Fatalf("repoDir left behind after a failed tag clone: stat err = %v", serr)
	}

	// The corrected retry now succeeds against the same target.
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatalf("retry after cleanup: %v", err)
	}
}

func TestListWorktreesAndCoreWorktree(t *testing.T) {
	origin := makeOrigin(t)
	root := t.TempDir()
	repoDir := filepath.Join(root, "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")

	cw, err := CoreWorktree(main)
	if err != nil || cw != ".." {
		t.Errorf("CoreWorktree = %q, %v; want \"..\", nil", cw, err)
	}

	// Only the main worktree, listed first.
	wts, err := ListWorktrees(main)
	if err != nil || len(wts) != 1 {
		t.Fatalf("ListWorktrees = %v, %v; want the main worktree only", wts, err)
	}
	if wts[0].Branch != "main" {
		t.Errorf("main worktree branch = %q, want main", wts[0].Branch)
	}

	wt := filepath.Join(root, "work", "myrepo")
	if err := AddWorktree(main, wt, "PROJ-1-x", "", true); err != nil {
		t.Fatal(err)
	}
	wts, err = ListWorktrees(main)
	if err != nil || len(wts) != 2 {
		t.Fatalf("ListWorktrees = %v, %v; want main + linked", wts, err)
	}
	if wts[1].Branch != "PROJ-1-x" || filepath.Base(wts[1].Path) != "myrepo" {
		t.Errorf("linked worktree = %+v, want branch PROJ-1-x at .../myrepo", wts[1])
	}

	// Unset core.worktree reads as "", not an error.
	run(t, main, "git", "config", "--unset", "core.worktree")
	cw, err = CoreWorktree(main)
	if err != nil || cw != "" {
		t.Errorf("unset CoreWorktree = %q, %v; want \"\", nil", cw, err)
	}

	// SetCoreWorktree restores it.
	if err := SetCoreWorktree(main, ".."); err != nil {
		t.Fatalf("SetCoreWorktree: %v", err)
	}
	cw, err = CoreWorktree(main)
	if err != nil || cw != ".." {
		t.Errorf("after SetCoreWorktree: CoreWorktree = %q, %v; want \"..\", nil", cw, err)
	}
}

func TestCurrentBranchAndCommonGitDir(t *testing.T) {
	origin := makeOrigin(t)
	root := t.TempDir()
	repoDir := filepath.Join(root, "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	wt := filepath.Join(root, "work", "myrepo")
	if err := AddWorktree(main, wt, "PROJ-1-x", "", true); err != nil {
		t.Fatal(err)
	}

	if b, err := CurrentBranch(main); err != nil || b != "main" {
		t.Errorf("CurrentBranch(main) = %q, %v; want main, nil", b, err)
	}
	if b, err := CurrentBranch(wt); err != nil || b != "PROJ-1-x" {
		t.Errorf("CurrentBranch(worktree) = %q, %v; want PROJ-1-x, nil", b, err)
	}

	// The common git dir of both the primary and a linked worktree is the
	// primary clone's .git; compare symlink-resolved (macOS /var -> /private/var).
	want, err := filepath.EvalSymlinks(filepath.Join(main, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{main, wt} {
		got, err := CommonGitDir(dir)
		if err != nil {
			t.Fatalf("CommonGitDir(%s): %v", dir, err)
		}
		if resolved, err := filepath.EvalSymlinks(got); err == nil {
			got = resolved
		}
		if got != want {
			t.Errorf("CommonGitDir(%s) = %q, want %q", dir, got, want)
		}
	}

	// Detached HEAD is an error.
	run(t, wt, "git", "checkout", "-q", "--detach")
	if b, err := CurrentBranch(wt); err == nil {
		t.Errorf("CurrentBranch(detached) = %q, want error", b)
	}
}

func TestPullFFOnly(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")

	// Up to date: nothing new on origin.
	res, err := PullFFOnly(main)
	if err != nil || res != PullUpToDate {
		t.Fatalf("up to date: PullFFOnly = %v, %v; want PullUpToDate, nil", res, err)
	}

	// Fast-forward: origin main advanced elsewhere.
	w := cloneWorkdir(t, origin)
	writeCommit(t, w, "new.txt", "new\n", "advance main")
	run(t, w, "git", "push", "-q", "origin", "main")
	res, err = PullFFOnly(main)
	if err != nil || res != PullFastForwarded {
		t.Fatalf("fast-forward: PullFFOnly = %v, %v; want PullFastForwarded, nil", res, err)
	}
	want := strings.TrimSpace(run(t, w, "git", "rev-parse", "HEAD"))
	got := strings.TrimSpace(run(t, main, "git", "rev-parse", "HEAD"))
	if got != want {
		t.Errorf("HEAD after pull = %s, want %s", got, want)
	}

	// Diverged: a local-only commit (same tree, so the worktree stays clean)
	// plus a new origin commit means no fast-forward is possible.
	tree := strings.TrimSpace(run(t, main, "git", "rev-parse", "HEAD^{tree}"))
	c := strings.TrimSpace(run(t, main, "git", "-c", "user.name=t", "-c", "user.email=t@t.t",
		"commit-tree", tree, "-p", "HEAD", "-m", "local-only"))
	run(t, main, "git", "update-ref", "refs/heads/main", c)
	writeCommit(t, w, "other.txt", "x\n", "advance again")
	run(t, w, "git", "push", "-q", "origin", "main")
	if _, err := PullFFOnly(main); err == nil {
		t.Error("expected PullFFOnly to fail on a diverged branch")
	}
	head := strings.TrimSpace(run(t, main, "git", "rev-parse", "HEAD"))
	if head != c {
		t.Errorf("HEAD after failed pull = %s, want untouched %s", head, c)
	}
}

// TestPullBranchFFOnly pins that pulling targets the named branch, not
// whatever the clone happens to have checked out: a primary left on another
// branch must still get its default branch fast-forwarded (via fetch,
// without touching the working tree).
func TestPullBranchFFOnly(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")

	// With the branch checked out it behaves like a plain ff-only pull.
	res, err := PullBranchFFOnly(main, "main")
	if err != nil || res != PullUpToDate {
		t.Fatalf("checked out, up to date: PullBranchFFOnly = %v, %v; want PullUpToDate, nil", res, err)
	}

	// Leave the primary on another branch, then advance origin main.
	run(t, main, "git", "checkout", "-q", "-b", "hotfix")
	w := cloneWorkdir(t, origin)
	writeCommit(t, w, "new.txt", "new\n", "advance main")
	run(t, w, "git", "push", "-q", "origin", "main")

	res, err = PullBranchFFOnly(main, "main")
	if err != nil || res != PullFastForwarded {
		t.Fatalf("non-checked-out: PullBranchFFOnly = %v, %v; want PullFastForwarded, nil", res, err)
	}
	want := strings.TrimSpace(run(t, w, "git", "rev-parse", "HEAD"))
	got := strings.TrimSpace(run(t, main, "git", "rev-parse", "refs/heads/main"))
	if got != want {
		t.Errorf("refs/heads/main after pull = %s, want origin tip %s", got, want)
	}
	if b, err := CurrentBranch(main); err != nil || b != "hotfix" {
		t.Errorf("checked-out branch after pull = %q, %v; want hotfix untouched", b, err)
	}
	if _, err := os.Stat(filepath.Join(main, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("working tree touched by a non-checked-out pull (new.txt appeared)")
	}

	// A diverged non-checked-out branch is refused (non-forced fetch
	// refspecs are ff-only), leaving the local ref untouched.
	tree := strings.TrimSpace(run(t, main, "git", "rev-parse", "refs/heads/main^{tree}"))
	c := strings.TrimSpace(run(t, main, "git", "-c", "user.name=t", "-c", "user.email=t@t.t",
		"commit-tree", tree, "-p", "refs/heads/main~1", "-m", "local-only"))
	run(t, main, "git", "update-ref", "refs/heads/main", c)
	if _, err := PullBranchFFOnly(main, "main"); err == nil {
		t.Error("expected PullBranchFFOnly to fail on a diverged non-checked-out branch")
	}
	if head := strings.TrimSpace(run(t, main, "git", "rev-parse", "refs/heads/main")); head != c {
		t.Errorf("refs/heads/main after failed pull = %s, want untouched %s", head, c)
	}
}

func TestPullResultString(t *testing.T) {
	cases := []struct {
		res  PullResult
		want string
	}{
		{PullUpToDate, "already up to date"},
		{PullFastForwarded, "fast-forwarded"},
	}
	for _, c := range cases {
		if got := c.res.String(); got != c.want {
			t.Errorf("PullResult(%d).String() = %q, want %q", c.res, got, c.want)
		}
	}
}

func TestIsPatchEquivalent(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")

	// A two-commit branch, squash-merged into main.
	w := cloneWorkdir(t, origin)
	run(t, w, "git", "checkout", "-q", "-b", "feat")
	writeCommit(t, w, "a.txt", "a\n", "c1")
	writeCommit(t, w, "b.txt", "b\n", "c2")
	run(t, w, "git", "push", "-q", "origin", "feat")
	run(t, w, "git", "checkout", "-q", "main")
	run(t, w, "git", "merge", "--squash", "-q", "feat")
	run(t, w, "git", "commit", "-qm", "squash feat")
	run(t, w, "git", "push", "-q", "origin", "main")
	run(t, main, "git", "fetch", "-q", "origin")

	// Ancestry says not merged...
	merged, err := IsMergedInto(main, "origin/feat", "origin/main")
	if err != nil || merged {
		t.Fatalf("IsMergedInto = %v, %v; want false (a squash merge is not an ancestor)", merged, err)
	}
	// ...but the squash carried the same patch.
	eq, err := IsPatchEquivalent(main, "origin/feat", "origin/main")
	if err != nil || !eq {
		t.Errorf("squash-merged: IsPatchEquivalent = %v, %v; want true, nil", eq, err)
	}

	// Extra commits on the branch beyond the squash -> NOT merged.
	run(t, w, "git", "checkout", "-q", "feat")
	writeCommit(t, w, "c.txt", "c\n", "c3")
	run(t, w, "git", "push", "-q", "origin", "feat")
	run(t, main, "git", "fetch", "-q", "origin")
	eq, err = IsPatchEquivalent(main, "origin/feat", "origin/main")
	if err != nil || eq {
		t.Errorf("extra commits: IsPatchEquivalent = %v, %v; want false, nil", eq, err)
	}

	// Empty branch (no commits since merge-base) is trivially contained.
	run(t, main, "git", "branch", "empty", "origin/main")
	eq, err = IsPatchEquivalent(main, "empty", "origin/main")
	if err != nil || !eq {
		t.Errorf("empty branch: IsPatchEquivalent = %v, %v; want true, nil", eq, err)
	}

	// Net-zero branch: commits since the merge-base whose combined diff is
	// empty (a change followed by its revert). `git cherry` cannot patch-id
	// the empty probe commit, so this needs the tree short-circuit.
	run(t, w, "git", "checkout", "-q", "-b", "netzero", "origin/main")
	writeCommit(t, w, "z.txt", "z\n", "add z")
	run(t, w, "git", "revert", "--no-edit", "HEAD")
	run(t, w, "git", "push", "-q", "origin", "netzero")
	run(t, main, "git", "fetch", "-q", "origin")
	eq, err = IsPatchEquivalent(main, "origin/netzero", "origin/main")
	if err != nil || !eq {
		t.Errorf("net-zero branch: IsPatchEquivalent = %v, %v; want true, nil", eq, err)
	}

	// Unknown ref is an error, not a silent "not merged".
	if _, err := IsPatchEquivalent(main, "no-such-branch", "origin/main"); err == nil {
		t.Error("expected error for unknown branch")
	}
}

// TestIsPatchEquivalentMultiCommitMerge covers a branch whose commits were
// carried into the target individually (cherry-picked/rebased) rather than
// squashed into one commit, and where the target then advanced with a
// follow-up commit re-editing one of the same files - the jenkins-server case
// from PROJ-1. Every branch commit is patch-present in target, but there is no
// single combined-diff commit, so the squash probe alone reports "not merged";
// the per-commit cherry path must recognize the branch as contained.
func TestIsPatchEquivalentMultiCommitMerge(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")

	w := cloneWorkdir(t, origin)
	run(t, w, "git", "checkout", "-q", "-b", "feat")
	writeCommit(t, w, "a.txt", "a\n", "c1")
	writeCommit(t, w, "b.txt", "b\n", "c2")
	run(t, w, "git", "push", "-q", "origin", "feat")

	// Diverge main first so the copied commits land on a different parent and
	// get new SHAs (a rebase/cherry-pick merge, not a fast-forward), then carry
	// the two commits over individually, then advance main again with a
	// follow-up commit that re-edits one of the same files.
	run(t, w, "git", "checkout", "-q", "main")
	writeCommit(t, w, "main.txt", "main moved\n", "main advances before merge")
	run(t, w, "git", "cherry-pick", "feat~1", "feat")
	writeCommit(t, w, "b.txt", "b revised\n", "revise b on main")
	run(t, w, "git", "push", "-q", "origin", "main")
	run(t, main, "git", "fetch", "-q", "origin")

	// Not an ancestor (the commits were copied, so the SHAs differ)...
	merged, err := IsMergedInto(main, "origin/feat", "origin/main")
	if err != nil || merged {
		t.Fatalf("IsMergedInto = %v, %v; want false (copied commits are not ancestors)", merged, err)
	}
	// ...and not a single-commit squash, but every commit is patch-present.
	eq, err := IsPatchEquivalent(main, "origin/feat", "origin/main")
	if err != nil || !eq {
		t.Errorf("multi-commit merge: IsPatchEquivalent = %v, %v; want true, nil", eq, err)
	}
}

// TestIsPatchEquivalentMixedMerge covers the merge-tree backstop: a branch
// arrived as a MIX - two commits squashed into one target commit, a third
// cherry-picked individually - with content fully in target. Neither the
// per-commit cherry (the squashed commits have no individual counterpart) nor
// the single-squash probe (target has no one commit equal to the branch's
// combined diff) recognizes it, so only mergeAbsorbedInto reports it contained.
func TestIsPatchEquivalentMixedMerge(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")

	// feat: three commits.
	w := cloneWorkdir(t, origin)
	run(t, w, "git", "checkout", "-q", "-b", "feat")
	writeCommit(t, w, "a.txt", "a\n", "c1")
	writeCommit(t, w, "b.txt", "b\n", "c2")
	writeCommit(t, w, "c.txt", "c\n", "c3")
	run(t, w, "git", "push", "-q", "origin", "feat")

	// Arrange as a MIX: squash c1+c2 into ONE commit on main, then cherry-pick c3
	// individually. Content ends up fully in main.
	run(t, w, "git", "branch", "feat12", "feat~1") // tip = c2, contains c1+c2
	run(t, w, "git", "checkout", "-q", "main")
	run(t, w, "git", "merge", "--squash", "-q", "feat12")
	run(t, w, "git", "commit", "-qm", "squash c1+c2")
	run(t, w, "git", "cherry-pick", "feat") // c3
	run(t, w, "git", "push", "-q", "origin", "main")
	run(t, main, "git", "fetch", "-q", "origin")

	// Not an ancestor, and neither cherry path recognizes the mix...
	merged, err := IsMergedInto(main, "origin/feat", "origin/main")
	if err != nil || merged {
		t.Fatalf("IsMergedInto = %v, %v; want false (mixed merge is not an ancestor)", merged, err)
	}
	// ...but the merge-tree backstop sees the content fully absorbed.
	eq, err := IsPatchEquivalent(main, "origin/feat", "origin/main")
	if err != nil || !eq {
		t.Errorf("mixed merge: IsPatchEquivalent = %v, %v; want true, nil", eq, err)
	}

	// Adversarial: a clean, non-overlapping branch-only edit not in target must
	// stay NOT contained - the backstop's merge adds a file, so the result tree
	// differs from target's.
	run(t, w, "git", "checkout", "-q", "-b", "only", "origin/main")
	writeCommit(t, w, "only.txt", "only\n", "branch-only edit")
	run(t, w, "git", "push", "-q", "origin", "only")
	run(t, main, "git", "fetch", "-q", "origin")
	eq, err = IsPatchEquivalent(main, "origin/only", "origin/main")
	if err != nil || eq {
		t.Errorf("branch-only edit: IsPatchEquivalent = %v, %v; want false, nil", eq, err)
	}
}

// TestCommitsNotIn pins the "+" SHAs of `git cherry`, oldest first: only the
// commits not yet patch-present in target, each carrying its subject line,
// empty when fully integrated.
func TestCommitsNotIn(t *testing.T) {
	origin := makeOrigin(t)
	w := cloneWorkdir(t, origin)

	// A three-commit feat branch.
	run(t, w, "git", "checkout", "-q", "-b", "feat")
	writeCommit(t, w, "a.txt", "a\n", "c1")
	writeCommit(t, w, "b.txt", "b\n", "c2")
	writeCommit(t, w, "c.txt", "c\n", "c3")
	c1 := strings.TrimSpace(run(t, w, "git", "rev-parse", "feat~2"))
	c2 := strings.TrimSpace(run(t, w, "git", "rev-parse", "feat~1"))
	c3 := strings.TrimSpace(run(t, w, "git", "rev-parse", "feat"))

	// target = main with only c1 carried over (patch-present); c2/c3 absent.
	run(t, w, "git", "checkout", "-q", "main")
	run(t, w, "git", "cherry-pick", c1)

	got, err := CommitsNotIn(w, "feat", "main")
	if err != nil {
		t.Fatal(err)
	}
	want := []Commit{{SHA: c2, Subject: "c2"}, {SHA: c3, Subject: "c3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CommitsNotIn = %v, want %v (oldest first, only the unintegrated)", got, want)
	}

	// Carry the rest over: now fully integrated → empty.
	run(t, w, "git", "cherry-pick", c2, c3)
	got, err = CommitsNotIn(w, "feat", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("fully integrated: CommitsNotIn = %v, want empty", got)
	}
}

// TestCherryPick pins the three outcomes: a clean apply, an already-present
// (empty) pick, and a conflicting pick left in progress.
func TestCherryPick(t *testing.T) {
	origin := makeOrigin(t)
	w := cloneWorkdir(t, origin)

	// A feat branch with one clean commit.
	run(t, w, "git", "checkout", "-q", "-b", "feat")
	writeCommit(t, w, "a.txt", "a\n", "add a")
	cleanSHA := strings.TrimSpace(run(t, w, "git", "rev-parse", "feat"))

	// Clean apply onto main.
	run(t, w, "git", "checkout", "-q", "main")
	st, err := CherryPick(w, cleanSHA)
	if err != nil || st != CherryPickOK {
		t.Fatalf("clean: CherryPick = %v, %v; want CherryPickOK, nil", st, err)
	}
	if _, err := os.Stat(filepath.Join(w, "a.txt")); err != nil {
		t.Errorf("clean pick did not apply a.txt: %v", err)
	}

	// Re-picking the same commit: the patch is already present → Empty, and
	// CherryPick clears the in-progress state itself so a caller can continue.
	st, err = CherryPick(w, cleanSHA)
	if err != nil || st != CherryPickEmpty {
		t.Fatalf("empty: CherryPick = %v, %v; want CherryPickEmpty, nil", st, err)
	}
	if _, err := os.Stat(filepath.Join(w, ".git", "CHERRY_PICK_HEAD")); !os.IsNotExist(err) {
		t.Errorf("empty pick should be cleared (no CHERRY_PICK_HEAD): stat err = %v", err)
	}

	// A conflicting pick: a sibling branch and main edit the same file
	// differently off the same base.
	run(t, w, "git", "checkout", "-q", "-b", "conf", "origin/main")
	writeCommit(t, w, "f.txt", "conflict side\n", "edit f on conf")
	confSHA := strings.TrimSpace(run(t, w, "git", "rev-parse", "conf"))
	run(t, w, "git", "checkout", "-q", "main")
	writeCommit(t, w, "f.txt", "main side\n", "edit f on main")
	st, err = CherryPick(w, confSHA)
	if err != nil || st != CherryPickConflict {
		t.Fatalf("conflict: CherryPick = %v, %v; want CherryPickConflict, nil", st, err)
	}
	if _, err := os.Stat(filepath.Join(w, ".git", "CHERRY_PICK_HEAD")); err != nil {
		t.Errorf("conflicting pick should be left in progress: %v", err)
	}
	run(t, w, "git", "cherry-pick", "--abort")
}

func TestRebaseOntoStatuses(t *testing.T) {
	origin := makeOrigin(t)

	// Advance origin main so there is something to rebase onto.
	wm := cloneWorkdir(t, origin)
	writeCommit(t, wm, "f.txt", "origin change\n", "main change")
	run(t, wm, "git", "push", "-q", "origin", "main")

	// Work clone with a branch off the pre-advance commit.
	w := cloneWorkdir(t, origin)
	run(t, w, "git", "checkout", "-q", "-b", "feat", "origin/main~1")
	writeCommit(t, w, "x.txt", "x\n", "feat work")

	// Dirty worktree: skipped, nothing attempted.
	os.WriteFile(filepath.Join(w, "x.txt"), []byte("dirty\n"), 0o644)
	st, err := RebaseOnto(w, "origin/main")
	if err != nil || st != RebaseDirty {
		t.Fatalf("dirty: RebaseOnto = %v, %v; want RebaseDirty, nil", st, err)
	}
	run(t, w, "git", "checkout", "-q", "--", "x.txt")

	// Clean rebase: branch ends up on top of origin/main.
	st, err = RebaseOnto(w, "origin/main")
	if err != nil || st != RebaseOK {
		t.Fatalf("clean: RebaseOnto = %v, %v; want RebaseOK, nil", st, err)
	}
	mainTip := strings.TrimSpace(run(t, w, "git", "rev-parse", "origin/main"))
	parent := strings.TrimSpace(run(t, w, "git", "rev-parse", "HEAD~1"))
	if parent != mainTip {
		t.Errorf("rebased parent = %s, want origin/main tip %s", parent, mainTip)
	}

	// Conflict: rebase is left in progress for --continue/--abort.
	run(t, w, "git", "checkout", "-q", "-b", "feat2", "origin/main~1")
	writeCommit(t, w, "f.txt", "feat change\n", "conflicting change")
	st, err = RebaseOnto(w, "origin/main")
	if err != nil || st != RebaseConflict {
		t.Fatalf("conflict: RebaseOnto = %v, %v; want RebaseConflict, nil", st, err)
	}
	if _, err := os.Stat(filepath.Join(w, ".git", "rebase-merge")); err != nil {
		t.Errorf("rebase should be left in progress: %v", err)
	}
	run(t, w, "git", "rebase", "--abort")
}

func TestRepairWorktrees(t *testing.T) {
	origin := makeOrigin(t)
	root := t.TempDir()
	repoDir := filepath.Join(root, "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "work", "myrepo")
	if err := AddWorktree(filepath.Join(repoDir, "main"), wt, "PROJ-1-x", "", true); err != nil {
		t.Fatal(err)
	}

	// Move the primary clone: the worktree's .git back-pointer goes stale.
	moved := filepath.Join(root, "elsewhere")
	if err := os.Rename(repoDir, moved); err != nil {
		t.Fatal(err)
	}
	newMain := filepath.Join(moved, "main")
	if err := exec.Command("git", "-C", wt, "rev-parse", "--git-dir").Run(); err == nil {
		t.Fatal("expected the stale back-pointer to break the worktree")
	}

	if err := RepairWorktrees(newMain, wt); err != nil {
		t.Fatalf("RepairWorktrees: %v", err)
	}
	run(t, wt, "git", "status", "--porcelain")
}

func TestBranchesMatchingBoundaries(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	for _, b := range []string{"PROJ-123", "PROJ-123-fix", "feature/PROJ-123", "PROJ-1234", "XPROJ-123"} {
		run(t, main, "git", "branch", b)
	}
	got, err := BranchesMatching(main, "PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PROJ-123", "PROJ-123-fix", "feature/PROJ-123"}
	names := candidateNames(got)
	sort.Strings(names)
	sort.Strings(want)
	if !reflect.DeepEqual(names, want) {
		t.Errorf("BranchesMatching = %v, want %v", names, want)
	}
}

// candidateNames renders candidates as the picker and the refusal show them.
func candidateNames(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}

// Candidates span origin: a branch that exists only there is listed after
// the local ones as origin/<name>, and one present in both places counts
// once, as the local branch.
func TestBranchesMatchingRemote(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	run(t, main, "git", "branch", "PROJ-7-local")
	run(t, main, "git", "branch", "PROJ-7-both")
	pushExtraBranch(t, origin, "PROJ-7-both")
	pushExtraBranch(t, origin, "feature/PROJ-7")
	pushExtraBranch(t, origin, "PROJ-70-other")
	if err := Fetch(main); err != nil {
		t.Fatal(err)
	}
	got, err := BranchesMatching(main, "PROJ-7")
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{Branch: "PROJ-7-both"}, {Branch: "PROJ-7-local"}, {Branch: "feature/PROJ-7", Remote: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BranchesMatching = %v, want %v", got, want)
	}
}

func TestCheckBranchArg(t *testing.T) {
	for _, ok := range []string{"", "feature/x", "origins/x", "my-origin/x"} {
		if err := CheckBranchArg(ok); err != nil {
			t.Errorf("CheckBranchArg(%q) = %v, want nil", ok, err)
		}
	}
	err := CheckBranchArg("origin/feature/x")
	if err == nil || !strings.Contains(err.Error(), `"feature/x"`) {
		t.Errorf("CheckBranchArg(origin/feature/x) = %v, want a refusal naming feature/x", err)
	}
}

func TestBranchesMatchingCaseInsensitive(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	// Branch names are lowercase (branch_case: lower), ticket id is uppercase.
	for _, b := range []string{"dev-tools-1-slug", "dev-tools-12-slug"} {
		run(t, main, "git", "branch", b)
	}

	// Query with uppercase ticket id - must match the lowercase branch.
	got, err := BranchesMatching(main, "DEV-TOOLS-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dev-tools-1-slug"}
	if names := candidateNames(got); !reflect.DeepEqual(names, want) {
		t.Errorf("BranchesMatching(DEV-TOOLS-1) = %v, want %v", names, want)
	}

	// DEV-TOOLS-12 must NOT match dev-tools-1-slug (boundary check).
	got2, err := BranchesMatching(main, "DEV-TOOLS-12")
	if err != nil {
		t.Fatal(err)
	}
	want2 := []string{"dev-tools-12-slug"}
	if names := candidateNames(got2); !reflect.DeepEqual(names, want2) {
		t.Errorf("BranchesMatching(DEV-TOOLS-12) = %v, want %v", names, want2)
	}
}

func TestBranchExists(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	run(t, main, "git", "branch", "PROJ-9-existing")

	got, err := BranchesMatching(main, "PROJ-9")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Branch != "PROJ-9-existing" || got[0].Remote {
		t.Errorf("BranchesMatching = %v", got)
	}
}

// pushExtraBranch pushes a one-commit branch named branch to origin via a
// throwaway clone (the primary-clone layout's core.worktree=.. makes
// committing inside main/ unreliable, so commits are made elsewhere).
func pushExtraBranch(t *testing.T, origin, branch string) {
	t.Helper()
	w := filepath.Join(t.TempDir(), "w-"+strings.ReplaceAll(branch, "/", "-"))
	run(t, "", "git", "clone", "-q", origin, w)
	run(t, w, "git", "config", "user.email", "t@t.t")
	run(t, w, "git", "config", "user.name", "t")
	run(t, w, "git", "checkout", "-q", "-b", branch)
	os.WriteFile(filepath.Join(w, "g.txt"), []byte(branch+"\n"), 0o644)
	run(t, w, "git", "add", "g.txt")
	run(t, w, "git", "commit", "-qm", "extra")
	run(t, w, "git", "push", "-q", "origin", branch)
}

func TestRemoteDefaultRef(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	run(t, main, "git", "remote", "set-head", "origin", "main")

	ref, err := RemoteDefaultRef(main)
	if err != nil || ref != "origin/main" {
		t.Errorf("RemoteDefaultRef = %q, %v; want origin/main, nil", ref, err)
	}

	run(t, main, "git", "remote", "set-head", "origin", "--delete")
	if _, err := RemoteDefaultRef(main); err == nil {
		t.Error("expected error when origin/HEAD is unset (no silent main fallback)")
	}
}

func TestIsMergedIntoDistinguishesErrors(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")

	// merged: a branch pointing at the same commit as main
	run(t, main, "git", "branch", "merged-branch")
	ok, err := IsMergedInto(main, "merged-branch", "main")
	if err != nil || !ok {
		t.Errorf("merged-branch: got %v, %v; want true, nil", ok, err)
	}

	// not merged: a branch with an extra commit
	pushExtraBranch(t, origin, "ahead")
	run(t, main, "git", "fetch", "-q", "origin")
	run(t, main, "git", "branch", "ahead", "origin/ahead")
	ok, err = IsMergedInto(main, "ahead", "main")
	if err != nil || ok {
		t.Errorf("ahead: got %v, %v; want false, nil", ok, err)
	}

	// unknown ref: an error, not a silent "not merged"
	if _, err := IsMergedInto(main, "no-such-branch", "main"); err == nil {
		t.Error("expected error for unknown branch")
	}
}

func TestRemoteBranchExistsAndPruneWorktrees(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	pushExtraBranch(t, origin, "PROJ-7-x")
	run(t, main, "git", "fetch", "-q", "--prune", "origin")

	if ok, err := RemoteBranchExists(main, "PROJ-7-x"); err != nil || !ok {
		t.Errorf("RemoteBranchExists(PROJ-7-x) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := RemoteBranchExists(main, "nope"); err != nil || ok {
		t.Errorf("RemoteBranchExists(nope) = %v, %v; want false, nil", ok, err)
	}
	if err := PruneWorktrees(main); err != nil {
		t.Errorf("PruneWorktrees: %v", err)
	}
}

func TestReadHeadOnBranch(t *testing.T) {
	w := cloneWorkdir(t, makeOrigin(t))
	run(t, w, "git", "checkout", "-q", "-b", "feature")
	h, err := ReadHead(w)
	if err != nil {
		t.Fatalf("ReadHead: %v", err)
	}
	if h.State != HeadOnBranch || h.Branch != "feature" {
		t.Errorf("ReadHead = %+v; want HeadOnBranch on feature", h)
	}
}

func TestReadHeadDetached(t *testing.T) {
	w := cloneWorkdir(t, makeOrigin(t))
	sha := strings.TrimSpace(run(t, w, "git", "rev-parse", "HEAD"))
	run(t, w, "git", "checkout", "-q", "--detach", sha)
	h, err := ReadHead(w)
	if err != nil {
		t.Fatalf("ReadHead: %v", err)
	}
	if h.State != HeadDetached {
		t.Errorf("ReadHead = %+v; want HeadDetached", h)
	}
}

// conflictedRebase leaves w mid-rebase, which detaches HEAD - the state
// git-work's own `rebase` deliberately leaves behind on conflict.
func conflictedRebase(t *testing.T, w string) {
	t.Helper()
	run(t, w, "git", "checkout", "-q", "-b", "work")
	writeCommit(t, w, "f.txt", "mine\n", "work change")
	run(t, w, "git", "checkout", "-q", "main")
	writeCommit(t, w, "f.txt", "theirs\n", "main change")
	run(t, w, "git", "checkout", "-q", "work")
	// Expected to fail with conflicts, so this cannot use run().
	cmd := exec.Command("git", "rebase", "main")
	cmd.Dir = w
	if err := cmd.Run(); err == nil {
		t.Fatal("rebase succeeded; wanted a conflict")
	}
}

func TestReadHeadDuringRebase(t *testing.T) {
	w := cloneWorkdir(t, makeOrigin(t))
	conflictedRebase(t, w)
	h, err := ReadHead(w)
	if err != nil {
		t.Fatalf("ReadHead: %v", err)
	}
	if h.State != HeadInOperation || h.Operation != "rebase" {
		t.Fatalf("ReadHead = %+v; want HeadInOperation rebase", h)
	}
	if !strings.Contains(h.Remedy, "--abort") {
		t.Errorf("Remedy = %q; want the abort escape named", h.Remedy)
	}
}

func TestWorktreeBranchProblem(t *testing.T) {
	t.Run("matching branch is no problem", func(t *testing.T) {
		w := cloneWorkdir(t, makeOrigin(t))
		run(t, w, "git", "checkout", "-q", "-b", "PROJ-1-work")
		msg, err := WorktreeBranchProblem(w, "PROJ-1-work")
		if err != nil {
			t.Fatalf("WorktreeBranchProblem: %v", err)
		}
		if msg != "" {
			t.Errorf("msg = %q; want none", msg)
		}
	})

	t.Run("drifted branch names both sides and the remedy", func(t *testing.T) {
		w := cloneWorkdir(t, makeOrigin(t))
		run(t, w, "git", "checkout", "-q", "-b", "side-quest")
		msg, err := WorktreeBranchProblem(w, "PROJ-1-work")
		if err != nil {
			t.Fatalf("WorktreeBranchProblem: %v", err)
		}
		for _, want := range []string{"PROJ-1-work", "side-quest", "git work adopt ./" + filepath.Base(w)} {
			if !strings.Contains(msg, want) {
				t.Errorf("msg = %q; want it to mention %q", msg, want)
			}
		}
	})

	t.Run("mid-rebase is named as such, not as drift", func(t *testing.T) {
		w := cloneWorkdir(t, makeOrigin(t))
		conflictedRebase(t, w)
		msg, err := WorktreeBranchProblem(w, "work")
		if err != nil {
			t.Fatalf("WorktreeBranchProblem: %v", err)
		}
		if !strings.Contains(msg, "rebase is in progress") {
			t.Errorf("msg = %q; want the in-progress rebase named", msg)
		}
		// The confusing reading must not appear: this is not branch drift.
		if strings.Contains(msg, "state records branch") {
			t.Errorf("msg = %q; mid-rebase must not report as drift", msg)
		}
	})

	t.Run("detached HEAD is distinguished from drift", func(t *testing.T) {
		w := cloneWorkdir(t, makeOrigin(t))
		sha := strings.TrimSpace(run(t, w, "git", "rev-parse", "HEAD"))
		run(t, w, "git", "checkout", "-q", "--detach", sha)
		msg, err := WorktreeBranchProblem(w, "PROJ-1-work")
		if err != nil {
			t.Fatalf("WorktreeBranchProblem: %v", err)
		}
		if !strings.Contains(msg, "detached HEAD") {
			t.Errorf("msg = %q; want the detached HEAD named", msg)
		}
	})
}

func TestIsDefaultBranch(t *testing.T) {
	origin := makeOrigin(t) // default branch "main"
	w := cloneWorkdir(t, origin)

	for _, tc := range []struct {
		branch string
		want   bool
	}{
		{"main", true},
		{"PROJ-1-work", false},
	} {
		got, err := IsDefaultBranch(w, tc.branch)
		if err != nil {
			t.Fatalf("IsDefaultBranch(%q): %v", tc.branch, err)
		}
		if got != tc.want {
			t.Errorf("IsDefaultBranch(%q) = %v; want %v", tc.branch, got, tc.want)
		}
	}
}

func TestIsDefaultBranchIgnoresCheckedOutBranch(t *testing.T) {
	// The drifted-primary case the guard exists for. A clone sitting on some
	// other branch still has origin/HEAD naming main, so main must still read
	// as the default: answering from the checked-out HEAD would say
	// "sidebranch" and wave the real default branch straight through.
	origin := makeOrigin(t)
	w := cloneWorkdir(t, origin)
	run(t, w, "git", "checkout", "-q", "-b", "sidebranch")

	if got, err := IsDefaultBranch(w, "main"); err != nil || !got {
		t.Errorf("IsDefaultBranch(main) = %v, %v with the clone on sidebranch; want true, nil", got, err)
	}
	if got, err := IsDefaultBranch(w, "sidebranch"); err != nil || got {
		t.Errorf("IsDefaultBranch(sidebranch) = %v, %v; want false, nil", got, err)
	}
}

func TestIsDefaultBranchLocalOnlyFallsBackToHead(t *testing.T) {
	// A repo that never had a remote has no origin/HEAD to read, so the
	// repo's own branch stands in. Without the fallback every branch would be
	// undeterminable and teardown would refuse to delete any of them.
	dir := filepath.Join(t.TempDir(), "local")
	run(t, "", "git", "init", "-q", "-b", "main", dir)
	run(t, dir, "git", "config", "user.email", "t@t.t")
	run(t, dir, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0o644)
	run(t, dir, "git", "add", "f.txt")
	run(t, dir, "git", "commit", "-qm", "init")

	if got, err := IsDefaultBranch(dir, "main"); err != nil || !got {
		t.Errorf("IsDefaultBranch(main) = %v, %v; want true, nil", got, err)
	}
	if got, err := IsDefaultBranch(dir, "PROJ-1-work"); err != nil || got {
		t.Errorf("IsDefaultBranch(PROJ-1-work) = %v, %v; want false, nil", got, err)
	}
}

func TestDefaultBranchProblem(t *testing.T) {
	origin := makeOrigin(t)
	w := cloneWorkdir(t, origin)

	problem, err := DefaultBranchProblem(w, "main")
	if err != nil {
		t.Fatal(err)
	}
	if problem == "" {
		t.Fatal(`DefaultBranchProblem(main) = ""; want a refusal`)
	}
	for _, want := range []string{"main", "default branch", "switch -c"} {
		if !strings.Contains(problem, want) {
			t.Errorf("problem = %q; want it to mention %q", problem, want)
		}
	}
	if problem, err := DefaultBranchProblem(w, "PROJ-1-work"); err != nil || problem != "" {
		t.Errorf(`DefaultBranchProblem(PROJ-1-work) = %q, %v; want "", nil`, problem, err)
	}
}

// AddTrackingWorktree checks out a branch that exists only on origin: the
// worktree sits at origin's tip on a local branch tracking origin/<branch>.
func TestAddTrackingWorktree(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	seed := cloneWorkdir(t, origin)
	run(t, seed, "git", "checkout", "-qb", "PROJ-1-fix")
	run(t, seed, "git", "commit", "-q", "--allow-empty", "-m", "remote work")
	run(t, seed, "git", "push", "-q", "origin", "PROJ-1-fix")
	want := strings.TrimSpace(run(t, seed, "git", "rev-parse", "HEAD"))
	if err := Fetch(main); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(repoDir, "wt")
	if err := AddTrackingWorktree(main, wt, "PROJ-1-fix"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run(t, wt, "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "PROJ-1-fix" {
		t.Errorf("worktree branch = %q, want PROJ-1-fix", got)
	}
	if got := strings.TrimSpace(run(t, wt, "git", "rev-parse", "HEAD")); got != want {
		t.Errorf("worktree tip = %s, want origin's %s", got, want)
	}
	if got := strings.TrimSpace(run(t, wt, "git", "rev-parse", "--abbrev-ref", "@{upstream}")); got != "origin/PROJ-1-fix" {
		t.Errorf("upstream = %q, want origin/PROJ-1-fix", got)
	}
}
