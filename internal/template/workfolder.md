## Work folder

This directory is a work folder: a per-feature container holding one git worktree per
participating repo plus a `.git-work.yaml` state file. It is not itself a git repo.

- The repos listed below are checked out on a shared branch. Changes spanning several of
  them are normal - keep work on that existing branch rather than creating new ones.
- Scratch material (notes, dumps, command output) goes in a flat `.scratch/` at the work
  folder root, where it can never be committed. There is no per-feature subdirectory: this
  folder already is the feature.
- Never hand-edit between the `git-work:begin` and `git-work:end` markers in this file or
  in `README.md`. `git work refresh` regenerates everything between them and will discard
  edits made there. Content outside the markers is preserved.
- Create, extend and tear down work folders with `git work` (`new`, `add`, `adopt`, `rm`,
  `done`), never with `mkdir`, `mv` or `rm -rf` - those leave `.git-work.yaml` and the
  real worktrees disagreeing. `git work -h` lists the commands, and every command and
  subcommand answers `-h`/`--help`.
