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
