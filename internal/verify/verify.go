// Package verify turns a declared gate into a proven one.
//
// A gate that passes when its rule is broken is worse than no gate at all: it
// manufactures confidence. Proving otherwise requires actually breaking the rule
// and watching the gate fail, so that is what this does.
//
// It never touches the repository being checked. Every fixture is applied inside a
// scratch copy under the OS temp directory, which is removed afterwards — the
// read-only guarantee survives verified mode.
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
	RuleID  string
	Verdict model.Verdict
	Why     string
}

// Options configures a verification pass.
type Options struct {
	Root    string
	Timeout time.Duration
	// Log receives progress, because a verify pass is slow and silence reads as a hang.
	Log io.Writer
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
			out = append(out, Result{r.ID, model.Breach,
				"the manifest declares no gate for this rule"})
			continue
		case r.Gate.FixturePath == "":
			// Declared but unprovable. Reported as HOLD, but the wording must not
			// let a reader mistake a claim for evidence.
			out = append(out, Result{r.ID, model.Hold,
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
	dir, err := scratch.Copy(root)
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
		return Result{r.ID, model.Broken, fmt.Sprintf(
			"the gate did not finish with the rule intact, so it decides nothing"+
				" - check that %q terminates", r.Gate.Run)}, nil
	}
	if clean.Exit != 0 {
		return Result{r.ID, model.Broken, fmt.Sprintf(
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
		return Result{r.ID, model.Broken, fmt.Sprintf(
			"the gate passed with the rule intact but did not finish once it was broken,"+
				" so it never reached a verdict - check that %q terminates", r.Gate.Run)}, nil
	}
	if broken.Exit != 0 {
		return Result{r.ID, model.Hold, fmt.Sprintf(
			"proven: the gate passed with the rule intact and failed (exit %d) when it was broken",
			broken.Exit)}, nil
	}
	return Result{r.ID, model.Fake,
		"the gate passed even with the rule broken - a patch that is only paint"}, nil
}

// Apply folds verification results into a reading, replacing matched verdicts with
// proven ones.
func Apply(rep model.Report, res []Result) model.Report {
	by := make(map[string]Result, len(res))
	for _, r := range res {
		by[r.RuleID] = r
	}
	for i := range rep.Findings {
		if r, ok := by[rep.Findings[i].Rule.ID]; ok {
			rep.Findings[i].Verdict = r.Verdict
			rep.Findings[i].Why = r.Why
		}
	}
	rep.Verified = true
	return rep
}
