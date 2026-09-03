// Package hullcheck is the public API: read a repository's gate coverage from Go,
// and assert on it inside an ordinary test suite.
//
// Adding hullcheck to a Go project should cost one test function:
//
//	func TestGateCoverage(t *testing.T) {
//	    hullcheck.AssertCoverage(t, ".", 80)
//	}
//
// It imports nothing outside the standard library, makes no network calls, and
// never writes to the directory it reads.
package hullcheck

import (
	"fmt"

	"github.com/space-pirate-zero/hullcheck/internal/model"
	"github.com/space-pirate-zero/hullcheck/internal/scan"
)

// Verdict values, re-exported so callers need not reach into internal packages.
const (
	Hold   = string(model.Hold)
	Breach = string(model.Breach)
	Fake   = string(model.Fake)
)

// Finding is one rule and how it fared.
type Finding struct {
	RuleID    string
	Statement string
	Source    string
	Severity  string
	Verdict   string
	// Stage is where the fastest gate for this rule runs: pre-commit,
	// pull-request, nightly, release, manual, or never.
	Stage string
	Why   string
}

// Report is a reading of one repository.
type Report struct {
	Root string
	// Docs are the policy documents the rules were read from.
	Docs     []string
	Findings []Finding
	// UnloggedGates are gates that run while enforcing nothing anyone wrote down.
	UnloggedGates []string

	coverage         float64
	weightedCoverage float64
}

// Coverage is the share of stated rules that hold, as a percentage.
func (r Report) Coverage() float64 { return r.coverage * 100 }

// WeightedCoverage weights each rule by declared severity, so gating three trivial
// rules cannot outscore gating one expensive one.
func (r Report) WeightedCoverage() float64 { return r.weightedCoverage * 100 }

// Breaches returns the rules nothing enforces.
func (r Report) Breaches() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Verdict == Breach {
			out = append(out, f)
		}
	}
	return out
}

// Read produces a reading of the repository rooted at dir.
//
// It returns an error rather than a flattering score when a repository states no
// rules at all: a reading with no denominator is a lie.
func Read(dir string) (Report, error) {
	rep, err := scan.Run(dir)
	if err != nil {
		return Report{}, err
	}
	out := Report{
		Root: rep.Root, Docs: rep.Docs,
		coverage: rep.Coverage(), weightedCoverage: rep.WeightedCoverage(),
	}
	for _, f := range rep.Findings {
		out.Findings = append(out.Findings, Finding{
			RuleID: f.Rule.ID, Statement: f.Rule.Statement, Source: f.Rule.Source,
			Severity: string(f.Rule.Severity), Verdict: string(f.Verdict),
			Stage: string(f.Stage()), Why: f.Why,
		})
	}
	for _, g := range rep.Unlogged {
		out.UnloggedGates = append(out.UnloggedGates, g.File+"::"+g.Name)
	}
	return out, nil
}

// TestingT is the slice of *testing.T this package needs. Declaring it here keeps
// the standard library's testing package out of a non-test build.
type TestingT interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// AssertCoverage fails the test if gate coverage in dir is below minPercent, and
// names the rules responsible — a bare percentage tells you that you failed, not
// what to fix.
func AssertCoverage(t TestingT, dir string, minPercent float64) Report {
	t.Helper()
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("hullcheck: %v", err)
		return Report{}
	}
	if got := rep.Coverage(); got < minPercent {
		t.Errorf("gate coverage %.0f%% is below the required %.0f%%\n%s",
			got, minPercent, describe(rep.Breaches()))
	}
	return rep
}

// AssertNoNewBreaches fails if any rule outside allowed is unenforced. Use it to
// ratchet: a repository can carry known gaps without letting new ones in.
func AssertNoNewBreaches(t TestingT, dir string, allowed ...string) Report {
	t.Helper()
	skip := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		skip[a] = true
	}
	rep, err := Read(dir)
	if err != nil {
		t.Fatalf("hullcheck: %v", err)
		return Report{}
	}
	var fresh []Finding
	for _, f := range rep.Breaches() {
		if !skip[f.RuleID] {
			fresh = append(fresh, f)
		}
	}
	if len(fresh) > 0 {
		t.Errorf("%d rule(s) newly unenforced\n%s", len(fresh), describe(fresh))
	}
	return rep
}

func describe(fs []Finding) string {
	s := ""
	for _, f := range fs {
		s += fmt.Sprintf("  BREACH  %-18s %s  (%s)\n", f.RuleID, f.Statement, f.Source)
	}
	return s
}
