// Package manifest reads and writes .hullcheck.yml.
//
// The parser handles exactly the subset of YAML this tool emits, and rejects
// anything it does not recognise rather than guessing. That is a deliberate
// trade: a real YAML library would be one dependency, and "zero third-party
// dependencies" is a claim a supply-chain-adjacent tool should be able to make.
// Refusing unknown keys also means a typo in a manifest is an error the user
// sees, not a rule that silently stops being checked.
package manifest

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/space-pirate-zero/hullcheck/internal/model"
)

// Name is the file hullcheck looks for in the root of a repository.
const Name = ".hullcheck.yml"

// Gate is a declared enforcement point, with the evidence that it works.
type Gate struct {
	Kind string `json:"kind"`
	Run  string `json:"run"`
	// Proves is prose: what the author claims this gate does. It is documentation.
	Proves string `json:"proves,omitempty"`
	// Fixture is the machine-checkable half: a file whose presence violates the
	// rule. --verify writes it into a scratch copy and asserts the gate fails.
	// Without a fixture a gate can be declared but never proven.
	FixturePath string `json:"fixture_path,omitempty"`
	FixtureBody string `json:"fixture_body,omitempty"`
	// NeedsGit copies .git into the scratch copy, for a gate that reads history
	// or tracked-file state. Without it such a gate fails its control run, which
	// says hullcheck could not create the conditions - not that the gate is
	// broken, and those are different findings.
	NeedsGit bool `json:"needs_git,omitempty"`
}

// Rule is a declared rule and the gate that holds it.
type Rule struct {
	ID string `json:"id"`
	// Source names the policy document the rule was read from. It is what makes
	// the declaration addressable: ids come from the clause number and the
	// document's basename, so two RULES.md files in one repository state two
	// different rules with the same id. Without a source, a declaration about
	// one of them silently flips both.
	Source    string `json:"source,omitempty"`
	Statement string `json:"statement"`
	Severity  string `json:"severity,omitempty"`
	Gate      *Gate  `json:"gate,omitempty"`
}

// SourceFile is Source with any ":line" suffix removed, because a line number
// moves every time the document is edited and the file does not. An empty result
// means the declaration did not say which document it is about.
func (r Rule) SourceFile() string {
	s := strings.TrimSpace(r.Source)
	if i := strings.LastIndexByte(s, ':'); i > 0 {
		if _, err := strconv.Atoi(s[i+1:]); err == nil {
			s = s[:i]
		}
	}
	return s
}

// Key is how a declaration addresses a rule: the source document and the id
// together. It matches model.Rule.Key, which is what a reading is indexed by.
func (r Rule) Key() string {
	if f := r.SourceFile(); f != "" {
		return f + "#" + r.ID
	}
	return r.ID
}

// Refusal is a claim that a tool declines to proceed under a stated condition.
//
// It is written as an experiment, not a description: Run is the command, When
// describes the condition in prose, and Fixture creates it. The audit runs the
// command twice - once normally, once with the condition present - so "it refuses"
// becomes something proven rather than believed.
type Refusal struct {
	Tool string `json:"tool"`
	Run  string `json:"run"`
	When string `json:"when"`
	// FixturePath and FixtureBody create the refusal condition, exactly as they
	// do for a gate. Empty means the condition is the absence of everything: the
	// command is run in an empty directory.
	FixturePath string `json:"fixture_path,omitempty"`
	FixtureBody string `json:"fixture_body,omitempty"`
	// EmptyDir runs the command against an empty directory instead of a copy of
	// the repository - the usual way to express "given nothing to work with".
	EmptyDir bool `json:"empty_dir,omitempty"`
	// Remove deletes a path in the scratch copy. Many refusal conditions are an
	// absence - no policy, no manifest, no credentials - and an additive fixture
	// cannot express one.
	Remove string `json:"remove,omitempty"`
	// ExpectExit is the exit code the tool claims to use when refusing.
	ExpectExit int `json:"expect_exit"`
	// ExpectOutput is a string the refusal must print, so an accidental crash
	// with the right exit code is not mistaken for a considered refusal.
	ExpectOutput string `json:"expect_output,omitempty"`
	// NeedsGit copies .git into the scratch copy. A refusal whose condition is a
	// git fact - "this is a linked worktree", "this is not a repository" - cannot
	// exist in a copy that has no .git, and a tool audited without it runs
	// happily and is reported as proceeding under a condition that was never
	// created.
	NeedsGit bool `json:"needs_git,omitempty"`
	// Env replaces or adds environment variables for the run that should refuse,
	// and UnsetEnv removes them. Most conditions an unattended tool refuses on
	// are environmental - a required binary missing from PATH, a credential
	// expired or absent - and none of them can be created by writing or deleting
	// a file inside the copy.
	//
	// The control still runs in the unmodified environment, so "it worked
	// normally, then stopped in the declared way" remains the standard.
	Env      map[string]string `json:"env,omitempty"`
	UnsetEnv []string          `json:"unset_env,omitempty"`
}

// Provenance declares what counts as a provenance record for a set of artifacts.
//
// Declaring the scheme is what makes the audit arithmetic instead of guesswork:
// hullcheck does not need to know whether you use sidecars, SPDX, CycloneDX,
// in-toto or C2PA - only where the artifacts are, where the record lives, and
// which fields must be filled.
type Provenance struct {
	// Artifacts is a glob, ** supported, e.g. "art/**/*.png".
	Artifacts string `json:"artifacts"`
	// Record is where the record lives. {artifact} expands to the artifact path,
	// {base} to it without its extension. A path with neither is a single shared
	// record, such as an SBOM covering a whole tree.
	Record string `json:"record"`
	// Require are field names that must be present and non-empty in the record.
	Require []string `json:"require,omitempty"`
	// Ignore are globs excluded from the artifact set.
	Ignore []string `json:"ignore,omitempty"`
}

// File is a parsed .hullcheck.yml.
type File struct {
	Version    int          `json:"version"`
	Rules      []Rule       `json:"rules"`
	Refusals   []Refusal    `json:"refusals,omitempty"`
	Provenance []Provenance `json:"provenance,omitempty"`
}

// Load reads a manifest. A missing file is not an error: it means unverified mode.
func Load(path string) (*File, bool, error) {
	fh, err := os.Open(path) //nolint:gosec // path is the manifest we were asked for
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = fh.Close() }()
	f, err := Parse(fh)
	if err != nil {
		return nil, true, err
	}
	return f, true, nil
}

// Parse reads the manifest subset. Indentation is significant and must be spaces.
func Parse(r io.Reader) (*File, error) {
	out := &File{Version: 1}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	var cur *Rule
	var curRef *Refusal
	var curProv *Provenance
	section := ""

	for sc.Scan() {
		line++
		raw := sc.Text()
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		if strings.ContainsRune(raw, '\t') {
			return nil, fmt.Errorf("line %d: tabs are not valid indentation in %s", line, Name)
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		body := strings.TrimLeft(raw, " ")

		switch {
		case indent == 0 && body == "rules:":
			section, cur, curRef, curProv = "rules", nil, nil, nil
		case indent == 0 && body == "refusals:":
			section, cur, curRef, curProv = "refusals", nil, nil, nil
		case indent == 0 && body == "provenance:":
			section, cur, curRef, curProv = "provenance", nil, nil, nil
		case indent == 0 && strings.HasPrefix(body, "version:"):
			v := strings.TrimSpace(strings.TrimPrefix(body, "version:"))
			if v != "1" {
				return nil, fmt.Errorf("line %d: unsupported manifest version %q (this build understands 1)", line, v)
			}
		case indent == 0:
			return nil, fmt.Errorf("line %d: unknown top-level key %q", line, firstKey(body))
		case section == "rules" && strings.HasPrefix(body, "- "):
			out.Rules = append(out.Rules, Rule{})
			cur = &out.Rules[len(out.Rules)-1]
			if err := assign(cur, strings.TrimPrefix(body, "- "), line); err != nil {
				return nil, err
			}
		case section == "refusals" && strings.HasPrefix(body, "- "):
			out.Refusals = append(out.Refusals, Refusal{})
			curRef = &out.Refusals[len(out.Refusals)-1]
			if err := assignRefusal(curRef, strings.TrimPrefix(body, "- "), line); err != nil {
				return nil, err
			}
		case section == "provenance" && strings.HasPrefix(body, "- "):
			out.Provenance = append(out.Provenance, Provenance{})
			curProv = &out.Provenance[len(out.Provenance)-1]
			if err := assignProv(curProv, strings.TrimPrefix(body, "- "), line); err != nil {
				return nil, err
			}
		case curRef != nil && indent >= 4:
			if err := assignRefusal(curRef, body, line); err != nil {
				return nil, err
			}
		case curProv != nil && indent >= 4:
			if err := assignProv(curProv, body, line); err != nil {
				return nil, err
			}
		case cur != nil && indent >= 4 && indent < 8 && body == "gate:":
			cur.Gate = &Gate{}
		case cur != nil && cur.Gate != nil && indent >= 6:
			if err := assignGate(cur.Gate, body, line); err != nil {
				return nil, err
			}
		case cur != nil && indent >= 4:
			if err := assign(cur, body, line); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("line %d: unexpected %q", line, body)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for i, r := range out.Rules {
		if r.ID == "" {
			return nil, fmt.Errorf("rule %d has no id", i+1)
		}
	}
	if err := checkDuplicates(out.Rules); err != nil {
		return nil, err
	}
	return out, nil
}

// checkDuplicates refuses two declarations that address the same rule.
//
// Silently letting the second win is how one entry ends up flipping a rule its
// author never read. Two entries sharing an id are fine when each names its own
// source; two that do not are indistinguishable, and the manifest has to say
// which document it means.
func checkDuplicates(rules []Rule) error {
	seen := make(map[string]int, len(rules))
	bare := make(map[string]int, len(rules))
	for i, r := range rules {
		if first, dup := seen[r.Key()]; dup {
			where := ""
			if f := r.SourceFile(); f != "" {
				where = " in " + f
			}
			return fmt.Errorf("rules %d and %d both declare %s%s; one rule, one entry",
				first+1, i+1, r.ID, where)
		}
		seen[r.Key()] = i
		if r.SourceFile() == "" {
			if first, dup := bare[r.ID]; dup {
				return fmt.Errorf("rules %d and %d both declare %s with no source:, "+
					"so nothing says which document each one means", first+1, i+1, r.ID)
			}
			bare[r.ID] = i
		}
	}
	return nil
}

func firstKey(s string) string {
	if i := strings.IndexByte(s, ':'); i > 0 {
		return s[:i]
	}
	return s
}

func assign(r *Rule, kv string, line int) error {
	k, v, ok := split(kv)
	if !ok {
		return fmt.Errorf("line %d: expected key: value, got %q", line, kv)
	}
	switch k {
	case "id":
		r.ID = v
	case "source":
		r.Source = v
	case "statement":
		r.Statement = v
	case "severity":
		r.Severity = v
	case "gate":
		if v == "" {
			r.Gate = &Gate{}
			return nil
		}
		return fmt.Errorf("line %d: gate must be a block, not an inline value", line)
	default:
		return fmt.Errorf("line %d: unknown rule key %q", line, k)
	}
	return nil
}

func assignRefusal(r *Refusal, kv string, line int) error {
	k, v, ok := split(kv)
	if !ok {
		return fmt.Errorf("line %d: expected key: value, got %q", line, kv)
	}
	switch k {
	case "tool":
		r.Tool = v
	case "run":
		r.Run = v
	case "when":
		r.When = v
	case "fixture_path":
		r.FixturePath = v
	case "fixture_body":
		r.FixtureBody = v
	case "empty_dir":
		r.EmptyDir = v == "true"
	case "remove":
		r.Remove = v
	case "expect_exit":
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("line %d: expect_exit must be a number, got %q", line, v)
		}
		r.ExpectExit = n
	case "expect_output":
		r.ExpectOutput = v
	case "needs_git":
		r.NeedsGit = v == "true"
	case "env":
		m, err := splitMap(v)
		if err != nil {
			return fmt.Errorf("line %d: env: %w", line, err)
		}
		r.Env = m
	case "unset_env":
		r.UnsetEnv = splitList(v)
	default:
		return fmt.Errorf("line %d: unknown refusal key %q", line, k)
	}
	return nil
}

func assignProv(p *Provenance, kv string, line int) error {
	k, v, ok := split(kv)
	if !ok {
		return fmt.Errorf("line %d: expected key: value, got %q", line, kv)
	}
	switch k {
	case "artifacts":
		p.Artifacts = v
	case "record":
		p.Record = v
	case "require":
		p.Require = splitList(v)
	case "ignore":
		p.Ignore = splitList(v)
	default:
		return fmt.Errorf("line %d: unknown provenance key %q", line, k)
	}
	return nil
}

// splitMap reads an inline mapping: { PATH: "/usr/bin:/bin", TOKEN: "" }.
//
// Quoted values may contain commas and colons, which a PATH usually does. A key
// with no value is rejected rather than guessed at: "present but empty" and
// "absent" are different conditions, and unset_env is how you say the second.
func splitMap(v string) (map[string]string, error) {
	t := strings.TrimSpace(v)
	if !strings.HasPrefix(t, "{") || !strings.HasSuffix(t, "}") {
		return nil, fmt.Errorf("expected an inline mapping like { PATH: \"/usr/bin:/bin\" }, got %q", v)
	}
	t = strings.TrimSpace(t[1 : len(t)-1])
	if t == "" {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	for _, pair := range splitOutsideQuotes(t, ',') {
		k, val, ok := strings.Cut(pair, ":")
		if !ok {
			return nil, fmt.Errorf("%q is not KEY: VALUE", strings.TrimSpace(pair))
		}
		k = strings.TrimSpace(strings.Trim(strings.TrimSpace(k), `"'`))
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return nil, fmt.Errorf("%q is not a usable variable name", k)
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
			val = unescape(val[1 : len(val)-1])
		}
		out[k] = val
	}
	return out, nil
}

// splitOutsideQuotes splits on sep, ignoring separators inside double quotes, so
// a quoted PATH keeps its commas.
func splitOutsideQuotes(s string, sep byte) []string {
	var out []string
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"' && (i == 0 || s[i-1] != '\\'):
			inQuote = !inQuote
			b.WriteByte(s[i])
		case s[i] == sep && !inQuote:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteByte(s[i])
		}
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}

// splitList reads an inline list: [a, b, c].
func splitList(v string) []string {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(v, "["), "]"))
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(strings.Trim(p, `"'`)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func assignGate(g *Gate, kv string, line int) error {
	k, v, ok := split(kv)
	if !ok {
		return fmt.Errorf("line %d: expected key: value, got %q", line, kv)
	}
	switch k {
	case "kind":
		g.Kind = v
	case "run":
		g.Run = v
	case "proves":
		g.Proves = v
	case "fixture_path":
		g.FixturePath = v
	case "fixture_body":
		g.FixtureBody = v
	case "needs_git":
		g.NeedsGit = v == "true"
	default:
		return fmt.Errorf("line %d: unknown gate key %q", line, k)
	}
	return nil
}

func split(kv string) (key, val string, ok bool) {
	i := strings.IndexByte(kv, ':')
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(kv[:i])
	val = strings.TrimSpace(kv[i+1:])
	// A double-quoted value may carry escapes. Fixture bodies are usually several
	// lines of a deliberately broken file, and a parser that only accepts single
	// lines makes the common case unexpressible.
	if len(val) >= 2 && strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
		return key, unescape(val[1 : len(val)-1]), true
	}
	return key, val, true
}

// unescape handles the small set of sequences a fixture body needs. Anything else
// is left alone rather than guessed at.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Print emits a manifest derived from a reading. It goes to a writer, never to a
// file: adopting verified mode must stay the user's deliberate act.
func Print(w io.Writer, r model.Report) error {
	if _, err := fmt.Fprintf(w, "# %s - generated by `hullcheck --print-manifest`.\n"+
		"# Review every entry. A gate listed here is a claim; add a fixture and run\n"+
		"# `hullcheck --verify` to turn the claim into evidence.\n", Name); err != nil {
		return err
	}
	// An id repeated across documents is not a mistake in the reading - it is
	// what the naming scheme produces - but it is a trap for anyone editing this
	// file, because `source:` is then the only thing telling two entries apart.
	if dup := repeatedIDs(r); len(dup) > 0 {
		if _, err := fmt.Fprintf(w,
			"#\n# NOTE: %d id(s) here appear in more than one policy document: %s.\n"+
				"# `source:` is what tells them apart. Do not remove it, or a declaration\n"+
				"# about one of them will not know which one you meant.\n",
			len(dup), strings.Join(dup, ", ")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, "version: 1\nrules:\n"); err != nil {
		return err
	}
	for _, f := range r.Findings {
		// The file, not "file:line": a line number moves whenever the document
		// is edited, and a declaration that stops matching on an unrelated edit
		// is worse than one that never matched.
		source := f.Rule.File
		if source == "" {
			source = f.Rule.Source
		}
		if _, err := fmt.Fprintf(w, "  - id: %s\n    source: %s\n    statement: %q\n    severity: %s\n",
			f.Rule.ID, source, f.Rule.Statement, f.Rule.Severity); err != nil {
			return err
		}
		if len(f.Gates) == 0 {
			if _, err := fmt.Fprintf(w, "    # BREACH: nothing enforces this. Add a gate block.\n"); err != nil {
				return err
			}
			continue
		}
		g := f.Gates[0]
		run := g.Run
		if run == "" {
			run = g.Name
		}
		if _, err := fmt.Fprintf(w, "    gate:\n      kind: %s\n      run: %q\n"+
			"      # fixture_path: path/that/violates/the/rule\n"+
			"      # fixture_body: \"content that should make the gate fail\"\n",
			g.Kind, run); err != nil {
			return err
		}
	}
	return nil
}

// repeatedIDs names the ids that more than one policy document produces, sorted
// so the generated manifest is byte-stable between runs.
func repeatedIDs(r model.Report) []string {
	seen := make(map[string]map[string]bool)
	for _, f := range r.Findings {
		if seen[f.Rule.ID] == nil {
			seen[f.Rule.ID] = make(map[string]bool)
		}
		seen[f.Rule.ID][f.Rule.File] = true
	}
	var out []string
	for id, files := range seen {
		if len(files) > 1 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
