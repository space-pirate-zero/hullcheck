# Governance

Written on day one, because most projects add this after their first painful
contributor.

## Who decides

Spaceship Alpha 9 maintains this project. Today that is a single maintainer, stated
honestly rather than dressed as a core team. Decisions are made in public issues.

## What is in scope

`hullcheck` measures whether stated rules are enforced. That is the whole product.

**In scope:** discovery of rules and gates, matching, verdicts, Time-to-Truth,
reporting, and adapters for new kinds of gate.

**Out of scope, and these will be closed with thanks:**

- Running or authoring policy. OPA, Conftest and ArchUnit already do that well.
- Linting code quality. Not our question.
- Anything that puts a model in the scoring path. The credibility of the number *is*
  the product; a score a model produces is a score a model can be talked out of.
- Telemetry, accounts, or a hosted service.

## Adapters

New gate adapters are the intended way to extend this. They are declarative
descriptors plus an exit-code contract — not a plugin API, and never dynamically
loaded code. A supply-chain-adjacent tool that executes third-party plugins is a joke.

## Changing a rule in CONTRIBUTING.md

Adding a rule without adding its gate will fail CI, by design. If a rule genuinely
cannot be mechanised, that is a finding worth discussing in the pull request — a rule
nobody can test is a rule nobody can follow.
