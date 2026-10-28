package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteProbe(t *testing.T) {
	origin := makeOrigin(t)
	def, err := RemoteDefaultBranch(origin)
	if err != nil {
		t.Fatal(err)
	}
	if def != "main" {
		t.Errorf("RemoteDefaultBranch = %q, want main", def)
	}
	if ok, err := RemoteBranchExistsAt(origin, "main"); err != nil || !ok {
		t.Errorf("RemoteBranchExistsAt(main) = %v, %v", ok, err)
	}
	if ok, err := RemoteBranchExistsAt(origin, "nope"); err != nil || ok {
		t.Errorf("RemoteBranchExistsAt(nope) = %v, %v", ok, err)
	}

	seed := cloneWorkdir(t, origin)
	run(t, seed, "git", "push", "-q", "origin", "HEAD:refs/heads/feature/PROJ-7")
	run(t, seed, "git", "push", "-q", "origin", "HEAD:refs/heads/PROJ-77")
	got, err := RemoteBranchesMatching(origin, "PROJ-7")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Branch != "feature/PROJ-7" || !got[0].Remote {
		t.Errorf("RemoteBranchesMatching = %+v, want feature/PROJ-7 remote", got)
	}
}

func TestCloneCheckoutAndSwitch(t *testing.T) {
	origin := makeOrigin(t)
	dir := filepath.Join(t.TempDir(), "api")
	if err := CloneCheckout(origin, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("clone has no .git: %v", err)
	}
	def, err := DefaultBranchName(dir)
	if err != nil || def != "main" {
		t.Fatalf("default = %q, %v", def, err)
	}
	// A new branch from the checked-out default.
	if err := SwitchToBranch(dir, BranchChoice{Mode: CheckoutNew, Branch: "T-1-x"}); err != nil {
		t.Fatal(err)
	}
	if cur, _ := CurrentBranch(dir); cur != "T-1-x" {
		t.Errorf("current branch = %q, want T-1-x", cur)
	}
	// A remote-only branch is created tracking origin/<name>.
	seed := cloneWorkdir(t, origin)
	run(t, seed, "git", "push", "-q", "origin", "HEAD:refs/heads/remote-only")
	if err := SwitchToBranch(dir, BranchChoice{Mode: CheckoutRemote, Branch: "remote-only"}); err != nil {
		t.Fatal(err)
	}
	if cur, _ := CurrentBranch(dir); cur != "remote-only" {
		t.Errorf("current branch = %q, want remote-only", cur)
	}
}
