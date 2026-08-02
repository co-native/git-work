// Package clitest holds the conformance check every git-work command's tests
// run against their descriptor.
//
// It exists so "uniform help" is enforced by CI rather than by review: a
// command whose descriptor is missing, malformed, or out of step with its real
// flags fails its own package's tests. Each command package contributes a
// few-line test that hands over its descriptor and a freshly built FlagSet;
// the FlagSet has to come from the package because that is where it is
// constructed.
package clitest

import (
	"flag"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/co-native/git-work/internal/cli"
)

// Check asserts that cmd is a well-formed descriptor and that it documents
// exactly the flags fs registers. Pass a nil fs for a command that parses no
// flags of its own.
func Check(t *testing.T, cmd *cli.Command, fs *flag.FlagSet) {
	t.Helper()
	checkDescriptor(t, cmd)
	checkFlagsMatch(t, cmd, fs)
	checkRenders(t, cmd)
}

func checkDescriptor(t *testing.T, cmd *cli.Command) {
	t.Helper()
	if cmd == nil {
		t.Fatal("descriptor is nil: every command must declare one")
	}
	if cmd.Name == "" {
		t.Error("Name is empty")
	}
	if cmd.Short == "" {
		t.Error("Short is empty: it is the command's row in the index listing")
	}
	if strings.HasSuffix(cmd.Short, ".") {
		t.Errorf("Short = %q; index rows take no trailing period", cmd.Short)
	}
	if r := []rune(cmd.Short); len(r) > 0 && unicode.IsUpper(r[0]) {
		t.Errorf("Short = %q; index rows start lowercase", cmd.Short)
	}
	if n := len(cmd.Short); n > 70 {
		t.Errorf("Short is %d chars; keep index rows under 70 so the table stays narrow", n)
	}
	if len(cmd.Synopsis) == 0 {
		t.Error("Synopsis is empty: help must show at least one typeable usage line")
	}
	for _, s := range cmd.Synopsis {
		if !strings.HasPrefix(s, "git work ") {
			t.Errorf("Synopsis line %q must be literally typeable, i.e. start with \"git work \"", s)
		}
	}
	for _, f := range cmd.Flags {
		if f.Short == "h" || f.Long == "help" {
			t.Error("descriptor declares -h/--help; the renderer appends it so every command documents it identically")
		}
		if f.Short == "" && f.Long == "" {
			t.Error("a flag declares neither a short nor a long form")
		}
		if f.Desc == "" {
			t.Errorf("flag %q has no description", f.Short+f.Long)
		}
		if strings.HasSuffix(f.Desc, ".") {
			t.Errorf("flag %q description %q takes no trailing period", f.Short+f.Long, f.Desc)
		}
	}
}

// checkFlagsMatch is the check that keeps documentation and behaviour in step:
// a flag added to the FlagSet without a descriptor entry (or removed from one
// but not the other) fails here.
func checkFlagsMatch(t *testing.T, cmd *cli.Command, fs *flag.FlagSet) {
	t.Helper()

	registered := map[string]bool{}
	if fs != nil {
		fs.VisitAll(func(f *flag.Flag) {
			// -h/--help is centralised: a command may still register it on the
			// FlagSet without documenting it, but nothing should any more.
			if f.Name == "h" || f.Name == "help" {
				t.Errorf("FlagSet registers %q; help is handled centrally by cli.Wanted before parsing", f.Name)
				return
			}
			registered[f.Name] = true
		})
	}

	documented := map[string]bool{}
	for _, f := range cmd.Flags {
		for _, name := range []string{f.Short, f.Long} {
			if name == "" {
				continue
			}
			if documented[name] {
				t.Errorf("flag %q is documented twice", name)
			}
			documented[name] = true
			if !registered[name] {
				t.Errorf("descriptor documents %q but no flag by that name is registered", name)
			}
		}
	}

	var undocumented []string
	for name := range registered {
		if !documented[name] {
			undocumented = append(undocumented, name)
		}
	}
	sort.Strings(undocumented)
	for _, name := range undocumented {
		t.Errorf("flag %q is registered but undocumented: add it to the descriptor", name)
	}
}

func checkRenders(t *testing.T, cmd *cli.Command) {
	t.Helper()
	out := cmd.Render()
	if !strings.HasPrefix(out, "usage: ") {
		t.Errorf("Render() must lead with a usage line; got:\n%s", out)
	}
	if !strings.Contains(out, "-h, --help") {
		t.Errorf("Render() must document -h/--help; got:\n%s", out)
	}
}

// CheckGroup asserts a group descriptor lists subcommands and that each is
// itself well-formed.
func CheckGroup(t *testing.T, cmd *cli.Command, subs []*cli.Command) {
	t.Helper()
	Check(t, cmd, nil)
	if len(cmd.Subs) == 0 {
		t.Error("group descriptor lists no subcommands")
	}
	if len(cmd.Subs) != len(subs) {
		t.Errorf("group lists %d subcommands but %d are dispatchable", len(cmd.Subs), len(subs))
	}
	for _, s := range subs {
		if cli.Lookup(cmd.Subs, lastWord(s.Name)) == nil {
			t.Errorf("subcommand %q is dispatchable but absent from the group's listing", s.Name)
		}
	}
}

func lastWord(s string) string {
	if i := strings.LastIndex(s, " "); i >= 0 {
		return s[i+1:]
	}
	return s
}
