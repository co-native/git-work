# Contributing

Issues and pull requests are welcome. No promises about response time - this is a tool its
author uses daily, not a staffed project.

## Sign off your commits

Every commit in a pull request must carry a `Signed-off-by` trailer. Adding it certifies
the [Developer Certificate of Origin](DCO): in short, that you wrote the change or
otherwise have the right to submit it under this project's license.

```sh
git commit -s               # adds the trailer from your git user.name and user.email
git rebase --signoff main   # to add it to commits you already made
```

It must be a real name and address, e.g. `Signed-off-by: Jane Doe <jane@example.com>`, and
**the address must match the commit's author address**. `git commit -s` does that for you
by using your `user.email` for both; a mismatch usually means the trailer was
hand-written, or the commit was made under a different identity than it was signed with -
a GitHub noreply address on one side and a real one on the other being the common case.

CI checks every commit in the PR and names any that are missing a trailer or whose address
does not match. A commit may carry several trailers - it only needs one that matches.

If you are contributing work done on an employer's time or equipment, the sign-off is you
stating you have the right to submit it - worth confirming before you do, rather than
after.

## Before opening a PR

```sh
make fmt      # deno fmt for markdown at 90 columns, gofmt for Go
make test     # go test ./...
go vet ./...
```

`make` alone lists every target. CI runs the same things on Linux, macOS and Windows.
`make fmt-check` is the non-writing version if you want to see what `fmt` would change.

Include the output of `git work version` in a bug report.

## What to know before changing behaviour

[`DESIGN.md`](DESIGN.md) is the single home for semantics and, more usefully, for _why_
the behaviour is what it is. A fair amount of this tool looks over-cautious until you know
the failure it is guarding against - `WorktreeBranchProblem` and the default-branch guard
are both there because something went wrong without them. If a check looks redundant, the
reason it exists is probably written down; please read it before removing it.

[`CLAUDE.md`](CLAUDE.md) lists those invariants in one place. [`CONTEXT.md`](CONTEXT.md)
is the glossary - worth skimming, because _un-integrated_, _unpushed_ and _contained_ mean
specific and different things here, and using them loosely in a PR description makes
review harder than it needs to be.

## Conventions

- Every command answers `-h`/`--help` with its usage on stdout and exit 0. Usage text
  lives in a `cli.Command` descriptor, and a conformance test fails the build if a flag is
  registered without being documented or documented without being registered. Never
  declare `-h`/`--help` yourself - the renderer appends it.
- Exit codes: **0** success, **2** malformed invocation, **1** the invocation was fine and
  the work failed.
- Errors name a remedy, not just a diagnosis. `command/adopt`'s refusals are the model.
- Prose wraps at 90 columns. Plain `-`, not em dashes.
- Tests shell out to real git and must stay hermetic: no git identity, no global config,
  no network. `internal/repo` and `internal/teardown` have the fixture helpers to copy.
