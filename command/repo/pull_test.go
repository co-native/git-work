package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// advanceOrigin pushes one new commit to origin's main via a throwaway clone
// and returns the new tip sha.
func advanceOrigin(t *testing.T, origin, fname string) string {
	t.Helper()
	w := filepath.Join(t.TempDir(), "w")
	run(t, "", "git", "clone", "-q", origin, w)
	run(t, w, "git", "config", "user.email", "t@t.t")
	run(t, w, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(w, fname), []byte("x\n"), 0o644)
	run(t, w, "git", "add", fname)
	run(t, w, "git", "commit", "-qm", "advance")
	run(t, w, "git", "push", "-q", "origin", "main")
	return strings.TrimSpace(run(t, w, "git", "rev-parse", "HEAD"))
}

// divergePrimary gives the primary a local-only commit (same tree, so the
// worktree stays clean) so a subsequent origin advance makes it diverged.
func divergePrimary(t *testing.T, main string) {
	t.Helper()
	tree := strings.TrimSpace(run(t, main, "git", "rev-parse", "HEAD^{tree}"))
	c := strings.TrimSpace(run(t, main, "git", "-c", "user.name=t", "-c", "user.email=t@t.t",
		"commit-tree", tree, "-p", "HEAD", "-m", "local-only"))
	run(t, main, "git", "update-ref", "refs/heads/main", c)
}

func TestPullAllUpToDateAndFastForward(t *testing.T) {
	l := newLayout(t)
	clonePrimary(t, l, makeOriginBranch(t, "main"), "alpha")
	betaOrigin := makeOriginBranch(t, "main")
	clonePrimary(t, l, betaOrigin, "beta")
	tip := advanceOrigin(t, betaOrigin, "new.txt")

	var b strings.Builder
	if err := pullAll(&b, l, []string{"alpha", "beta"}); err != nil {
		t.Fatalf("pullAll: %v", err)
	}
	want := "alpha: already up to date\n" +
		"beta: fast-forwarded\n"
	if got := b.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	betaMain, err := l.RepoMain("beta")
	if err != nil {
		t.Fatal(err)
	}
	if head := strings.TrimSpace(run(t, betaMain, "git", "rev-parse", "HEAD")); head != tip {
		t.Errorf("beta HEAD = %s, want origin tip %s", head, tip)
	}
}

func TestPullAllDivergedReportedNotFatalMidLoop(t *testing.T) {
	l := newLayout(t)
	alphaOrigin := makeOriginBranch(t, "main")
	clonePrimary(t, l, alphaOrigin, "alpha")
	clonePrimary(t, l, makeOriginBranch(t, "main"), "beta")
	alphaMain, err := l.RepoMain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	divergePrimary(t, alphaMain)
	advanceOrigin(t, alphaOrigin, "other.txt")

	var b strings.Builder
	err = pullAll(&b, l, []string{"alpha", "beta"})
	if err == nil || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("pullAll = %v, want a failure naming alpha", err)
	}
	out := b.String()
	if !strings.Contains(out, "alpha: ") || !strings.Contains(out, "beta: already up to date\n") {
		t.Errorf("output = %q; want an alpha failure line and beta still pulled", out)
	}
}

func TestPullAllEmpty(t *testing.T) {
	var b strings.Builder
	if err := pullAll(&b, newLayout(t), nil); err != nil {
		t.Fatalf("pullAll: %v", err)
	}
	if got := b.String(); got != "no managed repos\n" {
		t.Errorf("output = %q", got)
	}
}
