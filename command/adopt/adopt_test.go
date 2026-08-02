package adopt

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

// Help is intercepted by Run before run(), and adopt registers no FlagSet at
// all, so the descriptor is checked against a nil one; see TestRunHelp for the
// behaviour.
func TestHelpConformance(t *testing.T) {
	clitest.Check(t, Cmd, nil)
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

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			var code int
			out := captureStdout(t, func() { code = Run([]string{arg}) })
			if code != cli.OK {
				t.Fatalf("Run(%s) = %d; want %d", arg, code, cli.OK)
			}
			for _, want := range []string{"usage: git work adopt <dir>", "-h, --help"} {
				if !strings.Contains(out, want) {
					t.Errorf("help output = %q; want it to contain %q", out, want)
				}
			}
		})
	}
}

// TestRunUsageErrors pins the arity+leading-dash guard: a malformed invocation
// exits 2, distinct from work that failed.
func TestRunUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"a", "b"}, {"-x"}} {
		if got := Run(args); got != cli.Usage {
			t.Errorf("Run(%q) = %d; want %d", args, got, cli.Usage)
		}
	}
}

func mustRun(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

// makeOrigin creates a bare origin with one commit on main, returns its path.
func makeOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	mustRun(t, "", "git", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	mustRun(t, "", "git", "clone", "-q", origin, seed)
	mustRun(t, seed, "git", "config", "user.email", "t@t.t")
	mustRun(t, seed, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(seed, "f.txt"), []byte("hi\n"), 0o644)
	mustRun(t, seed, "git", "add", "f.txt")
	mustRun(t, seed, "git", "commit", "-qm", "init")
	mustRun(t, seed, "git", "push", "-q", "origin", "main")
	return origin
}

// clonePrimary sets up a conforming primary clone under l.ReposRoot.
func clonePrimary(t *testing.T, l layout.Layout, origin, name string) {
	t.Helper()
	if err := repo.Clone(origin, l.RepoDir(name), nil); err != nil {
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

// addWorktree adds a worktree of managed repo name at workDir/<dir> on a new
// branch and returns its path.
func addWorktree(t *testing.T, l layout.Layout, name, workDir, dir, branch string) string {
	t.Helper()
	main, err := l.RepoMain(name)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(workDir, dir)
	if err := repo.AddWorktree(main, wt, branch, "", true); err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestAdoptBootstrapsUnmanagedFolder(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOrigin(t), "alpha")
	workDir := filepath.Join(l.WorkRoot, "PROJ-3-git-work")
	wt := addWorktree(t, l, "alpha", workDir, "alpha", "PROJ-3-git-work")

	var out strings.Builder
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatalf("state not created: %v", err)
	}
	if st.TicketID != "PROJ-3" || st.Slug != "git-work" || st.Title != "PROJ-3" || st.Branch != "PROJ-3-git-work" {
		t.Errorf("bootstrapped state = %+v, want ticket PROJ-3, slug git-work, title PROJ-3, branch PROJ-3-git-work", st)
	}
	if len(st.Repos) != 1 || st.Repos[0] != (state.Repo{Name: "alpha", BranchSource: "existing", Branch: "PROJ-3-git-work"}) {
		t.Errorf("repos = %+v, want one existing alpha on PROJ-3-git-work", st.Repos)
	}
	for _, want := range []string{"Created", "Registered alpha"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output = %q, missing %q", out.String(), want)
		}
	}
}

// TestAdoptBootstrapsTicketlessFolder pins the no-ticket bootstrap: a folder
// name matching no ticket pattern must produce a ticketless state (mirroring
// `new --no-ticket`), not a fake ticket whose id stands in for the title.
func TestAdoptBootstrapsTicketlessFolder(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOrigin(t), "alpha")
	workDir := filepath.Join(l.WorkRoot, "my-experiment")
	wt := addWorktree(t, l, "alpha", workDir, "alpha", "side-quest")

	var out strings.Builder
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatalf("state not created: %v", err)
	}
	if !st.NoTicket || st.TicketID != "" {
		t.Errorf("state = %+v, want NoTicket with an empty ticket id", st)
	}
	if st.Title != "my-experiment" || st.Slug != "my-experiment" || st.Branch != "side-quest" {
		t.Errorf("state = %+v, want title/slug my-experiment, branch side-quest", st)
	}
	if !strings.Contains(out.String(), "ticketless") {
		t.Errorf("output = %q, want a ticketless bootstrap note", out.String())
	}
}

// TestAdoptBootstrapsMultiSegmentTicket pins multi-segment ticket prefixes
// (the repo's own dev-tools-N form) in the bootstrap inference.
func TestAdoptBootstrapsMultiSegmentTicket(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOrigin(t), "alpha")
	workDir := filepath.Join(l.WorkRoot, "dev-tools-12-fix-thing")
	wt := addWorktree(t, l, "alpha", workDir, "alpha", "dev-tools-12-fix-thing")

	var out strings.Builder
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatalf("state not created: %v", err)
	}
	if st.NoTicket || st.TicketID != "dev-tools-12" || st.Slug != "fix-thing" {
		t.Errorf("state = %+v, want ticket dev-tools-12 with slug fix-thing", st)
	}
}

func TestAdoptAddsToExistingState(t *testing.T) {
	l := newLayout(t)
	origin := makeOrigin(t)
	clonePrimary(t, l, origin, "alpha")
	clonePrimary(t, l, origin, "beta")
	workDir := filepath.Join(l.WorkRoot, "T-1-x")
	addWorktree(t, l, "alpha", workDir, "alpha", "T-1-x")
	existing := &state.State{
		TicketID: "T-1", Title: "The ticket", Slug: "x", Branch: "T-1-x",
		Repos: []state.Repo{{Name: "alpha", BranchSource: "new", Branch: "T-1-x"}},
	}
	if err := existing.Save(workDir); err != nil {
		t.Fatal(err)
	}
	wt := addWorktree(t, l, "beta", workDir, "beta", "feature/T-1")

	var out strings.Builder
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.TicketID != "T-1" || st.Title != "The ticket" || st.Branch != "T-1-x" {
		t.Errorf("existing state fields changed: %+v", st)
	}
	if len(st.Repos) != 2 || st.Repos[0].Name != "alpha" ||
		st.Repos[1] != (state.Repo{Name: "beta", BranchSource: "existing", Branch: "feature/T-1"}) {
		t.Errorf("repos = %+v, want alpha then existing beta on feature/T-1", st.Repos)
	}
	if strings.Contains(out.String(), "Created") {
		t.Errorf("output = %q, should not create a state file that already exists", out.String())
	}
}

func TestAdoptIdempotent(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOrigin(t), "alpha")
	workDir := filepath.Join(l.WorkRoot, "T-2-y")
	wt := addWorktree(t, l, "alpha", workDir, "alpha", "T-2-y")

	var out strings.Builder
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatalf("re-adopt: %v", err)
	}
	if !strings.Contains(out.String(), "already registered") || !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("re-adopt output = %q, want an already-registered no-op message", out.String())
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Repos) != 1 {
		t.Errorf("repos = %+v, want exactly one entry after re-adopt", st.Repos)
	}
}

func TestAdoptRejections(t *testing.T) {
	l := newLayout(t)
	origin := makeOrigin(t)
	clonePrimary(t, l, origin, "alpha")
	workDir := filepath.Join(l.WorkRoot, "T-3-z")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A plain directory, no .git.
	plain := filepath.Join(workDir, "plain")
	os.MkdirAll(plain, 0o755)

	// A full clone dropped into the work folder.
	clone := filepath.Join(workDir, "clone")
	mustRun(t, "", "git", "clone", "-q", origin, clone)

	// A worktree of an unmanaged clone (outside the repos root).
	outside := filepath.Join(t.TempDir(), "outside")
	mustRun(t, "", "git", "clone", "-q", origin, outside)
	unmanagedWt := filepath.Join(workDir, "outside")
	if err := repo.AddWorktree(outside, unmanagedWt, "T-3-z", "", true); err != nil {
		t.Fatal(err)
	}

	// A managed worktree whose directory name differs from the repo name.
	renamed := addWorktree(t, l, "alpha", workDir, "renamed", "T-3-renamed")

	tests := []struct {
		name, target, wantErr string
	}{
		{"missing dir", filepath.Join(workDir, "nope"), "not an existing directory"},
		{"no .git", plain, "not a git worktree"},
		{"full clone", clone, "not a worktree of a managed primary"},
		{"unmanaged primary", unmanagedWt, "not a worktree of a managed primary"},
		{"name mismatch", renamed, "does not match its repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			err := adoptOne(&out, l, workDir, tt.target)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("adoptOne(%s) err = %v, want containing %q", tt.target, err, tt.wantErr)
			}
		})
	}
	// No rejection may leave a state file behind.
	if _, err := state.Load(workDir); !os.IsNotExist(err) {
		t.Errorf("state file exists after rejections: %v", err)
	}
}

// TestResolveWorkDir pins the run-inside-the-work-folder contract: the
// target's parent must be the work folder located from the cwd (FindUp), or
// the cwd itself when nothing is managed yet - adopting into some other
// folder's state file is refused.
func TestResolveWorkDir(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	other := filepath.Join(root, "other")
	for _, d := range []string{
		filepath.Join(managed, "alpha"),
		filepath.Join(other, "beta"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st := &state.State{TicketID: "T-1", Title: "t", Slug: "x", Branch: "T-1-x"}
	if err := st.Save(managed); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, cwd, target, want, wantErr string
	}{
		{"managed, run in work folder", managed, filepath.Join(managed, "alpha"), managed, ""},
		{"managed, run in a subdir", filepath.Join(managed, "alpha"), filepath.Join(managed, "alpha"), managed, ""},
		{"managed, target elsewhere", managed, filepath.Join(other, "beta"), "", "not a subdirectory"},
		{"unmanaged, run in the folder", other, filepath.Join(other, "beta"), other, ""},
		{"unmanaged, run elsewhere", root, filepath.Join(other, "beta"), "", "not a subdirectory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWorkDir(tt.cwd, tt.target)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("resolveWorkDir(%s, %s) err = %v, want containing %q", tt.cwd, tt.target, err, tt.wantErr)
					return
				}
				// A target outside the work folder is a bad argument, so it
				// must exit 2 rather than look like work that failed.
				if !cli.IsUsage(err) {
					t.Errorf("resolveWorkDir(%s, %s) err = %v; want a usage error", tt.cwd, tt.target, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("resolveWorkDir(%s, %s) = %q, %v; want %q", tt.cwd, tt.target, got, err, tt.want)
			}
		})
	}
}

func TestInferTicket(t *testing.T) {
	tests := []struct {
		folder, ticket, slug string
	}{
		{"PROJ-3-git-work-updates", "PROJ-3", "git-work-updates"},
		{"PROJ-123", "PROJ-123", ""},
		{"proj-7-fix-thing", "proj-7", "fix-thing"},
		{"proj2-7-fix", "proj2-7", "fix"},
		// Multi-segment ticket prefixes (the repo's own dev-tools-N form).
		{"dev-tools-12-fix-thing", "dev-tools-12", "fix-thing"},
		{"DEV-TOOLS-1", "DEV-TOOLS-1", ""},
		// No ticket-shaped prefix: not a ticket at all (ticketless).
		{"my-cool-feature", "", ""},
		{"123-4-x", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.folder, func(t *testing.T) {
			ticket, slug := inferTicket(tt.folder)
			if ticket != tt.ticket || slug != tt.slug {
				t.Errorf("inferTicket(%q) = %q, %q; want %q, %q", tt.folder, ticket, slug, tt.ticket, tt.slug)
			}
		})
	}
}

func TestReadoptRecordsAMovedBranch(t *testing.T) {
	// State is written once at new/add/adopt time and nothing else re-syncs
	// it, so re-adopting is the supported way to say "this is the branch now"
	// after switching by hand - the remedy every drift refusal names.
	l := newLayout(t)
	clonePrimary(t, l, makeOrigin(t), "alpha")
	workDir := filepath.Join(l.WorkRoot, "T-2-y")
	wt := addWorktree(t, l, "alpha", workDir, "alpha", "T-2-y")

	var out strings.Builder
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatal(err)
	}
	mustRun(t, wt, "git", "checkout", "-q", "-b", "T-2-take-two")

	out.Reset()
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatalf("re-adopt: %v", err)
	}
	for _, want := range []string{"T-2-take-two", "T-2-y", "git work refresh"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("re-adopt output = %q; want it to mention %q", out.String(), want)
		}
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Repos) != 1 {
		t.Fatalf("repos = %+v; want exactly one entry", st.Repos)
	}
	if st.Repos[0].Branch != "T-2-take-two" {
		t.Errorf("recorded branch = %q; want the branch the worktree moved to", st.Repos[0].Branch)
	}
	if st.Repos[0].BranchSource != "existing" {
		t.Errorf("BranchSource = %q; want %q", st.Repos[0].BranchSource, "existing")
	}
}

func TestReadoptRefusesADetachedWorktree(t *testing.T) {
	// validateWorktree reads the branch before any state update, so a detached
	// worktree (mid-rebase, mid-bisect) cannot be recorded as the new branch.
	l := newLayout(t)
	clonePrimary(t, l, makeOrigin(t), "alpha")
	workDir := filepath.Join(l.WorkRoot, "T-2-y")
	wt := addWorktree(t, l, "alpha", workDir, "alpha", "T-2-y")

	var out strings.Builder
	if err := adoptOne(&out, l, workDir, wt); err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(mustRun(t, wt, "git", "rev-parse", "HEAD"))
	mustRun(t, wt, "git", "checkout", "-q", "--detach", sha)

	if err := adoptOne(&out, l, workDir, wt); err == nil {
		t.Fatal("re-adopt of a detached worktree succeeded; want a refusal")
	}
	st, err := state.Load(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Repos[0].Branch != "T-2-y" {
		t.Errorf("recorded branch = %q; want it left at T-2-y", st.Repos[0].Branch)
	}
}
