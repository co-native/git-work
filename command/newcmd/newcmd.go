package newcmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/provider"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
	"github.com/co-native/git-work/internal/template"
	"github.com/co-native/git-work/internal/tui"
)

type options struct {
	ticketID       string // the positional arg: a ticket id, or a plain name with --no-ticket
	reposCSV       string // raw --repos value; split into repos by parseFlags
	repos          []string
	description    string
	dir            string
	branch         string
	noTicket       bool
	nonInteractive bool
}

// Cmd describes `git work new` for the help system. main.go builds its index
// row from Short and answers `git work help new` from the same value that
// `git work new -h` prints.
var Cmd = &cli.Command{
	Name:  "new",
	Short: "start a new work folder with worktrees",
	Args:  "<ticket-id|name>",
	Synopsis: []string{
		"git work new <ticket-id> [options]",
		"git work new -n|--no-ticket <name> [options]",
	},
	Long: "Starts a work folder: resolves repos, fetches the ticket's details from the\n" +
		"matching provider, creates the folder under the work root, adds a worktree\n" +
		"(and branch) per repo, and writes the generated files. Any failure after the\n" +
		"folder exists rolls back - added worktrees are removed and the folder deleted.\n" +
		"With -n/--no-ticket the argument is a plain name instead: no provider lookup\n" +
		"and no TICKET.md, with folder and branch named from its slug. Existing branches\n" +
		"for the ticket, local or on origin, are offered for reuse; --branch names the\n" +
		"branch outright instead. Flags may come before or after the positional argument.",
	Examples: []string{
		"git work new PROJ-123 --repos api,web",
		"git work new PROJ-123 --repos api --branch feature/PROJ-123 --non-interactive",
		"git work new -n spike-harness --repos api --non-interactive",
	},
	Flags: []cli.Flag{
		{Long: "repos", Arg: "<a,b,c>", Desc: "comma-separated repo names; skips the interactive multi-select"},
		{Long: "description", Arg: "<slug>", Desc: "override the slug otherwise derived from the ticket title"},
		{Long: "dir", Arg: "<name>", Desc: "override the work folder name (default <ticket>-<slug>)"},
		{Long: "branch", Arg: "<name>", Desc: "this exact branch, reused if it exists or created; skips ticket matching (default <ticket>-<slug>)"},
		{Short: "n", Long: "no-ticket", Desc: "ticketless: the argument is a plain name, no provider lookup and no TICKET.md"},
		{Long: "non-interactive", Desc: "never prompt; use --repos or the add_by_default repos, fail if ticket branches exist"},
	},
}

// newFlagSet registers new's flags against o. It is separate from parseFlags
// so the conformance test can compare the registered flags with Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	// Errors are reported by cli.Fail with our own usage block; Go's flag
	// package would otherwise dump its flag table too.
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.reposCSV, "repos", "", "comma-separated repo names")
	fs.StringVar(&o.description, "description", "", "description slug override")
	fs.StringVar(&o.dir, "dir", "", "work folder name override")
	fs.StringVar(&o.branch, "branch", "", "branch name override")
	fs.BoolVar(&o.noTicket, "no-ticket", false, "ticketless: the argument is a plain name (no provider lookup, no TICKET.md)")
	fs.BoolVar(&o.noTicket, "n", false, "shorthand for --no-ticket")
	fs.BoolVar(&o.nonInteractive, "non-interactive", false, "fail instead of prompting")
	return fs
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// parseFlags parses `git work new` arguments.
//
// Help is handled by Run before this is reached, so -h/--help never appears
// here.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	fs := newFlagSet(o)
	// Flags may come before or after the positional arg (`new --no-ticket
	// <name>` reads naturally): parse, take the first remaining arg as the
	// ticket id / name, then parse the rest.
	if err := fs.Parse(args); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	rest := fs.Args()
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return nil, cli.Usagef("give exactly one <ticket-id|name>")
	}
	o.ticketID = rest[0]
	if err := fs.Parse(rest[1:]); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	// The flag package stops at the first non-flag token, so a stray second
	// positional would silently swallow it AND every flag after it.
	if extra := fs.Args(); len(extra) > 0 {
		return nil, cli.Usagef("unexpected argument %q after %q: `git work new` takes exactly one <ticket-id|name>", extra[0], o.ticketID)
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

// Run executes `git work new`; it prints any error to stderr and returns
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

	displayID := ""
	title := ""
	description := ""
	providerName := ""
	folderCase, branchCase := "", ""
	slug := o.description
	if o.noTicket {
		// Ticketless: the positional arg is a plain name - no provider
		// lookup, no TICKET.md; folder and branch derive from the name.
		title = o.ticketID
		if slug == "" {
			slug = slugify(o.ticketID)
		}
		if slug == "" {
			// Determined entirely by the argument typed, so it is the
			// invocation that is wrong, not the environment.
			return cli.Usagef("name %q produces an empty folder name", o.ticketID)
		}
	} else {
		if err := provider.Validate(cfg.Providers); err != nil {
			return err
		}
		match, err := provider.Resolve(cfg.Providers, o.ticketID)
		if err != nil {
			return err
		}
		if match.Provider == nil && len(cfg.Providers) > 0 {
			return fmt.Errorf("ticket id %q matches no configured provider (use --no-ticket for a ticketless work folder)", o.ticketID)
		}
		displayID = provider.DisplayID(o.ticketID)
		title = displayID
		if match.Provider != nil {
			folderCase, branchCase = match.Provider.FolderCase, match.Provider.BranchCase
			if d, err := provider.Fetch(match, o.ticketID); err != nil {
				fmt.Fprintf(os.Stderr, "git-work: %v (continuing without ticket details)\n", err)
			} else {
				providerName = match.Provider.Name
				if d.Title != "" {
					title = d.Title
				}
				description = d.Description
			}
		}
		if slug == "" {
			slug = slugify(title)
		}
	}

	// Resolve repos. --repos is a replacement, not an addition: naming repos
	// explicitly means exactly those, so add_by_default is consulted only when
	// nothing was named. A flag that could add but never narrow would be the
	// worse flag.
	if len(o.repos) == 0 {
		all, err := l.ListRepos()
		if err != nil {
			return err
		}
		if len(all) == 0 {
			return fmt.Errorf("no repos found under %s; run `git work clone` first", l.ReposRoot)
		}
		byDefault := defaultRepos(cfg, all)
		if o.nonInteractive {
			// Without a prompt the default set is the only thing that can
			// answer this; with nothing flagged there is genuinely no answer.
			if len(byDefault) == 0 {
				return cli.Usagef("--repos is required with --non-interactive " +
					"(no repo has add_by_default set)")
			}
			o.repos = byDefault
		} else {
			// The prompt still opens with the flagged repos ticked: the
			// operator confirms rather than discovering afterwards which
			// worktrees got created.
			o.repos, err = tui.MultiSelect("Select repos for "+o.ticketID, all, byDefault)
			if err != nil {
				return err
			}
		}
	}
	if len(o.repos) == 0 {
		return fmt.Errorf("no repos selected")
	}

	// Resolve work dir name and branch.
	dirName := o.dir
	if dirName == "" {
		if o.noTicket {
			dirName = slug
		} else {
			dirName = provider.CaseID(o.ticketID, folderCase) + "-" + slug
		}
		if !o.nonInteractive {
			dirName, err = tui.Input("Work folder name", dirName)
			if err != nil {
				return err
			}
		}
	}
	branch := o.branch
	if branch == "" {
		if o.noTicket {
			branch = slug
		} else {
			branch = provider.CaseID(o.ticketID, branchCase) + "-" + slug
		}
		if !o.nonInteractive {
			branch, err = tui.Input("Branch name", branch)
			if err != nil {
				return err
			}
		}
	}

	workDir := l.WorkDir(dirName)
	if _, err := os.Stat(workDir); err == nil {
		return fmt.Errorf("work folder already exists: %s", workDir)
	}

	// Existing branches are matched against the ticket id; ticketless work
	// has none, so its name-derived slug (the default branch name) stands in.
	matchID := o.ticketID
	if o.noTicket {
		matchID = slug
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
		Warn:   warn,
	})
	if err != nil {
		return err
	}

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}

	// On any failure after creating the work folder, roll back: remove any
	// worktrees we added and delete the folder, so the user can re-run cleanly.
	success := false
	var added []state.Repo
	defer func() {
		if success {
			return
		}
		for _, r := range added {
			main, err := l.RepoMain(r.Name)
			if err != nil {
				continue
			}
			_ = repo.RemoveWorktree(main, l.Worktree(dirName, r.Name), true)
		}
		_ = os.RemoveAll(workDir)
	}()

	st := &state.State{TicketID: displayID, Title: title, Slug: slug, Branch: branch, Provider: providerName, NoTicket: o.noTicket}
	for _, p := range plan {
		if err := p.Choice.Checkout(p.Main, l.Worktree(dirName, p.Name)); err != nil {
			return err
		}
		rec := state.Repo{Name: p.Name, BranchSource: p.Choice.Source, Branch: p.Choice.Branch}
		st.Repos = append(st.Repos, rec)
		added = append(added, rec)
	}

	if err := writeFiles(workDir, st, l.ReposRoot, description); err != nil {
		return err
	}
	if err := st.Save(workDir); err != nil {
		return err
	}
	success = true
	fmt.Printf("Created work folder %s with %d repo(s)\n", workDir, len(st.Repos))
	return nil
}

// warn prints a non-fatal notice in git-work's stderr format.
func warn(msg string) {
	fmt.Fprintf(os.Stderr, "git-work: %s\n", msg)
}

// writeFiles writes TICKET/README/CLAUDE managed blocks for the work folder.
// Ticketless folders get no TICKET.md and name-based headings.
// reposRoot is the resolved repos root (layout.Layout's), not the raw config
// value: it locates the per-repo CLAUDE.md overlays on disk, so an unexpanded
// ~ would find none and silently drop them from the generated file.
func writeFiles(workDir string, st *state.State, reposRoot, description string) error {
	repos := make([]string, len(st.Repos))
	refs := make([]template.RepoRef, len(st.Repos))
	for i, r := range st.Repos {
		repos[i] = r.Name
		refs[i] = template.RepoRef{Name: r.Name, Branch: r.Branch}
	}
	if !st.NoTicket {
		if err := template.WriteManaged(filepath.Join(workDir, "TICKET.md"),
			template.Ticket(st.TicketID, st.Title, description)); err != nil {
			return err
		}
	}
	if err := template.WriteManaged(filepath.Join(workDir, "README.md"),
		template.Readme(st.DisplayName(), st.Branch, repos)); err != nil {
		return err
	}
	return template.WriteManaged(filepath.Join(workDir, "CLAUDE.md"),
		template.AggregateClaude(template.ClaudeInput{
			ReposRoot:   reposRoot,
			OverlayPath: config.OverlayPath(),
			TicketID:    st.TicketID,
			Title:       st.Title,
			Branch:      st.Branch,
			Repos:       refs,
		}))
}

// defaultRepos returns the repos flagged add_by_default, in the order
// ListRepos gave them so the picker's ticks match its rows. Config entries
// naming a repo that is not present are ignored rather than erroring: a
// config shared across machines will routinely mention repos this one has not
// cloned.
func defaultRepos(cfg *config.Config, available []string) []string {
	var out []string
	for _, name := range available {
		if r, ok := cfg.Repos[name]; ok && r.AddByDefault {
			out = append(out, name)
		}
	}
	return out
}
