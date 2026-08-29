package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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

	// per-repo overlay for alpha
	os.WriteFile(filepath.Join(reposRoot, "alpha", "CLAUDE.md"), []byte("alpha overlay\n"), 0o644)

	gw(home, "new", "PROJ-2", "--repos", "alpha", "--branch", "PROJ-2-foo", "--non-interactive")
	workDir := filepath.Join(workRoot, "PROJ-2-proj-2")

	// refresh from inside a worktree
	gw(filepath.Join(workDir, "alpha"), "refresh")
	data, _ := os.ReadFile(filepath.Join(workDir, "CLAUDE.md"))
	if !strings.Contains(string(data), "alpha overlay") {
		t.Errorf("CLAUDE.md missing overlay after refresh:\n%s", data)
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
	if !strings.Contains(out, "alpha (main)") || !strings.Contains(out, "beta (main)") {
		t.Errorf("repos list should show both repos with their default branch, got:\n%s", out)
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
	// The re-rendered CLAUDE.md must state each repo's own branch: beta was
	// adopted on side-quest, not the folder-level my-feature branch.
	claude, _ := os.ReadFile(filepath.Join(workDir, "CLAUDE.md"))
	if !strings.Contains(string(claude), "## alpha (./alpha)\nBranch: my-feature\n") {
		t.Errorf("CLAUDE.md should state alpha's branch:\n%s", claude)
	}
	if !strings.Contains(string(claude), "## beta (./beta)\nBranch: side-quest\n") {
		t.Errorf("CLAUDE.md should state beta's own branch side-quest:\n%s", claude)
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
