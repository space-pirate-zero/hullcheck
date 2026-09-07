package scratch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seed(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for n, b := range map[string]string{
		"a.txt": "one", "sub/b.txt": "two", ".git/HEAD": "ref", "node_modules/x/y.js": "js",
	} {
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCopyTakesFilesAndSkipsNoise(t *testing.T) {
	d, err := Copy(seed(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, want := range []string{"a.txt", "sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(d.Path, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s missing from the copy", want)
		}
	}
	for _, skip := range []string{".git", "node_modules"} {
		if _, err := os.Stat(filepath.Join(d.Path, skip)); !os.IsNotExist(err) {
			t.Errorf("%s should not be copied", skip)
		}
	}
}

func TestCopyLeavesTheOriginalUntouched(t *testing.T) {
	src := seed(t)
	before, err := os.ReadFile(filepath.Join(src, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Copy(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Write("a.txt", "clobbered"); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove("sub"); err != nil {
		t.Fatal(err)
	}
	d.Close()

	after, err := os.ReadFile(filepath.Join(src, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("writing to the copy changed the original")
	}
	if _, err := os.Stat(filepath.Join(src, "sub")); err != nil {
		t.Fatal("removing from the copy removed from the original")
	}
}

func TestCloseRemovesTheCopy(t *testing.T) {
	d, err := Copy(seed(t))
	if err != nil {
		t.Fatal(err)
	}
	path := d.Path
	d.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Close must remove the scratch directory")
	}
}

func TestWriteAndRemoveRefuseEscapingPaths(t *testing.T) {
	d, err := Copy(seed(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Write("../escaped.txt", "x"); !errors.Is(err, ErrEscapes) {
		t.Errorf("Write escape: err = %v, want ErrEscapes", err)
	}
	if err := d.Remove("../../tmp"); !errors.Is(err, ErrEscapes) {
		t.Errorf("Remove escape: err = %v, want ErrEscapes", err)
	}
}

func TestRunCapturesExitAndOutput(t *testing.T) {
	d, err := Empty()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Run("echo hello; exit 3", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Exit != 3 {
		t.Errorf("exit = %d, want 3", got.Exit)
	}
	if got.Output == "" {
		t.Error("output was not captured")
	}
	if got.TimedOut {
		t.Error("should not report a timeout")
	}
}

// A command that hangs has decided nothing, and must never read as a considered
// failure.
func TestTimeoutIsDistinctFromANonZeroExit(t *testing.T) {
	d, err := Empty()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Run("sleep 30", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !got.TimedOut {
		t.Fatal("a hang must be reported as a timeout, not just a non-zero exit")
	}
}

// git is what makes the copy respect .gitignore. Where it is absent the fallback
// walk is what runs, and these tests would be testing nothing.
func hasGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"add", "-A"},
		{"commit", "-qm", "seed"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// The whole point of issue #1: the files a repository tells git to ignore are
// exactly the ones a naive copy spends its time and disk on.
func TestCopySkipsGitignoredFiles(t *testing.T) {
	hasGit(t)
	root := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "media/\n*.log\n")
	write("kept.go", "package p")
	write("media/huge.bin", strings.Repeat("x", 4096))
	write("noisy.log", "chatter")
	gitInit(t, root)
	// Untracked but not ignored: a file the user has just written is part of the
	// repository as it stands, and a gate under test must see it.
	write("fresh.txt", "new")

	d, err := Copy(root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	for _, want := range []string{"kept.go", "fresh.txt", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(d.Path, want)); err != nil {
			t.Errorf("%s should have been copied", want)
		}
	}
	for _, skip := range []string{"media/huge.bin", "noisy.log"} {
		if _, err := os.Stat(filepath.Join(d.Path, filepath.FromSlash(skip))); !os.IsNotExist(err) {
			t.Errorf("%s is gitignored and must not be copied", skip)
		}
	}
}

// A tree too big to copy is refused with its measurement, and nothing is written.
func TestCopyRefusesATreeOverTheLimit(t *testing.T) {
	root := seed(t)
	_, err := CopyWith(root, Options{MaxBytes: 1})
	var big *TooLargeError
	if !errors.As(err, &big) {
		t.Fatalf("err = %v, want *TooLargeError", err)
	}
	if big.Bytes == 0 || big.Files == 0 {
		t.Errorf("the refusal must carry the measurement, got %+v", big)
	}
	if !strings.Contains(big.Error(), "--max-copy") {
		t.Errorf("the refusal must name the flag that lifts it: %q", big.Error())
	}
}

func TestCopyRefusesTooManyFiles(t *testing.T) {
	_, err := CopyWith(seed(t), Options{MaxFiles: 1})
	var big *TooLargeError
	if !errors.As(err, &big) {
		t.Fatalf("err = %v, want *TooLargeError", err)
	}
	if !strings.Contains(big.Error(), "--max-files") {
		t.Errorf("the refusal must name the flag that lifts it: %q", big.Error())
	}
}

// A negative limit is the deliberate escape hatch, and must not refuse.
func TestNegativeLimitMeansNoLimit(t *testing.T) {
	d, err := CopyWith(seed(t), Options{MaxBytes: -1, MaxFiles: -1})
	if err != nil {
		t.Fatalf("an explicit no-limit copy must proceed: %v", err)
	}
	d.Close()
}

// Nothing may be left behind by a refusal: a half-copy is the failure the limit
// exists to prevent.
func TestARefusedCopyWritesNothing(t *testing.T) {
	into := t.TempDir()
	if _, err := CopyWith(seed(t), Options{Dir: into, MaxBytes: 1}); err == nil {
		t.Fatal("expected a refusal")
	}
	ents, err := os.ReadDir(into)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Errorf("a refused copy left %d entries behind", len(ents))
	}
}

func TestScratchDirPlacesTheCopy(t *testing.T) {
	into := t.TempDir()
	d, err := CopyWith(seed(t), Options{Dir: into})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !strings.HasPrefix(d.Path, into) {
		t.Errorf("copy went to %s, want it under %s", d.Path, into)
	}
	e, err := EmptyWith(Options{Dir: into})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !strings.HasPrefix(e.Path, into) {
		t.Errorf("empty dir went to %s, want it under %s", e.Path, into)
	}
}

func TestMeasureCopiesNothing(t *testing.T) {
	into := t.TempDir()
	bytes, files, _, err := Measure(seed(t), Options{Dir: into, MaxBytes: 1})
	if err != nil {
		t.Fatalf("Measure must report, never refuse: %v", err)
	}
	if bytes == 0 || files == 0 {
		t.Errorf("Measure = %d bytes, %d files; want a real measurement", bytes, files)
	}
	ents, err := os.ReadDir(into)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Errorf("Measure wrote %d entries; it must copy nothing", len(ents))
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"0": 0, "512": 512, "1KB": 1 << 10, "2MiB": 2 << 20,
		"1.5GB": 1610612736, "3 TB": 3 << 40, "10 b": 10,
	} {
		got, err := ParseSize(in)
		if err != nil {
			t.Errorf("ParseSize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "big", "12PB", "-4MB", "MB"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should have failed", bad)
		}
	}
}
