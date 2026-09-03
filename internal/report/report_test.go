package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
)

func sample() model.Report {
	return model.Report{
		Root: ".", Docs: []string{"RULES.md"},
		Findings: []model.Finding{
			{Rule: model.Rule{ID: "R-1", Statement: "Secrets must never enter git.", Severity: model.High},
				Gates: []model.Gate{{Name: "secrets", Stage: model.PreCommit}}, Verdict: model.Hold},
			{Rule: model.Rule{ID: "R-2", Statement: "Every asset records provenance.", Severity: model.High},
				Verdict: model.Breach},
			{Rule: model.Rule{ID: "R-3", Statement: "Prefer tabs.", Severity: model.Low},
				Gates: []model.Gate{{Name: "fmt", Stage: model.Nightly}}, Verdict: model.Hold},
		},
		Unlogged: []model.Gate{{Name: "licence", Stage: model.PullReq}},
	}
}

func TestTimingCountsNeverForBreaches(t *testing.T) {
	tt := Timing(sample())
	if tt.Never != 1 {
		t.Errorf("never = %d, want 1 (the breached rule)", tt.Never)
	}
	if !tt.WorstNever {
		t.Error("worst must be 'never' while any rule is unenforced")
	}
	if tt.ByStage[model.PreCommit] != 1 || tt.ByStage[model.Nightly] != 1 {
		t.Errorf("stage distribution wrong: %+v", tt.ByStage)
	}
}

func TestManualGatesAreNotAutomaticDetection(t *testing.T) {
	r := model.Report{Findings: []model.Finding{{
		Rule: model.Rule{ID: "R-1"}, Verdict: model.Hold,
		Gates: []model.Gate{{Name: "x", Stage: model.Manual}},
	}}}
	if tt := Timing(r); tt.Never != 1 {
		t.Fatalf("a manual gate must count as 'never' for detection latency, got %+v", tt)
	}
}

func TestDurationReadsLikeAnEngineer(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{-1, "never"}, {10, "10s"}, {720, "12m"}, {50400, "14h"}, {777600, "9d"},
	} {
		if got := Duration(tc.in); got != tc.want {
			t.Errorf("Duration(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTextMentionsEveryVerdictAndTheDisclaimer(t *testing.T) {
	var b bytes.Buffer
	Text(&b, sample(), false)
	out := b.String()
	for _, want := range []string{"HOLD", "BREACH", "UNLOGGED", "TIME-TO-TRUTH",
		"Gate Coverage", "READING, not a score"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q\n%s", want, out)
		}
	}
}

func TestVerifiedOutputDropsTheDisclaimer(t *testing.T) {
	var b bytes.Buffer
	Text(&b, sample(), true)
	if strings.Contains(b.String(), "READING, not a score") {
		t.Error("a verified run must not call itself unverified")
	}
}

func TestJSONIsValidAndCarriesTheScore(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, sample(), false); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("emitted invalid JSON: %v", err)
	}
	if _, ok := got["gate_coverage"]; !ok {
		t.Error("JSON must carry gate_coverage")
	}
	if _, ok := got["time_to_truth"]; !ok {
		t.Error("JSON must carry time_to_truth")
	}
}

func TestMarkdownIsPRShaped(t *testing.T) {
	var b bytes.Buffer
	Markdown(&b, sample())
	out := b.String()
	if !strings.HasPrefix(out, "### hullcheck") || !strings.Contains(out, "| verdict | count |") {
		t.Errorf("markdown not PR-shaped:\n%s", out)
	}
}

func TestTruncateNeverPanics(t *testing.T) {
	for _, n := range []int{0, 1, 2, 5, 100} {
		_ = truncate("a reasonably long statement", n)
	}
}
