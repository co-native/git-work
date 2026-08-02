package refresh

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
)

// Help is intercepted by Run before parseFlags, so the descriptor and the
// registered flags are what must agree; see TestRunHelp for the behaviour.
func TestHelpConformance(t *testing.T) {
	clitest.Check(t, Cmd, newFlagSet(&options{}))
}

// -h is answered before anything touches the work folder, so it works from
// outside one too.
func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		var code int
		out, _ := captureOutput(t, func() { code = Run([]string{arg}) })
		if code != cli.OK {
			t.Fatalf("Run(%s) = %d; want %d", arg, code, cli.OK)
		}
		for _, want := range []string{"usage: git work refresh", "--no-pull"} {
			if !strings.Contains(out, want) {
				t.Errorf("help output = %q; want %q", out, want)
			}
		}
	}
}

// An unknown flag is a malformed invocation: exit 2 plus the usage block,
// reported before the command looks for a work folder.
func TestRunUnknownFlag(t *testing.T) {
	var code int
	_, errOut := captureOutput(t, func() { code = Run([]string{"--nonesuch"}) })
	if code != cli.Usage {
		t.Errorf("Run(--nonesuch) = %d; want %d", code, cli.Usage)
	}
	if !strings.Contains(errOut, "usage: git work refresh") {
		t.Errorf("stderr = %q; want the usage block", errOut)
	}
}

// captureOutput runs f with os.Stdout/os.Stderr redirected, returning both.
func captureOutput(t *testing.T, f func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	f()
	wOut.Close()
	wErr.Close()
	out, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := io.ReadAll(rErr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), string(errOut)
}

func TestParseFlags(t *testing.T) {
	o, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.noPull {
		t.Error("default should pull")
	}

	o, err = parseFlags([]string{"--no-pull"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.noPull {
		t.Error("--no-pull not parsed")
	}
}

func runGit(t *testing.T, dir, name string, args ...string) string {
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
	runGit(t, "", "git", "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	runGit(t, "", "git", "clone", "-q", origin, seed)
	runGit(t, seed, "git", "config", "user.email", "t@t.t")
	runGit(t, seed, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(seed, "f.txt"), []byte("hi\n"), 0o644)
	runGit(t, seed, "git", "add", "f.txt")
	runGit(t, seed, "git", "commit", "-qm", "init")
	runGit(t, seed, "git", "push", "-q", "origin", "main")
	return origin
}

func TestPullPrimariesReportsAndNeverAborts(t *testing.T) {
	root := t.TempDir()
	l := layout.Layout{
		ReposRoot: filepath.Join(root, "repos"),
		WorkRoot:  filepath.Join(root, "work"),
	}
	if err := repo.Clone(makeOrigin(t), l.RepoDir("alpha"), nil); err != nil {
		t.Fatal(err)
	}
	betaOrigin := makeOrigin(t)
	if err := repo.Clone(betaOrigin, l.RepoDir("beta"), nil); err != nil {
		t.Fatal(err)
	}
	// Advance beta's origin so its primary needs a fast-forward.
	w := filepath.Join(t.TempDir(), "w")
	runGit(t, "", "git", "clone", "-q", betaOrigin, w)
	runGit(t, w, "git", "config", "user.email", "t@t.t")
	runGit(t, w, "git", "config", "user.name", "t")
	os.WriteFile(filepath.Join(w, "new.txt"), []byte("x\n"), 0o644)
	runGit(t, w, "git", "add", "new.txt")
	runGit(t, w, "git", "commit", "-qm", "advance")
	runGit(t, w, "git", "push", "-q", "origin", "main")

	var out, errw strings.Builder
	// "missing" has no primary clone: reported on errw, loop continues.
	pullPrimaries(&out, &errw, l, []string{"alpha", "missing", "beta"})

	want := "Pulled alpha: already up to date\n" +
		"Pulled beta: fast-forwarded\n"
	if got := out.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if e := errw.String(); !strings.Contains(e, "pull missing:") || !strings.Contains(e, "(skipping)") {
		t.Errorf("stderr = %q; want a skipping warning for the missing repo", e)
	}
}
