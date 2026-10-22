package hullcheck_test

import (
	"os"
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
