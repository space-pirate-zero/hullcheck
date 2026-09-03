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
