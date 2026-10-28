package add

import (
	"flag"
	"fmt"
	"os"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
	"github.com/co-native/git-work/internal/template"
	"github.com/co-native/git-work/internal/tui"
	"github.com/co-native/git-work/internal/workfolder"
)

type options struct {
	reposCSV       string // raw --repos value; split into repos by parseFlags
	repos          []string
	branch         string
	lfsFetchAll    bool
	nonInteractive bool
}

// Cmd describes `git work add` for the help system. main.go builds its index
// row from Short and answers `git work help add` from the same value that
// `git work add -h` prints.
var Cmd = &cli.Command{
	Name:     "add",
	Short:    "add another repo to the current work folder",
	Synopsis: []string{"git work add [options]"},
	Long: "Run from inside a work folder. Adds one or more repos - a checkout plus its\n" +
		"branch per repo - to the current ticket, then re-aggregates the generated\n" +
		"README.md and instructions file blocks. Branch resolution matches `new`: the\n" +
		"folder's work branch is used, existing branches for the ticket, local or on\n" +
		"origin, are offered for reuse, and --branch names the branch outright instead.\n" +
		"Added checkouts are rolled back if a later repo fails.",
	Examples: []string{
		"git work add --repos web",
		"git work add --repos api --branch feature/PROJ-123 --non-interactive",
	},
	Flags: []cli.Flag{
		{Long: "repos", Arg: "<a,b,c>", Desc: "repo names to add; skips the interactive multi-select"},
		{Long: "branch", Arg: "<name>", Desc: "this exact branch for the added repos, reused if it exists or created; skips ticket matching"},
		{Long: "lfs-fetch-all", Desc: "clone mode: fetch every LFS object, not just the checked-out branch"},
		{Long: "non-interactive", Desc: "never prompt; fail instead (requires --repos), also if ticket branches exist"},
	},
}

// newFlagSet registers add's flags against o. It is separate from parseFlags
// so the conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := cli.NewFlagSet("add")
	fs.StringVar(&o.reposCSV, "repos", "", "comma-separated repo names to add")
	fs.StringVar(&o.branch, "branch", "", "branch name override")
	fs.BoolVar(&o.lfsFetchAll, "lfs-fetch-all", false, "clone mode: fetch every LFS object")
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
	o.repos = cli.CommaList(o.reposCSV)
	if err := repo.CheckBranchArg(o.branch); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	return o, nil
}

// resolvePlan resolves added repos' work branches. Clone mode reads URLs,
// default branches and candidates from the remote; worktree mode uses the
// primary clones.
func resolvePlan(names []string, matchID, branch string, mode layout.Mode, cfg *config.Config, l layout.Layout, nonInteractive, explicit bool) ([]repo.Planned, error) {
	opts := repo.ResolveOpts{Explicit: explicit, NonInteractive: nonInteractive, Choose: tui.Select}
	if mode != layout.ModeClone {
		return repo.ResolveWorkBranches(names, matchID, branch, repo.PlanOpts{
			ResolveOpts: opts,
			MainOf:      l.RepoMain,
			Warn:        func(msg string) { fmt.Fprintf(os.Stderr, "git-work: %s\n", msg) },
		})
	}
	return repo.ResolveRemoteWorkBranches(names, matchID, branch, repo.RemotePlanOpts{
		ResolveOpts: opts,
		SourceOf: func(name string) (repo.RemoteSource, error) {
			url, err := workfolder.RepoURL(cfg, l, name)
			if err != nil {
				return repo.RemoteSource{}, err
			}
			def := cfg.RepoDefaultBranch(name)
			if def == "" {
				def, err = repo.RemoteDefaultBranch(url)
				if err != nil {
					return repo.RemoteSource{}, err
				}
			}
			return repo.RemoteSource{URL: url, DefaultBranch: def}, nil
		},
		Warn: func(msg string) { fmt.Fprintf(os.Stderr, "git-work: %s\n", msg) },
	})
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
	wf, err := workfolder.Load()
	if err != nil {
		return err
	}
	st, workDir, l := wf.State, wf.Dir, wf.Layout

	mode := wf.Mode()
	if len(o.repos) == 0 {
		if o.nonInteractive {
			// A flag combination the command line cannot satisfy: usage.
			return cli.Usagef("--repos is required with --non-interactive")
		}
		all, err := workfolder.AvailableRepos(wf.Config, l, mode)
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
	plan, err := resolvePlan(o.repos, matchID, branch, mode, wf.Config, l, o.nonInteractive, o.branch != "")
	if err != nil {
		return err
	}

	success := false
	var added []workfolder.PlannedRepo
	defer func() {
		if success {
			return
		}
		for _, p := range added {
			_ = p.Remove(workDir)
		}
	}()

	for _, p := range plan {
		pr := workfolder.PlannedRepo{Name: p.Name, Choice: p.Choice, Mode: mode, MainDir: p.Main, URL: p.URL, DefaultBranch: p.DefaultBranch, LFSFetchAll: o.lfsFetchAll, Warn: func(msg string) { fmt.Fprintf(os.Stderr, "git-work: %s\n", msg) }}
		added = append(added, pr)
		rec, err := pr.Materialize(workDir)
		if err != nil {
			return err
		}
		st.Repos = append(st.Repos, rec)
	}
	if err := st.Save(workDir); err != nil {
		return err
	}

	// Re-aggregate the README.md and instructions file managed blocks.
	if err := template.WriteWorkFolder(workDir, template.WorkFolderOptions{Mode: wf.Mode(), ReposRoot: l.ReposRoot, File: wf.Config.Instructions()}, st, nil); err != nil {
		return err
	}
	success = true
	fmt.Printf("Added %d repo(s) to %s\n", len(o.repos), workDir)
	return nil
}
