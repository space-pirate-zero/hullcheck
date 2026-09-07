package refusal

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
	for n, b := range files {
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(b), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func one(t *testing.T, m *manifest.File, root string) Result {
	t.Helper()
	rs, err := Audit(m, model.Report{}, Options{Root: root, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(rs), rs)
	}
	return rs[0]
}

// A tool that works normally and stops under the condition has proven its refusal.
func TestGenuineRefusalIsProven(t *testing.T) {
	root := repo(t, map[string]string{
		// Refuses when a .locked file is present, works otherwise.
		"tool.sh": "#!/bin/sh\nif [ -f .locked ]; then echo 'REFUSING: locked' >&2; exit 2; fi\nexit 0\n",
	})
	m := &manifest.File{Refusals: []manifest.Refusal{{
		Tool: "tool", Run: "sh tool.sh", When: "the tree is locked",
		FixturePath: ".locked", ExpectExit: 2, ExpectOutput: "REFUSING",
	}}}
	got := one(t, m, root)
	if got.Verdict != Refuses {
		t.Fatalf("verdict = %q, want REFUSES: %s", got.Verdict, got.Why)
	}
}

// The finding that matters: a tool that claims to stop and does not.
func TestToolThatRunsAnywayIsProceeds(t *testing.T) {
	root := repo(t, map[string]string{
		"tool.sh": "#!/bin/sh\nexit 0\n", // ignores the condition entirely
	})
	m := &manifest.File{Refusals: []manifest.Refusal{{
		Tool: "tool", Run: "sh tool.sh", When: "the tree is locked",
		FixturePath: ".locked", ExpectExit: 2,
	}}}
	got := one(t, m, root)
	if got.Verdict != Proceeds {
		t.Fatalf("verdict = %q, want PROCEEDS: %s", got.Verdict, got.Why)
	}
	if !strings.Contains(got.Why, "silent downgrade") {
		t.Errorf("the why must name the failure: %q", got.Why)
	}
}

// Without a control run, a tool that is simply broken looks like one that refuses.
func TestAlreadyBrokenToolIsNotCreditedWithRefusing(t *testing.T) {
	root := repo(t, map[string]string{"tool.sh": "#!/bin/sh\nexit 2\n"})
	m := &manifest.File{Refusals: []manifest.Refusal{{
		Tool: "tool", Run: "sh tool.sh", FixturePath: ".locked", ExpectExit: 2,
	}}}
	got := one(t, m, root)
	if got.Verdict != Broken {
		t.Fatalf("verdict = %q, want BROKEN - it fails with or without the condition", got.Verdict)
	}
}

func TestCrashWithTheWrongCodeIsNotARefusal(t *testing.T) {
	root := repo(t, map[string]string{
		"tool.sh": "#!/bin/sh\nif [ -f .locked ]; then exit 1; fi\nexit 0\n",
	})
	m := &manifest.File{Refusals: []manifest.Refusal{{
		Tool: "tool", Run: "sh tool.sh", FixturePath: ".locked", ExpectExit: 2,
	}}}
	got := one(t, m, root)
	if got.Verdict != Broken {
		t.Fatalf("verdict = %q, want BROKEN", got.Verdict)
	}
	if !strings.Contains(got.Why, "considered refusal") {
		t.Errorf("why = %q", got.Why)
	}
}

func TestRightCodeButSilentIsNotARefusal(t *testing.T) {
	root := repo(t, map[string]string{
		"tool.sh": "#!/bin/sh\nif [ -f .locked ]; then exit 2; fi\nexit 0\n",
	})
	m := &manifest.File{Refusals: []manifest.Refusal{{
		Tool: "tool", Run: "sh tool.sh", FixturePath: ".locked",
		ExpectExit: 2, ExpectOutput: "REFUSING",
	}}}
	if got := one(t, m, root); got.Verdict != Broken {
		t.Fatalf("verdict = %q, want BROKEN - it never said why it stopped", got.Verdict)
	}
}

func TestEmptyDirConditionIsSupported(t *testing.T) {
	root := repo(t, map[string]string{
		"tool.sh":  "#!/bin/sh\nif [ ! -f data.txt ]; then echo 'UNKNOWN' >&2; exit 2; fi\nexit 0\n",
		"data.txt": "x\n",
	})
	m := &manifest.File{Refusals: []manifest.Refusal{{
		Tool: "tool", Run: "sh " + filepath.Join(root, "tool.sh"),
		When: "given nothing to work with", EmptyDir: true,
		ExpectExit: 2, ExpectOutput: "UNKNOWN",
	}}}
	if got := one(t, m, root); got.Verdict != Refuses {
		t.Fatalf("verdict = %q, want REFUSES: %s", got.Verdict, got.Why)
	}
}

func TestProceedsSortsFirst(t *testing.T) {
	rs := []Result{{Verdict: Refuses}, {Verdict: Broken}, {Verdict: Proceeds}}
	sortForTest(rs)
	if rs[0].Verdict != Proceeds {
		t.Fatalf("worst finding must lead, got %q", rs[0].Verdict)
	}
}

func sortForTest(rs []Result) {
	for i := range rs {
		for j := i + 1; j < len(rs); j++ {
			if rank(rs[j].Verdict) < rank(rs[i].Verdict) {
				rs[i], rs[j] = rs[j], rs[i]
			}
		}
	}
}

// Many refusal conditions are an absence - no policy, no manifest, no key - and
// an additive fixture cannot create one.
func TestRemovalCreatesAnAbsenceCondition(t *testing.T) {
	root := repo(t, map[string]string{
		"needed.txt": "x\n",
		"tool.sh":    "#!/bin/sh\nif [ ! -f needed.txt ]; then echo 'UNKNOWN: nothing to read' >&2; exit 2; fi\nexit 0\n",
	})
	m := &manifest.File{Refusals: []manifest.Refusal{{
		Tool: "tool", Run: "sh tool.sh", When: "the input is absent",
		Remove: "needed.txt", ExpectExit: 2, ExpectOutput: "UNKNOWN",
	}}}
	if got := one(t, m, root); got.Verdict != Refuses {
		t.Fatalf("verdict = %q, want REFUSES: %s", got.Verdict, got.Why)
	}
}

func TestRemovalCannotEscapeTheCopy(t *testing.T) {
	root := repo(t, map[string]string{"tool.sh": "#!/bin/sh\nexit 0\n"})
	m := &manifest.File{Refusals: []manifest.Refusal{{
		Tool: "tool", Run: "sh tool.sh", Remove: "../../etc/hosts", ExpectExit: 2,
	}}}
	if _, err := Audit(m, model.Report{}, Options{Root: root, Timeout: 10 * time.Second}); err == nil {
		t.Fatal("a removal escaping the copy must be refused")
	}
}

// Issue #6, the sharp case: the scratch copy has no .git, so a refusal whose
// condition is a git fact cannot fire. The tool runs happily, and calling that
// PROCEEDS would send someone to fix a refusal that is correct and load-bearing.
func TestAGitConditionThatCannotExistIsUnprovableNotProceeds(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tool.sh"),
		[]byte("#!/bin/sh\n[ -f .git ] && { echo refusing: worktree; exit 1; }\necho ok\n"),
		0o700); err != nil {
		t.Fatal(err)
	}
	res, err := Audit(&manifest.File{Refusals: []manifest.Refusal{{
		Tool: "indexer", When: "the checkout is a git worktree",
		Run: "sh tool.sh", ExpectExit: 1, ExpectOutput: "refusing",
	}}}, model.Report{}, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].Verdict == Proceeds {
		t.Fatal("a condition that could not be created must never be reported as PROCEEDS")
	}
	if res[0].Verdict != Unprovable {
		t.Fatalf("verdict = %q, want UNPROVABLE", res[0].Verdict)
	}
	if !strings.Contains(res[0].Why, "needs_git") {
		t.Errorf("the verdict must name the fix: %q", res[0].Why)
	}
}

// A refusal that does fire is still proven, git-shaped or not: the downgrade
// applies only to the outcomes the missing .git could have caused.
func TestAGitConditionThatDoesFireIsStillProven(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tool.sh"),
		[]byte("#!/bin/sh\n[ -f STOP ] && { echo refusing: git worktree; exit 1; }\necho ok\n"),
		0o700); err != nil {
		t.Fatal(err)
	}
	res, err := Audit(&manifest.File{Refusals: []manifest.Refusal{{
		Tool: "indexer", When: "the checkout is a git worktree", Run: "sh tool.sh",
		FixturePath: "STOP", FixtureBody: "x", ExpectExit: 1, ExpectOutput: "refusing",
	}}}, model.Report{}, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Verdict != Refuses {
		t.Fatalf("verdict = %q, want REFUSES", res[0].Verdict)
	}
}

// With needs_git the copy carries .git, so the condition can be built and the
// audit reaches a real verdict.
func TestNeedsGitCarriesTheRepositoryIntoTheCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tool.sh"),
		[]byte("#!/bin/sh\n[ -f .git ] && { echo refusing: worktree; exit 1; }\n"+
			"[ -d .git ] || { echo no repository; exit 3; }\necho ok\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-qm", "seed"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	res, err := Audit(&manifest.File{Refusals: []manifest.Refusal{{
		Tool: "indexer", When: "the checkout is a git worktree", Run: "sh tool.sh",
		NeedsGit: true, Remove: ".git", FixturePath: ".git",
		FixtureBody: "gitdir: /elsewhere\n", ExpectExit: 1, ExpectOutput: "refusing",
	}}}, model.Report{}, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Verdict != Refuses {
		t.Fatalf("verdict = %q (%s), want REFUSES: the control needs a real .git and the "+
			"treatment needs it replaced by a file", res[0].Verdict, res[0].Why)
	}
}

// needs_git cannot conjure a repository that is not there. Whether the condition
// could exist is a fact about the copy, not about the flag.
func TestNeedsGitInADirectoryThatIsNotARepositoryIsStillUnprovable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tool.sh"),
		[]byte("#!/bin/sh\n[ -f .git ] && { echo refusing; exit 1; }\necho ok\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := Audit(&manifest.File{Refusals: []manifest.Refusal{{
		Tool: "indexer", When: "the checkout is a git worktree", Run: "sh tool.sh",
		NeedsGit: true, ExpectExit: 1, ExpectOutput: "refusing",
	}}}, model.Report{}, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Verdict != Unprovable {
		t.Fatalf("verdict = %q, want UNPROVABLE - needs_git is set but there is no repository",
			res[0].Verdict)
	}
	if !strings.Contains(res[0].Why, "not a git work tree") {
		t.Errorf("the verdict must say why needs_git did not help: %q", res[0].Why)
	}
}
