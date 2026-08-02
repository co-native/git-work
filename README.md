# git-work

Git plugin to help developer workflow when working with multiple repositories in one unit
of work.

One change often spans several repos. Doing that by hand means a branch per repo, a
checkout per repo, remembering which branch you used where, and - at the end - working out
which of them are safe to delete. `git-work` makes that one unit: a **work folder**
holding one git worktree per participating repo, all on a shared branch, created and torn
down as a set.

```
git work new PROJ-123 --repos api,web    # a folder with two worktrees, branches created
cd ~/dev/work/PROJ-123-add-auth          # edit across both repos
git work push -a                         # push both work branches
git work done PROJ-123-add-auth          # safety-check and tear the whole thing down
```

It installs as `git-work` on `PATH`, so git dispatches it: `git work <command>`.

## Install

Needs a [Go toolchain](https://go.dev/dl/) - there are no prebuilt binaries.

```sh
go install github.com/co-native/git-work@latest
```

The binary lands in `$(go env GOPATH)/bin`; make sure that is on your `PATH`. From a
checkout, `make install` puts it in `~/.local/bin` instead (`go install` is the route on
Windows, where the `Makefile` needs a POSIX shell).

Linux, macOS and Windows are all built and tested in CI. On Windows both `\` and `/` work
as path separators, including in `paths.repos`/`paths.work`.

**Requires** git 2.30 or newer. With git 2.38+ the teardown check additionally recognises
a branch that was merged as a mix of squashed and cherry-picked commits; on older git that
one case degrades quietly.

**Optional**, only for fetching ticket titles:
[`jira-cli`](https://github.com/ankitpokhrel/jira-cli) for Jira,
[`gh`](https://cli.github.com/) for GitHub issues. Both must already be authenticated.
Without them, `--no-ticket` work folders need nothing.

## How it is laid out

Two roots, both configurable:

```
~/dev/repos/<repo>/main/         # the primary clone, one per repo (the "repos root")
~/dev/work/<ticket>-<slug>/      # a work folder, one per unit of work (the "work root")
├── .git-work.yaml               #   its state: repos, branches, ticket
├── api/                         #   a worktree of ~/dev/repos/api
└── web/                         #   a worktree of ~/dev/repos/web
```

Worktrees rather than clones, so every repo is fetched once and a work folder costs a
checkout rather than a copy of history.

## Getting started

```sh
# 1. Bring repos under management (once per repo).
git work repo clone git@github.com:acme/api.git
git work repo adopt ~/dev/some-existing-clone     # or adopt one you already have

# 2. Start a unit of work. Ticketless if you have no ticket:
git work new PROJ-123 --repos api,web
git work new --no-ticket spike-harness --repos api

# 3. Work in it. From inside the folder:
git work git -a -- status          # run any git command in every repo
git work rebase -a                 # rebase every work branch onto its default branch
git work add                       # bring another repo into this unit of work
git work refresh                   # pull the primaries, re-render the generated files

# 4. Integrate, then tear down. Run `done` from OUTSIDE the folder: it deletes
#    the directory, and your shell would be left sitting in a deleted one.
git work push -a                   # push work branches
git work integrate -a              # or move the commits onto the default branch locally
cd ~/dev/work
git work done PROJ-123-add-auth    # safety-check, then tear down
```

`git work done` will not delete anything it is not sure about: it checks each repo for
uncommitted changes, unpushed commits, and whether the branch really made it into the
default branch (recognising squash merges), and it refuses to touch files it did not
create. Every command answers `-h`.

## Configuration

`${XDG_CONFIG_HOME:-~/.config}/git-work/config.yaml`, all of it optional:

```yaml
paths:
  repos: ~/dev/repos # where primary clones live
  work: ~/dev/work # where work folders live

repos: # per-repo overrides
  api:
    add_by_default: true # pre-ticked in `git work new`'s picker
    integration: pr # reaches its default branch via PR; `git work integrate` refuses

ticket_providers: # optional, for fetching ticket titles
  - name: jira
    type: jira
    default: true
    patterns: [{ prefix: PROJ }]
```

`~` and `${VAR}` in the two paths are expanded, so one config file works across machines.
Edit it with `git work config set paths.work ~/work`, or `git work config edit`.

## Documentation

- [`DESIGN.md`](DESIGN.md) - the full command, config and layout reference, and the
  reasoning behind the behaviour that is not obvious.
- [`CONTEXT.md`](CONTEXT.md) - glossary. Worth skimming first; terms like _un-integrated_,
  _unpushed_ and _contained_ mean specific different things here.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) - what to run before a PR, and what to read before
  changing behaviour.

## Status

Used daily by its author, but young and opinionated - it encodes one particular way of
working. Issues and pull requests are welcome; no promises about response time.

## License

Apache-2.0. See [LICENSE](LICENSE).
