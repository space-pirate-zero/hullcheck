package gates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
)

func fixture(t *testing.T, files map[string]string) string {
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

func find(gs []model.Gate, name string) (model.Gate, bool) {
	for _, g := range gs {
		if g.Name == name {
			return g, true
		}
	}
	return model.Gate{}, false
}

func TestWorkflowRunStepsBecomeGates(t *testing.T) {
	root := fixture(t, map[string]string{
		".github/workflows/ci.yml": `name: ci
on:
  pull_request:
  push:
    branches: [main]
jobs:
  brand:
    runs-on: ubuntu-latest
    steps:
      - run: python check_brand.py
`,
	})
	gs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := find(gs, "brand")
	if !ok {
		t.Fatalf("job 'brand' not found: %+v", gs)
	}
	if g.Stage != model.PullReq {
		t.Errorf("stage = %q, want pull-request", g.Stage)
	}
	if g.Run != "python check_brand.py" {
		t.Errorf("run = %q", g.Run)
	}
	if g.Kind != model.KindCIJob {
		t.Errorf("kind = %q", g.Kind)
	}
}

func TestScheduledWorkflowIsNightly(t *testing.T) {
	root := fixture(t, map[string]string{
		".github/workflows/nightly.yml": `name: nightly
on:
  schedule:
    - cron: "0 3 * * *"
jobs:
  audit:
    steps:
      - run: ./verify_all.sh
`,
	})
	gs, _ := Discover(root)
	g, ok := find(gs, "audit")
	if !ok {
		t.Fatalf("not found: %+v", gs)
	}
	if g.Stage != model.Nightly {
		t.Errorf("stage = %q, want nightly", g.Stage)
	}
}

func TestFastestTriggerWins(t *testing.T) {
	// A workflow on both schedule and pull_request catches violations at PR time.
	root := fixture(t, map[string]string{
		".github/workflows/both.yml": `on:
  schedule:
    - cron: "0 3 * * *"
  pull_request:
jobs:
  lint:
    steps:
      - run: make lint
`,
	})
	gs, _ := Discover(root)
	g, _ := find(gs, "lint")
	if g.Stage != model.PullReq {
		t.Errorf("stage = %q, want pull-request (fastest trigger wins)", g.Stage)
	}
}

func TestPreCommitHooksAreFastest(t *testing.T) {
	root := fixture(t, map[string]string{
		".pre-commit-config.yaml": "repos:\n  - repo: local\n    hooks:\n      - id: no-secrets\n",
	})
	gs, _ := Discover(root)
	g, ok := find(gs, "no-secrets")
	if !ok {
		t.Fatalf("hook not found: %+v", gs)
	}
	if g.Stage != model.PreCommit {
		t.Errorf("stage = %q, want pre-commit", g.Stage)
	}
}

func TestOnlyCheckishMakeTargetsCount(t *testing.T) {
	root := fixture(t, map[string]string{
		"Makefile": "deploy:\n\t./deploy.sh\n\npreflight:\n\t./check.sh\n\nbuild:\n\tgo build\n",
	})
	gs, _ := Discover(root)
	if _, ok := find(gs, "preflight"); !ok {
		t.Errorf("preflight target should be a gate: %+v", gs)
	}
	for _, bad := range []string{"deploy", "build"} {
		if _, ok := find(gs, bad); ok {
			t.Errorf("%q runs but does not enforce; it must not be a gate", bad)
		}
	}
}

func TestStandaloneScriptIsManualNotScheduled(t *testing.T) {
	root := fixture(t, map[string]string{"check_canon.py": "print('ok')\n"})
	gs, _ := Discover(root)
	g, ok := find(gs, "check_canon.py")
	if !ok {
		t.Fatalf("script not found: %+v", gs)
	}
	if g.Stage != model.Manual {
		t.Errorf("stage = %q, want manual — nothing schedules it", g.Stage)
	}
}

func TestPromoteLiftsScriptsInvokedByCI(t *testing.T) {
	root := fixture(t, map[string]string{
		"check_brand.py": "print('ok')\n",
		".github/workflows/ci.yml": `on: [pull_request]
jobs:
  brand:
    steps:
      - run: python check_brand.py
`,
	})
	gs, _ := Discover(root)
	gs = Promote(gs)
	g, ok := find(gs, "check_brand.py")
	if !ok {
		t.Fatalf("script not found: %+v", gs)
	}
	if g.Stage != model.PullReq {
		t.Errorf("stage = %q, want pull-request — CI invokes it", g.Stage)
	}
}

func TestVendoredWorkflowsAreIgnored(t *testing.T) {
	root := fixture(t, map[string]string{
		"node_modules/x/.github/workflows/ci.yml": "on: [push]\njobs:\n  a:\n    steps:\n      - run: echo hi\n",
	})
	gs, _ := Discover(root)
	if len(gs) != 0 {
		t.Fatalf("vendored gates leaked in: %+v", gs)
	}
}
