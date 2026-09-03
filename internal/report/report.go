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
	"strings"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
)

// stages is the Time-to-Truth ladder, fastest first.
var stages = []model.Stage{
	model.PreCommit, model.PullReq, model.Nightly, model.Release, model.Manual,
}

// TTT summarises how long a violation survives before something catches it.
type TTT struct {
	ByStage map[model.Stage]int `json:"by_stage"`
	Never   int                 `json:"never"`
	P50     int                 `json:"p50_seconds"`
	P90     int                 `json:"p90_seconds"`
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
			t.Never++
			t.WorstNever = true
		}
	}
	sort.Ints(lat)
	t.P50 = pct(lat, 0.50)
	t.P90 = pct(lat, 0.90)
	return t
}

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
	fmt.Fprintf(w, "  UNLOGGED  %4d   a gate runs, enforcing nothing anyone wrote down\n\n",
		len(r.Unlogged))

	t := Timing(r)
	worst := "never"
	if !t.WorstNever {
		worst = Duration(t.P90)
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
		fmt.Fprintf(w, "\n  Your loudest silences%s\n", strings.Repeat(" ", 12))
		for _, f := range loud {
			fmt.Fprintf(w, "    %-16s %-8s %s\n",
				truncate(f.Rule.ID, 16), f.Rule.Severity, truncate(f.Rule.Statement, 46))
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
		Coverage: r.Coverage(), WeightedCoverage: r.WeightedCoverage(),
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
