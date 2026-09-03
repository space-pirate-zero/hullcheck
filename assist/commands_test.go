package assist_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spaceship-alpha-9/hullcheck"
	"github.com/spaceship-alpha-9/hullcheck/assist"
)

// stub is a model that replies with whatever it is told to.
func stub(t *testing.T, reply string) assist.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))
	t.Cleanup(srv.Close)
	return assist.Provider{Name: "stub", BaseURL: srv.URL, Model: "stub-1", Local: true}
}

func readingWithBreach(t *testing.T) hullcheck.Report {
	t.Helper()
	root := t.TempDir()
	write(t, root, "RULES.md", "1.1 Secrets must never enter git.\n")
	rep, err := hullcheck.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlanDraftsAGateForEachBreach(t *testing.T) {
	var b strings.Builder
	err := assist.Plan(context.Background(), stub(t, "! git grep -qI 'PRIVATE KEY'"),
		readingWithBreach(t), &b, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "RULES-1.1") {
		t.Errorf("draft must name the rule:\n%s", out)
	}
	if !strings.Contains(out, "PRIVATE KEY") {
		t.Errorf("draft must contain the model's gate:\n%s", out)
	}
	if !strings.Contains(out, "Review every one") {
		t.Error("a draft must be labelled a proposal, not applied advice")
	}
}

func TestPlanStripsMarkdownFences(t *testing.T) {
	var b strings.Builder
	if err := assist.Plan(context.Background(), stub(t, "```sh\ngrep -q x file\n```"),
		readingWithBreach(t), &b, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "```") {
		t.Errorf("fences must be stripped so the output is pasteable:\n%s", b.String())
	}
}

func TestPlanPassesThroughAnUnmechanisableVerdict(t *testing.T) {
	var b strings.Builder
	if err := assist.Plan(context.Background(),
		stub(t, "UNMECHANISABLE: taste cannot be asserted in a shell."),
		readingWithBreach(t), &b, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "UNMECHANISABLE") {
		t.Errorf("an honest refusal from the model must survive:\n%s", b.String())
	}
}

func TestPlanSaysSoWhenThereIsNothingToDo(t *testing.T) {
	root := t.TempDir()
	write(t, root, "RULES.md", "1.1 Every asset must record its provenance.\n")
	write(t, root, "check_provenance.sh", "exit 0\n")
	rep, err := hullcheck.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := assist.Plan(context.Background(), stub(t, "x"), rep, &b, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Nothing to draft") {
		t.Errorf("got:\n%s", b.String())
	}
}

func TestPlanHonoursTheLimit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "RULES.md",
		"1.1 Secrets must never enter git.\n1.2 Tenants must never share data.\n1.3 Builds must never be unsigned.\n")
	rep, err := hullcheck.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := assist.Plan(context.Background(), stub(t, "true"), rep, &b, 1); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), "# ---- "); n != 1 {
		t.Errorf("drafted %d gates, want 1", n)
	}
	if !strings.Contains(b.String(), "--limit") {
		t.Error("truncation must be disclosed, not silent")
	}
}

func TestNameProposesRulesForUnloggedGates(t *testing.T) {
	root := t.TempDir()
	write(t, root, "RULES.md", "1.1 Secrets must never enter git.\n")
	write(t, root, "check_licences.sh", "exit 0\n")
	rep, err := hullcheck.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.UnloggedGates) == 0 {
		t.Fatal("fixture should produce an unlogged gate")
	}
	var b strings.Builder
	err = assist.Name(context.Background(), stub(t, "Every dependency must carry a permissive licence."),
		rep, func(string) (string, error) { return "exit 0", nil }, &b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "permissive licence") {
		t.Errorf("got:\n%s", b.String())
	}
}

func TestFixtureRefusesAPathThatEscapesTheRepo(t *testing.T) {
	var b strings.Builder
	err := assist.Fixture(context.Background(),
		stub(t, "PATH: ../../etc/passwd\nBODY: x"), readingWithBreach(t), &b, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "etc/passwd") {
		t.Errorf("an escaping fixture path must be refused here as well as in verify:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "did not produce a usable fixture") {
		t.Errorf("the refusal must be visible:\n%s", b.String())
	}
}

func TestTriageKeepsEveryRuleEvenWhenTheModelDropsOne(t *testing.T) {
	root := t.TempDir()
	write(t, root, "RULES.md", "1.1 Secrets must never enter git.\n1.2 Tenants must never share data.\n")
	rep, err := hullcheck.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	// The model ranks only one of the two.
	if err := assist.Triage(context.Background(), stub(t, "RULES-1.2"), rep, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "RULES-1.1") || !strings.Contains(out, "RULES-1.2") {
		t.Errorf("triage silently dropped a rule:\n%s", out)
	}
	if !strings.Contains(out, "unranked") {
		t.Error("a rule the model ignored must be shown as unranked, not omitted")
	}
}

func TestHarvestReportsNoneHonestly(t *testing.T) {
	var b strings.Builder
	if err := assist.Harvest(context.Background(), stub(t, "NONE"), "some prose", "notes.md", &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "no rules found") {
		t.Errorf("got:\n%s", b.String())
	}
}
