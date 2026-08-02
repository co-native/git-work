package repo

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/cli/clitest"
)

// The group's listing is rendered from Cmd.Subs, so the descriptors are what
// `git work repos` prints; see TestRunHelp for the dispatch behaviour. None of
// the four subcommands parses flags of its own - clone forwards git-clone's
// options verbatim rather than declaring them - hence the nil FlagSets.
func TestHelpConformance(t *testing.T) {
	clitest.CheckGroup(t, Cmd, []*cli.Command{CloneCmd, AdoptCmd, ListCmd, PullCmd})
	for _, sub := range Cmd.Subs {
		t.Run(sub.Name, func(t *testing.T) { clitest.Check(t, sub, nil) })
	}
}

// Help and dispatch must recognise the same set of words: a name in Subs with
// no runner would panic, and a runner absent from Subs would be unreachable.
func TestRunnersMatchSubs(t *testing.T) {
	for _, sub := range Cmd.Subs {
		name := sub.Name[strings.LastIndex(sub.Name, " ")+1:]
		if runners[name] == nil {
			t.Errorf("subcommand %q is listed but has no runner", name)
		}
	}
	for name := range runners {
		if cli.Lookup(Cmd.Subs, name) == nil {
			t.Errorf("runner %q is dispatchable but absent from the group's listing", name)
		}
	}
}

func TestRunHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string // a fragment the printed usage must contain
	}{
		{"help verb", []string{"help"}, "usage: git work repo <subcommand>"},
		{"short flag", []string{"-h"}, "subcommands:"},
		{"long flag", []string{"--help"}, "subcommands:"},
		{"help for a subcommand", []string{"help", "adopt"}, "usage: git work repo adopt <dir>"},
		{"subcommand -h", []string{"pull", "-h"}, "usage: git work repo pull"},
		// clone's own parser rejects a leading dash outside a `--` block, so
		// -h only works because Run intercepts it first.
		{"clone -h", []string{"clone", "-h"}, "usage: git work repo clone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var code int
			out := captureStdout(t, func() { code = Run(tt.args) })
			if code != cli.OK {
				t.Fatalf("Run(%v) = %d; want %d", tt.args, code, cli.OK)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("Run(%v) printed:\n%s\nwant it to contain %q", tt.args, out, tt.want)
			}
		})
	}
}

// The group's listing must name every subcommand, since it is the only place a
// user learns they exist.
func TestRunHelpListsSubcommands(t *testing.T) {
	out := captureStdout(t, func() { Run([]string{"help"}) })
	for _, want := range []string{"clone", "adopt", "list", "pull"} {
		if !strings.Contains(out, want) {
			t.Errorf("group help does not mention %q:\n%s", want, out)
		}
	}
}

// `repo list -h` asks for help, not for a listing: the interception has to
// happen before runList, which would otherwise load the config and walk the
// repos root. Asserting on the exact output is what catches a listing that
// slipped through.
func TestRunSubcommandHelpDoesNotRun(t *testing.T) {
	// If the interception ever regresses, this keeps the test off the real
	// repos root rather than reporting the developer's own clones.
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows and HOME elsewhere.
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var code int
	out := captureStdout(t, func() { code = Run([]string{"list", "-h"}) })
	if code != cli.OK {
		t.Fatalf("Run(list -h) = %d; want %d", code, cli.OK)
	}
	if want := ListCmd.Render() + "\n"; out != want {
		t.Errorf("Run(list -h) printed:\n%s\nwant exactly list's usage:\n%s", out, want)
	}
}

func TestRunUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"unknown subcommand", []string{"bogus"}},
		{"help for an unknown subcommand", []string{"help", "bogus"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var code int
			out := captureStdout(t, func() { code = Run(tt.args) })
			if code != cli.Usage {
				t.Fatalf("Run(%v) = %d; want %d", tt.args, code, cli.Usage)
			}
			// A usage error is a diagnostic: it belongs on stderr, so stdout
			// stays clean for callers piping real output.
			if out != "" {
				t.Errorf("Run(%v) wrote to stdout: %q", tt.args, out)
			}
		})
	}
}

// captureStdout runs f with both standard streams redirected and returns what
// it wrote to stdout. stderr is captured too - not to assert on, but so a
// command's error output does not land in the test log.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	outR, outW := pipe(t)
	errR, errW := pipe(t)
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	f()
	outW.Close()
	errW.Close()
	out, err := io.ReadAll(outR)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, errR); err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func pipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	return r, w
}
