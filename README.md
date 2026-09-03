<p align="center">
  <img src="brand/wordmark.png" alt="hullcheck" width="520">
</p>


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
- uses: space-pirate-zero/hullcheck@v1
```

Report first, fail later — start by seeing the number, then ratchet:

```yaml
- uses: space-pirate-zero/hullcheck@v1
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
  - repo: https://github.com/space-pirate-zero/hullcheck
    rev: v0.1.0
    hooks:
      - id: hullcheck-fail-under
```

**Any other CI, or no CI at all.** One command, nothing installed, repo mounted
read-only:

```sh
docker run --rm -v "$PWD:/repo:ro" ghcr.io/space-pirate-zero/hullcheck
```

**Locally.**

```sh
go install github.com/space-pirate-zero/hullcheck/cmd/hullcheck@latest
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

## Reading the shape of the gaps

```sh
hullcheck --paths      # which top-level trees have gates, and which have none
hullcheck --owners     # gates with one owner or none, from CODEOWNERS
hullcheck --badge      # a self-contained SVG, no third-party image service
```

`--paths` is the one that surprises people: a repository can score well overall
while an entire subsystem has no gate of its own. Breached rules are also dated
by the commit that introduced them, so a gap opened last week reads differently
from one open since 2019.

## Proving your tools stop when they say they stop

```sh
hullcheck --refusals
```

The most valuable thing an unattended tool does is decline to proceed. Most tools
have never had that conversation with themselves, and grepping scripts for
`exit 1` to guess produces confident-looking nonsense.

So this does not guess. A refusal is a **claim**, and a claim is testable. Declare
one, and hullcheck runs the tool twice - once normally, once with the condition
present:

```yaml
refusals:
  - tool: indexer
    when: "the checkout is a worktree"
    run: "./index_assets.py ."
    remove: "art/originals"        # conditions are often an ABSENCE
    expect_exit: 2
    expect_output: "refusing to index"
```

| | |
|---|---|
| **REFUSES** | proven: it worked normally, then stopped in the declared way |
| **PROCEEDS** | it ran anyway under a condition it claims to refuse — **a silent downgrade** |
| **BROKEN** | it failed either way, or crashed rather than refused |

`PROCEEDS` is the finding worth having. A crash with the right exit code is not a
considered refusal, and a tool that fails without the condition proves nothing —
which is why the control run exists.

## Can your artifacts say where they came from

```sh
hullcheck --provenance
```

Three questions get asked about a generated artifact: where did it come from, may
we ship it, can we reproduce it. Sniffing for provenance-shaped files and guessing
is worthless, so the repository **declares its scheme** and the audit becomes
arithmetic:

```yaml
provenance:
  - artifacts: "art/**/*.png"
    record: "{artifact}.meta.json"
    require: [source, model, license]
```

That works for sidecars, SPDX, CycloneDX, in-toto or C2PA without hullcheck
needing to understand any of them. Verdicts are `RECORDED`, `INCOMPLETE` (a record
that exists and does not answer — worse in one way than none, because it looks
answered) and `MISSING`.

With nothing declared it reports **UNKNOWN** and names the conventions it found by
exact filename, so you can declare one. It never reports 100% for a repository
with no artifacts registered: a denominator of zero is not a clean bill of health.

## Watching the trend, and gating pull requests

A snapshot starts an argument; a trend ends one.

```sh
hullcheck --since v1.4        # how coverage moved from a tag to now
hullcheck diff --base main    # rules this branch added without gates
```

`diff` exits 1 only on rules that are **newly** unenforced. A repository can carry
known gaps without every pull request paying for them; what it cannot accept is a
new rule with nothing behind it.

Both read history with `git archive` into a temp directory. `git worktree add`
would be the obvious approach and is disqualified: it writes into `.git`.

## Drafting the gates you are missing

`hullcheck` tells you 28 rules are unenforced. `hullcheck-assist` hands you 28
drafts to review.

```sh
hullcheck-assist plan .        # draft a gate for every BREACH
hullcheck-assist fixture .     # draft the violating fixture each gate needs to be proven
hullcheck-assist name .        # name the rules your UNLOGGED gates already enforce
hullcheck-assist explain .     # why a rule resists mechanising, and a restatement
hullcheck-assist triage .      # order the gaps by blast radius
hullcheck-assist harvest doc.md  # propose rules from prose the scanner missed
```

It is a **separate binary**, which is what keeps "no network package in the core's
dependency graph" a fact about an artifact rather than a promise about behaviour.

**The model never touches the number.** It drafts, names and explains. Every score
is computed by code you can read — a score a model produces is a score a model can
be talked out of.

It finds a model in this order, stopping at the first hit:

1. `--model`, or `HULLCHECK_MODEL` / `HULLCHECK_BASE_URL`
2. an API key already in your environment — read, never written, never persisted
3. **a local server already running** — Ollama, LM Studio, vLLM or llama.cpp,
   probed in parallel on a 300 ms timeout. This is the path we optimise for: free,
   private, and your repository never leaves the machine. A coder-class model is
   preferred when the host offers several.
4. an offer to start one in Docker — with the download size and RAM cost stated
   **before** anything is pulled, only interactively, never in CI, and never
   without you typing `y`

Nothing is stored. If you want an API key to persist, that is your shell profile's
job, not ours.

## Status

**v0.1, the reading.** Honest about what it is: deterministic discovery that matches
rules to gates by name and reference. It does **not** yet prove that a gate fails when
its rule is broken — that is verified mode (`--verify` and the `FAKE` verdict), and it
is not built yet. The tool says so on every unverified run rather than letting you
quote a number it did not earn.

Verified mode **is** built: `--print-manifest`, `--verify`, the control run, and the
`FAKE` and `BROKEN` verdicts.

Built: the reading, verified mode with the control run, Time-to-Truth, drift, PR
mode, path coverage, gate bus-factor, ungoverned-since, the badge, the refusal and
provenance audits, and all six assist commands.

**hullcheck proves its own refusals**, in CI, on every pull request.

**hullcheck verifies its own repository**: four gates declared with fixtures, all
four proven by breaking the rule and watching the gate fail. The first verified
run found one of them FAKE - `make secrets` used `git grep`, which only searches
tracked files and silently passes where there is no git repository. That gate is
fixed, and the finding is why the control run exists.

Honest limits: `--verify` proves gates you have declared *and given a fixture*; a
gate without one is reported as declared-but-not-proven rather than counted as
evidence. And a small local model is good at drafting a checker and naming a gate,
weaker at reading long prose policy — the tool prints which model it used so you
know how much scrutiny a draft deserves.

## Prior art, credited

`hullcheck` measures whether you have policy; it does not run it. [Open Policy
Agent](https://www.openpolicyagent.org/), Conftest and ArchUnit run policy, and
architecture fitness functions (Ford, Parsons & Kua) argued two decades ago for
governing characteristics without manual review. This is a meter, not an engine.

*The Plimsoll line — a mark on a hull, mandated by law in 1876, that turned an
unenforceable safety norm into something any dockhand could verify by looking.*

## Licence

Apache-2.0. See [LICENSE](LICENSE).
