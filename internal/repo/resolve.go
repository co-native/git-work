package repo

import (
	"fmt"
	"strings"
)

// ResolveOpts controls ResolveWorkBranch.
type ResolveOpts struct {
	ReuseExisting  bool // reuse a single existing match without prompting
	AlwaysNew      bool // never reuse ticket matches; create the new branch
	NonInteractive bool // never prompt; ambiguity is an error
	// Choose prompts the user to pick one of options (the last option is
	// always the "create new" sentinel). Only called on the interactive
	// paths, i.e. never when NonInteractive, ReuseExisting, or AlwaysNew
	// short-circuits the decision.
	Choose func(title string, options []string) (string, error)
}

// BranchChoice is the outcome of ResolveWorkBranch.
type BranchChoice struct {
	Create bool   // true: create Branch with -b; false: check out existing Branch
	Branch string // the branch to create or reuse
	Source string // "new" | "existing" (recorded as state.Repo.BranchSource)
}

// ResolveWorkBranch decides whether to create branch anew in mainDir or
// reuse an existing branch matching ticketID. name is the repo's display
// name, used in prompts and errors.
//
//   - If branch itself already exists it is always reused - creating it
//     would fail - even under AlwaysNew.
//   - AlwaysNew skips ticket matching and creates branch.
//   - No ticket matches: create branch.
//   - One match: reused when ReuseExisting or NonInteractive; otherwise the
//     user picks via Choose.
//   - Multiple matches: a hard error listing them when NonInteractive or
//     ReuseExisting (never a silent first-pick); otherwise the user picks.
func ResolveWorkBranch(mainDir, name, ticketID, branch string, opts ResolveOpts) (BranchChoice, error) {
	exists, err := LocalBranchExists(mainDir, branch)
	if err != nil {
		return BranchChoice{}, err
	}
	if exists {
		return BranchChoice{Create: false, Branch: branch, Source: "existing"}, nil
	}
	if opts.AlwaysNew {
		return BranchChoice{Create: true, Branch: branch, Source: "new"}, nil
	}
	matches, err := BranchesMatching(mainDir, ticketID)
	if err != nil {
		return BranchChoice{}, err
	}
	if len(matches) == 0 {
		return BranchChoice{Create: true, Branch: branch, Source: "new"}, nil
	}
	if len(matches) > 1 && (opts.NonInteractive || opts.ReuseExisting) {
		return BranchChoice{}, fmt.Errorf("%s: multiple branches match %s:\n  %s\nre-run interactively to pick one, or pass --always-new",
			name, ticketID, strings.Join(matches, "\n  "))
	}
	if opts.ReuseExisting || opts.NonInteractive {
		return BranchChoice{Create: false, Branch: matches[0], Source: "existing"}, nil
	}
	createOpt := "(create new: " + branch + ")"
	choice, err := opts.Choose(
		fmt.Sprintf("%s: existing branch found - reuse or create %q?", name, branch),
		append(matches, createOpt))
	if err != nil {
		return BranchChoice{}, err
	}
	if choice == createOpt {
		return BranchChoice{Create: true, Branch: branch, Source: "new"}, nil
	}
	return BranchChoice{Create: false, Branch: choice, Source: "existing"}, nil
}
