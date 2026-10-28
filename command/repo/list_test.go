package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	gitrepo "github.com/co-native/git-work/internal/repo"
)

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

// clonePrimary sets up a conforming primary clone under l.ReposRoot.
func clonePrimary(t *testing.T, l layout.Layout, origin, name string) {
	t.Helper()
	if err := gitrepo.Clone(origin, l.RepoDir(name), nil); err != nil {
		t.Fatalf("Clone %s: %v", name, err)
	}
}

func newLayout(t *testing.T) layout.Layout {
	t.Helper()
	root := t.TempDir()
	return layout.Layout{
		ReposRoot: filepath.Join(root, "repos"),
		WorkRoot:  filepath.Join(root, "work"),
	}
}

// driftContaining reports whether any drift finding contains substr.
func driftContaining(st repoStatus, substr string) bool {
	for _, d := range st.Drift {
		if strings.Contains(d, substr) {
			return true
		}
	}
	return false
}

func TestInspectRepoConforming(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "main"), "alpha")

	st := inspectRepo(&config.Config{}, l, "alpha")
	if len(st.Drift) != 0 {
		t.Errorf("conforming repo reported drift: %v", st.Drift)
	}
	if st.PrimaryDir != "main" || st.DefaultBranch != "main" {
		t.Errorf("PrimaryDir=%q DefaultBranch=%q, want main/main", st.PrimaryDir, st.DefaultBranch)
	}
	if len(st.Worktrees) != 0 {
		t.Errorf("expected no linked worktrees, got %v", st.Worktrees)
	}
}

func TestInspectRepoListsLinkedWorktrees(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "main"), "alpha")
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(l.WorkRoot, "PROJ-1-x", "alpha")
	if err := gitrepo.AddWorktree(main, wt, "PROJ-1-x", "", true); err != nil {
		t.Fatal(err)
	}

	st := inspectRepo(&config.Config{}, l, "alpha")
	if len(st.Drift) != 0 {
		t.Errorf("unexpected drift: %v", st.Drift)
	}
	if len(st.Worktrees) != 1 {
		t.Fatalf("Worktrees = %v, want exactly the linked worktree", st.Worktrees)
	}
	if got := st.Worktrees[0]; got.Branch != "PROJ-1-x" || filepath.Base(got.Path) != "alpha" {
		t.Errorf("worktree = %+v, want branch PROJ-1-x at .../alpha", got)
	}
}

func TestInspectRepoPrimaryNameDrift(t *testing.T) {
	// A master-defaulted repo whose primary folder is (legacy) "main".
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "master"), "alpha")
	repoDir := l.RepoDir("alpha")
	if err := os.Rename(filepath.Join(repoDir, "master"), filepath.Join(repoDir, "main")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ./main/.git\n"), 0o644)

	st := inspectRepo(&config.Config{}, l, "alpha")
	if st.PrimaryDir != "main" || st.DefaultBranch != "master" {
		t.Errorf("PrimaryDir=%q DefaultBranch=%q, want main/master", st.PrimaryDir, st.DefaultBranch)
	}
	if !driftContaining(st, `primary folder "main" does not match default branch "master"`) {
		t.Errorf("expected primary-name drift, got %v", st.Drift)
	}
}

func TestInspectRepoMissingPointer(t *testing.T) {
	// A plain clone dropped under the repos root: no parent .git pointer,
	// no core.worktree. Both must surface as drift; the primary is still
	// found by scanning.
	l := newLayout(t)
	origin := makeOriginBranch(t, "main")
	repoDir := l.RepoDir("alpha")
	os.MkdirAll(repoDir, 0o755)
	run(t, "", "git", "clone", "-q", origin, filepath.Join(repoDir, "main"))

	st := inspectRepo(&config.Config{}, l, "alpha")
	if !driftContaining(st, "missing .git pointer") {
		t.Errorf("expected missing-pointer drift, got %v", st.Drift)
	}
	if !driftContaining(st, "core.worktree") {
		t.Errorf("expected core.worktree drift, got %v", st.Drift)
	}
	if st.PrimaryDir != "main" || st.DefaultBranch != "main" {
		t.Errorf("fallback PrimaryDir=%q DefaultBranch=%q, want main/main", st.PrimaryDir, st.DefaultBranch)
	}
}

func TestInspectRepoCheckedOutBranchDrift(t *testing.T) {
	// A primary left on a non-default branch: pulls would freshen the wrong
	// branch and rebases would target an ever-staler default branch, so it
	// must surface as drift.
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "main"), "alpha")
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	run(t, main, "git", "checkout", "-q", "-b", "hotfix")

	st := inspectRepo(&config.Config{}, l, "alpha")
	if !driftContaining(st, `primary has "hotfix" checked out, want the default branch "main"`) {
		t.Errorf("expected checked-out-branch drift, got %v", st.Drift)
	}
}

func TestInspectRepoMissingCoreWorktree(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "main"), "alpha")
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	run(t, main, "git", "config", "--unset", "core.worktree")

	st := inspectRepo(&config.Config{}, l, "alpha")
	if !driftContaining(st, `core.worktree is "", want ".."`) {
		t.Errorf("expected core.worktree drift, got %v", st.Drift)
	}
}

func TestInspectRepoUnusableDirectory(t *testing.T) {
	// Not a repo at all: pointer drift is reported and nothing else is
	// determined.
	l := newLayout(t)
	os.MkdirAll(l.RepoDir("junk"), 0o755)

	st := inspectRepo(&config.Config{}, l, "junk")
	if !driftContaining(st, "missing .git pointer") {
		t.Errorf("expected missing-pointer drift, got %v", st.Drift)
	}
	if st.PrimaryDir != "" || st.DefaultBranch != "" || len(st.Worktrees) != 0 {
		t.Errorf("expected no details for an unusable dir, got %+v", st)
	}
}

func TestListReposSortedAndEmpty(t *testing.T) {
	l := newLayout(t)

	statuses, err := listRepos(&config.Config{}, l)
	if err != nil || len(statuses) != 0 {
		t.Fatalf("empty root: listRepos = %v, %v; want none, nil", statuses, err)
	}

	origin := makeOriginBranch(t, "main")
	clonePrimary(t, l, origin, "beta")
	clonePrimary(t, l, origin, "alpha")
	statuses, err = listRepos(&config.Config{}, l)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || statuses[0].Name != "alpha" || statuses[1].Name != "beta" {
		t.Errorf("listRepos order = %v, want alpha then beta", statuses)
	}
}

func TestWriteList(t *testing.T) {
	var b strings.Builder
	writeList(&b, nil)
	if got := b.String(); got != "no configured repos\n" {
		t.Errorf("empty listing = %q", got)
	}

	b.Reset()
	writeList(&b, []repoStatus{
		{Name: "alpha", PrimaryDir: "main", DefaultBranch: "main",
			ConfigURL: "git@x:alpha.git", OriginURL: "git@x:alpha.git",
			Worktrees: []gitrepo.Worktree{{Path: "/w/T-1/alpha", Branch: "T-1-x"}}},
		{Name: "beta", PrimaryDir: "main", DefaultBranch: "master",
			ConfigURL: "git@x:beta.git", OriginURL: "git@x:beta.git",
			Drift: []string{`primary folder "main" does not match default branch "master"`}},
		{Name: "gamma", ConfigOnly: true, ConfigURL: "git@x:gamma.git"},
	})
	got := b.String()
	for _, want := range []string{
		"NAME", "DEFAULT", "URL", "WORKTREES", "STATUS",
		"alpha  main", "git@x:alpha.git", "ok",
		"beta   master", "drift",
		"gamma  -", "configured, not cloned",
		"  alpha worktree /w/T-1/alpha (T-1-x)",
		`  beta drift: primary folder "main" does not match default branch "master"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listing missing %q; got:\n%s", want, got)
		}
	}
}

func TestListReposIncludesConfigOnly(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "main"), "alpha")
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"alpha": {URL: "git@x:alpha.git"},
		"ghost": {URL: "git@x:ghost.git", DefaultBranch: "trunk"},
	}}
	statuses, err := listRepos(cfg, l)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("got %d statuses, want 2: %+v", len(statuses), statuses)
	}
	byName := map[string]repoStatus{}
	for _, st := range statuses {
		byName[st.Name] = st
	}
	if byName["alpha"].ConfigOnly {
		t.Error("alpha should not be config-only")
	}
	if byName["alpha"].ConfigURL != "git@x:alpha.git" {
		t.Errorf("alpha config URL = %q", byName["alpha"].ConfigURL)
	}
	ghost := byName["ghost"]
	if !ghost.ConfigOnly || ghost.DefaultBranch != "trunk" || ghost.ConfigURL != "git@x:ghost.git" {
		t.Errorf("ghost = %+v", ghost)
	}
}
