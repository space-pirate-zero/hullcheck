// Package rules finds stated constraints in a repository's policy documents.
//
// Discovery is structural, never inferential. A rule is recognised by its SHAPE —
// a numbered clause, or a sentence carrying an obligation modal — not by a model's
// opinion of what the prose means. That is the whole reason the reading is
// reproducible and can run with the network off.
package rules

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
)

// policyNames are filenames that are policy documents wherever they appear.
var policyNames = map[string]bool{
	"rules.md": true, "claude.md": true, "agents.md": true,
	"contributing.md": true, "conventions.md": true, "standards.md": true,
	"policy.md": true, "architecture.md": true, "engineering.md": true,
	"style-guide.md": true, "styleguide.md": true, "security.md": true,
}

// policyDirs are directories whose Markdown is treated as policy.
var policyDirs = []string{"adr", "adrs", "decisions", "rfc", "rfcs", "policy", "policies"}

var (
	// "7.8 Every asset records its provenance." — numbered clause, the strongest
	// structural signal a document is stating rules rather than describing them.
	reClause = regexp.MustCompile(`^\s{0,3}(\d+\.\d+[a-z]?|\d+\.)\s+(\S.*)$`)
	// A bullet or plain sentence carrying an obligation.
	reBullet = regexp.MustCompile(`^\s{0,3}(?:[-*+]|\d+\))\s+(\S.*)$`)
	// An explicit gate annotation the repo already wrote, e.g. "*Gate: make preflight*".
	reGate     = regexp.MustCompile(`(?i)^\s*[*_]{0,2}gate[*_]{0,2}\s*:\s*[*_]{0,2}(.+?)[*_]{0,2}\s*$`)
	reFence    = regexp.MustCompile("^\\s{0,3}(```|~~~)")
	reHeading  = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+)$`)
	reEmphasis = regexp.MustCompile(`[*_` + "`" + `]`)
	reSpace    = regexp.MustCompile(`\s+`)
)

// strongModals denote a hard obligation. Their presence is what turns a sentence
// into a candidate rule.
var strongModals = []string{
	"must not", "must never", "must", "never", "always", "shall not", "shall",
	"do not", "don't", "cannot", "may not", "is forbidden", "is banned",
	"is required", "are required", "is prohibited", "no exceptions",
}

// softModals denote a preference rather than an obligation.
var softModals = []string{"should not", "should", "prefer", "avoid", "recommended", "ought to"}

// Discover walks root and returns every rule it can justify, plus the policy
// documents it read. Errors reading an individual file are skipped, not fatal:
// a single unreadable doc must not deny you a reading of the rest.
func Discover(root string) ([]model.Rule, []string, error) {
	files, err := policyFiles(root)
	if err != nil {
		return nil, nil, err
	}
	var out []model.Rule
	for _, f := range files {
		rs, err := parseFile(root, f)
		if err != nil {
			continue
		}
		out = append(out, rs...)
	}
	rel := make([]string, 0, len(files))
	for _, f := range files {
		r, _ := filepath.Rel(root, f)
		rel = append(rel, filepath.ToSlash(r))
	}
	sort.Strings(rel)
	return out, rel, nil
}

func policyFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree must not fail the walk
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() {
			if skipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		if policyNames[name] {
			out = append(out, path)
			return nil
		}
		parent := strings.ToLower(filepath.Base(filepath.Dir(path)))
		for _, d := range policyDirs {
			if parent == d {
				out = append(out, path)
				return nil
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "venv", ".venv", "dist", "build",
		"target", ".next", "__pycache__", ".terraform", "testdata":
		return true
	}
	return false
}

func parseFile(root, path string) ([]model.Rule, error) {
	fh, err := os.Open(path) //nolint:gosec // path comes from our own walk of root
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()

	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	base := strings.TrimSuffix(strings.ToUpper(filepath.Base(path)), ".MD")

	var (
		out     []model.Rule
		inFence bool
		n       int
		seq     int
	)
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		n++
		line := sc.Text()
		if reFence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// A gate annotation binds to the rule it follows.
		if m := reGate.FindStringSubmatch(line); m != nil && len(out) > 0 {
			out[len(out)-1].GateHint = clean(m[1])
			continue
		}
		stmt, id, ok := candidate(line, base, &seq)
		if !ok {
			continue
		}
		out = append(out, model.Rule{
			ID:        id,
			Source:    fmt.Sprintf("%s:%d", rel, n),
			File:      rel,
			Line:      n,
			Statement: stmt,
			Severity:  severityOf(stmt),
		})
	}
	return out, sc.Err()
}

// candidate decides whether a line states a rule, and returns its statement and id.
func candidate(line, base string, seq *int) (stmt, id string, ok bool) {
	if m := reClause.FindStringSubmatch(line); m != nil {
		body := clean(m[2])
		if len(body) < 12 || !hasModal(body) && !looksNormative(body) {
			return "", "", false
		}
		return body, fmt.Sprintf("%s-%s", base, strings.TrimSuffix(m[1], ".")), true
	}
	if m := reBullet.FindStringSubmatch(line); m != nil {
		body := clean(m[1])
		if len(body) < 12 || !hasModal(body) {
			return "", "", false
		}
		*seq++
		return body, fmt.Sprintf("%s-%d", base, *seq), true
	}
	if m := reHeading.FindStringSubmatch(line); m != nil {
		body := clean(m[1])
		if len(body) < 12 || !hasModal(body) {
			return "", "", false
		}
		*seq++
		return body, fmt.Sprintf("%s-%d", base, *seq), true
	}
	return "", "", false
}

// looksNormative catches numbered clauses that state a rule without a modal, e.g.
// "3.1 Palette is exactly: void, pink, cyan." Numbering already signals intent.
func looksNormative(s string) bool {
	l := strings.ToLower(s)
	for _, k := range []string{" is exactly", " are exactly", " only", " no other", " never"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

func hasModal(s string) bool {
	l := strings.ToLower(s)
	for _, m := range strongModals {
		if containsWord(l, m) {
			return true
		}
	}
	for _, m := range softModals {
		if containsWord(l, m) {
			return true
		}
	}
	return false
}

func severityOf(s string) model.Severity {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "critical"), strings.Contains(l, "security"),
		strings.Contains(l, "secret"), strings.Contains(l, "no exceptions"):
		return model.High
	}
	for _, m := range strongModals {
		if containsWord(l, m) {
			return model.High
		}
	}
	for _, m := range softModals {
		if containsWord(l, m) {
			return model.Medium
		}
	}
	return model.Unknown
}

// containsWord matches a phrase on word boundaries, so "must" does not fire on
// "mustard" and "no" does not fire on "north".
func containsWord(hay, needle string) bool {
	i := 0
	for {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		leftOK := start == 0 || !isWordByte(hay[start-1])
		rightOK := end == len(hay) || !isWordByte(hay[end])
		if leftOK && rightOK {
			return true
		}
		i = start + 1
		if i >= len(hay) {
			return false
		}
	}
}

func isWordByte(b byte) bool {
	return b == '_' || b == '\'' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func clean(s string) string {
	s = reEmphasis.ReplaceAllString(s, "")
	s = reSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
