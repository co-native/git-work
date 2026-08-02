package add

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/cli/clitest"
	"github.com/co-native/git-work/internal/state"
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
		for _, want := range []string{"usage: git work add", "--repos", "--reuse-existing", "--always-new", "--non-interactive"} {
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
	if !strings.Contains(errOut, "usage: git work add") {
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

func TestRemaining(t *testing.T) {
	st := &state.State{Repos: []state.Repo{{Name: "alpha"}}}
	got := remaining([]string{"alpha", "beta", "gamma"}, st)
	if len(got) != 2 || got[0] != "beta" || got[1] != "gamma" {
		t.Errorf("remaining = %v", got)
	}
}

func TestParseFlags(t *testing.T) {
	o, err := parseFlags([]string{"--repos", "beta,gamma", "--non-interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.repos) != 2 || !o.nonInteractive {
		t.Errorf("opts = %+v", o)
	}
}

func TestParseFlagsReuseAlwaysNew(t *testing.T) {
	o, err := parseFlags([]string{"--repos", "beta", "--reuse-existing", "--always-new"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.reuseExisting || !o.alwaysNew {
		t.Errorf("opts = %+v", o)
	}
}
