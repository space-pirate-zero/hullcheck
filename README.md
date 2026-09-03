```
   .        *            .         *

  ##### ##### ##### ##### #####
  #     #   # #   # #     #
  ##### ##### ##### #     #####
      # #     #   # #     #
  ##### #     #   # ##### #####

  ##### ##### ##### ##### ##### #####
  #   #   #   #   # #   #   #   #
  #####   #   ##### #####   #   #####
  #       #   #  #  #   #   #   #
  #     ##### #   # #   #   #   #####

  ##### ##### ##### #####
     #  #     #   # #   #
    #   ##### ##### #   #
   #    #     #  #  #   #
  ##### ##### #   # #####

  ------------------=[ o ]=---------
      *          .            *
```

# hullcheck

**Check the hull before you trust the air.**

Your repository states rules. Some of them are enforced by something that runs. The
rest are wishes. `hullcheck` tells you which is which.

A hull breach in vacuum does not announce itself. The seal looks fine, the reading
drifts, and the first hard signal is the one you cannot act on. Policy fails the same
way — quietly, and only when it matters.

```
HULLCHECK reading .  (unverified - no manifest present)

  12 rules found in 1 policy document
  16 gates found in CI, hooks and checker scripts

  HOLD        12   rule has a gate, and something runs
  BREACH       0   stated, nothing runs on it
  UNLOGGED     1   a gate runs, enforcing nothing anyone wrote down

  TIME-TO-TRUTH        p50 12m    p90 12m    worst 12m
    pull-request   12 gates   12m

  Gate Coverage  100%   weighted by severity  100%
```

Test coverage told a generation of engineers whether their code *runs*. Nothing told
them whether their *policy* runs.

## Why now

AI removed the rate limiter on code. It did not remove the review. Governance that
lives in prose was affordable when prose was read at the speed code was written; it
is not affordable now. A rule that cannot execute is a wish.

## The four verdicts

| | |
|---|---|
| **HOLD** | the rule has a gate, and something runs |
| **BREACH** | stated, nothing runs on it |
| **UNLOGGED** | a gate runs, enforcing nothing anyone wrote down |
| **FAKE** | the gate passes even when its rule is broken — a patch that is only paint |
| **BROKEN** | the gate fails either way, so it proves nothing — usually a command that cannot run |

**UNLOGGED** is the bucket nobody else reports: CI enforcing rules no one wrote down,
institutional knowledge that exists only as a red build.

## Time-to-Truth

Gate Coverage is a percentage. **Time-to-Truth is a latency**, and latency is a
language your leadership already prices. A rule gated only by a nightly job is
covered, and fourteen hours late. The same rule at pre-commit is caught in seconds.
Identical coverage, wildly different exposure.

## Add it to CI in one line

**GitHub Actions.** No Go toolchain, no install step, no version for you to maintain:

```yaml
- uses: spaceship-alpha-9/hullcheck@v1
```

Report first, fail later — start by seeing the number, then ratchet:

```yaml
- uses: spaceship-alpha-9/hullcheck@v1
  with:
    fail-under: 60      # omit entirely to report without failing
```

It exposes `coverage` and `breaches` as step outputs, so you can post them, chart
them, or gate on them yourself.

**Your Go test suite.** One function, no new tooling, runs with `go test`:

```go
func TestGateCoverage(t *testing.T) {
    hullcheck.AssertCoverage(t, ".", 80)
}
```

Or ratchet instead of gating, so a repository can carry known gaps without letting
new ones in:

```go
func TestNoNewBreaches(t *testing.T) {
    hullcheck.AssertNoNewBreaches(t, ".", "RULES-7.4", "RULES-8.1")
}
```

A failure names the rules, not just the percentage — a number tells you that you
failed, not what to fix.

**pre-commit.** Catch it in seconds instead of minutes:

```yaml
repos:
  - repo: https://github.com/spaceship-alpha-9/hullcheck
    rev: v0.1.0
    hooks:
      - id: hullcheck-fail-under
```

**Any other CI, or no CI at all.** One command, nothing installed, repo mounted
read-only:

```sh
docker run --rm -v "$PWD:/repo:ro" ghcr.io/spaceship-alpha-9/hullcheck
```

**Locally.**

```sh
go install github.com/spaceship-alpha-9/hullcheck/cmd/hullcheck@latest
hullcheck .
```

## Turning the reading into a score

The default run is a *reading*: it matched rules to gates by name and reference. To
get a number you can quote, declare the links and prove them:

```sh
hullcheck --print-manifest . > .hullcheck.yml   # review it, add fixtures
hullcheck --verify .
```

`--verify` breaks each rule inside a scratch copy of your repository and checks that
the gate actually fails. It runs a **control first** — the gate on an unmodified copy
— because without one, a gate that cannot run at all exits non-zero and is
indistinguishable from a gate that works. That control is the difference between an
experiment and a hopeful guess, and it is what makes `FAKE` and `BROKEN` trustworthy.

Your repository is never modified: fixtures are applied to a temp copy that is
removed afterwards.

## What it leaves behind

Not *zero* — a binary is a footprint, and claiming zero is the kind of unenforceable
wish this tool exists to find. The promise is narrower and keepable:

> Nothing left behind is ever inside your repository, everything is disclosed before
> it happens, and every piece of it is removable with one command.

| Tier | What | Removable by |
|---|---|---|
| 0 | the run itself — **nothing**, in your repo or `$HOME` | n/a |
| 1 | the binary you chose to install | deleting it |

The core makes **no network calls of any kind** and has **no HTTP client in its
dependency graph** — asserted by `make network`, not by a privacy policy. It has
**zero third-party dependencies**, asserted by `make deps`. It never writes to the
repository it reads, asserted by `make readonly`.

## Exit codes are the API

```
0  a reading was produced
1  --fail-under was breached
2  refused - see the message
```

It refuses rather than flatters. Point it at a repository that states no rules and it
reports `UNKNOWN` and exits 2. A reading with no denominator is a lie.

## Status

**v0.1, the reading.** Honest about what it is: deterministic discovery that matches
rules to gates by name and reference. It does **not** yet prove that a gate fails when
its rule is broken — that is verified mode (`--verify` and the `FAKE` verdict), and it
is not built yet. The tool says so on every unverified run rather than letting you
quote a number it did not earn.

Verified mode **is** built: `--print-manifest`, `--verify`, the control run, and the
`FAKE` and `BROKEN` verdicts.

Not built yet: drift across git history (`--since`), PR mode (`hullcheck diff`), and
the separate `hullcheck-assist` binary that drafts the gates you are missing.

## Prior art, credited

`hullcheck` measures whether you have policy; it does not run it. [Open Policy
Agent](https://www.openpolicyagent.org/), Conftest and ArchUnit run policy, and
architecture fitness functions (Ford, Parsons & Kua) argued two decades ago for
governing characteristics without manual review. This is a meter, not an engine.

*The Plimsoll line — a mark on a hull, mandated by law in 1876, that turned an
unenforceable safety norm into something any dockhand could verify by looking.*

## Licence

Apache-2.0. See [LICENSE](LICENSE).
