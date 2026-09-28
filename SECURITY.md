# Security policy

`hullcheck` reads repositories, so it is a tool people run against code they cannot
afford to leak. That shapes the whole design.

## Guarantees, and how they are enforced

| Guarantee | Enforced by |
|---|---|
| No network calls, ever | `make network` — asserts no network package in the core's dependency graph |
| Zero third-party dependencies | `make deps` — asserts an empty `require` block and no external packages |
| Never writes to your repository | `make readonly` — runs the binary and fails on any working-tree change |

These run on every pull request. They are gates, not promises.

## What `--verify` and `--refusals` execute

A plain reading executes nothing. `--verify` and `--refusals` are the exception, by
design: they run the `run:` commands declared in `.hullcheck.yml` through `sh -c`,
in a scratch copy, **with the caller's environment**. Whoever can edit that file can
run code wherever those flags are used. They carry the same trust as `make test`:
safe on a `pull_request` run, which GitHub gives no secrets for forks, and unsafe on
`pull_request_target` or any job that checks out untrusted code next to credentials.
`unset_env:` strips a variable from one refusal; it is not a sandbox.

Refs given to `--since` and `--base` are refused if they start with `-`, so a ref
can never become a `git archive` option.

## Reporting a vulnerability

Report privately via GitHub Security Advisories on this repository. Please do not open
a public issue for a vulnerability.

Expect an acknowledgement within 5 working days. If a fix is warranted we will agree a
disclosure timeline with you before publishing.

## Scope

In scope: anything that causes `hullcheck` to write outside its own output stream, make
a network call, execute repository content, or leak file contents it was not asked to
report.

Out of scope: an inaccurate reading. A wrong verdict is a bug, not a vulnerability —
please file it as an issue.
