package push

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli/clitest"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

// Help is intercepted by Run before parseFlags, so the descriptor and the
// registered flags are what must agree; see TestRunHelp for the behaviour.
func TestHelpConformance(t *testing.T) {
	clitest.Check(t, Cmd, newFlagSet(&options{}))
}

func TestParseFlags(t *testing.T) {
	// A leading-dash --all must be accepted.
	o, err := parseFlags([]string{"--all", "--force", "--allow-empty"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.all || !o.force || !o.allowEmpty || o.repo != "" {
		t.Errorf("opts = %+v", o)
	}

	// A single positional repo, flags on either side of it.
	o, err = parseFlags([]string{"alpha", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if o.repo != "alpha" || !o.force || o.all {
		t.Errorf("opts = %+v", o)
	}
	o, err = parseFlags([]string{"--dry-run", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if o.repo != "alpha" || !o.dryRun {
		t.Errorf("opts = %+v", o)
	}

	// Shorthands: -a, -f, -e, -n.
	o, err = parseFlags([]string{"-a", "-f", "-e", "-n"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.all || !o.force || !o.allowEmpty || !o.dryRun {
		t.Errorf("opts = %+v; want all, force, allowEmpty, dryRun set via shorthands", o)
	}

	// --main composes with everything except --force.
	o, err = parseFlags([]string{"-a", "--main", "-e", "-n"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.all || !o.main || !o.allowEmpty || !o.dryRun || o.force {
		t.Errorf("opts = %+v; want all, main, allowEmpty, dryRun set and force clear", o)
	}
}

func TestParseFlagsErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,                    // bare invocation
		{"--force"},            // flags but no repo and no --all
		{"--all", "alpha"},     // both --all and a repo
		{"alpha", "beta"},      // two positionals
		{"-a", "--main", "-f"}, // force-pushing a default branch is refused
		{"alpha", "--main", "-f"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("parseFlags(%v) = nil error; want error", args)
		}
	}
}

func TestRunHelp(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"--help"}) })
	if code != 0 {
		t.Fatalf("Run(--help) = %d; want 0", code)
	}
	for _, want := range []string{"usage: git work push", "--force", "--allow-empty", "--main", "--dry-run"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output = %q; want %q", out, want)
		}
	}
}

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

// makeOrigin creates a bare origin with one commit (f.txt) on main.
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

// setupWorkBranch clones origin as the primary for name, adds a worktree on a
// new PROJ-1-work branch under workDir, and commits n changes on it (each a
// distinct file). The work branch is NOT pushed. Returns the worktree path.
func setupWorkBranch(t *testing.T, l layout.Layout, workDir, name, origin string, n int) string {
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
	for i := 0; i < n; i++ {
		writeCommit(t, wt, "feat"+strconv.Itoa(i)+".txt", "feature "+strconv.Itoa(i)+"\n", "feat "+strconv.Itoa(i))
	}
	return wt
}

// originBranchCount returns the commit count of branch in the bare origin, or
// -1 if the branch does not exist there.
func originBranchCount(t *testing.T, origin, branch string) int {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", branch)
	cmd.Dir = origin
	out, err := cmd.CombinedOutput()
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// captureStdout runs f while collecting everything it writes to os.Stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func newLayout(t *testing.T) (layout.Layout, string) {
	t.Helper()
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	return l, workDir
}

func TestPushSingleRepoSetsUpstream(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	o := &options{repo: "alpha"}
	var err error
	out := captureStdout(t, func() { err = push(o, l, st, workDir) })
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != 2 {
		t.Fatalf("origin PROJ-1-work commit count = %d; want 2 (base + 1)", got)
	}
	// -u established @{upstream} regardless of the operator's gitconfig.
	up := strings.TrimSpace(runGit(t, wt, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"))
	if up != "origin/PROJ-1-work" {
		t.Errorf("@{upstream} = %q; want origin/PROJ-1-work", up)
	}
	if !strings.Contains(out, "alpha: pushing 1 commit(s) to origin/PROJ-1-work") ||
		!strings.Contains(out, "feat 0") ||
		!strings.Contains(out, "alpha: pushed PROJ-1-work to origin") {
		t.Errorf("output = %q; want pushing summary, commit detail, and pushed confirmation", out)
	}

	// Re-run: already fully pushed repos stay in scope and no-op via git.
	if err := push(o, l, st, workDir); err != nil {
		t.Fatalf("re-run push: %v", err)
	}
}

func TestPushEmptyBranchSkippedUnlessAllowEmpty(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 0)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}

	// Without --allow-empty the named repo is skipped (uniform predicate) and
	// the run still succeeds.
	if err := push(&options{repo: "alpha"}, l, st, workDir); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != -1 {
		t.Fatalf("origin PROJ-1-work exists (count %d); want no branch without --allow-empty", got)
	}

	// With --allow-empty the placeholder branch is pushed.
	var err error
	out := captureStdout(t, func() {
		err = push(&options{repo: "alpha", allowEmpty: true}, l, st, workDir)
	})
	if err != nil {
		t.Fatalf("push --allow-empty: %v", err)
	}
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != 1 {
		t.Fatalf("origin PROJ-1-work commit count = %d; want 1 (base only)", got)
	}
	if !strings.Contains(out, "alpha: pushing empty branch PROJ-1-work to origin") {
		t.Errorf("output = %q; want empty-branch summary", out)
	}
}

// TestPushAllContinuesPastFailures pins the continue-and-collect model: a
// rejected repo does not stop the others, and the collected error names the
// repo and the -f remedy.
func TestPushAllContinuesPastFailures(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "beta", origin, 1)  // will be rejected
	setupWorkBranch(t, l, workDir, "alpha", origin, 1) // must still push

	// Give beta its own origin (alpha and beta would otherwise share one and
	// a divergent PROJ-1-work would reject both), then diverge origin2's
	// PROJ-1-work so only beta's push is a non-fast-forward.
	origin2 := makeOrigin(t)
	betaMain, err := l.RepoMain("beta")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, betaMain, "remote", "set-url", "origin", origin2)
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, "", "clone", "-q", origin2, other)
	runGit(t, other, "config", "user.email", "t@t.t")
	runGit(t, other, "config", "user.name", "t")
	runGit(t, other, "checkout", "-qb", "PROJ-1-work")
	writeCommit(t, other, "divergent.txt", "other\n", "divergent")
	runGit(t, other, "push", "-q", "-u", "origin", "PROJ-1-work")

	st := &state.State{Repos: []state.Repo{
		{Name: "beta", Branch: "PROJ-1-work"},
		{Name: "alpha", Branch: "PROJ-1-work"},
	}}
	err = push(&options{all: true}, l, st, workDir)
	if err == nil {
		t.Fatal("push --all: expected a collected failure for beta")
	}
	if !strings.Contains(err.Error(), "beta") || !strings.Contains(err.Error(), "-f/--force") {
		t.Errorf("error = %q; want it to name beta and the -f/--force remedy", err)
	}
	// alpha, listed after the failing beta, still pushed.
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != 2 {
		t.Errorf("origin PROJ-1-work commit count = %d; want 2 (alpha pushed despite beta's failure)", got)
	}
	// beta's divergent remote branch was not clobbered.
	if got := originBranchCount(t, origin2, "PROJ-1-work"); got != 2 {
		t.Errorf("origin2 PROJ-1-work commit count = %d; want 2 (untouched)", got)
	}
}

// TestPushForceWithLease: an amended (diverged) branch needs -f, and -f is
// --force-with-lease - it still refuses when the remote has moved beyond our
// remote-tracking refs.
func TestPushForceWithLease(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := push(&options{repo: "alpha"}, l, st, workDir); err != nil {
		t.Fatalf("initial push: %v", err)
	}

	// Amend: local diverges from the pushed branch.
	runGit(t, wt, "commit", "-q", "--amend", "-m", "feat 0 amended")
	if err := push(&options{repo: "alpha"}, l, st, workDir); err == nil {
		t.Fatal("push after amend: expected non-fast-forward rejection without -f")
	}
	if err := push(&options{repo: "alpha", force: true}, l, st, workDir); err != nil {
		t.Fatalf("push -f after amend: %v", err)
	}
	subj := strings.TrimSpace(runGit(t, origin, "log", "-1", "--format=%s", "PROJ-1-work"))
	if subj != "feat 0 amended" {
		t.Errorf("origin tip subject = %q; want the amended commit", subj)
	}

	// Lease: someone else advances the remote branch; our amend + -f must be
	// rejected because our remote-tracking ref is stale.
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, "", "clone", "-q", "-b", "PROJ-1-work", origin, other)
	runGit(t, other, "config", "user.email", "t@t.t")
	runGit(t, other, "config", "user.name", "t")
	writeCommit(t, other, "unseen.txt", "unseen\n", "unseen")
	runGit(t, other, "push", "-q", "origin", "PROJ-1-work")

	runGit(t, wt, "commit", "-q", "--amend", "-m", "feat 0 re-amended")
	if err := push(&options{repo: "alpha", force: true}, l, st, workDir); err == nil {
		t.Fatal("push -f with stale remote-tracking refs: expected a lease rejection")
	}
	subj = strings.TrimSpace(runGit(t, origin, "log", "-1", "--format=%s", "PROJ-1-work"))
	if subj != "unseen" {
		t.Errorf("origin tip subject = %q; want the unseen commit preserved", subj)
	}
}

// TestPushIntegratedUnpublishedBranchReachesOrigin pins leg one's reference
// point: an integrate without --push moves the commits onto local main only,
// so measured against the LOCAL default branch there would be nothing to do -
// but origin has the work nowhere at all. push must publish the branch.
func TestPushIntegratedUnpublishedBranchReachesOrigin(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "merge", "-q", "--ff-only", "PROJ-1-work")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var perr error
	out := captureStdout(t, func() { perr = push(&options{all: true}, l, st, workDir) })
	if perr != nil {
		t.Fatalf("push -a after local-only integrate: %v", perr)
	}
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != 2 {
		t.Fatalf("origin PROJ-1-work commit count = %d; want 2 (base + feat 0)", got)
	}
	if got := originBranchCount(t, origin, "main"); got != 1 {
		t.Fatalf("origin main commit count = %d; want 1 (push must not touch main)", got)
	}
	if !strings.Contains(out, "alpha: pushing 1 commit(s) to origin/PROJ-1-work") ||
		!strings.Contains(out, "feat 0") {
		t.Errorf("output = %q; want the unpublished commit reported", out)
	}
}

// TestPushIntegratedAndPushedMainStaysLocal pins the complement: once the
// integrated commits are on origin/<default>, a branch that was never
// published has nothing origin lacks and stays local - the untouched-repo
// protection that keeps --all from littering origin with placeholder branches.
func TestPushIntegratedAndPushedMainStaysLocal(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "merge", "-q", "--ff-only", "PROJ-1-work")
	runGit(t, main, "push", "-q", "origin", "main")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := push(&options{all: true}, l, st, workDir); err != nil {
		t.Fatalf("push -a: %v", err)
	}
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != -1 {
		t.Errorf("origin PROJ-1-work exists (count %d); want the published-via-main branch kept local", got)
	}
}

// TestPushAfterIntegrateUpdatesRemoteBranch pins the second scope leg: once
// commits are integrated and the default branch pushed, leg one is zero, but
// origin's feature branch may still be missing the last of them. push must
// bring origin/<branch> up to date rather than skip the repo.
func TestPushAfterIntegrateUpdatesRemoteBranch(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := push(&options{repo: "alpha"}, l, st, workDir); err != nil {
		t.Fatalf("initial push: %v", err)
	}

	// One more commit, then integrate and push main - what `git work
	// integrate --push` leaves behind (fast-forward route).
	writeCommit(t, wt, "feat1.txt", "feature 1\n", "feat 1")
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "merge", "-q", "--ff-only", "PROJ-1-work")
	runGit(t, main, "push", "-q", "origin", "main")

	var perr error
	out := captureStdout(t, func() { perr = push(&options{all: true}, l, st, workDir) })
	if perr != nil {
		t.Fatalf("push -a after integrate: %v", perr)
	}
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != 3 {
		t.Fatalf("origin PROJ-1-work commit count = %d; want 3 (base + feat 0 + feat 1)", got)
	}
	if !strings.Contains(out, "alpha: pushing 1 commit(s) to origin/PROJ-1-work") ||
		!strings.Contains(out, "feat 1") {
		t.Errorf("output = %q; want the unpushed commit reported", out)
	}

	// With origin/<branch> current and nothing un-integrated, a re-run skips.
	out = captureStdout(t, func() { perr = push(&options{all: true}, l, st, workDir) })
	if perr != nil {
		t.Fatalf("re-run push -a: %v", perr)
	}
	if strings.Contains(out, "pushed") {
		t.Errorf("output = %q; want the up-to-date repo skipped", out)
	}
}

// TestPushAfterRewriteStaysInScope pins the unpushed leg's ancestry
// measurement: an amended (or rebased) branch holds the same patches under new
// SHAs, so patch equivalence sees nothing to push while origin's ref still
// needs moving. The repo must stay in scope, surfacing the usual
// non-fast-forward rejection and succeeding with -f.
func TestPushAfterRewriteStaysInScope(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := push(&options{repo: "alpha"}, l, st, workDir); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "merge", "-q", "--ff-only", "PROJ-1-work")

	// Message-only amend: identical patch, new SHA.
	runGit(t, wt, "commit", "-q", "--amend", "-m", "feat 0 reworded")
	if err := push(&options{all: true}, l, st, workDir); err == nil {
		t.Fatal("push after amend: expected non-fast-forward rejection without -f")
	}
	if err := push(&options{all: true, force: true}, l, st, workDir); err != nil {
		t.Fatalf("push -f after amend: %v", err)
	}
	subj := strings.TrimSpace(runGit(t, origin, "log", "-1", "--format=%s", "PROJ-1-work"))
	if subj != "feat 0 reworded" {
		t.Errorf("origin tip subject = %q; want the reworded commit", subj)
	}
}

// commitOnMain commits n changes directly on the primary clone's default branch,
// standing in for what `git work integrate` leaves behind: local main ahead of
// origin/main, and a work branch whose commits are already there.
func commitOnMain(t *testing.T, l layout.Layout, name string, n int) string {
	t.Helper()
	main, err := l.RepoMain(name)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		writeCommit(t, main, "integrated"+strconv.Itoa(i)+".txt", "integrated "+strconv.Itoa(i)+"\n", "integrated "+strconv.Itoa(i))
	}
	return main
}

// TestPushMainAfterIntegrate is the headline case: once commits are integrated onto main
// the work branch has nothing un-integrated, so the default mode skips the repo
// entirely. --main must still push main.
func TestPushMainAfterIntegrate(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 0)
	commitOnMain(t, l, "alpha", 2)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}

	// Default mode: nothing un-integrated on the work branch, so nothing happens.
	if err := push(&options{all: true}, l, st, workDir); err != nil {
		t.Fatalf("push -a: %v", err)
	}
	if got := originBranchCount(t, origin, "main"); got != 1 {
		t.Fatalf("origin main commit count = %d; want 1 (default mode must not touch main)", got)
	}

	var err error
	out := captureStdout(t, func() { err = push(&options{all: true, main: true}, l, st, workDir) })
	if err != nil {
		t.Fatalf("push -a --main: %v", err)
	}
	if got := originBranchCount(t, origin, "main"); got != 3 {
		t.Fatalf("origin main commit count = %d; want 3 (base + 2 integrated)", got)
	}
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != -1 {
		t.Errorf("origin PROJ-1-work exists (count %d); --main must not push the work branch", got)
	}
	if !strings.Contains(out, "alpha: pushing 2 commit(s) to origin/main") ||
		!strings.Contains(out, "integrated 0") || !strings.Contains(out, "integrated 1") ||
		!strings.Contains(out, "alpha: pushed main to origin") {
		t.Errorf("output = %q; want pushing summary, commit detail, and pushed confirmation", out)
	}
}

// TestPushMainUpToDateSkippedUnlessAllowEmpty pins the nothing-to-push skip and
// the -e escape hatch, mirroring the empty-work-branch behavior.
func TestPushMainUpToDateSkippedUnlessAllowEmpty(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := push(&options{repo: "alpha", main: true}, l, st, workDir); err != nil {
		t.Fatalf("push --main: %v", err)
	}

	var err error
	out := captureStdout(t, func() {
		err = push(&options{repo: "alpha", main: true, allowEmpty: true}, l, st, workDir)
	})
	if err != nil {
		t.Fatalf("push --main -e: %v", err)
	}
	if !strings.Contains(out, "alpha: pushing main to origin (already up to date)") {
		t.Errorf("output = %q; want the up-to-date push summary", out)
	}
	// Either way the work branch stays local.
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != -1 {
		t.Errorf("origin PROJ-1-work exists (count %d); --main must not push the work branch", got)
	}
}

// TestPushMainIgnoresWorktreeAndBranch: --main reads only the primary clone, so
// a torn-down worktree (or a deleted work branch) is not a reason to skip.
func TestPushMainIgnoresWorktreeAndBranch(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 0)
	main := commitOnMain(t, l, "alpha", 1)
	runGit(t, main, "worktree", "remove", "--force", wt)
	runGit(t, main, "branch", "-D", "PROJ-1-work")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := push(&options{all: true, main: true}, l, st, workDir); err != nil {
		t.Fatalf("push -a --main: %v", err)
	}
	if got := originBranchCount(t, origin, "main"); got != 2 {
		t.Fatalf("origin main commit count = %d; want 2 (base + 1 integrated)", got)
	}
}

// TestPushMainRejectionCollected: a diverged origin/main is a per-repo failure
// pointing at pull, not at -f (which --main refuses outright).
func TestPushMainRejectionCollected(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 0)
	commitOnMain(t, l, "alpha", 1)

	// Someone else advances origin/main behind our back.
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, "", "clone", "-q", origin, other)
	runGit(t, other, "config", "user.email", "t@t.t")
	runGit(t, other, "config", "user.name", "t")
	writeCommit(t, other, "theirs.txt", "theirs\n", "theirs")
	runGit(t, other, "push", "-q", "origin", "main")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	err := push(&options{all: true, main: true}, l, st, workDir)
	if err == nil {
		t.Fatal("push -a --main: expected a non-fast-forward failure")
	}
	if !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "pull main") {
		t.Errorf("error = %q; want it to name alpha and the pull remedy", err)
	}
	if strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %q; --main must not suggest force-pushing a default branch", err)
	}
	if got := originBranchCount(t, origin, "main"); got != 2 {
		t.Errorf("origin main commit count = %d; want 2 (their commit preserved)", got)
	}
}

func TestPushMainDryRunPushesNothing(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 0)
	commitOnMain(t, l, "alpha", 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var err error
	out := captureStdout(t, func() {
		err = push(&options{all: true, main: true, dryRun: true}, l, st, workDir)
	})
	if err != nil {
		t.Fatalf("push -a --main -n: %v", err)
	}
	if got := originBranchCount(t, origin, "main"); got != 1 {
		t.Fatalf("origin main commit count = %d; a dry run must push nothing", got)
	}
	if !strings.Contains(out, "alpha: would push 1 commit(s) to origin/main") || !strings.Contains(out, "integrated 0") {
		t.Errorf("output = %q; want would-push summary with commit detail", out)
	}
	if strings.Contains(out, "pushed main") {
		t.Errorf("output = %q; a dry run must not claim anything was pushed", out)
	}
}

func TestPushDryRunPushesNothing(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 2)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var err error
	out := captureStdout(t, func() {
		err = push(&options{repo: "alpha", dryRun: true, force: true}, l, st, workDir)
	})
	if err != nil {
		t.Fatalf("push --dry-run: %v", err)
	}
	if got := originBranchCount(t, origin, "PROJ-1-work"); got != -1 {
		t.Fatalf("origin PROJ-1-work exists (count %d); a dry run must push nothing", got)
	}
	if !strings.Contains(out, "alpha: would push 2 commit(s) to origin/PROJ-1-work (force-with-lease)") ||
		!strings.Contains(out, "feat 0") || !strings.Contains(out, "feat 1") {
		t.Errorf("output = %q; want would-push summary with force note and commit detail", out)
	}
	if strings.Contains(out, "pushed") {
		t.Errorf("output = %q; a dry run must not claim anything was pushed", out)
	}
}

func TestPushRefusesWhenWorktreeIsOnAnotherBranch(t *testing.T) {
	// push targets the branch state records, so a worktree sitting somewhere
	// else means publishing a branch the operator is not looking at.
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 1)
	runGit(t, wt, "checkout", "-q", "-b", "side-quest")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	err := push(&options{all: true}, l, st, workDir)
	if err == nil {
		t.Fatal("push succeeded; want a branch-drift failure")
	}
	for _, want := range []string{"PROJ-1-work", "side-quest", "git work adopt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v; want it to mention %q", err, want)
		}
	}
	// Nothing was published.
	if n := originBranchCount(t, origin, "PROJ-1-work"); n != -1 {
		t.Errorf("origin has PROJ-1-work (%d commits); want nothing pushed", n)
	}
}

func TestPushMainIgnoresBranchDrift(t *testing.T) {
	// --main reads only the primary clone; the worktree and work branch are
	// deliberately out of scope, so drift must not block it.
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 1)
	runGit(t, wt, "checkout", "-q", "-b", "side-quest")
	commitOnMain(t, l, "alpha", 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	out := captureStdout(t, func() {
		if err := push(&options{all: true, main: true}, l, st, workDir); err != nil {
			t.Fatalf("push --main: %v", err)
		}
	})
	if !strings.Contains(out, "pushed main to origin") {
		t.Errorf("stdout = %q; want the default branch pushed despite drift", out)
	}
}
