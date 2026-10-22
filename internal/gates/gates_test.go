package gates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/space-pirate-zero/hullcheck/internal/model"
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

// An xcodegen spec's build-phase scripts run on every local build of the target,
// so they are gates at the build stage, and a checker they invoke is lifted to it.
func TestXcodegenBuildPhasesAreBuildStageGates(t *testing.T) {
	root := fixture(t, map[string]string{
		"scripts/hullcheck.sh": "exit 0\n",
		"project.yml": `name: PhantomTiles
options:
  bundleIdPrefix: com.example
targets:
  PhantomTiles:
    type: application
    platform: iOS
    preBuildScripts:
      - name: hullcheck
        # The policy gate.
        script: '"${SRCROOT}/scripts/hullcheck.sh"'
        basedOnDependencyAnalysis: false
    postCompileScripts:
    - script: |
        set -e
        name: not-a-key
        swiftlint --strict
      name: Lint
    info:
      path: AppEntry/Info.plist
  PhantomTilesTests:
    type: bundle.unit-test
    postBuildScripts:
      - path: scripts/verify_bundle.sh
        name: Verify bundle
schemes:
  PhantomTiles:
    build:
      targets:
        PhantomTiles: all
`,
	})
	gs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, run, file string }{
		{"hullcheck", `"scripts/hullcheck.sh"`, "project.yml"},
		{"Lint", "set -e", "project.yml"},
		{"Verify bundle", "scripts/verify_bundle.sh", "project.yml"},
	} {
		g, ok := find(gs, tc.name)
		if !ok {
			t.Errorf("build phase %q not found: %+v", tc.name, gs)
			continue
		}
		if g.Stage != model.Build || g.Kind != model.KindBuildPhase {
			t.Errorf("%s: stage %q kind %q, want build / build-phase", tc.name, g.Stage, g.Kind)
		}
		if g.Run != tc.run {
			t.Errorf("%s: run = %q, want %q", tc.name, g.Run, tc.run)
		}
	}
	// Info.plist's path: belongs to the target, not to a build phase, and a block
	// scalar's body is shell however much it looks like a key.
	for _, bad := range []string{"not-a-key", "PhantomTiles"} {
		if _, ok := find(gs, bad); ok {
			t.Errorf("%q is not a build phase: %+v", bad, gs)
		}
	}
	if n := countKind(gs, model.KindBuildPhase); n != 3 {
		t.Errorf("found %d build phases, want 3: %+v", n, gs)
	}

	gs = Promote(gs)
	g, ok := find(gs, "hullcheck.sh")
	if !ok {
		t.Fatalf("checker script not found: %+v", gs)
	}
	if g.Stage != model.Build {
		t.Errorf("scripts/hullcheck.sh stage = %q, want build - a build phase runs it", g.Stage)
	}
}

func TestXcodegenSpecInASubdirectoryKeepsPathsRepoRelative(t *testing.T) {
	root := fixture(t, map[string]string{
		"ios/scripts/check_assets.sh": "exit 0\n",
		"ios/project.yml": "targets:\n  App:\n    preBuildScripts:\n" +
			"      - script: $SRCROOT/scripts/check_assets.sh\n" +
			"      - path: scripts/check_assets.sh\n",
	})
	gs := Promote(must(Discover(root)))
	var runs []string
	for _, g := range gs {
		if g.Kind == model.KindBuildPhase {
			runs = append(runs, g.Run)
		}
	}
	if len(runs) != 2 || runs[0] != "ios/scripts/check_assets.sh" || runs[1] != "ios/scripts/check_assets.sh" {
		t.Errorf("build phase runs = %q, want both repo-relative", runs)
	}
	if g, _ := find(gs, "check_assets.sh"); g.Stage != model.Build {
		t.Errorf("check_assets.sh stage = %q, want build", g.Stage)
	}
}

func TestProjectYmlThatIsNotXcodegenYieldsNothing(t *testing.T) {
	root := fixture(t, map[string]string{
		"project.yml": "name: docs\nsettings:\n  preBuildScripts:\n    - script: ./check.sh\n",
	})
	gs, _ := Discover(root)
	if n := countKind(gs, model.KindBuildPhase); n != 0 {
		t.Errorf("a project.yml with no targets: is not an xcodegen spec: %+v", gs)
	}
}

func countKind(gs []model.Gate, k model.Kind) int {
	n := 0
	for _, g := range gs {
		if g.Kind == k {
			n++
		}
	}
	return n
}

func must(gs []model.Gate, err error) []model.Gate {
	if err != nil {
		panic(err)
	}
	return gs
}
