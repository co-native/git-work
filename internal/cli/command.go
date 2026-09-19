// Package cli holds the shared help and exit-code contract every git-work
// command implements.
//
// The contract, in full:
//
//   - `-h` and `--help` are identical and mean the same thing everywhere: print
//     this command's usage to stdout and exit 0. They are recognised anywhere in
//     the argument list, scanning stops at the first `--`.
//   - the bare word `help` is a help token only where it already occupies a
//     subcommand slot (top level, `repos`, `config`). Leaf commands take names
//     as positionals, so `git work rm help` addresses a repo called `help`.
//   - exit 0 means success (a help request is a success), 2 means the
//     invocation was malformed, 1 means the invocation was fine and the work
//     failed.
//
// A Command is the single description of a command: `main.go` and the group
// dispatchers build their index from the same values that answer `-h`, so the
// listing cannot disagree with the command.
package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Flag describes one flag for help purposes. Short and Long are given without
// dashes ("a", "all"); either may be empty. Arg is the value placeholder for a
// non-boolean flag ("<names>") and is empty for booleans.
type Flag struct {
	Short string
	Long  string
	Arg   string
	Desc  string
}

// Command describes one invocation: a top-level command, a group, or a group's
// subcommand.
type Command struct {
	// Name is the invocation without the "git work " prefix: "push", or
	// "config set" for a subcommand.
	Name string
	// Short is the one-line description used in an index listing. Lowercase,
	// no trailing period.
	Short string
	// Args is the positional sketch shown beside Name in an index listing,
	// e.g. "<repo> | --all". Empty when the command takes no positionals.
	Args string
	// Synopsis holds one or more literally typeable usage lines, each
	// including the "git work " prefix.
	Synopsis []string
	// Long is optional prose shown under the synopsis.
	Long string
	// Examples are optional literal command lines.
	Examples []string
	// Flags are this command's own flags, in the order they should be
	// listed. -h/--help is appended automatically and must not appear here.
	Flags []Flag
	// Subs are a group's subcommands. Empty for leaf commands.
	Subs []*Command
}

// helpFlag is appended to every rendered flag list, so no descriptor declares
// it and every command documents it identically.
var helpFlag = Flag{Short: "h", Long: "help", Desc: "show this help"}

// label renders a flag's left-hand column: "-a, --all", "    --main", "-n".
// Long-only flags are indented by the width of a short form so the long forms
// stay in one column.
func (f Flag) label() string {
	var b strings.Builder
	switch {
	case f.Short != "" && f.Long != "":
		fmt.Fprintf(&b, "-%s, --%s", f.Short, f.Long)
	case f.Long != "":
		fmt.Fprintf(&b, "    --%s", f.Long)
	case f.Short != "":
		fmt.Fprintf(&b, "-%s", f.Short)
	}
	if f.Arg != "" {
		fmt.Fprintf(&b, " %s", f.Arg)
	}
	return b.String()
}

// Render produces the command's full help text, without a trailing newline.
//
// Section order is fixed so that every command in the tool reads the same way:
// synopsis, prose, examples, subcommands, flags.
func (c *Command) Render() string {
	var b strings.Builder

	for i, s := range c.Synopsis {
		if i == 0 {
			fmt.Fprintf(&b, "usage: %s\n", s)
			continue
		}
		fmt.Fprintf(&b, "       %s\n", s)
	}

	if c.Long != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(c.Long, "\n"))
	}

	if len(c.Examples) > 0 {
		b.WriteString("\nexamples:\n")
		for _, e := range c.Examples {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}

	if len(c.Subs) > 0 {
		b.WriteString("\nsubcommands:\n")
		b.WriteString(indexBody(c.Subs, "  "))
	}

	flags := append(append([]Flag{}, c.Flags...), helpFlag)
	b.WriteString("\noptions:\n")
	width := 0
	for _, f := range flags {
		if n := len(f.label()); n > width {
			width = n
		}
	}
	for _, f := range flags {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, f.label(), f.Desc)
	}

	return strings.TrimRight(b.String(), "\n")
}

// SubName is the last word of a subcommand's Name, i.e. how it is typed within
// its group: "config set" -> "set".
func (c *Command) SubName() string {
	if i := strings.LastIndex(c.Name, " "); i >= 0 {
		return c.Name[i+1:]
	}
	return c.Name
}

// indexBody renders an aligned "<name> <args>   <short>" listing, one row per
// command, each line prefixed with pad.
func indexBody(cmds []*Command, pad string) string {
	type row struct{ left, short string }
	rows := make([]row, 0, len(cmds))
	width := 0
	for _, c := range cmds {
		left := c.SubName()
		if c.Args != "" {
			left += " " + c.Args
		}
		if n := len(left); n > width {
			width = n
		}
		rows = append(rows, row{left, c.Short})
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s%-*s  %s\n", pad, width, r.left, r.short)
	}
	return b.String()
}

// Index renders a top-level listing: a title line, the command table, and a
// pointer at per-command help. Used for `git work` with no arguments and for
// `git work help`.
func Index(title, usageLine string, cmds []*Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nusage: %s\n\ncommands:\n", title, usageLine)
	b.WriteString(indexBody(cmds, "  "))
	fmt.Fprintf(&b, "\nRun `%s` for a command's flags and detail.\n", "git work <command> -h")
	return strings.TrimRight(b.String(), "\n")
}

// Lookup finds a subcommand by the name a user types within the group. It
// returns nil when there is no such subcommand.
func Lookup(cmds []*Command, name string) *Command {
	for _, c := range cmds {
		if c.SubName() == name {
			return c
		}
	}
	return nil
}

// Names lists the subcommand names in sorted order, for error messages.
func Names(cmds []*Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.SubName())
	}
	sort.Strings(out)
	return out
}

// NewFlagSet returns the FlagSet every command registers its flags on. Parse
// errors are reported by Fail with the command's own usage block, so the flag
// package's own output is discarded rather than dumping a second flag table.
func NewFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// ParseScope parses the arguments of a command whose scope is `<repo> | --all`
// and whose flags are all boolean, so the single positional may appear
// anywhere on the line: leading-dash tokens are partitioned out and parsed
// against fs, which must already register --all onto all. It deliberately
// does not reject a leading `-` first argument, so `push --all` parses. The
// result is the named repo, or "" when --all was given; exactly one of the two
// is guaranteed.
func ParseScope(fs *flag.FlagSet, all *bool, args []string) (string, error) {
	var flagArgs, positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
		} else {
			positional = append(positional, a)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return "", Usagef("%v", err)
	}
	if len(positional) > 1 {
		return "", Usagef("give at most one <repo>")
	}
	switch {
	case *all && len(positional) == 1:
		return "", Usagef("give a <repo> or --all, not both")
	case !*all && len(positional) == 0:
		return "", Usagef("give a <repo> or --all")
	}
	if len(positional) == 1 {
		return positional[0], nil
	}
	return "", nil
}

// CommaList splits a comma-separated flag value into its non-empty, trimmed
// entries; an empty value yields nil.
func CommaList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// RunGroup dispatches a group command (`git work repo <sub>`, `git work config
// <sub>`): the bare help verb, `help <sub>`, an unknown subcommand, and `<sub>
// -h` are all answered here, so run only ever sees a listed subcommand and
// arguments that are not a help request. It returns the process exit code.
//
// A group holds a verb in the first slot, so the bare word "help" is
// unambiguous here in a way it never is for a leaf command's positionals.
// Help is intercepted before run, so `git work repo list -h` prints list's
// usage instead of listing repos; Wanted stops scanning at the first `--`,
// which is what keeps `repo clone -- <repo>`'s git-clone option space opaque.
func RunGroup(group *Command, args []string, run func(sub *Command, rest []string) error) int {
	if len(args) == 0 {
		return FailText(Usagef("give a subcommand"), group.Render())
	}
	name, rest := args[0], args[1:]
	unknown := func(name string) int {
		return FailText(Usagef("unknown %s subcommand %q", group.Name, name), group.Render())
	}
	if IsHelpVerb(name) {
		if len(rest) == 0 {
			return HelpText(group.Render())
		}
		sub := Lookup(group.Subs, rest[0])
		if sub == nil {
			return unknown(rest[0])
		}
		return Help(sub)
	}
	sub := Lookup(group.Subs, name)
	if sub == nil {
		return unknown(name)
	}
	if Wanted(rest) {
		return Help(sub)
	}
	if err := run(sub, rest); err != nil {
		return Fail(err, sub)
	}
	return OK
}
