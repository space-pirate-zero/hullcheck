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
	"strings"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
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
}

// Rule is a declared rule and the gate that holds it.
type Rule struct {
	ID        string `json:"id"`
	Source    string `json:"source,omitempty"`
	Statement string `json:"statement"`
	Severity  string `json:"severity,omitempty"`
	Gate      *Gate  `json:"gate,omitempty"`
}

// File is a parsed .hullcheck.yml.
type File struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
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
	inRules := false

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
			inRules = true
		case indent == 0 && strings.HasPrefix(body, "version:"):
			v := strings.TrimSpace(strings.TrimPrefix(body, "version:"))
			if v != "1" {
				return nil, fmt.Errorf("line %d: unsupported manifest version %q (this build understands 1)", line, v)
			}
		case indent == 0:
			return nil, fmt.Errorf("line %d: unknown top-level key %q", line, firstKey(body))
		case inRules && strings.HasPrefix(body, "- "):
			out.Rules = append(out.Rules, Rule{})
			cur = &out.Rules[len(out.Rules)-1]
			if err := assign(cur, strings.TrimPrefix(body, "- "), line); err != nil {
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
	return out, nil
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
	val = strings.TrimSuffix(strings.TrimPrefix(val, `"`), `"`)
	return key, val, true
}

// Print emits a manifest derived from a reading. It goes to a writer, never to a
// file: adopting verified mode must stay the user's deliberate act.
func Print(w io.Writer, r model.Report) error {
	if _, err := fmt.Fprintf(w, "# %s - generated by `hullcheck --print-manifest`.\n"+
		"# Review every entry. A gate listed here is a claim; add a fixture and run\n"+
		"# `hullcheck --verify` to turn the claim into evidence.\nversion: 1\nrules:\n",
		Name); err != nil {
		return err
	}
	for _, f := range r.Findings {
		if _, err := fmt.Fprintf(w, "  - id: %s\n    source: %s\n    statement: %q\n    severity: %s\n",
			f.Rule.ID, f.Rule.Source, f.Rule.Statement, f.Rule.Severity); err != nil {
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
