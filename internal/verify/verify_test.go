package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spaceship-alpha-9/hullcheck/internal/manifest"
	"github.com/spaceship-alpha-9/hullcheck/internal/model"
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
	out := Apply(rep, []Result{{RuleID: "R-1", Verdict: model.Fake, Why: "paint"}})
	if out.Findings[0].Verdict != model.Fake || !out.Verified {
		t.Fatalf("apply did not fold the proof in: %+v", out)
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
