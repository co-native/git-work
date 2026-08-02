package integrate

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli/clitest"
	"github.com/co-native/git-work/internal/config"
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
	// A leading-dash --all must be accepted (unlike done's parseFlags).
	o, err := parseFlags([]string{"--all", "--multi", "--push", "--no-fetch"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.all || !o.multi || !o.push || !o.noFetch || o.repo != "" {
		t.Errorf("opts = %+v", o)
	}

	// A single positional repo, flags on either side of it.
	o, err = parseFlags([]string{"alpha", "--push"})
	if err != nil {
		t.Fatal(err)
	}
	if o.repo != "alpha" || !o.push || o.all {
		t.Errorf("opts = %+v", o)
	}
	o, err = parseFlags([]string{"--push", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if o.repo != "alpha" || !o.push {
		t.Errorf("opts = %+v", o)
	}

	// Shorthands: -a for --all, -n for --dry-run.
	o, err = parseFlags([]string{"-a", "-n"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.all || !o.dryRun {
		t.Errorf("opts = %+v; want all and dryRun set via shorthands", o)
	}
	o, err = parseFlags([]string{"--dry-run", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if o.repo != "alpha" || !o.dryRun {
		t.Errorf("opts = %+v; want repo alpha with dryRun", o)
	}
}

func TestRunHelp(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"--help"}) })
	if code != 0 {
		t.Fatalf("Run(--help) = %d; want 0", code)
	}
	for _, want := range []string{"usage: git work integrate", "--multi", "--push", "--dry-run", "-h, --help"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output = %q; want %q", out, want)
		}
	}
}

// TestRunMalformedInvocationExitsTwo pins the exit-code half of the contract:
// a bare `integrate` never reaches the work folder, so it is a usage error (2), not
// a failure (1).
func TestRunMalformedInvocationExitsTwo(t *testing.T) {
	if code := Run(nil); code != 2 {
		t.Errorf("Run(nil) = %d; want 2 (malformed invocation)", code)
	}
}

func TestParseFlagsErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,                // bare invocation
		{"--push"},         // flags but no repo and no --all
		{"--all", "alpha"}, // both --all and a repo
		{"alpha", "beta"},  // two positionals
	} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("parseFlags(%v) = nil error; want error", args)
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
// distinct file). The work branch is NOT pushed - integrate's world is one where the
// work branch lives only locally. Returns the worktree path.
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

// commitCount returns the number of commits reachable from the primary's HEAD
// (its default branch).
func commitCount(t *testing.T, l layout.Layout, name string) int {
	t.Helper()
	main, err := l.RepoMain(name)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.TrimSpace(runGit(t, main, "rev-list", "--count", "HEAD"))
	n, err := strconv.Atoi(out)
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

func TestIntegrateSingleCommitSucceedsAndIsIdempotent(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	o := &options{repo: "alpha", noFetch: true}
	if err := integrate(o, localCfg(), l, st, workDir); err != nil {
		t.Fatalf("integrate: %v", err)
	}
	if got := commitCount(t, l, "alpha"); got != 2 {
		t.Fatalf("after integrate, main commit count = %d; want 2 (base + 1 integrated)", got)
	}

	// Re-run: the commit is already present, so nothing integrates again.
	if err := integrate(o, localCfg(), l, st, workDir); err != nil {
		t.Fatalf("re-run integrate: %v", err)
	}
	if got := commitCount(t, l, "alpha"); got != 2 {
		t.Fatalf("after re-run, main commit count = %d; want 2 (no double-apply)", got)
	}
}

func TestIntegrateAllTwoCommitRepoWithoutMultiAborts(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1) // would integrate
	setupWorkBranch(t, l, workDir, "beta", origin, 2)  // trips the >=2 guard

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	o := &options{all: true, noFetch: true}
	err := integrate(o, localCfg(), l, st, workDir)
	if err == nil {
		t.Fatal("integrate: expected abort for a 2-commit repo without --multi")
	}
	if !strings.Contains(err.Error(), "beta") || !strings.Contains(err.Error(), "--multi") {
		t.Errorf("error = %q; want it to name beta and --multi", err)
	}
	// Zero cherry-picks: neither primary moved.
	if got := commitCount(t, l, "alpha"); got != 1 {
		t.Errorf("alpha commit count = %d; want 1 (no picks)", got)
	}
	if got := commitCount(t, l, "beta"); got != 1 {
		t.Errorf("beta commit count = %d; want 1 (no picks)", got)
	}

	// With --multi the same set integrates.
	o.multi = true
	if err := integrate(o, localCfg(), l, st, workDir); err != nil {
		t.Fatalf("integrate --multi: %v", err)
	}
	if got := commitCount(t, l, "alpha"); got != 2 {
		t.Errorf("alpha commit count = %d; want 2", got)
	}
	if got := commitCount(t, l, "beta"); got != 3 {
		t.Errorf("beta commit count = %d; want 3 (base + 2)", got)
	}
}

func TestIntegrateAllDirtyPrimaryAborts(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1) // clean, would integrate
	setupWorkBranch(t, l, workDir, "beta", origin, 1)

	// Dirty beta's primary clone (an untracked file in its working tree).
	betaMain, err := l.RepoMain("beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaMain, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	o := &options{all: true, noFetch: true}
	err = integrate(o, localCfg(), l, st, workDir)
	if err == nil {
		t.Fatal("integrate: expected pre-flight abort for a dirty primary")
	}
	if !strings.Contains(err.Error(), "beta") || !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error = %q; want it to name beta's uncommitted changes", err)
	}
	if got := commitCount(t, l, "alpha"); got != 1 {
		t.Errorf("alpha commit count = %d; want 1 (zero picks on abort)", got)
	}
}

func TestIntegrateConflictHaltsAndLeavesPickInProgress(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 0)

	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	// Two adds of the same path with different content: one on the local
	// default branch, one on the work branch -> cherry-pick conflicts.
	writeCommit(t, main, "clash.txt", "mainside\n", "mainside")
	writeCommit(t, wt, "clash.txt", "workside\n", "workside")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	o := &options{repo: "alpha", noFetch: true}
	err = integrate(o, localCfg(), l, st, workDir)
	if err == nil {
		t.Fatal("integrate: expected a conflict error")
	}
	if !strings.Contains(err.Error(), "conflict") || !strings.Contains(err.Error(), "cherry-pick --abort") {
		t.Errorf("error = %q; want a conflict + abort-instruction message", err)
	}
	// The pick is left in progress: CHERRY_PICK_HEAD present, and an abort
	// succeeds (proving one was mid-sequence).
	gitPath := strings.TrimSpace(runGit(t, main, "rev-parse", "--git-path", "CHERRY_PICK_HEAD"))
	if !filepath.IsAbs(gitPath) {
		gitPath = filepath.Join(main, gitPath)
	}
	if _, err := os.Stat(gitPath); err != nil {
		t.Fatalf("CHERRY_PICK_HEAD not present (%v); pick was not left in progress", err)
	}
	runGit(t, main, "cherry-pick", "--abort")
}

// TestIntegrateDryRunFastForwardsButSkipsPicks pins the decided dry-run semantics:
// the pre-flight runs for real - including the fetch and the fast-forward of
// the local default branch - and only the cherry-picks are skipped, so the
// report measures exactly what a real run would integrate.
func TestIntegrateDryRunFastForwardsButSkipsPicks(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	// Advance origin's main so the pre-flight fast-forward has work to do.
	seed2 := filepath.Join(t.TempDir(), "seed2")
	runGit(t, "", "clone", "-q", origin, seed2)
	runGit(t, seed2, "config", "user.email", "t@t.t")
	runGit(t, seed2, "config", "user.name", "t")
	writeCommit(t, seed2, "up.txt", "upstream\n", "upstream")
	runGit(t, seed2, "push", "-q", "origin", "main")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	o := &options{repo: "alpha", dryRun: true} // fetch on: the FF must happen
	var err error
	out := captureStdout(t, func() { err = integrate(o, localCfg(), l, st, workDir) })
	if err != nil {
		t.Fatalf("integrate --dry-run: %v", err)
	}
	// Local main advanced to origin's 2 commits (FF) - but the work commit
	// was not picked (3 would mean it integrated).
	if got := commitCount(t, l, "alpha"); got != 2 {
		t.Errorf("local main commit count = %d; want 2 (fast-forwarded, zero picks)", got)
	}
	if !strings.Contains(out, "alpha: would integrate 1 onto main") || !strings.Contains(out, "feat 0") {
		t.Errorf("output = %q; want a would-integrate summary with commit detail", out)
	}
	if strings.Contains(out, "integrated onto") {
		t.Errorf("output = %q; a dry run must not claim anything integrated", out)
	}
}

// TestIntegrateDryRunMultiViolationStillListsCommits: the multi guard fires after
// the commits are computed, so a dry run reports the violation (nonzero) AND
// prints the offending repo's commit detail.
func TestIntegrateDryRunMultiViolationStillListsCommits(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "beta", origin, 2)

	st := &state.State{Repos: []state.Repo{{Name: "beta", Branch: "PROJ-1-work"}}}
	o := &options{repo: "beta", noFetch: true, dryRun: true}
	var err error
	out := captureStdout(t, func() { err = integrate(o, localCfg(), l, st, workDir) })
	if err == nil || !strings.Contains(err.Error(), "--multi") {
		t.Fatalf("err = %v; want the --multi violation", err)
	}
	if !strings.Contains(out, "beta: would integrate 2 onto main") ||
		!strings.Contains(out, "feat 0") || !strings.Contains(out, "feat 1") {
		t.Errorf("output = %q; want commit detail despite the violation", out)
	}
	if got := commitCount(t, l, "beta"); got != 1 {
		t.Errorf("beta commit count = %d; want 1 (no picks)", got)
	}
}

// TestIntegratePrintsCommitDetail: a real run lists what it integrates, one line per
// commit (short SHA + subject), alongside the existing summary lines.
func TestIntegratePrintsCommitDetail(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	o := &options{repo: "alpha", noFetch: true}
	var err error
	out := captureStdout(t, func() { err = integrate(o, localCfg(), l, st, workDir) })
	if err != nil {
		t.Fatalf("integrate: %v", err)
	}
	if !strings.Contains(out, "alpha: integrating 1 onto main") ||
		!strings.Contains(out, "feat 0") ||
		!strings.Contains(out, "alpha: 1 integrated onto main") {
		t.Errorf("output = %q; want integrating summary, commit detail, and integrated count", out)
	}
}

func TestIntegratePushPushesDefaultBranch(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	o := &options{repo: "alpha", noFetch: true, push: true}
	if err := integrate(o, localCfg(), l, st, workDir); err != nil {
		t.Fatalf("integrate --push: %v", err)
	}
	// origin's main advanced to include the integrated commit.
	got := strings.TrimSpace(runGit(t, origin, "rev-list", "--count", "main"))
	if got != "2" {
		t.Errorf("origin main commit count = %s; want 2 (pushed)", got)
	}
}

func TestIntegrateWithoutPushLeavesOriginBehind(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	o := &options{repo: "alpha", noFetch: true}
	if err := integrate(o, localCfg(), l, st, workDir); err != nil {
		t.Fatalf("integrate: %v", err)
	}
	if got := commitCount(t, l, "alpha"); got != 2 {
		t.Errorf("local main commit count = %d; want 2", got)
	}
	// Nothing pushed without --push.
	got := strings.TrimSpace(runGit(t, origin, "rev-list", "--count", "main"))
	if got != "1" {
		t.Errorf("origin main commit count = %s; want 1 (not pushed)", got)
	}
}

func TestIntegrateAbortsWhenWorktreeIsOnAnotherBranch(t *testing.T) {
	// Pre-flight is atomic: one drifted repo aborts the whole run with zero
	// cherry-picks, including for the repos that were fine.
	l, workDir := newLayout(t)
	alphaOrigin := makeOrigin(t)
	alphaWt := setupWorkBranch(t, l, workDir, "alpha", alphaOrigin, 1)
	runGit(t, alphaWt, "checkout", "-q", "-b", "side-quest")

	betaOrigin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "beta", betaOrigin, 1)
	betaBefore := commitCount(t, l, "beta")

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	err := integrate(&options{all: true}, localCfg(), l, st, workDir)
	if err == nil {
		t.Fatal("integrate succeeded; want a pre-flight abort")
	}
	for _, want := range []string{"PROJ-1-work", "side-quest", "git work adopt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v; want it to mention %q", err, want)
		}
	}
	if got := commitCount(t, l, "beta"); got != betaBefore {
		t.Errorf("beta main moved (%d -> %d); pre-flight must abort atomically", betaBefore, got)
	}
}

// tipSHA returns the commit rev resolves to in dir.
func tipSHA(t *testing.T, dir, rev string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, "rev-parse", rev))
}

// advanceOriginMain pushes an unrelated commit to origin's main, so integrate's
// pre-flight fast-forwards local main past the work branch's base and a
// fast-forward is no longer possible.
func advanceOriginMain(t *testing.T, origin string) {
	t.Helper()
	w := filepath.Join(t.TempDir(), "adv")
	runGit(t, "", "clone", "-q", origin, w)
	runGit(t, w, "config", "user.email", "t@t.t")
	runGit(t, w, "config", "user.name", "t")
	writeCommit(t, w, "upstream.txt", "upstream\n", "upstream work")
	runGit(t, w, "push", "-q", "origin", "main")
}

func TestIntegrateFastForwardsWhenPossibleAndPreservesSHAs(t *testing.T) {
	// The point of preferring a fast-forward: the commits keep their
	// identities, so an already-pushed work branch matches the default branch
	// exactly instead of holding different objects with the same content.
	l, workDir := newLayout(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", makeOrigin(t), 1)
	branchTip := tipSHA(t, wt, "HEAD")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	out := captureStdout(t, func() {
		if err := integrate(&options{all: true}, localCfg(), l, st, workDir); err != nil {
			t.Fatalf("integrate: %v", err)
		}
	})
	if !strings.Contains(out, "integrating 1 onto main (fast-forward)") {
		t.Errorf("stdout = %q; want the fast-forward mechanism reported", out)
	}
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got := tipSHA(t, main, "main"); got != branchTip {
		t.Errorf("main tip = %s; want the work branch's own commit %s (a fast-forward must not rewrite)", got, branchTip)
	}
}

func TestIntegrateCherryPicksWhenTheDefaultBranchMoved(t *testing.T) {
	// Diverged: main has a commit the branch does not, so no fast-forward is
	// possible and the existing cherry-pick path must still run.
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", origin, 1)
	advanceOriginMain(t, origin)
	branchTip := tipSHA(t, wt, "HEAD")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	out := captureStdout(t, func() {
		if err := integrate(&options{all: true}, localCfg(), l, st, workDir); err != nil {
			t.Fatalf("integrate: %v", err)
		}
	})
	if !strings.Contains(out, "integrating 1 onto main (cherry-pick)") {
		t.Errorf("stdout = %q; want the cherry-pick mechanism reported", out)
	}
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got := tipSHA(t, main, "main"); got == branchTip {
		t.Errorf("main tip = %s; a cherry-pick onto a moved main must create a new commit", got)
	}
	// The upstream commit and the branch's change are both present.
	if _, err := os.Stat(filepath.Join(main, "upstream.txt")); err != nil {
		t.Errorf("primary missing the upstream commit's file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(main, "feat0.txt")); err != nil {
		t.Errorf("primary missing the integrated commit's file: %v", err)
	}
}

func TestIntegrateFastForwardStillNeedsMulti(t *testing.T) {
	// The --multi guard is about intent, not mechanism: a fast-forward carries
	// no per-commit risk, but integrating commits you forgot about is the same
	// surprise either way, so one rule covers both.
	l, workDir := newLayout(t)
	setupWorkBranch(t, l, workDir, "alpha", makeOrigin(t), 2)
	before := commitCount(t, l, "alpha")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	err := integrate(&options{all: true}, localCfg(), l, st, workDir)
	if err == nil || !strings.Contains(err.Error(), "2 un-integrated commits; pass --multi") {
		t.Fatalf("integrate = %v; want the multi guard to fire on a fast-forwardable branch", err)
	}
	if got := commitCount(t, l, "alpha"); got != before {
		t.Errorf("main moved (%d -> %d) despite the guard", before, got)
	}

	// With --multi it fast-forwards, still preserving the commits.
	wt := filepath.Join(workDir, "alpha")
	branchTip := tipSHA(t, wt, "HEAD")
	out := captureStdout(t, func() {
		if err := integrate(&options{all: true, multi: true}, localCfg(), l, st, workDir); err != nil {
			t.Fatalf("integrate --multi: %v", err)
		}
	})
	if !strings.Contains(out, "integrating 2 onto main (fast-forward)") {
		t.Errorf("stdout = %q; want a 2-commit fast-forward", out)
	}
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got := tipSHA(t, main, "main"); got != branchTip {
		t.Errorf("main tip = %s; want the work branch tip %s", got, branchTip)
	}
}

func TestIntegrateDryRunReportsTheMechanism(t *testing.T) {
	l, workDir := newLayout(t)

	// alpha: nothing integrated on main since it branched -> fast-forward.
	setupWorkBranch(t, l, workDir, "alpha", makeOrigin(t), 1)
	// beta: main moved -> cherry-pick.
	betaOrigin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "beta", betaOrigin, 1)
	advanceOriginMain(t, betaOrigin)

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	before := map[string]int{"alpha": commitCount(t, l, "alpha"), "beta": commitCount(t, l, "beta")}
	out := captureStdout(t, func() {
		if err := integrate(&options{all: true, dryRun: true}, localCfg(), l, st, workDir); err != nil {
			t.Fatalf("integrate -n: %v", err)
		}
	})
	if !strings.Contains(out, "alpha: would integrate 1 onto main (fast-forward)") {
		t.Errorf("stdout = %q; want alpha previewed as a fast-forward", out)
	}
	if !strings.Contains(out, "beta: would integrate 1 onto main (cherry-pick)") {
		t.Errorf("stdout = %q; want beta previewed as a cherry-pick", out)
	}
	// beta's count grew by the pre-flight's fast-forward of main to origin,
	// which -n runs deliberately; neither repo integrated anything.
	if got := commitCount(t, l, "alpha"); got != before["alpha"] {
		t.Errorf("alpha main moved during a dry run (%d -> %d)", before["alpha"], got)
	}
}

// localCfg is a config with no policy set, so every repo resolves to the
// local route - what the tests above assume and what git-work did before the
// integration key existed.
func localCfg() *config.Config { return &config.Config{} }

// prCfg makes the named repos PR-only, leaving the rest on the local route.
func prCfg(names ...string) *config.Config {
	c := &config.Config{Repos: map[string]config.RepoConfig{}}
	for _, n := range names {
		c.Repos[n] = config.RepoConfig{Integration: config.IntegrationPR}
	}
	return c
}

// TestIntegrateNamedPROnlyRepoFails: naming a repo whose route is `pr` is
// asking for something policy forbids, so it fails loudly rather than
// succeeding silently having done nothing.
func TestIntegrateNamedPROnlyRepoFails(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	o := &options{repo: "alpha", noFetch: true}
	err := integrate(o, prCfg("alpha"), l, st, workDir)
	if err == nil {
		t.Fatal("naming a PR-only repo should fail")
	}
	if !strings.Contains(err.Error(), "pull request") {
		t.Errorf("error should explain the route: %v", err)
	}
	if got := commitCount(t, l, "alpha"); got != 1 {
		t.Errorf("main commit count = %d; want 1 (nothing integrated)", got)
	}
}

// TestIntegrateAllSkipsPROnlyRepo: under --all a PR-only repo is declined
// with a note and the run still succeeds, integrating everything else. A
// standing policy that failed every run would train the exit code to be
// ignored.
func TestIntegrateAllSkipsPROnlyRepo(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)
	setupWorkBranch(t, l, workDir, "beta", origin, 1)

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	o := &options{all: true, noFetch: true}
	if err := integrate(o, prCfg("beta"), l, st, workDir); err != nil {
		t.Fatalf("--all with a PR-only repo should succeed: %v", err)
	}
	if got := commitCount(t, l, "alpha"); got != 2 {
		t.Errorf("alpha commit count = %d; want 2 (integrated)", got)
	}
	if got := commitCount(t, l, "beta"); got != 1 {
		t.Errorf("beta commit count = %d; want 1 (skipped)", got)
	}
}

// TestIntegrateDryRunMirrorsPolicy: -n must predict the real run's exit code,
// the property the dirty-worktree guard already relies on.
func TestIntegrateDryRunMirrorsPolicy(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := integrate(&options{repo: "alpha", noFetch: true, dryRun: true}, prCfg("alpha"), l, st, workDir); err == nil {
		t.Error("dry run of a named PR-only repo should fail like the real run")
	}
	if err := integrate(&options{all: true, noFetch: true, dryRun: true}, prCfg("alpha"), l, st, workDir); err != nil {
		t.Errorf("dry run of --all over a PR-only repo should succeed: %v", err)
	}
}

// TestIntegrateRepoInheritsHouseRule: a repo with no entry of its own takes
// defaults.integration, which is the work-machine shape (restrictive house
// rule, per-repo exceptions).
func TestIntegrateRepoInheritsHouseRule(t *testing.T) {
	l, workDir := newLayout(t)
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", origin, 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	house := &config.Config{Defaults: config.Defaults{Integration: config.IntegrationPR}}
	if err := integrate(&options{repo: "alpha", noFetch: true}, house, l, st, workDir); err == nil {
		t.Fatal("a repo with no entry should inherit the PR house rule")
	}

	// The same repo opting back in to the local route overrides the house rule.
	house.Repos = map[string]config.RepoConfig{"alpha": {Integration: config.IntegrationLocal}}
	if err := integrate(&options{repo: "alpha", noFetch: true}, house, l, st, workDir); err != nil {
		t.Fatalf("a local override should beat the house rule: %v", err)
	}
	if got := commitCount(t, l, "alpha"); got != 2 {
		t.Errorf("main commit count = %d; want 2 (integrated)", got)
	}
}
