# CONTEXT

Glossary of domain terms for the CLI tools in this repo. Currently the terms all belong to
`git-work`; if another tool grows a vocabulary of its own, consider splitting into
per-tool contexts via a `CONTEXT-MAP.md`.

## git-work

- **Operator** - the person running `git-work`.
- **Work folder** - a directory under `paths.work` (`~/dev/work/<ticket>-<slug>/`) holding
  one worktree per participating repo plus a `.git-work.yaml` state file.
- **Work branch** - the branch a repo has checked out in its worktree for a ticket;
  recorded per repo in state (`repos[].branch`). Never a repo's default branch.
- **Ticket prefix** - the leading segment of a ticket id (`PROJ` in `PROJ-10`), matched
  against a provider's patterns to select the provider that resolves it. _Avoid_: key,
  project key, Jira key - "key" reads as a credential to everyone outside Jira's own
  vocabulary.
- **Ticketless** - a work folder created with `--no-ticket`, named from a plain name
  rather than a ticket id: no provider lookup and no `TICKET.md`.
- **Primary (clone)** - the real clone of a repo under `paths.repos`, named after its
  upstream default branch; the source of worktrees and the rebase/integrate target.
- **Default branch** - the upstream HEAD branch of a repo (`main`/`master`); "main" in
  conversation always means this, not the literal name.
- **Un-integrated commits** - commits on a work branch that are not on the repo's _local
  default branch_. The measure `integrate` scopes from. Distinct from _unpushed_ and
  _unpublished_.
- **Unpublished commits** - commits on a work branch that origin does not have yet:
  missing from `origin/<default>` (patch equivalence) or from `origin/<branch>`
  (ancestry). The measure `push` scopes from. Local-only integration leaves commits
  integrated but still unpublished.
- **Unpushed commits** - commits on a branch that are not on its `@{upstream}`; a branch
  with no upstream counts as entirely unpushed. The measure `done`'s safety check uses. A
  branch can be unpushed but have zero un-integrated commits, and vice versa.
- **Empty branch** - a work branch with zero un-integrated commits (nothing since
  branching from the default branch). `push` skips a branch with no unpublished commits
  unless `--allow-empty`.
- **Contained** - a branch whose content is already in a target branch, whether by
  ancestry, squash-merge patch equivalence, or merge-tree absorption.
- **Teardown** - removing a repo's worktree/branches and dropping it from work folder
  state (`done` for the whole folder, `rm` for one repo).
- **Drift** - a managed thing no longer agreeing with what git-work recorded. Layout drift
  is a primary clone's on-disk shape departing from the managed layout; branch drift is a
  worktree's checked-out branch departing from the one state records for it.
- **Integrating** - moving a work branch's un-integrated commits onto its repo's local
  default branch, as opposed to merging via a remote PR. By fast-forward when the default
  branch is an ancestor of the work branch, by cherry-pick otherwise.
- **Integration route** - how a repo's work is meant to reach its default branch: `local`
  (git-work integrates it) or `pr` (a pull request does). Set per repo, or as a house rule
  under `defaults`.
- **PR-only repo** - a repo whose integration route is `pr`. `integrate` refuses it.
