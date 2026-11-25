// Package rename implements `git work rename`: change a work folder's folder
// name, slug, ticket and work branches as one operation. It is the only
// command that moves a folder; `new` and `adopt` fix the names when a folder is
// created, and nothing else changes them.
package rename

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/naming"
	"github.com/co-native/git-work/internal/provider"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
	"github.com/co-native/git-work/internal/teardown"
	"github.com/co-native/git-work/internal/template"
	"github.com/co-native/git-work/internal/workfolder"
)

type options struct {
	dir        string
	newName    string
	ticket     string
	branch     string
	keepBranch bool
	remote     bool
	dryRun     bool
}

// Cmd describes `git work rename` for the help system. main.go builds its index
// row from Short and answers `git work help rename` from the same value that
// `git work rename -h` prints.
var Cmd = &cli.Command{
	Name:     "rename",
	Short:    "rename a work folder, its ticket and its work branches",
	Args:     "<dir> [<new-name>]",
	Synopsis: []string{"git work rename <dir> [<new-name>] [options]"},
	Long: "Renames a work folder as one operation: the directory, its slug, its work\n" +
		"branch, and every repo sitting on that branch. <dir> is a bare name resolved\n" +
		"under the work root, or a path. Run it from outside the folder: the folder\n" +
		"moves, and a shell sitting inside it would be left in a path that no longer\n" +
		"exists.\n" +
		"\n" +
		"<new-name> sets the folder name; the branch follows the folder (<ticket>-<slug>,\n" +
		"or the slug alone for a ticketless folder). --branch names the branch instead,\n" +
		"and --keep-branch leaves it alone. Repos on a deliberately different branch are\n" +
		"left there and reported. --ticket gives the folder a ticket (or a different one):\n" +
		"the folder becomes <ticket>-<slug>, the provider's title is fetched, and TICKET.md\n" +
		"is written. There is no way back to ticketless.\n" +
		"\n" +
		"--remote also renames the branch on origin: it pushes the new name and deletes\n" +
		"the old one. That reads as a branch deletion to a hosting service, so open pull\n" +
		"requests against the old name are closed; leave it out and `git work push`\n" +
		"publishes the new name, or delete the old ref with `git work done --delete-remote`.\n" +
		"\n" +
		"Every check runs before anything changes: the target folder and branches must be\n" +
		"free, every worktree must agree with its recorded branch, the target must not be a\n" +
		"repo's default branch, and .worktrees must be empty. A failure renames nothing.\n" +
		"A failure after the first branch rename leaves the folder in place and the run\n" +
		"can be repeated; steps already done are skipped.",
	Examples: []string{
		"git work rename PROJ-1-add-auth PROJ-1-add-authentication",
		"git work rename spike-harness --ticket PROJ-42",
		"git work rename PROJ-1-add-auth --branch feature/PROJ-1",
		"git work rename PROJ-1-add-auth PROJ-1-add-login --dry-run",
	},
	Flags: []cli.Flag{
		{Long: "ticket", Arg: "<id>", Desc: "ticket the folder belongs to; changes it, or adds one to a ticketless folder"},
		{Long: "branch", Arg: "<name>", Desc: "new work branch name (default derived from the folder name)"},
		{Long: "keep-branch", Desc: "leave the work branch and its repos' branches alone"},
		{Long: "remote", Desc: "also rename the branch on origin (push new, delete old)"},
		{Long: "dry-run", Desc: "run every check and print the plan without changing anything"},
	},
}

// newFlagSet registers rename's flags against o. It is separate from
// parseFlags so the conformance test can compare the registered flags with
// Cmd's.
func newFlagSet(o *options) *flag.FlagSet {
	fs := cli.NewFlagSet("rename")
	fs.StringVar(&o.ticket, "ticket", "", "ticket id")
	fs.StringVar(&o.branch, "branch", "", "new work branch name")
	fs.BoolVar(&o.keepBranch, "keep-branch", false, "leave branches alone")
	fs.BoolVar(&o.remote, "remote", false, "also rename the branch on origin")
	fs.BoolVar(&o.dryRun, "dry-run", false, "check and print the plan without changing anything")
	return fs
}

// parseFlags parses `git work rename` arguments. <dir> is mandatory and must
// come first: a leading-dash first argument means the operator named no
// folder. <new-name> is the next token when it is not a flag, so flags may
// follow it in any order.
//
// Help is handled by Run before this is reached, so -h/--help never appears
// here.
func parseFlags(args []string) (*options, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, cli.Usagef("give a <dir> to rename")
	}
	o := &options{dir: args[0]}
	rest := args[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		o.newName = rest[0]
		rest = rest[1:]
	}
	fs := newFlagSet(o)
	if err := fs.Parse(rest); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	// A second positional is the new name when flags came first:
	// `rename <dir> --ticket PROJ-2 <new-name>`.
	extra := fs.Args()
	if o.newName == "" && len(extra) == 1 && !strings.HasPrefix(extra[0], "-") {
		o.newName = extra[0]
		extra = nil
	}
	if len(extra) > 0 {
		return nil, cli.Usagef("unexpected argument %q: `git work rename` takes a <dir> and at most one <new-name>", extra[0])
	}
	if o.newName == "" && o.ticket == "" && o.branch == "" {
		return nil, cli.Usagef("give a <new-name>, --ticket <id> or --branch <name>: a rename needs at least one new name")
	}
	if o.keepBranch && o.branch != "" {
		return nil, cli.Usagef("give --branch or --keep-branch, not both")
	}
	if o.remote && o.keepBranch {
		return nil, cli.Usagef("--remote renames branches on origin, so it needs a branch rename: drop --keep-branch")
	}
	if o.newName != "" {
		if problem := naming.FolderNameProblem(o.newName); problem != "" {
			return nil, cli.Usagef("new folder name %q: %s", o.newName, problem)
		}
	}
	if err := repo.CheckBranchArg(o.branch); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	if err := repo.CheckBranchName(o.branch); err != nil {
		return nil, cli.Usagef("%v", err)
	}
	return o, nil
}

// Run executes `git work rename`; it prints any error to stderr and returns
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
	cfg, l, err := config.LoadLayout()
	if err != nil {
		return err
	}
	workDir, err := workfolder.ResolveDir(l, o.dir)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := workfolder.RefuseInsideWorktrees(cwd, workDir); err != nil {
		return err
	}
	st, err := state.Load(workDir)
	if err != nil {
		return fmt.Errorf("load state from %s: %w", workDir, err)
	}

	var tc *ticketChange
	if o.ticket != "" {
		if tc, err = resolveTicket(cfg, o.ticket); err != nil {
			return err
		}
	}
	p, err := buildPlan(workDir, st, o, tc, l)
	if err != nil {
		return err
	}
	if !p.movesFolder && !p.branchChanged && !p.ticketChanged {
		fmt.Printf("Nothing to rename in %s\n", workDir)
		return nil
	}
	// done refuses the same way and for the same reason: a shell inside a
	// directory that is about to move keeps a path that no longer exists, and
	// on Windows the rename would fail outright.
	if p.movesFolder && workfolder.InsideDir(workDir, cwd) {
		return cli.Usagef("refusing to rename %s from inside it: the folder moves and this shell would be left in a path that no longer exists; run it from outside, e.g. `cd %s && git work rename %s %s`",
			workDir, filepath.Dir(workDir), filepath.Base(workDir), p.newBase)
	}
	if err := p.check(); err != nil {
		return err
	}
	if p.dryRun {
		p.report(os.Stdout)
		return nil
	}
	if err := p.execute(cfg, l, st); err != nil {
		return err
	}
	p.report(os.Stdout)
	return nil
}

// ticketChange is a resolved --ticket: the identity the folder records when the
// ticket changes.
type ticketChange = provider.Identity

// resolveTicket resolves a --ticket value. The policy for an id no provider
// matches mirrors `new`: an error while providers are configured, since the
// operator asked for a ticket and the tool cannot tell which project it is.
func resolveTicket(cfg *config.Config, id string) (*ticketChange, error) {
	in, matched, err := provider.ResolveIdentity(cfg.Providers, id, func(msg string) {
		fmt.Fprintf(os.Stderr, "git-work: %v (continuing without ticket details)\n", msg)
	})
	if err != nil {
		return nil, err
	}
	if !matched && len(cfg.Providers) > 0 {
		return nil, fmt.Errorf("ticket id %q matches no configured provider; add a provider pattern for it, or rename the folder without --ticket", id)
	}
	return &in, nil
}

// repoAction is what a rename does to one repo's branch.
type repoAction int

const (
	repoKeep    repoAction = iota // branches are not being renamed at all
	repoRename                    // the recorded branch moves to the new name
	repoAlready                   // already on the target name
	repoLeave                     // on a deliberately different branch
)

// repoChange is one repo's part of the plan.
type repoChange struct {
	name          string
	worktree      string // <workDir>/<name>, the checkout
	repoDir       string // primary clone (worktree mode) or clone (clone mode), for refs
	defaultBranch string // recorded default, for the default-branch guard
	from          string
	to            string
	action        repoAction
	oldRemote     bool // origin/<from> exists; set by check when --remote is given
}

// plan is everything the rename will do, computed before anything changes.
type plan struct {
	workDir, newDir  string
	oldBase, newBase string
	oldSlug, newSlug string
	oldBranch        string
	newBranch        string
	branchChanged    bool
	movesFolder      bool
	oldTicket        string
	ticket           *ticketChange
	ticketChanged    bool
	remote           bool
	dryRun           bool
	repos            []repoChange
}

// buildPlan derives every name and every repo's action without touching the
// filesystem, so the derivation is testable on its own.
func buildPlan(workDir string, st *state.State, o *options, tc *ticketChange, l layout.Layout) (*plan, error) {
	p := &plan{
		workDir:       workDir,
		oldBase:       filepath.Base(workDir),
		oldBranch:     st.Branch,
		oldTicket:     st.TicketID,
		ticket:        tc,
		ticketChanged: tc != nil && (st.NoTicket || !strings.EqualFold(tc.DisplayID, st.TicketID)),
		remote:        o.remote,
		dryRun:        o.dryRun,
	}
	p.oldSlug = naming.SlugForFolder(p.oldBase, st.TicketID)

	newTicket := st.TicketID
	if tc != nil {
		newTicket = tc.DisplayID
	}
	switch {
	case o.newName != "":
		p.newSlug = naming.SlugForFolder(o.newName, newTicket)
		p.newBase = o.newName
	case tc != nil:
		p.newSlug = p.oldSlug
		p.newBase = provider.CaseID(tc.DisplayID, tc.FolderCase) + "-" + p.newSlug
	default:
		p.newSlug = p.oldSlug
		p.newBase = p.oldBase
	}
	// A slug is only needed when a name depends on it. A branch-only rename of a
	// legacy folder whose name carried no slug (`PROJ-123` from adopt) is valid.
	if p.newSlug == "" && (o.newName != "" || tc != nil) {
		if o.newName == "" {
			return nil, fmt.Errorf("%s has no slug to keep: give a <new-name>", p.oldBase)
		}
		return nil, cli.Usagef("new folder name %q produces an empty slug; use letters or digits", o.newName)
	}
	if o.newName != "" {
		// A ticket-shaped prefix is a claim about the ticket. Keeping the old
		// ticket under a name that names another would contradict state, and on
		// a ticketless folder the name is how the ticket gets attached.
		prefix, _ := naming.InferTicket(o.newName)
		claimed := newTicket
		if claimed == "" {
			claimed = "(none)"
		}
		if prefix != "" && !strings.EqualFold(prefix, newTicket) {
			return nil, cli.Usagef("new name %q names ticket %q, but the folder belongs to %s; give --ticket %s to move it, or choose a name without a ticket prefix",
				o.newName, prefix, claimed, prefix)
		}
	}
	if st.NoTicket && tc == nil && o.newName != "" && o.newName != p.newSlug {
		// A ticketless folder is named by its slug, exactly as `new -n`
		// creates it. Accepting a name that differs would put a state slug and
		// a folder name out of step on the next rename.
		return nil, cli.Usagef("a ticketless work folder is named by its slug: use %q, or give --ticket to attach the folder to a ticket", p.newSlug)
	}
	p.movesFolder = p.newBase != p.oldBase
	p.newDir = filepath.Join(filepath.Dir(workDir), p.newBase)

	switch {
	case o.keepBranch:
		p.newBranch = st.Branch
	case o.branch != "":
		p.newBranch = o.branch
	case p.ticketChanged:
		p.newBranch = provider.CaseID(tc.DisplayID, tc.BranchCase) + "-" + p.newSlug
	case p.newSlug == st.Slug:
		p.newBranch = st.Branch
	default:
		branch, ok := naming.BranchForRename(st.Branch, p.oldSlug, p.newSlug)
		if !ok {
			return nil, fmt.Errorf("the work branch %s does not follow the folder name %s, so there is no new branch name to derive; give --branch <name> or --keep-branch", st.Branch, p.oldBase)
		}
		p.newBranch = branch
	}
	if err := repo.CheckBranchName(p.newBranch); err != nil {
		return nil, err
	}
	p.branchChanged = p.newBranch != st.Branch

	p.repos = make([]repoChange, 0, len(st.Repos))
	for _, r := range st.Repos {
		rc := repoChange{
			name:          r.Name,
			worktree:      filepath.Join(workDir, r.Name),
			from:          r.Branch,
			to:            p.newBranch,
			defaultBranch: r.DefaultBranch,
		}
		repoDir, err := workfolder.RepoDirFor(l, st, workDir, r.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.Name, err)
		}
		rc.repoDir = repoDir
		switch {
		case !p.branchChanged:
			rc.action = repoKeep
		case r.Branch == p.newBranch:
			rc.action = repoAlready
		case r.Branch == p.oldBranch:
			rc.action = repoRename
		default:
			rc.action = repoLeave
		}
		p.repos = append(p.repos, rc)
	}
	return p, nil
}

// check runs every gate before anything changes. All problems are collected
// so one run reports every repo that needs attention.
func (p *plan) check() error {
	var problems []string
	if left, err := teardown.Leftovers(p.workDir); err != nil {
		problems = append(problems, fmt.Sprintf(".worktrees: %v", err))
	} else if len(left) > 0 {
		problems = append(problems, fmt.Sprintf(".worktrees contains %s; remove those worktrees first, since a subagent or integrate checkout is using them", strings.Join(left, ", ")))
	}

	if p.movesFolder {
		if _, err := os.Lstat(p.newDir); err == nil {
			if !sameFile(p.workDir, p.newDir) {
				problems = append(problems, fmt.Sprintf("%s already exists", p.newDir))
			}
		} else if !os.IsNotExist(err) {
			problems = append(problems, fmt.Sprintf("%s: %v", p.newDir, err))
		}
		if p.newBase != p.oldBase {
			oldWS := filepath.Join(p.workDir, template.WorkspaceFile(p.workDir))
			newWS := filepath.Join(p.workDir, template.WorkspaceFile(filepath.Join(p.workDir, p.newBase)))
			// A case-only rename makes the old and new workspace files the same
			// file on a case-insensitive filesystem.
			if _, err := os.Lstat(newWS); err == nil && !sameFile(oldWS, newWS) {
				problems = append(problems, fmt.Sprintf("%s already exists; move or delete it first", newWS))
			}
		}
	}

	for i := range p.repos {
		r := &p.repos[i]
		if problem, err := repo.WorktreeBranchProblem(r.worktree, r.from); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", r.name, err))
		} else if problem != "" {
			problems = append(problems, fmt.Sprintf("%s: %s", r.name, problem))
		}
		if r.action != repoRename {
			continue
		}
		if exists, err := repo.LocalBranchExists(r.repoDir, r.to); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", r.name, err))
		} else if exists {
			problems = append(problems, fmt.Sprintf("%s: branch %s already exists; choose another name or delete it first", r.name, r.to))
		}
		if isDefault, err := repo.IsDefaultBranchFor(r.repoDir, r.defaultBranch, r.to); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", r.name, err))
		} else if isDefault {
			problems = append(problems, fmt.Sprintf("%s: %s is the repo's default branch and cannot be a work branch", r.name, r.to))
		}
		if !p.remote {
			continue
		}
		old, err := repo.RemoteBranchExistsOnOrigin(r.repoDir, r.from)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", r.name, err))
			continue
		}
		r.oldRemote = old
		taken, err := repo.RemoteBranchExistsOnOrigin(r.repoDir, r.to)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", r.name, err))
		} else if taken {
			problems = append(problems, fmt.Sprintf("%s: origin/%s already exists; choose another name or delete it first", r.name, r.to))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("cannot rename; nothing changed:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// execute applies the plan in an order that keeps the folder findable through
// a partial failure: branches and state first, the folder move last. A re-run
// skips whatever already happened.
func (p *plan) execute(cfg *config.Config, l layout.Layout, st *state.State) error {
	var failures []string

	// Rename branches first: this is the step that can fail per repo. Each
	// success is saved immediately, so a partial failure leaves state matching
	// the branches that moved.
	for i := range p.repos {
		r := &p.repos[i]
		if r.action != repoRename {
			continue
		}
		if err := repo.RenameBranch(r.worktree, r.from, r.to); err != nil {
			failures = append(failures, fmt.Sprintf("%s: rename branch %s to %s: %v", r.name, r.from, r.to, err))
			continue
		}
		for j := range st.Repos {
			if st.Repos[j].Name == r.name {
				st.Repos[j].Branch = r.to
			}
		}
		if err := st.Save(p.workDir); err != nil {
			return fmt.Errorf("save state after renaming %s: %w", r.name, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("renamed some branches but not all; the work folder was not moved:\n  %s\nfix the failures and run the command again", strings.Join(failures, "\n  "))
	}

	if p.movesFolder || p.ticketChanged {
		st.Slug = p.newSlug
	}
	st.Branch = p.newBranch
	if p.ticketChanged {
		st.TicketID = p.ticket.DisplayID
		st.Title = p.ticket.Title
		st.Provider = p.ticket.ProviderName
		st.NoTicket = false
	}
	// A ticketless folder's plain name is its title; a ticket folder's title
	// comes from its provider and does not follow the folder name.
	if st.NoTicket && p.movesFolder {
		st.Title = p.newBase
	}
	if err := st.Save(p.workDir); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	// The remote rename runs before the folder move. It needs only the branch
	// names and a working checkout, which the move does not change; a later
	// move failure can then be retried without the old remote name.
	if p.remote {
		for i := range p.repos {
			r := &p.repos[i]
			if r.action != repoRename || !r.oldRemote {
				continue
			}
			dir := filepath.Join(p.workDir, r.name)
			if err := repo.PushBranchUpstream(dir, r.to, false); err != nil {
				failures = append(failures, fmt.Sprintf("%s: push origin/%s: %v\n    retry with: git -C %s push -u origin %s", r.name, r.to, err, dir, r.to))
				continue
			}
			if err := repo.DeleteRemoteBranch(dir, r.from); err != nil {
				failures = append(failures, fmt.Sprintf("%s: pushed origin/%s but could not delete origin/%s: %v\n    delete it with: git -C %s push origin --delete %s", r.name, r.to, r.from, err, dir, r.from))
			}
		}
	}

	finalDir := p.workDir
	if p.movesFolder {
		if err := os.Rename(p.workDir, p.newDir); err != nil {
			return fmt.Errorf("branches and state are renamed, but the work folder could not be moved: %w\nrun the command again to finish the move", err)
		}
		finalDir = p.newDir
	}

	// Worktree mode records each worktree's absolute path in its primary. The
	// move leaves those links pointing at the old path, which git reports as
	// prunable; repair rewrites them.
	if p.movesFolder && st.Mode.OrDefault() == layout.ModeWorktree {
		for i := range p.repos {
			r := &p.repos[i]
			main, err := l.RepoMain(r.name)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", r.name, err))
				continue
			}
			if err := repo.RepairWorktrees(main, filepath.Join(finalDir, r.name)); err != nil {
				failures = append(failures, fmt.Sprintf("%s: repair worktree link: %v\n    repair it with: git -C %s worktree repair %s", r.name, err, main, filepath.Join(finalDir, r.name)))
			}
		}
	}

	// The workspace file is named after the folder, so it moves with it. Its
	// content is the operator's except for the folders list, which the
	// re-render below refreshes.
	if p.movesFolder && p.newBase != p.oldBase {
		from := filepath.Join(finalDir, template.WorkspaceFile(p.workDir))
		to := filepath.Join(finalDir, template.WorkspaceFile(filepath.Join(finalDir, p.newBase)))
		if _, err := os.Lstat(from); err == nil {
			if err := os.Rename(from, to); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", from, err))
			}
		}
	}
	var details *template.TicketDetails
	if p.ticketChanged {
		details = &template.TicketDetails{Title: p.ticket.Title, Description: p.ticket.Description}
	}
	if err := template.WriteWorkFolder(finalDir, template.WorkFolderOptions{
		Mode:      st.Mode.OrDefault(),
		ReposRoot: l.ReposRoot,
		File:      cfg.Instructions(),
	}, st, details); err != nil {
		failures = append(failures, err.Error())
	}

	if len(failures) > 0 {
		return fmt.Errorf("renamed to %s, but some follow-up steps failed:\n  %s\nrun the command again to retry them", finalDir, strings.Join(failures, "\n  "))
	}
	return nil
}

// report prints the plan: the folder, slug, ticket and branch summary, then one
// block per repo whose branch changes. In a dry run the same lines describe
// what would happen.
func (p *plan) report(w io.Writer) {
	if p.dryRun {
		fmt.Fprintln(w, "Dry run; nothing changed. Plan:")
	}
	if p.movesFolder {
		fmt.Fprintf(w, "work folder: %s -> %s\n", p.workDir, p.newDir)
	} else {
		fmt.Fprintf(w, "work folder: %s (name unchanged)\n", p.workDir)
	}
	if p.newSlug != p.oldSlug {
		fmt.Fprintf(w, "slug: %s -> %s\n", p.oldSlug, p.newSlug)
	}
	if p.branchChanged {
		fmt.Fprintf(w, "branch: %s -> %s\n", p.oldBranch, p.newBranch)
	}
	if p.ticketChanged {
		before := p.oldTicket
		if before == "" {
			before = "(none)"
		}
		fmt.Fprintf(w, "ticket: %s -> %s (%s)\n", before, p.ticket.DisplayID, p.ticket.Title)
	}
	sections := cli.NewSections(w)
	for _, r := range p.repos {
		sections.Begin()
		switch r.action {
		case repoRename:
			if p.remote && r.oldRemote {
				fmt.Fprintf(sections, "%s: branch %s -> %s; origin/%s -> origin/%s\n", r.name, r.from, r.to, r.from, r.to)
			} else {
				fmt.Fprintf(sections, "%s: branch %s -> %s\n", r.name, r.from, r.to)
			}
		case repoAlready:
			fmt.Fprintf(sections, "%s: already on %s\n", r.name, r.to)
		case repoLeave:
			fmt.Fprintf(sections, "%s: left on %s (not the folder branch)\n", r.name, r.from)
		}
	}
}

// sameFile reports whether a and b name the same file on disk. A case-only rename
// makes the old and new paths name the same file on a case-insensitive filesystem.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}
