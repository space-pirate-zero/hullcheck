package scratch

import (
	"errors"
	"os"
	"path/filepath"
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
