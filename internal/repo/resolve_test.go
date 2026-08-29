package repo

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalBranchExists(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	run(t, main, "git", "branch", "PROJ-1-x")
	if ok, err := LocalBranchExists(main, "PROJ-1-x"); err != nil || !ok {
		t.Errorf("LocalBranchExists(PROJ-1-x) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := LocalBranchExists(main, "nope"); err != nil || ok {
		t.Errorf("LocalBranchExists(nope) = %v, %v; want false, nil", ok, err)
	}
}

func TestResolveWorkBranch(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	noChoose := func(string, []string) (string, error) {
		t.Fatal("choose called unexpectedly")
		return "", nil
	}

	// no matching branches -> create the new branch
	c, err := ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix",
		ResolveOpts{NonInteractive: true, Choose: noChoose})
	if err != nil || c.Mode != CheckoutNew || c.Branch != "PROJ-1-fix" || c.Source != "new" {
		t.Errorf("no-match = %+v, %v", c, err)
	}

	// single match, non-interactive -> reuse it
	run(t, main, "git", "branch", "feature/PROJ-1")
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix",
		ResolveOpts{NonInteractive: true, Choose: noChoose})
	if err != nil || c.Mode != CheckoutLocal || c.Branch != "feature/PROJ-1" || c.Source != "existing" {
		t.Errorf("single-match = %+v, %v", c, err)
	}

	// always-new skips matching entirely
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix",
		ResolveOpts{AlwaysNew: true, NonInteractive: true, Choose: noChoose})
	if err != nil || c.Mode != CheckoutNew || c.Branch != "PROJ-1-fix" || c.Source != "new" {
		t.Errorf("always-new = %+v, %v", c, err)
	}

	// multiple matches, non-interactive -> hard error listing them
	run(t, main, "git", "branch", "PROJ-1-old")
	_, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix",
		ResolveOpts{NonInteractive: true, Choose: noChoose})
	if err == nil || !strings.Contains(err.Error(), "feature/PROJ-1") || !strings.Contains(err.Error(), "PROJ-1-old") {
		t.Errorf("multi-match err = %v; want error listing both branches", err)
	}

	// multiple matches + reuse-existing -> also a hard error (no first-pick)
	_, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix",
		ResolveOpts{ReuseExisting: true, Choose: noChoose})
	if err == nil {
		t.Error("reuse-existing with multiple matches should be a hard error")
	}

	// the exact branch already exists -> always reused, even under AlwaysNew
	run(t, main, "git", "branch", "PROJ-1-fix")
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix",
		ResolveOpts{AlwaysNew: true, NonInteractive: true, Choose: noChoose})
	if err != nil || c.Mode != CheckoutLocal || c.Branch != "PROJ-1-fix" || c.Source != "existing" {
		t.Errorf("exact-exists = %+v, %v", c, err)
	}

	// interactive: choosing the create-new sentinel creates the branch
	run(t, main, "git", "branch", "-D", "PROJ-1-fix")
	chooseCreate := func(_ string, options []string) (string, error) {
		return options[len(options)-1], nil
	}
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix",
		ResolveOpts{Choose: chooseCreate})
	if err != nil || c.Mode != CheckoutNew || c.Branch != "PROJ-1-fix" || c.Source != "new" {
		t.Errorf("choose-create = %+v, %v", c, err)
	}

	// interactive: choosing a match reuses it
	chooseFirst := func(_ string, options []string) (string, error) {
		return options[0], nil
	}
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix",
		ResolveOpts{Choose: chooseFirst})
	if err != nil || c.Mode != CheckoutLocal || c.Source != "existing" {
		t.Errorf("choose-reuse = %+v, %v", c, err)
	}
}

// A branch that exists on origin but not locally is the exact-name case in
// remote form: it is reused as a tracking branch in every mode, never
// shadowed by a new local branch of the same name.
func TestResolveWorkBranchRemoteOnly(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	seed := cloneWorkdir(t, origin)
	run(t, seed, "git", "push", "-q", "origin", "HEAD:refs/heads/PROJ-1-fix")
	if err := Fetch(main); err != nil {
		t.Fatal(err)
	}
	if ok, _ := LocalBranchExists(main, "PROJ-1-fix"); ok {
		t.Fatal("fixture: PROJ-1-fix must not exist locally")
	}
	noChoose := func(string, []string) (string, error) {
		t.Fatal("choose called unexpectedly")
		return "", nil
	}
	for name, opts := range map[string]ResolveOpts{
		"interactive":     {Choose: noChoose},
		"non-interactive": {NonInteractive: true, Choose: noChoose},
		"always-new":      {AlwaysNew: true, NonInteractive: true, Choose: noChoose},
	} {
		c, err := ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", opts)
		if err != nil || c.Mode != CheckoutRemote || c.Branch != "PROJ-1-fix" || c.Source != "existing" {
			t.Errorf("%s: remote-only = %+v, %v; want CheckoutRemote of PROJ-1-fix as existing", name, c, err)
		}
	}
}
