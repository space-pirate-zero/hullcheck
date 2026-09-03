package match

import (
	"testing"

	"github.com/space-pirate-zero/hullcheck/internal/model"
)

func rule(id, stmt string) model.Rule {
	return model.Rule{ID: id, Statement: stmt, Severity: model.High}
}

func gate(name, run, file string, st model.Stage) model.Gate {
	return model.Gate{ID: file + "::" + name, Name: name, Run: run, File: file, Stage: st}
}

func verdictOf(rep model.Report, id string) model.Verdict {
	for _, f := range rep.Findings {
		if f.Rule.ID == id {
			return f.Verdict
		}
	}
	return ""
}

func TestExplicitGateHintHolds(t *testing.T) {
	rs := []model.Rule{{ID: "R-1", Statement: "Every asset records its provenance.",
		Severity: model.High, GateHint: "make preflight"}}
	gs := []model.Gate{gate("preflight", "make preflight", "Makefile", model.PullReq)}
	rep := Run(rs, gs)
	if got := verdictOf(rep, "R-1"); got != model.Hold {
		t.Fatalf("verdict = %q, want HOLD (rule names its own gate)", got)
	}
	if rep.Findings[0].Why == "" {
		t.Error("a match must record why, so a reader can audit it")
	}
}

func TestDistinctiveTokenHolds(t *testing.T) {
	rs := []model.Rule{rule("R-1", "Every asset must record its provenance.")}
	gs := []model.Gate{gate("provenance", "python check_provenance.py",
		"check_provenance.py", model.PullReq)}
	if got := verdictOf(Run(rs, gs), "R-1"); got != model.Hold {
		t.Fatalf("verdict = %q, want HOLD", got)
	}
}

func TestUnrelatedGateDoesNotHold(t *testing.T) {
	// The worst failure this tool can have is inflating coverage. A generic gate
	// must never be credited against an unrelated rule.
	rs := []model.Rule{rule("R-1", "Secrets must never enter git.")}
	gs := []model.Gate{gate("build", "go build ./...", "Makefile", model.PullReq)}
	if got := verdictOf(Run(rs, gs), "R-1"); got != model.Breach {
		t.Fatalf("verdict = %q, want BREACH — an unrelated gate was credited", got)
	}
}

func TestStopwordsDoNotMatch(t *testing.T) {
	// "should" and "always" are everywhere; they must not link anything.
	rs := []model.Rule{rule("R-1", "Builds should always be reproducible.")}
	gs := []model.Gate{gate("always", "echo always", "always.sh", model.PullReq)}
	if got := verdictOf(Run(rs, gs), "R-1"); got != model.Breach {
		t.Fatalf("verdict = %q, want BREACH — matched on a stopword", got)
	}
}

func TestUngatedRuleIsBreach(t *testing.T) {
	rs := []model.Rule{rule("R-1", "Services must never read another tenant's data.")}
	if got := verdictOf(Run(rs, nil), "R-1"); got != model.Breach {
		t.Fatalf("verdict = %q, want BREACH", got)
	}
}

func TestUnmatchedGateIsUnlogged(t *testing.T) {
	gs := []model.Gate{gate("licence", "./check_licence.sh", "check_licence.sh", model.PullReq)}
	rep := Run(nil, gs)
	if len(rep.Unlogged) != 1 {
		t.Fatalf("unlogged = %+v, want the one unmatched gate", rep.Unlogged)
	}
	if rep.Unlogged[0].Name != "licence" {
		t.Errorf("unlogged gate = %q", rep.Unlogged[0].Name)
	}
}

func TestFindingTakesFastestStage(t *testing.T) {
	rs := []model.Rule{rule("R-1", "Every asset must record its provenance.")}
	gs := []model.Gate{
		gate("provenance-nightly", "check_provenance.py", "a.yml", model.Nightly),
		gate("provenance-hook", "check_provenance.py", "b.yml", model.PreCommit),
	}
	rep := Run(rs, gs)
	if got := rep.Findings[0].Stage(); got != model.PreCommit {
		t.Fatalf("stage = %q, want pre-commit (fastest gate wins)", got)
	}
}

func TestCoverageAndWeighting(t *testing.T) {
	rs := []model.Rule{
		{ID: "R-1", Statement: "Every asset must record its provenance.", Severity: model.High},
		{ID: "R-2", Statement: "Prefer tabs over spaces in generated output.", Severity: model.Low},
	}
	gs := []model.Gate{gate("provenance", "check_provenance.py", "check_provenance.py", model.PullReq)}
	rep := Run(rs, gs)
	if got := rep.Coverage(); got != 0.5 {
		t.Errorf("coverage = %v, want 0.5", got)
	}
	// One high rule (weight 3) held of 3+1=4 total weight.
	if got := rep.WeightedCoverage(); got != 0.75 {
		t.Errorf("weighted coverage = %v, want 0.75", got)
	}
}
