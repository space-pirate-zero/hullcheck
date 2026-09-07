package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/space-pirate-zero/hullcheck/internal/manifest"
	"github.com/space-pirate-zero/hullcheck/internal/model"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func only(t *testing.T, res []Result) Result {
	t.Helper()
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(res), res)
	}
	return res[0]
}

// A gate that really catches the violation is PROVEN.
func TestRealGateIsProven(t *testing.T) {
	root := repo(t, map[string]string{
		// Fails if any .secret file exists — a gate that actually works.
		"check.sh": "#!/bin/sh\nif ls *.secret >/dev/null 2>&1; then exit 1; fi\nexit 0\n",
	})
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Statement: "No secrets in the tree.",
		Gate: &manifest.Gate{Kind: "command", Run: "sh check.sh",
			FixturePath: "leaked.secret", FixtureBody: "hunter2"},
	}}}
	got := only(t, mustRun(t, m, root))
	if got.Verdict != model.Hold {
		t.Fatalf("verdict = %q, want HOLD: %s", got.Verdict, got.Why)
	}
	if !strings.Contains(got.Why, "proven") {
		t.Errorf("a proven gate should say so: %q", got.Why)
	}
}

// A gate that passes anyway is FAKE. This is the verdict the whole tool exists for.
func TestGateThatPassesAnywayIsFake(t *testing.T) {
	root := repo(t, map[string]string{
		"check.sh": "#!/bin/sh\nexit 0\n", // always passes: a patch that is only paint
	})
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Statement: "No secrets in the tree.",
		Gate: &manifest.Gate{Kind: "command", Run: "sh check.sh",
			FixturePath: "leaked.secret", FixtureBody: "hunter2"},
	}}}
	got := only(t, mustRun(t, m, root))
	if got.Verdict != model.Fake {
		t.Fatalf("verdict = %q, want FAKE: %s", got.Verdict, got.Why)
	}
}

// A gate that cannot run at all exits non-zero and, without a control run, is
// indistinguishable from a gate that caught the violation. This is the regression
// test for that false PROVEN.
func TestGateThatCannotRunIsBrokenNotProven(t *testing.T) {
	root := repo(t, map[string]string{"present.txt": "x"})
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Statement: "x",
		Gate: &manifest.Gate{Run: "definitely-not-a-command-anywhere", FixturePath: "v.txt"},
	}}}
	got := only(t, mustRun(t, m, root))
	if got.Verdict != model.Broken {
		t.Fatalf("verdict = %q, want BROKEN: %s", got.Verdict, got.Why)
	}
	if !strings.Contains(got.Why, "discriminates nothing") {
		t.Errorf("BROKEN must explain itself: %q", got.Why)
	}
}

// A gate that is always red proves nothing either, and gets disabled by the third
// person who hits it.
func TestAlwaysFailingGateIsBroken(t *testing.T) {
	root := repo(t, map[string]string{"check.sh": "#!/bin/sh\nexit 1\n"})
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Gate: &manifest.Gate{Run: "sh check.sh", FixturePath: "v.txt"},
	}}}
	if got := only(t, mustRun(t, m, root)); got.Verdict != model.Broken {
		t.Fatalf("verdict = %q, want BROKEN", got.Verdict)
	}
}

func TestNoGateIsBreach(t *testing.T) {
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{ID: "R-1", Statement: "x"}}}
	if got := only(t, mustRun(t, m, t.TempDir())); got.Verdict != model.Breach {
		t.Fatalf("verdict = %q, want BREACH", got.Verdict)
	}
}

func TestGateWithoutFixtureIsHeldButNotProven(t *testing.T) {
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Statement: "x",
		Gate: &manifest.Gate{Kind: "command", Run: "true"},
	}}}
	got := only(t, mustRun(t, m, t.TempDir()))
	if got.Verdict != model.Hold {
		t.Fatalf("verdict = %q, want HOLD", got.Verdict)
	}
	if !strings.Contains(got.Why, "not proven") {
		t.Errorf("an unproven claim must not read as evidence: %q", got.Why)
	}
}

func TestVerifyNeverTouchesTheRealRepository(t *testing.T) {
	root := repo(t, map[string]string{"check.sh": "#!/bin/sh\nexit 1\n"})
	before := snapshot(t, root)
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Gate: &manifest.Gate{Run: "sh check.sh", FixturePath: "violation.txt"},
	}}}
	mustRun(t, m, root)
	if after := snapshot(t, root); after != before {
		t.Fatalf("verify modified the repository it was checking\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestFixturePathCannotEscapeTheRepo(t *testing.T) {
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Gate: &manifest.Gate{Run: "true", FixturePath: "../../etc/passwd"},
	}}}
	if _, err := Run(m, Options{Root: t.TempDir(), Timeout: 5 * time.Second}); err == nil {
		t.Fatal("a fixture path escaping the repo must be refused")
	}
}

// A gate that hangs never completes, so it cannot discriminate. The control run
// catches this on the clean tree, before the fixture is ever applied - and BROKEN
// is the honest verdict. It is emphatically not a pass.
func TestHangingGateIsBrokenNotPassing(t *testing.T) {
	root := repo(t, map[string]string{"hang.sh": "#!/bin/sh\nsleep 30\n"})
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Gate: &manifest.Gate{Run: "sh hang.sh", FixturePath: "x.txt"},
	}}}
	res, err := Run(m, Options{Root: root, Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	got := only(t, res)
	if got.Verdict == model.Hold {
		t.Fatal("a gate that times out must never be reported as holding")
	}
	if got.Verdict != model.Broken {
		t.Fatalf("verdict = %q, want BROKEN", got.Verdict)
	}
}

func TestApplyFoldsResultsIntoTheReading(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{
		{Rule: model.Rule{ID: "R-1"}, Verdict: model.Hold},
	}}
	out, warn := Apply(rep, []Result{{RuleID: "R-1", Verdict: model.Fake, Why: "paint"}})
	if out.Findings[0].Verdict != model.Fake || !out.Verified {
		t.Fatalf("apply did not fold the proof in: %+v", out)
	}
	if len(warn) != 0 {
		t.Errorf("an unambiguous declaration must not warn: %v", warn)
	}
}

// Issue #2: ids are derived from the clause number and the document's basename,
// so two RULES.md files state two different rules called RULES-5.3. A declaration
// naming one document must not touch the other.
func TestApplyAddressesARuleByDocumentAndID(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{
		{Rule: model.Rule{ID: "RULES-5.3", File: "RULES.md"}, Verdict: model.Breach},
		{Rule: model.Rule{ID: "RULES-5.3", File: "second-brand/RULES.md"}, Verdict: model.Breach},
	}}
	out, warn := Apply(rep, []Result{{
		RuleID: "RULES-5.3", Source: "RULES.md", Verdict: model.Hold, Why: "proven",
	}})
	if out.Findings[0].Verdict != model.Hold {
		t.Errorf("the declared rule was not updated: %+v", out.Findings[0])
	}
	if out.Findings[1].Verdict != model.Breach {
		t.Errorf("a rule in another document was flipped by a declaration that never mentioned it: %+v",
			out.Findings[1])
	}
	if len(warn) != 0 {
		t.Errorf("a qualified declaration must not warn: %v", warn)
	}
}

// A declaration with no source: still works where the id is unique.
func TestApplyHonoursAnUnqualifiedDeclarationWhenTheIDIsUnique(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{
		{Rule: model.Rule{ID: "RULES-1.1", File: "RULES.md"}, Verdict: model.Breach},
	}}
	out, warn := Apply(rep, []Result{{RuleID: "RULES-1.1", Verdict: model.Hold, Why: "proven"}})
	if out.Findings[0].Verdict != model.Hold {
		t.Errorf("an unambiguous id must still match: %+v", out.Findings[0])
	}
	if len(warn) != 0 {
		t.Errorf("unexpected warning: %v", warn)
	}
}

// ...and where it is not unique, it changes nothing and says so. Guessing which
// of two rules was meant is exactly the false link this tool exists to prevent.
func TestApplyRefusesToGuessBetweenDuplicateIDs(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{
		{Rule: model.Rule{ID: "RULES-5.3", File: "RULES.md"}, Verdict: model.Breach},
		{Rule: model.Rule{ID: "RULES-5.3", File: "other/RULES.md"}, Verdict: model.Breach},
	}}
	out, warn := Apply(rep, []Result{{RuleID: "RULES-5.3", Verdict: model.Hold, Why: "proven"}})
	for i, f := range out.Findings {
		if f.Verdict != model.Breach {
			t.Errorf("finding %d was changed on an ambiguous declaration: %+v", i, f)
		}
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "RULES-5.3") {
		t.Errorf("the ambiguity must be reported, got %v", warn)
	}
}

func mustRun(t *testing.T, m *manifest.File, root string) []Result {
	t.Helper()
	res, err := Run(m, Options{Root: root, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func snapshot(t *testing.T, root string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		sb.WriteString(rel + "|")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// Issue #3: a declared rule the scanner never produced was verified, logged, and
// then dropped from the report and from the score. The manifest is authoritative
// in verified mode, so it joins the reading instead.
func TestApplyAddsADeclaredRuleDiscoveryDidNotFind(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{
		{Rule: model.Rule{ID: "RULES-1.1", File: "RULES.md"}, Verdict: model.Breach},
	}}
	out, warn := Apply(rep, []Result{{
		RuleID: "RULES-1.6", Source: "RULES.md",
		Rule: model.Rule{ID: "RULES-1.6", File: "RULES.md", Source: "RULES.md",
			Statement: "hullcheck must never write to the repository it reads",
			Severity:  model.High},
		Verdict: model.Hold, Why: "proven",
	}})
	if len(out.Findings) != 2 {
		t.Fatalf("got %d findings, want the declared rule added: %+v", len(out.Findings), out.Findings)
	}
	got := out.Findings[1]
	if got.Rule.ID != "RULES-1.6" || got.Verdict != model.Hold {
		t.Errorf("the proven rule did not reach the reading: %+v", got)
	}
	if got.Rule.Severity != model.High {
		t.Errorf("severity = %q, want the declared high", got.Rule.Severity)
	}
	if !strings.Contains(got.Why, "the scanner did not find this rule") {
		t.Errorf("a manifest-only rule must say where it came from: %q", got.Why)
	}
	// A proven gate has to move the number, or there is no reason to write
	// fixtures at all.
	if out.Coverage() != 0.5 {
		t.Errorf("coverage = %v, want 0.5 across both rules", out.Coverage())
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "discovery did not find") {
		t.Errorf("the added rules must be announced, got %v", warn)
	}
	if len(out.Docs) != 1 || out.Docs[0] != manifest.Name {
		t.Errorf("the manifest must be listed among the documents, got %v", out.Docs)
	}
}

// The one thing it will not do is invent a rule when it cannot tell which of
// several a declaration meant.
func TestApplyAddsNothingForAnAmbiguousDeclaration(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{
		{Rule: model.Rule{ID: "RULES-5.3", File: "RULES.md"}, Verdict: model.Breach},
		{Rule: model.Rule{ID: "RULES-5.3", File: "other/RULES.md"}, Verdict: model.Breach},
	}}
	out, warn := Apply(rep, []Result{{
		RuleID:  "RULES-5.3",
		Rule:    model.Rule{ID: "RULES-5.3", Statement: "probe"},
		Verdict: model.Hold, Why: "proven",
	}})
	if len(out.Findings) != 2 {
		t.Errorf("an ambiguous declaration must not add a third rule: %+v", out.Findings)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "none was added") {
		t.Errorf("want an ambiguity warning, got %v", warn)
	}
}

// A HOLD with no gate falls into the "never" bucket, which the ladder does not
// print, so the rows would sum to fewer rules than the HOLD count. A rule added
// from the manifest carries the gate the manifest declared.
func TestAnAddedRuleCarriesTheDeclaredGate(t *testing.T) {
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-9", Source: "RULES.md", Statement: "x",
		Gate: &manifest.Gate{Kind: "makefile", Run: "make readonly"},
	}}}
	res, err := Run(m, Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Apply(model.Report{}, res)
	if len(out.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(out.Findings))
	}
	f := out.Findings[0]
	if f.Verdict != model.Hold {
		t.Fatalf("verdict = %q, want HOLD", f.Verdict)
	}
	if len(f.Gates) != 1 {
		t.Fatalf("an added HOLD must carry its declared gate, got %+v", f.Gates)
	}
	if f.Gates[0].Run != "make readonly" || f.Gates[0].Kind != model.KindMakefile {
		t.Errorf("the gate lost what the manifest declared: %+v", f.Gates[0])
	}
	// A manifest says what a gate does, never when it runs, so the stage must be
	// the one with no automatic detection.
	if f.Stage() != model.Manual {
		t.Errorf("stage = %q, want manual", f.Stage())
	}
}

// A rule declared with no gate at all is a BREACH, and carries no gate.
func TestAnAddedRuleWithNoGateCarriesNone(t *testing.T) {
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-9", Source: "RULES.md", Statement: "x",
	}}}
	res, err := Run(m, Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Apply(model.Report{}, res)
	if len(out.Findings) != 1 || out.Findings[0].Verdict != model.Breach {
		t.Fatalf("want one BREACH, got %+v", out.Findings)
	}
	if len(out.Findings[0].Gates) != 0 {
		t.Errorf("a rule with no declared gate must carry none: %+v", out.Findings[0].Gates)
	}
}

// A git-dependent gate in a copy with no .git has not been shown to be broken.
// BROKEN is a finding about the gate; this is a finding about the experiment.
func TestAGitGateWithoutNeedsGitIsUnprovableNotBroken(t *testing.T) {
	root := repo(t, map[string]string{"x.txt": "x"})
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Source: "RULES.md",
		Gate: &manifest.Gate{
			Run: "git rev-parse --is-inside-work-tree", FixturePath: "bad.txt",
		},
	}}}
	got := only(t, mustRun(t, m, root))
	if got.Verdict == model.Broken {
		t.Fatal("a gate hullcheck could not run must not be reported as one that decides nothing")
	}
	if got.Verdict != model.Unprovable {
		t.Fatalf("verdict = %q, want UNPROVABLE", got.Verdict)
	}
	if !strings.Contains(got.Why, "needs_git") {
		t.Errorf("the verdict must name the fix: %q", got.Why)
	}
}

// A gate that has nothing to do with git and fails its control is still BROKEN.
func TestANonGitGateThatFailsItsControlIsStillBroken(t *testing.T) {
	root := repo(t, map[string]string{"x.txt": "x"})
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Source: "RULES.md",
		Gate: &manifest.Gate{Run: "exit 7", FixturePath: "bad.txt"},
	}}}
	if got := only(t, mustRun(t, m, root)); got.Verdict != model.Broken {
		t.Fatalf("verdict = %q, want BROKEN", got.Verdict)
	}
}

// With needs_git the copy carries .git, so a git-reading gate can be proven.
func TestNeedsGitLetsAGitGateBeProven(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := repo(t, map[string]string{
		"ok.txt": "x",
		// Fails when an untracked file appears: a gate that genuinely reads git.
		"gate.sh": "#!/bin/sh\ntest -z \"$(git status --porcelain)\"\n",
	})
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-qm", "seed"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	m := &manifest.File{Version: 1, Rules: []manifest.Rule{{
		ID: "R-1", Source: "RULES.md",
		Gate: &manifest.Gate{
			Run: "sh gate.sh", NeedsGit: true,
			FixturePath: "stray.txt", FixtureBody: "untracked\n",
		},
	}}}
	got := only(t, mustRun(t, m, root))
	if got.Verdict != model.Hold {
		t.Fatalf("verdict = %q (%s), want HOLD: with .git present the gate can be proven",
			got.Verdict, got.Why)
	}
}
