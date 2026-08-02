package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestWanted(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"short", []string{"-h"}, true},
		{"long", []string{"--help"}, true},
		{"after positional", []string{"myrepo", "-h"}, true},
		{"after flags", []string{"-a", "-n", "--help"}, true},
		{"no help", []string{"-a", "myrepo"}, false},

		// The bare word is deliberately not a help token: leaf commands take
		// repo/folder/ticket names positionally, and a repo really can be
		// called "help".
		{"bare help word", []string{"help"}, false},
		{"help as positional", []string{"help"}, false},

		// ...nor is it one as a flag *value*: `--repos help` is two tokens.
		{"help as flag value", []string{"--repos", "help"}, false},
		{"help as dir value", []string{"--dir", "help", "TICKET-1"}, false},

		// Scanning stops at `--` so `git work git demo -- log -h` forwards -h
		// to git rather than answering it.
		{"after separator", []string{"demo", "--", "log", "-h"}, false},
		{"separator alone", []string{"demo", "--", "-h"}, false},
		{"before separator", []string{"-h", "--", "status"}, true},
		{"separator with no help", []string{"demo", "--", "status"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Wanted(tt.args); got != tt.want {
				t.Errorf("Wanted(%q) = %v; want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestIsHelpVerb(t *testing.T) {
	for _, s := range []string{"-h", "--help", "help"} {
		if !IsHelpVerb(s) {
			t.Errorf("IsHelpVerb(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"list", "clone", "", "helper"} {
		if IsHelpVerb(s) {
			t.Errorf("IsHelpVerb(%q) = true; want false", s)
		}
	}
}

var sample = &Command{
	Name:     "push",
	Short:    "push work branches to origin",
	Args:     "<repo> | --all",
	Synopsis: []string{"git work push <repo> [options]", "git work push -a|--all [options]"},
	Long:     "Pushes work branches with un-integrated commits.",
	Flags: []Flag{
		{Short: "a", Long: "all", Desc: "push every repo in the work folder"},
		{Long: "main", Desc: "push each repo's default branch instead"},
		{Short: "n", Long: "dry-run", Desc: "show what would be pushed"},
		{Long: "repos", Arg: "<names>", Desc: "comma-separated repo names"},
	},
}

func TestRenderShape(t *testing.T) {
	got := sample.Render()

	if !strings.HasPrefix(got, "usage: git work push <repo> [options]\n") {
		t.Errorf("first line = %q; want the first synopsis prefixed with \"usage: \"", firstLine(got))
	}
	// Continuation synopsis lines align under the first.
	if !strings.Contains(got, "\n       git work push -a|--all [options]\n") {
		t.Errorf("render missing aligned second synopsis line:\n%s", got)
	}
	// -h is appended by the renderer, never declared by a descriptor.
	if !strings.Contains(got, "-h, --help") {
		t.Errorf("render does not document -h/--help:\n%s", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Error("Render() should not end with a newline")
	}
	for _, want := range []string{"options:", "Pushes work branches"} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q:\n%s", want, got)
		}
	}
}

// Every flag description must start in the same column, including the
// automatically appended -h/--help row and the long-only and value-taking
// flags whose labels are shaped differently.
func TestRenderFlagAlignment(t *testing.T) {
	got := sample.Render()
	descs := []string{
		"push every repo in the work folder",
		"push each repo's default branch instead",
		"show what would be pushed",
		"comma-separated repo names",
		"show this help",
	}
	col := -1
	for _, d := range descs {
		line := lineContaining(t, got, d)
		at := strings.Index(line, d)
		if col == -1 {
			col = at
			continue
		}
		if at != col {
			t.Errorf("description %q starts at column %d; want %d:\n%s", d, at, col, got)
		}
	}
}

func lineContaining(t *testing.T, s, want string) string {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", want, s)
	return ""
}

func TestFlagLabel(t *testing.T) {
	tests := []struct {
		flag Flag
		want string
	}{
		{Flag{Short: "a", Long: "all"}, "-a, --all"},
		{Flag{Long: "main"}, "    --main"},
		{Flag{Short: "n"}, "-n"},
		{Flag{Long: "repos", Arg: "<names>"}, "    --repos <names>"},
	}
	for _, tt := range tests {
		if got := tt.flag.label(); got != tt.want {
			t.Errorf("label(%+v) = %q; want %q", tt.flag, got, tt.want)
		}
	}
}

func TestUsageErrorRoundTrip(t *testing.T) {
	err := Usagef("give a <repo> or --all, not both")
	if !IsUsage(err) {
		t.Error("IsUsage(Usagef(...)) = false; want true")
	}
	if IsUsage(errors.New("boom")) {
		t.Error("IsUsage(plain error) = true; want false")
	}
	// Wrapped usage errors keep their identity, so a command may add context.
	if !IsUsage(errWrap(err)) {
		t.Error("IsUsage(wrapped) = false; want true")
	}
}

func errWrap(err error) error { return errors.Join(err) }

func TestFailExitCodes(t *testing.T) {
	t.Run("usage error prints help and exits 2", func(t *testing.T) {
		var buf bytes.Buffer
		code := failTo(&buf, Usagef("give a <repo>"), sample.Render())
		if code != Usage {
			t.Errorf("code = %d; want %d", code, Usage)
		}
		out := buf.String()
		if !strings.HasPrefix(out, "git-work: give a <repo>\n") {
			t.Errorf("output does not lead with the error:\n%s", out)
		}
		if !strings.Contains(out, "usage: git work push") {
			t.Errorf("usage error did not print the usage block:\n%s", out)
		}
	})

	t.Run("operation failure exits 1 without help", func(t *testing.T) {
		var buf bytes.Buffer
		code := failTo(&buf, errors.New("2 repo(s) failed"), sample.Render())
		if code != Failure {
			t.Errorf("code = %d; want %d", code, Failure)
		}
		if strings.Contains(buf.String(), "usage:") {
			t.Errorf("operation failure should not print usage:\n%s", buf.String())
		}
	})
}

func TestHelpGoesToStdoutAndSucceeds(t *testing.T) {
	var buf bytes.Buffer
	if code := helpTo(&buf, sample.Render()); code != OK {
		t.Errorf("code = %d; want %d", code, OK)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("help output should end with exactly one newline")
	}
}

func TestSubcommandHelpers(t *testing.T) {
	subs := []*Command{
		{Name: "config set", Short: "set value(s)"},
		{Name: "config get", Short: "print one value"},
		{Name: "config edit", Short: "open the config in $EDITOR"},
	}
	if got := Lookup(subs, "get"); got == nil || got.Name != "config get" {
		t.Errorf("Lookup(get) = %v; want the config get command", got)
	}
	if got := Lookup(subs, "nope"); got != nil {
		t.Errorf("Lookup(nope) = %v; want nil", got)
	}
	want := []string{"edit", "get", "set"}
	got := Names(subs)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Names() = %v; want %v (sorted)", got, want)
	}
}

func TestIndexListsEveryCommand(t *testing.T) {
	cmds := []*Command{
		{Name: "new", Short: "start a new work folder", Args: "<ticket-id>"},
		{Name: "rm", Short: "remove one repo", Args: "<repo>"},
	}
	got := Index("git-work - multi-repo worktree manager", "git work <command> [args]", cmds)
	for _, want := range []string{"new <ticket-id>", "rm <repo>", "start a new work folder", "commands:"} {
		if !strings.Contains(got, want) {
			t.Errorf("index missing %q:\n%s", want, got)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
