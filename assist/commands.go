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

const systemFixture = `You write the smallest possible file whose presence violates a
rule, so a gate can be proven to fail on it.

Output exactly two lines and nothing else:
PATH: <a repo-relative path that would violate the rule>
BODY: <one line of content, or - if an empty file is enough>

No prose, no markdown fences, no explanation.`

const systemExplain = `You are told a rule that nobody has managed to mechanise.
Say in one sentence why it resists automation, then give a testable restatement of
it that a shell command could check. Two short lines, no preamble:
WHY: ...
INSTEAD: ...`

const systemTriage = `You rank unenforced rules by blast radius: how much damage a
violation would do before anyone noticed. Reply with the rule ids only, worst
first, one per line, and nothing else.`

const systemHarvest = `You extract rules from prose. A rule is an obligation someone
could violate and a machine could check. Ignore description, history and rationale.
Output one rule per line, imperative and specific, and nothing else. If the text
states no rules, output exactly: NONE`

// Fixture drafts the violating file that turns a declared gate into a proven one.
// Fixtures are the tedious half of verified mode, and without them --verify never
// gets adopted.
func Fixture(ctx context.Context, p Provider, rep hullcheck.Report, w io.Writer, limit int) error {
	breaches := rep.Breaches()
	if len(breaches) == 0 {
		fmt.Fprintln(w, "# Nothing to draft: every stated rule already has a gate.")
		return nil
	}
	if limit > 0 && len(breaches) > limit {
		fmt.Fprintf(w, "# %d unenforced rules; drafting %d fixtures (--limit).\n", len(breaches), limit)
		breaches = breaches[:limit]
	}
	fmt.Fprintf(w, "# Fixture stanzas drafted by hullcheck-assist using %s.\n"+
		"# Paste into .hullcheck.yml under each rule's gate, then run --verify.\n\n", p.String())
	for _, f := range breaches {
		reply, err := p.Complete(ctx, systemFixture,
			fmt.Sprintf("Rule id: %s\nRule: %s", f.RuleID, f.Statement))
		if err != nil {
			return fmt.Errorf("fixture for %s: %w", f.RuleID, err)
		}
		path, body := parseFixture(reply)
		if path == "" {
			fmt.Fprintf(w, "# %s: the model did not produce a usable fixture\n\n", f.RuleID)
			continue
		}
		fmt.Fprintf(w, "  # %s - %s\n      fixture_path: %s\n", f.RuleID, f.Statement, path)
		if body != "" && body != "-" {
			fmt.Fprintf(w, "      fixture_body: %q\n", body)
		}
		fmt.Fprintln(w)
	}
	return nil
}

func parseFixture(reply string) (path, body string) {
	for _, l := range strings.Split(stripFence(reply), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "PATH:"):
			path = strings.TrimSpace(strings.TrimPrefix(l, "PATH:"))
		case strings.HasPrefix(l, "BODY:"):
			body = strings.TrimSpace(strings.TrimPrefix(l, "BODY:"))
		}
	}
	// A fixture that escapes the repository is refused here as well as in verify.
	if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return "", ""
	}
	return path, body
}

// Explain says why a rule resists mechanisation, and how to restate it so it does
// not. A rule nobody can test is a rule nobody can follow.
func Explain(ctx context.Context, p Provider, rep hullcheck.Report, w io.Writer, limit int) error {
	breaches := rep.Breaches()
	if len(breaches) == 0 {
		fmt.Fprintln(w, "# Nothing to explain: every stated rule already has a gate.")
		return nil
	}
	if limit > 0 && len(breaches) > limit {
		breaches = breaches[:limit]
	}
	fmt.Fprintf(w, "# hullcheck-assist explain, using %s\n\n", p.String())
	for _, f := range breaches {
		reply, err := p.Complete(ctx, systemExplain, f.Statement)
		if err != nil {
			return fmt.Errorf("explaining %s: %w", f.RuleID, err)
		}
		fmt.Fprintf(w, "%s  %s\n%s\n\n", f.RuleID, f.Statement, indent(reply))
	}
	return nil
}

// Triage ranks unenforced rules by likely blast radius, turning a list of gaps
// into an order of work.
func Triage(ctx context.Context, p Provider, rep hullcheck.Report, w io.Writer) error {
	breaches := rep.Breaches()
	if len(breaches) == 0 {
		fmt.Fprintln(w, "# Nothing to triage.")
		return nil
	}
	var sb strings.Builder
	for _, f := range breaches {
		fmt.Fprintf(&sb, "%s (%s): %s\n", f.RuleID, f.Severity, f.Statement)
	}
	reply, err := p.Complete(ctx, systemTriage, sb.String())
	if err != nil {
		return err
	}
	byID := map[string]hullcheck.Finding{}
	for _, f := range breaches {
		byID[f.RuleID] = f
	}
	fmt.Fprintf(w, "# Fix in this order, worst blast radius first. Ranked by %s;\n"+
		"# the severities beside each line are your repository's own.\n\n", p.String())
	n := 0
	for _, l := range strings.Split(reply, "\n") {
		id := strings.TrimSpace(strings.TrimLeft(l, "-*0123456789. "))
		if f, ok := byID[id]; ok {
			n++
			fmt.Fprintf(w, "  %d. %-18s %-7s %s\n", n, f.RuleID, f.Severity, f.Statement)
			delete(byID, id)
		}
	}
	for _, f := range breaches {
		if _, still := byID[f.RuleID]; still {
			n++
			fmt.Fprintf(w, "  %d. %-18s %-7s %s   (unranked)\n", n, f.RuleID, f.Severity, f.Statement)
		}
	}
	return nil
}

// Harvest reads prose the structural scanner could not, and proposes rules from it.
// Structural discovery finds numbered clauses; half of real policy is in paragraphs.
func Harvest(ctx context.Context, p Provider, text, source string, w io.Writer) error {
	reply, err := p.Complete(ctx, systemHarvest, text)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) == "NONE" {
		fmt.Fprintf(w, "# %s: no rules found in this text.\n", source)
		return nil
	}
	fmt.Fprintf(w, "# Candidate rules harvested from %s by %s.\n"+
		"# These are proposals. Add the ones you actually mean to your policy document.\n\n",
		source, p.String())
	for _, l := range strings.Split(reply, "\n") {
		if l = strings.TrimSpace(strings.TrimLeft(l, "-*0123456789. ")); l != "" {
			fmt.Fprintf(w, "- %s\n", l)
		}
	}
	return nil
}

func indent(s string) string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		out = append(out, "  "+strings.TrimSpace(l))
	}
	return strings.Join(out, "\n")
}
