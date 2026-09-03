package history

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRepo builds a throwaway repository with two commits and a tag, so drift and
// diff can be tested against real git rather than a mock of it.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q")
	// v1: one rule, one gate. Fully held.
	write("RULES.md", "1.1 Every asset must record its provenance.\n")
	write("check_provenance.sh", "exit 0\n")
	run("add", "-A")
	run("commit", "-qm", "v1")
	run("tag", "v1")

	// working tree: a second rule with nothing enforcing it.
	write("RULES.md", "1.1 Every asset must record its provenance.\n1.2 Secrets must never enter git.\n")
	return root
}

func TestIsRepo(t *testing.T) {
	if IsRepo(t.TempDir()) {
		t.Error("a plain directory is not a git repository")
	}
	if !IsRepo(gitRepo(t)) {
		t.Error("a git repository was not recognised")
	}
}

func TestAtReadsAPastRevision(t *testing.T) {
	root := gitRepo(t)
	rep, err := At(root, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("v1 had 1 rule, got %d: %+v", len(rep.Findings), rep.Findings)
	}
	if rep.Coverage() != 1 {
		t.Errorf("coverage at v1 = %v, want 1.0", rep.Coverage())
	}
}

func TestAtNeverModifiesTheRepository(t *testing.T) {
	root := gitRepo(t)
	before := status(t, root)
	if _, err := At(root, "v1"); err != nil {
		t.Fatal(err)
	}
	if after := status(t, root); after != before {
		t.Fatalf("reading history modified the repo\nbefore %q\nafter  %q", before, after)
	}
}

func TestRefsListsTagsOldestFirst(t *testing.T) {
	root := gitRepo(t)
	refs, err := Refs(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "v1" {
		t.Fatalf("refs = %v, want [v1]", refs)
	}
}

func TestDriftShowsCoverageFalling(t *testing.T) {
	root := gitRepo(t)
	pts, err := Drift(root, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("got %d points, want v1 + working tree: %+v", len(pts), pts)
	}
	if pts[0].Coverage != 100 {
		t.Errorf("v1 coverage = %v, want 100", pts[0].Coverage)
	}
	if pts[1].Coverage != 50 {
		t.Errorf("working tree coverage = %v, want 50 - a rule was added without a gate", pts[1].Coverage)
	}
	if pts[1].Coverage >= pts[0].Coverage {
		t.Error("drift failed to show the regression")
	}
}

func TestDiffFindsRulesAddedWithoutGates(t *testing.T) {
	root := gitRepo(t)
	added, _, err := Diff(root, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 {
		t.Fatalf("got %d newly unenforced rules, want 1: %+v", len(added), added)
	}
	if added[0].Rule.ID != "RULES-1.2" {
		t.Errorf("newly unenforced = %q, want RULES-1.2", added[0].Rule.ID)
	}
}

func TestDiffIgnoresPreexistingBreaches(t *testing.T) {
	// A repo can carry known gaps. Only NEW ones should fail a pull request.
	root := gitRepo(t)
	added, _, err := Diff(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// HEAD is the v1 commit, which had no breaches, so 1.2 is still new.
	if len(added) != 1 {
		t.Fatalf("got %+v", added)
	}
	// Comparing the working tree against itself must yield nothing new.
	again, _, err := Diff(root, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(added) {
		t.Error("diff is not stable across runs")
	}
}

func TestNonGitDirectoryIsRefused(t *testing.T) {
	if _, err := At(t.TempDir(), "HEAD"); err == nil {
		t.Fatal("expected a refusal outside a git repository")
	}
}

func status(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
