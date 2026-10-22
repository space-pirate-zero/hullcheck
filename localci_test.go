package hullcheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The CI is local: .githooks/pre-push runs every gate in `make check`. A hook
// that runs a different list from check: is a gate suite that passes on push
// and fails on the bench, or the reverse, and nobody notices which.
func TestPrePushRunsExactlyTheCheckSuite(t *testing.T) {
	mk, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, l := range strings.Split(string(mk), "\n") {
		if rest, ok := strings.CutPrefix(l, "check:"); ok {
			want = strings.Fields(rest)
		}
	}
	if len(want) == 0 {
		t.Fatal("Makefile has no check: target")
	}

	hook, err := os.ReadFile(filepath.Join(".githooks", "pre-push"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range strings.Split(string(hook), "\n") {
		if tgt, ok := strings.CutPrefix(strings.TrimSpace(l), "make "); ok {
			got = append(got, strings.TrimSpace(tgt))
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("pre-push runs %v, make check runs %v; they must be the same list in the same order", got, want)
	}

	fi, err := os.Stat(filepath.Join(".githooks", "pre-push"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Error(".githooks/pre-push is not executable, so git will not run it")
	}
}

// No hosted CI. The gates run on the machine that pushes, and a workflow file
// would quietly move the verdict somewhere else.
func TestNoHostedWorkflows(t *testing.T) {
	for _, pat := range []string{".github/workflows/*", ".gitlab-ci.yml", ".gitlab-ci.yaml"} {
		if m, _ := filepath.Glob(pat); len(m) > 0 {
			t.Errorf("hosted CI found: %v; CI here is local (make hooks)", m)
		}
	}
}

// The hook judges the commits being pushed, not the checkout. A push of a commit
// that breaks a gate must fail even when the working tree, uncommitted, is fine.
func TestPrePushChecksThePushedCommitNotTheCheckout(t *testing.T) {
	for _, tool := range []string{"git", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not available")
		}
	}
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "runs")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e", "OUT="+out)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	write := func(name, body string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	helper, err := os.ReadFile(filepath.Join(".githooks", "pushed.sh"))
	if err != nil {
		t.Fatal(err)
	}

	git("init", "-q")
	write(".githooks/pushed.sh", string(helper), 0o644)
	write(".githooks/pre-push", "#!/bin/sh\nset -e\n. \"$(dirname \"$0\")/pushed.sh\"\n"+
		"echo run >> \"$OUT\"\ntest \"$(cat marker)\" = good\n", 0o755)
	write("marker", "good\n", 0o644)
	git("add", "-A")
	git("commit", "-qm", "good")
	good := git("rev-parse", "HEAD")
	write("marker", "bad\n", 0o644)
	git("commit", "-qam", "bad")
	bad := git("rev-parse", "HEAD")
	write("marker", "good\n", 0o644) // fixed in the checkout, not committed

	push := func(stdin string) error {
		t.Helper()
		_ = os.Remove(out)
		cmd := exec.Command(filepath.Join(root, ".githooks", "pre-push"), "origin", "x")
		cmd.Dir, cmd.Env, cmd.Stdin = root, env, strings.NewReader(stdin)
		return cmd.Run()
	}
	runs := func() int {
		b, _ := os.ReadFile(out)
		return strings.Count(string(b), "run")
	}
	line := func(sha string) string {
		return "refs/heads/main " + sha + " refs/heads/main " + strings.Repeat("0", 40) + "\n"
	}

	if err := push(line(bad)); err == nil {
		t.Error("pushing the bad commit passed: the hook judged the checkout, not the commit")
	}
	if err := push(line(good)); err != nil {
		t.Errorf("pushing the good commit failed: %v", err)
	}
	if err := push(line(good) + line(bad)); err == nil || runs() != 2 {
		t.Errorf("two commits pushed: err=%v runs=%d, want a failure after checking both", err, runs())
	}
	if err := push("(delete) " + strings.Repeat("0", 40) + " refs/heads/old " + good + "\n"); err != nil || runs() != 0 {
		t.Errorf("a deletion pushes nothing to check: err=%v runs=%d", err, runs())
	}
	if wt := git("worktree", "list"); strings.Count(wt, "\n") != 0 {
		t.Errorf("scratch worktrees were left behind:\n%s", wt)
	}
}
