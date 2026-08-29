package add

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
	"github.com/co-native/git-work/internal/template"
	"github.com/co-native/git-work/internal/tui"
)

type options struct {
	reposCSV       string // raw --repos value; split into repos by parseFlags
	repos          []string
	branch         string
	nonInteractive bool
}

// Cmd describes `git work add` for the help system. main.go builds its index
// row from Short and answers `git work help add` from the same value that
// `git work add -h` prints.
var Cmd = &cli.Command{
	Name:     "add",
	Short:    "add another repo to the current work folder",
	Synopsis: []string{"git work add [options]"},
	Long: "Run from inside a work folder. Adds one or more repos - a worktree plus its\n" +
		"branch per repo - to the current ticket, then re-aggregates the generated\n" +
		"README.md and CLAUDE.md blocks. Branch resolution matches `new`: the folder's\n" +
		"work branch is used, existing branches for the ticket, local or on origin,\n" +
		"are offered for reuse, and --branch names the branch outright instead.\n" +
		"Added worktrees are rolled back if a later repo fails.",
	Examples: []string{
		"git work add --repos web",
		"git work add --repos api --branch feature/PROJ-123 --non-interactive",
	},
	Flags: []cli.Flag{
		{Long: "repos", Arg: "<a,b,c>", Desc: "repo names to add; skips the interactive multi-select"},
		{Long: "branch", Arg: "<name>", Desc: "this exact branch for the added repos, reused if it exists or created; skips ticket matching"},
		{Long: "non-interactive", Desc: "never prompt; fail instead (requires --repos), also if ticket branches exist"},
	},
}

// newFlagSet registers add's flags against o. It is separate from parseFlags
// so the conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.reposCSV, "repos", "", "comma-separated repo names to add")
	fs.StringVar(&o.branch, "branch", "", "branch name override")
	fs.BoolVar(&o.nonInteractive, "non-interactive", false, "fail instead of prompting")
	return fs
}

// parseFlags parses `git work add` arguments.
//
// Help is handled by Run before this is reached, so -h/--help never appears
// here.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	fs := newFlagSet(o)
	if err := fs.Parse(args); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	if o.reposCSV != "" {
		for _, r := range strings.Split(o.reposCSV, ",") {
			if r = strings.TrimSpace(r); r != "" {
				o.repos = append(o.repos, r)
			}
		}
	}
	if err := repo.CheckBranchArg(o.branch); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	return o, nil
}

// remaining returns repos from all that aren't already in st.
func remaining(all []string, st *state.State) []string {
	have := map[string]bool{}
	for _, r := range st.Repos {
		have[r.Name] = true
	}
	var out []string
	for _, a := range all {
		if !have[a] {
			out = append(out, a)
		}
	}
	return out
}

// Run executes `git work add`; it prints any error to stderr and returns
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

// run adds one or more repos to the current work folder.
func run(args []string) error {
	o, err := parseFlags(args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	st, workDir, err := state.FindUp(cwd)
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

	if len(o.repos) == 0 {
		if o.nonInteractive {
			// A flag combination the command line cannot satisfy: usage.
			return cli.Usagef("--repos is required with --non-interactive")
		}
		all, err := l.ListRepos()
		if err != nil {
			return err
		}
		choices := remaining(all, st)
		if len(choices) == 0 {
			return fmt.Errorf("all repos are already included")
		}
		// No pre-selection: add_by_default answers "which repos start a work
		// folder", and by the time `add` runs that question is settled.
		o.repos, err = tui.MultiSelect("Add repos to "+st.DisplayName(), choices, nil)
		if err != nil {
			return err
		}
	}
	if len(o.repos) == 0 {
		return fmt.Errorf("no repos selected")
	}

	// Existing branches are matched against the ticket id; ticketless work
	// has none, so its name-derived slug (the default branch name) stands in.
	matchID := st.TicketID
	if st.NoTicket {
		matchID = st.Slug
	}
	branch := o.branch
	if branch == "" {
		branch = st.Branch
	}
	// Every repo resolves before anything is created: a refusal reports all
	// repos at once and leaves nothing to roll back.
	plan, err := repo.ResolveWorkBranches(o.repos, matchID, branch, repo.PlanOpts{
		ResolveOpts: repo.ResolveOpts{
			Explicit:       o.branch != "",
			NonInteractive: o.nonInteractive,
			Choose:         tui.Select,
		},
		MainOf: l.RepoMain,
		Warn:   func(msg string) { fmt.Fprintf(os.Stderr, "git-work: %s\n", msg) },
	})
	if err != nil {
		return err
	}

	success := false
	var added []string
	defer func() {
		if success {
			return
		}
		for _, name := range added {
			main, err := l.RepoMain(name)
			if err != nil {
				continue
			}
			_ = repo.RemoveWorktree(main, filepath.Join(workDir, name), true)
		}
	}()

	for _, p := range plan {
		if err := p.Choice.Checkout(p.Main, filepath.Join(workDir, p.Name)); err != nil {
			return err
		}
		st.Repos = append(st.Repos, state.Repo{Name: p.Name, BranchSource: p.Choice.Source, Branch: p.Choice.Branch})
		added = append(added, p.Name)
	}
	if err := st.Save(workDir); err != nil {
		return err
	}

	// Re-aggregate CLAUDE.md + README managed blocks.
	repos := make([]string, len(st.Repos))
	refs := make([]template.RepoRef, len(st.Repos))
	for i, r := range st.Repos {
		repos[i] = r.Name
		refs[i] = template.RepoRef{Name: r.Name, Branch: r.Branch}
	}
	title := st.Title
	if err := template.WriteManaged(filepath.Join(workDir, "README.md"),
		template.Readme(st.DisplayName(), st.Branch, repos)); err != nil {
		return err
	}
	if err := template.WriteManaged(filepath.Join(workDir, "CLAUDE.md"),
		template.AggregateClaude(template.ClaudeInput{
			ReposRoot:   l.ReposRoot,
			OverlayPath: config.OverlayPath(),
			TicketID:    st.TicketID,
			Title:       title,
			Branch:      st.Branch,
			Repos:       refs,
		})); err != nil {
		return err
	}
	success = true
	fmt.Printf("Added %d repo(s) to %s\n", len(o.repos), workDir)
	return nil
}
