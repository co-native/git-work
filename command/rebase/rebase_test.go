package rebase

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	tests := []struct {
		name    string
		args    []string
		wantErr string // substring; "" means the parse must succeed
		want    options
	}{
		// Scope is always explicit - bare `rebase` no longer means --all.
		{name: "bare is a usage error", args: nil, wantErr: "give a <repo> or --all"},
		{name: "repo", args: []string{"alpha"}, want: options{repo: "alpha"}},
		{name: "all", args: []string{"--all"}, want: options{all: true}},
		{name: "all shorthand", args: []string{"-a"}, want: options{all: true}},
		{name: "repo and all", args: []string{"alpha", "-a"}, wantErr: "not both"},
		{name: "two repos", args: []string{"alpha", "beta"}, wantErr: "give at most one <repo>"},
		{name: "dry run", args: []string{"-a", "-n"}, want: options{all: true, dryRun: true}},
		{name: "dry run long", args: []string{"-a", "--dry-run"}, want: options{all: true, dryRun: true}},
		{name: "no pull", args: []string{"-a", "--no-pull"}, want: options{all: true, noPull: true}},
		// The positional may sit anywhere on the line, flags being boolean.
		{name: "flag before repo", args: []string{"-n", "alpha"}, want: options{repo: "alpha", dryRun: true}},
		{name: "flag after repo", args: []string{"alpha", "--no-pull"}, want: options{repo: "alpha", noPull: true}},
		{name: "unknown flag", args: []string{"-a", "--nope"}, wantErr: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFlags(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseFlags(%q) error = %v; want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlags(%q) = %v; want success", tt.args, err)
			}
			if *got != tt.want {
				t.Errorf("parseFlags(%q) = %+v; want %+v", tt.args, *got, tt.want)
			}
		})
	}
}

// TestRunHelp: -h is intercepted before parsing and before the work-folder
// lookup, so help works anywhere and needs no <repo>/--all.
func TestRunHelp(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"-h"}) })
	if code != 0 {
		t.Fatalf("Run(-h) = %d; want 0", code)
	}
	for _, want := range []string{"usage: git work rebase", "--no-pull", "--dry-run", "-h, --help"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output = %q; want %q", out, want)
		}
	}
}

// TestRunMalformedInvocationExitsTwo pins the exit-code half of the contract:
// a bare `rebase` never reaches the work folder, so it is a usage error (2),
// not a failure (1).
func TestRunMalformedInvocationExitsTwo(t *testing.T) {
	if code := Run(nil); code != 2 {
		t.Errorf("Run(nil) = %d; want 2 (malformed invocation)", code)
	}
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

// advanceOrigin pushes a new commit (fname=content) to origin's main from a
// throwaway clone, so primaries need a pull and work branches need a rebase.
func advanceOrigin(t *testing.T, origin, fname, content string) {
	t.Helper()
	w := filepath.Join(t.TempDir(), "w")
	runGit(t, "", "clone", "-q", origin, w)
	runGit(t, w, "config", "user.email", "t@t.t")
	runGit(t, w, "config", "user.name", "t")
	writeCommit(t, w, fname, content, "advance")
	runGit(t, w, "push", "-q", "origin", "main")
}

// setupWorkRepo clones origin as the primary for name and adds a worktree on
// a new work branch under workDir. Returns the worktree path.
func setupWorkRepo(t *testing.T, l layout.Layout, workDir, name, origin string) string {
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
	return wt
}

func rebaseInProgress(t *testing.T, wt string) bool {
	t.Helper()
	return strings.Contains(runGit(t, wt, "status"), "rebase in progress")
}

func TestRebaseAllDirtySkipped(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkRepo(t, l, workDir, "alpha", origin)
	advanceOrigin(t, origin, "extra.txt", "x\n")

	// Dirty the worktree: an uncommitted change must skip the rebase.
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD"))

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	err := rebaseAll(&options{all: true}, &out, &errw, l, st, workDir)
	if err == nil || !strings.Contains(err.Error(), "alpha: uncommitted changes; rebase skipped") {
		t.Errorf("rebaseAll = %v; want an uncommitted-changes problem", err)
	}
	if got := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD moved on a dirty worktree: %s -> %s", head, got)
	}
	if data, _ := os.ReadFile(filepath.Join(wt, "f.txt")); string(data) != "dirty\n" {
		t.Errorf("dirty file content = %q; want untouched", data)
	}
	if rebaseInProgress(t, wt) {
		t.Error("dirty worktree has a rebase in progress; want none")
	}
	// The primary was still pulled before the dirty check reported.
	if !strings.Contains(out.String(), "Pulled alpha: fast-forwarded") {
		t.Errorf("stdout = %q; want the primary pulled", out.String())
	}
}

func TestRebaseAllConflictLeftInProgressAndOthersProceed(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")

	// alpha: origin's main and the work branch both edit f.txt -> conflict.
	alphaOrigin := makeOrigin(t)
	alphaWt := setupWorkRepo(t, l, workDir, "alpha", alphaOrigin)
	writeCommit(t, alphaWt, "f.txt", "branch change\n", "work")
	advanceOrigin(t, alphaOrigin, "f.txt", "main change\n")

	// beta: origin's main advances on a different file -> clean rebase.
	betaOrigin := makeOrigin(t)
	betaWt := setupWorkRepo(t, l, workDir, "beta", betaOrigin)
	writeCommit(t, betaWt, "g.txt", "work\n", "work")
	advanceOrigin(t, betaOrigin, "extra.txt", "x\n")

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	err := rebaseAll(&options{all: true}, &out, &errw, l, st, workDir)
	if err == nil || !strings.Contains(err.Error(), "rebase finished with 1 problem(s)") {
		t.Fatalf("rebaseAll = %v; want exactly one problem", err)
	}
	if !strings.Contains(err.Error(), "alpha:") ||
		!strings.Contains(err.Error(), "git rebase --continue") ||
		!strings.Contains(err.Error(), "git rebase --abort") {
		t.Errorf("error = %v; want alpha's conflict with continue/abort guidance", err)
	}
	if !rebaseInProgress(t, alphaWt) {
		t.Error("alpha: conflicted rebase not left in progress")
	}
	// beta was still processed and rebased onto the advanced main.
	if !strings.Contains(out.String(), "Rebased beta onto main") {
		t.Errorf("stdout = %q; want beta rebased", out.String())
	}
	if rebaseInProgress(t, betaWt) {
		t.Error("beta has a rebase in progress; want a completed rebase")
	}
	if out := runGit(t, betaWt, "log", "--oneline", "main..HEAD"); strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("beta not rebased onto main tip: main..HEAD = %q", out)
	}
	if _, err := os.Stat(filepath.Join(betaWt, "extra.txt")); err != nil {
		t.Errorf("beta worktree missing rebased-in file extra.txt: %v", err)
	}

	// The conflicted rebase resolves with normal git commands.
	runGit(t, alphaWt, "rebase", "--abort")
	if rebaseInProgress(t, alphaWt) {
		t.Error("alpha: rebase --abort did not clear the in-progress rebase")
	}
}

func TestRebaseAllPrimaryOnNonDefaultBranch(t *testing.T) {
	// A primary left on another branch (e.g. after a hand-managed clone was
	// adopted mid-branch): the pull must still fast-forward the DEFAULT
	// branch, so the rebase target is fresh - not whatever branch the
	// primary happens to have checked out.
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkRepo(t, l, workDir, "alpha", origin)
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "checkout", "-q", "-b", "hotfix")
	writeCommit(t, wt, "x.txt", "x\n", "feat work")
	advanceOrigin(t, origin, "extra.txt", "up\n")

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := rebaseAll(&options{all: true}, &out, &errw, l, st, workDir); err != nil {
		t.Fatalf("rebaseAll: %v (stderr %q)", err, errw.String())
	}
	if !strings.Contains(out.String(), "Pulled alpha: fast-forwarded") {
		t.Errorf("stdout = %q; want the default branch fast-forwarded", out.String())
	}
	// Local main advanced to the origin tip even though hotfix is checked out...
	originTip := strings.TrimSpace(runGit(t, wt, "rev-parse", "origin/main"))
	localMain := strings.TrimSpace(runGit(t, main, "rev-parse", "refs/heads/main"))
	if localMain != originTip {
		t.Errorf("local main = %s, want origin tip %s", localMain, originTip)
	}
	// ...the work branch was rebased onto it...
	runGit(t, wt, "merge-base", "--is-ancestor", originTip, "HEAD")
	// ...and the primary's checked-out branch was left alone.
	if head := strings.TrimSpace(runGit(t, main, "rev-parse", "--abbrev-ref", "HEAD")); head != "hotfix" {
		t.Errorf("primary HEAD = %q, want hotfix untouched", head)
	}
}

func TestRebaseAllMissingWorktreeSkippedWithNote(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{{Name: "gone", Branch: "PROJ-1-work"}}}
	if err := rebaseAll(&options{all: true}, &out, &errw, l, st, workDir); err != nil {
		t.Errorf("rebaseAll = %v; want nil (missing worktree is a skip, not a problem)", err)
	}
	if !strings.Contains(errw.String(), "gone: worktree missing; skipping") {
		t.Errorf("stderr = %q; want a skip note", errw.String())
	}
}

func TestRebaseScopedToOneRepo(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")

	alphaOrigin := makeOrigin(t)
	alphaWt := setupWorkRepo(t, l, workDir, "alpha", alphaOrigin)
	writeCommit(t, alphaWt, "a.txt", "work\n", "alpha work")
	advanceOrigin(t, alphaOrigin, "extra.txt", "x\n")

	betaOrigin := makeOrigin(t)
	betaWt := setupWorkRepo(t, l, workDir, "beta", betaOrigin)
	writeCommit(t, betaWt, "b.txt", "work\n", "beta work")
	advanceOrigin(t, betaOrigin, "extra.txt", "x\n")
	betaHead := strings.TrimSpace(runGit(t, betaWt, "rev-parse", "HEAD"))

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	if err := rebaseAll(&options{repo: "alpha"}, &out, &errw, l, st, workDir); err != nil {
		t.Fatalf("rebaseAll: %v (stderr %q)", err, errw.String())
	}
	if !strings.Contains(out.String(), "Rebased alpha onto main") {
		t.Errorf("stdout = %q; want alpha rebased", out.String())
	}
	// beta is out of scope: neither pulled nor rebased.
	if strings.Contains(out.String(), "beta") {
		t.Errorf("stdout = %q; want no mention of the out-of-scope repo", out.String())
	}
	if got := strings.TrimSpace(runGit(t, betaWt, "rev-parse", "HEAD")); got != betaHead {
		t.Errorf("beta HEAD moved: %s -> %s; want untouched", betaHead, got)
	}
}

func TestRebaseUnknownRepoIsAnError(t *testing.T) {
	var out, errw strings.Builder
	st := &state.State{TicketID: "PROJ-1", Slug: "work", Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	err := rebaseAll(&options{repo: "nope"}, &out, &errw, l(t), st, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), `repo "nope" is not part of`) {
		t.Errorf("rebaseAll = %v; want an unknown-repo error", err)
	}
}

// l builds a throwaway layout for tests that never reach the filesystem.
func l(t *testing.T) layout.Layout {
	t.Helper()
	root := t.TempDir()
	return layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
}

func TestRebaseNoPullSkipsThePull(t *testing.T) {
	root := t.TempDir()
	lay := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(lay.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkRepo(t, lay, workDir, "alpha", origin)
	writeCommit(t, wt, "x.txt", "x\n", "work")
	advanceOrigin(t, origin, "extra.txt", "up\n")

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := rebaseAll(&options{all: true, noPull: true}, &out, &errw, lay, st, workDir); err != nil {
		t.Fatalf("rebaseAll: %v (stderr %q)", err, errw.String())
	}
	if strings.Contains(out.String(), "Pulled") {
		t.Errorf("stdout = %q; want no pull with --no-pull", out.String())
	}
	// The rebase still ran, against the un-freshened local default branch, so
	// origin's new commit is absent from the worktree.
	if !strings.Contains(out.String(), "Rebased alpha onto main") {
		t.Errorf("stdout = %q; want the rebase to still run", out.String())
	}
	if _, err := os.Stat(filepath.Join(wt, "extra.txt")); !os.IsNotExist(err) {
		t.Errorf("worktree has extra.txt; --no-pull must not freshen the rebase target")
	}
}

func TestRebaseDryRunReportsWithoutRebasing(t *testing.T) {
	root := t.TempDir()
	lay := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(lay.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkRepo(t, lay, workDir, "alpha", origin)
	writeCommit(t, wt, "x.txt", "x\n", "first work commit")
	writeCommit(t, wt, "y.txt", "y\n", "second work commit")
	advanceOrigin(t, origin, "extra.txt", "up\n")
	head := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD"))

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := rebaseAll(&options{all: true, dryRun: true}, &out, &errw, lay, st, workDir); err != nil {
		t.Fatalf("rebaseAll: %v (stderr %q)", err, errw.String())
	}
	if !strings.Contains(out.String(), "alpha: would rebase 2 commit(s) from PROJ-1-work onto main") {
		t.Errorf("stdout = %q; want the would-rebase summary", out.String())
	}
	for _, subject := range []string{"first work commit", "second work commit"} {
		if !strings.Contains(out.String(), subject) {
			t.Errorf("stdout = %q; want commit subject %q listed", out.String(), subject)
		}
	}
	// The pull still ran (integrate's model: -n performs the identical pre-flight)...
	if !strings.Contains(out.String(), "Pulled alpha: fast-forwarded") {
		t.Errorf("stdout = %q; want the primary still pulled", out.String())
	}
	// ...but nothing was rebased.
	if got := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD moved during a dry run: %s -> %s", head, got)
	}
	if rebaseInProgress(t, wt) {
		t.Error("dry run left a rebase in progress")
	}
}

func TestRebaseDryRunDirtyIsAProblem(t *testing.T) {
	// A dirty worktree fails the real run, so -n must predict that nonzero
	// exit rather than reporting a rebase that would in fact be skipped.
	root := t.TempDir()
	lay := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(lay.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkRepo(t, lay, workDir, "alpha", origin)
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	err := rebaseAll(&options{all: true, dryRun: true}, &out, &errw, lay, st, workDir)
	if err == nil || !strings.Contains(err.Error(), "alpha: uncommitted changes; rebase would be skipped") {
		t.Errorf("rebaseAll = %v; want an uncommitted-changes problem", err)
	}
}

func TestRebaseDryRunNothingToReplay(t *testing.T) {
	root := t.TempDir()
	lay := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(lay.WorkRoot, "PROJ-1-work")

	// alpha: no commits of its own, and origin moved -> a fast-forward.
	alphaOrigin := makeOrigin(t)
	setupWorkRepo(t, lay, workDir, "alpha", alphaOrigin)
	advanceOrigin(t, alphaOrigin, "extra.txt", "up\n")

	// beta: no commits of its own and origin unchanged -> already current.
	betaOrigin := makeOrigin(t)
	setupWorkRepo(t, lay, workDir, "beta", betaOrigin)

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	if err := rebaseAll(&options{all: true, dryRun: true}, &out, &errw, lay, st, workDir); err != nil {
		t.Fatalf("rebaseAll: %v (stderr %q)", err, errw.String())
	}
	if !strings.Contains(out.String(), "alpha: no commits to replay; PROJ-1-work would fast-forward to main") {
		t.Errorf("stdout = %q; want alpha reported as a fast-forward", out.String())
	}
	if !strings.Contains(out.String(), "beta: PROJ-1-work is already up to date with main") {
		t.Errorf("stdout = %q; want beta reported as up to date", out.String())
	}
}

func TestRebaseRefusesWhenWorktreeIsOnAnotherBranch(t *testing.T) {
	root := t.TempDir()
	lay := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(lay.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkRepo(t, lay, workDir, "alpha", origin)
	advanceOrigin(t, origin, "extra.txt", "x\n")
	runGit(t, wt, "checkout", "-q", "-b", "side-quest")
	head := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD"))

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	err := rebaseAll(&options{all: true}, &out, &errw, lay, st, workDir)
	if err == nil {
		t.Fatal("rebaseAll succeeded; want a branch-drift problem")
	}
	for _, want := range []string{"PROJ-1-work", "side-quest", "git work adopt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v; want it to mention %q", err, want)
		}
	}
	if got := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD moved despite the refusal: %s -> %s", head, got)
	}
	// Refused before the pull, so no network round trip was spent.
	if strings.Contains(out.String(), "Pulled") {
		t.Errorf("stdout = %q; want the refusal to precede the pull", out.String())
	}
}

func TestRebaseNamesAnInProgressRebaseRatherThanDrift(t *testing.T) {
	// `git work rebase` leaves conflicted rebases in progress by design, and a
	// replaying rebase detaches HEAD - so the very next run must explain that
	// rather than report unexplained branch drift.
	root := t.TempDir()
	lay := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	workDir := filepath.Join(lay.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	wt := setupWorkRepo(t, lay, workDir, "alpha", origin)
	writeCommit(t, wt, "f.txt", "branch change\n", "work")
	advanceOrigin(t, origin, "f.txt", "main change\n")

	var out, errw strings.Builder
	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := rebaseAll(&options{all: true}, &out, &errw, lay, st, workDir); err == nil {
		t.Fatal("first rebase succeeded; wanted a conflict")
	}
	if !rebaseInProgress(t, wt) {
		t.Fatal("conflicted rebase not left in progress")
	}

	// Second run: the worktree is mid-rebase with a detached HEAD.
	out.Reset()
	errw.Reset()
	err := rebaseAll(&options{all: true}, &out, &errw, lay, st, workDir)
	if err == nil {
		t.Fatal("second rebase succeeded; want the in-progress rebase reported")
	}
	if !strings.Contains(err.Error(), "rebase is in progress") {
		t.Errorf("error = %v; want the in-progress rebase named", err)
	}
	if !strings.Contains(err.Error(), "--abort") {
		t.Errorf("error = %v; want the way out named", err)
	}
	if strings.Contains(err.Error(), "state records branch") {
		t.Errorf("error = %v; an in-progress rebase must not report as drift", err)
	}
}
