package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/layout"
	gitrepo "github.com/co-native/git-work/internal/repo"
)

// assertConforming verifies the managed layout of repo name: primary folder
// named after the default branch, parent .git pointer file, core.worktree
// "..", and no drift reported by inspectRepo.
func assertConforming(t *testing.T, l layout.Layout, name, def string) {
	t.Helper()
	st := inspectRepo(l, name)
	if len(st.Drift) != 0 {
		t.Errorf("%s: drift after adopt: %v", name, st.Drift)
	}
	if st.PrimaryDir != def || st.DefaultBranch != def {
		t.Errorf("%s: PrimaryDir=%q DefaultBranch=%q, want %s/%s", name, st.PrimaryDir, st.DefaultBranch, def, def)
	}
	data, err := os.ReadFile(filepath.Join(l.RepoDir(name), ".git"))
	if err != nil || string(data) != "gitdir: ./"+def+"/.git\n" {
		t.Errorf("%s: pointer file = %q, %v; want gitdir: ./%s/.git", name, data, err, def)
	}
}

// assertWorktreeAttached verifies wt is a functional linked worktree: its
// .git back-pointer resolves to an existing admin dir and git commands work.
func assertWorktreeAttached(t *testing.T, wt string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatalf("read worktree .git: %v", err)
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(wt, gitdir)
	}
	if _, err := os.Stat(gitdir); err != nil {
		t.Errorf("worktree back-pointer %q does not resolve: %v", gitdir, err)
	}
	run(t, wt, "git", "status", "--porcelain")
}

func TestAdoptFreshWithWorktree(t *testing.T) {
	l := newLayout(t)
	origin := makeOriginBranch(t, "main")
	src := filepath.Join(filepath.Dir(l.ReposRoot), "alpha")
	run(t, "", "git", "clone", "-q", origin, src)
	wt := filepath.Join(l.WorkRoot, "T-1-x", "alpha")
	if err := gitrepo.AddWorktree(src, wt, "T-1-x", "", true); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := adopt(&out, l, src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source %s still exists after adoption", src)
	}
	assertConforming(t, l, "alpha", "main")
	assertWorktreeAttached(t, wt)
	if !strings.Contains(out.String(), "Adopted") || !strings.Contains(out.String(), "Repaired 1 worktree") {
		t.Errorf("output = %q, want Adopted + Repaired lines", out.String())
	}
}

func TestAdoptNormalizesDriftThenIdempotent(t *testing.T) {
	// Legacy layout: upstream default is master but the primary folder is
	// named main. Adopt renames the folder, rewrites the pointer file, and
	// repairs the worktree back-pointer; a second run is a no-op.
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "master"), "alpha")
	repoDir := l.RepoDir("alpha")
	if err := os.Rename(filepath.Join(repoDir, "master"), filepath.Join(repoDir, "main")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ./main/.git\n"), 0o644)
	wt := filepath.Join(l.WorkRoot, "T-2-y", "alpha")
	if err := gitrepo.AddWorktree(filepath.Join(repoDir, "main"), wt, "T-2-y", "", true); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := adopt(&out, l, repoDir); err != nil {
		t.Fatal(err)
	}
	assertConforming(t, l, "alpha", "master")
	assertWorktreeAttached(t, wt)
	for _, want := range []string{
		"renamed primary main -> master",
		"wrote .git pointer file (gitdir: ./master/.git)",
		"Repaired 1 worktree",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output = %q, missing %q", out.String(), want)
		}
	}

	out.Reset()
	if err := adopt(&out, l, repoDir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("re-run output = %q, want a nothing-to-do message", out.String())
	}
	assertWorktreeAttached(t, wt)
}

func TestAdoptFreshChecksOutDefaultBranch(t *testing.T) {
	// A plain clone sitting on a non-default branch: adoption must check out
	// the default branch, or the new primary would pull/rebase-target a
	// stale default branch forever.
	l := newLayout(t)
	origin := makeOriginBranch(t, "main")
	src := filepath.Join(filepath.Dir(l.ReposRoot), "alpha")
	run(t, "", "git", "clone", "-q", origin, src)
	run(t, src, "git", "checkout", "-q", "-b", "hotfix")

	var out strings.Builder
	if err := adopt(&out, l, src); err != nil {
		t.Fatal(err)
	}
	assertConforming(t, l, "alpha", "main")
	main := filepath.Join(l.RepoDir("alpha"), "main")
	if head := strings.TrimSpace(run(t, main, "git", "rev-parse", "--abbrev-ref", "HEAD")); head != "main" {
		t.Errorf("primary HEAD = %q, want main", head)
	}
	if !strings.Contains(out.String(), "checked out the default branch main") {
		t.Errorf("output = %q, want a checkout note", out.String())
	}
}

func TestAdoptNormalizesCheckedOutBranch(t *testing.T) {
	// An already-managed repo whose primary was left on another branch:
	// re-running adopt is the single normalizer and must restore the
	// default branch (and only that - everything else already conforms).
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "main"), "alpha")
	main, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	run(t, main, "git", "checkout", "-q", "-b", "hotfix")

	var out strings.Builder
	if err := adopt(&out, l, l.RepoDir("alpha")); err != nil {
		t.Fatal(err)
	}
	assertConforming(t, l, "alpha", "main")
	if head := strings.TrimSpace(run(t, main, "git", "rev-parse", "--abbrev-ref", "HEAD")); head != "main" {
		t.Errorf("primary HEAD = %q, want main", head)
	}
	if !strings.Contains(out.String(), "checked out the default branch main") {
		t.Errorf("output = %q, want a checkout note", out.String())
	}

	out.Reset()
	if err := adopt(&out, l, l.RepoDir("alpha")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("re-run output = %q, want a nothing-to-do message", out.String())
	}
}

func TestAdoptNormalizesPlainCloneUnderReposRoot(t *testing.T) {
	// A plain clone dropped at <repos>/<name>/main by hand: no pointer
	// file, no core.worktree. Adopt fills both in without moving anything.
	l := newLayout(t)
	origin := makeOriginBranch(t, "main")
	repoDir := l.RepoDir("alpha")
	os.MkdirAll(repoDir, 0o755)
	run(t, "", "git", "clone", "-q", origin, filepath.Join(repoDir, "main"))

	var out strings.Builder
	if err := adopt(&out, l, repoDir); err != nil {
		t.Fatal(err)
	}
	assertConforming(t, l, "alpha", "main")
	for _, want := range []string{"wrote .git pointer file", "set core.worktree .."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output = %q, missing %q", out.String(), want)
		}
	}
}

func TestAdoptConformingIsNoOp(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "main"), "alpha")

	// The primary subdir also resolves to the repo, not a fresh adoption.
	for _, src := range []string{l.RepoDir("alpha"), filepath.Join(l.RepoDir("alpha"), "main")} {
		var out strings.Builder
		if err := adopt(&out, l, src); err != nil {
			t.Fatalf("adopt %s: %v", src, err)
		}
		if !strings.Contains(out.String(), "nothing to do") {
			t.Errorf("adopt %s output = %q, want a nothing-to-do message", src, out.String())
		}
	}
	assertConforming(t, l, "alpha", "main")
}

func TestAdoptErrors(t *testing.T) {
	l := newLayout(t)
	root := filepath.Dir(l.ReposRoot)
	origin := makeOriginBranch(t, "main")

	// An existing managed repo blocks fresh adoption under the same name.
	clonePrimary(t, l, origin, "taken")
	src := filepath.Join(root, "taken")
	run(t, "", "git", "clone", "-q", origin, src)

	// A linked worktree is not adoptable.
	main, err := l.RepoMain("taken")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wtree")
	if err := gitrepo.AddWorktree(main, wt, "T-3-z", "", true); err != nil {
		t.Fatal(err)
	}

	// Not a clone at all.
	notRepo := filepath.Join(root, "notrepo")
	os.MkdirAll(notRepo, 0o755)

	tests := []struct {
		name, src, wantErr string
	}{
		{"repos root itself", l.ReposRoot, "repos root itself"},
		{"too deep", filepath.Join(l.RepoDir("taken"), "main", "sub"), "nested too deep"},
		{"name collision", src, "already exists"},
		{"linked worktree", wt, "linked worktree"},
		{"not a clone", notRepo, "not a git clone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			err := adopt(&out, l, tt.src)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("adopt(%s) err = %v, want containing %q", tt.src, err, tt.wantErr)
			}
		})
	}
}

func TestResolveSource(t *testing.T) {
	root := t.TempDir()
	reposRoot := filepath.Join(root, "repos")
	for _, d := range []string{
		filepath.Join(reposRoot, "managed"),
		filepath.Join(reposRoot, "both"),
		filepath.Join(root, "devclone"),
		filepath.Join(root, "both"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name, arg, want, wantErr string
	}{
		{"bare managed", "managed", filepath.Join(reposRoot, "managed"), ""},
		{"bare dev", "devclone", filepath.Join(root, "devclone"), ""},
		{"bare ambiguous", "both", "", "ambiguous"},
		{"bare missing", "nope", "", "not found"},
		{"explicit path", filepath.Join(root, "devclone"), filepath.Join(root, "devclone"), ""},
		{"missing path", filepath.Join(root, "nope"), "", "not an existing directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSource(tt.arg, reposRoot)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("resolveSource(%q) err = %v, want containing %q", tt.arg, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("resolveSource(%q) = %q, %v; want %q", tt.arg, got, err, tt.want)
			}
		})
	}
}
