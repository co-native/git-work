package changes

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/cli/clitest"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

func TestHelpConformance(t *testing.T) {
	clitest.Check(t, Cmd, newFlagSet(&options{}))
}

func TestParseFlags(t *testing.T) {
	all, err := parseFlags([]string{"--all"})
	if err != nil {
		t.Fatal(err)
	}
	if !all.all || all.repo != "" {
		t.Errorf("options = %+v; want --all", all)
	}

	one, err := parseFlags([]string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if one.repo != "alpha" || one.all {
		t.Errorf("options = %+v; want alpha", one)
	}
	if one.behind {
		t.Errorf("options = %+v; want --behind off by default", one)
	}

	for _, args := range [][]string{{"alpha", "--behind"}, {"alpha", "-b"}, {"--all", "--behind"}} {
		o, err := parseFlags(args)
		if err != nil {
			t.Fatalf("parseFlags(%v): %v", args, err)
		}
		if !o.behind {
			t.Errorf("parseFlags(%v) = %+v; want behind set", args, o)
		}
	}
}

func TestParseFlagsRejectsInvalidScope(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--all", "alpha"},
		{"alpha", "beta"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("parseFlags(%v) = nil error; want error", args)
		}
	}
}

func TestRunRejectsInvalidScope(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--all", "alpha"},
		{"alpha", "beta"},
	} {
		var code int
		errOut := captureStderr(t, func() { code = Run(args) })
		if code != cli.Usage {
			t.Errorf("Run(%v) = %d; want %d", args, code, cli.Usage)
		}
		if !strings.Contains(errOut, "usage: git work changes") {
			t.Errorf("Run(%v) stderr = %q; want usage", args, errOut)
		}
	}
}

func TestRunHelp(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = Run([]string{"--help"}) })
	if code != 0 {
		t.Fatalf("Run(--help) = %d; want 0", code)
	}
	for _, want := range []string{"usage: git work changes", "--all", "-b, --behind", "-h, --help"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output = %q; want %q", out, want)
		}
	}
}

func TestChangesListsUnintegratedCommitsForScope(t *testing.T) {
	l, workDir := newLayout(t)
	alpha := setupWorkBranch(t, l, workDir, "alpha", 2)
	setupWorkBranch(t, l, workDir, "beta", 1)

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}

	var err error
	out := captureStdout(t, func() {
		err = changes(&options{repo: "alpha"}, l, st, workDir)
	})
	if err != nil {
		t.Fatalf("changes alpha: %v", err)
	}
	for _, want := range []string{
		"alpha: 2 un-integrated commits on PROJ-1-work not in main",
		"feat 0",
		"feat 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q; want %q", out, want)
		}
	}
	if strings.Contains(out, "beta") {
		t.Errorf("output for alpha includes beta:\n%s", out)
	}
	if got := strings.TrimSpace(runGit(t, alpha, "branch", "--show-current")); got != "PROJ-1-work" {
		t.Errorf("changes changed the checked-out branch to %q", got)
	}
}

func TestChangesExcludesPatchEquivalentCommit(t *testing.T) {
	l, workDir := newLayout(t)
	wt := setupWorkBranch(t, l, workDir, "alpha", 1)
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD"))
	runGit(t, main, "cherry-pick", sha)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var changesErr error
	out := captureStdout(t, func() {
		changesErr = changes(&options{repo: "alpha"}, l, st, workDir)
	})
	if changesErr != nil {
		t.Fatalf("changes alpha: %v", changesErr)
	}
	if !strings.Contains(out, "alpha: no un-integrated commits on PROJ-1-work not in main") {
		t.Errorf("output = %q; want an empty report", out)
	}
	if strings.Contains(out, "feat 0") {
		t.Errorf("output includes the patch-equivalent commit:\n%s", out)
	}
}

func TestChangesAllReportsEmptyRepo(t *testing.T) {
	l, workDir := newLayout(t)
	setupWorkBranch(t, l, workDir, "alpha", 1)
	setupWorkBranch(t, l, workDir, "beta", 0)

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}

	var err error
	out := captureStdout(t, func() {
		err = changes(&options{all: true}, l, st, workDir)
	})
	if err != nil {
		t.Fatalf("changes --all: %v", err)
	}
	for _, want := range []string{
		"alpha: 1 un-integrated commit on PROJ-1-work not in main",
		"feat 0",
		"beta: no un-integrated commits on PROJ-1-work not in main",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q; want %q", out, want)
		}
	}
	// One repo's block must not run straight into the next: the commit detail
	// ends alpha's block, then a blank line, then beta's line.
	if !strings.Contains(out, "feat 0\n\nbeta: no un-integrated commits") {
		t.Errorf("output = %q; want a blank line between the alpha and beta blocks", out)
	}
}

// TestChangesBehindListsCommitsOnDefault covers the plain case: the default
// branch advanced while the work branch was open, so those commits are the
// ones a rebase would replay onto.
func TestChangesBehindListsCommitsOnDefault(t *testing.T) {
	l, workDir := newLayout(t)
	setupWorkBranch(t, l, workDir, "alpha", 1)
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	writeCommit(t, main, "landed.txt", "landed\n", "landed on main")
	writeCommit(t, main, "more.txt", "more\n", "also landed on main")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var changesErr error
	out := captureStdout(t, func() {
		changesErr = changes(&options{repo: "alpha", behind: true}, l, st, workDir)
	})
	if changesErr != nil {
		t.Fatalf("changes alpha --behind: %v", changesErr)
	}
	for _, want := range []string{
		"alpha: 2 commits in main not on PROJ-1-work",
		"landed on main",
		"also landed on main",
		"alpha: 1 un-integrated commit on PROJ-1-work not in main",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q; want %q", out, want)
		}
	}
	// The work branch's own commit belongs to the forward block only.
	if strings.Count(out, "feat 0") != 1 {
		t.Errorf("output = %q; want feat 0 listed once", out)
	}
}

// TestChangesBehindReportsInSync pins the wording of an empty behind list, so a
// repo whose branch is current reads as such instead of silently dropping the
// block.
func TestChangesBehindReportsInSync(t *testing.T) {
	l, workDir := newLayout(t)
	setupWorkBranch(t, l, workDir, "alpha", 1)

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var changesErr error
	out := captureStdout(t, func() {
		changesErr = changes(&options{repo: "alpha", behind: true}, l, st, workDir)
	})
	if changesErr != nil {
		t.Fatalf("changes alpha --behind: %v", changesErr)
	}
	if !strings.Contains(out, "alpha: no commits in main not on PROJ-1-work") {
		t.Errorf("output = %q; want an in-sync behind report", out)
	}
}

// TestChangesBehindListsSquashedIntegration documents the boundary between the
// two rules. The forward block uses patch equivalence, so a squash-integrated
// branch has nothing left to integrate; the behind block uses ancestry, so the
// squash commit is listed - the branch really does not have that commit as
// history. This is deliberate, not a bug: see reportBehind.
func TestChangesBehindListsSquashedIntegration(t *testing.T) {
	l, workDir := newLayout(t)
	setupWorkBranch(t, l, workDir, "alpha", 2)
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "merge", "--squash", "PROJ-1-work")
	runGit(t, main, "commit", "-qm", "squashed work")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var changesErr error
	out := captureStdout(t, func() {
		changesErr = changes(&options{repo: "alpha", behind: true}, l, st, workDir)
	})
	if changesErr != nil {
		t.Fatalf("changes alpha --behind: %v", changesErr)
	}
	if !strings.Contains(out, "alpha: 1 commit in main not on PROJ-1-work") {
		t.Errorf("output = %q; want the squash commit in the behind list", out)
	}
	if !strings.Contains(out, "squashed work") {
		t.Errorf("output = %q; want the squash commit subject", out)
	}
}

// TestChangesWithoutBehindKeepsOutput checks the flag changes nothing when it
// is absent: the behind list is the only output it may add.
func TestChangesWithoutBehindKeepsOutput(t *testing.T) {
	l, workDir := newLayout(t)
	setupWorkBranch(t, l, workDir, "alpha", 1)
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	writeCommit(t, main, "landed.txt", "landed\n", "landed on main")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var changesErr error
	out := captureStdout(t, func() {
		changesErr = changes(&options{repo: "alpha"}, l, st, workDir)
	})
	if changesErr != nil {
		t.Fatalf("changes alpha: %v", changesErr)
	}
	want := "alpha: 1 un-integrated commit on PROJ-1-work not in main\n" +
		"  " + shortSHA(t, workDir, "alpha", "PROJ-1-work") + " feat 0\n"
	if out != want {
		t.Errorf("output = %q; want exactly %q", out, want)
	}
}

func shortSHA(t *testing.T, workDir, name, branch string) string {
	t.Helper()
	sha := strings.TrimSpace(runGit(t, filepath.Join(workDir, name), "rev-parse", branch))
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func newLayout(t *testing.T) (layout.Layout, string) {
	t.Helper()
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	return l, filepath.Join(l.WorkRoot, "PROJ-1-work")
}

func setupWorkBranch(t *testing.T, l layout.Layout, workDir, name string, commits int) string {
	t.Helper()
	origin := makeOrigin(t, name)
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
	for i := 0; i < commits; i++ {
		n := strconv.Itoa(i)
		writeCommit(t, wt, "feat"+n+".txt", "feature\n", "feat "+n)
	}
	return wt
}

func makeOrigin(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, name+".git")
	runGit(t, "", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	runGit(t, "", "clone", "-q", origin, seed)
	runGit(t, seed, "config", "user.email", "t@t.t")
	runGit(t, seed, "config", "user.name", "t")
	writeCommit(t, seed, "f.txt", "base\n", "init")
	runGit(t, seed, "push", "-q", "origin", "main")
	return origin
}

func writeCommit(t *testing.T, dir, file, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", file)
	runGit(t, dir, "commit", "-qm", message)
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

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	f()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
