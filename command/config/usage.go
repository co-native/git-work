package config

import "github.com/co-native/git-work/internal/cli"

// Cmd describes the `git work config` group. Subs is the single source for both
// the group's "subcommands:" listing and `git work config <sub> -h`.
var Cmd = &cli.Command{
	Name:     "config",
	Short:    "view/edit the global configuration",
	Args:     "<subcommand>",
	Synopsis: []string{"git work config <subcommand> [args]"},
	Long: "Views and edits the global config at ${XDG_CONFIG_HOME:-~/.config}/git-work/config.yaml.\n" +
		"A missing file means built-in defaults are used, which is not an error.\n" +
		"\n" +
		"keys: paths.repos, paths.work, defaults.integration,\n" +
		"      repos.<name>.{integration,add_by_default},\n" +
		"      providers.<name>.{type,default,folder_case,branch_case,patterns}",
	Subs: []*cli.Command{ListCmd, GetCmd, SetCmd, UnsetCmd, EditCmd, PathCmd, InitCmd},
}

var ListCmd = &cli.Command{
	Name:     "config list",
	Short:    "print the effective config as YAML",
	Synopsis: []string{"git work config list"},
	Long:     "Prints the effective configuration - defaults merged with the config file.",
}

var GetCmd = &cli.Command{
	Name:     "config get",
	Short:    "print one value",
	Args:     "<key>",
	Synopsis: []string{"git work config get <key>"},
	Long:     "Prints a single value. <key> is a dot-path, e.g. paths.work.",
	Examples: []string{
		"git work config get paths.work",
		"git work config get providers.github.patterns",
	},
}

var SetCmd = &cli.Command{
	Name:     "config set",
	Short:    "set value(s)",
	Args:     "<key> <value>...",
	Synopsis: []string{"git work config set <key> <value>..."},
	Long: "Sets a value and writes the config file. List-valued keys take several\n" +
		"values. Provider patterns use <prefix>=<owner/repo> for GitHub, or a bare\n" +
		"<prefix> for Jira.",
	Examples: []string{
		"git work config set paths.work ~/work",
		"git work config set providers.github.patterns dev-tools=acme/api",
	},
}

var UnsetCmd = &cli.Command{
	Name:     "config unset",
	Short:    "clear a scalar / drop a provider or field",
	Args:     "<key>",
	Synopsis: []string{"git work config unset <key>"},
	Long:     "Clears a scalar back to its default, or removes a provider or provider field.",
}

var EditCmd = &cli.Command{
	Name:     "config edit",
	Short:    "open the config in $EDITOR (creates defaults if absent)",
	Synopsis: []string{"git work config edit"},
	Long: "Opens the config file in $VISUAL or $EDITOR, writing a defaults file first\n" +
		"if none exists. Requires an interactive terminal.",
}

var PathCmd = &cli.Command{
	Name:     "config path",
	Short:    "print the config file path",
	Synopsis: []string{"git work config path"},
	Long:     "Prints the path the other subcommands read and write, whether or not it exists.",
}

var InitCmd = &cli.Command{
	Name:     "config init",
	Short:    "write a defaults config file",
	Args:     "[--force]",
	Synopsis: []string{"git work config init [--force]"},
	Long:     "Writes a config file containing the built-in defaults.",
	Flags: []cli.Flag{
		{Long: "force", Desc: "overwrite an existing config file"},
	},
}
