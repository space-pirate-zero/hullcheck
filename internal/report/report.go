// Package report renders a reading for humans and for machines.
//
// Human output goes to stdout as a table; --json emits the same reading as data.
// The two are generated from one model so they can never disagree.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/space-pirate-zero/hullcheck/internal/model"
)

// stages is the Time-to-Truth ladder, fastest first.
var stages = []model.Stage{
	model.PreCommit, model.PullReq, model.Nightly, model.Release, model.Manual,
}

// TTT summarises how long a violation survives before something catches it.
type TTT struct {
	ByStage map[model.Stage]int `json:"by_stage"`
	// Never counts rules with no gate at all. It deliberately does NOT include
	// rules gated only at a manual stage: those are already counted in ByStage,
	// where the row prints its latency as "never" anyway. Adding them here too
	// made the ladder rows sum to more than the rule count and told the reader
	// that gated rules were ungoverned.
	Never int `json:"never"`
	// ManualOnly counts rules whose only gate is a manual stage. Together with
	// Never it is the set of rules nothing detects automatically.
	ManualOnly int `json:"manual_only"`
	P50        int `json:"p50_seconds"`
	P90        int `json:"p90_seconds"`
	// Worst is the slowest observed latency, not p90. The two differ whenever
	// latencies straddle stages, and the tail is the number a reader wants.
	Worst int `json:"worst_seconds"`
	// WorstNever is true when at least one rule has no automatic detection at all.
	WorstNever bool `json:"worst_is_never"`
}

// Timing computes the Time-to-Truth distribution for a reading.
func Timing(r model.Report) TTT {
	t := TTT{ByStage: map[model.Stage]int{}}
	var lat []int
	for _, f := range r.Findings {
		if f.Verdict != model.Hold {
			t.Never++
			t.WorstNever = true
			continue
		}
		st := f.Stage()
		t.ByStage[st]++
		if l := st.Latency(); l >= 0 {
			lat = append(lat, l)
		} else {
			// A gate nothing schedules is not an automatic detection.
			t.ManualOnly++
			t.WorstNever = true
		}
	}
	sort.Ints(lat)
	t.P50 = pct(lat, 0.50)
	t.P90 = pct(lat, 0.90)
	t.Worst = -1
	if len(lat) > 0 {
		t.Worst = lat[len(lat)-1]
	}
	return t
}

// NoAutoDetection is the number of rules nothing catches without a human
// remembering to look: ungoverned rules plus manually-gated ones.
func (t TTT) NoAutoDetection() int { return t.Never + t.ManualOnly }

func pct(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return -1
	}
	i := int(p * float64(len(sorted)-1))
	return sorted[i]
}

// Duration renders seconds the way an engineer says them out loud.
func Duration(sec int) string {
	switch {
	case sec < 0:
		return "never"
	case sec < 90:
		return fmt.Sprintf("%ds", sec)
	case sec < 90*60:
		return fmt.Sprintf("%dm", sec/60)
	case sec < 48*3600:
		return fmt.Sprintf("%dh", sec/3600)
	default:
		return fmt.Sprintf("%dd", sec/86400)
	}
}

// Text writes the human reading.
func Text(w io.Writer, r model.Report, verified bool) {
	c := r.Counts()
	mode := "unverified - no manifest present"
	if verified {
		mode = "verified against .hullcheck.yml"
	}
	fmt.Fprintf(w, "HULLCHECK reading %s  (%s)\n\n", r.Root, mode)
	fmt.Fprintf(w, "  %d rules found in %d policy document%s\n",
		len(r.Findings), len(r.Docs), plural(len(r.Docs)))
	gates := 0
	for _, f := range r.Findings {
		gates += len(f.Gates)
	}
	fmt.Fprintf(w, "  %d gates found in CI, hooks and checker scripts\n\n",
		gates+len(r.Unlogged))

	fmt.Fprintf(w, "  HOLD      %4d   rule has a gate, and something runs\n", c[model.Hold])
	fmt.Fprintf(w, "  BREACH    %4d   stated, nothing runs on it\n", c[model.Breach])
	if c[model.Fake] > 0 {
		fmt.Fprintf(w, "  FAKE      %4d   the gate passes even when its rule is broken\n", c[model.Fake])
	}
	if c[model.Broken] > 0 {
		fmt.Fprintf(w, "  BROKEN    %4d   the gate fails either way; it proves nothing\n", c[model.Broken])
	}
	fmt.Fprintf(w, "  UNLOGGED  %4d   a gate runs, enforcing nothing anyone wrote down\n\n",
		len(r.Unlogged))

	t := Timing(r)
	worst := "never"
	if !t.WorstNever {
		worst = Duration(t.Worst)
	}
	fmt.Fprintf(w, "  TIME-TO-TRUTH        p50 %-6s p90 %-6s worst %s\n",
		Duration(t.P50), Duration(t.P90), worst)
	for _, st := range stages {
		if n := t.ByStage[st]; n > 0 {
			fmt.Fprintf(w, "    %-13s %3d gates   %s\n", st, n, Duration(st.Latency()))
		}
	}
	if t.Never > 0 {
		fmt.Fprintf(w, "    %-13s %3d rules   never\n", "never", t.Never)
	}

	if loud := loudest(r, 3); len(loud) > 0 {
		fmt.Fprintf(w, "\n  Your loudest silences    %-8s %-13s %s\n", "severity", "ungoverned", "rule")
		for _, f := range loud {
			since := r.Since[f.Rule.Key()]
			if since == "" {
				since = "-"
			}
			fmt.Fprintf(w, "    %-20s %-8s %-13s %s\n",
				truncate(f.Rule.ID, 20), f.Rule.Severity, since, truncate(f.Rule.Statement, 40))
		}
	}

	fmt.Fprintf(w, "\n  Gate Coverage  %.0f%%   weighted by severity  %.0f%%\n",
		r.Coverage()*100, r.WeightedCoverage()*100)
	if !verified {
		fmt.Fprint(w, "\n  This is a READING, not a score. It matched rules to gates by name\n"+
			"  and reference; it did not prove any gate fails when its rule is broken.\n"+
			"  Next:  hullcheck --print-manifest > .hullcheck.yml\n")
	}
}

func loudest(r model.Report, n int) []model.Finding {
	var out []model.Finding
	for _, f := range r.Findings {
		if f.Verdict == model.Breach {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Rule.Severity.Weight() > out[j].Rule.Severity.Weight()
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "."
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// JSON writes the reading as data. Stable field order comes from the model.
func JSON(w io.Writer, r model.Report, verified bool) error {
	// Percentages, not fractions. The human output, the Go API and this all say
	// "100" for full coverage; emitting 1.0 here made the GitHub Action publish a
	// coverage of "1" while calling it a percentage.
	type out struct {
		model.Report
		Verified         bool    `json:"verified"`
		Coverage         float64 `json:"gate_coverage"`
		WeightedCoverage float64 `json:"weighted_gate_coverage"`
		TimeToTruth      TTT     `json:"time_to_truth"`
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out{
		Report: r, Verified: verified,
		Coverage: r.Coverage() * 100, WeightedCoverage: r.WeightedCoverage() * 100,
		TimeToTruth: Timing(r),
	})
}

// Markdown writes a PR-comment-shaped summary.
func Markdown(w io.Writer, r model.Report) {
	c := r.Counts()
	fmt.Fprintf(w, "### hullcheck\n\n")
	fmt.Fprintf(w, "**Gate Coverage %.0f%%** (%.0f%% weighted by severity)\n\n",
		r.Coverage()*100, r.WeightedCoverage()*100)
	fmt.Fprintf(w, "| verdict | count |\n|---|---|\n")
	fmt.Fprintf(w, "| HOLD | %d |\n| BREACH | %d |\n", c[model.Hold], c[model.Breach])
	if c[model.Fake] > 0 {
		fmt.Fprintf(w, "| FAKE | %d |\n", c[model.Fake])
	}
	fmt.Fprintf(w, "| UNLOGGED | %d |\n", len(r.Unlogged))
	if loud := loudest(r, 5); len(loud) > 0 {
		fmt.Fprintf(w, "\n**Loudest silences**\n\n")
		for _, f := range loud {
			fmt.Fprintf(w, "- `%s` (%s) — %s\n", f.Rule.ID, f.Rule.Severity, f.Rule.Statement)
		}
	}
}

// Badge renders a self-contained SVG. No external image service, because a badge
// that phones a third party on every README view is a tracking pixel.
func Badge(w io.Writer, r model.Report) error {
	pct := int(r.Coverage()*100 + 0.5)
	// Colour is a judgement, so it is stated plainly: red below half, amber to
	// four fifths, green above.
	fill := "#e05d44"
	switch {
	case pct >= 80:
		fill = "#4c1"
	case pct >= 50:
		fill = "#dfb317"
	}
	label, value := "gate coverage", fmt.Sprintf("%d%%", pct)
	lw, vw := 6*len(label)+20, 7*len(value)+20
	_, err := fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">
<title>%s: %s</title>
<linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>
<g clip-path="url(#r)">
<rect width="%d" height="20" fill="#555"/>
<rect x="%d" width="%d" height="20" fill="%s"/>
<rect width="%d" height="20" fill="url(#s)"/>
</g>
<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%d" y="14">%s</text>
<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%d" y="14">%s</text>
</g></svg>
`, lw+vw, label, value, label, value, lw+vw, lw, lw, vw, fill, lw+vw,
		lw/2, label, lw/2, label, lw+vw/2, value, lw+vw/2, value)
	return err
}
