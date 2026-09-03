# Contributing to hullcheck

Every rule below names the gate that enforces it. If you add a rule here without a
gate, `hullcheck` will report it as a BREACH against its own repository, and CI will
fail. That is deliberate — this project has to survive its own tool.

## 1. Engineering rules

1.1 The core must have zero third-party dependencies. A supply-chain-adjacent tool
that carries a supply chain is a joke.

*Gate: make deps*

1.2 The core must never import a network package. "No network calls" has to be a
property of the artifact, not a promise in a README.

*Gate: make network*

1.3 Every source file must be gofmt-clean before it is committed.

*Gate: make fmt*

1.4 All tests must pass, with the race detector enabled.

*Gate: make test*

1.5 Code must never ship with a `go vet` finding.

*Gate: make vet*

1.6 hullcheck must never write to the repository it is reading.

*Gate: make readonly*

1.7 hullcheck must score its own repository, and must not regress below its
declared threshold.

*Gate: make dogfood*

## 2. Review rules

2.1 A pull request must not reduce gate coverage. Adding a rule without adding its
gate is the one change this project cannot accept.

*Gate: make dogfood*

2.2 Every exported symbol must carry a doc comment explaining why it exists, not
what it does. The what is readable; the why is not.

*Gate: make vet*

## 3. Behaviour rules

3.1 hullcheck must never report a score for a repository that states no rules. A
reading with no denominator is a lie, so it refuses and exits 2.

*Gate: make test*

3.2 The banner must never be written to stdout, because it would corrupt piped
JSON output.

*Gate: make test*

3.3 Secrets must never enter git. hullcheck reads repositories; it has no business
holding credentials.

*Gate: make secrets*
