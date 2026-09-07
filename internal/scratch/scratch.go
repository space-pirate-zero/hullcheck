// Package scratch runs a command against a throwaway copy of a repository.
//
// Both the gate verifier and the refusal auditor need the same thing: break
// something in a copy, run a command, watch what happens - without ever touching
// the repository being examined. Having one implementation is the point; two
// copies of this logic would eventually disagree about what "never writes to your
// repo" means.
//
// A copy is bounded before it is made. The set of files is what git already knows
// about - tracked plus untracked-but-not-ignored - so build output and the media a
// repository deliberately ignores are never copied. What remains is measured, and
// a tree too big to copy is refused with its size rather than half-written into
// the temp directory. Filling a user's disk to answer a question about their gates
// is not a trade this tool gets to make on their behalf.
package scratch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Timeout bounds a single command. Something that hangs has proven nothing.
const Timeout = 2 * time.Minute

// gitTimeout bounds the one git call this package makes. Listing files is fast;
// a git that hangs must not become a hullcheck that hangs.
const gitTimeout = 60 * time.Second

// DefaultMaxBytes is how much a copy may weigh before hullcheck refuses. Two
// gibibytes is generous for the tracked contents of a repository and small enough
// that N rules times N copies cannot quietly fill a disk.
const DefaultMaxBytes int64 = 2 << 30

// DefaultMaxFiles caps the file count independently of the byte count: a million
// tiny files is slow to copy however little it weighs.
const DefaultMaxFiles = 200000

// ErrEscapes is returned when a fixture path would be written outside the copy.
var ErrEscapes = errors.New("fixture path escapes the repository root")

// TooLargeError reports a tree hullcheck declined to copy, with the measurement
// that decided it. A refusal that does not say how big is a refusal the user
// cannot act on.
type TooLargeError struct {
	// Root is the tree that was measured.
	Root string
	// Bytes and Files are what a copy would cost.
	Bytes int64
	Files int
	// MaxBytes and MaxFiles are the limits in force.
	MaxBytes int64
	MaxFiles int
	// Tracked records whether the measurement came from git's idea of the
	// repository or from a plain directory walk, because the remedy differs.
	Tracked bool
}

func (e *TooLargeError) Error() string {
	what := "the working tree here weighs"
	if e.Tracked {
		what = "the files git tracks here weigh"
	}
	over := fmt.Sprintf("%s %s across %d files", what, humanBytes(e.Bytes), e.Files)
	switch {
	case e.MaxFiles > 0 && e.Files > e.MaxFiles:
		return fmt.Sprintf("%s, over the --max-files limit of %d", over, e.MaxFiles)
	default:
		return fmt.Sprintf("%s, over the --max-copy limit of %s", over, humanBytes(e.MaxBytes))
	}
}

// Options bounds and places a scratch copy.
type Options struct {
	// Dir is where the copy is created. Empty means the OS temp directory,
	// which on a small root volume is exactly the wrong place for a large repo.
	Dir string
	// MaxBytes and MaxFiles cap a copy. Zero means the default; a negative
	// value means no limit, which is the escape hatch for someone who has
	// looked at the number and decided it is fine.
	MaxBytes int64
	MaxFiles int
}

func (o Options) maxBytes() int64 {
	if o.MaxBytes == 0 {
		return DefaultMaxBytes
	}
	return o.MaxBytes
}

func (o Options) maxFiles() int {
	if o.MaxFiles == 0 {
		return DefaultMaxFiles
	}
	return o.MaxFiles
}

// Dir is a temporary copy of a repository. Close removes it.
type Dir struct{ Path string }

// Copy makes a throwaway copy of src under the default limits.
func Copy(src string) (*Dir, error) { return CopyWith(src, Options{}) }

// CopyWith makes a throwaway copy of src, bounded by opt.
//
// It copies what git reports as tracked or untracked-but-not-ignored when src is
// a git work tree, and falls back to a directory walk otherwise. Either way the
// set is measured first, and a tree over the limit is refused with a
// *TooLargeError rather than partially copied.
func CopyWith(src string, opt Options) (*Dir, error) {
	files, tracked, err := plan(src, opt)
	if err != nil {
		return nil, err
	}
	dst, err := os.MkdirTemp(opt.Dir, "hullcheck-scratch-")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := copyFile(filepath.Join(src, filepath.FromSlash(f.rel)),
			filepath.Join(dst, filepath.FromSlash(f.rel))); err != nil {
			_ = os.RemoveAll(dst)
			return nil, err
		}
	}
	// Empty directories carry meaning for some tools, but only git-tracked
	// content is copied above, and git does not track them. Restoring the
	// directory shape of a walked tree keeps the fallback faithful.
	if !tracked {
		if err := mirrorDirs(src, dst); err != nil {
			_ = os.RemoveAll(dst)
			return nil, err
		}
	}
	return &Dir{Path: dst}, nil
}

// Measure reports what a copy of src would cost, without copying anything. It is
// what --scratch-plan prints, and what the refusal message is built from.
func Measure(src string, opt Options) (bytes int64, files int, tracked bool, err error) {
	unlimited := opt
	unlimited.MaxBytes, unlimited.MaxFiles = -1, -1
	fs, tracked, err := plan(src, unlimited)
	if err != nil {
		return 0, 0, tracked, err
	}
	for _, f := range fs {
		bytes += f.size
	}
	return bytes, len(fs), tracked, nil
}

// Empty makes a throwaway empty directory, for conditions that mean "given
// nothing to work with".
func Empty() (*Dir, error) { return EmptyWith(Options{}) }

// EmptyWith makes a throwaway empty directory under opt.Dir.
func EmptyWith(opt Options) (*Dir, error) {
	dst, err := os.MkdirTemp(opt.Dir, "hullcheck-empty-")
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

// entry is one file the copy would contain.
type entry struct {
	rel  string
	size int64
}

// plan decides what a copy would contain and refuses if that is too much. Nothing
// is written until the whole set has been measured, so a refusal leaves no
// half-copy behind.
func plan(src string, opt Options) ([]entry, bool, error) {
	files, tracked, err := listTracked(src)
	if err != nil {
		return nil, false, err
	}
	// An empty answer from git is not proof of an empty directory: a subtree
	// that is itself gitignored lists nothing, and copying nothing would fail
	// every gate's control run for a reason the output could not explain. Fall
	// back to the walk and let the size limits deal with the cost.
	if tracked && len(files) == 0 {
		tracked = false
	}
	if !tracked {
		files, err = listWalk(src)
		if err != nil {
			return nil, false, err
		}
	}
	var total int64
	for _, f := range files {
		total += f.size
	}
	maxB, maxF := opt.maxBytes(), opt.maxFiles()
	if (maxB >= 0 && total > maxB) || (maxF >= 0 && len(files) > maxF) {
		return nil, tracked, &TooLargeError{
			Root: src, Bytes: total, Files: len(files),
			MaxBytes: maxB, MaxFiles: maxF, Tracked: tracked,
		}
	}
	return files, tracked, nil
}

// listTracked asks git what belongs to the repository: tracked files plus
// untracked files that .gitignore does not exclude. That is precisely the set a
// gate under test expects to find, and it leaves out the build output and ignored
// media that make a naive copy unusable.
//
// The second return is false when src is not a git work tree, or git is not
// installed, or the call fails for any reason - all of which mean "fall back to
// walking", never "the repository is empty".
func listTracked(src string) ([]entry, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", src, //nolint:gosec // fixed local plumbing
		"ls-files", "-z", "--cached", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, false, nil //nolint:nilerr // not a repo, or no git: walk instead
	}
	seen := make(map[string]bool)
	var files []entry
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if skipPath(name) {
			continue
		}
		st, serr := os.Lstat(filepath.Join(src, filepath.FromSlash(name)))
		if serr != nil || !st.Mode().IsRegular() {
			// Deleted-but-still-indexed files, submodule gitlinks and symlinks
			// all land here. None of them is a file to copy.
			continue
		}
		files = append(files, entry{rel: name, size: st.Size()})
	}
	return files, true, nil
}

// listWalk is the fallback for a directory that is not a git work tree.
func listWalk(src string) ([]entry, error) {
	var files []entry
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
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
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil //nolint:nilerr
		}
		files = append(files, entry{rel: filepath.ToSlash(rel), size: info.Size()})
		return nil
	})
	return files, err
}

// mirrorDirs recreates the directory shape of a walked tree, including the empty
// directories a file list cannot describe.
func mirrorDirs(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil || rel == "." {
			return nil //nolint:nilerr
		}
		if Skip(d.Name()) {
			return filepath.SkipDir
		}
		return os.MkdirAll(filepath.Join(dst, rel), 0o750)
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

// skipPath applies Skip to every element of a slash-separated path, so the
// git-provided list honours the same exclusions as the walk.
func skipPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if Skip(part) {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src comes from our own file list
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

// ParseSize reads a byte count with an optional unit: 512, 100MB, 2GiB. It exists
// so --max-copy can be written the way a human thinks about disk space.
func ParseSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, errors.New("empty size")
	}
	i := 0
	for i < len(t) && (t[i] == '.' || (t[i] >= '0' && t[i] <= '9')) {
		i++
	}
	num, unit := t[:i], strings.ToUpper(strings.TrimSpace(t[i:]))
	if num == "" {
		return 0, fmt.Errorf("%q has no number", s)
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	mult := map[string]int64{
		"": 1, "B": 1,
		"K": 1 << 10, "KB": 1 << 10, "KIB": 1 << 10,
		"M": 1 << 20, "MB": 1 << 20, "MIB": 1 << 20,
		"G": 1 << 30, "GB": 1 << 30, "GIB": 1 << 30,
		"T": 1 << 40, "TB": 1 << 40, "TIB": 1 << 40,
	}
	m, ok := mult[unit]
	if !ok {
		return 0, fmt.Errorf("%q: unknown unit %q (use B, KB, MB, GB or TB)", s, unit)
	}
	if v < 0 {
		return 0, fmt.Errorf("%q is negative", s)
	}
	// Go leaves an out-of-range float-to-int conversion implementation-defined,
	// and on amd64 it produces the most negative int64 - which the limits read as
	// "no limit". A typo must never silently switch the cap off, so the range is
	// checked before the conversion, not after.
	if v > float64(math.MaxInt64)/float64(m) {
		return 0, fmt.Errorf("%q is larger than this machine can count in bytes", s)
	}
	n := int64(v * float64(m))
	if n == 0 && v != 0 {
		return 0, fmt.Errorf("%q rounds to zero bytes, and zero means no limit", s)
	}
	return n, nil
}

// humanBytes renders a byte count the way the refusal message needs to read.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
