package gates

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
        export PATH="$PATH:/opt/homebrew/bin"
        swiftlint --strict
        "${SRCROOT}"/scripts/check_style.sh
        swiftformat --lint . || true
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
	// One gate per command a phase runs. The PATH line and the "|| true" line
	// cannot fail the build; Info.plist's path: belongs to the target.
	var got []string
	for _, g := range gs {
		if g.Kind != model.KindBuildPhase {
			continue
		}
		if g.Stage != model.Build {
			t.Errorf("%s: stage %q, want build", g.ID, g.Stage)
		}
		got = append(got, g.Name+": "+g.Run)
	}
	want := []string{
		`hullcheck: "scripts/hullcheck.sh"`,
		"Lint: swiftlint --strict",
		"Lint: scripts/check_style.sh",
		"Verify bundle: scripts/verify_bundle.sh",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("build phase gates = %q, want %q", got, want)
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

func TestShellCommandsKeepWhatCanFail(t *testing.T) {
	script := []string{
		"#!/bin/sh",
		"set -e",
		"export PATH=/opt/bin:$PATH",
		"GOFLAGS=-mod=mod",
		`echo "running gates"`,
		"if ! npm test; then echo fail; exit 1; fi",
		"then make vet",
		"for f in scripts/*.sh; do shellcheck \"$f\"; done",
		"while make wait-ready; do sleep 1; done",
		"npm run lint || true",
		"lint() {",
		"  exec make lint \\",
		"    --strict",
		"}",
		"cat <<EOF",
		"make not-a-gate",
		"EOF",
		"GOFLAGS=-mod=mod go test ./...",
	}
	var got []string
	for _, c := range shellCommands(script, 1) {
		got = append(got, fmt.Sprintf("%d %s", c.line, c.run))
	}
	want := []string{
		"6 npm test", "7 make vet", `8 shellcheck "$f"`, "9 make wait-ready",
		"12 make lint --strict", "15 cat <<EOF", "18 GOFLAGS=-mod=mod go test ./...",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands =\n  %q\nwant\n  %q", got, want)
	}
}

func TestQuotedSrcRootIsTheSpecDirectory(t *testing.T) {
	for _, tc := range []struct{ in, dir, want string }{
		{`"${SRCROOT}"/scripts/x.sh`, "ios", "ios/scripts/x.sh"},
		{`"${SRCROOT}/scripts/x.sh"`, ".", `"scripts/x.sh"`},
		{`$(PROJECT_DIR)/x.sh --fast`, ".", "x.sh --fast"},
		{`"$SRCROOT"/x.sh`, "app", "app/x.sh"},
	} {
		if got := srcRoot(tc.in, tc.dir); got != tc.want {
			t.Errorf("srcRoot(%q, %q) = %q, want %q", tc.in, tc.dir, got, tc.want)
		}
	}
}

// A git hook the repository ships is a gate at the hook's stage, one per command,
// and a checker it runs is lifted to that stage.
func TestShippedGitHooksAreGates(t *testing.T) {
	root := fixture(t, map[string]string{
		"check_secrets.sh": "exit 0\n",
		".githooks/pre-push": "#!/bin/sh\n# the local CI\nset -e\nexport GOFLAGS=-mod=mod\n" +
			"echo running gates\nmake deps\nexec ./check_secrets.sh \\\n  --strict\n",
		".husky/pre-commit":   "npx lint-staged\n",
		".husky/post-commit":  "make notify\n",
		".husky/_/husky.sh":   "make internals\n",
		".git-hooks/README":   "make docs\n",
		"scripts/pre-push.sh": "make nothing\n",
	})
	// fixture writes 0600; git runs only an executable hook.
	for _, h := range []string{".githooks/pre-push", ".husky/post-commit"} {
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(h)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gs := Promote(must(Discover(root)))
	var runs []string
	for _, g := range gs {
		if g.Kind == model.KindGitHook {
			runs = append(runs, string(g.Stage)+" "+g.Run)
		}
	}
	want := []string{ // sorted by gate id: .githooks before .husky
		"pre-push make deps",
		"pre-push ./check_secrets.sh --strict",
		"pre-commit npx lint-staged",
	}
	if strings.Join(runs, "|") != strings.Join(want, "|") {
		t.Errorf("hook gates = %q, want %q", runs, want)
	}
	if g, _ := find(gs, "check_secrets.sh"); g.Stage != model.PrePush {
		t.Errorf("check_secrets.sh stage = %q, want pre-push - the hook runs it", g.Stage)
	}
}

// A hook's name is git's vocabulary, not the repository's. Naming the gate after
// it once linked "never commit a key" to a lint hook through the word "commit".
func TestAHookNameIsNotEvidence(t *testing.T) {
	root := fixture(t, map[string]string{".githooks/pre-commit": "#!/bin/sh\nnpx lint-staged\n"})
	if err := os.Chmod(filepath.Join(root, ".githooks", "pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	gs, _ := Discover(root)
	if len(gs) != 1 || gs[0].Name != "npx lint-staged" {
		t.Errorf("hook gate should be named by its command: %+v", gs)
	}
}

// git skips a hook that is not executable, so it is no gate.
func TestANonExecutableHookIsNotAGate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no executable bit")
	}
	root := fixture(t, map[string]string{".githooks/pre-push": "make deps\n"})
	if gs, _ := Discover(root); countKind(gs, model.KindGitHook) != 0 {
		t.Errorf("a hook git will not run was counted: %+v", gs)
	}
}

// An aggregate target's buildScripts run when it builds. A template's scripts run
// in the targets that use it - directly or through another template - and nowhere
// if nothing does.
func TestXcodegenAggregatesAndTemplates(t *testing.T) {
	root := fixture(t, map[string]string{
		"project.yml": `name: App
targetTemplates:
  Policy:
    templates: [Lintable]
    preBuildScripts:
      - name: policy
        script: scripts/check_policy.sh
  Lintable:
    postCompileScripts:
      - name: lint
        script: swiftlint --strict
  Unused:
    preBuildScripts:
      - name: never-runs
        script: scripts/check_unused.sh
targets:
  App:
    type: application
    templates:
      - Policy
aggregateTargets:
  Release:
    targets: [App]
    buildScripts:
      - name: gate
        script: make verify
`,
	})
	gs, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range gs {
		if g.Kind == model.KindBuildPhase {
			got = append(got, g.Name+": "+g.Run)
		}
	}
	slices.Sort(got)
	want := []string{"gate: make verify", "lint: swiftlint --strict", "policy: scripts/check_policy.sh"}
	if !slices.Equal(got, want) {
		t.Errorf("build phase gates = %q, want %q (Unused applies to no target)", got, want)
	}
}
