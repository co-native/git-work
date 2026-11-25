package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

// buildBinary compiles git-work into a temp path.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "git-work")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func sh(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// shOut runs a command and returns its combined output, failing the test on error.
func shOut(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func TestCloneNewDone(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	reposRoot := filepath.Join(home, "dev", "repos")
	workRoot := filepath.Join(home, "dev", "work")

	// config
	cfgDir := filepath.Join(home, ".config", "git-work")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.yaml"),
		[]byte("paths:\n  repos: "+reposRoot+"\n  work: "+workRoot+"\n"), 0o644)

	// bare origin with a commit
	origin := filepath.Join(home, "origin.git")
	sh(t, "", "git", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(home, "seed")
	sh(t, "", "git", "clone", "-q", origin, seed)
	sh(t, seed, "git", "config", "user.email", "t@t.t")
	sh(t, seed, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(seed, "f.txt"), []byte("hi\n"), 0o644)
	sh(t, seed, "git", "add", "f.txt")
	sh(t, seed, "git", "commit", "-qm", "init")
	sh(t, seed, "git", "branch", "-M", "main")
	sh(t, seed, "git", "push", "-q", "origin", "main")

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"))

	gw := func(args ...string) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git-work %v: %v\n%s", args, err, out)
		}
	}

	gw("clone", origin, "myrepo")
	if _, err := os.Stat(filepath.Join(reposRoot, "myrepo", "main", ".git")); err != nil {
		t.Fatalf("clone layout missing: %v", err)
	}
	gw("new", "PROJ-1", "--repos", "myrepo", "--branch", "PROJ-1-foo", "--non-interactive")
	wt := filepath.Join(workRoot, "PROJ-1-proj-1", "myrepo")
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}
	gw("done", "PROJ-1-proj-1", "--non-interactive", "--force")
	if _, err := os.Stat(filepath.Join(workRoot, "PROJ-1-proj-1")); !os.IsNotExist(err) {
		t.Fatalf("work folder not removed")
	}
}

func TestChangesCommand(t *testing.T) {
	bin, home, _, workRoot, env := newTestEnv(t)
	origin := makeOriginIn(t, home, "alpha")
	if out, err := gwRun(t, bin, home, env, "clone", origin, "alpha"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if out, err := gwRun(t, bin, home, env,
		"new", "PROJ-1", "--repos", "alpha", "--branch", "PROJ-1-work", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}

	workDir := filepath.Join(workRoot, "PROJ-1-proj-1")
	checkout := filepath.Join(workDir, "alpha")
	sh(t, checkout, "git", "config", "user.email", "t@t.t")
	sh(t, checkout, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(checkout, "feature.txt"), []byte("feature\n"), 0o644)
	sh(t, checkout, "git", "add", "feature.txt")
	sh(t, checkout, "git", "commit", "-qm", "feature work")

	out, err := gwRun(t, bin, workDir, env, "changes", "alpha")
	if err != nil {
		t.Fatalf("changes alpha: %v\n%s", err, out)
	}
	for _, want := range []string{
		"alpha: 1 un-integrated commit on PROJ-1-work not in main",
		"feature work",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("changes output = %q; want %q", out, want)
		}
	}

	out, err = gwRun(t, bin, workDir, env, "changes", "--all")
	if err != nil || !strings.Contains(out, "feature work") {
		t.Errorf("changes --all = %v; output:\n%s", err, out)
	}
}

func TestChangesCommandCloneMode(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	alphaOrigin := makeOriginIn(t, home, "alpha")
	betaOrigin := makeOriginIn(t, home, "beta")
	writeCloneModeConfig(t, home, reposRoot, workRoot,
		map[string]string{"alpha": alphaOrigin, "beta": betaOrigin})
	if out, err := gwRun(t, bin, home, env,
		"new", "PROJ-1", "--repos", "alpha,beta", "--branch", "PROJ-1-work", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}

	checkout := filepath.Join(workRoot, "PROJ-1-proj-1", "alpha")
	sh(t, checkout, "git", "config", "user.email", "t@t.t")
	sh(t, checkout, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(checkout, "feature.txt"), []byte("feature\n"), 0o644)
	sh(t, checkout, "git", "add", "feature.txt")
	sh(t, checkout, "git", "commit", "-qm", "feature work")

	out, err := gwRun(t, bin, checkout, env, "changes", "alpha")
	if err != nil {
		t.Fatalf("changes in clone mode: %v\n%s", err, out)
	}
	if !strings.Contains(out, "alpha: 1 un-integrated commit on PROJ-1-work not in main") {
		t.Errorf("changes output = %q", out)
	}

	out, err = gwRun(t, bin, checkout, env, "changes", "--all")
	if err != nil {
		t.Fatalf("changes --all in clone mode: %v\n%s", err, out)
	}
	for _, want := range []string{
		"alpha: 1 un-integrated commit on PROJ-1-work not in main",
		"beta: no un-integrated commits on PROJ-1-work not in main",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("changes --all output = %q; want %q", out, want)
		}
	}
}

// newTestEnv builds the binary and a config pointing at temp repos/work roots.
func newTestEnv(t *testing.T) (bin, home, reposRoot, workRoot string, env []string) {
	t.Helper()
	bin = buildBinary(t)
	home = t.TempDir()
	reposRoot = filepath.Join(home, "dev", "repos")
	workRoot = filepath.Join(home, "dev", "work")
	cfgDir := filepath.Join(home, ".config", "git-work")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.yaml"),
		[]byte("paths:\n  repos: "+reposRoot+"\n  work: "+workRoot+"\n"), 0o644)
	env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	return bin, home, reposRoot, workRoot, env
}

// makeOriginIn creates a bare origin with one commit on main, under home.
func makeOriginIn(t *testing.T, home, name string) string {
	t.Helper()
	origin := filepath.Join(home, name+".git")
	sh(t, "", "git", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(home, "seed-"+name)
	sh(t, "", "git", "clone", "-q", origin, seed)
	sh(t, seed, "git", "config", "user.email", "t@t.t")
	sh(t, seed, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(seed, "f.txt"), []byte("hi\n"), 0o644)
	sh(t, seed, "git", "add", "f.txt")
	sh(t, seed, "git", "commit", "-qm", "init")
	sh(t, seed, "git", "branch", "-M", "main")
	sh(t, seed, "git", "push", "-q", "origin", "main")
	return origin
}

// gwRun runs the built binary, returning combined output and error.
func gwRun(t *testing.T, bin, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Non-interactive new never adopts a ticket-matching branch on its own: one
// candidate or several, local or on origin, it refuses with the full list
// across every repo and creates nothing. --branch is the scripted answer,
// reusing the branch where it exists and creating it where it does not.
func TestNewNonInteractiveAmbiguousBranches(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	for _, name := range []string{"alpha", "beta"} {
		origin := makeOriginIn(t, home, name)
		if out, err := gwRun(t, bin, home, env, "clone", origin, name); err != nil {
			t.Fatalf("clone %s: %v\n%s", name, err, out)
		}
	}
	// alpha: one local candidate plus one that exists only on origin
	sh(t, filepath.Join(reposRoot, "alpha", "main"), "git", "branch", "PROJ-9-old")
	sh(t, filepath.Join(home, "seed-alpha"), "git", "push", "-q", "origin", "HEAD:refs/heads/feature/PROJ-9")
	// beta: a single local candidate
	sh(t, filepath.Join(reposRoot, "beta", "main"), "git", "branch", "PROJ-9-beta")

	out, err := gwRun(t, bin, home, env, "new", "PROJ-9", "--repos", "alpha,beta", "--non-interactive")
	if err == nil {
		t.Fatalf("expected new to refuse with matching branches, got:\n%s", out)
	}
	for _, want := range []string{"PROJ-9-old", "origin/feature/PROJ-9", "PROJ-9-beta", "--branch"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal should mention %q, got:\n%s", want, out)
		}
	}
	// Nothing was created: the refusal came before the folder existed.
	entries, err := os.ReadDir(workRoot)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read work root: %v", err)
	}
	for _, e := range entries {
		t.Errorf("expected work root to be empty after the refusal, found %s", e.Name())
	}

	// --branch answers for both repos: alpha reuses origin's branch as a
	// tracking branch, beta creates it.
	if out, err := gwRun(t, bin, home, env, "new", "PROJ-9", "--repos", "alpha,beta", "--branch", "feature/PROJ-9", "--non-interactive"); err != nil {
		t.Fatalf("new --branch: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-9-proj-9")
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"alpha": "existing", "beta": "new"}
	for _, r := range st.Repos {
		if r.Branch != "feature/PROJ-9" || r.BranchSource != want[r.Name] {
			t.Errorf("state %s = %+v, want feature/PROJ-9 as %s", r.Name, r, want[r.Name])
		}
	}
	up := shOut(t, filepath.Join(workDir, "alpha"), "git", "rev-parse", "--abbrev-ref", "@{upstream}")
	if strings.TrimSpace(up) != "origin/feature/PROJ-9" {
		t.Errorf("alpha upstream = %q, want origin/feature/PROJ-9", strings.TrimSpace(up))
	}
}

// A branch that already exists on origin (pushed from another machine, or a
// folder torn down after pushing) is checked out from there, not shadowed by
// a new branch of the same name cut from main.
func TestNewChecksOutRemoteOnlyBranch(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	origin := makeOriginIn(t, home, "alpha")
	if out, err := gwRun(t, bin, home, env, "clone", origin, "alpha"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	// Push feat-x, one commit ahead of main, from the seed clone only.
	seed := filepath.Join(home, "seed-alpha")
	sh(t, seed, "git", "checkout", "-qb", "feat-x")
	sh(t, seed, "git", "commit", "-q", "--allow-empty", "-m", "feat-x work")
	sh(t, seed, "git", "push", "-q", "origin", "feat-x")
	want := strings.TrimSpace(shOut(t, seed, "git", "rev-parse", "HEAD"))
	main := filepath.Join(reposRoot, "alpha", "main")
	if out, err := exec.Command("git", "-C", main, "show-ref", "--verify", "-q", "refs/heads/feat-x").CombinedOutput(); err == nil {
		t.Fatalf("fixture: feat-x must not exist locally in the primary\n%s", out)
	}

	if out, err := gwRun(t, bin, home, env, "new", "-n", "feat-x", "--repos", "alpha", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "feat-x")
	wt := filepath.Join(workDir, "alpha")
	if got := strings.TrimSpace(shOut(t, wt, "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "feat-x" {
		t.Errorf("worktree branch = %q, want feat-x", got)
	}
	if got := strings.TrimSpace(shOut(t, wt, "git", "rev-parse", "HEAD")); got != want {
		t.Errorf("worktree tip = %s, want origin/feat-x's %s (a fresh branch from main)", got, want)
	}
	if got := strings.TrimSpace(shOut(t, wt, "git", "rev-parse", "--abbrev-ref", "@{upstream}")); got != "origin/feat-x" {
		t.Errorf("upstream = %q, want origin/feat-x", got)
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Repos) != 1 || st.Repos[0].Branch != "feat-x" || st.Repos[0].BranchSource != "existing" {
		t.Errorf("state repos = %+v, want feat-x recorded as existing", st.Repos)
	}
}

func TestRefreshAndAdd(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	ob := makeOriginIn(t, home, "beta")

	gw := func(dir string, args ...string) {
		if out, err := gwRun(t, bin, dir, env, args...); err != nil {
			t.Fatalf("git-work %v: %v\n%s", args, err, out)
		}
	}

	gw(home, "clone", oa, "alpha")
	gw(home, "clone", ob, "beta")

	// per-repo overlay for alpha, under the old name: it must still be found
	// when the generated file is AGENTS.md
	os.WriteFile(filepath.Join(reposRoot, "alpha", "CLAUDE.md"), []byte("alpha overlay\n"), 0o644)

	gw(home, "new", "PROJ-2", "--repos", "alpha", "--branch", "PROJ-2-foo", "--non-interactive")
	workDir := filepath.Join(workRoot, "PROJ-2-proj-2")

	// refresh from inside a worktree
	gw(filepath.Join(workDir, "alpha"), "refresh")
	data, _ := os.ReadFile(filepath.Join(workDir, "AGENTS.md"))
	if !strings.Contains(string(data), "alpha overlay") {
		t.Errorf("AGENTS.md missing overlay after refresh:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(workDir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("AGENTS.md is the default instructions file; no CLAUDE.md should be generated")
	}

	// Switching instructions_file renames the generated file on the next
	// re-render, and hand content outside the markers moves with it.
	f, _ := os.OpenFile(filepath.Join(workDir, "AGENTS.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("\n## My notes\nkeep me\n")
	f.Close()
	gw(home, "config", "set", "instructions_file", "CLAUDE.md")
	gw(workDir, "refresh", "--no-pull")
	if _, err := os.Stat(filepath.Join(workDir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Error("AGENTS.md should be renamed away once instructions_file is CLAUDE.md")
	}
	data, _ = os.ReadFile(filepath.Join(workDir, "CLAUDE.md"))
	for _, want := range []string{"alpha overlay", "keep me"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("CLAUDE.md missing %q after the switch:\n%s", want, data)
		}
	}
	gw(home, "config", "unset", "instructions_file")
	gw(workDir, "refresh", "--no-pull")
	if _, err := os.Stat(filepath.Join(workDir, "AGENTS.md")); err != nil {
		t.Error("unsetting instructions_file should switch back to AGENTS.md on refresh")
	}

	// add beta
	gw(workDir, "add", "--repos", "beta", "--non-interactive")
	if _, err := os.Stat(filepath.Join(workDir, "beta")); err != nil {
		t.Errorf("beta worktree missing after add: %v", err)
	}

	// The VS Code workspace file is named after the work folder and follows the
	// repo set.
	wsPath := filepath.Join(workDir, "PROJ-2-proj-2.code-workspace")
	ws, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("workspace file missing after add: %v", err)
	}
	for _, want := range []string{`"path": "."`, `"path": "alpha"`, `"path": "beta"`} {
		if !strings.Contains(string(ws), want) {
			t.Errorf("workspace file missing %s after add:\n%s", want, ws)
		}
	}

	// Only the folders key is git-work's, so a refresh rewrites it and keeps
	// everything the operator put in the file.
	if err := os.WriteFile(wsPath, []byte("{\n  \"folders\": [],\n  \"settings\": { \"editor.tabSize\": 4 }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gw(workDir, "refresh", "--no-pull")
	ws, err = os.ReadFile(wsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"editor.tabSize": 4`, `"path": "beta"`} {
		if !strings.Contains(string(ws), want) {
			t.Errorf("refresh should rewrite folders and keep %s:\n%s", want, ws)
		}
	}
}

func TestAddReusesExistingBranch(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	for _, name := range []string{"alpha", "beta"} {
		origin := makeOriginIn(t, home, name)
		if out, err := gwRun(t, bin, home, env, "clone", origin, name); err != nil {
			t.Fatalf("clone %s: %v\n%s", name, err, out)
		}
	}
	// beta already has the work branch (e.g. from earlier work on the ticket)
	sh(t, filepath.Join(reposRoot, "beta", "main"), "git", "branch", "PROJ-3-foo")

	if out, err := gwRun(t, bin, home, env,
		"new", "PROJ-3", "--repos", "alpha", "--branch", "PROJ-3-foo", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-3-proj-3")

	if out, err := gwRun(t, bin, workDir, env, "add", "--repos", "beta", "--non-interactive"); err != nil {
		t.Fatalf("add should reuse the existing branch instead of failing on -b: %v\n%s", err, out)
	}
	head := shOut(t, filepath.Join(workDir, "beta"), "git", "rev-parse", "--abbrev-ref", "HEAD")
	if strings.TrimSpace(head) != "PROJ-3-foo" {
		t.Errorf("beta worktree branch = %q, want PROJ-3-foo", strings.TrimSpace(head))
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range st.Repos {
		if r.Branch != "PROJ-3-foo" {
			t.Errorf("state %s: Branch = %q, want PROJ-3-foo", r.Name, r.Branch)
		}
	}
}

// add refuses a ticket-matching branch non-interactively, creating no
// worktree and leaving state untouched; --branch then reuses it.
func TestAddReusesTicketMatchedBranch(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	for _, name := range []string{"alpha", "beta"} {
		origin := makeOriginIn(t, home, name)
		if out, err := gwRun(t, bin, home, env, "clone", origin, name); err != nil {
			t.Fatalf("clone %s: %v\n%s", name, err, out)
		}
	}
	// beta has a branch matching the ticket, but NOT named like the work branch
	sh(t, filepath.Join(reposRoot, "beta", "main"), "git", "branch", "feature/PROJ-4")

	if out, err := gwRun(t, bin, home, env,
		"new", "PROJ-4", "--repos", "alpha", "--branch", "PROJ-4-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-4-proj-4")

	out, err := gwRun(t, bin, workDir, env, "add", "--repos", "beta", "--non-interactive")
	if err == nil || !strings.Contains(out, "feature/PROJ-4") || !strings.Contains(out, "--branch") {
		t.Fatalf("add should refuse, naming feature/PROJ-4 and --branch; err = %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(workDir, "beta")); !os.IsNotExist(err) {
		t.Errorf("refused add must not create the beta worktree (stat err = %v)", err)
	}
	if st, err := state.Load(workDir); err != nil || len(st.Repos) != 1 {
		t.Errorf("refused add must leave state untouched: repos = %+v, err = %v", st.Repos, err)
	}

	if out, err := gwRun(t, bin, workDir, env, "add", "--repos", "beta", "--branch", "feature/PROJ-4", "--non-interactive"); err != nil {
		t.Fatalf("add --branch: %v\n%s", err, out)
	}
	head := shOut(t, filepath.Join(workDir, "beta"), "git", "rev-parse", "--abbrev-ref", "HEAD")
	if strings.TrimSpace(head) != "feature/PROJ-4" {
		t.Errorf("beta worktree branch = %q, want feature/PROJ-4 (ticket-match reuse)", strings.TrimSpace(head))
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	var beta *state.Repo
	for i := range st.Repos {
		if st.Repos[i].Name == "beta" {
			beta = &st.Repos[i]
		}
	}
	if beta == nil {
		t.Fatal("beta missing from state")
	}
	if beta.Branch != "feature/PROJ-4" || beta.BranchSource != "existing" {
		t.Errorf("beta state = %+v, want Branch feature/PROJ-4, BranchSource existing", *beta)
	}
}

func TestAddLocalOnlyRepoWarnsOnFetchFailure(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	origin := makeOriginIn(t, home, "alpha")
	if out, err := gwRun(t, bin, home, env, "clone", origin, "alpha"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}

	// A local-only repo in the expected layout, with a phantom origin
	// (URL-less remote.origin.* config) so `git fetch --all` fails -
	// the same shape as a repo that was never pushed anywhere.
	local := filepath.Join(reposRoot, "gamma", "main")
	os.MkdirAll(local, 0o755)
	os.WriteFile(filepath.Join(reposRoot, "gamma", ".git"), []byte("gitdir: ./main/.git\n"), 0o644)
	sh(t, "", "git", "init", "-q", "-b", "main", local)
	sh(t, local, "git", "config", "user.email", "t@t.t")
	sh(t, local, "git", "config", "user.name", "t")
	sh(t, local, "git", "config", "remote.origin.prune", "true")
	os.WriteFile(filepath.Join(local, "g.txt"), []byte("hi\n"), 0o644)
	sh(t, local, "git", "add", "g.txt")
	sh(t, local, "git", "commit", "-qm", "init")

	if out, err := gwRun(t, bin, home, env,
		"new", "PROJ-7", "--repos", "alpha", "--branch", "PROJ-7-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-7-proj-7")

	out, err := gwRun(t, bin, workDir, env, "add", "--repos", "gamma", "--non-interactive")
	if err != nil {
		t.Fatalf("add should warn, not fail, when fetch fails: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fetch gamma") || !strings.Contains(out, "continuing with local refs") {
		t.Errorf("expected a fetch warning, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(workDir, "gamma")); err != nil {
		t.Errorf("gamma worktree missing after add: %v", err)
	}
}

func TestReposCloneAliasAndList(t *testing.T) {
	bin, home, reposRoot, _, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	ob := makeOriginIn(t, home, "beta")

	// The group form and the top-level alias must produce the same layout.
	if out, err := gwRun(t, bin, home, env, "repos", "clone", oa, "alpha"); err != nil {
		t.Fatalf("repos clone: %v\n%s", err, out)
	}
	if out, err := gwRun(t, bin, home, env, "clone", ob, "beta"); err != nil {
		t.Fatalf("clone alias: %v\n%s", err, out)
	}
	for _, name := range []string{"alpha", "beta"} {
		ptr, err := os.ReadFile(filepath.Join(reposRoot, name, ".git"))
		if err != nil || string(ptr) != "gitdir: ./main/.git\n" {
			t.Errorf("%s pointer = %q err=%v", name, ptr, err)
		}
	}

	out, err := gwRun(t, bin, home, env, "repos", "list")
	if err != nil {
		t.Fatalf("repos list: %v\n%s", err, out)
	}
	for _, want := range []string{"alpha", "beta", "main"} {
		if !strings.Contains(out, want) {
			t.Errorf("repos list missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "drift") {
		t.Errorf("fresh clones should have no drift, got:\n%s", out)
	}

	if out, err := gwRun(t, bin, home, env, "repos", "bogus"); err == nil {
		t.Errorf("expected nonzero exit for unknown repos subcommand, got:\n%s", out)
	}
}

// TestTicketlessAdoptRefreshRebaseSquashDone drives the PROJ-3 surface as one
// end-to-end flow on temp repos: ticketless new, work-folder adopt of a
// hand-added worktree, refresh (pulling the primaries), rebase onto the moved
// default branch, an upstream squash merge, and done succeeding without
// --force via squash-merge detection.
func TestTicketlessAdoptRefreshRebaseSquashDone(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	ob := makeOriginIn(t, home, "beta")
	for name, origin := range map[string]string{"alpha": oa, "beta": ob} {
		if out, err := gwRun(t, bin, home, env, "clone", origin, name); err != nil {
			t.Fatalf("clone %s: %v\n%s", name, err, out)
		}
	}

	// Ticketless new: folder and branch derive from the name, no provider
	// lookup, no TICKET.md.
	if out, err := gwRun(t, bin, home, env,
		"new", "--no-ticket", "My Feature", "--repos", "alpha", "--non-interactive"); err != nil {
		t.Fatalf("new --no-ticket: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "my-feature")
	wtAlpha := filepath.Join(workDir, "alpha")
	if _, err := os.Stat(wtAlpha); err != nil {
		t.Fatalf("alpha worktree missing after ticketless new: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "TICKET.md")); !os.IsNotExist(err) {
		t.Errorf("ticketless new must not write TICKET.md")
	}
	head := strings.TrimSpace(shOut(t, wtAlpha, "git", "rev-parse", "--abbrev-ref", "HEAD"))
	if head != "my-feature" {
		t.Errorf("alpha worktree branch = %q, want my-feature", head)
	}

	// Adopt: a beta worktree added by hand (outside git-work) is registered
	// into the folder's state; nothing moves, nothing is rewritten in git.
	betaMain := filepath.Join(reposRoot, "beta", "main")
	sh(t, betaMain, "git", "worktree", "add", "-b", "side-quest", filepath.Join(workDir, "beta"))
	out, err := gwRun(t, bin, workDir, env, "adopt", "beta")
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Registered beta (branch side-quest)") {
		t.Errorf("adopt output missing registration note, got:\n%s", out)
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	var beta *state.Repo
	for i := range st.Repos {
		if st.Repos[i].Name == "beta" {
			beta = &st.Repos[i]
		}
	}
	if beta == nil {
		t.Fatal("beta missing from state after adopt")
	}
	if beta.Branch != "side-quest" || beta.BranchSource != "existing" {
		t.Errorf("beta state = %+v, want Branch side-quest, BranchSource existing", *beta)
	}

	// Upstream moves: push a commit to alpha's origin main via the seed clone.
	seed := filepath.Join(home, "seed-alpha")
	os.WriteFile(filepath.Join(seed, "up.txt"), []byte("upstream\n"), 0o644)
	sh(t, seed, "git", "add", "up.txt")
	sh(t, seed, "git", "commit", "-qm", "upstream change")
	sh(t, seed, "git", "push", "-q", "origin", "main")

	// Refresh: pulls the ticket primaries (ff-only) before re-rendering, and
	// the re-rendered README now lists the adopted beta repo.
	out, err = gwRun(t, bin, workDir, env, "refresh")
	if err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Pulled alpha: fast-forwarded") {
		t.Errorf("refresh should ff-pull alpha's primary, got:\n%s", out)
	}
	upstream := strings.TrimSpace(shOut(t, "", "git", "-C", oa, "rev-parse", "main"))
	alphaMain := filepath.Join(reposRoot, "alpha", "main")
	if got := strings.TrimSpace(shOut(t, alphaMain, "git", "rev-parse", "HEAD")); got != upstream {
		t.Errorf("alpha primary HEAD = %s after refresh, want origin main %s", got, upstream)
	}
	readme, _ := os.ReadFile(filepath.Join(workDir, "README.md"))
	if !strings.Contains(string(readme), "beta") {
		t.Errorf("README.md should list adopted repo beta after refresh:\n%s", readme)
	}
	// The re-rendered AGENTS.md must state each repo's own branch: beta was
	// adopted on side-quest, not the folder-level my-feature branch.
	agents, _ := os.ReadFile(filepath.Join(workDir, "AGENTS.md"))
	if !strings.Contains(string(agents), "## alpha (./alpha)\nBranch: my-feature\n") {
		t.Errorf("AGENTS.md should state alpha's branch:\n%s", agents)
	}
	if !strings.Contains(string(agents), "## beta (./beta)\nBranch: side-quest\n") {
		t.Errorf("AGENTS.md should state beta's own branch side-quest:\n%s", agents)
	}

	// Rebase: commit work on the branch (still based on the old main tip),
	// then rebase it onto the pulled default branch.
	sh(t, wtAlpha, "git", "config", "user.email", "t@t.t")
	sh(t, wtAlpha, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(wtAlpha, "feat.txt"), []byte("feature\n"), 0o644)
	sh(t, wtAlpha, "git", "add", "feat.txt")
	sh(t, wtAlpha, "git", "commit", "-qm", "feature work")
	// Scope is explicit, the same rule push and integrate follow: bare `rebase` is
	// a usage error, not an implicit --all.
	if out, err := gwRun(t, bin, workDir, env, "rebase"); err == nil {
		t.Errorf("bare rebase succeeded; want a usage error, got:\n%s", out)
	} else if !strings.Contains(out, "usage: git work rebase") {
		t.Errorf("bare rebase should print usage, got:\n%s", out)
	}
	out, err = gwRun(t, bin, workDir, env, "rebase", "-a")
	if err != nil {
		t.Fatalf("rebase: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Rebased alpha onto main") {
		t.Errorf("rebase output missing alpha, got:\n%s", out)
	}
	// The upstream commit must now be an ancestor of the work branch.
	sh(t, wtAlpha, "git", "merge-base", "--is-ancestor", upstream, "HEAD")

	// Publish both branches so done sees no unpushed work.
	sh(t, wtAlpha, "git", "push", "-q", "-u", "origin", "my-feature")
	sh(t, filepath.Join(workDir, "beta"), "git", "push", "-q", "-u", "origin", "side-quest")

	// Squash-merge the work branch into origin main: a new commit object, so
	// the ancestry check alone would read "not merged".
	sh(t, seed, "git", "fetch", "-q", "origin")
	sh(t, seed, "git", "merge", "--squash", "origin/my-feature")
	sh(t, seed, "git", "commit", "-qm", "My Feature (squashed)")
	sh(t, seed, "git", "push", "-q", "origin", "main")

	// done without --force: patch equivalence detects the squash merge.
	out, err = gwRun(t, bin, home, env, "done", "my-feature",
		"--non-interactive", "--delete-local", "--delete-remote")
	if err != nil {
		t.Fatalf("done should succeed without --force on a squash-merged branch: %v\n%s", err, out)
	}
	if !strings.Contains(out, "squash-merged") {
		t.Errorf("done output should label the branch squash-merged, got:\n%s", out)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("work folder not removed")
	}
	if got := strings.TrimSpace(shOut(t, alphaMain, "git", "branch", "--list", "my-feature")); got != "" {
		t.Errorf("local branch my-feature not deleted: %q", got)
	}
	if got := strings.TrimSpace(shOut(t, "", "git", "-C", oa, "branch", "--list", "my-feature")); got != "" {
		t.Errorf("remote branch my-feature not deleted: %q", got)
	}
	if got := strings.TrimSpace(shOut(t, "", "git", "-C", ob, "branch", "--list", "side-quest")); got != "" {
		t.Errorf("remote branch side-quest not deleted: %q", got)
	}
}

func TestDoneResumableAfterPartialTeardown(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	for _, name := range []string{"alpha", "beta"} {
		origin := makeOriginIn(t, home, name)
		if out, err := gwRun(t, bin, home, env, "clone", origin, name); err != nil {
			t.Fatalf("clone %s: %v\n%s", name, err, out)
		}
	}
	if out, err := gwRun(t, bin, home, env,
		"new", "PROJ-5", "--repos", "alpha,beta", "--branch", "PROJ-5-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-5-proj-5")

	// Simulate a partial teardown: alpha's worktree is already gone.
	sh(t, filepath.Join(reposRoot, "alpha", "main"),
		"git", "worktree", "remove", filepath.Join(workDir, "alpha"))

	out, err := gwRun(t, bin, home, env, "done", "PROJ-5-proj-5", "--non-interactive", "--force")
	if err != nil {
		t.Fatalf("done should succeed after a partial teardown: %v\n%s", err, out)
	}
	if !strings.Contains(out, "alpha: worktree already removed") {
		t.Errorf("expected a skip note for alpha, got:\n%s", out)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("work folder not removed")
	}
}

func TestDoneRemovalGuard(t *testing.T) {
	bin, home, _, workRoot, env := newTestEnv(t)
	origin := makeOriginIn(t, home, "alpha")
	if out, err := gwRun(t, bin, home, env, "clone", origin, "alpha"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if out, err := gwRun(t, bin, home, env,
		"new", "PROJ-6", "--repos", "alpha", "--branch", "PROJ-6-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-6-proj-6")
	os.WriteFile(filepath.Join(workDir, "notes.txt"), []byte("keep me\n"), 0o644)

	// Without --remove-all, a leftover file no longer dead-ends the run:
	// done tears the repo down, keeps the folder + honest state, and reports
	// the kept files (--force must NOT imply --remove-all).
	out, err := gwRun(t, bin, home, env, "done", "PROJ-6-proj-6", "--non-interactive", "--force")
	if err != nil {
		t.Fatalf("done should keep (not error on) a folder with leftovers, got:\n%s\nerr: %v", out, err)
	}
	if !strings.Contains(out, "notes.txt") {
		t.Errorf("kept-files listing should name notes.txt, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(workDir, "notes.txt")); err != nil {
		t.Fatalf("notes.txt should survive the Keep run: %v", err)
	}
	// State stays honest: the torn-down repo is gone but the folder + state
	// file remain (a zero-repo folder is still a first-class work folder).
	if _, err := os.Stat(filepath.Join(workDir, ".git-work.yaml")); err != nil {
		t.Fatalf("state file should survive the Keep run: %v", err)
	}

	// Re-run with --remove-all: teardown is already done (idempotent), and
	// the leftover file is deleted along with the folder.
	out, err = gwRun(t, bin, home, env, "done", "PROJ-6-proj-6", "--non-interactive", "--force", "--remove-all")
	if err != nil {
		t.Fatalf("done --remove-all: %v\n%s", err, out)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("work folder not removed with --remove-all")
	}
}

func TestCloneModeNewPushDone(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	ob := makeOriginIn(t, home, "beta")
	cfgPath := filepath.Join(home, ".config", "git-work", "config.yaml")
	cfg := "paths:\n  repos: " + reposRoot + "\n  work: " + workRoot + "\n" +
		"defaults:\n  mode: clone\n" +
		"repos:\n  alpha:\n    url: " + oa + "\n  beta:\n    url: " + ob + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := gwRun(t, bin, home, env, "new", "PROJ-1", "--repos", "alpha,beta", "--branch", "PROJ-1-x", "--non-interactive")
	if err != nil {
		t.Fatalf("new clone mode: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-1-proj-1")
	for _, name := range []string{"alpha", "beta"} {
		repoDir := filepath.Join(workDir, name)
		if fi, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil || !fi.IsDir() {
			t.Fatalf("%s is not a full clone (.git dir): %v", name, err)
		}
		if got := strings.TrimSpace(shOut(t, repoDir, "git", "branch", "--show-current")); got != "PROJ-1-x" {
			t.Errorf("%s branch = %q, want PROJ-1-x", name, got)
		}
		if got := strings.TrimSpace(shOut(t, repoDir, "git", "rev-parse", "--verify", "main")); got == "" {
			t.Errorf("%s has no local main", name)
		}
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != "clone" {
		t.Errorf("state mode = %q, want clone", st.Mode)
	}
	for _, r := range st.Repos {
		if r.DefaultBranch != "main" {
			t.Errorf("%s default branch = %q, want main", r.Name, r.DefaultBranch)
		}
	}
	if !strings.Contains(shOut(t, workDir, "cat", "README.md"), "PROJ-1") {
		t.Error("README.md missing the ticket")
	}
	if !strings.Contains(shOut(t, workDir, "cat", "AGENTS.md"), "one full git\nclone per participating repo") {
		t.Error("AGENTS.md does not carry the clone-mode contract")
	}

	// Commit and push from the clone.
	os.WriteFile(filepath.Join(workDir, "alpha", "work.txt"), []byte("x\n"), 0o644)
	sh(t, filepath.Join(workDir, "alpha"), "git", "config", "user.email", "t@t.t")
	sh(t, filepath.Join(workDir, "alpha"), "git", "config", "user.name", "t")
	sh(t, filepath.Join(workDir, "alpha"), "git", "add", "work.txt")
	sh(t, filepath.Join(workDir, "alpha"), "git", "commit", "-qm", "work")
	if out, err := gwRun(t, bin, workDir, env, "push", "--all"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}

	if out, err := gwRun(t, bin, home, env, "done", "PROJ-1-proj-1", "--non-interactive", "--force"); err != nil {
		t.Fatalf("done: %v\n%s", err, out)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatal("clone-mode work folder not removed")
	}
}

func writeCloneModeConfig(t *testing.T, home, reposRoot, workRoot string, urls map[string]string) {
	t.Helper()
	cfgPath := filepath.Join(home, ".config", "git-work", "config.yaml")
	cfg := "paths:\n  repos: " + reposRoot + "\n  work: " + workRoot + "\n" +
		"defaults:\n  mode: clone\nrepos:\n"
	for name, url := range urls {
		cfg += "  " + name + ":\n    url: " + url + "\n"
	}
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCloneModeAdd(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	ob := makeOriginIn(t, home, "beta")
	writeCloneModeConfig(t, home, reposRoot, workRoot, map[string]string{"alpha": oa, "beta": ob})
	if out, err := gwRun(t, bin, home, env, "new", "PROJ-2", "--repos", "alpha", "--branch", "PROJ-2-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-2-proj-2")
	if out, err := gwRun(t, bin, workDir, env, "add", "--repos", "beta", "--non-interactive"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if fi, err := os.Stat(filepath.Join(workDir, "beta", ".git")); err != nil || !fi.IsDir() {
		t.Fatalf("beta was not cloned: %v", err)
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Repos) != 2 {
		t.Errorf("state has %d repos, want 2", len(st.Repos))
	}
}

func TestCloneModeRefreshRebaseIntegrate(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	origin := makeOriginIn(t, home, "alpha")
	writeCloneModeConfig(t, home, reposRoot, workRoot, map[string]string{"alpha": origin})
	if out, err := gwRun(t, bin, home, env, "new", "PROJ-3", "--repos", "alpha", "--branch", "PROJ-3-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-3-proj-3")
	clone := filepath.Join(workDir, "alpha")
	sh(t, clone, "git", "config", "user.email", "t@t.t")
	sh(t, clone, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(clone, "feature.txt"), []byte("x\n"), 0o644)
	sh(t, clone, "git", "add", "feature.txt")
	sh(t, clone, "git", "commit", "-qm", "feature")

	// Advance origin/main from a seed clone, then refresh/rebase the feature
	// branch onto the moved default.
	seed := filepath.Join(home, "seed-alpha")
	os.WriteFile(filepath.Join(seed, "main.txt"), []byte("m\n"), 0o644)
	sh(t, seed, "git", "add", "main.txt")
	sh(t, seed, "git", "commit", "-qm", "main moves")
	sh(t, seed, "git", "push", "-q", "origin", "main")

	if out, err := gwRun(t, bin, workDir, env, "refresh"); err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	if out, err := gwRun(t, bin, workDir, env, "rebase", "alpha"); err != nil {
		t.Fatalf("rebase: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(shOut(t, clone, "git", "log", "-1", "--format=%s", "main")); got != "main moves" {
		t.Errorf("local main = %q, want the refreshed commit", got)
	}
	if got := strings.TrimSpace(shOut(t, clone, "git", "merge-base", "--is-ancestor", "main", "PROJ-3-x")); got != "" {
		// command exits nonzero on failure; shOut would have failed
	}

	if out, err := gwRun(t, bin, workDir, env, "integrate", "alpha", "--push"); err != nil {
		t.Fatalf("integrate: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(shOut(t, origin, "git", "log", "-1", "--format=%s", "main")); got != "feature" {
		t.Errorf("origin/main = %q, want feature", got)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".worktrees", "integrate-alpha")); !os.IsNotExist(err) {
		t.Error("integrate worktree was not cleaned up")
	}
}

func TestCloneModeIntegrateConflictResume(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	origin := makeOriginIn(t, home, "alpha")
	writeCloneModeConfig(t, home, reposRoot, workRoot, map[string]string{"alpha": origin})
	if out, err := gwRun(t, bin, home, env, "new", "PROJ-4", "--repos", "alpha", "--branch", "PROJ-4-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-4-proj-4")
	clone := filepath.Join(workDir, "alpha")
	sh(t, clone, "git", "config", "user.email", "t@t.t")
	sh(t, clone, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(clone, "f.txt"), []byte("feature\n"), 0o644)
	sh(t, clone, "git", "add", "f.txt")
	sh(t, clone, "git", "commit", "-qm", "feature edit")

	seed := filepath.Join(home, "seed-alpha")
	os.WriteFile(filepath.Join(seed, "f.txt"), []byte("main\n"), 0o644)
	sh(t, seed, "git", "add", "f.txt")
	sh(t, seed, "git", "commit", "-qm", "main edit")
	sh(t, seed, "git", "push", "-q", "origin", "main")

	out, err := gwRun(t, bin, workDir, env, "integrate", "alpha")
	if err == nil {
		t.Fatalf("expected a conflict, got:\n%s", out)
	}
	temp := filepath.Join(workDir, ".worktrees", "integrate-alpha")
	if _, err := os.Stat(temp); err != nil {
		t.Fatalf("conflict did not leave the integrate worktree: %v\n%s", err, out)
	}
	if out, err := gwRun(t, bin, workDir, env, "rebase", "alpha"); err == nil || !strings.Contains(out, "integrate conflict is waiting") {
		t.Fatalf("rebase during an integrate conflict = %v, want a refusal:\n%s", err, out)
	}
	out, err = gwRun(t, bin, workDir, env, "refresh")
	if err != nil || !strings.Contains(out, "integrate conflict is waiting") {
		t.Fatalf("refresh during an integrate conflict = %v, want a skip warning:\n%s", err, out)
	}

	sh(t, temp, "git", "config", "user.email", "t@t.t")
	sh(t, temp, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(temp, "f.txt"), []byte("resolved\n"), 0o644)
	sh(t, temp, "git", "add", "f.txt")
	sh(t, temp, "git", "cherry-pick", "--continue")

	if out, err := gwRun(t, bin, workDir, env, "integrate", "alpha", "--push"); err != nil {
		t.Fatalf("resume integrate: %v\n%s", err, out)
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Error("resume did not clean up the integrate worktree")
	}
	if got := strings.TrimSpace(shOut(t, origin, "git", "show", "main:f.txt")); got != "resolved" {
		t.Errorf("origin/main file = %q, want resolved", got)
	}
}

func makeLFSOriginIn(t *testing.T, home, name string) string {
	t.Helper()
	origin := filepath.Join(home, name+".git")
	sh(t, "", "git", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(home, "seed-"+name)
	sh(t, "", "git", "clone", "-q", origin, seed)
	sh(t, seed, "git", "config", "user.email", "t@t.t")
	sh(t, seed, "git", "config", "user.name", "t")
	sh(t, seed, "git", "lfs", "install", "--local")
	sh(t, seed, "git", "lfs", "track", "*.bin")
	os.WriteFile(filepath.Join(seed, "main.bin"), []byte("main-lfs\n"), 0o644)
	sh(t, seed, "git", "add", ".gitattributes", "main.bin")
	sh(t, seed, "git", "commit", "-qm", "main lfs")
	sh(t, seed, "git", "push", "-q", "origin", "main")
	// A second branch with its own LFS object, which a normal branch clone
	// does not need until fetch-all.
	sh(t, seed, "git", "switch", "-q", "-c", "other")
	os.WriteFile(filepath.Join(seed, "other.bin"), []byte("other-lfs\n"), 0o644)
	sh(t, seed, "git", "add", "other.bin")
	sh(t, seed, "git", "commit", "-qm", "other lfs")
	sh(t, seed, "git", "push", "-q", "origin", "other")
	return origin
}

func TestCloneModeLFSFetchAll(t *testing.T) {
	if !repo.LFSInstalled() {
		t.Skip("git-lfs not installed")
	}
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	origin := makeLFSOriginIn(t, home, "alpha")
	writeCloneModeConfig(t, home, reposRoot, workRoot, map[string]string{"alpha": origin})
	if out, err := gwRun(t, bin, home, env, "new", "PROJ-5", "--repos", "alpha", "--branch", "PROJ-5-x", "--non-interactive", "--lfs-fetch-all"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	clone := filepath.Join(workRoot, "PROJ-5-proj-5", "alpha")
	data, err := os.ReadFile(filepath.Join(clone, "main.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(string(data), "version https://git-lfs") {
		t.Errorf("main.bin is still an LFS pointer:\n%s", data)
	}
	objects := 0
	filepath.Walk(filepath.Join(clone, ".git", "lfs", "objects"), func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			objects++
		}
		return nil
	})
	if objects < 2 {
		t.Errorf("LFS store has %d object(s); --lfs-fetch-all should include the other branch's object", objects)
	}
}

func TestCloneModeRelocation(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	ob := makeOriginIn(t, home, "beta")
	writeCloneModeConfig(t, home, reposRoot, workRoot, map[string]string{"alpha": oa, "beta": ob})
	if out, err := gwRun(t, bin, home, env, "new", "PROJ-6", "--repos", "alpha,beta", "--branch", "PROJ-6-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	oldDir := filepath.Join(workRoot, "PROJ-6-proj-6")
	clone := filepath.Join(oldDir, "alpha")
	sh(t, clone, "git", "config", "user.email", "t@t.t")
	sh(t, clone, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(clone, "moved.txt"), []byte("x\n"), 0o644)
	sh(t, clone, "git", "add", "moved.txt")
	sh(t, clone, "git", "commit", "-qm", "moved")

	// done removes the folder, so running it from inside is refused before any
	// teardown. This is the Windows failure the old test used to hit.
	if out, err := gwRun(t, bin, oldDir, env, "done", oldDir, "--non-interactive"); err == nil || !strings.Contains(out, "refusing to tear down") {
		t.Fatalf("done from inside the folder = %v, want a refusal:\n%s", err, out)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("refusal removed the work folder: %v", err)
	}

	newDir := filepath.Join(home, "relocated", "PROJ-6-proj-6")
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"push", "alpha"},
		{"refresh"},
		{"rebase", "alpha"},
	} {
		if out, err := gwRun(t, bin, newDir, env, args...); err != nil {
			t.Fatalf("%v after relocation: %v\n%s", args, err, out)
		}
	}
	if out, err := gwRun(t, bin, home, env, "done", newDir, "--non-interactive", "--delete-remote", "--force"); err != nil {
		t.Fatalf("done after relocation: %v\n%s", err, out)
	}
}

func TestCloneModeRmForceCleansFolderLeftovers(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	ob := makeOriginIn(t, home, "beta")
	writeCloneModeConfig(t, home, reposRoot, workRoot, map[string]string{"alpha": oa, "beta": ob})
	if out, err := gwRun(t, bin, home, env, "new", "PROJ-7", "--repos", "alpha,beta", "--branch", "PROJ-7-x", "--non-interactive"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	workDir := filepath.Join(workRoot, "PROJ-7-proj-7")
	agentAlpha := filepath.Join(workDir, ".worktrees", "agent-x", "alpha")
	agentBeta := filepath.Join(workDir, ".worktrees", "agent-x", "beta")
	integrateAlpha := filepath.Join(workDir, ".worktrees", "integrate-alpha")
	for _, dir := range []string{agentAlpha, agentBeta, integrateAlpha} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := gwRun(t, bin, workDir, env, "rm", "alpha", "--non-interactive", "--force"); err != nil {
		t.Fatalf("rm --force: %v\n%s", err, out)
	}
	for _, dir := range []string{agentAlpha, integrateAlpha} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s survived rm --force", dir)
		}
	}
	if _, err := os.Stat(agentBeta); err != nil {
		t.Errorf("other repo's agent worktree was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "alpha")); !os.IsNotExist(err) {
		t.Error("alpha clone survived rm")
	}
}

// gwMust returns a helper that runs git-work and fails the test on error.
func gwMust(t *testing.T, bin string, env []string) func(dir string, args ...string) string {
	t.Helper()
	return func(dir string, args ...string) string {
		t.Helper()
		out, err := gwRun(t, bin, dir, env, args...)
		if err != nil {
			t.Fatalf("git-work %v: %v\n%s", args, err, out)
		}
		return out
	}
}

// worktreePointsAt reports whether mainDir's worktree list names want, compared
// by file identity so separators and path case do not decide the answer.
func worktreePointsAt(t *testing.T, mainDir, want string) bool {
	t.Helper()
	wts, err := repo.ListWorktrees(mainDir)
	if err != nil {
		t.Fatalf("list worktrees of %s: %v", mainDir, err)
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat %s: %v", want, err)
	}
	for _, wt := range wts {
		if info, err := os.Stat(wt.Path); err == nil && os.SameFile(wantInfo, info) {
			return true
		}
	}
	return false
}

// localBranchExists reports whether dir has a local branch with that name.
func localBranchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	return strings.TrimSpace(shOut(t, dir, "git", "branch", "--list", branch)) != ""
}

// remoteBranchExists reports whether origin has a branch with that name.
func remoteBranchExists(t *testing.T, origin, branch string) bool {
	t.Helper()
	return strings.TrimSpace(shOut(t, "", "git", "ls-remote", "--heads", origin, "refs/heads/"+branch)) != ""
}

// Folder, state, work branches and every path that points at the folder move
// together, and a repo on its own branch is left there. A dry run changes
// nothing, and a second identical run is a no-op.
func TestRenameWorkFolder(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	ob := makeOriginIn(t, home, "beta")
	gw := gwMust(t, bin, env)

	gw(home, "clone", oa, "alpha")
	gw(home, "clone", ob, "beta")
	gw(home, "new", "PROJ-1", "--repos", "alpha", "--non-interactive")
	oldDir := filepath.Join(workRoot, "PROJ-1-proj-1")
	gw(oldDir, "add", "--repos", "beta", "--branch", "PROJ-9-beta", "--non-interactive")

	// The dry run reports the plan and touches nothing.
	out := gw(home, "rename", "PROJ-1-proj-1", "PROJ-1-fix-login", "--dry-run")
	if !strings.Contains(out, "Dry run") || !strings.Contains(out, "PROJ-9-beta") {
		t.Errorf("dry run output does not describe the plan:\n%s", out)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("dry run moved the folder: %v", err)
	}
	if localBranchExists(t, filepath.Join(reposRoot, "alpha", "main"), "PROJ-1-fix-login") {
		t.Error("dry run renamed a branch")
	}

	newDir := filepath.Join(workRoot, "PROJ-1-fix-login")
	out = gw(home, "rename", "PROJ-1-proj-1", "PROJ-1-fix-login")
	if !strings.Contains(out, "beta: left on PROJ-9-beta") {
		t.Errorf("rename output does not report the repo left on its own branch:\n%s", out)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("old folder %s still exists", oldDir)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("new folder missing: %v", err)
	}

	st, err := state.Load(newDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Slug != "fix-login" || st.Branch != "PROJ-1-fix-login" {
		t.Errorf("state slug/branch = %q/%q", st.Slug, st.Branch)
	}
	if st.TicketID != "PROJ-1" || st.Title != "PROJ-1" {
		t.Errorf("state ticket/title = %q/%q", st.TicketID, st.Title)
	}
	if alpha, _ := st.Repo("alpha"); alpha.Branch != "PROJ-1-fix-login" {
		t.Errorf("alpha branch = %q", alpha.Branch)
	}
	if beta, _ := st.Repo("beta"); beta.Branch != "PROJ-9-beta" {
		t.Errorf("beta branch = %q, want PROJ-9-beta", beta.Branch)
	}

	// The primary's registration follows the moved worktree, and the worktree
	// itself sits on the new branch.
	if !worktreePointsAt(t, filepath.Join(reposRoot, "alpha", "main"), filepath.Join(newDir, "alpha")) {
		list := shOut(t, filepath.Join(reposRoot, "alpha", "main"), "git", "worktree", "list")
		t.Errorf("alpha primary does not point at %q:\n%s", filepath.Join(newDir, "alpha"), list)
	}
	if got := strings.TrimSpace(shOut(t, filepath.Join(newDir, "alpha"), "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "PROJ-1-fix-login" {
		t.Errorf("alpha worktree branch = %q, want PROJ-1-fix-login", got)
	}

	// The workspace file is named after the folder, and the generated text
	// carries the new branch.
	if _, err := os.Stat(filepath.Join(newDir, "PROJ-1-fix-login.code-workspace")); err != nil {
		t.Errorf("workspace file not renamed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newDir, "PROJ-1-proj-1.code-workspace")); !os.IsNotExist(err) {
		t.Error("old workspace file left behind")
	}
	readme, _ := os.ReadFile(filepath.Join(newDir, "README.md"))
	if !strings.Contains(string(readme), "Branch: PROJ-1-fix-login") {
		t.Errorf("README.md was not re-rendered:\n%s", readme)
	}
	agents, _ := os.ReadFile(filepath.Join(newDir, "AGENTS.md"))
	if !strings.Contains(string(agents), "Branch: PROJ-1-fix-login") {
		t.Errorf("AGENTS.md was not re-rendered:\n%s", agents)
	}

	// A second identical run reports nothing to do.
	if out := gw(home, "rename", "PROJ-1-fix-login", "PROJ-1-fix-login"); !strings.Contains(out, "Nothing to rename") {
		t.Errorf("re-run did not report a no-op:\n%s", out)
	}

	// push acts on the renamed branch and publishes it under the new name.
	clone := filepath.Join(newDir, "alpha")
	sh(t, clone, "git", "config", "user.email", "t@t.t")
	sh(t, clone, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(clone, "work.txt"), []byte("x\n"), 0o644)
	sh(t, clone, "git", "add", "work.txt")
	sh(t, clone, "git", "commit", "-qm", "work")
	gw(newDir, "push", "alpha")
	if !remoteBranchExists(t, oa, "PROJ-1-fix-login") {
		t.Error("push after rename did not publish the new branch name")
	}
	if remoteBranchExists(t, oa, "PROJ-1-proj-1") {
		t.Error("push after rename published the old branch name")
	}
}

// Clone mode has no primary, so the move needs no worktree repair; the clones
// travel with the folder. --keep-branch renames the folder and slug and
// leaves the branch where it is.
func TestRenameCloneModeAndKeepBranch(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	origin := makeOriginIn(t, home, "alpha")
	writeCloneModeConfig(t, home, reposRoot, workRoot, map[string]string{"alpha": origin})
	gw := gwMust(t, bin, env)

	gw(home, "new", "PROJ-4", "--repos", "alpha", "--non-interactive")
	oldDir := filepath.Join(workRoot, "PROJ-4-proj-4")
	newDir := filepath.Join(workRoot, "PROJ-4-clone")
	gw(home, "rename", "PROJ-4-proj-4", "PROJ-4-clone")

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("old clone folder still exists")
	}
	if fi, err := os.Stat(filepath.Join(newDir, "alpha", ".git")); err != nil || !fi.IsDir() {
		t.Fatalf("clone did not move with the folder: %v", err)
	}
	if got := strings.TrimSpace(shOut(t, filepath.Join(newDir, "alpha"), "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "PROJ-4-clone" {
		t.Errorf("clone branch = %q, want PROJ-4-clone", got)
	}

	// The folder, slug and VS Code file move; the branch stays.
	gw(home, "rename", "PROJ-4-clone", "PROJ-4-kept", "--keep-branch")
	keptDir := filepath.Join(workRoot, "PROJ-4-kept")
	st, err := state.Load(keptDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Slug != "kept" {
		t.Errorf("slug = %q, want kept", st.Slug)
	}
	if st.Branch != "PROJ-4-clone" {
		t.Errorf("branch = %q, want the unchanged PROJ-4-clone", st.Branch)
	}
	if got := strings.TrimSpace(shOut(t, filepath.Join(keptDir, "alpha"), "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "PROJ-4-clone" {
		t.Errorf("clone branch = %q, want PROJ-4-clone", got)
	}
	if _, err := os.Stat(filepath.Join(keptDir, "PROJ-4-kept.code-workspace")); err != nil {
		t.Errorf("workspace file did not follow the folder: %v", err)
	}
}

// A ticketless folder gains a ticket: the folder becomes <ticket>-<slug>, the
// branch follows, and TICKET.md appears where there was none.
func TestRenameTicketlessAddsTicket(t *testing.T) {
	bin, home, _, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	gw := gwMust(t, bin, env)

	gw(home, "clone", oa, "alpha")
	gw(home, "new", "-n", "Spike Harness", "--repos", "alpha", "--non-interactive")
	if _, err := os.Stat(filepath.Join(workRoot, "spike-harness", "TICKET.md")); !os.IsNotExist(err) {
		t.Fatal("a ticketless folder must not have TICKET.md")
	}

	gw(home, "rename", "spike-harness", "--ticket", "PROJ-8")
	workDir := filepath.Join(workRoot, "PROJ-8-spike-harness")
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.NoTicket || st.TicketID != "PROJ-8" {
		t.Errorf("state ticket = %q, no_ticket = %v", st.TicketID, st.NoTicket)
	}
	if st.Slug != "spike-harness" || st.Branch != "PROJ-8-spike-harness" {
		t.Errorf("slug/branch = %q/%q", st.Slug, st.Branch)
	}
	if st.Title != "PROJ-8" {
		t.Errorf("title = %q, want the display id with no provider configured", st.Title)
	}
	ticket, err := os.ReadFile(filepath.Join(workDir, "TICKET.md"))
	if err != nil {
		t.Fatalf("TICKET.md was not written: %v", err)
	}
	if !strings.Contains(string(ticket), "# PROJ-8: PROJ-8") {
		t.Errorf("TICKET.md = %s", ticket)
	}
	if got := strings.TrimSpace(shOut(t, filepath.Join(workDir, "alpha"), "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "PROJ-8-spike-harness" {
		t.Errorf("branch = %q, want PROJ-8-spike-harness", got)
	}

	// A later rename keeps the ticket and moves both names along.
	gw(home, "rename", "PROJ-8-spike-harness", "PROJ-8-spike-otel")
	st, err = state.Load(filepath.Join(workRoot, "PROJ-8-spike-otel"))
	if err != nil {
		t.Fatal(err)
	}
	if st.TicketID != "PROJ-8" || st.Slug != "spike-otel" || st.Branch != "PROJ-8-spike-otel" {
		t.Errorf("state after the second rename = %+v", st)
	}
}

// --remote publishes the new branch name and deletes the old one, and the
// upstream follows.
func TestRenameRemote(t *testing.T) {
	bin, home, _, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	gw := gwMust(t, bin, env)

	gw(home, "clone", oa, "alpha")
	gw(home, "new", "PROJ-5", "--repos", "alpha", "--branch", "PROJ-5-old", "--non-interactive")
	oldDir := filepath.Join(workRoot, "PROJ-5-proj-5")
	clone := filepath.Join(oldDir, "alpha")
	sh(t, clone, "git", "config", "user.email", "t@t.t")
	sh(t, clone, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(clone, "work.txt"), []byte("x\n"), 0o644)
	sh(t, clone, "git", "add", "work.txt")
	sh(t, clone, "git", "commit", "-qm", "work")
	gw(oldDir, "push", "alpha")
	if !remoteBranchExists(t, oa, "PROJ-5-old") {
		t.Fatal("setup: the old branch was not pushed")
	}

	gw(home, "rename", "PROJ-5-proj-5", "PROJ-5-new", "--branch", "PROJ-5-new", "--remote")
	newDir := filepath.Join(workRoot, "PROJ-5-new")
	if remoteBranchExists(t, oa, "PROJ-5-old") {
		t.Error("--remote left the old branch on origin")
	}
	if !remoteBranchExists(t, oa, "PROJ-5-new") {
		t.Error("--remote did not push the new branch")
	}
	if got := strings.TrimSpace(shOut(t, filepath.Join(newDir, "alpha"), "git", "rev-parse", "--abbrev-ref", "@{upstream}")); got != "origin/PROJ-5-new" {
		t.Errorf("upstream = %q, want origin/PROJ-5-new", got)
	}
}

// Every gate refuses the whole run before anything moves.
func TestRenameRefusals(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	oa := makeOriginIn(t, home, "alpha")
	gw := gwMust(t, bin, env)

	gw(home, "clone", oa, "alpha")
	gw(home, "new", "PROJ-1", "--repos", "alpha", "--non-interactive")
	workDir := filepath.Join(workRoot, "PROJ-1-proj-1")

	refuse := func(want string, args ...string) {
		t.Helper()
		out, err := gwRun(t, bin, home, env, args...)
		if err == nil {
			t.Fatalf("git-work %v succeeded; want a refusal", args)
		}
		if !strings.Contains(out, want) {
			t.Errorf("git-work %v output does not contain %q:\n%s", args, want, out)
		}
		if _, statErr := os.Stat(workDir); statErr != nil {
			t.Fatalf("a refusal moved the work folder: %v", statErr)
		}
	}

	// The target folder is taken.
	os.MkdirAll(filepath.Join(workRoot, "taken"), 0o755)
	refuse("already exists", "rename", "PROJ-1-proj-1", "taken")
	os.Remove(filepath.Join(workRoot, "taken"))

	// The target branch is taken.
	sh(t, filepath.Join(reposRoot, "alpha", "main"), "git", "branch", "PROJ-1-taken")
	refuse("already exists", "rename", "PROJ-1-proj-1", "PROJ-1-taken")

	// The target is the default branch.
	refuse("default branch", "rename", "PROJ-1-proj-1", "--branch", "main")

	// A hand-switched worktree is drift, not a rename target.
	sh(t, filepath.Join(workDir, "alpha"), "git", "switch", "-q", "-c", "elsewhere")
	refuse("state records branch", "rename", "PROJ-1-proj-1", "PROJ-1-next")
	sh(t, filepath.Join(workDir, "alpha"), "git", "switch", "-q", "PROJ-1-proj-1")

	// .worktrees holds a worktree that must not move under its owner.
	os.MkdirAll(filepath.Join(workDir, ".worktrees", "agent-x"), 0o755)
	refuse(".worktrees contains", "rename", "PROJ-1-proj-1", "PROJ-1-next")
	os.RemoveAll(filepath.Join(workDir, ".worktrees"))

	// A name claiming another ticket without --ticket.
	refuse("names ticket", "rename", "PROJ-1-proj-1", "PROJ-2-thing")

	// A folder that moves refuses to run from inside itself.
	cmd := exec.Command(bin, "rename", workDir, "PROJ-1-next")
	cmd.Dir = workDir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "refusing to rename") {
		t.Errorf("rename from inside the folder = %v, want a refusal:\n%s", err, out)
	}

	// Nothing above changed anything.
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Slug != "proj-1" || st.Branch != "PROJ-1-proj-1" {
		t.Errorf("state changed after refusals: %+v", st)
	}
	if localBranchExists(t, filepath.Join(reposRoot, "alpha", "main"), "PROJ-1-next") {
		t.Error("a refusal renamed a branch")
	}
}

// A file at the repos root lands in every folder's instructions file, between
// the operator overlay and the per-repo sections, and follows instructions_file
// with the other name as the fallback.
func TestReposRootOverlay(t *testing.T) {
	bin, home, reposRoot, workRoot, env := newTestEnv(t)
	origin := makeOriginIn(t, home, "alpha")
	gw := gwMust(t, bin, env)

	gw(home, "clone", origin, "alpha")
	os.WriteFile(filepath.Join(reposRoot, "AGENTS.md"), []byte("repos-root guidance\n"), 0o644)
	gw(home, "new", "-n", "overlay-probe", "--repos", "alpha", "--non-interactive")
	workDir := filepath.Join(workRoot, "overlay-probe")

	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(workDir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(data)
	}
	if agents := read("AGENTS.md"); !strings.Contains(agents, "repos-root guidance") {
		t.Fatalf("repos-root overlay missing from the generated file:\n%s", agents)
	}

	// General rules come first: operator overlay, then repos-root, then repos.
	cfgDir := filepath.Join(home, ".config", "git-work")
	if err := os.WriteFile(filepath.Join(cfgDir, "AGENTS.md"), []byte("operator guidance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gw(workDir, "refresh", "--no-pull")
	agents := read("AGENTS.md")
	at := -1
	for _, section := range []string{"operator guidance", "repos-root guidance", "## alpha (./alpha)"} {
		i := strings.Index(agents, section)
		if i < 0 {
			t.Fatalf("section %q missing:\n%s", section, agents)
		}
		if i < at {
			t.Fatalf("section %q is out of order:\n%s", section, agents)
		}
		at = i
	}

	// The fallback name is read too: rename the file, switch the setting, and
	// the next render picks it up under the new instructions file name.
	if err := os.Rename(filepath.Join(reposRoot, "AGENTS.md"), filepath.Join(reposRoot, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	gw(home, "config", "set", "instructions_file", "CLAUDE.md")
	gw(workDir, "refresh", "--no-pull")
	if claude := read("CLAUDE.md"); !strings.Contains(claude, "repos-root guidance") {
		t.Errorf("repos-root CLAUDE.md fallback missing after the switch:\n%s", claude)
	}
}
