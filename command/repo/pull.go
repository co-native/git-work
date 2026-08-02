package repo

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	gitrepo "github.com/co-native/git-work/internal/repo"
)

// PullCmd describes `git work repo pull`.
var PullCmd = &cli.Command{
	Name:     "repo pull",
	Short:    "ff-only-pull every managed repo's primary clone",
	Synopsis: []string{"git work repo pull"},
	Long: "Fast-forward-pulls every managed repo's default branch in its primary clone.\n" +
		"The default branch is pulled explicitly, so a primary left on another branch\n" +
		"still gets its default branch freshened without its working tree being\n" +
		"touched. A pull that would need a merge or rebase fails for that repo alone;\n" +
		"failures are reported per repo and make the command exit nonzero at the end.",
}

// runPull executes `git work repo pull`.
func runPull(args []string) error {
	if len(args) > 0 {
		return cli.Usagef("repo pull takes no arguments")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	l, err := cfg.Layout()
	if err != nil {
		return err
	}
	names, err := l.ListRepos()
	if err != nil {
		return err
	}
	return pullAll(os.Stdout, l, names)
}

// pullAll ff-only-pulls every named repo's default branch in its primary
// clone (explicitly, so a primary left on another branch still freshens the
// default branch), printing one result line per repo. A failed pull
// (diverged branch, offline, layout problems) is reported inline and does
// not stop the remaining pulls; any failure makes the whole command fail
// after the loop.
func pullAll(w io.Writer, l layout.Layout, names []string) error {
	if len(names) == 0 {
		fmt.Fprintln(w, "no managed repos")
		return nil
	}
	var failed []string
	for _, name := range names {
		main, err := l.RepoMain(name)
		if err != nil {
			fmt.Fprintf(w, "%s: %v\n", name, err)
			failed = append(failed, name)
			continue
		}
		def, err := gitrepo.DefaultBranchName(main)
		if err != nil {
			fmt.Fprintf(w, "%s: %v\n", name, err)
			failed = append(failed, name)
			continue
		}
		res, err := gitrepo.PullBranchFFOnly(main, def)
		if err != nil {
			fmt.Fprintf(w, "%s: %v\n", name, err)
			failed = append(failed, name)
			continue
		}
		fmt.Fprintf(w, "%s: %s\n", name, res)
	}
	if len(failed) > 0 {
		return fmt.Errorf("pull failed for: %s", strings.Join(failed, ", "))
	}
	return nil
}
