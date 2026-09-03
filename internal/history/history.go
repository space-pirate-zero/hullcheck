// Package history reads a repository as it was at a past revision.
//
// It uses `git archive`, which is local and read-only. `git worktree add` would be
// the obvious alternative and is disqualified: it writes into .git, which would
// break the guarantee that hullcheck never modifies the repository it reads.
//
// Every git invocation here is a local plumbing command. None of them touch a
// remote, so the no-network property of the core still holds.
package history

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spaceship-alpha-9/hullcheck/internal/model"
	"github.com/spaceship-alpha-9/hullcheck/internal/scan"
)

// Timeout bounds any single git call.
const Timeout = 60 * time.Second

// ErrNotGit is returned when the directory is not a git repository.
var ErrNotGit = errors.New("not a git repository, so there is no history to read")

// Point is a reading at one revision.
type Point struct {
	Ref              string  `json:"ref"`
	Coverage         float64 `json:"gate_coverage"`
	WeightedCoverage float64 `json:"weighted_gate_coverage"`
	Rules            int     `json:"rules"`
	Breaches         int     `json:"breaches"`
}

// IsRepo reports whether root is inside a git work tree.
func IsRepo(root string) bool {
	out, err := git(root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// At reads the repository as it stood at ref.
func At(root, ref string) (model.Report, error) {
	if !IsRepo(root) {
		return model.Report{}, ErrNotGit
	}
	dir, err := os.MkdirTemp("", "hullcheck-at-")
	if err != nil {
		return model.Report{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := extract(root, ref, dir); err != nil {
		return model.Report{}, err
	}
	rep, err := scan.Run(dir)
	if err != nil {
		return model.Report{}, err
	}
	rep.Root = ref
	return rep, nil
}

// extract materialises ref into dir via `git archive | tar -x`.
func extract(root, ref, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	archive := exec.CommandContext(ctx, "git", "-C", root, "archive", "--format=tar", ref) //nolint:gosec
	untar := exec.CommandContext(ctx, "tar", "-x", "-C", dir)                              //nolint:gosec
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	untar.Stdin = pipe
	var stderr bytes.Buffer
	archive.Stderr = &stderr
	if err := untar.Start(); err != nil {
		return err
	}
	if err := archive.Run(); err != nil {
		_ = untar.Wait()
		return fmt.Errorf("git archive %s: %s", ref, strings.TrimSpace(stderr.String()))
	}
	return untar.Wait()
}

// Refs returns up to n recent tags, oldest first. Tags are the honest unit for a
// coverage trend: they are the points a team actually shipped.
func Refs(root string, n int) ([]string, error) {
	if !IsRepo(root) {
		return nil, ErrNotGit
	}
	out, err := git(root, "tag", "--sort=-creatordate")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			tags = append(tags, l)
		}
	}
	if len(tags) > n {
		tags = tags[:n]
	}
	// reverse to oldest-first
	for i, j := 0, len(tags)-1; i < j; i, j = i+1, j-1 {
		tags[i], tags[j] = tags[j], tags[i]
	}
	return tags, nil
}

// Drift reads coverage at each ref, then at the working tree.
func Drift(root string, refs []string) ([]Point, error) {
	out := make([]Point, 0, len(refs)+1)
	for _, ref := range refs {
		rep, err := At(root, ref)
		if err != nil {
			// A tag that predates any policy document is not an error; it is a
			// data point meaning "there was nothing to check yet".
			if errors.Is(err, scan.ErrNoPolicy) {
				out = append(out, Point{Ref: ref})
				continue
			}
			return nil, err
		}
		out = append(out, point(ref, rep))
	}
	rep, err := scan.Run(root)
	if err != nil && !errors.Is(err, scan.ErrNoPolicy) {
		return nil, err
	}
	if err == nil {
		out = append(out, point("working tree", rep))
	}
	return out, nil
}

func point(ref string, rep model.Report) Point {
	return Point{
		Ref: ref, Coverage: rep.Coverage() * 100,
		WeightedCoverage: rep.WeightedCoverage() * 100,
		Rules:            len(rep.Findings), Breaches: rep.Counts()[model.Breach],
	}
}

// Diff compares a base revision with the working tree and returns the rules that
// are newly unenforced. This is the PR gate: adding a rule without adding its gate
// is the one change a living codebase cannot accept.
func Diff(root, base string) (added []model.Finding, rep model.Report, err error) {
	baseRep, err := At(root, base)
	if err != nil && !errors.Is(err, scan.ErrNoPolicy) {
		return nil, model.Report{}, err
	}
	known := map[string]bool{}
	for _, f := range baseRep.Findings {
		if f.Verdict == model.Breach {
			known[f.Rule.ID] = true
		}
	}
	rep, err = scan.Run(root)
	if err != nil {
		return nil, model.Report{}, err
	}
	for _, f := range rep.Findings {
		if f.Verdict == model.Breach && !known[f.Rule.ID] {
			added = append(added, f)
		}
	}
	return added, rep, nil
}

func git(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	full := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // fixed local plumbing
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
