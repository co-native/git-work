## Work folder

This directory is a clone-mode work folder: a per-feature container holding one full git
clone per participating repo plus a `.git-work.yaml` state file. It is not itself a git
repo and is self-contained; the folder can be moved or mounted as a unit while no linked
worktree exists under `.worktrees/`.

- The repos listed below are checked out on a shared branch. Changes spanning several of
  them are normal - keep work on that existing branch rather than creating new ones.
- Scratch material (notes, dumps, command output) goes in a flat `.scratch/` at the work
  folder root, where it can never be committed. There is no per-feature subdirectory: this
  folder already is the feature.
- `.worktrees/` is reserved: `integrate-<repo>` is git-work's transient checkout, and
  `agent-<agent>/<repo>` is a subagent worktree following the documented convention. Do
  not put notes or other files there.
- Subagents use plain git (`git status`, `add`, `commit`, `diff`, `log`, `checkout`,
  `rebase`) inside their `.worktrees/agent-<agent>/<repo>` worktree. git-work commands act
  on the main checkout, so they refuse when run from inside `.worktrees/`.
- Repos using Git LFS need the `git-lfs` binary in the environment, even when the
  checked-out branch's objects are already local.
- Never hand-edit between the `git-work:begin` and `git-work:end` markers in this file or
  in `README.md`. `git work refresh` regenerates everything between them and will discard
  edits made there. Content outside the markers is preserved.
- Create, extend and tear down work folders with `git work` (`new`, `add`, `adopt`, `rm`,
  `done`), never with `mkdir`, `mv` or `rm -rf` - those leave `.git-work.yaml` and the
  real checkouts disagreeing. `git work -h` lists the commands, and every command and
  subcommand answers `-h`/`--help`.
