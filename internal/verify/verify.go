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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/space-pirate-zero/hullcheck/internal/manifest"
	"github.com/space-pirate-zero/hullcheck/internal/model"
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
	scratch, err := os.MkdirTemp("", "hullcheck-verify-")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	if err := copyTree(root, scratch); err != nil {
		return Result{}, err
	}

	// CONTROL: run the gate on an unmodified copy first. Without this, a gate
	// that cannot run at all exits non-zero and reads as one that works - exit
	// 127 is indistinguishable from a caught violation. The control is what
	// makes this an experiment rather than a hopeful guess.
	clean, err := runGate(scratch, r.Gate.Run, opt.Timeout)
	if err != nil {
		return Result{}, err
	}
	if clean != 0 {
		return Result{r.ID, model.Broken, fmt.Sprintf(
			"the gate fails (exit %d) even with the rule intact, so it discriminates nothing"+
				" - check that %q can run", clean, r.Gate.Run)}, nil
	}

	// TREATMENT: break the rule.
	fx := filepath.Join(scratch, filepath.FromSlash(r.Gate.FixturePath))
	if !strings.HasPrefix(filepath.Clean(fx), filepath.Clean(scratch)+string(os.PathSeparator)) {
		return Result{}, errors.New("fixture_path escapes the repository root")
	}
	if err := os.MkdirAll(filepath.Dir(fx), 0o750); err != nil {
		return Result{}, err
	}
	body := r.Gate.FixtureBody
	if body == "" {
		body = "hullcheck fixture: this file should make the gate fail\n"
	}
	if err := os.WriteFile(fx, []byte(body), 0o600); err != nil {
		return Result{}, err
	}

	code, err := runGate(scratch, r.Gate.Run, opt.Timeout)
	if err != nil {
		return Result{}, err
	}
	if code != 0 {
		return Result{r.ID, model.Hold, fmt.Sprintf(
			"proven: the gate passed with the rule intact and failed (exit %d) when it was broken",
			code)}, nil
	}
	return Result{r.ID, model.Fake,
		"the gate passed even with the rule broken - a patch that is only paint"}, nil
}

func runGate(dir, run string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", run) //nolint:gosec // the manifest author declared this
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.Env = append(os.Environ(), "HULLCHECK_VERIFY=1")
	err := cmd.Run()
	if ctx.Err() != nil {
		// A gate that hangs has not proven anything. Treat it as a failure to
		// prove rather than as a pass.
		return -1, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// copyTree copies a repository into scratch, skipping the directories that make a
// copy expensive and that no gate should need.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry must not abort the copy
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if skipCopy(d.Name()) {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o750)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, filepath.Join(dst, rel))
	})
}

func skipCopy(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "venv", "dist", "build",
		"target", ".next", "__pycache__", ".terraform":
		return true
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src comes from our own walk
	if err != nil {
		return nil //nolint:nilerr // skip what we cannot read
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return nil //nolint:nilerr
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm()) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
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
