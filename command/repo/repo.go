// Package repo implements the `git work repo` command group: repo-level
// operations on the primary clones under the repos root. The internal git
// helpers are imported as gitrepo here, mirroring how command/config aliases
// internal/config - the collision is with the directory of many repos
// (paths.repos), which stays plural because that is what it names.
package repo

import (
	"github.com/co-native/git-work/internal/cli"
)

// Cmd describes the `git work repo` group for the help system. main.go builds
// its index row from Short, and the group's own listing is rendered from Subs -
// so there is no second copy of the subcommand table to drift.
var Cmd = &cli.Command{
	Name:     "repo",
	Short:    `manage primary repos (clone, adopt, list, pull); "repos" also works`,
	Args:     "<subcommand>",
	Synopsis: []string{"git work repo <subcommand> [args]"},
	Long: "Repo-level operations on the primary clones under the repos root. Day-to-day\n" +
		"commands never mutate the repo layout as a side effect: `repo adopt` is the\n" +
		"single normalizer for layout drift, and `repo list` reports drift without\n" +
		"fixing it.",
	Subs: []*cli.Command{CloneCmd, AdoptCmd, ListCmd, PullCmd},
}

// runners maps the word a user types to the implementation behind it.
// Cmd.Subs answers "what is a subcommand" for help and dispatch alike; this
// answers "what does it do". The package's tests assert the two agree.
var runners = map[string]func([]string) error{
	"clone": runClone,
	"adopt": runAdopt,
	"list":  runList,
	"pull":  runPull,
}

// Run dispatches `git work repo <subcommand>`; it prints any error to stderr
// and returns the process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		return cli.FailText(cli.Usagef("give a subcommand"), Cmd.Render())
	}
	name, rest := args[0], args[1:]

	// A group holds a verb in the first slot, so the bare word "help" is
	// unambiguous here in a way it never is for a leaf command's positionals.
	if cli.IsHelpVerb(name) {
		if len(rest) == 0 {
			return cli.HelpText(Cmd.Render())
		}
		sub := cli.Lookup(Cmd.Subs, rest[0])
		if sub == nil {
			return cli.FailText(cli.Usagef("unknown repo subcommand %q", rest[0]), Cmd.Render())
		}
		return cli.Help(sub)
	}

	sub := cli.Lookup(Cmd.Subs, name)
	if sub == nil {
		return cli.FailText(cli.Usagef("unknown repo subcommand %q", name), Cmd.Render())
	}
	// Help is intercepted before the subcommand's run function, so
	// `git work repo list -h` prints list's usage instead of listing repos.
	// cli.Wanted stops scanning at the first `--`, which is what keeps
	// `repo clone -- <repo>`'s git-clone option space opaque.
	if cli.Wanted(rest) {
		return cli.Help(sub)
	}
	if err := runners[name](rest); err != nil {
		return cli.Fail(err, sub)
	}
	return cli.OK
}
