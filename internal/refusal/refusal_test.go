package refusal

import (
	"os"
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
