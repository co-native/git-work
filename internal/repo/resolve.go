package repo

import (
	"errors"
	"fmt"
	"strings"
)

// ResolveOpts controls ResolveWorkBranch.
type ResolveOpts struct {
	// Explicit says the operator named the branch (--branch), which is the
	// whole answer: reused if it exists, created otherwise, no ticket
	// matching and nothing to choose.
	Explicit bool
	// NonInteractive never prompts: any candidate is an error naming them
	// all, so the caller can answer with --branch.
	NonInteractive bool
	// Choose prompts the user to pick one of options (the last option is
	// always the "create new" sentinel). Only called on the interactive
	// path, i.e. never when Explicit or NonInteractive decides.
	Choose func(title string, options []string) (string, error)
}

// CheckoutMode says how a worktree obtains its branch. The zero value is
// the local checkout, which fails rather than creates when the branch is
// missing - the safe default for an unset choice.
type CheckoutMode int

const (
	CheckoutLocal  CheckoutMode = iota // check out the existing local Branch
	CheckoutRemote                     // create Branch tracking origin/Branch, which exists only there
	CheckoutNew                        // create Branch from the primary's HEAD
)

// BranchChoice is the outcome of ResolveWorkBranch.
type BranchChoice struct {
	Mode   CheckoutMode
	Branch string // the branch to create or reuse
	Source string // "new" | "existing" (recorded as state.Repo.BranchSource)
}

// Checkout adds a worktree at path on the chosen branch, obtaining the
// branch the way Mode says.
func (c BranchChoice) Checkout(mainDir, path string) error {
	switch c.Mode {
	case CheckoutRemote:
		return AddTrackingWorktree(mainDir, path, c.Branch)
	case CheckoutNew:
		return AddWorktree(mainDir, path, c.Branch, "", true)
	default:
		return AddWorktree(mainDir, path, c.Branch, "", false)
	}
}

// reuse is the choice that checks out an existing candidate.
func reuse(c Candidate) BranchChoice {
	mode := CheckoutLocal
	if c.Remote {
		mode = CheckoutRemote
	}
	return BranchChoice{Mode: mode, Branch: c.Branch, Source: "existing"}
}

// ResolveWorkBranch decides whether to create branch anew in mainDir or
// reuse an existing branch matching ticketID. name is the repo's display
// name, used in prompts and errors.
//
//   - If branch itself already exists it is always reused: locally, because
//     creating it would fail; on origin only, because a second branch of
//     that name would be rejected at push time. The remote case checks out
//     a local branch tracking origin/<branch>.
//   - Explicit: create branch; existing branches for the ticket are not
//     consulted, since the operator has already answered.
//   - No candidate matches ticketID (locally or on origin): create branch.
//   - Candidates and NonInteractive: an error listing them, never a silent
//     pick - the remedy is --branch, or an interactive run.
//   - Candidates, interactive: the user picks one, or "create new".
func ResolveWorkBranch(mainDir, name, ticketID, branch string, opts ResolveOpts) (BranchChoice, error) {
	exists, err := LocalBranchExists(mainDir, branch)
	if err != nil {
		return BranchChoice{}, err
	}
	if exists {
		return BranchChoice{Mode: CheckoutLocal, Branch: branch, Source: "existing"}, nil
	}
	remote, err := RemoteBranchExists(mainDir, branch)
	if err != nil {
		return BranchChoice{}, err
	}
	if remote {
		return BranchChoice{Mode: CheckoutRemote, Branch: branch, Source: "existing"}, nil
	}
	create := BranchChoice{Mode: CheckoutNew, Branch: branch, Source: "new"}
	if opts.Explicit {
		return create, nil
	}
	candidates, err := BranchesMatching(mainDir, ticketID)
	if err != nil {
		return BranchChoice{}, err
	}
	if len(candidates) == 0 {
		return create, nil
	}
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.String()
	}
	if opts.NonInteractive {
		return BranchChoice{}, fmt.Errorf("%s: branches matching %s already exist:\n  %s\n"+
			"pass --branch <name> to reuse one (without the origin/ prefix) or to create %q regardless, or re-run interactively to pick",
			name, ticketID, strings.Join(names, "\n  "), branch)
	}
	createOpt := "(create new: " + branch + ")"
	choice, err := opts.Choose(
		fmt.Sprintf("%s: existing branch found - reuse or create %q?", name, branch),
		append(names, createOpt))
	if err != nil {
		return BranchChoice{}, err
	}
	for i, c := range candidates {
		if choice == names[i] {
			return reuse(c), nil
		}
	}
	return create, nil
}

// Planned is one repo's resolved work branch, ready to check out.
type Planned struct {
	Name   string
	Main   string // the repo's primary clone
	Choice BranchChoice
}

// PlanOpts controls ResolveWorkBranches.
type PlanOpts struct {
	ResolveOpts
	MainOf func(name string) (string, error) // the primary clone of a repo
	Warn   func(msg string)                  // per-repo notices that do not stop the run
}

// ResolveWorkBranches resolves a work branch for every named repo before
// any worktree exists, so a refusal in one repo - candidates under
// NonInteractive, the default branch, a repo that is not cloned - is
// reported alongside the others in a single run, and nothing needs rolling
// back. Each repo is fetched first; a failed fetch is a warning and
// resolution continues with the local refs (a local-only repo, or being
// offline). An undeterminable default branch is likewise a warning: nothing
// is destroyed by recording it, and teardown re-checks before deleting.
func ResolveWorkBranches(names []string, ticketID, branch string, opts PlanOpts) ([]Planned, error) {
	var plan []Planned
	var errs []error
	for _, name := range names {
		main, err := opts.MainOf(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := Fetch(main); err != nil {
			opts.Warn(fmt.Sprintf("fetch %s: %v (continuing with local refs)", name, err))
		}
		choice, err := ResolveWorkBranch(main, name, ticketID, branch, opts.ResolveOpts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// A work branch is never the repo's default branch. Reachable via
		// --branch, or a reused branch whose name matches the ticket id.
		if problem, perr := DefaultBranchProblem(main, choice.Branch); perr != nil {
			opts.Warn(fmt.Sprintf("warning: %s: could not determine the default branch: %v", name, perr))
		} else if problem != "" {
			errs = append(errs, fmt.Errorf("%s: %s", name, problem))
			continue
		}
		plan = append(plan, Planned{Name: name, Main: main, Choice: choice})
	}
	return plan, errors.Join(errs...)
}
