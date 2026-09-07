// Package verify turns a declared gate into a proven one.
//
// A gate that passes when its rule is broken is worse than no gate at all: it
// manufactures confidence. Proving otherwise requires actually breaking the rule
// and watching the gate fail, so that is what this does.
//
// It never touches the repository being checked. Every fixture is applied inside a
// scratch copy — under the OS temp directory unless --scratch-dir says otherwise —
// which is removed afterwards, so the read-only guarantee survives verified mode.
// A pass copies the repository once per rule, so the copy is bounded and measured
// before it is made; see internal/scratch.
//
// --verify runs commands the manifest declares. That is executing content from the
// repository, by explicit opt-in, and it is documented as such in SECURITY.md.
package verify

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/space-pirate-zero/hullcheck/internal/manifest"
	"github.com/space-pirate-zero/hullcheck/internal/model"
	"github.com/space-pirate-zero/hullcheck/internal/scratch"
)

// DefaultTimeout bounds a single gate run. A gate that hangs is a gate that fails.
const DefaultTimeout = 2 * time.Minute

// Result is the outcome of proving one rule.
type Result struct {
	RuleID string
	// Source is the policy document the declaration named, without any line
	// number. It is half the rule's identity: an id on its own addresses every
	// rule that happens to share it.
	Source  string
	Verdict model.Verdict
	Why     string
}

// Key is how this result addresses a rule, matching model.Rule.Key. A result
// with no source falls back to the bare id, which Apply only honours when
// exactly one rule in the reading carries it.
func (r Result) Key() string {
	if r.Source != "" {
		return r.Source + "#" + r.RuleID
	}
	return r.RuleID
}

// Options configures a verification pass.
type Options struct {
	Root    string
	Timeout time.Duration
	// Log receives progress, because a verify pass is slow and silence reads as a hang.
	Log io.Writer
	// Scratch bounds and places the throwaway copy each proof runs in. A verify
	// pass copies the repository once per rule, so an unbounded copy is a
	// disk-filling surprise rather than a slow answer.
	Scratch scratch.Options
}

// Run proves every rule in the manifest that carries a fixture.
func Run(m *manifest.File, opt Options) ([]Result, error) {
	if opt.Timeout == 0 {
		opt.Timeout = DefaultTimeout
	}
	abs, err := filepath.Abs(opt.Root)
	if err != nil {
		return nil, err
	}

	out := make([]Result, 0, len(m.Rules))
	for _, r := range m.Rules {
		switch {
		case r.Gate == nil || r.Gate.Run == "":
			out = append(out, Result{r.ID, r.SourceFile(), model.Breach,
				"the manifest declares no gate for this rule"})
			continue
		case r.Gate.FixturePath == "":
			// Declared but unprovable. Reported as HOLD, but the wording must not
			// let a reader mistake a claim for evidence.
			out = append(out, Result{r.ID, r.SourceFile(), model.Hold,
				"gate declared but not proven: add fixture_path to prove it fails when the rule is broken"})
			continue
		}
		res, err := proveOne(abs, r, opt)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", r.ID, err)
		}
		out = append(out, res)
		if opt.Log != nil {
			fmt.Fprintf(opt.Log, "  %-10s %s\n", res.Verdict, r.ID)
		}
	}
	return out, nil
}

func proveOne(root string, r manifest.Rule, opt Options) (Result, error) {
	// The scratch copy, the timeout and the escape check all live in
	// internal/scratch so the verifier and the refusal auditor cannot drift
	// apart about what "never writes to your repository" means.
	dir, err := scratch.CopyWith(root, opt.Scratch)
	if err != nil {
		return Result{}, err
	}
	defer dir.Close()

	// CONTROL: run the gate on an unmodified copy first. Without this, a gate
	// that cannot run at all exits non-zero and reads as one that works - exit
	// 127 is indistinguishable from a caught violation. The control is what
	// makes this an experiment rather than a hopeful guess.
	clean, err := dir.Run(r.Gate.Run, opt.Timeout)
	if err != nil {
		return Result{}, err
	}
	if clean.TimedOut {
		return Result{r.ID, r.SourceFile(), model.Broken, fmt.Sprintf(
			"the gate did not finish with the rule intact, so it decides nothing"+
				" - check that %q terminates", r.Gate.Run)}, nil
	}
	if clean.Exit != 0 {
		return Result{r.ID, r.SourceFile(), model.Broken, fmt.Sprintf(
			"the gate fails (exit %d) even with the rule intact, so it discriminates nothing"+
				" - check that %q can run", clean.Exit, r.Gate.Run)}, nil
	}

	// TREATMENT: break the rule.
	body := r.Gate.FixtureBody
	if body == "" {
		body = "hullcheck fixture: this file should make the gate fail\n"
	}
	if err := dir.Write(r.Gate.FixturePath, body); err != nil {
		return Result{}, err
	}

	broken, err := dir.Run(r.Gate.Run, opt.Timeout)
	if err != nil {
		return Result{}, err
	}
	if broken.TimedOut {
		// A hang is not a catch. Reading it as one would manufacture a HOLD out
		// of a gate that never reached a verdict.
		return Result{r.ID, r.SourceFile(), model.Broken, fmt.Sprintf(
			"the gate passed with the rule intact but did not finish once it was broken,"+
				" so it never reached a verdict - check that %q terminates", r.Gate.Run)}, nil
	}
	if broken.Exit != 0 {
		return Result{r.ID, r.SourceFile(), model.Hold, fmt.Sprintf(
			"proven: the gate passed with the rule intact and failed (exit %d) when it was broken",
			broken.Exit)}, nil
	}
	return Result{r.ID, r.SourceFile(), model.Fake,
		"the gate passed even with the rule broken - a patch that is only paint"}, nil
}

// Apply folds verification results into a reading, replacing matched verdicts with
// proven ones. It returns whatever it could not apply, in words, because a
// declaration that quietly reaches nothing is the failure mode this addresses.
//
// A rule is addressed by its source document and its id together. Matching on the
// id alone credits every rule that happens to share it: ids come from the clause
// number and the document's basename, so a repository with two RULES.md files has
// two different rules called RULES-5.3, and one declaration would flip both.
//
// A declaration with no source: is still honoured, but only where exactly one rule
// in the reading carries that id. Where more than one does, nothing is changed and
// the ambiguity is reported - guessing would be the lie.
func Apply(rep model.Report, res []Result) (model.Report, []string) {
	byKey := make(map[string]Result, len(res))
	byID := make(map[string][]Result, len(res))
	for _, r := range res {
		byKey[r.Key()] = r
		if r.Source == "" {
			byID[r.RuleID] = append(byID[r.RuleID], r)
		}
	}

	// How many rules in the reading answer to each bare id, so an unqualified
	// declaration can tell "the only one" from "any of several".
	count := make(map[string]int, len(rep.Findings))
	for _, f := range rep.Findings {
		count[f.Rule.ID]++
	}

	applied := make(map[string]bool, len(res))
	var warn []string
	for i := range rep.Findings {
		rule := rep.Findings[i].Rule
		r, ok := byKey[rule.Key()]
		if !ok {
			if unqualified, has := byID[rule.ID]; has {
				if count[rule.ID] > 1 {
					continue // reported once, below
				}
				r, ok = unqualified[0], true
			}
		}
		if !ok {
			continue
		}
		applied[r.Key()] = true
		rep.Findings[i].Verdict = r.Verdict
		rep.Findings[i].Why = r.Why
	}

	// Everything that reached nothing is said out loud. Requiring a source makes
	// a new way to miss - a typo, a moved document, a leading "./" - and a
	// declaration that quietly applies to nothing is the failure this change is
	// about, not a smaller version of it that is acceptable.
	for _, r := range res {
		if applied[r.Key()] {
			continue
		}
		switch {
		case r.Source == "" && count[r.RuleID] > 1:
			warn = append(warn, fmt.Sprintf(
				"%s: %d rules in this repository carry that id and the declaration names no source:,"+
					" so none of them was changed", r.RuleID, count[r.RuleID]))
		case r.Source != "" && count[r.RuleID] > 0:
			warn = append(warn, fmt.Sprintf(
				"%s: no rule with that id was read from %s, so the declaration changed nothing"+
					" - check the source: path against the reading", r.RuleID, r.Source))
		default:
			warn = append(warn, fmt.Sprintf(
				"%s: the reading contains no rule with that id, so the declaration changed nothing",
				r.RuleID))
		}
	}

	rep.Verified = true
	return rep, warn
}
