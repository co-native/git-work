package gitcmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	tests := []struct {
		name     string
		args     []string
		wantErr  string // substring; "" means the parse must succeed
		wantRepo string
		wantAll  bool
		wantArgs []string
	}{
		{name: "all with a simple command", args: []string{"-a", "--", "st"},
			wantAll: true, wantArgs: []string{"st"}},
		{name: "repo with a simple command", args: []string{"alpha", "--", "status"},
			wantRepo: "alpha", wantArgs: []string{"status"}},
		{name: "long all", args: []string{"--all", "--", "status"},
			wantAll: true, wantArgs: []string{"status"}},
		// The whole point of the -- split: git's own flags must survive.
		{name: "git flags are not parsed as git-work flags", args: []string{"-a", "--", "log", "--oneline", "-5"},
			wantAll: true, wantArgs: []string{"log", "--oneline", "-5"}},
		{name: "a git -a is not git-work's -a", args: []string{"alpha", "--", "commit", "-a", "-m", "x"},
			wantRepo: "alpha", wantArgs: []string{"commit", "-a", "-m", "x"}},
		// A second -- belongs to git (e.g. `git log -- path`), not to us.
		{name: "only the first -- splits", args: []string{"-a", "--", "log", "--", "some/path"},
			wantAll: true, wantArgs: []string{"log", "--", "some/path"}},
		{name: "positional may follow the flag", args: []string{"-a", "--", "st"},
			wantAll: true, wantArgs: []string{"st"}},
		// cli.Wanted stops at `--`, so a -h past it is git's own and reaches
		// the parser as an ordinary token rather than asking for our help.
		{name: "-h after -- belongs to git", args: []string{"demo", "--", "log", "-h"},
			wantRepo: "demo", wantArgs: []string{"log", "-h"}},

		{name: "bare is a usage error", args: nil, wantErr: "after `--`"},
		{name: "scope but no separator", args: []string{"-a", "status"}, wantErr: "after `--`"},
		{name: "separator with nothing after", args: []string{"-a", "--"}, wantErr: "nothing after `--`"},
		{name: "repo and all", args: []string{"alpha", "-a", "--", "st"}, wantErr: "not both"},
		{name: "two repos", args: []string{"alpha", "beta", "--", "st"}, wantErr: "at most one <repo>"},
		{name: "unknown git-work flag", args: []string{"-a", "--nope", "--", "st"}, wantErr: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFlags(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseFlags(%q) error = %v; want %q", tt.args, err, tt.wantErr)
				}
				// Every way of getting the invocation wrong is a usage
				// error: exit 2 plus the usage block, never a bare 1.
				if !cli.IsUsage(err) {
					t.Errorf("parseFlags(%q) error %v is not a usage error", tt.args, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlags(%q) = %v; want success", tt.args, err)
			}
			if got.repo != tt.wantRepo || got.all != tt.wantAll {
				t.Errorf("parseFlags(%q) scope = (repo %q, all %v); want (%q, %v)",
					tt.args, got.repo, got.all, tt.wantRepo, tt.wantAll)
			}
			if !reflect.DeepEqual(got.args, tt.wantArgs) {
				t.Errorf("parseFlags(%q) git args = %q; want %q", tt.args, got.args, tt.wantArgs)
			}
		})
	}
}

func TestRunHelp(t *testing.T) {
	// -h and --help are identical, must not fall through to the work-folder
	// lookup (help works anywhere), and go to stdout with exit 0.
	for _, flag := range []string{"-h", "--help"} {
		var code int
		out := captureStdout(t, func() { code = Run([]string{flag}) })
		if code != cli.OK {
			t.Fatalf("Run(%s) = %d; want %d", flag, code, cli.OK)
		}
		for _, want := range []string{"usage: git work git", "-a, --all", "-h, --help", "git work git -a -- log --oneline -5"} {
			if !strings.Contains(out, want) {
				t.Errorf("Run(%s) stdout = %q; want %q", flag, out, want)
			}
		}
	}
}

func TestRunHelpIsRecognisedAlongsideScope(t *testing.T) {
	// Scanning is positional-agnostic before the separator, so a -h tacked
	// onto a real scope still prints help rather than running anything.
	var code int
	out := captureStdout(t, func() { code = Run([]string{"-a", "-h"}) })
	if code != cli.OK {
		t.Fatalf("Run(-a -h) = %d; want %d", code, cli.OK)
	}
	if !strings.HasPrefix(out, "usage: git work git") {
		t.Errorf("Run(-a -h) stdout = %q; want the usage block", out)
	}
}

func TestHelpAfterSeparatorIsGits(t *testing.T) {
	// The load-bearing case for this command: `git work git demo -- log -h`
	// must forward -h to git, so Run must not intercept it as our own help.
	args := []string{"demo", "--", "log", "-h"}
	if cli.Wanted(args) {
		t.Fatalf("cli.Wanted(%q) = true; -h past `--` belongs to git", args)
	}
	o, err := parseFlags(args)
	if err != nil {
		t.Fatalf("parseFlags(%q) = %v; want success", args, err)
	}
	if !reflect.DeepEqual(o.args, []string{"log", "-h"}) {
		t.Errorf("git args = %q; want [log -h] forwarded verbatim", o.args)
	}
}

func TestRunBadInvocationExitsUsage(t *testing.T) {
	// A malformed invocation is exit 2, distinguishable from work that failed.
	for _, args := range [][]string{nil, {"-a", "status"}, {"-a", "--"}, {"alpha", "-a", "--", "st"}} {
		var code int
		errOut := captureStderr(t, func() { code = Run(args) })
		if code != cli.Usage {
			t.Errorf("Run(%q) = %d; want %d", args, code, cli.Usage)
		}
		// A usage error carries the usage block, so the fix is on screen.
		if !strings.Contains(errOut, "usage: git work git") {
			t.Errorf("Run(%q) stderr = %q; want the usage block alongside the error", args, errOut)
		}
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	return capture(t, &os.Stdout, f)
}

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	return capture(t, &os.Stderr, f)
}

// capture swaps one of the process streams for a pipe while f runs and returns
// what was written to it.
func capture(t *testing.T, stream **os.File, f func()) string {
	t.Helper()
	old := *stream
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	*stream = w
	defer func() { *stream = old }()
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
	if err := os.WriteFile(filepath.Join(seed, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "f.txt")
	runGit(t, seed, "commit", "-qm", "init")
	runGit(t, seed, "push", "-q", "origin", "main")
	return origin
}

// setupWorkRepo clones origin as name's primary and adds a worktree on a new
// work branch under workDir. Returns the worktree path.
func setupWorkRepo(t *testing.T, l layout.Layout, workDir, name, origin, branch string) string {
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
	return wt
}

func newLayout(t *testing.T) (layout.Layout, string) {
	t.Helper()
	root := t.TempDir()
	l := layout.Layout{ReposRoot: filepath.Join(root, "repos"), WorkRoot: filepath.Join(root, "work")}
	return l, filepath.Join(l.WorkRoot, "PROJ-1-work")
}

func TestRunAllInEveryWorktree(t *testing.T) {
	l, workDir := newLayout(t)
	setupWorkRepo(t, l, workDir, "alpha", makeOrigin(t), "PROJ-1-work")
	setupWorkRepo(t, l, workDir, "beta", makeOrigin(t), "side-quest")

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "side-quest"},
	}}
	var out, errw strings.Builder
	o := &options{all: true, args: []string{"rev-parse", "--abbrev-ref", "HEAD"}}
	if err := runAll(o, strings.NewReader(""), &out, &errw, st, workDir); err != nil {
		t.Fatalf("runAll: %v (stderr %q)", err, errw.String())
	}
	// Headers make each block attributable, and the command really ran in
	// each worktree - beta reports its own branch, not alpha's.
	want := "=== alpha ===\nPROJ-1-work\n\n=== beta ===\nside-quest\n"
	if out.String() != want {
		t.Errorf("stdout = %q; want %q", out.String(), want)
	}
}

func TestRunScopedToOneRepo(t *testing.T) {
	l, workDir := newLayout(t)
	setupWorkRepo(t, l, workDir, "alpha", makeOrigin(t), "PROJ-1-work")
	setupWorkRepo(t, l, workDir, "beta", makeOrigin(t), "side-quest")

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "side-quest"},
	}}
	var out, errw strings.Builder
	o := &options{repo: "beta", args: []string{"rev-parse", "--abbrev-ref", "HEAD"}}
	if err := runAll(o, strings.NewReader(""), &out, &errw, st, workDir); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	if strings.Contains(out.String(), "alpha") {
		t.Errorf("stdout = %q; want no mention of the out-of-scope repo", out.String())
	}
	if !strings.Contains(out.String(), "side-quest") {
		t.Errorf("stdout = %q; want beta's branch", out.String())
	}
}

func TestRunPassesGitFlagsThrough(t *testing.T) {
	// The -- split has to survive real invocation, not just parsing: these
	// flags belong to git and must never be inspected by git-work.
	l, workDir := newLayout(t)
	wt := setupWorkRepo(t, l, workDir, "alpha", makeOrigin(t), "PROJ-1-work")
	if err := os.WriteFile(filepath.Join(wt, "extra.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "add", "extra.txt")
	runGit(t, wt, "commit", "-qm", "second commit")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var out, errw strings.Builder
	o := &options{all: true, args: []string{"log", "--oneline", "-1", "--format=%s"}}
	if err := runAll(o, strings.NewReader(""), &out, &errw, st, workDir); err != nil {
		t.Fatalf("runAll: %v (stderr %q)", err, errw.String())
	}
	if !strings.Contains(out.String(), "second commit") {
		t.Errorf("stdout = %q; want the git flags honored", out.String())
	}
}

func TestRunCollectsNonzeroExitsAndContinues(t *testing.T) {
	l, workDir := newLayout(t)
	alphaWt := setupWorkRepo(t, l, workDir, "alpha", makeOrigin(t), "PROJ-1-work")
	setupWorkRepo(t, l, workDir, "beta", makeOrigin(t), "PROJ-1-work")
	// Dirty alpha only, so `diff --quiet` exits 1 there and 0 in beta.
	if err := os.WriteFile(filepath.Join(alphaWt, "f.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "beta", Branch: "PROJ-1-work"},
	}}
	var out, errw strings.Builder
	o := &options{all: true, args: []string{"diff", "--quiet"}}
	err := runAll(o, strings.NewReader(""), &out, &errw, st, workDir)
	if err == nil || !strings.Contains(err.Error(), "alpha: git diff exited 1") {
		t.Errorf("runAll = %v; want alpha's nonzero exit reported", err)
	}
	// beta still ran despite alpha's failure.
	if !strings.Contains(out.String(), "=== beta ===") {
		t.Errorf("stdout = %q; want beta processed after alpha failed", out.String())
	}
}

func TestRunSkipsMissingWorktree(t *testing.T) {
	l, workDir := newLayout(t)
	setupWorkRepo(t, l, workDir, "alpha", makeOrigin(t), "PROJ-1-work")

	st := &state.State{Repos: []state.Repo{
		{Name: "alpha", Branch: "PROJ-1-work"},
		{Name: "gone", Branch: "PROJ-1-work"},
	}}
	var out, errw strings.Builder
	o := &options{all: true, args: []string{"rev-parse", "--abbrev-ref", "HEAD"}}
	if err := runAll(o, strings.NewReader(""), &out, &errw, st, workDir); err != nil {
		t.Errorf("runAll = %v; want nil (a missing worktree is a skip, not a failure)", err)
	}
	if !strings.Contains(errw.String(), "gone: worktree missing; skipping") {
		t.Errorf("stderr = %q; want a skip note", errw.String())
	}
}

func TestRunIgnoresBranchDrift(t *testing.T) {
	// Deliberately exempt from the shared worktree/state branch check: this
	// command exists to inspect whatever is checked out, drift included.
	l, workDir := newLayout(t)
	wt := setupWorkRepo(t, l, workDir, "alpha", makeOrigin(t), "PROJ-1-work")
	runGit(t, wt, "checkout", "-q", "-b", "side-quest")

	st := &state.State{Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var out, errw strings.Builder
	o := &options{all: true, args: []string{"rev-parse", "--abbrev-ref", "HEAD"}}
	if err := runAll(o, strings.NewReader(""), &out, &errw, st, workDir); err != nil {
		t.Fatalf("runAll = %v; want drift ignored", err)
	}
	if !strings.Contains(out.String(), "side-quest") {
		t.Errorf("stdout = %q; want the actually-checked-out branch reported", out.String())
	}
}

func TestRunUnknownRepoIsAnError(t *testing.T) {
	_, workDir := newLayout(t)
	st := &state.State{Title: "demo", Slug: "demo", NoTicket: true,
		Repos: []state.Repo{{Name: "alpha", Branch: "PROJ-1-work"}}}
	var out, errw strings.Builder
	o := &options{repo: "nope", args: []string{"status"}}
	err := runAll(o, strings.NewReader(""), &out, &errw, st, workDir)
	if err == nil || !strings.Contains(err.Error(), `repo "nope" is not part of`) {
		t.Fatalf("runAll = %v; want an unknown-repo error", err)
	}
	// Naming a repo that is not here is a malformed invocation, not a failed
	// run, so it exits 2 with the usage block.
	if !cli.IsUsage(err) {
		t.Errorf("runAll = %v; want a usage error", err)
	}
}
