package rm

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
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
	o, err := parseFlags([]string{"alpha", "--delete-local", "--delete-remote", "--force", "--non-interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if o.repo != "alpha" || !o.deleteLocal || !o.deleteRemote || !o.force || !o.nonInteractive {
		t.Errorf("opts = %+v", o)
	}
}

// A missing <repo>, a leading-dash first argument, a stray positional and an
// unknown flag are all malformed invocations: they must report as usage errors
// (exit 2) rather than as work that failed.
func TestParseFlagsUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,                 // bare invocation
		{"--force"},         // a flag where <repo> belongs
		{"alpha", "stray"},  // stray second positional
		{"alpha", "--nope"}, // unknown flag
	} {
		_, err := parseFlags(args)
		if err == nil {
			t.Errorf("parseFlags(%v) = nil error; want error", args)
			continue
		}
		if !cli.IsUsage(err) {
			t.Errorf("parseFlags(%v) err = %v; want a usage error", args, err)
		}
	}
}

func TestParseFlagsNoFetch(t *testing.T) {
	o, err := parseFlags([]string{"alpha", "--no-fetch"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.noFetch {
		t.Errorf("opts = %+v", o)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRunHelp: -h/--help is answered before any argument checking, prints the
// usage block to stdout and exits 0 - even though a bare `rm` needs <repo>.
// "help" is deliberately NOT a help token here: it can name a repo.
func TestRunHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		var code int
		out := captureStdout(t, func() { code = Run([]string{flag}) })
		if code != cli.OK {
			t.Fatalf("Run(%s) = %d; want %d", flag, code, cli.OK)
		}
		for _, want := range []string{
			"usage: git work rm <repo>",
			"--delete-local", "--delete-remote", "--force",
			"--no-fetch", "--non-interactive", "-h, --help",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("help output = %q; want %q", out, want)
			}
		}
	}
}

// TestRunUsageExitCode: a malformed invocation exits 2, distinguishable from
// work that failed.
func TestRunUsageExitCode(t *testing.T) {
	if code := Run(nil); code != cli.Usage {
		t.Errorf("Run(nil) = %d; want %d", code, cli.Usage)
	}
}

// --- integration harness (mirrors internal/teardown/teardown_test.go and
// main_e2e_test.go's config/env setup, duplicated locally per-package) ---

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

// writeCommit writes content to fname in dir and commits it.
func writeCommit(t *testing.T, dir, fname, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, fname), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", fname)
	runGit(t, dir, "commit", "-qm", msg)
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

// setupWorkBranch clones origin as the primary for name, adds a worktree on
// branch under workDir, commits one change on it, and pushes the branch with
// an upstream. Returns the worktree path.
func setupWorkBranch(t *testing.T, l layout.Layout, workDir, name, branch, origin string) string {
	t.Helper()
	if err := repo.Clone(origin, l.RepoDir(name), nil); err != nil {
		t.Fatal(err)
	}
	main, err := l.RepoMain(name)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(workDir, name)
	if err := repo.AddWorktree(main, wt, branch, "", true); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "config", "user.email", "t@t.t")
	runGit(t, wt, "config", "user.name", "t")
	writeCommit(t, wt, "feat.txt", name+" feature\n", "feat")
	runGit(t, wt, "push", "-q", "-u", "origin", branch)
	return wt
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

// newTestLayout builds a layout backed by temp repos/work roots and points
// XDG_CONFIG_HOME at a matching config.yaml, so run()'s internal
// config.Load() resolves the same roots. t.Setenv scopes the env var to this
// test/subtest.
func newTestLayout(t *testing.T) layout.Layout {
	t.Helper()
	root := t.TempDir()
	reposRoot := filepath.Join(root, "repos")
	workRoot := filepath.Join(root, "work")
	cfgHome := filepath.Join(root, "config")
	cfgDir := filepath.Join(cfgHome, "git-work")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "paths:\n  repos: " + reposRoot + "\n  work: " + workRoot + "\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	return layout.Layout{ReposRoot: reposRoot, WorkRoot: workRoot}
}

func TestRunUnknownRepoErrors(t *testing.T) {
	l := newTestLayout(t)
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", "PROJ-1-work", origin)

	st := &state.State{TicketID: "PROJ-1", Title: "Proj 1", Slug: "proj-1", Branch: "PROJ-1-work",
		Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := st.Save(workDir); err != nil {
		t.Fatal(err)
	}

	t.Chdir(workDir)
	err := run([]string{"bogus", "--non-interactive"})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("run() err = %v, want an error naming the unknown repo", err)
	}
	// Naming a repo the folder does not hold is a malformed invocation.
	if !cli.IsUsage(err) {
		t.Errorf("run() err = %v; want a usage error", err)
	}
}

// TestRunRemovesContainedRepoAndUpdatesAggregates covers the happy path: rm
// on a squash-merged (contained) repo removes its worktree, deletes both
// branches, drops it from .git-work.yaml, leaves the other repo untouched,
// and re-renders README/CLAUDE without the removed repo.
func TestRunRemovesContainedRepoAndUpdatesAggregates(t *testing.T) {
	l := newTestLayout(t)
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	alphaOrigin := makeOrigin(t)
	betaOrigin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", "PROJ-1-work", alphaOrigin)
	betaWt := setupWorkBranch(t, l, workDir, "beta", "PROJ-1-work", betaOrigin)
	squashMergeOnOrigin(t, alphaOrigin, "PROJ-1-work")

	st := &state.State{TicketID: "PROJ-1", Title: "Proj 1", Slug: "proj-1", Branch: "PROJ-1-work",
		Repos: []state.Repo{
			{Name: "alpha", Branch: "PROJ-1-work"},
			{Name: "beta", Branch: "PROJ-1-work"},
		}}
	if err := st.Save(workDir); err != nil {
		t.Fatal(err)
	}

	t.Chdir(workDir)
	if err := run([]string{"alpha", "--non-interactive", "--delete-local", "--delete-remote"}); err != nil {
		t.Fatalf("run() = %v", err)
	}

	// alpha's worktree is gone; beta's is untouched.
	if _, err := os.Stat(filepath.Join(workDir, "alpha")); !os.IsNotExist(err) {
		t.Errorf("alpha worktree should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(betaWt); err != nil {
		t.Errorf("beta worktree should survive: %v", err)
	}

	// alpha's local+remote branches are gone.
	alphaMain, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := repo.LocalBranchExists(alphaMain, "PROJ-1-work"); err != nil || exists {
		t.Errorf("alpha local branch exists = %v, %v; want deleted", exists, err)
	}
	if exists, err := repo.RemoteBranchExists(alphaMain, "PROJ-1-work"); err != nil || exists {
		t.Errorf("alpha remote branch exists = %v, %v; want deleted", exists, err)
	}

	// beta's branches survive.
	betaMain, err := l.RepoMain("beta")
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := repo.LocalBranchExists(betaMain, "PROJ-1-work"); err != nil || !exists {
		t.Errorf("beta local branch exists = %v, %v; want present", exists, err)
	}

	// State on disk: only beta remains.
	reloaded, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repos) != 1 || reloaded.Repos[0].Name != "beta" {
		t.Errorf("reloaded repos = %v, want only beta", reloaded.Repos)
	}

	// README/CLAUDE re-rendered without alpha, still with beta.
	readme, err := os.ReadFile(filepath.Join(workDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readme), "alpha") {
		t.Errorf("README.md should no longer mention alpha:\n%s", readme)
	}
	if !strings.Contains(string(readme), "beta") {
		t.Errorf("README.md should still mention beta:\n%s", readme)
	}
	claude, err := os.ReadFile(filepath.Join(workDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(claude), "## alpha") {
		t.Errorf("CLAUDE.md should no longer have an alpha section:\n%s", claude)
	}
	if !strings.Contains(string(claude), "## beta (./beta)") {
		t.Errorf("CLAUDE.md should still have a beta section:\n%s", claude)
	}
}

// TestRunUnmergedAbortsWithoutForceProceedsWithForce covers the safety-check
// gate: a genuinely unmerged branch blocks rm unless --force is passed, and
// --force then proceeds and tears the repo down.
func TestRunUnmergedAbortsWithoutForceProceedsWithForce(t *testing.T) {
	l := newTestLayout(t)
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", "PROJ-1-work", origin)
	// No merge/squash: the branch's commit is nowhere on origin/main.

	st := &state.State{TicketID: "PROJ-1", Title: "Proj 1", Slug: "proj-1", Branch: "PROJ-1-work",
		Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := st.Save(workDir); err != nil {
		t.Fatal(err)
	}

	t.Chdir(workDir)
	err := run([]string{"alpha", "--non-interactive", "--delete-local"})
	if err == nil || !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("run() err = %v, want an abort naming the unmerged branch", err)
	}
	// Nothing torn down: alpha still in state and its worktree still present.
	reloaded, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repos) != 1 || reloaded.Repos[0].Name != "alpha" {
		t.Fatalf("reloaded repos = %v, want alpha still present after aborted rm", reloaded.Repos)
	}

	if err := run([]string{"alpha", "--non-interactive", "--delete-local", "--force"}); err != nil {
		t.Fatalf("run() with --force = %v", err)
	}
	reloaded, err = state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repos) != 0 {
		t.Errorf("reloaded repos = %v, want empty after --force rm", reloaded.Repos)
	}
	if _, err := os.Stat(filepath.Join(workDir, "alpha")); !os.IsNotExist(err) {
		t.Errorf("alpha worktree should be removed after --force rm, stat err = %v", err)
	}
}

// TestRunLastRepoLeavesAccurateZeroRepoFolder covers §7.2: rm of the last
// repo leaves an accurate zero-repo work folder rather than deleting it.
func TestRunLastRepoLeavesAccurateZeroRepoFolder(t *testing.T) {
	l := newTestLayout(t)
	workDir := filepath.Join(l.WorkRoot, "PROJ-1-work")
	origin := makeOrigin(t)
	setupWorkBranch(t, l, workDir, "alpha", "PROJ-1-work", origin)
	squashMergeOnOrigin(t, origin, "PROJ-1-work")

	st := &state.State{TicketID: "PROJ-1", Title: "Proj 1", Slug: "proj-1", Branch: "PROJ-1-work",
		Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	if err := st.Save(workDir); err != nil {
		t.Fatal(err)
	}

	t.Chdir(workDir)
	if err := run([]string{"alpha", "--non-interactive", "--delete-local", "--delete-remote"}); err != nil {
		t.Fatalf("run() = %v", err)
	}

	if _, err := os.Stat(workDir); err != nil {
		t.Fatalf("work folder should survive rm of its last repo: %v", err)
	}
	reloaded, err := state.Load(workDir)
	if err != nil {
		t.Fatalf("state should still load after rm of the last repo: %v", err)
	}
	if len(reloaded.Repos) != 0 {
		t.Errorf("reloaded repos = %v, want empty", reloaded.Repos)
	}
	if _, err := os.Stat(filepath.Join(workDir, "README.md")); err != nil {
		t.Errorf("README.md should still be regenerated: %v", err)
	}
}
