// Package scratch runs a command against a throwaway copy of a repository.
//
// Both the gate verifier and the refusal auditor need the same thing: break
// something in a copy, run a command, watch what happens - without ever touching
// the repository being examined. Having one implementation is the point; two
// copies of this logic would eventually disagree about what "never writes to your
// repo" means.
package scratch

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Timeout bounds a single command. Something that hangs has proven nothing.
const Timeout = 2 * time.Minute

// ErrEscapes is returned when a fixture path would be written outside the copy.
var ErrEscapes = errors.New("fixture path escapes the repository root")

// Dir is a temporary copy of a repository. Close removes it.
type Dir struct{ Path string }

// Copy makes a throwaway copy of src, skipping the directories that make a copy
// expensive and that no command under test should need.
func Copy(src string) (*Dir, error) {
	dst, err := os.MkdirTemp("", "hullcheck-scratch-")
	if err != nil {
		return nil, err
	}
	if err := copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return nil, err
	}
	return &Dir{Path: dst}, nil
}

// Empty makes a throwaway empty directory, for conditions that mean "given
// nothing to work with".
func Empty() (*Dir, error) {
	dst, err := os.MkdirTemp("", "hullcheck-empty-")
	if err != nil {
		return nil, err
	}
	return &Dir{Path: dst}, nil
}

// Close removes the copy.
func (d *Dir) Close() {
	if d != nil && d.Path != "" {
		_ = os.RemoveAll(d.Path)
	}
}

// Write places a file inside the copy, refusing paths that escape it.
func (d *Dir) Write(rel, body string) error {
	p := filepath.Join(d.Path, filepath.FromSlash(rel))
	if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(d.Path)+string(os.PathSeparator)) {
		return ErrEscapes
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(body), 0o600)
}

// Remove deletes a path inside the copy, refusing paths that escape it.
func (d *Dir) Remove(rel string) error {
	p := filepath.Join(d.Path, filepath.FromSlash(rel))
	if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(d.Path)+string(os.PathSeparator)) {
		return ErrEscapes
	}
	return os.RemoveAll(p)
}

// Result is what a command did.
type Result struct {
	Exit   int
	Output string
	// TimedOut is separate from a non-zero exit: a command that hangs has not
	// decided anything, and must never be read as a considered failure.
	TimedOut bool
}

// Run executes a command inside the copy and captures its combined output.
func (d *Dir) Run(command string, timeout time.Duration) (Result, error) {
	if timeout == 0 {
		timeout = Timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // declared by the manifest author
	cmd.Dir = d.Path
	cmd.Env = append(os.Environ(), "HULLCHECK=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return Result{Exit: -1, Output: string(out), TimedOut: true}, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return Result{Exit: ee.ExitCode(), Output: string(out)}, nil
	}
	if err != nil {
		return Result{Exit: -1, Output: string(out)}, err
	}
	return Result{Exit: 0, Output: string(out)}, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry must not abort the copy
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil || rel == "." {
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			if Skip(d.Name()) {
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

// Skip reports directories not worth copying or walking.
func Skip(name string) bool {
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
