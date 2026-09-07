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
	"strconv"
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

// maxGateShare is the fraction of gates a token may appear in and still count as
// evidence. A word that turns up in a quarter of every gate in the repository is
// describing the repository, not the rule: "books" matching 43 of 206 gates is
// self-evidently not evidence.
const maxGateShare = 4

// minGatesToJudgeShare is how many gates it takes before that fraction means
// anything. In a repository with three gates, "one in four" is noise.
const minGatesToJudgeShare = 8

// Run produces a reading: every rule judged, every unmatched gate surfaced.
func Run(rules []model.Rule, gates []model.Gate) model.Report {
	rep := model.Report{Findings: make([]model.Finding, 0, len(rules))}
	claimed := make(map[string]bool, len(gates))
	idx := index(gates)

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
			for i, g := range gates {
				if why, ok := idx.match(i, g, toks); ok {
					f.Gates = append(f.Gates, g)
					claimed[g.ID] = true
					if f.Why == "" {
						f.Why = why
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
// available, because the repository itself wrote it down. A path is trusted here
// and nowhere else: naming one in a Gate: annotation is a deliberate act.
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

// corpus is the gates a reading is matched against, indexed by how much each
// part of a gate's identity is worth as evidence.
//
// The parts are not equal. A gate's name and command are what a human chose to
// describe what it does. Its path is where it happens to live, and in a monorepo
// a path is a topic list: books/meatware-nightly/check_brand.py offers "books",
// "meatware", "nightly" and "brand" to any rule that mentions any of them. So the
// directories are dropped outright, the basename counts only when two distinct
// tokens agree, and a token common across the repository's gates counts nowhere.
type corpus struct {
	// strong is the name and command of each gate, by gate index.
	strong []map[string]bool
	// weak is the basename of each gate's file, minus anything already strong.
	weak []map[string]bool
	// common are tokens too widespread among the gates to identify one of them.
	common map[string]bool
}

func index(gates []model.Gate) *corpus {
	c := &corpus{
		strong: make([]map[string]bool, len(gates)),
		weak:   make([]map[string]bool, len(gates)),
		common: map[string]bool{},
	}
	df := map[string]int{}
	for i, g := range gates {
		strong := tokenSet(g.Name + " " + flatten(g.Run))
		weak := map[string]bool{}
		for t := range tokenSet(base(g.File)) {
			if !strong[t] {
				weak[t] = true
			}
		}
		c.strong[i], c.weak[i] = strong, weak
		for t := range strong {
			df[t]++
		}
		for t := range weak {
			df[t]++
		}
	}
	if len(gates) >= minGatesToJudgeShare {
		for t, n := range df {
			if n*maxGateShare > len(gates) {
				c.common[t] = true
			}
		}
	}
	return c
}

// match reports whether a rule's distinctive tokens identify this gate, and the
// evidence in words, so a reader can audit the matcher without reading it.
func (c *corpus) match(i int, g model.Gate, ruleToks []string) (why string, ok bool) {
	for _, t := range ruleToks {
		if c.common[t] || !c.strong[i][t] {
			continue
		}
		where := fmt.Sprintf("the command %q runs", g.Name)
		if tokenSet(g.Name)[t] {
			where = fmt.Sprintf("the gate's name %q", g.Name)
		}
		return fmt.Sprintf("%q appears in both the rule and %s (%s)", t, where, g.File), true
	}
	// The filename alone is weak evidence, so it takes two words agreeing. One
	// shared word with a path is how a coverage number becomes fiction.
	var hits []string
	for _, t := range ruleToks {
		if !c.common[t] && c.weak[i][t] {
			hits = append(hits, strconv.Quote(t))
		}
	}
	if len(hits) >= 2 {
		return fmt.Sprintf("%s appear in both the rule and the filename %s",
			strings.Join(hits, " and "), base(g.File)), true
	}
	return "", false
}

// flatten reduces every path-shaped word in a command to its last element. A
// check script's Run is its own path, so leaving the directories in would let the
// path back in through the command - the same leak by another route. What a
// command does is its program and its flags, not the tree it happens to sit in.
func flatten(cmd string) string {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if strings.ContainsAny(f, "/\\") {
			fields[i] = base(f)
		}
	}
	return strings.Join(fields, " ")
}

// base is the last element of a slash-separated path. Directory names in a
// monorepo are distinctive words that carry no evidence about what a gate checks.
func base(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
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
