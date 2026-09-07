package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/space-pirate-zero/hullcheck/internal/model"
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

// Issue #4: a rule written as a declarative headline with the obligation in the
// block beneath it was invisible, and the whole subsection went with it.
func TestClauseIsJudgedByItsWholeBlock(t *testing.T) {
	root := t.TempDir()
	doc := "# Section 8\n\n" +
		"8.8 **Skills are living operator docs — every render updates them.**\n\n" +
		"  - The owning skill must be updated in the same PR as the render.\n\n" +
		"8.0a **Two independent studios, one repo.**\n\n" +
		"  Each studio owns its pipeline and must never reach into the other's tree.\n\n" +
		"8.7 Overview of the section that follows.\n"
	if err := os.WriteFile(filepath.Join(root, "RULES.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, _, skipped, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]model.Rule{}
	for _, r := range rs {
		got[r.ID] = r
	}
	for _, id := range []string{"RULES-8.8", "RULES-8.0a"} {
		if _, ok := got[id]; !ok {
			t.Errorf("%s has its obligation beneath its headline and must be counted; got %v", id, keys(rs))
		}
	}
	// Severity comes from the block when the headline carries no modal.
	if s := got["RULES-8.0a"].Severity; s != model.High {
		t.Errorf("severity = %q, want high from the block's \"must never\"", s)
	}
	// A clause with no obligation anywhere is still declined, and said so.
	if _, ok := got["RULES-8.7"]; ok {
		t.Error("a clause with no obligation in it or beneath it must not be counted")
	}
	if len(skipped) != 1 || skipped[0].ID != "RULES-8.7" {
		t.Fatalf("the skip must be reported, got %+v", skipped)
	}
	if skipped[0].Why == "" || skipped[0].Line == 0 {
		t.Errorf("a skip must carry a reason and a line: %+v", skipped[0])
	}
}

// A document that names a gate for a clause has already said it is a rule. That
// is the repository's own word, and stronger than any modal.
func TestAGateLineAloneMakesAClauseARule(t *testing.T) {
	root := t.TempDir()
	doc := "9.1 The palette is void, pink and cyan.\n\n*Gate: make brand*\n"
	if err := os.WriteFile(filepath.Join(root, "RULES.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, _, _, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].ID != "RULES-9.1" {
		t.Fatalf("a clause naming its gate must be counted, got %v", keys(rs))
	}
	if rs[0].GateHint != "make brand" {
		t.Errorf("gate hint = %q, want it bound to the clause that owns the block", rs[0].GateHint)
	}
}

// The block stops at the next clause, so one clause's obligation cannot make its
// unrelated neighbour above it look normative.
func TestABlockStopsAtTheNextClause(t *testing.T) {
	root := t.TempDir()
	doc := "3.1 A summary line with no obligation at all here.\n\n" +
		"3.2 Every asset must record its provenance.\n"
	if err := os.WriteFile(filepath.Join(root, "RULES.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, _, skipped, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].ID != "RULES-3.2" {
		t.Fatalf("3.1 must not borrow 3.2's modal, got %v", keys(rs))
	}
	if len(skipped) != 1 || skipped[0].ID != "RULES-3.1" {
		t.Fatalf("skipped = %+v", skipped)
	}
}

// A fenced sample showing what NOT to do is full of modals and states nothing.
func TestAFencedBlockGrantsNoObligation(t *testing.T) {
	root := t.TempDir()
	doc := "4.1 An example of the wrong way to write this.\n\n" +
		"```\nyou must never do this\n```\n"
	if err := os.WriteFile(filepath.Join(root, "RULES.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, _, _, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 {
		t.Errorf("a fenced sample must not make a clause a rule, got %v", keys(rs))
	}
}

func keys(rs []model.Rule) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// A Gate: line is the repository's own word that a clause is a rule, so it wins
// over the length guard, which exists to filter clauses nobody has vouched for.
func TestAGateLineBeatsTheLengthGuard(t *testing.T) {
	root := t.TempDir()
	doc := "3.1 Palette.\n\n*Gate: make brand*\n\n3.2 Summary.\n"
	if err := os.WriteFile(filepath.Join(root, "RULES.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, _, skipped, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].ID != "RULES-3.1" {
		t.Fatalf("a short clause that names its gate must be counted, got %v", keys(rs))
	}
	if !rs[0].Clause {
		t.Error("a numbered clause must be marked as one, so the counts can be audited")
	}
	if len(skipped) != 1 || skipped[0].ID != "RULES-3.2" {
		t.Fatalf("a short clause with no gate is still declined, got %+v", skipped)
	}
}
