package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/cli/clitest"
)

func TestHelpConformance(t *testing.T) {
	subs := []*cli.Command{ListCmd, GetCmd, SetCmd, UnsetCmd, EditCmd, PathCmd, InitCmd}
	clitest.CheckGroup(t, Cmd, subs)

	for _, c := range subs {
		t.Run(c.Name, func(t *testing.T) {
			// init is the only config subcommand with flags of its own.
			if c == InitCmd {
				var force bool
				clitest.Check(t, c, newInitFlagSet(&force))
				return
			}
			clitest.Check(t, c, nil)
		})
	}
}

// The subcommands that ignore their arguments structurally are the reason help
// is resolved in Run rather than in run: `config edit` takes no parameters at
// all, so before this it opened $EDITOR on the real config - creating it if
// absent - when asked for help.
func TestHelpNeverRunsTheSubcommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows and HOME elsewhere.
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// If `edit` ever ran, this would be the process it launched.
	t.Setenv("EDITOR", "false")
	t.Setenv("VISUAL", "")

	for _, sub := range []string{"list", "get", "set", "unset", "edit", "path", "init"} {
		for _, flag := range []string{"-h", "--help"} {
			t.Run(sub+" "+flag, func(t *testing.T) {
				out, code := captureRun(t, []string{sub, flag})
				if code != cli.OK {
					t.Errorf("Run(%s %s) = %d; want %d", sub, flag, code, cli.OK)
				}
				want := "usage: git work config " + sub
				if !strings.Contains(out, want) {
					t.Errorf("help for %q did not print %q; got:\n%s", sub, want, out)
				}
			})
		}
	}

	// No subcommand may have written a config file along the way.
	if _, err := os.Stat(filepath.Join(home, ".config", "git-work", "config.yaml")); err == nil {
		t.Error("a help request created the config file; help must never write to disk")
	}
}

func TestHelpVerbRouting(t *testing.T) {
	t.Run("bare group help", func(t *testing.T) {
		out, code := captureRun(t, []string{"help"})
		if code != cli.OK {
			t.Errorf("Run(help) = %d; want 0", code)
		}
		for _, want := range []string{"usage: git work config", "subcommands:", "edit"} {
			if !strings.Contains(out, want) {
				t.Errorf("group help missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("help names a subcommand", func(t *testing.T) {
		out, code := captureRun(t, []string{"help", "set"})
		if code != cli.OK {
			t.Errorf("Run(help set) = %d; want 0", code)
		}
		if !strings.Contains(out, "usage: git work config set") {
			t.Errorf("`config help set` did not print set's usage:\n%s", out)
		}
	})

	t.Run("unknown names are rejected, not silently ignored", func(t *testing.T) {
		if _, code := captureRun(t, []string{"help", "nonsense"}); code != cli.Usage {
			t.Errorf("Run(help nonsense) = %d; want %d", code, cli.Usage)
		}
		if _, code := captureRun(t, []string{"nonsense"}); code != cli.Usage {
			t.Errorf("Run(nonsense) = %d; want %d", code, cli.Usage)
		}
	})

	t.Run("no subcommand is a usage error", func(t *testing.T) {
		if _, code := captureRun(t, nil); code != cli.Usage {
			t.Errorf("Run() = %d; want %d", code, cli.Usage)
		}
	})
}

// captureRun runs the command with stdout redirected, returning what it wrote.
func captureRun(t *testing.T, args []string) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	code := Run(args)
	os.Stdout = orig
	w.Close()

	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	r.Close()
	return sb.String(), code
}
