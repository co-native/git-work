// Package adopt implements `git work adopt <dir>`: inside a work folder,
// register one existing worktree subdirectory in the folder's state file
// (.git-work.yaml), creating the state file first when the folder is
// entirely unmanaged (ticket inferred from the folder name). Nothing moves
// and nothing is rewritten in git.
package adopt

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/co-native/git-work/internal/cli"
	"github.com/co-native/git-work/internal/config"
	"github.com/co-native/git-work/internal/layout"
	"github.com/co-native/git-work/internal/repo"
	"github.com/co-native/git-work/internal/state"
)

// Cmd describes `git work adopt` for the help system. main.go builds its index
// row from Short and answers `git work help adopt` from the same value that
// `git work adopt -h` prints. adopt parses no flags of its own, so the
// descriptor declares none - the renderer still appends -h/--help.
var Cmd = &cli.Command{
	Name:     "adopt",
	Short:    "register an existing worktree dir in the current work folder",
	Args:     "<dir>",
	Synopsis: []string{"git work adopt <dir>"},
	Long: "Registers one existing worktree subdirectory of the current work folder in\n" +
		"that folder's .git-work.yaml. <dir> must be a direct subdirectory of the work\n" +
		"folder the command runs in; nothing moves and nothing is rewritten in git.\n" +
		"An entirely unmanaged folder is bootstrapped first - ticket and slug inferred\n" +
		"from the folder name, the adopted branch becoming the work branch. Re-adopting\n" +
		"a repo whose branch moved re-records it, the remedy branch-drift refusals name.",
	Examples: []string{"git work adopt ./api"},
}

// Run executes `git work adopt`; it prints any error to stderr and returns
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

// run validates the invocation by arity rather than with a FlagSet: adopt
// takes exactly one positional and no flags of its own. Help is handled by
// Run before this is reached, so -h/--help never appears here.
func run(args []string) error {
	if len(args) != 1 {
		return cli.Usagef("give exactly one <dir>")
	}
	if strings.HasPrefix(args[0], "-") {
		return cli.Usagef("unrecognized flag %q; adopt takes a single <dir> and no flags", args[0])
	}
	target, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	workDir, err := resolveWorkDir(cwd, target)
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
	return adoptOne(os.Stdout, l, workDir, target)
}

// resolveWorkDir returns the work folder being adopted into - the target's
// parent directory - after checking that it is the work folder the command
// runs inside: the folder FindUp locates from cwd, or cwd itself when
// nothing is managed yet (the bootstrap case). Without this check,
// `git work adopt /elsewhere/dir` would silently create or modify
// /elsewhere/.git-work.yaml.
//
// A target outside the folder is a usage error, not a failure: it is the
// argument that is wrong, the same way a repo name absent from the work folder
// is wrong elsewhere.
func resolveWorkDir(cwd, target string) (string, error) {
	workDir := filepath.Dir(target)
	expected := cwd
	if _, stateDir, err := state.FindUp(cwd); err == nil {
		expected = stateDir
	}
	if !sameDir(workDir, expected) {
		return "", cli.Usagef("%s is not a subdirectory of the current work folder %s; run `git work adopt` from inside the work folder being adopted into", target, expected)
	}
	return workDir, nil
}

// sameDir reports whether two paths name the same directory, tolerating
// symlinked path spellings (e.g. macOS /tmp vs /private/tmp).
func sameDir(a, b string) bool {
	if r, err := filepath.EvalSymlinks(a); err == nil {
		a = r
	}
	if r, err := filepath.EvalSymlinks(b); err == nil {
		b = r
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// adoptOne registers the worktree at target in workDir's state file,
// creating the state file first when workDir is entirely unmanaged.
// Re-adopting an already-registered repo is a no-op with a message.
func adoptOne(w io.Writer, l layout.Layout, workDir, target string) error {
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s: not an existing directory", target)
	}
	repoName, branch, err := validateWorktree(l, target)
	if err != nil {
		return err
	}
	if base := filepath.Base(target); base != repoName {
		return fmt.Errorf("%s: directory name %q does not match its repo %q - the state file references worktrees by repo name; rename the directory first", target, base, repoName)
	}
	// Refuse to record the repo's default branch as a work branch. Recording is
	// not itself destructive, so a repo whose default branch cannot be
	// determined is a warning rather than a refusal - teardown re-checks before
	// it deletes anything, which is where the cost actually lands.
	if main, merr := l.RepoMain(repoName); merr != nil {
		fmt.Fprintf(os.Stderr, "git-work: warning: %s: could not locate the primary clone to check the default branch: %v\n", repoName, merr)
	} else if problem, perr := repo.DefaultBranchProblem(main, branch); perr != nil {
		fmt.Fprintf(os.Stderr, "git-work: warning: %s: could not determine the default branch: %v\n", repoName, perr)
	} else if problem != "" {
		return fmt.Errorf("%s: %s", target, problem)
	}
	st, err := state.Load(workDir)
	created := false
	if os.IsNotExist(err) {
		st = bootstrapState(filepath.Base(workDir), branch)
		created = true
	} else if err != nil {
		return err
	}
	// An already-registered repo whose branch still matches is the no-op it
	// always was. When it no longer matches, re-adopting re-records it: state
	// is written once at new/add/adopt time and nothing else re-syncs it, so
	// this is the supported way to say "this is the branch now" after
	// switching by hand. Every command that acts on the recorded branch
	// refuses while the two disagree (repo.WorktreeBranchProblem) and names
	// this command as the remedy.
	for i, r := range st.Repos {
		if r.Name != repoName {
			continue
		}
		if r.Branch == branch {
			fmt.Fprintf(w, "%s is already registered in %s on branch %s; nothing to do\n",
				repoName, filepath.Join(workDir, state.FileName), branch)
			return nil
		}
		st.Repos[i].Branch = branch
		st.Repos[i].BranchSource = "existing"
		if err := st.Save(workDir); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s: recorded branch %s (was %s); run `git work refresh` to update the generated files\n",
			repoName, branch, r.Branch)
		return nil
	}
	st.Repos = append(st.Repos, state.Repo{Name: repoName, BranchSource: "existing", Branch: branch})
	if err := st.Save(workDir); err != nil {
		return err
	}
	if created {
		if st.NoTicket {
			fmt.Fprintf(w, "Created %s (ticketless; the folder name %s matches no ticket pattern)\n",
				filepath.Join(workDir, state.FileName), st.Title)
		} else {
			fmt.Fprintf(w, "Created %s (ticket %s inferred from the folder name)\n",
				filepath.Join(workDir, state.FileName), st.TicketID)
		}
	}
	fmt.Fprintf(w, "Registered %s (branch %s); run `git work refresh` to update the generated files\n",
		repoName, branch)
	return nil
}

// validateWorktree checks that target is a linked git worktree of a managed
// primary clone under the repos root, returning the repo name and the branch
// checked out in the worktree.
func validateWorktree(l layout.Layout, target string) (repoName, branch string, err error) {
	fi, err := os.Stat(filepath.Join(target, ".git"))
	if err != nil {
		return "", "", fmt.Errorf("%s: not a git worktree (no .git)", target)
	}
	if fi.IsDir() {
		// Converting a clone into a worktree is out of scope for one command -
		// `repo adopt` moves the directory, so fusing the two would need a
		// rollback for the window between the move and the worktree existing.
		// Naming the two steps and their order is the whole remedy, and every
		// other refusal in this function names its remedy too.
		name := filepath.Base(target)
		return "", "", fmt.Errorf("%s: a full git clone, not a worktree of a managed primary.\n"+
			"  To bring it into the managed layout, run these from the work folder, in order:\n"+
			"    git work repo adopt ./%s    # moves the clone under the repos root\n"+
			"    git work add --repos %s     # creates a worktree here, on the work branch\n"+
			"  The first step moves the directory, so do not run it from inside ./%s.",
			target, name, name, name)
	}
	common, err := repo.CommonGitDir(target)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", target, err)
	}
	if resolved, rerr := filepath.EvalSymlinks(common); rerr == nil {
		common = resolved
	}
	// A managed worktree's common git dir is <repos-root>/<repo>/<primary>/.git.
	root, rerr := filepath.EvalSymlinks(l.ReposRoot)
	if rerr != nil || filepath.Base(common) != ".git" {
		return "", "", notManaged(target, common, l.ReposRoot)
	}
	primary := filepath.Dir(common)
	repoDir := filepath.Dir(primary)
	if filepath.Dir(repoDir) != root {
		return "", "", notManaged(target, common, l.ReposRoot)
	}
	repoName = filepath.Base(repoDir)
	dir, perr := layout.PrimaryDir(repoDir)
	if perr != nil {
		return "", "", fmt.Errorf("%s: %s is not a managed primary clone (%v); run `git work repos adopt %s` first", target, repoDir, perr, repoName)
	}
	if dir != filepath.Base(primary) {
		return "", "", fmt.Errorf("%s: %s's .git pointer names primary %q but the worktree belongs to %q; run `git work repos adopt %s` to normalize", target, repoDir, dir, filepath.Base(primary), repoName)
	}
	branch, err = repo.CurrentBranch(target)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", target, err)
	}
	return repoName, branch, nil
}

// notManaged is the rejection for a git dir outside the repos root.
func notManaged(target, gitDir, reposRoot string) error {
	return fmt.Errorf("%s: not a worktree of a managed primary (git dir %s is not under the repos root %s)", target, gitDir, reposRoot)
}

// bootstrapState builds a fresh state for an unmanaged work folder: ticket
// id and slug are inferred from the folder name, the ticket id stands in
// for the title (no provider fetch), and the first adopted worktree's
// branch becomes the work branch. A folder name without a ticket-shaped
// prefix bootstraps a ticketless state (mirroring `new --no-ticket`): the
// folder name is the title and slug, and refresh writes no TICKET.md.
func bootstrapState(folder, branch string) *state.State {
	ticket, slug := inferTicket(folder)
	if ticket == "" {
		return &state.State{Title: folder, Slug: folder, Branch: branch, NoTicket: true}
	}
	return &state.State{TicketID: ticket, Title: ticket, Slug: slug, Branch: branch}
}

// ticketRe matches a leading ticket id: one or more "<word>-" segments
// followed by an issue number, so multi-segment prefixes like "dev-tools-1"
// work too.
var ticketRe = regexp.MustCompile(`^((?:[A-Za-z][A-Za-z0-9]*-)+[0-9]+)(?:-(.+))?$`)

// inferTicket splits a work-folder name into ticket id and slug: a leading
// "<prefix>-<number>" is the ticket ("PROJ-3-git-work" -> "PROJ-3" +
// "git-work", "dev-tools-12-fix" -> "dev-tools-12" + "fix"); a name without
// one is not a ticket at all (empty ticket and slug - the ticketless case).
func inferTicket(folder string) (ticket, slug string) {
	if m := ticketRe.FindStringSubmatch(folder); m != nil {
		return m[1], m[2]
	}
	return "", ""
}
