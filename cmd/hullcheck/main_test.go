package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The exit-code contract is this tool's public API. These tests are the contract.

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func gated(t *testing.T) string {
	return repo(t, map[string]string{
		"RULES.md":            "1.1 Every asset must record its provenance.\n",
		"check_provenance.sh": "exit 0\n",
	})
}

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = execute(args, &o, &e)
	return code, o.String(), e.String()
}

func TestExitZeroOnAReading(t *testing.T) {
	code, out, _ := run(t, "--no-banner", gated(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "HOLD") {
		t.Errorf("stdout missing a verdict:\n%s", out)
	}
}

func TestExitTwoWhenThereIsNoPolicy(t *testing.T) {
	code, out, errOut := run(t, "--no-banner", t.TempDir())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "UNKNOWN") {
		t.Errorf("a refusal must say UNKNOWN:\n%s", errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("a refusal must not emit a reading on stdout:\n%s", out)
	}
}

func TestExitOneWhenBelowThreshold(t *testing.T) {
	r := repo(t, map[string]string{"RULES.md": "1.1 Secrets must never enter git.\n"})
	code, _, errOut := run(t, "--no-banner", "--fail-under", "50", r)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "below --fail-under") {
		t.Errorf("threshold breach must explain itself:\n%s", errOut)
	}
}

func TestExitZeroWhenThresholdIsMet(t *testing.T) {
	if code, _, _ := run(t, "--no-banner", "--fail-under", "50", gated(t)); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

func TestJSONGoesToStdoutAndIsCleanWhenPiped(t *testing.T) {
	code, out, _ := run(t, "--json", gated(t))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout was not clean JSON (chrome leaked into the data plane): %v\n%s", err, out)
	}
}

func TestBannerNeverReachesStdout(t *testing.T) {
	_, out, _ := run(t, gated(t))
	if strings.Contains(out, "#####") {
		t.Fatal("the banner reached stdout; `hullcheck --json | jq` would break")
	}
}

func TestVersionExitsZero(t *testing.T) {
	code, out, _ := run(t, "--version")
	if code != 0 || !strings.Contains(out, "hullcheck") {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
}

func TestUnknownFlagIsRefusedNotIgnored(t *testing.T) {
	if code, _, _ := run(t, "--nonsense"); code != 2 {
		t.Fatalf("exit = %d, want 2 for an unknown flag", code)
	}
}
