package layout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPaths(t *testing.T) {
	// Roots and expectations are built with filepath.Join rather than written
	// as literals: these are OS paths, so the separator is `\` on Windows, and
	// a hardcoded "/home/u/..." only ever matched on Unix.
	repos := filepath.Join("home", "u", "dev", "repos")
	work := filepath.Join("home", "u", "dev", "work")
	l := Layout{ReposRoot: repos, WorkRoot: work}

	if got, want := l.RepoDir("myrepo"), filepath.Join(repos, "myrepo"); got != want {
		t.Errorf("RepoDir = %q; want %q", got, want)
	}
	if got, want := l.RepoOverlay("myrepo"), filepath.Join(repos, "myrepo", "CLAUDE.md"); got != want {
		t.Errorf("RepoOverlay = %q; want %q", got, want)
	}
	if got, want := l.WorkDir("PROJ-1-foo"), filepath.Join(work, "PROJ-1-foo"); got != want {
		t.Errorf("WorkDir = %q; want %q", got, want)
	}
	if got, want := l.Worktree("PROJ-1-foo", "myrepo"), filepath.Join(work, "PROJ-1-foo", "myrepo"); got != want {
		t.Errorf("Worktree = %q; want %q", got, want)
	}
}

func TestRepoMain(t *testing.T) {
	cases := []struct {
		name    string
		pointer string // "" = no pointer file written
		want    string // primary dir relative to the repo dir; "" = expect error
	}{
		{"main primary", "gitdir: ./main/.git\n", "main"},
		{"master primary", "gitdir: ./master/.git\n", "master"},
		{"no dot-slash prefix", "gitdir: main/.git\n", "main"},
		{"nested primary", "gitdir: ./release/1.0/.git\n", "release/1.0"},
		{"missing pointer", "", ""},
		{"not a gitdir line", "ref: refs/heads/main\n", ""},
		{"absolute path", "gitdir: /elsewhere/main/.git\n", ""},
		{"escapes repo dir", "gitdir: ./../evil/.git\n", ""},
		{"no .git suffix", "gitdir: ./main\n", ""},
		{"empty dir", "gitdir: /.git\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			repoDir := filepath.Join(root, "myrepo")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if c.pointer != "" {
				if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte(c.pointer), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			l := Layout{ReposRoot: root}
			got, err := l.RepoMain("myrepo")
			if c.want == "" {
				if err == nil {
					t.Fatalf("RepoMain = %q, want error", got)
				}
				if !strings.Contains(err.Error(), ".git pointer file") {
					t.Errorf("error should name the .git pointer file, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(repoDir, c.want); got != want {
				t.Errorf("RepoMain = %q, want %q", got, want)
			}
		})
	}
}

func TestListRepos(t *testing.T) {
	dir := t.TempDir()
	for _, r := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(dir, r, "main"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// a stray file should be ignored (not a directory)
	os.WriteFile(filepath.Join(dir, "notarepo.txt"), []byte("x"), 0o644)

	l := Layout{ReposRoot: dir}
	repos, err := l.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0] != "alpha" || repos[1] != "beta" {
		t.Errorf("ListRepos = %v", repos)
	}
}
