package done

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/state"
	teardownpkg "github.com/co-native/git-work/internal/teardown"
	"github.com/co-native/git-work/internal/tui"
)

type options struct {
	dir            string
	deleteLocal    bool
	deleteRemote   bool
	force          bool
	removeAll      bool
	noFetch        bool
	nonInteractive bool
}

// Cmd describes `git work done` for the help system. main.go builds its index
// row from Short and answers `git work help done` from the same value that
// `git work done -h` prints.
var Cmd = &cli.Command{
	Name:     "done",
	Short:    "tear down a work folder",
	Args:     "<dir>",
	Synopsis: []string{"git work done <dir> [options]"},
	Long: "Tears down a work folder: removes each repo's worktree, deletes branches when\n" +
		"asked, then removes the folder. <dir> is a bare name resolved under the work\n" +
		"root, or a path. Checks and teardown run per repo, failures are collected at\n" +
		"the end, and a re-run resumes exactly the repos still outstanding. --force\n" +
		"overrides safety-check problems (uncommitted, unpushed, unmerged) but never\n" +
		"deletes files git-work did not create - that is --remove-all, which --force\n" +
		"does NOT imply; without it such a folder is kept and the run still succeeds.",
	Examples: []string{
		"git work done PROJ-123-add-auth --delete-local --delete-remote",
		"git work done ~/dev/work/PROJ-123-add-auth --non-interactive --remove-all",
	},
	Flags: []cli.Flag{
		{Long: "delete-local", Desc: "delete each repo's local branch (prompted for interactively)"},
		{Long: "delete-remote", Desc: "delete each repo's remote branch (prompted for interactively)"},
		{Long: "force", Desc: "override safety-check problems; does not imply --remove-all"},
		{Long: "remove-all", Desc: "remove the folder even when it holds files git-work did not create"},
		{Long: "no-fetch", Desc: "skip fetching before the merged check"},
		{Long: "non-interactive", Desc: "skip the prompts and take the flags as given"},
	},
}

// newFlagSet registers done's flags against o. It is separate from parseFlags
// so the conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("done", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.deleteLocal, "delete-local", false, "delete local branches")
	fs.BoolVar(&o.deleteRemote, "delete-remote", false, "delete remote branches")
	fs.BoolVar(&o.force, "force", false, "override safety-check problems")
	fs.BoolVar(&o.removeAll, "remove-all", false, "delete the work folder even when it contains files git-work did not create (--force does NOT imply this)")
	fs.BoolVar(&o.noFetch, "no-fetch", false, "skip fetching before the merged check")
	fs.BoolVar(&o.nonInteractive, "non-interactive", false, "skip the TUI; use flags")
	return fs
}

// parseFlags parses `git work done` arguments. <dir> is mandatory and must come
// first: a leading-dash first argument means the operator named no folder.
//
// Help is handled by Run before this is reached, so -h/--help never appears
// here.
func parseFlags(args []string) (*options, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, cli.Usagef("give a <dir> to tear down")
	}
	o := &options{dir: args[0]}
	fs := newFlagSet(o)
	if err := fs.Parse(args[1:]); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	return o, nil
}

// Run executes `git work done`; it prints any error to stderr and returns
// the process exit code.
func Run(args []string) int {
	if cli.Wanted(args) {
		return cli.Help(Cmd)
	}
	if err := run(args); err != nil {
		return cli.Fail(err, Cmd)
	}
	return cli.OK
}

func run(args []string) error {
	o, err := parseFlags(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	l, err := cfg.Layout()
	if err != nil {
		return err
	}

	// Resolve work dir: a bare name (no path separator) resolves under
	// WorkRoot; anything containing a separator is treated as a path.
	var workDir string
	if containsPathSeparator(o.dir) || filepath.IsAbs(o.dir) {
		workDir = o.dir
	} else {
		workDir = l.WorkDir(o.dir)
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return err
	}
	st, err := state.Load(workDir)
	if err != nil {
		return fmt.Errorf("load state from %s: %w", workDir, err)
	}

	// Compute the extra (non-git-work) files at the work-folder root UP FRONT,
	// while the repo worktrees still exist so the walk can skip them cheaply.
	// This drives the leftover decision below and the end-of-run folder removal.
	extra, err := extraFiles(workDir, st)
	if err != nil {
		return err
	}

	// Interactive: ask what to do.
	if !o.nonInteractive {
		if !tui.IsInteractive() {
			return tui.ErrNoTTY
		}
		if o.deleteLocal, err = tui.Confirm("Delete local branches?", false); err != nil {
			return err
		}
		if o.deleteRemote, err = tui.Confirm("Delete remote branches?", false); err != nil {
			return err
		}
	}

	// Decide what to do with the extra files. Default: remove the whole folder
	// (nothing extra, or an explicit --remove-all/Delete). Keep leaves the
	// folder + honest state; Cancel aborts before any checks or teardown.
	decision := decideRemove
	if len(extra) > 0 && !o.removeAll {
		if o.nonInteractive {
			// Non-interactive: absence of --remove-all means Keep.
			decision = decideKeep
		} else {
			fmt.Printf("Work folder %s contains files git-work did not create:\n  %s\n",
				workDir, strings.Join(extra, "\n  "))
			choice, err := tui.Select("What should done do with these files?",
				[]string{leftoverDelete, leftoverKeep, leftoverCancel})
			if err != nil {
				return err
			}
			switch choice {
			case leftoverDelete:
				decision = decideRemove
			case leftoverKeep:
				decision = decideKeep
			case leftoverCancel:
				fmt.Printf("Cancelled; %s left untouched.\n", workDir)
				return nil
			}
		}
	}

	problems := runChecks(o, l, st, workDir)
	if len(problems) > 0 {
		if !o.force {
			return fmt.Errorf("aborting (most of these can be overridden with --force):\n  %s", strings.Join(problems, "\n  "))
		}
		fmt.Fprintf(os.Stderr, "git-work: proceeding despite %d problem(s) (--force)\n", len(problems))
	}

	// Capture the count before teardown: TeardownRepo shrinks st.Repos as
	// each repo is torn down, so len(st.Repos) is zero (or the not-torn-down
	// remainder) by the time the final report prints.
	repoCount := len(st.Repos)
	failures := teardown(o, l, st, workDir)
	if len(failures) > 0 {
		return fmt.Errorf("teardown finished with %d failure(s) (work folder kept: %s):\n  %s",
			len(failures), workDir, strings.Join(failures, "\n  "))
	}

	// Keep: leave the folder with the extra files and the now-honest
	// (torn-down repos removed) state; the run still succeeds.
	if decision == decideKeep {
		fmt.Printf("Tore down %d repo(s); kept %s (contains files git-work did not create):\n  %s\n",
			repoCount, workDir, strings.Join(extra, "\n  "))
		return nil
	}
	if err := os.RemoveAll(workDir); err != nil {
		return err
	}
	fmt.Printf("Closed out %s (%d repo(s))\n", workDir, repoCount)
	return nil
}

// containsPathSeparator reports whether s holds a path separator for this OS.
//
// Not `strings.ContainsRune(s, filepath.Separator)`: on Windows that only
// matches `\`, so `git work done proj/PROJ-1` - the spelling a Windows operator
// typing a path is most likely to use - would be mistaken for a bare folder
// name and resolved under the work root. os.IsPathSeparator accepts both there
// while still treating a backslash as an ordinary filename character on Unix.
func containsPathSeparator(s string) bool {
	for i := 0; i < len(s); i++ {
		if os.IsPathSeparator(s[i]) {
			return true
		}
	}
	return false
}

// tdOptions maps done's flags onto the shared teardown package's Options.
func tdOptions(o *options) teardownpkg.Options {
	return teardownpkg.Options{
		DeleteLocal:  o.deleteLocal,
		DeleteRemote: o.deleteRemote,
		Force:        o.force,
		NoFetch:      o.noFetch,
	}
}

// runChecks aggregates the shared per-repo safety checks across every repo in
// the work folder. An error while running a check is a warning plus a counted
// problem (conservative), never a mid-loop abort; --force overrides problems.
func runChecks(o *options, l layout.Layout, st *state.State, workDir string) []string {
	to := tdOptions(o)
	var problems []string
	for _, r := range st.Repos {
		problems = append(problems, teardownpkg.CheckRepo(to, l, r, workDir)...)
	}
	return problems
}

// teardown tears down every repo via the shared per-repo teardown, aggregating
// failures for an end-of-run report. TeardownRepo drops each fully-torn-down
// repo from st (persisting honest state), so iterate a snapshot of st.Repos.
func teardown(o *options, l layout.Layout, st *state.State, workDir string) []string {
	to := tdOptions(o)
	var failures []string
	for _, r := range append([]state.Repo(nil), st.Repos...) {
		failures = append(failures, teardownpkg.TeardownRepo(to, l, st, r, workDir)...)
	}
	return failures
}

// leftoverDecision selects what happens to the work folder at the end of a run
// when it holds files git-work did not create.
type leftoverDecision int

const (
	decideRemove leftoverDecision = iota // remove the whole folder, extra files included
	decideKeep                           // leave the folder + honest state
	// (interactive Cancel aborts before teardown by returning early - no state.)
)

// Interactive leftover-prompt option labels (also the tui.Select values).
const (
	leftoverDelete = "Delete the folder including these files"
	leftoverKeep   = "Keep the folder (leave these files + honest state)"
	leftoverCancel = "Cancel (do nothing)"
)

// generatedNames are files done may delete without --remove-all: the state
// files (including the atomic-save temp a crashed Save may leave) plus the
// files new/refresh generate at the work-folder root.
var generatedNames = map[string]bool{
	state.FileName:          true,
	state.FileName + ".tmp": true,
	state.JSONFileName:      true,
	"TICKET.md":             true,
	"README.md":             true,
	"CLAUDE.md":             true,
}

// extraFiles returns the workDir-relative paths of files at the work-folder
// root that done did not generate. It skips the repo worktree directories
// wholesale (their contents belong to git-work, not the user) using the names
// in st.Repos, and excludes generatedNames. Empty directories (recursively)
// are not extra files; anything else - user notes, manually added checkouts -
// is what the leftover decision is about. Computed up front, while worktrees
// still exist, so it can be shown alongside the delete prompts.
func extraFiles(workDir string, st *state.State) ([]string, error) {
	worktrees := make(map[string]bool, len(st.Repos))
	for _, r := range st.Repos {
		worktrees[r.Name] = true
	}
	var extra []string
	err := filepath.WalkDir(workDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// A repo's worktree dir sits at the root; skip it whole.
		if d.IsDir() && !strings.ContainsRune(rel, filepath.Separator) && worktrees[rel] {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if !strings.ContainsRune(rel, filepath.Separator) && generatedNames[rel] {
			return nil
		}
		extra = append(extra, rel)
		return nil
	})
	return extra, err
}
