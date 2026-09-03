// Package provenance audits whether generated artifacts can answer the three
// questions somebody will eventually ask about them: where did this come from,
// may we ship it, and can we reproduce it.
//
// The naive version sniffs for files that look provenance-shaped and guesses.
// This does not guess. The repository DECLARES its scheme - where the artifacts
// are, where their record lives, and which fields must be filled - and the audit
// is then arithmetic. That makes it work for sidecars, SPDX, CycloneDX, in-toto
// or C2PA without hullcheck needing to understand any of them.
//
// With nothing declared it reports UNKNOWN and names the conventions it looked
// for. It never reports 100% for a repository that simply has no artifacts
// registered: a denominator of zero is not a clean bill of health.
package provenance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spaceship-alpha-9/hullcheck/internal/manifest"
)

// Verdict is the state of one artifact's provenance.
type Verdict string

const (
	// Recorded: a record exists and every required field is filled.
	Recorded Verdict = "RECORDED"
	// Missing: the artifact has no provenance record at all.
	Missing Verdict = "MISSING"
	// Incomplete: a record exists but required fields are absent or empty. Worse
	// than missing in one way - it looks answered and is not.
	Incomplete Verdict = "INCOMPLETE"
)

// Item is one audited artifact.
type Item struct {
	Artifact string   `json:"artifact"`
	Record   string   `json:"record,omitempty"`
	Verdict  Verdict  `json:"verdict"`
	Absent   []string `json:"absent_fields,omitempty"`
}

// Report is a provenance audit.
type Report struct {
	// Declared is false when the repository declares no scheme. Everything else
	// is then meaningless, and the caller must say UNKNOWN rather than a number.
	Declared bool     `json:"declared"`
	Items    []Item   `json:"items"`
	Found    []string `json:"conventions_found,omitempty"`
}

// Coverage is the share of artifacts with a complete record.
func (r Report) Coverage() float64 {
	if len(r.Items) == 0 {
		return 0
	}
	n := 0
	for _, i := range r.Items {
		if i.Verdict == Recorded {
			n++
		}
	}
	return float64(n) / float64(len(r.Items))
}

// Counts summarises verdicts.
func (r Report) Counts() map[Verdict]int {
	c := map[Verdict]int{Recorded: 0, Missing: 0, Incomplete: 0}
	for _, i := range r.Items {
		c[i.Verdict]++
	}
	return c
}

// knownRecords are exact filename conventions, not guesses. Each is a published
// format with a fixed name, so recognising one is a fact rather than a heuristic.
var knownRecords = []struct{ suffix, name string }{
	{".spdx.json", "SPDX SBOM"},
	{".cdx.json", "CycloneDX SBOM"},
	{"bom.json", "CycloneDX SBOM"},
	{".intoto.jsonl", "in-toto attestation"},
	{".sigstore.json", "Sigstore bundle"},
	{".meta.json", "sidecar record"},
	{"c2pa.json", "C2PA manifest"},
}

// Audit runs the declared scheme. With none declared it reports what conventions
// are present so the user can declare one, and Declared stays false.
func Audit(root string, decls []manifest.Provenance) (Report, error) {
	if len(decls) == 0 {
		return Report{Declared: false, Found: discover(root)}, nil
	}
	out := Report{Declared: true}
	for _, d := range decls {
		files, err := glob(root, d.Artifacts, d.Ignore)
		if err != nil {
			return Report{}, err
		}
		for _, rel := range files {
			out.Items = append(out.Items, audit(root, rel, d))
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].Verdict != out.Items[j].Verdict {
			return out.Items[i].Verdict < out.Items[j].Verdict
		}
		return out.Items[i].Artifact < out.Items[j].Artifact
	})
	return out, nil
}

func audit(root, rel string, d manifest.Provenance) Item {
	rec := expand(d.Record, rel)
	it := Item{Artifact: rel, Record: rec}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rec))) //nolint:gosec
	if err != nil {
		it.Verdict = Missing
		return it
	}
	if len(d.Require) == 0 {
		it.Verdict = Recorded
		return it
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		// A record we cannot read cannot answer anything.
		it.Verdict = Incomplete
		it.Absent = d.Require
		return it
	}
	for _, f := range d.Require {
		if !filled(doc, f) {
			it.Absent = append(it.Absent, f)
		}
	}
	if len(it.Absent) > 0 {
		it.Verdict = Incomplete
		return it
	}
	it.Verdict = Recorded
	return it
}

// expand fills the record template. {artifact} is the artifact path, {base} the
// same without its extension.
func expand(tmpl, artifact string) string {
	base := strings.TrimSuffix(artifact, filepath.Ext(artifact))
	r := strings.NewReplacer("{artifact}", artifact, "{base}", base)
	return r.Replace(tmpl)
}

// filled reports whether a dotted field path exists and is non-empty. Dotted paths
// matter because SPDX and CycloneDX nest the fields worth requiring.
func filled(doc any, path string) bool {
	cur := doc
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[part]
		if !ok {
			return false
		}
	}
	switch v := cur.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case bool:
		return true
	default:
		return true
	}
}

// skipWalk excludes only directories that are never artifacts. It deliberately
// does NOT reuse the shared skip list: that one excludes dist, build and target,
// which are precisely where generated artifacts live. Skipping them would audit
// nothing and report success.
func skipWalk(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "venv", "__pycache__":
		return true
	}
	return false
}

// discover names the provenance conventions actually present, by exact filename.
func discover(root string) []string {
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			if skipWalk(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		n := strings.ToLower(d.Name())
		for _, k := range knownRecords {
			if strings.HasSuffix(n, k.suffix) {
				seen[k.name] = true
			}
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// glob walks root matching a pattern that may contain **.
func glob(root, pattern string, ignore []string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			if skipWalk(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil //nolint:nilerr
		}
		rel = filepath.ToSlash(rel)
		if !Match(pattern, rel) {
			return nil
		}
		for _, ig := range ignore {
			if Match(ig, rel) {
				return nil
			}
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// Match is a glob with ** support. filepath.Match has no ** and would silently
// fail to match "art/x/y.png" against "art/**/*.png", quietly auditing nothing.
func Match(pattern, name string) bool {
	return matchParts(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchParts(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// ** matches zero or more path segments.
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchParts(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := filepath.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// Describe renders a one-line summary for the caller.
func Describe(r Report) string {
	if !r.Declared {
		if len(r.Found) == 0 {
			return "UNKNOWN: no provenance scheme declared, and no known convention found"
		}
		return fmt.Sprintf("UNKNOWN: no scheme declared, but these are present: %s",
			strings.Join(r.Found, ", "))
	}
	c := r.Counts()
	return fmt.Sprintf("%.0f%% recorded (%d recorded, %d incomplete, %d missing)",
		r.Coverage()*100, c[Recorded], c[Incomplete], c[Missing])
}
