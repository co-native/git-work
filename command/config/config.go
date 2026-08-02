package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/co-native/git-work/internal/cli"
	cfg "github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/tui"
	"gopkg.in/yaml.v3"
)

// Run dispatches `git work config`; it prints any error to stderr and returns
// the process exit code.
//
// Help is resolved entirely here, before run() is reached. That ordering is
// the point: `path`, `list` and `edit` ignore their arguments structurally
// (runEdit takes none at all), so a help check inside run() could not work -
// `git work config edit -h` used to open $EDITOR on the config file, creating
// it if absent.
func Run(args []string) int {
	if len(args) == 0 {
		return cli.FailText(cli.Usagef("give a subcommand"), Cmd.Render())
	}

	// `config help <sub>` and `config -h` / `config --help`.
	if cli.IsHelpVerb(args[0]) {
		if len(args) == 1 {
			return cli.Help(Cmd)
		}
		sub := cli.Lookup(Cmd.Subs, args[1])
		if sub == nil {
			return cli.FailText(cli.Usagef("unknown config subcommand %q", args[1]), Cmd.Render())
		}
		return cli.Help(sub)
	}

	sub := cli.Lookup(Cmd.Subs, args[0])
	if sub == nil {
		return cli.FailText(cli.Usagef("unknown config subcommand %q", args[0]), Cmd.Render())
	}
	// `config <sub> -h`, intercepted before the subcommand can act.
	if cli.Wanted(args[1:]) {
		return cli.Help(sub)
	}
	if err := run(args, os.Stdout); err != nil {
		return cli.Fail(err, sub)
	}
	return cli.OK
}

func run(args []string, out io.Writer) error {
	sub, rest := args[0], args[1:]
	switch sub {
	case "path":
		fmt.Fprintln(out, cfg.DefaultPath())
		return nil
	case "init":
		return runInit(rest)
	case "edit":
		return runEdit()
	case "list":
		c, err := cfg.Load()
		if err != nil {
			return err
		}
		data, err := yaml.Marshal(c)
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	case "get":
		if len(rest) != 1 {
			return cli.Usagef("give exactly one <key>")
		}
		c, err := cfg.Load()
		if err != nil {
			return err
		}
		v, err := getKey(c, rest[0])
		if err != nil {
			return err
		}
		fmt.Fprintln(out, v)
		return nil
	case "set":
		if len(rest) < 2 {
			return cli.Usagef("give a <key> and at least one <value>")
		}
		c, err := cfg.Load()
		if err != nil {
			return err
		}
		if err := setKey(c, rest[0], rest[1:]); err != nil {
			return err
		}
		return c.SaveTo(cfg.DefaultPath())
	case "unset":
		if len(rest) != 1 {
			return cli.Usagef("give exactly one <key>")
		}
		c, err := cfg.Load()
		if err != nil {
			return err
		}
		if err := unsetKey(c, rest[0]); err != nil {
			return err
		}
		return c.SaveTo(cfg.DefaultPath())
	}
	return fmt.Errorf("unknown subcommand %q", sub)
}

// newInitFlagSet registers `config init`'s flags. It is separate from runInit
// so the conformance test can compare them with InitCmd's.
func newInitFlagSet(force *bool) *flag.FlagSet {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.BoolVar(force, "force", false, "overwrite an existing config file")
	return fs
}

func runInit(args []string) error {
	var force bool
	if err := newInitFlagSet(&force).Parse(args); err != nil {
		return cli.Usagef("%v", err)
	}
	path := cfg.DefaultPath()
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", path)
	}
	return cfg.Default().SaveTo(path)
}

func runEdit() error {
	if !tui.IsInteractive() {
		return fmt.Errorf("edit requires an interactive terminal")
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		return fmt.Errorf("no editor set; export $EDITOR or $VISUAL")
	}
	path := cfg.DefaultPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := cfg.Default().SaveTo(path); err != nil {
			return err
		}
	}
	cmd := exec.Command("sh", "-c", editor+" "+path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
