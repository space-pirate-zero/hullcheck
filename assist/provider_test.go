package assist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// noProbe simulates a machine with nothing listening.
func noProbe(context.Context, candidate) (string, bool) { return "", false }

// probeOnly answers for exactly one candidate.
func probeOnly(name, model string) func(context.Context, candidate) (string, bool) {
	return func(_ context.Context, c candidate) (string, bool) {
		if c.name == name {
			return model, true
		}
		return "", false
	}
}

func TestExplicitModelWinsOverEverything(t *testing.T) {
	d := Discover(context.Background(), "my-model", "http://example.invalid",
		env(map[string]string{"OPENAI_API_KEY": "sk-x"}), probeOnly("Ollama", "qwen"))
	if d.Provider == nil || d.Provider.Model != "my-model" {
		t.Fatalf("provider = %+v, want the explicitly named model", d.Provider)
	}
}

func TestEnvAPIKeyBeatsLocalProbe(t *testing.T) {
	d := Discover(context.Background(), "", "",
		env(map[string]string{"GROQ_API_KEY": "gsk-x"}), probeOnly("Ollama", "qwen"))
	if d.Provider == nil || d.Provider.Name != "Groq" {
		t.Fatalf("provider = %+v, want Groq", d.Provider)
	}
	if d.Provider.Local {
		t.Error("a hosted key is not a local provider")
	}
}

func TestLocalServerIsUsedWhenNoKeyIsSet(t *testing.T) {
	d := Discover(context.Background(), "", "", env(nil), probeOnly("Ollama", "qwen2.5-coder:7b"))
	if d.Provider == nil || !d.Provider.Local {
		t.Fatalf("provider = %+v, want a local one", d.Provider)
	}
	if d.Provider.Model != "qwen2.5-coder:7b" {
		t.Errorf("model = %q", d.Provider.Model)
	}
}

func TestNothingFoundYieldsNoProviderAndAnExplanation(t *testing.T) {
	d := Discover(context.Background(), "", "", env(nil), noProbe)
	if d.Provider != nil {
		t.Fatalf("expected no provider, got %+v", d.Provider)
	}
	if len(d.Steps) < 3 {
		t.Fatalf("a refusal must explain what was tried, got %v", d.Steps)
	}
	joined := strings.Join(d.Steps, "|")
	for _, want := range []string{"HULLCHECK_MODEL", "API key", "local model"} {
		if !strings.Contains(joined, want) {
			t.Errorf("explanation missing %q: %v", want, d.Steps)
		}
	}
}

func TestDiscoveryIsDeterministicWhenSeveralServersAnswer(t *testing.T) {
	all := func(context.Context, candidate) (string, bool) { return "m", true }
	first := Discover(context.Background(), "", "", env(nil), all).Provider
	for i := 0; i < 10; i++ {
		got := Discover(context.Background(), "", "", env(nil), all).Provider
		if got.Name != first.Name {
			t.Fatalf("discovery is not deterministic: %s then %s", first.Name, got.Name)
		}
	}
}

func TestProbePrefersACoderModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"id": "llama3.1:8b"}, {"id": "qwen2.5-coder:7b"},
		}})
	}))
	defer srv.Close()
	got, ok := ProbeHTTP(context.Background(), candidate{"test", srv.URL})
	if !ok {
		t.Fatal("probe failed against a live server")
	}
	if got != "qwen2.5-coder:7b" {
		t.Errorf("model = %q, want the coder model", got)
	}
}

func TestProbeIgnoresAnEmptyModelList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()
	if _, ok := ProbeHTTP(context.Background(), candidate{"test", srv.URL}); ok {
		t.Error("a server with no models must not be treated as available")
	}
}

func TestCompleteSendsAndParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth header = %q", got)
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["model"] != "m" {
			t.Errorf("model = %v", in["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": " drafted "}}},
		})
	}))
	defer srv.Close()
	p := Provider{Name: "t", BaseURL: srv.URL, Model: "m", APIKey: "k"}
	got, err := p.Complete(context.Background(), "sys", "usr")
	if err != nil {
		t.Fatal(err)
	}
	if got != "drafted" {
		t.Errorf("content = %q, want trimmed", got)
	}
}

func TestCompleteSurfacesServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	defer srv.Close()
	p := Provider{Name: "t", BaseURL: srv.URL, Model: "m"}
	_, err := p.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want the status surfaced", err)
	}
}
