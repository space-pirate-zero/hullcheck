package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/space-pirate-zero/hullcheck/internal/manifest"
)

func repo(t *testing.T, files map[string]string) string {
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

func item(t *testing.T, r Report, artifact string) Item {
	t.Helper()
	for _, i := range r.Items {
		if i.Artifact == artifact {
			return i
		}
	}
	t.Fatalf("artifact %q not audited; items: %+v", artifact, r.Items)
	return Item{}
}

var sidecar = manifest.Provenance{
	Artifacts: "art/**/*.png",
	Record:    "{artifact}.meta.json",
	Require:   []string{"source", "model", "license"},
}

func TestCompleteRecordIsRecorded(t *testing.T) {
	root := repo(t, map[string]string{
		"art/a/one.png":           "x",
		"art/a/one.png.meta.json": `{"source":"generated","model":"m","license":"studio-owned"}`,
	})
	rep, err := Audit(root, []manifest.Provenance{sidecar})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Declared {
		t.Fatal("a declared scheme must set Declared")
	}
	if got := item(t, rep, "art/a/one.png"); got.Verdict != Recorded {
		t.Fatalf("verdict = %q, want RECORDED (absent: %v)", got.Verdict, got.Absent)
	}
	if rep.Coverage() != 1 {
		t.Errorf("coverage = %v, want 1", rep.Coverage())
	}
}

func TestNoRecordIsMissing(t *testing.T) {
	root := repo(t, map[string]string{"art/b/two.png": "x"})
	rep, _ := Audit(root, []manifest.Provenance{sidecar})
	if got := item(t, rep, "art/b/two.png"); got.Verdict != Missing {
		t.Fatalf("verdict = %q, want MISSING", got.Verdict)
	}
}

// A record that exists but does not answer is worse in one way than none: it
// looks answered.
func TestEmptyRequiredFieldIsIncomplete(t *testing.T) {
	root := repo(t, map[string]string{
		"art/c/three.png":           "x",
		"art/c/three.png.meta.json": `{"source":"generated","model":"  ","license":""}`,
	})
	rep, _ := Audit(root, []manifest.Provenance{sidecar})
	got := item(t, rep, "art/c/three.png")
	if got.Verdict != Incomplete {
		t.Fatalf("verdict = %q, want INCOMPLETE", got.Verdict)
	}
	if len(got.Absent) != 2 {
		t.Errorf("absent = %v, want model and license", got.Absent)
	}
}

func TestUnreadableRecordIsIncompleteNotRecorded(t *testing.T) {
	root := repo(t, map[string]string{
		"art/d/four.png":           "x",
		"art/d/four.png.meta.json": "not json at all",
	})
	rep, _ := Audit(root, []manifest.Provenance{sidecar})
	if got := item(t, rep, "art/d/four.png"); got.Verdict != Incomplete {
		t.Fatalf("verdict = %q, want INCOMPLETE - a record we cannot read answers nothing", got.Verdict)
	}
}

func TestNestedFieldsAreCheckedByDottedPath(t *testing.T) {
	// SPDX and CycloneDX nest the fields worth requiring, so a top-level-only
	// check would pass documents that answer nothing.
	root := repo(t, map[string]string{
		"dist/app":  "x",
		"sbom.json": `{"creationInfo":{"created":"2026-01-01"},"name":"app"}`,
	})
	rep, _ := Audit(root, []manifest.Provenance{{
		Artifacts: "dist/*", Record: "sbom.json",
		Require: []string{"name", "creationInfo.created"},
	}})
	if got := item(t, rep, "dist/app"); got.Verdict != Recorded {
		t.Fatalf("verdict = %q (absent %v)", got.Verdict, got.Absent)
	}

	rep2, _ := Audit(root, []manifest.Provenance{{
		Artifacts: "dist/*", Record: "sbom.json",
		Require: []string{"creationInfo.creators"},
	}})
	if got := item(t, rep2, "dist/app"); got.Verdict != Incomplete {
		t.Errorf("a missing nested field must be caught, got %q", got.Verdict)
	}
}

func TestIgnoreGlobsExcludeArtifacts(t *testing.T) {
	root := repo(t, map[string]string{
		"art/keep.png":  "x",
		"art/tmp/x.png": "x",
	})
	rep, _ := Audit(root, []manifest.Provenance{{
		Artifacts: "art/**/*.png", Record: "{artifact}.meta.json",
		Ignore: []string{"art/tmp/**"},
	}})
	for _, i := range rep.Items {
		if strings.HasPrefix(i.Artifact, "art/tmp/") {
			t.Fatalf("ignored path was audited: %+v", i)
		}
	}
}

// filepath.Match has no **, so a pattern like art/**/*.png would silently match
// nothing and the audit would report success over an empty set.
func TestDoubleStarGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"art/**/*.png", "art/a/b/c.png", true},
		{"art/**/*.png", "art/c.png", true},
		{"art/**/*.png", "other/c.png", false},
		{"art/**", "art/a/b", true},
		{"dist/*", "dist/app", true},
		{"dist/*", "dist/a/b", false},
		{"**/*.json", "a/b/c.json", true},
		{"art/tmp/**", "art/tmp/x.png", true},
		{"art/tmp/**", "art/keep.png", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

// No scheme declared must produce UNKNOWN - never 0%, and never a flattering 100%.
func TestUndeclaredSchemeReportsUnknown(t *testing.T) {
	root := repo(t, map[string]string{"art/x.png": "x", "sbom.spdx.json": "{}"})
	rep, err := Audit(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Declared {
		t.Fatal("nothing was declared")
	}
	d := Describe(rep)
	if !strings.HasPrefix(d, "UNKNOWN") {
		t.Fatalf("describe = %q, want UNKNOWN", d)
	}
	if !strings.Contains(d, "SPDX SBOM") {
		t.Errorf("known conventions present should be named so the user can declare one: %q", d)
	}
}

func TestDiscoveryRecognisesKnownFormatsByExactName(t *testing.T) {
	root := repo(t, map[string]string{
		"a.intoto.jsonl": "{}", "b.cdx.json": "{}", "c/art.png.meta.json": "{}",
		"random.txt": "x",
	})
	got := discover(root)
	want := map[string]bool{"in-toto attestation": true, "CycloneDX SBOM": true, "sidecar record": true}
	if len(got) != len(want) {
		t.Fatalf("discovered %v, want %v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected convention %q", g)
		}
	}
}
