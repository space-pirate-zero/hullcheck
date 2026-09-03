// Command hullcheck reads a repository's stated rules and reports which of them
// are actually enforced by something that runs.
//
// Exit codes are the API:
//
//	0  a reading was produced
//	1  --fail-under was breached
//	2  hullcheck refused (see the message)
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"path/filepath"
	"strings"

	"github.com/spaceship-alpha-9/hullcheck/internal/banner"
	"github.com/spaceship-alpha-9/hullcheck/internal/history"
	"github.com/spaceship-alpha-9/hullcheck/internal/manifest"
	"github.com/spaceship-alpha-9/hullcheck/internal/report"
	"github.com/spaceship-alpha-9/hullcheck/internal/scan"
	"github.com/spaceship-alpha-9/hullcheck/internal/verify"
)

// version is stamped at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `hullcheck - check the hull before you trust the air

  Reads the rules your repository states, finds the gates that actually run,
  and reports which rules HOLD, which are a BREACH, and which gates are
  UNLOGGED - running while enforcing nothing anyone wrote down.

usage:
  hullcheck [flags] [path]
  hullcheck diff [--base REF] [path]     rules newly unenforced since REF

flags:
  --json            emit the reading as JSON
  --markdown        emit a PR-comment summary
  --print-manifest  print a .hullcheck.yml derived from this reading, to stdout
  --verify          prove each declared gate fails when its rule is broken
  --since REF       show how coverage moved from REF to now (repeatable trend)
  --base REF        diff mode: treat REF as the baseline (default origin/HEAD)
  --fail-under N    exit 1 if gate coverage is below N percent
  --weighted        judge --fail-under against severity-weighted coverage
  --no-banner       suppress the wordmark
  --version         print version and exit

exit codes:
  0  a reading was produced
  1  --fail-under was breached
  2  refused - see the message

hullcheck reads. It never writes to your repository, and it makes no network
calls of any kind.
`

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

// execute is main() with its edges injected, so the exit-code contract - which is
// this tool's public API - can be tested rather than asserted.
func execute(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "diff" {
		return diffMode(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("hullcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		asJSON    = fs.Bool("json", false, "emit the reading as JSON")
		asMD      = fs.Bool("markdown", false, "emit a PR-comment summary")
		failUnder = fs.Int("fail-under", -1, "exit 1 below this coverage percentage")
		weighted  = fs.Bool("weighted", false, "judge --fail-under against weighted coverage")
		noBanner  = fs.Bool("no-banner", false, "suppress the wordmark")
		showVer   = fs.Bool("version", false, "print version and exit")
		printMan  = fs.Bool("print-manifest", false, "print a manifest to stdout")
		doVerify  = fs.Bool("verify", false, "prove declared gates actually fail")
		since     = fs.String("since", "", "show how coverage moved from this ref")
	)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVer {
		fmt.Fprintln(stdout, "hullcheck "+version)
		return 0
	}

	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}

	// Chrome goes to stderr and only to a terminal, so `hullcheck --json | jq`
	// stays byte-identical with and without a tty.
	banner.Write(stderr, cols(), *noBanner, isTTY(os.Stderr))

	if *since != "" {
		return driftMode(root, *since, stdout, stderr)
	}

	rep, err := scan.Run(root)
	if err != nil {
		if errors.Is(err, scan.ErrNoPolicy) {
			fmt.Fprintf(stderr,
				"hullcheck: UNKNOWN\n\n  %v\n\n"+
					"  Nothing was scored. A repository with no stated rules has no\n"+
					"  denominator, and reporting 100%% would be a lie.\n", err)
			return 2
		}
		fmt.Fprintf(os.Stderr, "hullcheck: %v\n", err)
		return 2
	}

	if *printMan {
		if err := manifest.Print(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "hullcheck: %v\n", err)
			return 2
		}
		return 0
	}

	verified := false
	if *doVerify {
		m, found, err := manifest.Load(filepath.Join(root, manifest.Name))
		if err != nil {
			fmt.Fprintf(stderr, "hullcheck: %s: %v\n", manifest.Name, err)
			return 2
		}
		if !found {
			fmt.Fprintf(stderr,
				"hullcheck: --verify needs %s, and this repository has none.\n\n"+
					"  Create one from the current reading, review it, then verify:\n"+
					"    hullcheck --print-manifest . > %s\n", manifest.Name, manifest.Name)
			return 2
		}
		res, err := verify.Run(m, verify.Options{Root: root, Log: stderr})
		if err != nil {
			fmt.Fprintf(stderr, "hullcheck: verify: %v\n", err)
			return 2
		}
		rep = verify.Apply(rep, res)
		verified = true
	}

	switch {
	case *asJSON:
		if err := report.JSON(stdout, rep, verified); err != nil {
			fmt.Fprintf(stderr, "hullcheck: %v\n", err)
			return 2
		}
	case *asMD:
		report.Markdown(stdout, rep)
	default:
		report.Text(stdout, rep, verified)
	}

	if *failUnder >= 0 {
		cov := rep.Coverage()
		label := "gate coverage"
		if *weighted {
			cov = rep.WeightedCoverage()
			label = "weighted gate coverage"
		}
		if pct := cov * 100; pct < float64(*failUnder) {
			fmt.Fprintf(stderr, "\nhullcheck: %s %.0f%% is below --fail-under %s\n",
				label, pct, strconv.Itoa(*failUnder))
			return 1
		}
	}
	return 0
}

// isTTY reports whether f is a character device. Done with os.Stat rather than a
// terminal library so the core keeps zero third-party dependencies.
func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// cols reads the terminal width from COLUMNS, falling back to a conservative 80.
// An ioctl would be more accurate and would cost a dependency; the banner only
// needs to pick between three width tiers.
func cols() int {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

// driftMode reports coverage at a past ref and now. A snapshot starts an argument;
// a trend ends one.
func driftMode(root, since string, stdout, stderr io.Writer) int {
	pts, err := history.Drift(root, []string{since})
	if err != nil {
		fmt.Fprintf(stderr, "hullcheck: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "HULLCHECK drift\n\n  %-24s %8s %8s %7s\n",
		"ref", "coverage", "rules", "breach")
	for _, p := range pts {
		fmt.Fprintf(stdout, "  %-24s %7.0f%% %8d %7d\n", shortRef(p.Ref), p.Coverage, p.Rules, p.Breaches)
	}
	if len(pts) >= 2 {
		first, last := pts[0], pts[len(pts)-1]
		delta := last.Coverage - first.Coverage
		switch {
		case delta < 0:
			fmt.Fprintf(stdout, "\n  Coverage fell %.0f points since %s.\n", -delta, shortRef(first.Ref))
		case delta > 0:
			fmt.Fprintf(stdout, "\n  Coverage rose %.0f points since %s.\n", delta, shortRef(first.Ref))
		default:
			fmt.Fprintf(stdout, "\n  Coverage is unchanged since %s.\n", shortRef(first.Ref))
		}
	}
	return 0
}

// diffMode is the pull-request gate: adding a rule without adding its gate is the
// one change a living codebase cannot accept. Pre-existing gaps are not the PR's
// fault and do not fail it.
func diffMode(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hullcheck diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "origin/HEAD", "baseline ref")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	added, _, err := history.Diff(root, *base)
	if err != nil {
		fmt.Fprintf(stderr, "hullcheck: %v\n", err)
		return 2
	}
	if len(added) == 0 {
		fmt.Fprintf(stdout, "hullcheck: no rules were left unenforced since %s\n", *base)
		return 0
	}
	fmt.Fprintf(stdout, "hullcheck: %d rule(s) added without a gate since %s\n\n", len(added), *base)
	for _, f := range added {
		fmt.Fprintf(stdout, "  BREACH  %-18s %s\n          %s\n", f.Rule.ID, f.Rule.Statement, f.Rule.Source)
	}
	fmt.Fprint(stdout, "\n  A rule nobody can test is a rule nobody can follow.\n")
	return 1
}

// shortRef abbreviates a full object id so the drift table keeps its columns. Tags
// and branch names are already short and are left alone.
func shortRef(ref string) string {
	if len(ref) == 40 && !strings.ContainsAny(ref, "/ .") {
		return ref[:12]
	}
	return ref
}
