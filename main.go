package main

import (
	"fmt"
	"os"

	"github.com/co-native/git-work/command/add"
	"github.com/co-native/git-work/command/adopt"
	configcmd "github.com/co-native/git-work/command/config"
	"github.com/co-native/git-work/command/done"
	"github.com/co-native/git-work/command/gitcmd"
	"github.com/co-native/git-work/command/integrate"
	"github.com/co-native/git-work/command/newcmd"
	"github.com/co-native/git-work/command/push"
	"github.com/co-native/git-work/command/rebase"
	"github.com/co-native/git-work/command/refresh"
	repocmd "github.com/co-native/git-work/command/repo"
	"github.com/co-native/git-work/command/rm"
	"github.com/co-native/git-work/command/version"
	"github.com/co-native/git-work/internal/cli"
)

const title = "git-work - multi-repo worktree manager"

// entry pairs a command's description with the function that runs it. The
// description is the same value the command's own -h prints, so the index
// below cannot drift from the commands it lists.
type entry struct {
	cmd *cli.Command
	run func([]string) int
}

// commands is the dispatch table, in the order they appear in the index.
var commands = []entry{
	{repocmd.Cmd, repocmd.Run},
	{repocmd.CloneCmd, func(args []string) int { return repocmd.Run(append([]string{"clone"}, args...)) }},
	{newcmd.Cmd, newcmd.Run},
	{add.Cmd, add.Run},
	{adopt.Cmd, adopt.Run},
	{refresh.Cmd, refresh.Run},
	{rebase.Cmd, rebase.Run},
	{push.Cmd, push.Run},
	{integrate.Cmd, integrate.Run},
	{gitcmd.Cmd, gitcmd.Run},
	{done.Cmd, done.Run},
	{rm.Cmd, rm.Run},
	{configcmd.Cmd, configcmd.Run},
	{version.Cmd, version.Run},
}

// aliases map a typed name to the canonical command it resolves to. "repos"
// is a spelling of "repo" - the canonical names are singular verbs over a
// collection, as git's own are (`git remote`, `git branch`). Top-level "clone"
// is a shorthand for "repo clone". Aliases deliberately do not appear in the
// index - it lists commands, not every way to type one.
var aliases = map[string]string{
	"repos": "repo",
	"clone": "repo clone",
}

// index renders the top-level listing. Its rows come from each command's own
// descriptor, so a command cannot be described one way here and another way by
// its own help.
func index() string {
	cmds := make([]*cli.Command, 0, len(commands))
	for _, e := range commands {
		cmds = append(cmds, e.cmd)
	}
	return cli.Index(title, "git work <command> [args]", cmds)
}

// lookup resolves a typed command name to its entry.
func lookup(name string) (entry, bool) {
	if target, ok := aliases[name]; ok {
		name = target
	}
	for _, e := range commands {
		if e.cmd.Name == name {
			return e, true
		}
	}
	return entry{}, false
}

// helpFor answers `git work help <args>`: the index with no arguments, a
// command's usage with one, and a group subcommand's usage with two. An
// unrecognised name is an error rather than a silent fallback to the index -
// answering the wrong question with exit 0 is worse than saying so.
func helpFor(args []string) int {
	if len(args) == 0 {
		return cli.HelpText(index())
	}
	e, ok := lookup(args[0])
	if !ok {
		return cli.FailText(cli.Usagef("unknown command %q", args[0]), index())
	}
	if len(args) == 1 {
		return cli.Help(e.cmd)
	}
	sub := cli.Lookup(e.cmd.Subs, args[1])
	if sub == nil {
		if len(e.cmd.Subs) == 0 {
			return cli.FailText(cli.Usagef("%s has no subcommands", args[0]), e.cmd.Render())
		}
		return cli.FailText(cli.Usagef("unknown %s subcommand %q", args[0], args[1]), e.cmd.Render())
	}
	return cli.Help(sub)
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(cli.FailText(cli.Usagef("give a command"), index()))
	}
	name, rest := os.Args[1], os.Args[2:]

	if cli.IsHelpVerb(name) {
		os.Exit(helpFor(rest))
	}
	// `--version` is the spelling people reach for; the `version` command is
	// the one that appears in the index and answers -h like every other
	// command. Both print the same line. Not -v, which is worth reserving.
	if name == "--version" {
		os.Exit(version.Run(nil))
	}
	e, ok := lookup(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "git-work: unknown command %q\n\n%s\n", name, index())
		os.Exit(cli.Usage)
	}
	os.Exit(e.run(rest))
}
