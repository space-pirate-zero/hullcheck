package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The assist binary's contract: it refuses clearly when there is no model, and it
// never invents a number.

func run(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	return execute(args, &o, &e), o.String(), e.String()
}

func repoWithBreach(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "RULES.md"),
		[]byte("1.1 Secrets must never enter git.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// isolate clears every provider signal so discovery genuinely finds nothing.
func isolate(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HULLCHECK_MODEL", "HULLCHECK_BASE_URL", "HULLCHECK_API_KEY",
		"OPENAI_API_KEY", "GROQ_API_KEY", "TOGETHER_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(k, "")
	}
}

func TestNoArgsPrintsUsageAndExitsTwo(t *testing.T) {
	code, _, errOut := run()
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "hullcheck-assist") {
		t.Error("usage should be printed to stderr")
	}
}

func TestUnknownCommandIsRefused(t *testing.T) {
	if code, _, _ := run("invent"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestVersionExitsZero(t *testing.T) {
	code, out, _ := run("--version")
	if code != 0 || !strings.Contains(out, "hullcheck-assist") {
		t.Fatalf("exit = %d out = %q", code, out)
	}
}

func TestRefusesWhenNoModelIsAvailable(t *testing.T) {
	isolate(t)
	// Point discovery at a dead endpoint so no local server can answer.
	t.Setenv("HULLCHECK_BASE_URL", "")
	code, out, errOut := run("plan", repoWithBreach(t))
	if code != 2 {
		t.Skipf("a model is reachable on this machine (exit %d); refusal path not exercised", code)
	}
	if !strings.Contains(errOut, "no model found") {
		t.Errorf("a refusal must explain itself:\n%s", errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("a refusal must not emit drafts on stdout:\n%s", out)
	}
}

func TestPolicylessRepoIsRefusedBeforeAnyModelIsSought(t *testing.T) {
	code, _, errOut := run("plan", t.TempDir())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "no policy documents") {
		t.Errorf("err = %q", errOut)
	}
}

func TestHarvestNeedsAFile(t *testing.T) {
	if code, _, _ := run("harvest"); code != 2 {
		t.Fatalf("exit = %d, want 2 when harvest is given no file", code)
	}
}

func TestPlanAgainstAStubModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": "stub-coder"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{
			{"message": map[string]string{"content": "! git grep -qI 'PRIVATE KEY'"}}}})
	}))
	defer srv.Close()
	isolate(t)
	t.Setenv("HULLCHECK_MODEL", "stub-coder")
	t.Setenv("HULLCHECK_BASE_URL", srv.URL)

	code, out, _ := run("plan", repoWithBreach(t))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "PRIVATE KEY") || !strings.Contains(out, "RULES-1.1") {
		t.Errorf("plan output:\n%s", out)
	}
	if !strings.Contains(out, "Review every one") {
		t.Error("drafts must be labelled proposals")
	}
}

func TestFixtureAgainstAStubModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "s"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{
			{"message": map[string]string{"content": "PATH: leaked.pem\nBODY: REDACTED-KEY-MATERIAL"}}}})
	}))
	defer srv.Close()
	isolate(t)
	t.Setenv("HULLCHECK_MODEL", "s")
	t.Setenv("HULLCHECK_BASE_URL", srv.URL)

	code, out, _ := run("fixture", repoWithBreach(t))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "fixture_path: leaked.pem") {
		t.Errorf("fixture stanza missing:\n%s", out)
	}
}
