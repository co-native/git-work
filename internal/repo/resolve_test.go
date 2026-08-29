package repo

import (
	"fmt"
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

// noChoose fails the test if the picker is reached: the modes under test
// must decide without it.
func noChoose(t *testing.T) func(string, []string) (string, error) {
	return func(string, []string) (string, error) {
		t.Fatal("choose called unexpectedly")
		return "", nil
	}
}

func TestResolveWorkBranch(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	nonInt := ResolveOpts{NonInteractive: true, Choose: noChoose(t)}
	explicit := ResolveOpts{Explicit: true, NonInteractive: true, Choose: noChoose(t)}

	// no candidates -> create the new branch
	c, err := ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", nonInt)
	if err != nil || c.Mode != CheckoutNew || c.Branch != "PROJ-1-fix" || c.Source != "new" {
		t.Errorf("no-match = %+v, %v", c, err)
	}

	// one candidate is never adopted silently: non-interactive refuses,
	// naming it and the --branch remedy
	run(t, main, "git", "branch", "feature/PROJ-1")
	_, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", nonInt)
	if err == nil || !strings.Contains(err.Error(), "feature/PROJ-1") || !strings.Contains(err.Error(), "--branch") {
		t.Errorf("single-match err = %v; want a refusal naming feature/PROJ-1 and --branch", err)
	}

	// --branch is the whole answer: candidates are not consulted, the
	// named branch is created...
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", explicit)
	if err != nil || c.Mode != CheckoutNew || c.Branch != "PROJ-1-fix" || c.Source != "new" {
		t.Errorf("explicit-new = %+v, %v", c, err)
	}
	// ...or reused when it names an existing one
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "feature/PROJ-1", explicit)
	if err != nil || c.Mode != CheckoutLocal || c.Branch != "feature/PROJ-1" || c.Source != "existing" {
		t.Errorf("explicit-reuse = %+v, %v", c, err)
	}

	// several candidates, non-interactive -> the refusal lists them all
	run(t, main, "git", "branch", "PROJ-1-old")
	_, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", nonInt)
	if err == nil || !strings.Contains(err.Error(), "feature/PROJ-1") || !strings.Contains(err.Error(), "PROJ-1-old") {
		t.Errorf("multi-match err = %v; want error listing both branches", err)
	}

	// the exact branch already exists -> reused without matching, in any mode
	run(t, main, "git", "branch", "PROJ-1-fix")
	for name, opts := range map[string]ResolveOpts{"non-interactive": nonInt, "explicit": explicit, "interactive": {Choose: noChoose(t)}} {
		c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", opts)
		if err != nil || c.Mode != CheckoutLocal || c.Branch != "PROJ-1-fix" || c.Source != "existing" {
			t.Errorf("%s: exact-exists = %+v, %v", name, c, err)
		}
	}

	// interactive: choosing the create-new sentinel creates the branch
	run(t, main, "git", "branch", "-D", "PROJ-1-fix")
	chooseCreate := func(_ string, options []string) (string, error) {
		return options[len(options)-1], nil
	}
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", ResolveOpts{Choose: chooseCreate})
	if err != nil || c.Mode != CheckoutNew || c.Branch != "PROJ-1-fix" || c.Source != "new" {
		t.Errorf("choose-create = %+v, %v", c, err)
	}

	// interactive: choosing a candidate reuses it
	chooseFirst := func(_ string, options []string) (string, error) {
		return options[0], nil
	}
	c, err = ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", ResolveOpts{Choose: chooseFirst})
	if err != nil || c.Mode != CheckoutLocal || c.Branch != "PROJ-1-old" || c.Source != "existing" {
		t.Errorf("choose-reuse = %+v, %v; want the first listed candidate", c, err)
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
	for name, opts := range map[string]ResolveOpts{
		"interactive":     {Choose: noChoose(t)},
		"non-interactive": {NonInteractive: true, Choose: noChoose(t)},
		"explicit":        {Explicit: true, NonInteractive: true, Choose: noChoose(t)},
	} {
		c, err := ResolveWorkBranch(main, "myrepo", "PROJ-1", "PROJ-1-fix", opts)
		if err != nil || c.Mode != CheckoutRemote || c.Branch != "PROJ-1-fix" || c.Source != "existing" {
			t.Errorf("%s: remote-only = %+v, %v; want CheckoutRemote of PROJ-1-fix as existing", name, c, err)
		}
	}
}

// A ticket-matching branch that exists only on origin is a candidate like a
// local one: listed as origin/<name> in the refusal and the picker, and
// checked out as a tracking branch when chosen.
func TestResolveWorkBranchRemoteCandidate(t *testing.T) {
	origin := makeOrigin(t)
	repoDir := filepath.Join(t.TempDir(), "myrepo")
	if err := Clone(origin, repoDir, nil); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(repoDir, "main")
	seed := cloneWorkdir(t, origin)
	run(t, seed, "git", "push", "-q", "origin", "HEAD:refs/heads/feature/PROJ-2")
	if err := Fetch(main); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveWorkBranch(main, "myrepo", "PROJ-2", "PROJ-2-fix", ResolveOpts{NonInteractive: true, Choose: noChoose(t)})
	if err == nil || !strings.Contains(err.Error(), "origin/feature/PROJ-2") {
		t.Errorf("non-interactive err = %v; want a refusal listing origin/feature/PROJ-2", err)
	}

	choose := func(_ string, options []string) (string, error) {
		if len(options) != 2 || options[0] != "origin/feature/PROJ-2" {
			t.Errorf("picker options = %v; want [origin/feature/PROJ-2 (create new ...)]", options)
		}
		return options[0], nil
	}
	c, err := ResolveWorkBranch(main, "myrepo", "PROJ-2", "PROJ-2-fix", ResolveOpts{Choose: choose})
	if err != nil || c.Mode != CheckoutRemote || c.Branch != "feature/PROJ-2" || c.Source != "existing" {
		t.Errorf("choose-remote = %+v, %v; want CheckoutRemote of feature/PROJ-2", c, err)
	}
}

// ResolveWorkBranches reports every repo's refusal in one go, keeps going
// past a failed fetch, and returns a plan in the order the repos were named.
func TestResolveWorkBranches(t *testing.T) {
	mains := map[string]string{}
	for _, name := range []string{"alpha", "beta"} {
		repoDir := filepath.Join(t.TempDir(), name)
		if err := Clone(makeOrigin(t), repoDir, nil); err != nil {
			t.Fatal(err)
		}
		mains[name] = filepath.Join(repoDir, "main")
	}
	run(t, mains["beta"], "git", "branch", "feature/PROJ-5")
	mainOf := func(name string) (string, error) {
		if m, ok := mains[name]; ok {
			return m, nil
		}
		return "", fmt.Errorf("%s: not cloned", name)
	}
	var warned []string
	warn := func(msg string) { warned = append(warned, msg) }
	nonInt := ResolveOpts{NonInteractive: true, Choose: noChoose(t)}

	// beta's candidate and the missing repo are reported together; alpha
	// (resolvable) does not appear in the error
	_, err := ResolveWorkBranches([]string{"alpha", "beta", "ghost"}, "PROJ-5", "PROJ-5-x",
		PlanOpts{ResolveOpts: nonInt, MainOf: mainOf, Warn: warn})
	if err == nil || !strings.Contains(err.Error(), "feature/PROJ-5") || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v; want beta's candidate and ghost reported together", err)
	}
	if strings.Contains(err.Error(), "alpha") {
		t.Errorf("err = %v; alpha resolves and must not be reported", err)
	}

	// --branch answers for every repo; the plan follows the order named
	plan, err := ResolveWorkBranches([]string{"beta", "alpha"}, "PROJ-5", "PROJ-5-x",
		PlanOpts{ResolveOpts: ResolveOpts{Explicit: true, NonInteractive: true, Choose: noChoose(t)}, MainOf: mainOf, Warn: warn})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 || plan[0].Name != "beta" || plan[1].Name != "alpha" || plan[0].Main != mains["beta"] {
		t.Errorf("plan = %+v; want beta then alpha with their primaries", plan)
	}
	for _, p := range plan {
		if p.Choice.Mode != CheckoutNew || p.Choice.Branch != "PROJ-5-x" {
			t.Errorf("%s: choice = %+v; want a new PROJ-5-x", p.Name, p.Choice)
		}
	}
	if len(warned) != 0 {
		t.Errorf("unexpected warnings: %v", warned)
	}

	// a repo whose fetch fails is a warning, not a refusal
	run(t, mains["alpha"], "git", "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nowhere.git"))
	plan, err = ResolveWorkBranches([]string{"alpha"}, "PROJ-5", "PROJ-5-x",
		PlanOpts{ResolveOpts: nonInt, MainOf: mainOf, Warn: warn})
	if err != nil || len(plan) != 1 {
		t.Fatalf("plan, err = %+v, %v; want alpha planned despite the failed fetch", plan, err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "fetch alpha") {
		t.Errorf("warnings = %v; want one about fetching alpha", warned)
	}
}
