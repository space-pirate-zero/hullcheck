// Package model holds the vocabulary hullcheck reports in. The words matter: they
// are what users repeat, and they come from the hull metaphor.
package model

// Verdict is the state of a single stated rule.
type Verdict string

const (
	// Hold: the rule has a gate, and something runs.
	Hold Verdict = "HOLD"
	// Breach: the rule is stated and nothing runs on it.
	Breach Verdict = "BREACH"
	// Fake: a gate exists and passes even when its rule is broken — a patch
	// that is only paint. Worse than a breach, because it manufactures
	// confidence. Only reachable in verified mode.
	Fake Verdict = "FAKE"
)

// Severity is how expensive a violation is, as declared by the repo.
type Severity string

const (
	High    Severity = "high"
	Medium  Severity = "medium"
	Low     Severity = "low"
	Unknown Severity = "unknown"
)

// Weight is the multiplier severity contributes to a weighted score.
func (s Severity) Weight() float64 {
	switch s {
	case High:
		return 3
	case Medium:
		return 2
	case Low:
		return 1
	default:
		return 2
	}
}

// Stage is where in the lifecycle a gate runs. The order of these constants is
// the order of the Time-to-Truth ladder.
type Stage string

const (
	PreCommit Stage = "pre-commit"
	PullReq   Stage = "pull-request"
	Nightly   Stage = "nightly"
	Release   Stage = "release"
	// Manual: the gate exists and works, but nothing schedules it. It runs only
	// when a human remembers to run it, which is not a detection latency.
	Manual Stage = "manual"
	Never  Stage = "never"
)

// Latency is the representative time-to-detection for a stage, in seconds.
// These are deliberately coarse: the point is the order of magnitude, and a
// precise-looking number would imply a measurement we did not take.
func (s Stage) Latency() int {
	switch s {
	case PreCommit:
		return 10
	case PullReq:
		return 12 * 60
	case Nightly:
		return 14 * 3600
	case Release:
		return 9 * 24 * 3600
	default:
		return -1 // manual or never: no automatic detection
	}
}

// Rank orders stages fastest to slowest for sorting and for picking the best
// gate when a rule has several.
func (s Stage) Rank() int {
	switch s {
	case PreCommit:
		return 0
	case PullReq:
		return 1
	case Nightly:
		return 2
	case Release:
		return 3
	case Manual:
		return 4
	default:
		return 5
	}
}

// Kind is how a gate is executed.
type Kind string

const (
	KindCommand   Kind = "command"
	KindCIJob     Kind = "ci-job"
	KindPreCommit Kind = "pre-commit"
	KindMakefile  Kind = "makefile"
	KindOPA       Kind = "opa"
	KindTest      Kind = "test"
)

// Rule is a stated constraint found in a policy document.
type Rule struct {
	ID        string   `json:"id"`
	Source    string   `json:"source"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	Statement string   `json:"statement"`
	Severity  Severity `json:"severity"`
	// GateHint is an explicit gate named by the rule itself, e.g. a
	// "Gate: make preflight" annotation. Strongest possible match signal.
	GateHint string `json:"gate_hint,omitempty"`
}

// Gate is something in the repository that runs and can fail a build.
type Gate struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Name  string `json:"name"`
	Run   string `json:"run,omitempty"`
	Stage Stage  `json:"stage"`
}

// Finding pairs a rule with whatever was found to enforce it.
type Finding struct {
	Rule    Rule    `json:"rule"`
	Gates   []Gate  `json:"gates,omitempty"`
	Verdict Verdict `json:"verdict"`
	// Why records the evidence for a match in plain language, so a reader can
	// audit the matcher without reading the matcher.
	Why string `json:"why,omitempty"`
}

// Stage returns the fastest stage among a finding's gates.
func (f Finding) Stage() Stage {
	best := Never
	for _, g := range f.Gates {
		if g.Stage.Rank() < best.Rank() {
			best = g.Stage
		}
	}
	return best
}

// Report is a complete reading of a repository.
type Report struct {
	Root     string    `json:"root"`
	Verified bool      `json:"verified"`
	Findings []Finding `json:"findings"`
	// Unlogged are gates that run but enforce nothing anyone wrote down.
	Unlogged []Gate   `json:"unlogged"`
	Docs     []string `json:"policy_documents"`
}

// Counts summarises verdicts.
func (r Report) Counts() map[Verdict]int {
	c := map[Verdict]int{Hold: 0, Breach: 0, Fake: 0}
	for _, f := range r.Findings {
		c[f.Verdict]++
	}
	return c
}

// Coverage is the share of rules that hold, 0..1. Fake never counts as covered.
func (r Report) Coverage() float64 {
	if len(r.Findings) == 0 {
		return 0
	}
	return float64(r.Counts()[Hold]) / float64(len(r.Findings))
}

// WeightedCoverage weights each rule by its declared severity, so gating three
// trivial rules cannot outscore gating one expensive one.
func (r Report) WeightedCoverage() float64 {
	var held, total float64
	for _, f := range r.Findings {
		w := f.Rule.Severity.Weight()
		total += w
		if f.Verdict == Hold {
			held += w
		}
	}
	if total == 0 {
		return 0
	}
	return held / total
}
