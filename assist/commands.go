package assist

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spaceship-alpha-9/hullcheck"
)

// The system prompt is deliberately narrow. The model is drafting a gate for a
// human to review, not deciding whether a rule is enforced - that judgement stays
// in code the reader can audit.
const systemPlan = `You draft enforcement gates for a repository.
Given a rule that nothing currently enforces, write the smallest possible check
that would fail when the rule is broken.

Rules for your output:
- Emit a shell script, or a single command, and nothing else.
- It must exit non-zero when the rule is violated and zero otherwise.
- Prefer tools that are already common: grep, test, find, git, jq.
- Do not invent files or commands that may not exist.
- If the rule cannot be mechanically checked, reply with exactly:
  UNMECHANISABLE: <one sentence saying why, and what testable restatement would work>
No prose, no markdown fences, no explanation.`

const systemName = `You read a CI job or checker script and state, in one sentence,
the rule it enforces. Write it as a rule someone would put in a policy document:
imperative, specific, and testable. Output only that one sentence.`

// Plan drafts a gate for every rule nothing enforces.
//
// It writes to w and never to the repository: the output is a patch a human reads,
// applies and takes responsibility for.
func Plan(ctx context.Context, p Provider, rep hullcheck.Report, w io.Writer, limit int) error {
	breaches := rep.Breaches()
	if len(breaches) == 0 {
		fmt.Fprintln(w, "# Nothing to draft: every stated rule already has a gate.")
		return nil
	}
	if limit > 0 && len(breaches) > limit {
		fmt.Fprintf(w, "# %d rules are unenforced; drafting the first %d (--limit).\n\n",
			len(breaches), limit)
		breaches = breaches[:limit]
	}
	fmt.Fprintf(w, "# Drafted by hullcheck-assist using %s.\n"+
		"# These are proposals. Review every one before you commit it, and add each\n"+
		"# to .hullcheck.yml with a fixture so --verify can prove it actually fails.\n\n",
		p.String())

	for _, f := range breaches {
		reply, err := p.Complete(ctx, systemPlan, fmt.Sprintf(
			"Rule id: %s\nSeverity: %s\nStated at: %s\nRule: %s", f.RuleID, f.Severity, f.Source, f.Statement))
		if err != nil {
			return fmt.Errorf("drafting %s: %w", f.RuleID, err)
		}
		fmt.Fprintf(w, "# ---- %s  (%s)\n# %s\n", f.RuleID, f.Severity, f.Statement)
		if strings.HasPrefix(reply, "UNMECHANISABLE") {
			fmt.Fprintf(w, "# %s\n\n", reply)
			continue
		}
		fmt.Fprintf(w, "%s\n\n", strings.TrimSpace(stripFence(reply)))
	}
	return nil
}

// Name proposes the rule an unlogged gate is really enforcing, so undocumented
// policy can be written down instead of living only as a red build.
func Name(ctx context.Context, p Provider, rep hullcheck.Report, read func(string) (string, error), w io.Writer) error {
	if len(rep.UnloggedGates) == 0 {
		fmt.Fprintln(w, "# No unlogged gates: everything that runs maps to a stated rule.")
		return nil
	}
	fmt.Fprintf(w, "# Rules proposed by hullcheck-assist using %s, for gates that\n"+
		"# currently enforce nothing anyone wrote down. Review, then add to your\n"+
		"# policy document.\n\n", p.String())

	for _, g := range rep.UnloggedGates {
		file := g
		if i := strings.Index(g, "::"); i > 0 {
			file = g[:i]
		}
		body, err := read(file)
		if err != nil {
			body = "(could not read " + file + ")"
		}
		if len(body) > 4000 {
			body = body[:4000] + "\n...(truncated)"
		}
		reply, err := p.Complete(ctx, systemName, "Gate: "+g+"\n\n"+body)
		if err != nil {
			return fmt.Errorf("naming %s: %w", g, err)
		}
		fmt.Fprintf(w, "# from %s\n- %s\n\n", g, strings.TrimSpace(reply))
	}
	return nil
}

// stripFence removes a markdown code fence a model added despite being asked not to.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(strings.TrimSpace(s), "```")
}
