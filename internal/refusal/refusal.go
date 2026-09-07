// Package refusal audits whether unattended tools actually stop when they claim to.
//
// The naive version of this audit greps scripts for `exit 1` and guesses. That
// produces confident-looking nonsense, which is worse than saying nothing.
//
// This is not that. A refusal is a claim - "this tool declines to proceed when X" -
// and a claim is testable. The audit runs each tool TWICE: once normally, and once
// with the condition present. Only a tool that works in the first case and stops,
// in the declared way, in the second has proven anything.
//
// The verdict that matters is PROCEEDS: a tool that claims to stop and does not.
// That is a silent downgrade, and it is the failure the whole project is about.
package refusal

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/space-pirate-zero/hullcheck/internal/manifest"
	"github.com/space-pirate-zero/hullcheck/internal/model"
	"github.com/space-pirate-zero/hullcheck/internal/scratch"
)

// Verdict is the outcome of testing one declared refusal.
type Verdict string

const (
	// Refuses: proven. The tool worked normally and stopped, as declared, under
	// the condition.
	Refuses Verdict = "REFUSES"
	// Proceeds: the tool ran anyway under a condition it claims to refuse. A
	// silent downgrade - the most valuable finding this audit produces.
	Proceeds Verdict = "PROCEEDS"
	// Broken: it failed in the control too, or it failed differently from the way
	// it declared. A crash with the right exit code is not a considered refusal.
	Broken Verdict = "BROKEN"
	// Unprovable: the condition could not be created in the scratch copy, so the
	// tool was never actually put in the situation it claims to refuse.
	//
	// This exists because the alternative is a false PROCEEDS, and PROCEEDS is
	// the finding this whole audit is for. A refusal whose condition is a git
	// fact cannot fire in a copy with no .git: the tool runs happily, and
	// reporting "it ran anyway under a condition it claims to refuse" would send
	// someone to fix a refusal that is correct and load-bearing. A verdict the
	// audit cannot earn must not be emitted as though it had been.
	Unprovable Verdict = "UNPROVABLE"
)

// Result is one audited refusal.
type Result struct {
	Tool    string  `json:"tool"`
	When    string  `json:"when,omitempty"`
	Verdict Verdict `json:"verdict"`
	Why     string  `json:"why"`
}

// Options configures an audit.
type Options struct {
	Root    string
	Timeout time.Duration
	// Scratch bounds and places the throwaway copies. The audit makes two per
	// declared refusal - a control and a treatment - so the limits matter here
	// for the same reason they do in the verifier.
	Scratch scratch.Options
}

// Audit proves every declared refusal. It reports only what it can demonstrate.
func Audit(m *manifest.File, rep model.Report, opt Options) ([]Result, error) {
	_ = rep // kept in the signature: the reading is what a caller already has to hand
	out := make([]Result, 0, len(m.Refusals))
	for _, r := range m.Refusals {
		res, err := prove(r, opt)
		if err != nil {
			return nil, fmt.Errorf("refusal %q: %w", r.Tool, err)
		}
		out = append(out, res)
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Verdict) < rank(out[j].Verdict) })
	return out, nil
}

func rank(v Verdict) int {
	switch v {
	case Proceeds:
		return 0
	case Broken:
		return 1
	case Unprovable:
		return 2
	default:
		return 3
	}
}

// needsGit reports whether a refusal turns on a git fact, from the command it
// runs and from the condition as its author described it. Word-boundary
// matching, so "digit" and "gitignore" do not trigger it.
func needsGit(r manifest.Refusal) bool {
	for _, f := range strings.FieldsFunc(strings.ToLower(r.Run+" "+r.When),
		func(c rune) bool {
			return !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9')
		}) {
		switch f {
		case "git", "worktree", "worktrees":
			return true
		}
	}
	return false
}

// advice names the fix, which differs by why the copy has no repository.
func advice(r manifest.Refusal) string {
	if r.NeedsGit {
		return "needs_git is set, but this directory is not a git work tree, so there was" +
			" no repository to carry into the copy"
	}
	return "add needs_git: true to audit this refusal"
}

func prove(r manifest.Refusal, opt Options) (Result, error) {
	if r.Run == "" {
		return Result{r.Tool, r.When, Broken, "no command declared, so nothing can be proven"}, nil
	}

	sopt := opt.Scratch
	sopt.IncludeGit = r.NeedsGit

	// CONTROL: the tool must work when the condition is absent. Without this, a
	// tool that is simply broken looks exactly like one that refuses correctly.
	ctl, err := scratch.CopyWith(opt.Root, sopt)
	if err != nil {
		return Result{}, err
	}
	defer ctl.Close()

	// A condition that turns on a git fact cannot exist in a copy with no .git.
	// Read that off the copy rather than off the flag: a directory that is not a
	// git work tree has no .git to carry, so needs_git changes nothing there and
	// the condition is just as impossible.
	//
	// Everything below still runs - a tool that refuses anyway has proven it -
	// but PROCEEDS and a failed control are downgraded to UNPROVABLE, because
	// neither of them is a finding about the tool.
	blind := needsGit(r) && !ctl.HasGit()
	base, err := ctl.Run(r.Run, opt.Timeout)
	if err != nil {
		return Result{}, err
	}
	if base.TimedOut {
		return Result{r.Tool, r.When, Broken, "the tool hangs even without the condition present"}, nil
	}
	if base.Exit != 0 {
		if blind {
			return Result{r.Tool, r.When, Unprovable, fmt.Sprintf(
				"the condition is a git fact and the scratch copy has no .git, and the tool"+
					" already fails (exit %d) without it - %s",
				base.Exit, advice(r))}, nil
		}
		return Result{r.Tool, r.When, Broken, fmt.Sprintf(
			"the tool already fails (exit %d) without the condition, so its refusal cannot be told apart from being broken",
			base.Exit)}, nil
	}

	// TREATMENT: create the condition and run again.
	var dir *scratch.Dir
	if r.EmptyDir {
		dir, err = scratch.EmptyWith(sopt)
	} else {
		dir, err = scratch.CopyWith(opt.Root, sopt)
	}
	if err != nil {
		return Result{}, err
	}
	defer dir.Close()
	if r.Remove != "" {
		if rerr := dir.Remove(r.Remove); rerr != nil {
			return Result{}, rerr
		}
	}
	if r.FixturePath != "" {
		if werr := dir.Write(r.FixturePath, r.FixtureBody); werr != nil {
			return Result{}, werr
		}
	}
	got, err := dir.Run(r.Run, opt.Timeout)
	if err != nil {
		return Result{}, err
	}

	switch {
	case got.TimedOut:
		return Result{r.Tool, r.When, Broken, "the tool hung instead of refusing"}, nil
	case got.Exit == 0 && blind:
		// The sharp case. Without this the audit reports its most valuable
		// finding about a refusal that is correct, and someone goes and
		// "fixes" a check that has already prevented a real incident.
		return Result{r.Tool, r.When, Unprovable,
			"the tool ran to success, but the condition is a git fact and the scratch copy" +
				" has no .git, so it was never put in the situation it claims to refuse" +
				" - " + advice(r)}, nil
	case got.Exit == 0:
		return Result{r.Tool, r.When, Proceeds,
			"the tool ran to success under a condition it claims to refuse - a silent downgrade"}, nil
	case got.Exit != r.ExpectExit:
		return Result{r.Tool, r.When, Broken, fmt.Sprintf(
			"it stopped with exit %d but declares %d; a crash is not a considered refusal",
			got.Exit, r.ExpectExit)}, nil
	case r.ExpectOutput != "" && !strings.Contains(got.Output, r.ExpectOutput):
		return Result{r.Tool, r.When, Broken, fmt.Sprintf(
			"exit %d matched but it never said %q, so it may have failed for another reason",
			got.Exit, r.ExpectOutput)}, nil
	}
	return Result{r.Tool, r.When, Refuses, fmt.Sprintf(
		"proven: worked normally, then stopped with exit %d and said so", got.Exit)}, nil
}

// A note on what this audit deliberately does NOT report.
//
// An earlier version also flagged every unattended gate that declared no refusal.
// Run against this repository it flagged `fmt`, `deps` and `network` - assertion
// gates whose entire job is to fail loudly when something is wrong. Telling their
// author to "declare a refusal" is a confident-looking false finding, which is the
// one thing this tool must never produce. If it cannot be proven, it is not
// reported.

// Counts summarises an audit.
func Counts(rs []Result) map[Verdict]int {
	c := map[Verdict]int{Refuses: 0, Proceeds: 0, Broken: 0, Unprovable: 0}
	for _, r := range rs {
		c[r.Verdict]++
	}
	return c
}
