package match

import (
	"fmt"
	"strings"
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

// Issue #5: in a monorepo a path is a topic list, not evidence. Every rule that
// mentions a directory name was credited to every gate that happens to live
// under it.
func TestADirectoryNameDoesNotLinkARuleToAGate(t *testing.T) {
	rs := []model.Rule{rule("R-1", "New titles start from the template; never copy an existing book.")}
	gs := []model.Gate{
		gate("brand-check", "make brand-check", "books/_template/Makefile", model.PullReq),
	}
	if got := verdictOf(Run(rs, gs), "R-1"); got != model.Breach {
		t.Fatalf("verdict = %q, want BREACH — the rule was credited to a gate on the "+
			"directory it happens to live in", got)
	}
}

// A check script's command is its own path, so the directories must not come back
// in through the command.
func TestAPathInTheCommandDoesNotLinkEither(t *testing.T) {
	rs := []model.Rule{rule("R-1", "New titles start from the template; never copy an existing book.")}
	gs := []model.Gate{
		gate("check_style.py", "books/nightly/check_style.py", "books/nightly/check_style.py", model.PullReq),
	}
	if got := verdictOf(Run(rs, gs), "R-1"); got != model.Breach {
		t.Fatalf("verdict = %q, want BREACH — a path leaked in through the command", got)
	}
}

// The filename is weak evidence, so it takes two words agreeing.
func TestAFilenameLinksOnlyWhenTwoWordsAgree(t *testing.T) {
	one := []model.Gate{gate("job", "bash ci.sh", "provenance.sh", model.PullReq)}
	rs := []model.Rule{rule("R-1", "Every asset must record its provenance somewhere durable.")}
	if got := verdictOf(Run(rs, one), "R-1"); got != model.Breach {
		t.Errorf("verdict = %q, want BREACH — one shared word with a filename is not evidence", got)
	}

	two := []model.Gate{gate("job", "bash ci.sh", "provenance_sidecar.sh", model.PullReq)}
	rs = []model.Rule{rule("R-1", "Every asset must record its provenance in a sidecar.")}
	if got := verdictOf(Run(rs, two), "R-1"); got != model.Hold {
		t.Errorf("verdict = %q, want HOLD — two words agreeing in a filename is a link", got)
	}
}

// A word that turns up in a quarter of every gate is describing the repository,
// not the rule.
func TestATokenCommonAcrossGatesCarriesNoMatch(t *testing.T) {
	var gs []model.Gate
	for i := 0; i < 12; i++ {
		gs = append(gs, gate(fmt.Sprintf("brand-check-%d", i),
			fmt.Sprintf("make brand-check-%d", i), fmt.Sprintf("books/b%d/Makefile", i), model.PullReq))
	}
	rs := []model.Rule{rule("R-1", "Only brand voice identifiers from the registry may be used.")}
	if got := verdictOf(Run(rs, gs), "R-1"); got != model.Breach {
		t.Fatalf("verdict = %q, want BREACH — \"brand\" is in every gate and identifies none", got)
	}

	// The same word in a repository where only one gate carries it is evidence.
	few := []model.Gate{
		gate("brand-check", "make brand-check", "Makefile", model.PullReq),
		gate("fmt", "make fmt", "Makefile", model.PullReq),
	}
	if got := verdictOf(Run(rs, few), "R-1"); got != model.Hold {
		t.Errorf("verdict = %q, want HOLD — one gate named for the rule is a link", got)
	}
}

// The evidence has to name where it came from, or a reader cannot audit it.
func TestWhyNamesTheProvenanceOfTheMatch(t *testing.T) {
	rs := []model.Rule{rule("R-1", "Every asset must record its provenance.")}
	gs := []model.Gate{gate("provenance", "make provenance", "Makefile", model.PullReq)}
	rep := Run(rs, gs)
	if why := rep.Findings[0].Why; !strings.Contains(why, "the gate's name") {
		t.Errorf("why = %q, want it to say the match came from the gate's name", why)
	}
}

func TestBaseAndFlatten(t *testing.T) {
	for in, want := range map[string]string{
		"a/b/c.py": "c.py", "c.py": "c.py", "": "", "a/": "",
	} {
		if got := base(in); got != want {
			t.Errorf("base(%q) = %q, want %q", in, got, want)
		}
	}
	if got := flatten("python books/nightly/check.py --strict"); got != "python check.py --strict" {
		t.Errorf("flatten = %q", got)
	}
	if got := flatten("make deps"); got != "make deps" {
		t.Errorf("flatten must leave a plain command alone, got %q", got)
	}
}
