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
	// An out-of-range value must be an error, never an implementation-defined
	// int64 that the limits would read as "no limit".
	for _, bad := range []string{"", "big", "12PB", "-4MB", "MB",
		"99999999999999TB", "0.4"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should have failed", bad)
		}
	}
}

// A gitignored subtree lists nothing, and an empty copy would fail every control
// run for a reason the output could not explain.
func TestAnEmptyGitListingFallsBackToWalking(t *testing.T) {
	hasGit(t)
	root := t.TempDir()
	for _, n := range []string{".gitignore", "inner/thing.txt"} {
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("inner/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInit(t, root)

	inner := filepath.Join(root, "inner")
	d, err := Copy(inner)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := os.Stat(filepath.Join(d.Path, "thing.txt")); err != nil {
		t.Error("a gitignored subtree must still be copied, by walking it")
	}
}

// Nothing under test can see git history unless it asks, and when it asks it must
// actually get it.
func TestIncludeGitCopiesTheRepository(t *testing.T) {
	hasGit(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInit(t, root)

	plain, err := Copy(root)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err := os.Stat(filepath.Join(plain.Path, ".git")); !os.IsNotExist(err) {
		t.Error(".git must stay out of the copy by default")
	}

	withGit, err := CopyWith(root, Options{IncludeGit: true})
	if err != nil {
		t.Fatal(err)
	}
	defer withGit.Close()
	got, err := withGit.Run("git rev-parse --is-inside-work-tree", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Exit != 0 {
		t.Errorf("the copied .git must be a usable repository, got exit %d: %s", got.Exit, got.Output)
	}
}

// Writing a file where a directory stands has one common cause - simulating a
// linked worktree over a copied .git - and it deserves better than a raw
// "is a directory" from the operating system.
func TestWritingOverADirectoryExplainsItself(t *testing.T) {
	d, err := Copy(seed(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	err = d.Write("sub", "x")
	if err == nil {
		t.Fatal("expected an error writing a file over a directory")
	}
	if !strings.Contains(err.Error(), "remove:") {
		t.Errorf("the error must name the way out: %v", err)
	}
}

// A condition the copy cannot hold - a variable gone, a PATH replaced - is the
// one most unattended tools actually refuse on.
func TestRunWithChangesTheEnvironment(t *testing.T) {
	t.Setenv("HC_PROBE", "ambient")
	d, err := Empty()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	got, err := d.Run("echo ${HC_PROBE:-gone}", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Output, "ambient") {
		t.Errorf("Run must inherit the ambient environment, got %q", got.Output)
	}

	got, err = d.RunWith("echo ${HC_PROBE:-gone}", 10*time.Second, Env{Unset: []string{"HC_PROBE"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Output, "gone") {
		t.Errorf("unset must remove the variable, got %q", got.Output)
	}

	got, err = d.RunWith("echo [$HC_PROBE]", 10*time.Second,
		Env{Set: map[string]string{"HC_PROBE": ""}})
	if err != nil {
		t.Fatal(err)
	}
	// Present but empty is a different condition from absent, and the parser
	// keeps them apart, so the runner must too.
	if !strings.Contains(got.Output, "[]") {
		t.Errorf("an empty value must be present and empty, got %q", got.Output)
	}
}

// The shell itself is found on the ambient PATH, so replacing PATH removes what
// the command can reach without making the command unrunnable.
func TestReplacingPathStillRunsTheCommand(t *testing.T) {
	d, err := Empty()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.RunWith("echo still here", 10*time.Second,
		Env{Set: map[string]string{"PATH": "/nonexistent"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Output, "still here") {
		t.Errorf("the shell must still start, got exit %d: %q", got.Exit, got.Output)
	}
}

func TestEnvDescribe(t *testing.T) {
	e := Env{Set: map[string]string{"PATH": "/bin", "AWS_PROFILE": "x"}, Unset: []string{"TOKEN"}}
	if got, want := e.Describe(), "AWS_PROFILE set, PATH set, TOKEN unset"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
	if !(Env{}).Empty() || e.Empty() {
		t.Error("Empty is wrong")
	}
}

// head reads a repository's HEAD commit from outside any scratch copy.
func head(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// linkedWorktree makes a repository with a linked worktree beside it, and returns
// the main repository and the worktree.
func linkedWorktree(t *testing.T) (repo, wt string) {
	t.Helper()
	hasGit(t)
	repo = t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	wt = filepath.Join(t.TempDir(), "wt")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "side", wt)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	return repo, wt
}

// A linked worktree's .git is a file holding an ABSOLUTE gitdir path. Copied
// verbatim it still resolves, so a command under test ran against the real
// repository's index and refs - and could commit to it. The copy must keep the
// fact "this is a worktree" and lose the way back.
func TestWorktreeCopyCannotReachTheRealRepository(t *testing.T) {
	repo, wt := linkedWorktree(t)
	before := head(t, wt)

	d, err := CopyWith(wt, Options{IncludeGit: true})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	st, err := os.Lstat(filepath.Join(d.Path, ".git"))
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("a worktree copy must still have a .git FILE, so a worktree check can fire: %v", err)
	}
	if d.HasGit() {
		t.Error("a withheld pointer is not a repository; HasGit must say so")
	}
	got, err := d.Run("git -c user.email=t@e -c user.name=t commit -q --allow-empty -m escaped", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Exit == 0 {
		t.Errorf("git succeeded inside the copy of a worktree: %s", got.Output)
	}
	if after := head(t, wt); after != before {
		t.Errorf("a command in the copy committed to the real worktree: %s -> %s", before, after)
	}
	if b, _ := exec.Command("git", "-C", repo, "log", "--all", "--oneline").Output(); strings.Contains(string(b), "escaped") {
		t.Error("a command in the copy wrote a commit into the real repository")
	}
}

// With no .git in the copy, git walks up the parent directories. A copy placed
// under a repository (--scratch-dir inside one) must not find it.
func TestCopyDoesNotDiscoverAnEnclosingRepository(t *testing.T) {
	hasGit(t)
	outer := t.TempDir()
	if err := os.WriteFile(filepath.Join(outer, "o.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInit(t, outer)
	d, err := CopyWith(seed(t), Options{Dir: outer})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Run("git rev-parse --show-toplevel", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Exit == 0 {
		t.Errorf("a command in the copy found the enclosing repository: %s", got.Output)
	}
}

// git sets GIT_DIR and GIT_INDEX_FILE for hooks, and a caller may export them.
// Inherited, they point every git command in the copy at the real repository.
func TestAmbientGitLocationIsNotInherited(t *testing.T) {
	repo, _ := linkedWorktree(t)
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", repo)
	d, err := Copy(seed(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Run("git rev-parse --git-dir", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Exit == 0 {
		t.Errorf("GIT_DIR leaked into the copy: %s", got.Output)
	}
	// A manifest that sets one on purpose still gets it.
	got, err = d.RunWith("echo [$GIT_DIR]", 10*time.Second, Env{Set: map[string]string{"GIT_DIR": "declared"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Output, "[declared]") {
		t.Errorf("a declared GIT_DIR must be honoured, got %q", got.Output)
	}
}

// A copied .git DIRECTORY can point out too: core.worktree is an absolute path in
// .git/config, and git in the copy then treats the REAL checkout as its work tree.
func TestCopiedRepositoryDoesNotKeepAnAbsoluteWorkTree(t *testing.T) {
	hasGit(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInit(t, root)
	if out, err := exec.Command("git", "-C", root, "config", "core.worktree", root).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	d, err := CopyWith(root, Options{IncludeGit: true})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Run("git rm -q a.txt", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(root, "a.txt")); serr != nil {
		t.Fatalf("a command in the copy deleted a file in the real checkout (exit %d: %s)", got.Exit, got.Output)
	}
	if _, serr := os.Stat(filepath.Join(d.Path, "a.txt")); !os.IsNotExist(serr) {
		t.Errorf("git in the copy must act on the copy (exit %d: %s)", got.Exit, got.Output)
	}
}

// A manifest that unsets GIT_CEILING_DIRECTORIES on purpose has declared a
// condition, and the wall must not quietly put it back.
func TestDeclaredCeilingUnsetIsHonoured(t *testing.T) {
	d, err := Empty()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.RunWith("echo [${GIT_CEILING_DIRECTORIES-absent}]", 10*time.Second,
		Env{Unset: []string{"GIT_CEILING_DIRECTORIES"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Output, "[absent]") {
		t.Errorf("a declared unset must hold, got %q", got.Output)
	}
}
