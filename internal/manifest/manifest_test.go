package manifest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
)

func TestParsesAWellFormedManifest(t *testing.T) {
	f, err := Parse(strings.NewReader(`version: 1
rules:
  - id: R-1
    source: RULES.md:7
    statement: "Every asset records its provenance."
    severity: high
    gate:
      kind: command
      run: "make provenance"
      proves: "exits non-zero when a sidecar is missing"
      fixture_path: art/orphan.png
      fixture_body: "x"
  - id: R-2
    statement: "Secrets must never enter git."
    severity: high
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(f.Rules))
	}
	g := f.Rules[0].Gate
	if g == nil || g.Run != "make provenance" || g.FixturePath != "art/orphan.png" {
		t.Fatalf("gate parsed wrong: %+v", g)
	}
	if f.Rules[1].Gate != nil {
		t.Error("R-2 declares no gate; it must parse as nil, not empty")
	}
}

func TestRejectsUnknownKeysRatherThanIgnoringThem(t *testing.T) {
	// A typo must be an error the user sees, not a rule that silently stops
	// being checked. That is the whole failure mode this tool exists to find.
	for _, bad := range []string{
		"version: 1\nrules:\n  - id: R\n    statment: typo\n",
		"version: 1\nrules:\n  - id: R\n    gate:\n      runn: oops\n",
		"verison: 1\n",
	} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("silently accepted a typo:\n%s", bad)
		}
	}
}

func TestRejectsTabsAndFutureVersions(t *testing.T) {
	if _, err := Parse(strings.NewReader("version: 1\nrules:\n\t- id: R\n")); err == nil {
		t.Error("tabs must be rejected")
	}
	if _, err := Parse(strings.NewReader("version: 99\n")); err == nil {
		t.Error("an unsupported version must be refused, not guessed at")
	}
}

func TestRequiresAnID(t *testing.T) {
	if _, err := Parse(strings.NewReader("version: 1\nrules:\n  - statement: \"x\"\n")); err == nil {
		t.Error("a rule with no id must be refused")
	}
}

func TestPrintRoundTrips(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{
		{Rule: model.Rule{ID: "R-1", Source: "RULES.md:7", Statement: "Assets record provenance.",
			Severity: model.High},
			Gates:   []model.Gate{{Kind: model.KindCommand, Run: "make prov"}},
			Verdict: model.Hold},
		{Rule: model.Rule{ID: "R-2", Statement: "Secrets never enter git.", Severity: model.High},
			Verdict: model.Breach},
	}}
	var b bytes.Buffer
	if err := Print(&b, rep); err != nil {
		t.Fatal(err)
	}
	f, err := Parse(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatalf("printed a manifest we cannot read back: %v\n%s", err, b.String())
	}
	if len(f.Rules) != 2 {
		t.Fatalf("round trip lost rules: %+v", f.Rules)
	}
	if f.Rules[0].Gate == nil || f.Rules[0].Gate.Run != "make prov" {
		t.Errorf("gate lost in round trip: %+v", f.Rules[0].Gate)
	}
	if !strings.Contains(b.String(), "BREACH") {
		t.Error("the printed manifest should mark ungated rules for the reader")
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	f, found, err := Load(t.TempDir() + "/nope.yml")
	if err != nil || found || f != nil {
		t.Fatalf("missing manifest must mean unverified mode, got (%v,%v,%v)", f, found, err)
	}
}
