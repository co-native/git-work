package workfolder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/state"
)

func TestScope(t *testing.T) {
	st := &state.State{TicketID: "T-1", Repos: []state.Repo{{Name: "a"}, {Name: "b"}}}

	all, err := Scope(st, "", true)
	if err != nil || len(all) != 2 {
		t.Fatalf("--all: got %d repos, err %v", len(all), err)
	}
	one, err := Scope(st, "b", false)
	if err != nil || len(one) != 1 || one[0].Name != "b" {
		t.Fatalf("named: got %v, err %v", one, err)
	}
	_, err = Scope(st, "c", false)
	if !cli.IsUsage(err) {
		t.Fatalf("unknown repo: err = %v, want a usage error", err)
	}
}

func TestRepoDirByMode(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "api")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := layout.WritePrimaryPointer(repoDir, "main"); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()

	wt := &Context{State: &state.State{Mode: layout.ModeWorktree}, Dir: work,
		Layout: layout.Layout{ReposRoot: root, WorkRoot: filepath.Dir(work)}}
	got, err := wt.RepoDir("api")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repoDir, "main"); got != want {
		t.Errorf("worktree RepoDir = %q, want %q", got, want)
	}
	if wt.Mode() != layout.ModeWorktree {
		t.Errorf("Mode = %q, want worktree", wt.Mode())
	}

	cl := &Context{State: &state.State{Mode: layout.ModeClone}, Dir: work}
	got, err = cl.RepoDir("api")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(work, "api"); got != want {
		t.Errorf("clone RepoDir = %q, want %q", got, want)
	}
	if got, want := cl.WorktreesDir(), filepath.Join(work, layout.WorktreesDirName); got != want {
		t.Errorf("WorktreesDir = %q, want %q", got, want)
	}
}

func TestRefuseInsideWorktrees(t *testing.T) {
	work := t.TempDir()
	inside := filepath.Join(work, layout.WorktreesDirName, "agent-x", "api")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RefuseInsideWorktrees(inside, work); err == nil {
		t.Error("expected refusal from inside .worktrees")
	}
	if err := RefuseInsideWorktrees(filepath.Join(work, "api"), work); err != nil {
		t.Errorf("ordinary repo dir refused: %v", err)
	}
	if err := RefuseInsideWorktrees(work, work); err != nil {
		t.Errorf("work root refused: %v", err)
	}
	if err := RefuseInsideWorktrees(t.TempDir(), work); err != nil {
		t.Errorf("unrelated dir refused: %v", err)
	}
}
