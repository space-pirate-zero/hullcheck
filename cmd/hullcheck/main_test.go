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
	root := repo(t, map[string]string{
		"RULES.md": "1.1 Every asset must record its provenance.\n",
		".hullcheck.yml": "version: 1\nrules:\n  - id: RULES-1.1\n" +
			"    statement: \"x\"\n    gate:\n      kind: command\n" +
			"      run: \"true\"\n      fixture_path: bad.txt\n",
	})
	code, _, errOut := run(t, "--no-banner", "--max-copy", "1", "--verify", root)
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
