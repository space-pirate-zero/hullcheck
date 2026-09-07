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

## What counts as a rule

A numbered clause is judged by its **whole block** — the clause line and everything
under it up to the next clause or heading — not by its first line. A rule written as
a declarative headline with the obligation in the bullets beneath it is a common
style, and judging the headline alone dropped the clause and its whole subsection:

```markdown
8.8 **Skills are living operator docs — every render updates them.**

  - The owning skill must be updated in the same PR as the render.

*Gate: make check-skills*
```

An explicit `Gate:` line inside the block is sufficient on its own. If the document
names a gate for a clause, the document has already said it is a rule, and that is
the repository's own word rather than the scanner's inference.

Fenced code blocks state nothing — a sample showing what *not* to do is full of
modals — so they never grant an obligation.

The clauses that were seen and not counted are printable, so the denominator can be
checked rather than believed:

```sh
hullcheck --clauses .
```

```
  15 rule(s) counted across 2 policy documents
  1 numbered clause(s) seen and not counted

  RULES-8.7          RULES.md:20
    Overview of the section that follows.
    no obligation in the clause or in the text beneath it
```

## How a rule is matched to a gate

Three signals, in descending order of trust:

1. **The rule names its gate.** A `Gate: make preflight` annotation is the repository's
   own word, and nothing beats it.
2. **The gate's name or command.** Those are what a human chose to describe what the
   gate does. One distinctive shared word is a link.
3. **The gate's filename.** Weak, so it takes two distinct words agreeing.

The gate's **directories are never evidence**. In a flat repository a path adds a
couple of words; in a monorepo it is a topic list —
`books/meatware-nightly/check_brand.py` offers `books`, `meatware`, `nightly` and
`brand` to any rule that mentions any of them. A path is treated as a path wherever
it appears — in the gate's file *or* inside its command — because a check script's
command is its own path, and `cd books/meatware-nightly && make check` ends in a
directory name that is not a description of anything.

A word that appears in more than a quarter of the repository's gates is describing
the repository, not the rule, and carries no match. `books` matching 43 of 206 gates
is not evidence.

Every match says where it came from, so it can be argued with:

```
"provenance" appears in both the rule and the gate's name "check-provenance" (Makefile)
"sidecar" and "provenance" appear in both the rule and the filename provenance_sidecar.sh
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

### In verified mode the manifest is authoritative

The reading is the manifest's rules **unioned** with discovery's, not the
intersection. A rule you declare counts whether or not the scanner found it, and
rules only the scanner found are still reported, because unlogged surprises are the
point.

That is what makes a declaration worth writing. Under an intersection, a rule the
scanner missed was verified, logged, and then silently absent from the report and
from the score — so declaring an accurate link could not correct an inflated
reading, and proving a gate need not move the number.

Rules that came from the manifest alone say so, and the changed denominator is
announced:

```
hullcheck: .hullcheck.yml declares 4 rule(s) discovery did not find; they are
counted in this reading
```

The one thing it will not do is guess: where a declaration names no `source:` and
several rules share its id, nothing is changed and nothing is added.

### Gates and refusals that read git

The scratch copy leaves out `.git` by default: it is often the largest thing in a
repository and most commands under test have no use for it. Anything that reads
history, tracked-file state, or whether the checkout is a worktree does, and says so:

```yaml
    gate:
      run: "python check_skill_updates.py --range origin/master...HEAD"
      needs_git: true
```

`needs_git: true` works on a refusal too, and is what makes a git-shaped condition
constructible — replace the copied `.git` directory with a `.git` file and a tool
that refuses to run inside a linked worktree can be proven:

```yaml
  refusals:
    - tool: asset-indexer
      when: "the checkout is a git worktree"
      run: "./index.py"
      needs_git: true
      remove: ".git"
      fixture_path: ".git"
      fixture_body: "gitdir: /elsewhere/.git/worktrees/w\n"
      expect_exit: 1
      expect_output: "refusing to index"
```

The copy is verbatim, including a `.git` that is a file rather than a directory.
Rewriting that pointer to reach the original repository would let a command under
test write into the repository hullcheck is reading, and that is not a trade worth a
verdict.

### Refusals that turn on the environment

Writing a file, deleting a path, or running in an empty directory covers "the input
is absent" well. It does not cover the conditions unattended tools actually refuse
on, which are mostly environmental. `env:` replaces or adds variables and
`unset_env:` removes them, for the run that should refuse:

```yaml
refusals:
  - tool: merch-cost
    when: "node is not on PATH"
    run: "python3 publishing/merch_cost.py --check"
    env: { PATH: "/usr/bin:/bin" }
    expect_exit: 1
    expect_output: "needs `node`"

  - tool: uploader
    when: "the credential is absent"
    run: "./upload.sh"
    unset_env: [GOOGLE_APPLICATION_CREDENTIALS]
    expect_exit: 2
```

The **control still runs in the unmodified environment**, so "it worked normally,
then stopped in the declared way" remains the standard — a tool that is simply
broken cannot pass as one that refuses. The verdict names the change that proved it:

```
REFUSES  uploader  proven: worked normally, then stopped with exit 2 and said so
                   (with GOOGLE_APPLICATION_CREDENTIALS unset)
```

A value of `""` means present-but-empty, which is a different condition from absent;
`unset_env:` is how you say the second.

One practical note on a replaced `PATH`: the shell hullcheck starts is found on the
ambient `PATH`, so the command always runs — but anything the command itself looks
up is subject to the new `PATH`. Prefer `./tool.sh` over `sh tool.sh` there, or exit
127 will be reported as a crash rather than a refusal, which is what it is.

### UNPROVABLE: the verdict hullcheck could not earn

Without `needs_git`, a command whose condition is a git fact runs in a copy where
that fact cannot be true. It works, and the old audit called that **PROCEEDS** — the
finding this whole feature exists to produce — against a refusal that is correct,
load-bearing, and may already have prevented a real incident. Someone acting on it
would go and break a working check.

So hullcheck now reports `UNPROVABLE` instead, and names the fix:

```
  UNPROVABLE asset-indexer   the tool ran to success, but the condition is a git fact
                             and the scratch copy has no .git, so it was never put in
                             the situation it claims to refuse - add needs_git: true
```

`UNPROVABLE` is not a pass, and `--refusals` exits 1 on it: a green build on a claim
nothing tested is the exact silent downgrade this audit is for. A gate whose control
run fails only because git is missing is reported the same way — `BROKEN` is a finding
about the gate, and this is a finding about the experiment.

### The manifest format

`.hullcheck.yml` is read by a hand-written parser covering a subset of YAML. That is
a deliberate trade: a real YAML library would be one dependency, and *zero
third-party dependencies* is a claim a supply-chain-adjacent tool should be able to
make. Unknown keys are an error rather than a silently-ignored typo.

What it accepts:

| | |
|---|---|
| scalars | plain or double-quoted, on one line |
| block scalars | `\|`, `\|-`, `>`, `>-` — for prose that does not fit on one line |
| lists | inline `[a, b]`, or `- item` on their own lines |
| mappings | inline `{ k: v }`, or indented `k: v` lines |
| indentation | spaces, never tabs |

```yaml
  - id: RULES-8.8
    source: RULES.md
    statement: >-
      Every render and every publish updates the owning skill with what it
      taught, in the same PR.
    gate:
      kind: command
      run: "make check-skills"
      fixture_body: |
        a file whose presence
        should make the gate fail
```

A line that does not fit says so, and says what does:

```
hullcheck: .hullcheck.yml: line 30: expected "key: value", got "Every render and every"
  this file is read by a small YAML subset parser: plain or double-quoted
  single-line values, block scalars (|, |-, >, >-), lists inline as [a, b] or as
  "- item" lines, and mappings inline as { k: v } or as indented "k: v" lines;
  indentation must be spaces
```

`--print-manifest` writes the same sentence as a header comment, since that file is
where people start editing.

### `source:` is half a rule's name

A rule id is the clause number plus the document's basename, so a repository with
more than one `RULES.md` states two different rules called `RULES-5.3`. `source:`
is what tells them apart, and `--verify` addresses a rule by document **and** id:

```yaml
  - id: RULES-5.3
    source: RULES.md              # not second-brand/RULES.md
```

A declaration that omits `source:` still works where the id is unique. Where it is
not, nothing is changed and hullcheck says so, rather than picking one:

```
hullcheck: RULES-5.3: 2 rules in this repository carry that id and the
declaration names no source:, so none of them was changed
```

Two entries that address the same rule are refused when the manifest loads, and
`--print-manifest` marks any id it emitted more than once.

### What gets copied, and what stops it

A verify pass copies the repository once per rule, and a refusal audit twice per
declared refusal, so what goes into that copy matters.

The copy is **what git already knows about** — tracked files plus untracked files
`.gitignore` does not exclude. Build output and ignored media are never copied, which
is usually the difference between a copy that costs seconds and one that costs a
volume. Outside a git work tree hullcheck falls back to walking the directory.

The set is **measured before anything is written**, and a tree over the limit is
refused with its size rather than half-copied:

```
hullcheck: REFUSED

  the files git tracks here weigh 41.6 GiB across 90210 files, over the
  --max-copy limit of 2.0 GiB.
```

| flag | what it does |
|---|---|
| `--scratch-plan` | measure what would be copied, and copy nothing |
| `--max-copy SIZE` | raise the byte limit (default `2GiB`; `0` removes it) |
| `--max-files N` | raise the file-count limit (default `200000`; `0` removes it) |
| `--scratch-dir DIR` | put the copy on a volume with room, instead of `$TMPDIR` |

Each rule still gets its own fresh copy. Reusing one across rules would be faster and
would let one rule's fixture leak into the next rule's control, which is the kind of
cross-contamination that turns a proof back into a guess.

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
