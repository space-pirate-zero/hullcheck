// Package match links stated rules to the gates that enforce them.
//
// The matcher is deliberately conservative. The worst failure this tool can have
// is crediting an unrelated gate against a rule, because that inflates coverage
// and manufactures exactly the confidence the tool exists to puncture. When in
// doubt it reports BREACH. A missed link is an understatement a user can correct;
// a false link is a lie they will act on.
package match

import (
	"fmt"
	"sort"
	"strings"

	"github.com/space-pirate-zero/hullcheck/internal/model"
)

// minToken is the shortest word allowed to carry a match. Short words are almost
// never distinctive ("data", "code", "file"), and matching on them is how a
// coverage number becomes fiction.
const minToken = 5

// stopwords never link a rule to a gate. They are either grammatical filler, or
// the vocabulary of gates themselves — every checker mentions "check" and "test",
// so those words carry no information about WHICH rule a gate enforces.
var stopwords = map[string]bool{
	"about": true, "after": true, "again": true, "against": true, "already": true,
	"always": true, "another": true, "avoid": true, "based": true, "because": true,
	"before": true, "being": true, "below": true, "between": true, "cannot": true,
	"could": true, "during": true, "every": true, "except": true, "first": true,
	"forbidden": true, "under": true, "until": true, "using": true, "where": true,
	"which": true, "while": true, "whole": true, "would": true, "never": true,
	"other": true, "prefer": true, "recommended": true, "required": true,
	"shall": true, "should": true, "since": true, "their": true, "there": true,
	"these": true, "thing": true, "those": true, "through": true, "value": true,
	// gate vocabulary — present in nearly every gate, distinctive of none
	"check": true, "checks": true, "verify": true, "verifies": true, "audit": true,
	"lint": true, "linting": true, "tests": true, "testing": true, "validate": true,
	"build": true, "builds": true, "runs": true, "running": true, "script": true,
	"scripts": true, "enforce": true, "enforced": true, "gates": true, "rules": true,
	"policy": true, "policies": true, "repository": true, "project": true,
}

// Run produces a reading: every rule judged, every unmatched gate surfaced.
func Run(rules []model.Rule, gates []model.Gate) model.Report {
	rep := model.Report{Findings: make([]model.Finding, 0, len(rules))}
	claimed := make(map[string]bool, len(gates))

	for _, r := range rules {
		f := model.Finding{Rule: r, Verdict: model.Breach}

		if r.GateHint != "" {
			for _, g := range gates {
				if hintMatches(r.GateHint, g) {
					f.Gates = append(f.Gates, g)
					claimed[g.ID] = true
				}
			}
			if len(f.Gates) > 0 {
				f.Verdict = model.Hold
				f.Why = fmt.Sprintf("the rule names its gate: %q", r.GateHint)
			}
		}

		if f.Verdict != model.Hold {
			toks := distinctive(r.Statement)
			for _, g := range gates {
				if hit, ok := tokenMatch(toks, g); ok {
					f.Gates = append(f.Gates, g)
					claimed[g.ID] = true
					if f.Why == "" {
						f.Why = fmt.Sprintf("%q appears in both the rule and %s", hit, g.File)
					}
				}
			}
			if len(f.Gates) > 0 {
				f.Verdict = model.Hold
			}
		}

		if f.Verdict == model.Breach {
			f.Why = "nothing in the repository references this rule"
		}
		sort.Slice(f.Gates, func(i, j int) bool {
			return f.Gates[i].Stage.Rank() < f.Gates[j].Stage.Rank()
		})
		rep.Findings = append(rep.Findings, f)
	}

	for _, g := range gates {
		if !claimed[g.ID] {
			rep.Unlogged = append(rep.Unlogged, g)
		}
	}
	sort.Slice(rep.Unlogged, func(i, j int) bool { return rep.Unlogged[i].ID < rep.Unlogged[j].ID })
	return rep
}

// hintMatches links a gate a rule named explicitly. This is the strongest signal
// available, because the repository itself wrote it down.
func hintMatches(hint string, g model.Gate) bool {
	h := strings.ToLower(hint)
	if g.Name != "" && len(g.Name) >= 3 && strings.Contains(h, strings.ToLower(g.Name)) {
		return true
	}
	if g.File != "" && strings.Contains(h, strings.ToLower(g.File)) {
		return true
	}
	if g.Run != "" && strings.Contains(strings.ToLower(g.Run), h) {
		return true
	}
	return false
}

// tokenMatch requires a whole distinctive token to appear in the gate's identity.
// Token equality rather than substring: substring matching turns "test" into a
// hit on "latest" and quietly inflates the score.
func tokenMatch(ruleToks []string, g model.Gate) (string, bool) {
	gateToks := tokenSet(g.Name + " " + g.Run + " " + g.File)
	for _, t := range ruleToks {
		if gateToks[t] {
			return t, true
		}
	}
	return "", false
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokenize(s) {
		out[singular(t)] = true
	}
	return out
}

// distinctive reduces a statement to the words that could identify a gate.
func distinctive(stmt string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range tokenize(stmt) {
		// Check the stopword list BEFORE and AFTER singularising. Stripping the
		// plural first lets "always" slip through as "alway" and match anything.
		if stopwords[raw] {
			continue
		}
		t := singular(raw)
		if len(t) < minToken || stopwords[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// tokenize splits on anything that is not a letter or digit, so paths, snake_case
// and kebab-case all decompose into the same words.
func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}

// singular strips a trailing plural "s" so "assets" and "asset" agree. Deliberately
// naive: a real stemmer would create matches we cannot explain to a user.
func singular(t string) string {
	if len(t) > minToken && strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss") {
		return strings.TrimSuffix(t, "s")
	}
	return t
}
