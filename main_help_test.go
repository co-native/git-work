package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
)

// The per-package conformance tests check that each command describes itself
// correctly. These check the other half: that every described command is
// actually reachable, that the index and the commands agree, and that the three
// help routes resolve.

// paths lists every invocation a user can type, as an end-to-end guard that the
// registry has not silently lost one. It is written out by hand rather than
// derived from `commands` so that dropping an entry from the registry fails
// here instead of quietly shrinking the expectation.
var paths = [][]string{
	{"repos"}, {"repo"},
	{"repos", "clone"}, {"repos", "adopt"}, {"repos", "list"}, {"repos", "pull"},
	{"clone"}, {"new"}, {"add"}, {"adopt"}, {"refresh"},
	{"rebase"}, {"push"}, {"integrate"}, {"git"}, {"done"}, {"rm"},
	{"config"},
	{"config", "list"}, {"config", "get"}, {"config", "set"},
	{"config", "unset"}, {"config", "edit"}, {"config", "path"}, {"config", "init"},
}

// TestEveryPathAnswersHelp is the contract in one assertion: for all 25 paths
// and both spellings, usage goes to stdout, stderr stays empty, and the exit
// code is 0. It runs the real binary because the exit code and the stream are
// the observable behaviour, and both were wrong in different ways before.
func TestEveryPathAnswersHelp(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()

	for _, p := range paths {
		for _, flag := range []string{"-h", "--help"} {
			name := strings.Join(append(append([]string{}, p...), flag), " ")
			t.Run(name, func(t *testing.T) {
				cmd := exec.Command(bin, append(append([]string{}, p...), flag)...)
				cmd.Dir = t.TempDir()
				cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home+"/.config")
				var stdout, stderr strings.Builder
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()

				if err != nil {
					t.Errorf("exit = %v; want 0 (help is a success)", err)
				}
				if !strings.HasPrefix(stdout.String(), "usage: git work ") {
					t.Errorf("stdout does not start with a usage line:\n%s", stdout.String())
				}
				if stderr.Len() != 0 {
					t.Errorf("help wrote to stderr; it belongs on stdout:\n%s", stderr.String())
				}
				if !strings.Contains(stdout.String(), "-h, --help") {
					t.Errorf("help does not document -h/--help:\n%s", stdout.String())
				}
			})
		}
	}
}

func TestIndexCoversEveryCommand(t *testing.T) {
	got := index()
	for _, e := range commands {
		if !strings.Contains(got, e.cmd.Short) {
			t.Errorf("index omits %q's description %q:\n%s", e.cmd.Name, e.cmd.Short, got)
		}
	}
	if !strings.HasPrefix(got, title) {
		t.Errorf("index does not lead with the title; got %q", firstLine(got))
	}
}

func TestEveryRegistryEntryIsReachable(t *testing.T) {
	for _, e := range commands {
		name := e.cmd.Name
		// A subcommand entry (e.g. "repos clone") is typed by its alias.
		if strings.Contains(name, " ") {
			continue
		}
		if _, ok := lookup(name); !ok {
			t.Errorf("registry entry %q is not resolvable by lookup", name)
		}
	}
	for alias, target := range aliases {
		e, ok := lookup(alias)
		if !ok {
			t.Errorf("alias %q resolves to nothing", alias)
			continue
		}
		if e.cmd.Name != target {
			t.Errorf("alias %q resolved to %q; want %q", alias, e.cmd.Name, target)
		}
	}
}

func TestHelpForRouting(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"bare", nil, cli.OK},
		{"command", []string{"push"}, cli.OK},
		{"group", []string{"config"}, cli.OK},
		{"group subcommand", []string{"config", "edit"}, cli.OK},
		{"repos subcommand", []string{"repos", "clone"}, cli.OK},
		{"alias", []string{"repo"}, cli.OK},
		{"clone alias", []string{"clone"}, cli.OK},

		// An unknown name must be reported, never answered with the index and
		// a success code - a caller cannot tell it was ignored.
		{"unknown command", []string{"nonsense"}, cli.Usage},
		{"unknown subcommand", []string{"config", "nonsense"}, cli.Usage},
		{"subcommand of a leaf", []string{"push", "nonsense"}, cli.Usage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withDevNull(t, func() int { return helpFor(tt.args) })
			if got != tt.want {
				t.Errorf("helpFor(%q) = %d; want %d", tt.args, got, tt.want)
			}
		})
	}
}

// withDevNull runs fn with stdout and stderr pointed at /dev/null; these cases
// assert on the exit code, and the help text would otherwise flood the log.
func withDevNull(t *testing.T, fn func() int) int {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	outOrig, errOrig := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	defer func() { os.Stdout, os.Stderr = outOrig, errOrig }()
	return fn()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
