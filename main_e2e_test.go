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
