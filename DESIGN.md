# git-work - Design

`git-work` manages work that spans one or more repositories using git worktrees. It keeps
a single primary clone per repo under a _repos root_, and lays out per-ticket _work
folders_ (each a sibling collection of worktrees) under a _work root_. It installs as
`git-work` on `PATH` so git dispatches it as `git work <command>`.

- Module: `github.com/co-native/git-work`
- Binary: `git-work` → invoked as `git work <command>`
- Config: `${XDG_CONFIG_HOME:-~/.config}/git-work/config.yaml`

## Architecture

`main.go` is a thin argv dispatcher: `os.Args[1]` selects a `command/<cmd>` package whose
`Run(args []string) int` does the work, and `main` calls `os.Exit` on the returned code.
Each command package owns its own flag parsing. Shared building blocks live under
`internal/`:

| Package    | Responsibility                                                                                                                                                                                                                                                                  |
| ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `config`   | Load/save the global config (YAML, or JSON by extension); defaults; dot-path key access (in `command/config`).                                                                                                                                                                  |
| `state`    | Per-work-folder metadata (`.git-work.yaml`/`.json`): read, write (atomic), and `FindUp` from the cwd.                                                                                                                                                                           |
| `layout`   | Resolves on-disk paths from the repos/work roots (`RepoMain`, `WorkDir`, `Worktree`, `ListRepos`). The primary clone folder is discovered by parsing the parent `.git` pointer file (`PrimaryDir`); a missing/malformed pointer is an error.                                    |
| `repo`     | Git plumbing: clone, fetch, ff-only pull, add/remove/repair worktrees, branch existence/merge/patch-equivalence checks, rebase-onto, branch deletion, branch resolution, HEAD classification (`ReadHead`) and the shared worktree/state branch check (`WorktreeBranchProblem`). |
| `provider` | Resolve a ticket id to a provider and dispatch on its type (`jira`/`github`) to fetch title/description.                                                                                                                                                                        |
| `template` | Render the managed blocks of generated files (`TICKET.md`, `README.md`, `CLAUDE.md`), including the embedded work folder contract (`workfolder.md`) and the operator/per-repo overlays inlined into `CLAUDE.md`.                                                                |
| `tui`      | Interactive prompts: select, multi-select, input, confirm; TTY detection.                                                                                                                                                                                                       |
| `teardown` | Per-repo safety checks (`CheckRepo`) and teardown (`TeardownRepo`), shared by `done` and `rm`; mutates and saves state as each repo is fully torn down.                                                                                                                         |

## Command reference

```
git work <command> [args]

commands:
  repo <subcommand>                 manage primary repos (clone, adopt, list, pull); "repos" also works
  clone [opts] [--] <repo> [<dir>]  clone a repo into the primary repos root
  new <ticket-id|name>              start a new work folder with worktrees
  add                               add another repo to the current work folder
  adopt <dir>                       register an existing worktree dir in the current work folder
  refresh                           pull the ticket repos' primaries, re-render generated files
  rebase <repo> | --all             pull the repos' primaries, rebase work branches onto their defaults
  push <repo> | --all               push work branches with unpushed commits to origin
  integrate <repo> | --all          move a work branch's un-integrated commits onto its default branch
  git <repo> | --all -- <args>      run a git command in each repo's worktree
  done <dir>                        tear down a work folder
  rm <repo>                         remove one repo from the current work folder
  config <subcommand>               view/edit the global configuration
  version                           print the git-work version
```

Flags are **not** listed here. Every command and subcommand answers `-h`/`--help` with its
own flags and semantics, printed to stdout with exit 0, and `git work help <command>` (or
`git work help repo clone`) reaches the same text. That help is generated from a
descriptor that a conformance test checks against each command's real flag set, so it
cannot fall out of step the way a hand-maintained table here would. This section documents
the _why_ - the semantics behind the flags - and leaves the _what_ to the tool.

### The help contract

git-work once had three different answers to `-h`: some commands printed real usage and
exited 0, some fell through to Go's `flag` package (`Usage of new:` on stderr, exit 1),
and the rest rejected it as a bad argument - while three `config` subcommands ignored it
and ran, so `git work config edit -h` opened `$EDITOR` on the config file and created it
if absent. One contract now covers all command paths, and a conformance test rather than
review is what holds it:

- `-h` and `--help` are identical: the command's usage to **stdout**, exit **0**.
  Recognised anywhere in the argument list, with the scan stopping at the first `--` (so
  `git work git demo -- log -h` still forwards `-h` to git).
- The bare word `help` is a help token **only** where it already occupies a subcommand
  slot - top level, `repo`, `config` - because leaf commands take names positionally and a
  repo really can be called `help`.
- Exit codes are uniform: **0** success (a help request is a success), **2** malformed
  invocation, **1** the invocation was fine and the work failed. A usage error prints the
  error and then the command's usage, on stderr.
- Help is reachable two ways for every path: `git work <cmd> -h` and
  `git work help <cmd>`, the latter also resolving `git work help repo clone`.
- Usage text lives in a `cli.Command` descriptor per command. `main.go` and the group
  dispatchers build their index from the same descriptors that answer `-h`, so a listing
  cannot describe a command differently from the command itself.

`internal/cli/clitest.Check` enforces this per package: a flag registered without being
documented, or documented without being registered, fails the build. Never declare
`-h`/`--help` in a descriptor's `Flags` or register it on a `FlagSet` - the renderer
appends it.

### `repo <subcommand>`

Repo-level operations on the primary clones under the repos root. Day-to-day commands
never mutate the repo layout as a side effect; `repo adopt` is the single normalizer for
layout drift.

#### `repo clone [opts] [--] <repo> [<dir>]`

Clones `<repo>` into the repos root as the primary clone; the primary folder is named
after the upstream default branch (detected from `origin/HEAD`). Top-level
`git work clone` is a thin alias, and `git work repos` is accepted for `repo` everywhere.
`<dir>` overrides the repo directory name (derived from the URL otherwise); a `<dir>` that
itself looks like a repo URL is rejected - clone takes exactly one repo, run it once per
URL. Any git-clone options must come **before** a `--` separator - without `--`, an arg
starting with `-` is rejected as ambiguous:

```
git work clone git@github.com:org/api.git
git work clone --depth 1 -- git@github.com:org/api.git api
```

#### `repo adopt <dir>`

Adopts an existing plain clone into the managed layout
(`<repos-root>/<repo>/<default-branch>`): checks out the default branch (a primary must
sit on it - pulls and rebases target it), moves the clone, writes the parent `.git`
pointer file, sets `core.worktree ..`, and repairs existing worktrees' `.git`
back-pointers (`git worktree repair`). `<dir>` is a path, or a bare name looked up under
the repos root and under the repos root's parent (where plain clones live, e.g.
`~/dev/<dir>`); a name present in both places is ambiguous and needs a path.

Re-running against an already-managed repo normalizes drift instead: renames the primary
folder to match the default branch, rewrites the pointer file, sets `core.worktree`,
checks out the default branch when the primary was left on another one, and repairs
worktree back-pointers. A conforming repo is a no-op with a clear message. Linked
worktrees and already-managed parent dirs are rejected (clone-to-worktree conversion is
out of scope).

#### `repo list`

Lists every managed repo under the repos root: default branch, linked worktrees (path and
branch), and layout drift findings (missing/malformed pointer file, primary folder not
named after the default branch, primary checked out on a non-default branch,
`core.worktree` not `..`). Drift is reported, never fixed - run `repo adopt` to normalize.

#### `repo pull`

Fast-forward-pulls every managed repo's default branch in its primary clone. The default
branch is pulled explicitly: a primary left on another branch gets its default branch
fast-forwarded via fetch without the working tree being touched, so the rebase/merge
target keeps freshening either way. A pull that would need a merge or rebase (a diverged
branch) fails for that repo without touching its tree; failures are reported per repo and
make the command exit nonzero after the loop.

### `new <ticket-id|name> [flags]`

Starts a work folder: resolves repos, optionally fetches ticket details from a provider,
creates the folder under the work root, adds a worktree (and branch) per repo, and writes
the generated files. Rolls back (removes added worktrees and the folder) on any failure
after the folder is created. A failed per-repo `git fetch` (e.g. a local-only repo with no
usable remote, or being offline) is a warning, not an error - branch resolution continues
with the local refs. `add` behaves the same way.

**Ticketless mode** (`--no-ticket`/`-n`): the positional argument is a plain name instead
of a ticket id - no provider lookup, no `TICKET.md`; the folder and branch are named from
the slugified name (state records `no_ticket:
true`). Without the flag, a ticket id that
matches no configured provider is an error suggesting `--no-ticket` (when providers are
configured) - there is no silent fallback.

**Repo selection.** `--repos` is a **replacement**, not an addition: naming repos gets
exactly those, and `add_by_default` is consulted only when nothing was named. A flag that
could add but never narrow would be the worse flag. With nothing named and a TTY, the
multi-select opens with the `add_by_default` repos already ticked - the prompt still runs,
so the operator confirms rather than discovering afterwards which worktrees were created.
With `--non-interactive` and nothing named, the default set is used directly; that
combination used to be an unconditional usage error, and is now one only when no repo has
`add_by_default` set, since then nothing can answer the question. Config entries naming
repos that are not cloned locally are ignored rather than erroring - a config synced
across machines will routinely mention repos a given machine does not have.

**Branch.** The work branch defaults to `<ticket>-<slug>` (cased per the provider) and
`--branch` names it outright. Existing branches for the ticket, local or on origin, are
handled by [Branch resolution](#branch-resolution); every repo is resolved before the
folder is created, so a refusal reports all repos in one run and creates nothing.

### `add [flags]`

Run from inside a work folder (located via `FindUp`). Adds one or more repos - new
worktrees + branches - to the current ticket and re-aggregates the generated
`README.md`/`CLAUDE.md`. The folder's work branch is the default for each added repo and
`--branch` overrides it for the repos being added (recorded per repo in `repos[].branch`);
existing branches are handled by [Branch resolution](#branch-resolution), resolved for
every repo before the first worktree is added.

### `adopt <dir>`

Run from inside a work folder with an explicit directory argument. The target must be a
direct subdirectory of the work folder the command runs in (the folder `FindUp` locates
from the cwd, or the cwd itself when nothing is managed yet) - adopting into some other
folder's state file is refused. Registers that one existing worktree subdirectory in the
folder's `.git-work.yaml`, creating the state file first when the folder is entirely
unmanaged (ticket id and slug inferred from the folder name - multi-segment prefixes like
`dev-tools-12-fix` work; a name with no ticket-shaped prefix bootstraps a ticketless
state, mirroring `new --no-ticket`, so refresh writes no TICKET.md. The first adopted
worktree's branch becomes the work branch). Nothing moves and nothing is rewritten in git.

Re-adopting an already-registered repo whose branch still matches is a no-op with a
message. When the branch no longer matches, re-adopting **re-records it** - this is the
supported way to say "this is the branch now" after switching by hand, and the remedy
every branch-drift refusal names (see
[Worktree/state branch agreement](#worktreestate-branch-agreement)). It is deliberately
targeted rather than folded into `refresh`: `refresh` runs over every repo as a matter of
routine, so re-syncing there would silently bless drift across the whole folder and defeat
the guard. `validateWorktree` reads the branch before any state update, so a detached
worktree (mid-rebase, mid-bisect) cannot be recorded.

The target must be a linked worktree of a managed primary clone under the repos root, and
its directory name must match the repo name (the state file references worktrees by repo
name). A full clone is rejected (clone-to-worktree conversion is out of scope), as is a
worktree whose primary has layout drift (`repo adopt` first).

### `refresh [flags]`

Run from inside a work folder. First ff-only-pulls each ticket repo's default branch in
its primary clone (explicitly, so a primary left on another branch still freshens the
default branch; a failed pull, e.g. a diverged branch, being offline, or layout problems,
is a warning, never an abort), then re-renders the managed blocks of `TICKET.md`,
`README.md`, and `CLAUDE.md` from current state - re-fetching provider details for the
title/description (keeps the existing title if the fetch fails). Ticketless work folders
skip the provider fetch and `TICKET.md`.

### `rebase <repo> | --all [flags]`

Run from inside a work folder. Scope is one repo (`<repo>`) or every repo in the work
folder (`-a`/`--all`); bare `rebase` is a usage error, same shape as `push` and
`integrate`. Scope is always explicit across the three commands that take one - rebasing
branches in repos you did not name rewrites history you did not mean to touch, so an
implicit `--all` is not worth the keystroke it saves.

Per in-scope repo: ff-only-pull the repo's default branch in its primary clone
(explicitly, so a primary left on another branch still freshens the rebase target; a
failed pull is a warning

- the rebase proceeds against the local default branch), then rebase the branch checked
  out in the repo's worktree onto the repo's default branch. Repos are processed
  independently; problems are collected and reported at the end (nonzero exit). A dirty
  worktree is skipped and reported as a problem. On conflict, that repo's rebase is left
  in progress for normal `git rebase --continue`/`git rebase --abort` resolution - other
  repos still proceed.

`--no-pull` mirrors `refresh --no-pull`: the rebase still runs, just against whatever the
local default branch already points at.

`-n`/`--dry-run` follows `integrate`'s model - the pull still happens (unless
`--no-pull`), so the report reflects the same target a real run would use. Per repo it
prints the commits that would be replayed (short SHA + subject, `git cherry` semantics, so
commits already patch-present in the target are excluded exactly as rebase would drop
them) onto the named target branch, reading the branch from the **worktree** rather than
from state because that is what a real run rebases. With nothing to replay the branch ends
up at the target either way, so the report distinguishes the two remaining cases: already
up to date, or a fast-forward. A dirty worktree is reported as a problem here too, exiting
nonzero - `-n` predicts the real run's outcome rather than describing a rebase that would
in fact be skipped.

### `push <repo> | --all [flags]`

Pushes work branches to origin - the multi-repo equivalent of `cd`-ing into each worktree
and running `git push`. Scope is one repo (`<repo>`) or every repo in the work folder
(`-a`/`--all`); bare `push` is a usage error, same shape as `integrate`.

A repo is pushed when its work branch has **unpublished commits** - commits origin does
not have yet, on either ref it could hold them. Leg one asks whether `origin/<default>`
lacks any of the branch's commits (`git cherry` patch equivalence, like `integrate`); leg
two whether `origin/<branch>` does (plain `git rev-list` ancestry - a rebased or amended
branch holds the same patches under new SHAs, and the ref still needs the push, with `-f`,
to move). Both reference points are origin's, deliberately: push's whole job is origin,
and the local default branch - leg one's original reference - goes quiet the moment an
integrate runs, which skipped exactly the push that would first publish the branch of an
operator who integrates before pushing. The local default branch stands in for leg one
only when origin has no default branch at all (a local-only repo). The report counts leg
two when non-empty, since that is what the push will actually transfer, and falls back to
leg one (a first push, or the fully-pushed repo staying in scope to no-op via git's
"Everything up-to-date", matching normal `git push`). A branch with nothing origin lacks -
fully published, or fully integrated with its integration pushed - is skipped with a note
unless `-e`/`--allow-empty`, which pushes it anyway (as a remote placeholder when new);
the predicate applies uniformly - naming a repo does not bypass it. An untouched branch is
an ancestor of origin's default, so it counts zero on both legs and `--all` cannot litter
origin with placeholder branches.

Each push runs `git push -u origin <branch>` from the repo's worktree
(`repo.PushBranchUpstream`), targeting the branch recorded in state - not whatever the
worktree has checked out. `-u` is explicit so a first push establishes `@{upstream}`
regardless of the operator's `push.autoSetupRemote` (`done`'s unpushed check and later
bare `git push` key off it). `-f`/`--force` adds `--force-with-lease` - never bare
`--force`; a lease rejection (the remote moved beyond our remote-tracking refs) is a
failure to inspect, not something to override. Deliberately **no fetch** before pushing:
the lease _is_ the remote-tracking refs, and fetching right before the push would weaken
it. No dirty check either - normal `git push` ignores uncommitted changes.

Failure handling is `done`'s model, not `integrate`'s: pushes are independent, so per-repo
failures (e.g. a non-fast-forward rejection, reported with the rebase-or-`-f` remedy) are
collected while the rest keep going; exit 1 if any repo failed. A missing worktree or
locally-deleted branch is skipped with a note. Output is integrate-style:
`pushing N commit(s) to origin/<branch>` plus the short-SHA + subject detail, then a
`pushed` confirmation per repo. `-n`/`--dry-run` resolves scope and prints the same detail
with `would push` (force noted), pushing nothing.

#### `--main`

`--main` switches the command to push each in-scope repo's **default branch** instead of
its work branch - the multi-repo equivalent of `cd`-ing into each primary clone and
running a plain `git push`. It is a _replacement_, not an addition: work branches are
never touched, so publishing both is two runs (`push -a` and `push -a --main`). The
motivating case is `integrate` without `--push`, which leaves every local default branch
ahead of origin.

The mode is deliberately independent of the work branch and the worktree. It reads only
the primary clone (`git push origin <default>` via `repo.PushBranch`), so neither a
torn-down worktree nor a deleted work branch skips a repo - after `integrate` the commits
live on the default branch, which the default mode never touches: pushing the work branch
can bring `origin/<branch>` up to date, never `origin/<default>`. So the default branch
gets its own predicate: the commits in local `<default>` that are not on
`origin/<default>` (`git cherry`, same patch-equivalence semantics as everywhere else).
Zero means "nothing to push", skipped with a note unless `-e`/`--allow-empty`, which
pushes anyway (git then reports "Everything up-to-date"). A missing `origin/<default>`
cannot be measured, so the push simply proceeds and is reported as a new branch. Still
**no fetch** first, consistent with the work-branch mode: a stale `origin/<default>` costs
at most a redundant push attempt or a rejection that lands in the failure list, never a
wrong ref.

`-f`/`--force` combined with `--main` is a **usage error**, not a silent no-op: a default
branch is shared history and git-work never rewrites it. A rejection therefore points at
pulling the primary, not at `-f`. Scope, failure collection, dry-run, and output shape are
otherwise identical to the work-branch mode.

### `integrate <repo> | --all [flags]`

Moves a work branch's un-integrated commits onto its repo's local default branch - by
**fast-forward** where possible, cherry-pick otherwise - as an alternative to a lossy
`git merge --squash` integrating. Scope is one repo (`<repo>`) or every repo in the work
folder (`-a`/`--all`); a leading-dash `--all` parses fine (unlike `done`'s parser,
`integrate` doesn't reject a leading `-` first argument).

**Pre-flight is fully atomic across the whole scope, before any cherry-pick.** Per repo:
fetch (unless `--no-fetch`), verify the primary clone is clean and on its default branch,
fast-forward the local default branch to `origin/<default>`, then compute the
un-integrated commits (`repo.CommitsNotIn`) between the branch and the _local_ default
branch - not `origin/<default>`, since integrating without `--push` leaves local ahead of
origin and comparing against origin would re-list already-integrated commits. If any repo
in scope is dirty, off its default branch, can't fast-forward, or has 2+ un-integrated
commits without `--multi`, the whole run aborts listing every violation - zero
cherry-picks happen. A repo with no local branch or no worktree is skipped with a note
(nothing to integrate), not a violation.

**A PR-only repo - one whose resolved `integration` route is `pr` - is never integrated
locally, and how that is reported depends on how it entered scope.** Named explicitly it
is a pre-flight violation: the operator asked for something the configured route forbids,
and answering that with a silent exit 0 would be the worst outcome. Reached through
`-a`/`--all` it is skipped with a note and the run still succeeds, because declining it is
the appropriate thing to do there - a standing policy that failed every run would train
the exit code to be ignored. The check runs before any git work and is shared with the dry
run, so `-n` predicts the real run's exit code. There is deliberately **no override
flag**: `force` already means two different things in git-work (`done --force` bypasses
safety checks, `push --force` maps to `--force-with-lease`), the guarantee is against
accidents rather than adversaries, and `git work git <repo> -- cherry-pick <sha>` is the
honest bypass because it makes the bypass visible as one.

**`-n`/`--dry-run` runs that identical pre-flight - fetch, checks, and the fast-forward
included, deliberately, so the report measures exactly what a real run would do - and
skips only the cherry-picks (and any `--push`).** Per repo it prints
`would integrate N onto <default>` followed by the commit detail, and `would push` when
`--push` is set. Violations are reported the same as a real run (same abort message,
nonzero exit) so a dry run faithfully predicts "a real run would abort here" - but commit
detail is still printed for every repo that got far enough to compute it, including repos
tripping the 2+-commits guard (that violation fires after the commits are computed).

Both modes list the un-integrated commits one per line (short SHA + subject, oldest first)

- default output, no flag. A real run prints `integrating N onto <default>` plus the
  detail before picking, then the existing `N integrated` summary (the counts can differ
  when a pick turns out empty).

#### Fast-forward vs cherry-pick

Pre-flight picks the transfer mechanism per repo and both modes report it, in
`integrating N onto <default> (<mechanism>)` / `would integrate …`, because which one runs
decides whether the commits keep their SHAs.

A **fast-forward** is used exactly when the local default branch is an ancestor of the
work branch (`repo.IsMergedInto`) - nothing integrated onto the default branch since the
work branch diverged. The check runs _after_ pre-flight has fast-forwarded the default
branch to `origin/<default>`, so it asks about the same tip the transfer would move.
Transfer is then `git merge --ff-only` (`repo.FastForwardTo`) in the primary: a pointer
move that cannot conflict and creates no new objects.

That last part is the reason to prefer it. A cherry-pick rewrites the commits, so a work
branch already pushed to origin ends up holding _different objects_ than the default
branch with the same content - exactly the situation `done`'s patch-equivalence fallback
exists to paper over. A fast-forward keeps the SHAs, so `done`'s merged check passes by
plain ancestry and the pushed branch really does match.

The `--multi` guard applies to **both** mechanisms. A fast-forward carries no per-commit
risk, so the guard is not about mechanism there - it is about intent, and integrating
commits you had forgotten about is the same surprise either way, so it stays one rule
rather than two.

When a fast-forward is not possible the existing path runs unchanged: each repo's
un-integrated commits are cherry-picked oldest-first onto its local default branch
(`repo.CherryPick`); an empty pick (patch already present) is skipped, a conflicting pick
halts the entire run immediately - the primary's path, that a pick is in progress, and
that `git cherry-pick --abort` restores it are reported; no other repos are touched after
a halt. `--push` pushes the default branch to origin once a repo's commits integrate;
without `--push`, `integrate` warns that local is ahead of origin and must be pushed
before `done` will see the branch as contained (`done` compares against `origin/HEAD`, not
local). The tool never authors commit messages - `--multi` only overrides the 2+ guard, it
does not squash. The documented workflow is `integrate` → push (or `integrate --push`) →
`done`.

### `git <repo> | --all -- <git args>`

Runs one git command in each in-scope repo's **worktree** - the multi-repo equivalent of
`cd`-ing into each and running it by hand. Scope is one repo (`<repo>`) or every repo
(`-a`/`--all`), the same rule as `push`, `integrate` and `rebase`.

Everything after the first `--` is passed to git **verbatim** and is never inspected, so
aliases and flags behave exactly as they do in a single repo:

```
git work git -a -- st
git work git -a -- log --oneline -5
git work git api -- diff --stat
```

The `--` is **required**, and the parser splits on it rather than using the leading-dash
partitioning `push` and `integrate` share - that approach would swallow the git command's
own flags (`--oneline`, `-5`) as git-work's. Making `--` optional would mean
`git work git -a st` works while `git work git -a log --oneline` silently misbehaves, so a
missing separator is an error, reported _before_ the scope rules: without `--` there is no
way to tell a repo name from a git command, and "give a `<repo>` or `--all`, not both"
would be a useless answer to `git work git -a status`.

Output is **not** captured or reformatted. The child inherits git-work's own
stdin/stdout/stderr, so git still sees a TTY and keeps its color, formatting and pager
exactly as it would in one repo; only a `=== <repo> ===` header is printed before each
invocation so the blocks stay attributable. Pagers therefore fire per repo - a passthrough
passes through.

This command is deliberately **exempt from the worktree/state branch check** every other
branch-acting command applies (see
[Worktree/state branch agreement](#worktreestate-branch-agreement)). It runs against
whatever the worktree has checked out, which is exactly what makes
`git work git -a -- status` the tool for _investigating_ drift; refusing there would break
it precisely when it is needed.

Failure handling is `done`'s model: a missing worktree is skipped with a note, per-repo
failures are collected while the rest keep going, and the run exits nonzero if any repo
did. Note that a nonzero exit is reported for _any_ git command that exits nonzero,
including where that is a normal answer (`git diff --quiet` on a dirty tree) - git-work
cannot tell those apart, so it reports the code and leaves the judgment to the operator.

### `done <dir> [flags]`

Tears down a work folder. `<dir>` is a bare name (resolved under the work root) or a path.
**Resumable and safety-checked:** per-repo checks and teardown run independently, missing
worktrees/branches are skipped with a note, and all failures are collected and reported at
the end (nonzero exit, folder kept).

**Run it from outside the folder being torn down.** Nothing stops you running it from
inside - on Unix the removal succeeds and exits 0, leaving your shell in a directory that
no longer exists: `pwd` keeps printing the old path, `ls` comes back empty, and anything
that resolves the working directory starts failing until you `cd` elsewhere. The same is
true from inside one of the repo worktrees. Windows is stricter about deleting a directory
that is a running process's working directory, so there the teardown is likely to fail
partway instead; either way, `cd` out first.

Safety checks (per repo): uncommitted changes, unpushed commits, and - when deleting
branches - whether the branch is merged into the remote default branch (from
`origin/HEAD`). When the ancestry check says "not merged" it falls back to patch
equivalence (`repo.IsPatchEquivalent`), so squash-merged branches count as merged (labeled
"squash-merged" in output, and force-deleted locally since `git branch -d` would refuse
them). `IsPatchEquivalent` finishes with a `git merge-tree --write-tree` backstop: if
merging the branch into the target produces the target's tree unchanged, the branch is
contained even when it arrived as a mix of squashed and individually-cherry-picked commits
(a shape the per-commit-cherry and single-squash-probe checks alone can't see); a
conflicting merge, an old git without `--write-tree`, or any other failure falls back to
the earlier signals rather than surfacing an error. `--force` remains the path for
genuinely unmerged teardown. Teardown is strictly worktree-then-branch per repo, and
shared with `rm` via `internal/teardown` (`CheckRepo`/`TeardownRepo`); each repo that's
fully torn down is dropped from `.git-work.yaml` and saved immediately, so state stays
honest through a partial or blocked teardown - a re-run resumes exactly the repos still
outstanding, and the final repo count is captured before teardown mutates state.

The unpushed check treats a branch with no upstream as unpushed, so a branch that was
squash-merged and never pushed would otherwise be blocked. Bypass condition: when BOTH
`--delete-local` and `--delete-remote` are passed and the branch is verified contained in
the remote default branch - by ancestry (`repo.IsMergedInto`) or patch equivalence
(`repo.IsPatchEquivalent`), the same containment the merged check performs, computed once
per repo and shared - the unpushed problem is suppressed and a note is emitted instead. If
either delete flag is absent, or containment cannot be verified (any check error stays a
warning-plus-problem), the unpushed problem fires as usual. Patch equivalence is measured
over the full merge-base..branch diff, so a branch carrying commits beyond the squash
merge is not contained and still reports both the unpushed and the unmerged problem.

The work folder's "extra" files (anything at its root git-work didn't generate; repo
worktree dirs are skipped whole, not walked into) are computed up front, before any
teardown, and drive what happens to the folder at the end. No extra files, or
`--remove-all`, removes the whole folder including any leftovers. Otherwise,
interactively, after the delete-local/delete-remote prompts `done` lists the extra files
and offers a 3-way `tui.Select`: **Delete** (proceed as `--remove-all` would), **Keep**
(leave the files; state is still saved honestly per above), or **Cancel** (abort before
any checks or teardown run). Non-interactively, `--remove-all` means Delete and its
absence means Keep - the run tears down repos, keeps the folder and its leftover files,
prints the list, and **exits 0** (this replaced an earlier hard error with no
non-interactive way to keep the folder).

### `rm <repo> [flags]`

Removes one repo from the current work folder - `done` narrowed to a single repo. Run from
inside a work folder (located via `state.FindUp`); `<repo>` must already be in
`.git-work.yaml` or the command errors. Runs the same `teardown.CheckRepo` safety checks
and `teardown.TeardownRepo` teardown as `done` (uncommitted/unpushed/merged-or-contained,
`--force` to override, worktree-then-branch order), scoped to just that repo, then
re-aggregates `README.md`/`CLAUDE.md`'s managed blocks from the repos that remain. The
rest of the work folder - other repos, `TICKET.md`, the state file itself - is left
untouched; `rm` never removes the folder. Removing the last repo leaves an accurate
zero-repo `.git-work.yaml` and regenerated `TICKET.md`/ `README.md`/`CLAUDE.md` rather
than an invisible or half-deleted folder.

### `config <subcommand>`

View and edit the global configuration. See [Configuration](#configuration) for the schema
and [Dot-path keys](#dot-path-keys) for what `<key>` accepts; `git work config -h` lists
the subcommands.

### `version`

Prints one line identifying the build - version, VCS revision and date, Go version and
platform - to paste into a bug report. `git work --version` prints the same thing. Values
come from `runtime/debug.ReadBuildInfo`, so a `go install` from a tag reports the tag and
a build from a checkout reports its commit; `-v` is left unclaimed.

## The default-branch guard

**A work branch is never a repo's default branch.** `new`, `add` and `adopt` refuse to
record one, and `teardown` refuses to delete one - the latter not overridable by
`--force`.

The guard exists because the default branch satisfies every other teardown safety check
trivially, and does so _because_ it is the default branch. It is merged into `origin/HEAD`
by definition, so the merged check passes. It has an upstream with nothing unpushed, so
the unpushed check passes. A worktree sitting on it agrees with state, so
`WorktreeBranchProblem` passes. Nothing was dirty. The result was that
`done --delete-local --delete-remote` on a work folder whose state recorded `main` would
report zero problems and proceed to delete `main` locally and on origin - and every check
that should have caught it was, correctly, saying yes.

`repo.IsDefaultBranch` reads `origin/HEAD`, deliberately rather than
`repo.DefaultBranchName`: the latter falls back to the primary's checked-out branch, which
is the wrong answer in precisely the drifted layout where a worktree can hold the default
branch at all. A repo with no `origin/HEAD` (local-only, or a remote that never set it)
has no upstream default to read, so the primary's own branch stands in - a primary sits on
its default by design. That keeps "cannot tell" from becoming "cannot delete anything" for
repos with no remote.

The two halves are asymmetric on purpose. **Recording** is not destructive, so a repo
whose default branch cannot be determined produces a warning and proceeds. **Deleting**
is, so the same uncertainty is a refusal. The check runs before the worktree is removed,
leaving a refused repo exactly as it was, and `CheckRepo` reports it up front as well so
the operator learns before any repo is torn down rather than mid-run.

It is deliberately not `--force`-able. `--force` exists for judgment calls - "this branch
is not merged, delete it anyway" - and deleting a repo's default branch is not a judgment
call, it is a wrong state file. This mirrors the stance `push --main -f` already takes,
where combining them is a usage error rather than a silent no-op. The honest bypass, as
with `integrate`'s PR-only refusal, is `git work git <repo> -- branch -D <name>`, which
makes the bypass visible as one.

## Configuration

The global config lives at `${XDG_CONFIG_HOME:-~/.config}/git-work/config.yaml`. A missing
file means built-in defaults are used (no error). Format is chosen by extension: `.json` →
JSON, anything else → YAML. Saves are atomic (temp + rename) and create parent dirs.

### Path expansion

`paths.repos` and `paths.work` are expanded before use: a leading `~` (or `~/…`) becomes
the home directory, and `${VAR}`/`$VAR` are read from the environment. On Windows `\`
works as the separator too, so `~\dev\repos` is fine; on Unix a backslash stays an
ordinary filename character.

The result is cleaned to a native path, so a `~/dev/repos` written on Windows resolves to
`C:\Users\me\dev\repos` rather than carrying the operator's separators into every path
built from it and every error message that names one.

**An undefined variable is a hard error, never an empty expansion.** `os.ExpandEnv` would
turn a mistyped `${HOEM}/dev/repos` into `/dev/repos` - an absolute path rooted at `/`
that looks plausible and is silently wrong, which is the entire class of bug this exists
to prevent. `~user` paths are rejected for the same reason: passing them through untouched
is how the original bug behaved. Expansion is a single pass in a fixed order - `~` against
the literal, then variables - so a variable whose value contains `~` or `$` is not
rescanned.

Expansion happens at the point of use, in `Config.Layout()`, **not** at load. That is what
lets a config file keep the `~` the operator wrote: expanding on load would mean the next
`config set` writes the resolved absolute path back, silently destroying the portability
of a file synced across machines. `config get`/`config list` therefore show the raw value,
and the built-in defaults are themselves stored in `~` form for the same reason. Error
messages report the resolved path, since that is the directory actually searched.

### Structure

```yaml
paths:
  repos: ~/dev/repos # primary clones root (default ~/dev/repos)
  work: ~/dev/work # work folders root  (default ~/dev/work)

defaults: # optional; house rules every repo inherits
  integration: pr # local | pr (default local)

repos: # optional; per-repo overrides, keyed by name
  api:
    integration: local # overrides the house rule in either direction
    add_by_default: true # pre-selected in `git work new`'s picker

ticket_providers: # optional; first matching pattern wins
  - name: jira
    type: jira
    default: true
    patterns: [{ prefix: PROJ }]
  - name: github
    type: github
    patterns:
      - prefix: dev-tools
        repo: acme/api
```

| Field                  | Meaning                                                                                                                                                                                |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `paths.repos`          | Root holding one primary clone per repo. Default `~/dev/repos`.                                                                                                                        |
| `paths.work`           | Root holding per-ticket work folders. Default `~/dev/work`.                                                                                                                            |
| `defaults.integration` | House integration route, inherited by every repo that does not set its own: `local` or `pr`. Unset means `local`.                                                                      |
| `repos.<name>`         | Per-repo overrides, keyed by repo name. Absent entries are equivalent to empty ones.                                                                                                   |
| `…integration`         | This repo's integration route: `local` (git-work moves the commits) or `pr` (a pull request does, so `integrate` refuses). Empty inherits `defaults.integration`.                      |
| `…add_by_default`      | `true` pre-selects this repo in `git work new`'s repo picker.                                                                                                                          |
| `ticket_providers[]`   | Ordered list of providers for fetching ticket details.                                                                                                                                 |
| `…name`                | Provider name (recorded in state as the resolved provider).                                                                                                                            |
| `…type`                | Built-in provider type: `jira` or `github`.                                                                                                                                            |
| `…default`             | If set, this provider is used when no pattern matches. Setting one default clears the others. Not valid for `type: github` (a default match has no pattern, so no repo to fetch from). |
| `…folder_case`         | Case for the ticket id in the work folder name: `upper` (default) or `lower`.                                                                                                          |
| `…branch_case`         | Case for the ticket id in the branch name: `upper` (default) or `lower`.                                                                                                               |
| `…patterns`            | Ordered list of patterns matched against the ticket id; first match wins.                                                                                                              |
| `…patterns[].prefix`   | Prefix string (mutually exclusive with `regex`). Expands to `(?i)^<prefix>-([0-9]+)$`.                                                                                                 |
| `…patterns[].regex`    | Full regexp (mutually exclusive with `prefix`). Must contain exactly one capture group (the issue number).                                                                             |
| `…patterns[].repo`     | GitHub repo (`owner/repo`). Required for `github` providers; forbidden for `jira`.                                                                                                     |

### Provider resolution

Providers are built-in types - `jira` or `github` - not generic shell commands. `Resolve`
walks providers in order: the first whose pattern matches the ticket id wins; if none
match, the provider flagged `default` is used; otherwise no provider is used and the
ticket id stands in for the title (except in `new`, which treats a no-match as an error
when providers are configured - use `--no-ticket` for ticketless work). Matching is
**case-insensitive**; display is always uppercase (`DisplayID`).

**Pattern matching:** a `prefix` entry expands to `(?i)^<prefix>-([0-9]+)$`; capture group
1 is the issue number used for GitHub lookups. A `regex` entry is wrapped in `(?i)` for
case-insensitive matching and must also have exactly one capture group. Both forms match
the raw typed id case-insensitively.

**Fetch:** dispatched by type after resolution.

- `jira` - runs `jira issue view <ID> --raw` (using the uppercased display id), extracts
  `.fields.summary` (title) and `.fields.description` (description). Requires
  [`jira-cli`](https://github.com/ankitpokhrel/jira-cli) on `PATH`, authenticated.
  `.fields.description` is Atlassian Document Format (ADF) on Jira Cloud; git-work renders
  it to plain text (paragraphs, bullet/ordered lists, headings, code blocks, hard breaks).
- `github` - runs `gh issue view <number> --repo <repo> --json title,body`, extracts
  `.title` (title) and `.body` (description). Requires [`gh`](https://cli.github.com/) on
  `PATH`, authenticated.

A fetch failure is a warning - `new`/`refresh` continue without the details.

### Dot-path keys

`config get/set/unset` address values by dot-path:

| Key                            | Notes                                                                                                                                                                   |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `paths.repos`                  | Scalar. `unset` resets to the default.                                                                                                                                  |
| `paths.work`                   | Scalar. `unset` resets to the default.                                                                                                                                  |
| `defaults.integration`         | Scalar (`local` or `pr`). `unset` clears it back to the built-in `local`.                                                                                               |
| `repos.<name>`                 | `get` prints the entry as YAML; `unset` drops it.                                                                                                                       |
| `repos.<name>.integration`     | Scalar (`local` or `pr`). Auto-creates the entry if absent; `unset` clears it back to inheriting `defaults.integration`.                                                |
| `repos.<name>.add_by_default`  | `true`/`false`. Auto-creates the entry if absent.                                                                                                                       |
| `providers.<name>`             | `get` prints the provider as YAML; `unset` drops it.                                                                                                                    |
| `providers.<name>.type`        | Scalar (`jira` or `github`). Auto-creates the provider if absent.                                                                                                       |
| `providers.<name>.default`     | `true`/`false`; setting `true` clears other defaults.                                                                                                                   |
| `providers.<name>.folder_case` | Scalar (`upper` or `lower`).                                                                                                                                            |
| `providers.<name>.branch_case` | Scalar (`upper` or `lower`).                                                                                                                                            |
| `providers.<name>.patterns`    | One or more pattern entries. Jira: `<prefix>` (e.g. `PROJ`). GitHub: `<prefix>=<owner/repo>` (e.g. `dev-tools=acme/api`). Regex patterns must be set via `config edit`. |

```sh
git work config set paths.work ~/work
git work config set providers.jira.type jira
git work config set providers.jira.patterns PROJ
git work config set providers.jira.default true
git work config set providers.github.type github
git work config set providers.github.patterns dev-tools=acme/api
git work config get providers.github          # prints the whole provider as YAML
git work config unset providers.jira          # drop the whole provider
```

## Storage layout

Primary clones are nested so git works from both the repo's parent dir and its primary
worktree. The primary folder is named after the upstream default branch (`main`/`master`,
detected from `origin/HEAD` at clone/adopt time):

```
~/dev/repos/<repo>/
├── .git            # pointer file: "gitdir: ./main/.git" (core.worktree = ..)
├── CLAUDE.md       # optional per-repo overlay (RepoOverlay)
└── main/           # the real clone (RepoMain); named after the default branch
```

At runtime the primary folder is discovered by parsing the parent `.git` pointer file
(`layout.PrimaryDir`) - the on-disk source of truth - never assumed. A missing or
malformed pointer is a clear error; `repo adopt` is the single normalizer for such drift
(no command renames the layout as a side effect).

Work folders hold one worktree per repo plus generated files:

```
~/dev/work/<ticket>-<slug>/
├── .git-work.yaml  # state (or .git-work.json)
├── TICKET.md       # generated: ticket id, title, description
├── README.md       # generated: ticket, branch, repo list
├── CLAUDE.md       # generated: embedded contract + operator overlay + repo overlays
├── <repoA>/        # worktree (absolute path) on the work branch
└── <repoB>/
```

Worktrees are added with absolute paths. `ListRepos` enumerates the directories directly
under the repos root as the candidate repo set.

## State file

Each work folder carries `.git-work.yaml` (or `.git-work.json`); `FindUp` walks up from
the cwd to locate it. Schema:

```yaml
ticket_id: PROJ-123
title: Make the thing faster
slug: make-the-thing-faster
branch: PROJ-123-make-the-thing-faster # default work branch
provider: jira # resolved provider name, if any
no_ticket: true # ticketless folder (new --no-ticket): ticket_id is
# empty and title carries the plain name; omitted otherwise
created_at: 2026-06-21T00:00:00Z
repos:
  - name: api
    branch_source: new # "new" | "existing"
    branch: PROJ-123-make-the-thing-faster
  - name: web
    branch_source: existing
    branch: feature/PROJ-123
```

`repos[].branch` records the branch actually checked out per repo (which may differ from
the top-level `branch` when an existing branch was reused). State files written before
that field existed fall back to the top-level `branch` on load - a best guess that
`done`'s existence and merged checks gate before any deletion.

## Generated files

`new`/`add`/`refresh` write **managed blocks** into `TICKET.md`, `README.md`, and
`CLAUDE.md` via `template.WriteManaged`, so hand edits outside the managed region survive
a re-render. `done` treats these three files plus the two state file names as its own and
may delete them; any other file blocks folder removal unless `--remove-all` is given.

`CLAUDE.md`'s block has a fixed section order - general rules before repo specifics, with
the identity header first:

1. **Identity** - `# <ticket>: <title>` (or the title alone when ticketless) and the
   folder-level branch.
2. **Work folder contract** - `internal/template/workfolder.md`, compiled into the binary
   with `go:embed`. It describes what a work folder is, that `.scratch/` is flat at the
   root, that the managed region must not be hand-edited, and that folders are created and
   torn down with `git work` rather than by hand. It ships in the binary rather than being
   installed beside it so there is nothing to keep in sync per machine and the text always
   matches the tool that wrote it; `git work refresh` propagates an upgrade.
3. **Operator overlay** - the optional `${XDG_CONFIG_HOME:-~/.config}/git-work/CLAUDE.md`
   (`config.OverlayPath()`), for guidance that applies to every work folder but is the
   operator's rather than the tool's. Missing or blank is silently skipped.
4. **Per-repo sections** - one per included repo, each stating that repo's own branch and
   inlining its optional `~/dev/repos/<repo>/CLAUDE.md` overlay.

Both overlays are inlined verbatim; an installer's leading HTML comment is left in place,
since Claude Code strips block-level comments before reading a `CLAUDE.md`. Because the
content is copied into each folder, editing an overlay takes effect in existing folders
only after `git work refresh`.

## Branch resolution

When `new`/`add` includes a repo, `repo.ResolveWorkBranch` decides which branch its
worktree gets. The rules, in order:

1. A branch already carrying the intended name is always reused, whether it exists locally
   or only on origin (after the pre-resolution fetch). The remote case is checked out as a
   local branch tracking `origin/<branch>` (`repo.AddTrackingWorktree`): cutting a second
   branch of that name from `main` would only defer the collision to `push`.
2. `--branch` is the whole answer: the named branch is reused by rule 1 or created, and
   existing branches for the ticket are not consulted. An explicit name that a fuzzy match
   could override would not be explicit.
3. Otherwise the existing branches matching the ticket id are candidates - local ones and
   those that exist only on origin, a branch present in both places counting once, as the
   local one. Matching is boundary-aware and case-insensitive, so `PROJ-123` matches
   `feature/PROJ-123` but not `PROJ-1234`. No candidates: the branch is created. With
   candidates, an interactive run picks one (remote-only ones shown as `origin/<name>`) or
   "create new"; `--non-interactive` refuses with the full list, for one candidate as much
   as for several. A lone match is never adopted silently - it may be a teammate's
   branch - and `--branch <name>` is the one-flag answer for a script or agent that wants
   it.

Resolution runs for every repo before anything is created (`repo.ResolveWorkBranches`), so
a refusal reports all repos at once and leaves nothing to roll back. A `--branch` value
spelled `origin/<name>` is a usage error: git would create a local branch literally named
that, and the listings above are where the spelling comes from. There are no
`--reuse-existing`/`--always-new` flags: each pre-answered the picker for scripts, and
`--branch` answers it more precisely.

## Worktree/state branch agreement

`repos[].branch` in the state file is written once, at `new`/`add`/`adopt` time. Nothing
re-syncs it when a branch is switched by hand, so it can drift from what a worktree
actually has checked out. That matters because the commands split on which one they read:
`push` and `integrate` act on the **recorded** branch, `rebase` acts on the
**checked-out** one, and `done`/`rm` safety-check the worktree's HEAD
(`repo.HasUnpushedCommits`) but delete the recorded branch - locally and on the remote.
Left alone, drift means publishing a branch you are not looking at, integrating commits
you did not mean to, or deleting a branch other than the one that was checked.

So every command that acts on the recorded branch refuses while the two disagree, via one
shared check - `repo.WorktreeBranchProblem` - so the wording and the remedy read the same
everywhere:

| Command       | Treatment                                                                                                                                                   |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `push`        | Per-repo failure; other repos continue. **`--main` is exempt** - it reads only the primary clone and deliberately ignores the worktree and the work branch. |
| `rebase`      | Per-repo problem, checked _before_ the pull so a refused repo costs no network round trip.                                                                  |
| `integrate`   | Pre-flight violation - the whole run aborts with zero cherry-picks, matching integrate's atomic model.                                                      |
| `done` / `rm` | A safety-check problem, reported before the checks it invalidates; `--force` overrides it like any other.                                                   |

The check distinguishes three failures, because they need different advice:

- **Drift** - the worktree is on another branch. Names both branches and points at
  `git work adopt ./<repo>` to re-record it.
- **Operation in progress** - `rebase` or `bisect`. Both detach HEAD, and git-work's own
  `rebase` _deliberately_ leaves conflicted rebases in progress, so this is the common
  case rather than an exotic one; reporting it as unexplained drift would be actively
  misleading. Names the operation and the way out (`git rebase --continue`/`--abort`,
  `git bisect reset`).
- **Detached HEAD** - a raw commit checked out with nothing running.

A conflicted merge, cherry-pick, or revert keeps the branch checked out, so those never
look detached and are not probed.

One consequence worth noting: `state.Load` migrates pre-`Repo.Branch` state files by
copying the top-level branch into every repo, which its own comment calls a best guess.
For an old folder where a repo reused a differently-named existing branch, that guess is
wrong - and this check is what surfaces it, with re-adopt as the fix.
