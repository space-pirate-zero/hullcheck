package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The exit-code contract is this tool's public API. These tests are the contract.

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func gated(t *testing.T) string {
	return repo(t, map[string]string{
		"RULES.md":            "1.1 Every asset must record its provenance.\n",
		"check_provenance.sh": "exit 0\n",
	})
}

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = execute(args, &o, &e)
	return code, o.String(), e.String()
}

func TestExitZeroOnAReading(t *testing.T) {
	code, out, _ := run(t, "--no-banner", gated(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "HOLD") {
		t.Errorf("stdout missing a verdict:\n%s", out)
	}
}

func TestExitTwoWhenThereIsNoPolicy(t *testing.T) {
	code, out, errOut := run(t, "--no-banner", t.TempDir())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "UNKNOWN") {
		t.Errorf("a refusal must say UNKNOWN:\n%s", errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("a refusal must not emit a reading on stdout:\n%s", out)
	}
}

func TestExitOneWhenBelowThreshold(t *testing.T) {
	r := repo(t, map[string]string{"RULES.md": "1.1 Secrets must never enter git.\n"})
	code, _, errOut := run(t, "--no-banner", "--fail-under", "50", r)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "below --fail-under") {
		t.Errorf("threshold breach must explain itself:\n%s", errOut)
	}
}

func TestExitZeroWhenThresholdIsMet(t *testing.T) {
	if code, _, _ := run(t, "--no-banner", "--fail-under", "50", gated(t)); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

func TestJSONGoesToStdoutAndIsCleanWhenPiped(t *testing.T) {
	code, out, _ := run(t, "--json", gated(t))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout was not clean JSON (chrome leaked into the data plane): %v\n%s", err, out)
	}
}

func TestBannerNeverReachesStdout(t *testing.T) {
	_, out, _ := run(t, gated(t))
	if strings.Contains(out, "#####") {
		t.Fatal("the banner reached stdout; `hullcheck --json | jq` would break")
	}
}

func TestVersionExitsZero(t *testing.T) {
	code, out, _ := run(t, "--version")
	if code != 0 || !strings.Contains(out, "hullcheck") {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
}

func TestUnknownFlagIsRefusedNotIgnored(t *testing.T) {
	if code, _, _ := run(t, "--nonsense"); code != 2 {
		t.Fatalf("exit = %d, want 2 for an unknown flag", code)
	}
}

func gitFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(a ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, a...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	mk := func(n, b string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, n), []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	mk("RULES.md", "1.1 Every asset must record its provenance.\n")
	mk("check_provenance.sh", "exit 0\n")
	run("add", "-A")
	run("commit", "-qm", "base")
	run("tag", "v1")
	mk("RULES.md", "1.1 Every asset must record its provenance.\n1.2 Secrets must never enter git.\n")
	return root
}

func TestDiffExitsOneOnANewlyUnenforcedRule(t *testing.T) {
	code, out, _ := run(t, "diff", "--base", "v1", gitFixture(t))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "RULES-1.2") {
		t.Errorf("diff must name the rule:\n%s", out)
	}
}

func TestDiffExitsZeroWhenNothingRegressed(t *testing.T) {
	r := gitFixture(t)
	// Compare the base against itself: nothing new can have been added.
	code, _, _ := run(t, "diff", "--base", "HEAD", r)
	if code != 1 {
		t.Skip("working tree differs from HEAD by design in this fixture")
	}
}

func TestSinceProducesADriftTable(t *testing.T) {
	code, out, _ := run(t, "--no-banner", "--since", "v1", gitFixture(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"HULLCHECK drift", "working tree", "Coverage fell"} {
		if !strings.Contains(out, want) {
			t.Errorf("drift output missing %q:\n%s", want, out)
		}
	}
}

func TestSinceOutsideGitIsRefused(t *testing.T) {
	r := repo(t, map[string]string{"RULES.md": "1.1 Secrets must never enter git.\n"})
	if code, _, _ := run(t, "--no-banner", "--since", "v1", r); code != 2 {
		t.Fatalf("exit = %d, want 2 outside a git repository", code)
	}
}

// A copy hullcheck declines to make is a refusal like any other: exit 2, and a
// message that says what it measured and which flag lifts it.
func TestVerifyRefusesATreeOverTheCopyLimit(t *testing.T) {
	code, _, errOut := run(t, "--no-banner", "--max-copy", "1", "--verify", verifiable(t))
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	for _, want := range []string{"REFUSED", "--max-copy", "Nothing was copied"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal must mention %q:\n%s", want, errOut)
		}
	}
}

func TestScratchPlanMeasuresAndCopiesNothing(t *testing.T) {
	code, out, _ := run(t, "--no-banner", "--scratch-plan", gated(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"scratch plan", "files", "bytes"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan must report %q:\n%s", want, out)
		}
	}
}

func TestBadCopyFlagsAreRefusedNotIgnored(t *testing.T) {
	for _, args := range [][]string{
		{"--no-banner", "--max-copy", "enormous", "."},
		{"--no-banner", "--max-files", "-3", "."},
		{"--no-banner", "--scratch-dir", "/no/such/place", "."},
	} {
		if code, _, _ := run(t, args...); code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
	}
}

// verifiable is a repository --verify will act on: a rule, and a manifest that
// declares a gate with a fixture for it.
func verifiable(t *testing.T) string {
	return repo(t, map[string]string{
		"RULES.md": "1.1 Every asset must record its provenance.\n",
		".hullcheck.yml": "version: 1\nrules:\n  - id: RULES-1.1\n" +
			"    statement: \"x\"\n    gate:\n      kind: command\n" +
			"      run: \"true\"\n      fixture_path: bad.txt\n",
	})
}

// Zero is the documented way to remove a limit. An int flag defaulting to zero
// would swallow it and leave the default silently in force.
func TestZeroRemovesTheCopyLimits(t *testing.T) {
	root := verifiable(t)
	for _, args := range [][]string{
		{"--no-banner", "--max-copy", "0", "--verify", root},
		{"--no-banner", "--max-files", "0", "--verify", root},
	} {
		if code, _, e := run(t, args...); code != 0 {
			t.Errorf("%v: exit = %d, want 0\n%s", args, code, e)
		}
	}
	// ...and a one-file limit still refuses, so the flag is really being read.
	code, _, errOut := run(t, "--no-banner", "--max-files", "1", "--verify", root)
	if code != 2 || !strings.Contains(errOut, "REFUSED") {
		t.Errorf("--max-files 1 should refuse: exit = %d\n%s", code, errOut)
	}
}

// Issue #2, end to end: two policy documents with the same basename state two
// different rules with the same id. A manifest entry naming one document must
// not flip the other.
func TestDeclarationDoesNotReachIntoAnotherDocument(t *testing.T) {
	root := repo(t, map[string]string{
		// Both documents mention provenance, so the matcher credits the same
		// gate to both and both read HOLD before verification.
		"RULES.md":              "5.3 Every asset must record its provenance.\n",
		"second-brand/RULES.md": "5.3 Provenance must never be dropped on release.\n",
		"check_provenance.sh":   "exit 0\n",
		// The declaration names one document and declares no gate, so it flips
		// exactly one of them to BREACH.
		".hullcheck.yml": "version: 1\nrules:\n  - id: RULES-5.3\n" +
			"    source: RULES.md\n    statement: \"probe\"\n",
	})
	code, out, _ := run(t, "--no-banner", "--json", "--verify", root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	var rep struct {
		Findings []struct {
			Rule struct {
				ID   string `json:"id"`
				File string `json:"file"`
			} `json:"rule"`
			Verdict string `json:"verdict"`
			Why     string `json:"why"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(rep.Findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(rep.Findings), rep.Findings)
	}
	for _, f := range rep.Findings {
		switch f.Rule.File {
		case "RULES.md":
			if f.Verdict != "BREACH" {
				t.Errorf("the declared rule should carry the declaration's verdict, got %s", f.Verdict)
			}
		case "second-brand/RULES.md":
			if f.Verdict != "HOLD" {
				t.Errorf("a rule in another document was flipped by a declaration "+
					"that never mentioned it: %+v", f)
			}
		default:
			t.Errorf("unexpected document %q", f.Rule.File)
		}
	}
}

// An unqualified declaration where the id is ambiguous changes nothing, and says
// so on stderr rather than picking one.
func TestAmbiguousDeclarationIsReportedNotGuessed(t *testing.T) {
	root := repo(t, map[string]string{
		"RULES.md":       "5.3 Every asset must record its provenance.\n",
		"other/RULES.md": "5.3 Releases must never ship without a changelog.\n",
		".hullcheck.yml": "version: 1\nrules:\n  - id: RULES-5.3\n" +
			"    statement: \"probe\"\n    gate:\n      kind: command\n      run: \"true\"\n",
	})
	code, _, errOut := run(t, "--no-banner", "--verify", root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "RULES-5.3") || !strings.Contains(errOut, "source:") {
		t.Errorf("the ambiguity must be reported:\n%s", errOut)
	}
}

// Issue #3: a manifest rule the scanner never produced was proven, logged, and
// then absent from the report and from the coverage number. In verified mode the
// manifest is authoritative, so it lands in both.
func TestVerifyCountsARuleDiscoveryDidNotFind(t *testing.T) {
	root := repo(t, map[string]string{
		"RULES.md":            "1.1 Every asset must record its provenance.\n",
		"check_provenance.sh": "exit 0\n",
		// RULES-1.6 is nowhere in RULES.md: the clause it refers to is one the
		// scanner skips. Declaring it must still count.
		".hullcheck.yml": "version: 1\nrules:\n" +
			"  - id: RULES-1.6\n    source: RULES.md\n" +
			"    statement: \"hullcheck must never write to the repository it reads\"\n" +
			"    severity: high\n",
	})
	code, out, errOut := run(t, "--no-banner", "--json", "--verify", root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, errOut)
	}
	var rep struct {
		Findings []struct {
			Rule struct {
				ID string `json:"id"`
			} `json:"rule"`
			Verdict string `json:"verdict"`
		} `json:"findings"`
		Docs []string `json:"policy_documents"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var found bool
	for _, f := range rep.Findings {
		if f.Rule.ID == "RULES-1.6" {
			found = true
			if f.Verdict != "BREACH" {
				t.Errorf("verdict = %q, want the declaration's BREACH (it declares no gate)", f.Verdict)
			}
		}
	}
	if !found {
		t.Errorf("a declared rule was proven and then dropped from the reading:\n%s", out)
	}
	if len(rep.Findings) != 2 {
		t.Errorf("got %d findings, want the discovered rule and the declared one", len(rep.Findings))
	}
	if !strings.Contains(errOut, "discovery did not find") {
		t.Errorf("a changed denominator must be announced:\n%s", errOut)
	}
	var sawManifest bool
	for _, d := range rep.Docs {
		if d == ".hullcheck.yml" {
			sawManifest = true
		}
	}
	if !sawManifest {
		t.Errorf("the manifest supplied a rule and must be listed as a source: %v", rep.Docs)
	}
}
