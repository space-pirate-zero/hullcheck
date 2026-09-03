package hullcheck_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spaceship-alpha-9/hullcheck"
)

// recorder stands in for *testing.T so the assertion helpers can be tested.
type recorder struct{ errs []string }

func (r *recorder) Helper()                   {}
func (r *recorder) Errorf(f string, a ...any) { r.errs = append(r.errs, sprintf(f, a...)) }
func (r *recorder) Fatalf(f string, a ...any) { r.errs = append(r.errs, sprintf(f, a...)) }
func sprintf(f string, a ...any) string       { return strings.TrimSpace(fmt.Sprintf(f, a...)) }

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

func half(t *testing.T) string {
	return repo(t, map[string]string{
		"RULES.md":            "1.1 Every asset must record its provenance.\n1.2 Secrets must never enter git.\n",
		"check_provenance.sh": "exit 0\n",
	})
}

func TestReadExposesTheReading(t *testing.T) {
	rep, err := hullcheck.Read(half(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Coverage() != 50 {
		t.Errorf("coverage = %v, want 50", rep.Coverage())
	}
	if len(rep.Breaches()) != 1 {
		t.Errorf("breaches = %+v, want 1", rep.Breaches())
	}
	if len(rep.Docs) != 1 {
		t.Errorf("docs = %v", rep.Docs)
	}
}

func TestAssertCoverageFailsBelowThresholdAndNamesTheRules(t *testing.T) {
	var r recorder
	hullcheck.AssertCoverage(&r, half(t), 80)
	if len(r.errs) != 1 {
		t.Fatalf("expected one failure, got %v", r.errs)
	}
	if !strings.Contains(r.errs[0], "BREACH") || !strings.Contains(r.errs[0], "Secrets") {
		t.Errorf("a failure must name what to fix, got:\n%s", r.errs[0])
	}
}

func TestAssertCoveragePassesAtThreshold(t *testing.T) {
	var r recorder
	hullcheck.AssertCoverage(&r, half(t), 50)
	if len(r.errs) != 0 {
		t.Fatalf("unexpected failure: %v", r.errs)
	}
}

func TestAssertNoNewBreachesRatchets(t *testing.T) {
	var r recorder
	hullcheck.AssertNoNewBreaches(&r, half(t), "RULES-1.2")
	if len(r.errs) != 0 {
		t.Fatalf("an allowed breach must not fail: %v", r.errs)
	}
	var r2 recorder
	hullcheck.AssertNoNewBreaches(&r2, half(t))
	if len(r2.errs) != 1 {
		t.Fatalf("an unlisted breach must fail: %v", r2.errs)
	}
}

func TestReadRefusesAPolicylessRepo(t *testing.T) {
	if _, err := hullcheck.Read(t.TempDir()); err == nil {
		t.Fatal("a repo with no rules must error, not report 100%")
	}
}
