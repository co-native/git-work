package teardown

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// makeOrigin creates a bare origin with one commit (f.txt) on main and
// returns its path.
func makeOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	runGit(t, "", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	runGit(t, "", "clone", "-q", origin, seed)
	runGit(t, seed, "config", "user.email", "t@t.t")
	runGit(t, seed, "config", "user.name", "t")
	writeCommit(t, seed, "f.txt", "base\n", "init")
	runGit(t, seed, "push", "-q", "origin", "main")
	return origin
}

// writeCommit writes content to fname in dir and commits it.
func writeCommit(t *testing.T, dir, fname, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, fname), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", fname)
	runGit(t, dir, "commit", "-qm", msg)
}

// setupWorkBranch clones origin as the primary for name, adds a worktree on
// a new work branch under workDir, commits one change on it, and pushes the
// branch with an upstream. Returns the worktree path.
func setupWorkBranch(t *testing.T, l layout.Layout, workDir, name, origin string) string {
	t.Helper()
	if err := repo.Clone(origin, l.RepoDir(name), nil); err != nil {
		t.Fatal(err)
	}
	main, err := l.RepoMain(name)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(workDir, name)
	if err := repo.AddWorktree(main, wt, "PROJ-1-work", "", true); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "config", "user.email", "t@t.t")
	runGit(t, wt, "config", "user.name", "t")
	writeCommit(t, wt, "feat.txt", "feature\n", "feat")
	runGit(t, wt, "push", "-q", "-u", "origin", "PROJ-1-work")
	return wt
}

// setupWorkBranchNoPush clones origin as the primary for name, adds a worktree
// on a new work branch under workDir, and commits one change on it WITHOUT
// pushing (no upstream) - the never-pushed case the unpushed check trips on.
// Returns the worktree path.
func setupWorkBranchNoPush(t *testing.T, l layout.Layout, workDir, name, origin string) string {
	t.Helper()
	if err := repo.Clone(origin, l.RepoDir(name), nil); err != nil {
		t.Fatal(err)
	}
	main, err := l.RepoMain(name)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(workDir, name)
	if err := repo.AddWorktree(main, wt, "PROJ-1-work", "", true); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "config", "user.email", "t@t.t")
	runGit(t, wt, "config", "user.name", "t")
	writeCommit(t, wt, "feat.txt", "feature\n", "feat")
	return wt
}

// commitOnOrigin writes fname=content on origin's main from a throwaway clone - a
// squash-merge-equivalent commit carrying the same change on the default branch
// without the work branch ever being pushed.
func commitOnOrigin(t *testing.T, origin, fname, content, msg string) {
	t.Helper()
	w := filepath.Join(t.TempDir(), "clone")
	runGit(t, "", "clone", "-q", origin, w)
	runGit(t, w, "config", "user.email", "t@t.t")
	runGit(t, w, "config", "user.name", "t")
	writeCommit(t, w, fname, content, msg)
	runGit(t, w, "push", "-q", "origin", "main")
}

// squashMergeOnOrigin squash-merges branch into origin's main from a
// throwaway clone and deletes the remote branch, as a forge's "squash and
// merge" with automatic branch deletion does.
func squashMergeOnOrigin(t *testing.T, origin, branch string) {
	t.Helper()
	w := filepath.Join(t.TempDir(), "w")
	runGit(t, "", "clone", "-q", origin, w)
	runGit(t, w, "config", "user.email", "t@t.t")
	runGit(t, w, "config", "user.name", "t")
	runGit(t, w, "merge", "--squash", "origin/"+branch)
	runGit(t, w, "commit", "-qm", "squash "+branch)
	runGit(t, w, "push", "-q", "origin", "main")
	runGit(t, w, "push", "-q", "origin", "--delete", branch)
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCheckRepoSquashMergedPassesWithoutForce(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin)
	squashMergeOnOrigin(t, origin, "PROJ-1-work")

	o := Options{DeleteLocal: true}
	r := state.Repo{Name: "alpha", Branch: "PROJ-1-work"}
	var problems []string
	stderr := captureStderr(t, func() {
		problems = CheckRepo(o, l, r, workDir)
	})
	if len(problems) != 0 {
		t.Errorf("problems = %v; want none for a squash-merged branch", problems)
	}
	if !strings.Contains(stderr, "alpha: branch PROJ-1-work squash-merged into origin/main") {
		t.Errorf("stderr = %q; want a squash-merged label", stderr)
	}
}

func TestCheckRepoGenuinelyUnmergedStillBlocks(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin)
	// No squash merge: the branch's commit is nowhere on origin/main.

	o := Options{DeleteLocal: true}
	r := state.Repo{Name: "alpha", Branch: "PROJ-1-work"}
	var problems []string
	_ = captureStderr(t, func() {
		problems = CheckRepo(o, l, r, workDir)
	})
	want := "alpha: branch PROJ-1-work not merged into origin/main"
	if len(problems) != 1 || problems[0] != want {
		t.Errorf("problems = %v; want exactly [%q]", problems, want)
	}
}

func TestTeardownRepoDeletesSquashMergedBranchWithoutForce(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin)
	squashMergeOnOrigin(t, origin, "PROJ-1-work")

	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	// Prune the deleted remote branch (as CheckRepo's fetch does) so
	// `git branch -d` has no merged upstream to accept - the exact case
	// the force-delete fallback exists for.
	if err := repo.Fetch(main); err != nil {
		t.Fatal(err)
	}

	o := Options{DeleteLocal: true}
	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var failures []string
	stderr := captureStderr(t, func() {
		failures = TeardownRepo(o, l, st, st.Repos[0], workDir)
	})
	if len(failures) != 0 {
		t.Errorf("failures = %v; want none for a squash-merged branch", failures)
	}
	if !strings.Contains(stderr, "alpha: branch PROJ-1-work squash-merged; force-deleting") {
		t.Errorf("stderr = %q; want a force-deleting note", stderr)
	}
	exists, err := repo.LocalBranchExists(main, "PROJ-1-work")
	if err != nil || exists {
		t.Errorf("LocalBranchExists = %v, %v; want branch deleted", exists, err)
	}
}

func TestCheckRepoSquashMergedNeverPushedBothDeleteBypassesUnpushed(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranchNoPush(t, l, workDir, "alpha", origin)
	commitOnOrigin(t, origin, "feat.txt", "feature\n", "squash PROJ-1-work")

	o := Options{DeleteLocal: true, DeleteRemote: true}
	r := state.Repo{Name: "alpha", Branch: "PROJ-1-work"}
	var problems []string
	stderr := captureStderr(t, func() {
		problems = CheckRepo(o, l, r, workDir)
	})
	if len(problems) != 0 {
		t.Errorf("problems = %v; want none for a squash-merged never-pushed branch with both delete flags", problems)
	}
	if !strings.Contains(stderr, "alpha: branch PROJ-1-work contained in origin/main; unpushed commits already merged") {
		t.Errorf("stderr = %q; want an unpushed-suppression note", stderr)
	}

	// Teardown must succeed without --force: CheckRepo already fetched, so the
	// local branch force-deletes as a squash merge and there is no remote
	// branch to delete.
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Repos: []state.Repo{r}}
	var failures []string
	_ = captureStderr(t, func() {
		failures = TeardownRepo(o, l, st, st.Repos[0], workDir)
	})
	if len(failures) != 0 {
		t.Errorf("failures = %v; want none", failures)
	}
	exists, err := repo.LocalBranchExists(main, "PROJ-1-work")
	if err != nil || exists {
		t.Errorf("LocalBranchExists = %v, %v; want branch deleted", exists, err)
	}
}

func TestCheckRepoSquashMergedNeverPushedSingleDeleteKeepsUnpushed(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    Options
	}{
		{"delete-local only", Options{DeleteLocal: true}},
		{"no delete flags", Options{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
			workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
			origin := makeOrigin(t)
			setupWorkBranchNoPush(t, l, workDir, "alpha", origin)
			commitOnOrigin(t, origin, "feat.txt", "feature\n", "squash PROJ-1-work")

			r := state.Repo{Name: "alpha", Branch: "PROJ-1-work"}
			var problems []string
			_ = captureStderr(t, func() {
				problems = CheckRepo(tc.o, l, r, workDir)
			})
			want := []string{"alpha: unpushed commits"}
			if !reflect.DeepEqual(problems, want) {
				t.Errorf("problems = %v; want %v (unpushed must fire without both delete flags)", problems, want)
			}
		})
	}
}

func TestCheckRepoSquashMergedWithExtraCommitKeepsBothProblems(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkBranchNoPush(t, l, workDir, "alpha", origin)
	commitOnOrigin(t, origin, "feat.txt", "feature\n", "squash PROJ-1-work")
	// An extra local commit beyond the squash merge: the full merge-base..branch
	// diff is no longer patch-equivalent to origin/main, so containment fails.
	writeCommit(t, wt, "extra.txt", "extra\n", "extra")

	o := Options{DeleteLocal: true, DeleteRemote: true}
	r := state.Repo{Name: "alpha", Branch: "PROJ-1-work"}
	var problems []string
	_ = captureStderr(t, func() {
		problems = CheckRepo(o, l, r, workDir)
	})
	sort.Strings(problems)
	want := []string{
		"alpha: branch PROJ-1-work not merged into origin/main",
		"alpha: unpushed commits",
	}
	if !reflect.DeepEqual(problems, want) {
		t.Errorf("problems = %v; want %v", problems, want)
	}
}

// TestTeardownRepoPartialLeavesFailedRepoInState verifies that when one repo's
// worktree removal fails, that repo stays listed in st.Repos and in the
// on-disk .git-work.yaml, while the succeeded repos are dropped - so a re-run
// resumes precisely the not-torn-down repo.
func TestTeardownRepoPartialLeavesFailedRepoInState(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranchNoPush(t, l, workDir, "alpha", origin)
	betaWt := setupWorkBranchNoPush(t, l, workDir, "beta", origin)
	// An untracked file makes `git worktree remove` refuse without --force,
	// so beta's teardown fails while alpha's succeeds.
	if err := os.WriteFile(filepath.Join(betaWt, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	if err := st.Save(workDir); err != nil {
		t.Fatal(err)
	}

	o := Options{}
	var failures []string
	_ = captureStderr(t, func() {
		for _, r := range append([]state.Repo(nil), st.Repos...) {
			failures = append(failures, TeardownRepo(o, l, st, r, workDir)...)
		}
	})
	if len(failures) != 1 || !strings.Contains(failures[0], "beta: remove worktree") {
		t.Fatalf("failures = %v; want exactly one beta remove-worktree failure", failures)
	}

	// In-memory state keeps only beta.
	if len(st.Repos) != 1 || st.Repos[0].Name != "beta" {
		t.Errorf("st.Repos = %v; want only beta", st.Repos)
	}
	// On-disk state matches: a re-run would resume beta alone.
	reloaded, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repos) != 1 || reloaded.Repos[0].Name != "beta" {
		t.Errorf("reloaded .git-work.yaml repos = %v; want only beta", reloaded.Repos)
	}
}

func TestCheckRepoReportsBranchDrift(t *testing.T) {
	// Teardown deletes r.Branch locally and remotely while the unpushed check
	// measures the worktree's HEAD; a disagreement means checking one branch
	// and deleting another, so it has to be a problem.
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin)
	runGit(t, wt, "checkout", "-q", "-b", "side-quest")

	o := Options{DeleteLocal: true, DeleteRemote: true}
	r := state.Repo{Name: "alpha", Branch: "PROJ-1-work"}
	var problems []string
	_ = captureStderr(t, func() {
		problems = CheckRepo(o, l, r, workDir)
	})
	var drift string
	for _, p := range problems {
		if strings.Contains(p, "state records branch") {
			drift = p
		}
	}
	if drift == "" {
		t.Fatalf("problems = %v; want one naming the branch drift", problems)
	}
	for _, want := range []string{"PROJ-1-work", "side-quest", "git work adopt"} {
		if !strings.Contains(drift, want) {
			t.Errorf("drift problem = %q; want it to mention %q", drift, want)
		}
	}
}

func TestCheckRepoReportsBranchDriftRegardlessOfForce(t *testing.T) {
	// CheckRepo always reports; --force is applied by the callers that consume
	// the list (done.go's `if !o.force`), which is what makes drift bypassable
	// like every other safety-check problem. Pin that layering: suppressing
	// drift inside CheckRepo when Force is set would drop it from the report
	// the operator reads before overriding.
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin)
	runGit(t, wt, "checkout", "-q", "-b", "side-quest")

	r := state.Repo{Name: "alpha", Branch: "PROJ-1-work"}
	drifts := func(o Options) bool {
		var problems []string
		_ = captureStderr(t, func() { problems = CheckRepo(o, l, r, workDir) })
		for _, p := range problems {
			if strings.Contains(p, "state records branch") {
				return true
			}
		}
		return false
	}
	base := Options{DeleteLocal: true, DeleteRemote: true}
	forced := base
	forced.Force = true
	if !drifts(base) || !drifts(forced) {
		t.Errorf("drift reported without Force = %v, with Force = %v; want both true", drifts(base), drifts(forced))
	}
}

func TestCheckRepoReportsDefaultBranch(t *testing.T) {
	// A state file recording the repo's default branch as the work branch
	// passes every other safety check by construction - it is merged into
	// origin/HEAD, has an upstream with nothing unpushed, and is clean - so
	// without this problem nothing at all stands between the operator and
	// deleting main locally and on origin.
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin)

	o := Options{DeleteLocal: true, DeleteRemote: true}
	r := state.Repo{Name: "alpha", Branch: "main"}
	var problems []string
	_ = captureStderr(t, func() { problems = CheckRepo(o, l, r, workDir) })

	var found string
	for _, p := range problems {
		if strings.Contains(p, "default branch") {
			found = p
		}
	}
	if found == "" {
		t.Fatalf("problems = %v; want one naming the default branch", problems)
	}
	if !strings.Contains(found, "--force does not override") {
		t.Errorf("problem = %q; want it to say --force will not help", found)
	}
}

func TestTeardownRepoRefusesDefaultBranchEvenWithForce(t *testing.T) {
	// Unlike every other safety-check problem, this one is not a judgment call,
	// so --force must not get past it - and the refusal has to land before the
	// worktree is removed, leaving the repo exactly as it was.
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "main"}}}
	if err := st.Save(workDir); err != nil {
		t.Fatal(err)
	}

	o := Options{DeleteLocal: true, DeleteRemote: true, Force: true}
	var failures []string
	_ = captureStderr(t, func() {
		failures = TeardownRepo(o, l, st, st.Repos[0], workDir)
	})
	if len(failures) != 1 || !strings.Contains(failures[0], "default branch") {
		t.Fatalf("failures = %v; want exactly one naming the default branch", failures)
	}

	// Nothing was touched: worktree intact, branch intact, repo still listed.
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree %s removed despite the refusal: %v", wt, err)
	}
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := repo.LocalBranchExists(main, "main"); err != nil || !exists {
		t.Errorf("local branch main exists = %v, %v; want true, nil", exists, err)
	}
	if exists, err := repo.RemoteBranchExists(main, "main"); err != nil || !exists {
		t.Errorf("origin/main exists = %v, %v; want true, nil", exists, err)
	}
	if len(st.Repos) != 1 {
		t.Errorf("st.Repos = %v; want alpha still listed after a failed teardown", st.Repos)
	}
}
