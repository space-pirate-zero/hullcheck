package insight

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/space-pirate-zero/hullcheck/internal/model"
)

func mk(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for n, b := range files {
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func tree(t *testing.T, ts []Tree, path string) Tree {
	t.Helper()
	for _, x := range ts {
		if x.Path == path {
			return x
		}
	}
	t.Fatalf("tree %q not reported in %+v", path, ts)
	return Tree{}
}

func TestPathCoverageFindsAnUngovernedTree(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 6; i++ {
		files[filepath.ToSlash(filepath.Join("payments", "p"+string(rune('a'+i))+".go"))] = "x"
		files[filepath.ToSlash(filepath.Join("infra", "i"+string(rune('a'+i))+".tf"))] = "x"
	}
	root := mk(t, files)
	rep := model.Report{Findings: []model.Finding{{
		Gates: []model.Gate{{ID: "g1", Name: "pay", File: "payments/check_pay.sh"}},
	}}}
	got := PathCoverage(root, rep)
	if p := tree(t, got, "payments"); p.Gates != 1 {
		t.Errorf("payments gates = %d, want 1", p.Gates)
	}
	if i := tree(t, got, "infra"); i.Gates != 0 {
		t.Errorf("infra gates = %d, want 0 - it is ungoverned", i.Gates)
	}
	// Ungoverned trees sort first: that is the finding.
	if got[0].Gates != 0 {
		t.Errorf("ungoverned trees must sort first, got %+v", got[0])
	}
}

func TestPathCoverageIgnoresTinyDirectories(t *testing.T) {
	root := mk(t, map[string]string{"scripts/one.sh": "x", "scripts/two.sh": "x"})
	for _, tr := range PathCoverage(root, model.Report{}) {
		if tr.Path == "scripts" {
			t.Error("a two-file directory is not an ungoverned subsystem; reporting it is noise")
		}
	}
}

func TestBusFactorFlagsSingleOwnerGates(t *testing.T) {
	root := mk(t, map[string]string{
		"CODEOWNERS": "* @team\npayments/ @solo\n",
	})
	rep := model.Report{
		Findings: []model.Finding{{Gates: []model.Gate{
			{ID: "g1", Name: "pay", File: "payments/check_pay.sh"},
		}}},
		Unlogged: []model.Gate{{ID: "g2", Name: "misc", File: "docs/check_docs.sh"}},
	}
	got := BusFactor(root, rep)
	var pay *Owner
	for i := range got {
		if got[i].Gate == "pay" {
			pay = &got[i]
		}
	}
	if pay == nil {
		t.Fatalf("single-owner gate not flagged: %+v", got)
	}
	if len(pay.Owners) != 1 || pay.Owners[0] != "@solo" {
		t.Errorf("owners = %v, want [@solo] - the last matching rule wins", pay.Owners)
	}
}

func TestBusFactorSkipsWellOwnedGates(t *testing.T) {
	root := mk(t, map[string]string{"CODEOWNERS": "* @a @b\n"})
	rep := model.Report{Findings: []model.Finding{{Gates: []model.Gate{
		{ID: "g1", Name: "x", File: "check.sh"},
	}}}}
	if got := BusFactor(root, rep); len(got) != 0 {
		t.Errorf("a two-owner gate is not a bus-factor risk: %+v", got)
	}
}

func TestBusFactorIsSilentWithoutCODEOWNERS(t *testing.T) {
	rep := model.Report{Findings: []model.Finding{{Gates: []model.Gate{{ID: "g", File: "x.sh"}}}}}
	if got := BusFactor(t.TempDir(), rep); got != nil {
		t.Errorf("without CODEOWNERS there is no ownership signal; inventing one would be noise: %+v", got)
	}
}
