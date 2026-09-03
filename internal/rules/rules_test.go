package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
)

// fixture writes a throwaway repo and returns its root.
func fixture(t *testing.T, files map[string]string) string {
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

func byID(rs []model.Rule, id string) (model.Rule, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return model.Rule{}, false
}

func TestFindsNumberedClauses(t *testing.T) {
	root := fixture(t, map[string]string{
		"RULES.md": "# Rules\n\n" +
			"7.8 **Every asset records its provenance.** No exceptions.\n\n" +
			"3.1 Palette is exactly: void, pink, cyan. No other hues.\n",
	})
	rs, docs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0] != "RULES.md" {
		t.Fatalf("policy docs = %v, want [RULES.md]", docs)
	}
	r, ok := byID(rs, "RULES-7.8")
	if !ok {
		t.Fatalf("RULES-7.8 not found in %+v", rs)
	}
	if r.Severity != model.High {
		t.Errorf("severity = %q, want high (says 'no exceptions')", r.Severity)
	}
	if r.Line != 3 {
		t.Errorf("line = %d, want 3", r.Line)
	}
	// A numbered clause with no modal but normative phrasing still counts.
	if _, ok := byID(rs, "RULES-3.1"); !ok {
		t.Errorf("normative clause 3.1 was missed: %+v", rs)
	}
}

func TestAttachesGateAnnotation(t *testing.T) {
	root := fixture(t, map[string]string{
		"RULES.md": "8.2 Secrets must never enter git.\n\n*Gate: make preflight*\n",
	})
	rs, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := byID(rs, "RULES-8.2")
	if !ok {
		t.Fatalf("rule not found: %+v", rs)
	}
	if r.GateHint != "make preflight" {
		t.Errorf("gate hint = %q, want %q", r.GateHint, "make preflight")
	}
}

func TestIgnoresFencedCode(t *testing.T) {
	root := fixture(t, map[string]string{
		"CONTRIBUTING.md": "# Contributing\n\n```sh\n# you must never run this\nrm -rf /\n```\n\n" +
			"- You must sign your commits.\n",
	})
	rs, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d rules, want 1 (code fence must be ignored): %+v", len(rs), rs)
	}
	if got := rs[0].Statement; got != "You must sign your commits." {
		t.Errorf("statement = %q", got)
	}
}

func TestSkipsProseWithoutObligation(t *testing.T) {
	root := fixture(t, map[string]string{
		"CLAUDE.md": "# Notes\n\n- This project uses Go and ships a single binary.\n" +
			"- The build is reproducible on Linux and macOS.\n",
	})
	rs, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 {
		t.Fatalf("descriptive prose was misread as rules: %+v", rs)
	}
}

func TestWordBoundaryAvoidsFalsePositives(t *testing.T) {
	// "mustard" and "northern" must not trip the "must"/"no" modals.
	root := fixture(t, map[string]string{
		"POLICY.md": "- The mustard supply in the northern office is restocked weekly.\n",
	})
	rs, _, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 {
		t.Fatalf("substring match produced false positives: %+v", rs)
	}
}

func TestReadsADRDirectories(t *testing.T) {
	root := fixture(t, map[string]string{
		"docs/adr/0014-tenancy.md": "# ADR 14\n\n- Services must never read another tenant's data.\n",
		"docs/notes/random.md":     "- You must ignore this file, it is not policy.\n",
	})
	rs, docs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %v, want only the ADR", docs)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d rules, want 1: %+v", len(rs), rs)
	}
}

func TestSkipsVendoredAndGitDirectories(t *testing.T) {
	root := fixture(t, map[string]string{
		"node_modules/pkg/RULES.md": "- You must not read vendored policy.\n",
		"vendor/x/CONTRIBUTING.md":  "- You must not read vendored policy.\n",
		"RULES.md":                  "- You must read this one.\n",
	})
	rs, docs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0] != "RULES.md" {
		t.Fatalf("docs = %v, want only RULES.md", docs)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d rules, want 1: %+v", len(rs), rs)
	}
}

func TestEmptyRepoYieldsNothingNotAnError(t *testing.T) {
	rs, docs, err := Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 || len(docs) != 0 {
		t.Fatalf("expected an empty reading, got %d rules / %d docs", len(rs), len(docs))
	}
}
