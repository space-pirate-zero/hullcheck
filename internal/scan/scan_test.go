package scan

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesRepoWithNoPolicy(t *testing.T) {
	_, err := Run(t.TempDir())
	if !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("err = %v, want ErrNoPolicy - a repo with no rules must be refused, not scored 100%%", err)
	}
}

func TestRefusesAPathThatIsNotADirectory(t *testing.T) {
	root := t.TempDir()
	write(t, root, "file.txt", "x")
	if _, err := Run(filepath.Join(root, "file.txt")); err == nil {
		t.Fatal("expected a refusal for a non-directory path")
	}
}

func TestEndToEndReading(t *testing.T) {
	root := t.TempDir()
	write(t, root, "RULES.md",
		"7.8 **Every asset must record its provenance.**\n\n8.2 Secrets must never enter git.\n")
	write(t, root, "check_provenance.py", "print('ok')\n")
	write(t, root, ".github/workflows/ci.yml",
		"on: [pull_request]\njobs:\n  prov:\n    steps:\n      - run: python check_provenance.py\n")

	rep, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(rep.Findings), rep.Findings)
	}
	if rep.Coverage() != 0.5 {
		t.Errorf("coverage = %v, want 0.5 (provenance gated, secrets not)", rep.Coverage())
	}
	// The script is promoted to pull-request because CI invokes it.
	for _, f := range rep.Findings {
		if f.Rule.ID == "RULES-7.8" && f.Stage() != "pull-request" {
			t.Errorf("provenance stage = %q, want pull-request", f.Stage())
		}
	}
}

func TestReadingIsDeterministic(t *testing.T) {
	root := t.TempDir()
	write(t, root, "RULES.md", "1.1 Secrets must never enter git.\n2.1 Assets must record provenance.\n")
	write(t, root, "check_secrets.sh", "exit 0\n")
	first, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Run(root)
		if err != nil {
			t.Fatal(err)
		}
		if again.Coverage() != first.Coverage() || len(again.Findings) != len(first.Findings) {
			t.Fatal("the same repository produced a different reading; the tool must be deterministic")
		}
		for j := range again.Findings {
			if again.Findings[j].Rule.ID != first.Findings[j].Rule.ID {
				t.Fatal("finding order is not stable")
			}
		}
	}
}

// A Gate: hint written as a code span names the gate's path exactly, underscores
// and all. Stripping them as emphasis once left the rule ungoverned.
func TestCodeSpanGateHintLinksTheGate(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "# Rules\n\n"+
		"- Portraits must always be conditioned on the reference photos.\n"+
		"  *Gate: `scripts/check_likeness.py`*\n")
	write(t, root, "scripts/check_likeness.py", "print('ok')\n")

	rep, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(rep.Findings), rep.Findings)
	}
	f := rep.Findings[0]
	if f.Verdict != "HOLD" || len(f.Gates) != 1 || f.Gates[0].File != "scripts/check_likeness.py" {
		t.Errorf("rule should hold on scripts/check_likeness.py, got %s %+v (%s)", f.Verdict, f.Gates, f.Why)
	}
}

// A rule whose gate an Xcode build phase runs is caught at build time, not left
// to whoever remembers to run the checker by hand.
func TestBuildPhaseGateReadsAsBuildStage(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "# Rules\n\n"+
		"- Portraits must always be conditioned on the reference photos.\n"+
		"  *Gate: `scripts/check_likeness.py`*\n")
	write(t, root, "scripts/check_likeness.py", "print('ok')\n")
	write(t, root, "project.yml", "targets:\n  App:\n    preBuildScripts:\n"+
		"      - name: likeness\n        script: python3 \"${SRCROOT}/scripts/check_likeness.py\" .\n")

	rep, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(rep.Findings), rep.Findings)
	}
	if got := rep.Findings[0].Stage(); got != "build" {
		t.Errorf("stage = %q, want build - every local build runs the gate", got)
	}
}

// Xcode runs build phases with a bare PATH, so a phase's script commonly fixes
// PATH first and calls the checker after. The checker is still run by every build.
func TestCheckerAfterTheFirstLineOfABuildScriptReadsAsBuild(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "# Rules\n\n"+
		"- Portraits must always be conditioned on the reference photos.\n"+
		"  *Gate: `scripts/likeness_guard.sh`*\n")
	write(t, root, "scripts/likeness_guard.sh", "exit 0\n")
	write(t, root, "project.yml", "targets:\n  App:\n    preBuildScripts:\n"+
		"      - name: policy\n        script: |\n"+
		"          export PATH=\"$PATH:/opt/homebrew/bin\"\n"+
		"          \"${SRCROOT}/scripts/likeness_guard.sh\"\n")

	rep, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(rep.Findings))
	}
	f := rep.Findings[0]
	if f.Stage() != "build" {
		t.Errorf("stage = %q, want build - the build phase calls the checker on its second line: %+v", f.Stage(), f.Gates)
	}
}
