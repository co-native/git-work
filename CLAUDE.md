# git-work

`git-work` binary; manages multi-repo work via git worktrees. Installs to `~/.local/bin`
so git dispatches `git work <cmd>`. [`DESIGN.md`](DESIGN.md) is the single home for
semantics and the reasoning behind them - command reference, config schema, storage
layout, state file. [`CONTEXT.md`](CONTEXT.md) is the glossary. This file is the map plus
the invariants; when the two disagree, `DESIGN.md` wins.

## Layout

- `main.go` - thin argv dispatcher; `os.Args[1]` selects a `command/<cmd>` package whose
  `Run(args []string) int` does the work, and `main` calls `os.Exit` on the returned code.
- `command/` - one package per subcommand. `repo` is a group (clone, adopt, list, pull),
  also reachable as `repos`; top-level `clone` is an alias for `repo clone`.
  `command/repo` imports `internal/repo` as `gitrepo`, and `main.go` imports it as
  `repocmd` - the same two-sided aliasing `command/config` uses against `internal/config`.
- `internal/` - config, state, layout, repo (git plumbing), provider, template, tui,
  teardown (shared `done`/`rm` logic), cli (the help and exit-code contract).

`DESIGN.md`'s Architecture table says what each package owns.

## Build and test

```sh
make build       # -> dist/git-work
make test        # go test ./...
make fmt         # deno fmt (markdown, 90 cols) + gofmt
make fmt-check   # both, without rewriting
```

Tests shell out to real git and are hermetic: they need no git identity and no global git
config, so they pass on a bare CI runner. Single package or test:
`go test ./internal/repo -run TestIsDefaultBranch`.

## Invariants

Things that are expensive to get wrong. Each is enforced in code and reasoned about in
`DESIGN.md` - do not remove one because it looks redundant.

- **The help contract.** `-h`/`--help` prints usage to stdout and exits 0, everywhere.
  Exit codes are 0 success, 2 malformed invocation, 1 the work failed. Usage text lives in
  a `cli.Command` descriptor, and `clitest.Check` fails the build if a flag is registered
  without being documented or documented without being registered. Never declare
  `-h`/`--help` in a descriptor or on a `FlagSet` - the renderer appends it.

- **A work branch is never a repo's default branch.** `new`/`add`/`adopt` refuse to record
  it; `teardown` refuses to delete it and `--force` does not override that. Every other
  teardown safety check is trivially satisfied by the default branch - it is merged into
  `origin/HEAD` by definition, has an upstream with nothing unpushed, and agrees with a
  worktree sitting on it - so this guard is the only thing standing between
  `done --delete-local --delete-remote` and deleting `main` locally and on origin.

- **Worktree and state must agree on the branch.** `repos[].branch` is written once and
  never re-synced, so it can drift. Commands split on which one they read, so every
  command acting on the recorded branch refuses while they disagree, via the one shared
  `repo.WorktreeBranchProblem`. `git work git` is deliberately exempt - it is the tool for
  investigating drift.

- **Configured paths are resolved through `cfg.Layout()`.** It expands `~` and `${VAR}`;
  the raw config value is never used as a path. An undefined variable is a hard error, not
  an empty expansion - `os.ExpandEnv` would silently turn `${HOEM}/dev/repos` into
  `/dev/repos`.

- **`done`/`rm` never delete files git-work did not create.** `--force` bypasses
  safety-check problems; removing user files is `--remove-all` or the interactive prompt,
  and the two are separate on purpose.

- **Multi-repo commands collect failures rather than aborting.** Per-repo problems are
  gathered, the rest continue, and the run exits nonzero. `integrate` is the exception:
  its pre-flight is atomic across the whole scope, so a violation means zero cherry-picks.

- **`--force` is for judgment calls, not for correctness.** It overrides "this branch is
  not merged, delete it anyway". It does not override guards against things that are never
  intended - the default-branch refusal, or `push --main -f`, which is a usage error.

## Generated files

`new`/`add`/`refresh` write **managed blocks** into the work folder's `TICKET.md`,
`README.md` and `CLAUDE.md` via `template.WriteManaged`, so hand edits outside the markers
survive. `internal/template/workfolder.md` is compiled in with `go:embed` and is git-work
describing its own contract - keep it to the tool's semantics, since operator preferences
do not belong in the binary. `DESIGN.md` covers the assembly order and the two overlays.

## Style

Prose wraps at 90 columns (`make fmt-md`). No em dashes - plain `-`. Errors name a remedy,
not just a diagnosis; every refusal in `command/adopt` is the model.
