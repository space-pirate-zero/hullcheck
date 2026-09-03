package model

import "testing"

func TestStageLadderIsOrdered(t *testing.T) {
	ladder := []Stage{PreCommit, PullReq, Nightly, Release, Manual, Never}
	for i := 1; i < len(ladder); i++ {
		if ladder[i-1].Rank() >= ladder[i].Rank() {
			t.Fatalf("%s must rank before %s", ladder[i-1], ladder[i])
		}
	}
}

func TestOnlyScheduledStagesHaveLatency(t *testing.T) {
	for _, s := range []Stage{PreCommit, PullReq, Nightly, Release} {
		if s.Latency() < 0 {
			t.Errorf("%s should have a latency", s)
		}
	}
	for _, s := range []Stage{Manual, Never} {
		if s.Latency() >= 0 {
			t.Errorf("%s has no automatic detection; latency must be negative", s)
		}
	}
}

func TestFindingStageTakesFastestGate(t *testing.T) {
	f := Finding{Gates: []Gate{{Stage: Nightly}, {Stage: PreCommit}, {Stage: Release}}}
	if got := f.Stage(); got != PreCommit {
		t.Fatalf("stage = %q, want pre-commit", got)
	}
	if got := (Finding{}).Stage(); got != Never {
		t.Fatalf("a finding with no gates must be %q, got %q", Never, got)
	}
}

func TestEmptyReportScoresZeroNotOneHundred(t *testing.T) {
	// A reading with no denominator must never flatter.
	var r Report
	if got := r.Coverage(); got != 0 {
		t.Fatalf("coverage = %v, want 0", got)
	}
	if got := r.WeightedCoverage(); got != 0 {
		t.Fatalf("weighted = %v, want 0", got)
	}
}

func TestSeverityWeighting(t *testing.T) {
	if High.Weight() <= Medium.Weight() || Medium.Weight() <= Low.Weight() {
		t.Fatal("severity weights must be strictly ordered")
	}
	if Unknown.Weight() != Medium.Weight() {
		t.Error("unknown severity should be treated as medium, not discounted")
	}
}

func TestFakeIsNeverCountedAsCovered(t *testing.T) {
	r := Report{Findings: []Finding{
		{Rule: Rule{Severity: High}, Verdict: Fake},
		{Rule: Rule{Severity: High}, Verdict: Hold},
	}}
	if got := r.Coverage(); got != 0.5 {
		t.Fatalf("coverage = %v, want 0.5 - FAKE must not count as covered", got)
	}
}
