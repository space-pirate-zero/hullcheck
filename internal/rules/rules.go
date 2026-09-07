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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/space-pirate-zero/hullcheck/internal/model"
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

// Skipped is a numbered clause discovery saw and did not count.
//
// A denominator nobody can audit is the same problem as a rule nobody can test.
// Every clause the scanner declines is recorded with the reason, so "36 rules
// found" can be checked rather than believed.
type Skipped struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Statement string `json:"statement"`
	Why       string `json:"why"`
}

// Discover walks root and returns every rule it can justify, plus the policy
// documents it read. Errors reading an individual file are skipped, not fatal:
// a single unreadable doc must not deny you a reading of the rest.
func Discover(root string) ([]model.Rule, []string, error) {
	rs, docs, _, err := DiscoverAll(root)
	return rs, docs, err
}

// DiscoverAll is Discover plus the numbered clauses it declined to count. It is
// what --clauses prints.
func DiscoverAll(root string) ([]model.Rule, []string, []Skipped, error) {
	files, err := policyFiles(root)
	if err != nil {
		return nil, nil, nil, err
	}
	var (
		out     []model.Rule
		skipped []Skipped
	)
	for _, f := range files {
		rs, sk, err := parseFile(root, f)
		if err != nil {
			continue
		}
		out = append(out, rs...)
		skipped = append(skipped, sk...)
	}
	rel := make([]string, 0, len(files))
	for _, f := range files {
		r, _ := filepath.Rel(root, f)
		rel = append(rel, filepath.ToSlash(r))
	}
	sort.Strings(rel)
	return out, rel, skipped, nil
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

func parseFile(root, path string) ([]model.Rule, []Skipped, error) {
	fh, err := os.Open(path) //nolint:gosec // path comes from our own walk of root
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = fh.Close() }()

	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	base := strings.TrimSuffix(strings.ToUpper(filepath.Base(path)), ".MD")

	lines, fenced, err := read(fh)
	if err != nil {
		return nil, nil, err
	}

	var (
		out     []model.Rule
		skipped []Skipped
		seq     int
		// clause is the index in out of the rule that owns the block being
		// walked, so a gate annotation inside the block can bind to it and not
		// only to whichever bullet happened to come last.
		clause = -1
	)
	for i, line := range lines {
		if fenced[i] {
			continue
		}
		n := i + 1
		source := fmt.Sprintf("%s:%d", rel, n)

		// A gate annotation binds to the rule it follows.
		if m := reGate.FindStringSubmatch(line); m != nil {
			hint := clean(m[1])
			if len(out) > 0 {
				out[len(out)-1].GateHint = hint
			}
			if clause >= 0 && out[clause].GateHint == "" {
				out[clause].GateHint = hint
			}
			continue
		}

		if m := reClause.FindStringSubmatch(line); m != nil {
			body := clean(m[2])
			id := fmt.Sprintf("%s-%s", base, strings.TrimSuffix(m[1], "."))
			blk := block(lines, fenced, i)
			why, ok := clauseHolds(body, blk)
			if !ok {
				skipped = append(skipped, Skipped{
					ID: id, Source: source, File: rel, Line: n, Statement: body, Why: why,
				})
				clause = -1
				continue
			}
			out = append(out, model.Rule{
				ID: id, Source: source, File: rel, Line: n,
				Statement: body, Severity: clauseSeverity(body, blk),
			})
			clause = len(out) - 1
			continue
		}

		// A heading closes whatever block was open.
		if reHeading.MatchString(line) {
			clause = -1
		}

		stmt, id, ok := candidate(line, base, &seq)
		if !ok {
			continue
		}
		out = append(out, model.Rule{
			ID: id, Source: source, File: rel, Line: n,
			Statement: stmt, Severity: severityOf(stmt),
		})
	}
	return out, skipped, nil
}

// read slurps a document and marks the lines inside fenced code blocks, which
// state nothing: a sample showing what NOT to do is full of modals.
func read(r io.Reader) (lines []string, fenced []bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	in := false
	for sc.Scan() {
		line := sc.Text()
		if reFence.MatchString(line) {
			in = !in
			lines, fenced = append(lines, line), append(fenced, true)
			continue
		}
		lines, fenced = append(lines, line), append(fenced, in)
	}
	return lines, fenced, sc.Err()
}

// block is the text a numbered clause owns: everything from the line after it
// until the next numbered clause or heading. A rule is often written as a
// declarative headline with the obligation in the subsection beneath it, and
// judging the headline alone drops the clause and the whole subsection with it.
func block(lines []string, fenced []bool, start int) []string {
	var out []string
	for i := start + 1; i < len(lines); i++ {
		if fenced[i] {
			continue
		}
		if reClause.MatchString(lines[i]) || reHeading.MatchString(lines[i]) {
			break
		}
		out = append(out, lines[i])
	}
	return out
}

// clauseHolds decides whether a numbered clause states a rule, reading the whole
// block rather than the first line, and returns why when it does not.
func clauseHolds(body string, blk []string) (why string, ok bool) {
	if len(body) < 12 {
		return "too short to be a statement", false
	}
	if hasModal(body) || looksNormative(body) {
		return "", true
	}
	// A document that names a gate for a clause has already said it is a rule.
	// That is a stronger signal than any modal, and it is the repository's own
	// word rather than the scanner's inference.
	for _, l := range blk {
		if reGate.MatchString(l) {
			return "", true
		}
	}
	for _, l := range blk {
		c := clean(l)
		if hasModal(c) || looksNormative(c) {
			return "", true
		}
	}
	return "no obligation in the clause or in the text beneath it", false
}

// clauseSeverity reads the headline first and falls back to the block, so a
// clause whose obligation lives beneath its headline is not graded as unknown.
func clauseSeverity(body string, blk []string) model.Severity {
	if sev := severityOf(body); sev != model.Unknown {
		return sev
	}
	return severityOf(clean(strings.Join(blk, " ")))
}

// candidate decides whether a bullet or heading states a rule, and returns its
// statement and id. Numbered clauses are judged separately, against their whole
// block rather than one line.
func candidate(line, base string, seq *int) (stmt, id string, ok bool) {
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
