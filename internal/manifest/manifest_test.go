package manifest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/space-pirate-zero/hullcheck/internal/model"
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

func TestFixtureBodyCarriesEscapes(t *testing.T) {
	// Fixture bodies are usually several lines of a deliberately broken file.
	f, err := Parse(strings.NewReader(
		"version: 1\nrules:\n  - id: R\n    gate:\n      run: x\n" +
			"      fixture_body: \"package p\\nfunc  Bad( ) {}\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := f.Rules[0].Gate.FixtureBody
	want := "package p\nfunc  Bad( ) {}"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	f, found, err := Load(t.TempDir() + "/nope.yml")
	if err != nil || found || f != nil {
		t.Fatalf("missing manifest must mean unverified mode, got (%v,%v,%v)", f, found, err)
	}
}

// Issue #2: two entries that address the same rule cannot both be right, and
// letting the second win silently is how one declaration flips a rule its author
// never read.
func TestDuplicateDeclarationsAreRefused(t *testing.T) {
	for name, in := range map[string]string{
		"same source and id": "version: 1\nrules:\n" +
			"  - id: R-1.1\n    source: RULES.md\n" +
			"  - id: R-1.1\n    source: RULES.md\n",
		"both unqualified": "version: 1\nrules:\n" +
			"  - id: R-1.1\n" +
			"  - id: R-1.1\n",
		"line numbers differ, document does not": "version: 1\nrules:\n" +
			"  - id: R-1.1\n    source: RULES.md:12\n" +
			"  - id: R-1.1\n    source: RULES.md:98\n",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: expected a refusal, got none", name)
		}
	}
}

// The same id in two different documents is not a duplicate: it is what the
// naming scheme produces, and `source:` is what tells them apart.
func TestSameIDInDifferentDocumentsIsAllowed(t *testing.T) {
	f, err := Parse(strings.NewReader("version: 1\nrules:\n" +
		"  - id: R-5.3\n    source: RULES.md\n" +
		"  - id: R-5.3\n    source: second-brand/RULES.md\n"))
	if err != nil {
		t.Fatalf("two documents, two rules, one id: %v", err)
	}
	if len(f.Rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(f.Rules))
	}
	if f.Rules[0].Key() == f.Rules[1].Key() {
		t.Errorf("both rules share the key %q", f.Rules[0].Key())
	}
}

func TestSourceFileDropsTheLineNumber(t *testing.T) {
	for in, want := range map[string]string{
		"RULES.md:230": "RULES.md", "RULES.md": "RULES.md",
		"a/b/RULES.md:1": "a/b/RULES.md", "": "",
		// A colon that is not a line number is part of the path.
		"weird:name.md": "weird:name.md",
	} {
		if got := (Rule{Source: in}).SourceFile(); got != want {
			t.Errorf("SourceFile(%q) = %q, want %q", in, got, want)
		}
	}
}

// A generated manifest is where people start editing, so it has to warn about the
// one thing that will silently bite them.
func TestPrintWarnsAboutIDsSharedBetweenDocuments(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{
		{Rule: model.Rule{ID: "RULES-5.3", File: "RULES.md", Source: "RULES.md:230"}},
		{Rule: model.Rule{ID: "RULES-5.3", File: "other/RULES.md", Source: "other/RULES.md:81"}},
	}}
	var sb strings.Builder
	if err := Print(&sb, rep); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "more than one policy document") || !strings.Contains(out, "RULES-5.3") {
		t.Errorf("the generated manifest must name the shared id:\n%s", out)
	}
	// The source must be the document, not "document:line": a line number moves
	// on every edit and would break the declaration it is meant to anchor.
	if strings.Contains(out, "source: RULES.md:230") {
		t.Errorf("source must not carry a line number:\n%s", out)
	}
	if !strings.Contains(out, "source: RULES.md\n") || !strings.Contains(out, "source: other/RULES.md\n") {
		t.Errorf("each entry must name its own document:\n%s", out)
	}
	// And what it prints must parse, including the two same-id entries.
	if _, err := Parse(strings.NewReader(out)); err != nil {
		t.Errorf("a generated manifest must parse: %v", err)
	}
}

// needs_git is what makes a git-dependent gate or refusal auditable at all, so it
// has to survive the parser on both.
func TestNeedsGitParsesOnGatesAndRefusals(t *testing.T) {
	f, err := Parse(strings.NewReader("version: 1\nrules:\n" +
		"  - id: R-1\n    source: RULES.md\n    gate:\n      kind: command\n" +
		"      run: \"git log\"\n      needs_git: true\n" +
		"refusals:\n  - tool: t\n    run: \"./t\"\n    needs_git: true\n    expect_exit: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Rules[0].Gate == nil || !f.Rules[0].Gate.NeedsGit {
		t.Errorf("gate needs_git was not read: %+v", f.Rules[0].Gate)
	}
	if !f.Refusals[0].NeedsGit {
		t.Errorf("refusal needs_git was not read: %+v", f.Refusals[0])
	}
}

func TestEnvAndUnsetEnvParse(t *testing.T) {
	f, err := Parse(strings.NewReader("version: 1\nrefusals:\n  - tool: t\n    run: \"./t\"\n" +
		"    env: { PATH: \"/usr/bin:/bin\", TOKEN: \"\" }\n" +
		"    unset_env: [GOOGLE_APPLICATION_CREDENTIALS, AWS_PROFILE]\n    expect_exit: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := f.Refusals[0]
	if r.Env["PATH"] != "/usr/bin:/bin" {
		t.Errorf("a quoted value must keep its colons: %q", r.Env["PATH"])
	}
	if v, ok := r.Env["TOKEN"]; !ok || v != "" {
		t.Errorf("present-but-empty must survive as its own condition: %q %v", v, ok)
	}
	if len(r.UnsetEnv) != 2 || r.UnsetEnv[0] != "GOOGLE_APPLICATION_CREDENTIALS" {
		t.Errorf("unset_env = %v", r.UnsetEnv)
	}
}

func TestBadEnvIsRefusedNotGuessed(t *testing.T) {
	for name, in := range map[string]string{
		"not a mapping": "version: 1\nrefusals:\n  - tool: t\n    env: PATH=/bin\n",
		"no value":      "version: 1\nrefusals:\n  - tool: t\n    env: { PATH }\n",
		"empty name":    "version: 1\nrefusals:\n  - tool: t\n    env: { : x }\n",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// A declaration that cannot mean anything must be refused, not written into an
// environment where nothing can read it back.
func TestUnusableEnvNamesAreRefused(t *testing.T) {
	for name, in := range map[string]string{
		"a space":       "version: 1\nrefusals:\n  - tool: t\n    env: { MY VAR: x }\n",
		"leading digit": "version: 1\nrefusals:\n  - tool: t\n    env: { 2FA: x }\n",
		"punctuation":   "version: 1\nrefusals:\n  - tool: t\n    env: { \"A-B\": x }\n",
		"in unset_env":  "version: 1\nrefusals:\n  - tool: t\n    unset_env: [MY VAR]\n",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// Setting and unsetting the same variable says two opposite things, and picking
// one silently audits a tool under a condition its author did not declare.
func TestAVariableInBothEnvAndUnsetEnvIsRefused(t *testing.T) {
	_, err := Parse(strings.NewReader("version: 1\nrefusals:\n  - tool: t\n" +
		"    env: { TOKEN: \"x\" }\n    unset_env: [TOKEN]\n"))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("the error must name the variable: %v", err)
	}
}
