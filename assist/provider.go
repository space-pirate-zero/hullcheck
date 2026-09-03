// Package assist is the optional model layer. It lives outside internal/ and ships
// as a separate binary so the core's "no network package in its dependency graph"
// remains a fact about an artifact rather than a promise about behaviour.
//
// The rule that keeps the product honest: the model never touches the number. It
// drafts, names and explains. Every score is computed by code you can read.
package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNoModel is returned when no model could be found by any route.
var ErrNoModel = errors.New("no model available")

// Provider is a resolved model endpoint. Everything speaks the OpenAI-compatible
// chat-completions shape, which is what Ollama, LM Studio, vLLM, llama.cpp, Groq
// and OpenAI itself all serve - one client covers the field.
type Provider struct {
	Name    string // human label, e.g. "Ollama"
	BaseURL string
	Model   string
	APIKey  string
	// Local is true when the endpoint is on this machine. Local is the path we
	// optimise for: it is free, private, and the repository never leaves the host.
	Local bool
}

func (p Provider) String() string {
	where := "hosted"
	if p.Local {
		where = "local"
	}
	return fmt.Sprintf("%s (%s) %s", p.Name, where, p.Model)
}

// candidate is a local server we know how to look for.
type candidate struct {
	name string
	url  string
}

// LocalCandidates are probed in parallel. All of them serve /v1/models.
var LocalCandidates = []candidate{
	{"Ollama", "http://127.0.0.1:11434"},
	{"LM Studio", "http://127.0.0.1:1234"},
	{"vLLM", "http://127.0.0.1:8000"},
	{"llama.cpp", "http://127.0.0.1:8080"},
	{"text-generation-webui", "http://127.0.0.1:5000"},
}

// hostedKeys are environment variables that, if already set, mean the user has
// chosen a provider. hullcheck reads them; it never writes or persists one.
var hostedKeys = []struct{ env, name, url string }{
	{"OPENAI_API_KEY", "OpenAI", "https://api.openai.com"},
	{"GROQ_API_KEY", "Groq", "https://api.groq.com/openai"},
	{"TOGETHER_API_KEY", "Together", "https://api.together.xyz"},
	{"OPENROUTER_API_KEY", "OpenRouter", "https://openrouter.ai/api"},
}

// Discovery reports what was tried, so a refusal can explain itself.
type Discovery struct {
	Steps    []string
	Provider *Provider
}

// Discover resolves a provider, in a fixed order, stopping at the first hit:
//
//  1. an explicit --model / HULLCHECK_MODEL
//  2. an API key already present in the environment
//  3. a local model server already running
//  4. (caller's job) offer to start one in Docker
//
// It never pulls anything and never writes a credential anywhere.
func Discover(ctx context.Context, explicitModel, explicitURL string, getenv func(string) string,
	probe func(context.Context, candidate) (string, bool)) Discovery {
	var d Discovery

	if explicitModel != "" {
		url := explicitURL
		if url == "" {
			url = getenv("HULLCHECK_BASE_URL")
		}
		if url == "" {
			url = LocalCandidates[0].url
		}
		d.Steps = append(d.Steps, "using the model you named")
		d.Provider = &Provider{Name: "explicit", BaseURL: url, Model: explicitModel,
			APIKey: getenv("HULLCHECK_API_KEY"), Local: isLocal(url)}
		return d
	}
	if m := getenv("HULLCHECK_MODEL"); m != "" {
		url := getenv("HULLCHECK_BASE_URL")
		if url == "" {
			url = LocalCandidates[0].url
		}
		d.Steps = append(d.Steps, "HULLCHECK_MODEL is set")
		d.Provider = &Provider{Name: "env", BaseURL: url, Model: m,
			APIKey: getenv("HULLCHECK_API_KEY"), Local: isLocal(url)}
		return d
	}
	d.Steps = append(d.Steps, "no --model / HULLCHECK_MODEL set")

	for _, h := range hostedKeys {
		if k := getenv(h.env); k != "" {
			d.Steps = append(d.Steps, "found "+h.env+" in the environment")
			model := getenv("HULLCHECK_MODEL")
			if model == "" {
				model = "gpt-4o-mini"
			}
			d.Provider = &Provider{Name: h.name, BaseURL: h.url, Model: model, APIKey: k}
			return d
		}
	}
	d.Steps = append(d.Steps, "no API key in env (OPENAI_API_KEY, GROQ_API_KEY, ...)")

	if p := probeAll(ctx, probe); p != nil {
		d.Steps = append(d.Steps, "found a local model: "+p.String())
		d.Provider = p
		return d
	}
	d.Steps = append(d.Steps, "no local model server responding")
	return d
}

// probeAll asks every candidate at once and takes the first that answers, so a
// dead port costs nothing.
func probeAll(ctx context.Context, probe func(context.Context, candidate) (string, bool)) *Provider {
	type hit struct {
		c     candidate
		model string
	}
	ch := make(chan hit, len(LocalCandidates))
	var wg sync.WaitGroup
	for _, c := range LocalCandidates {
		wg.Add(1)
		go func(c candidate) {
			defer wg.Done()
			if m, ok := probe(ctx, c); ok {
				ch <- hit{c, m}
			}
		}(c)
	}
	wg.Wait()
	close(ch)

	var hits []hit
	for h := range ch {
		hits = append(hits, h)
	}
	if len(hits) == 0 {
		return nil
	}
	// Deterministic: same machine, same answer, every run.
	sort.Slice(hits, func(i, j int) bool { return hits[i].c.name < hits[j].c.name })
	h := hits[0]
	return &Provider{Name: h.c.name, BaseURL: h.c.url, Model: h.model, Local: true}
}

func isLocal(url string) bool {
	return strings.Contains(url, "127.0.0.1") || strings.Contains(url, "localhost")
}

// ProbeHTTP asks a candidate for its model list. 300ms: a local server answers
// instantly, and a closed port must not cost the user a pause.
func ProbeHTTP(ctx context.Context, c candidate) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/v1/models", nil)
	if err != nil {
		return "", false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", false
	}
	if len(body.Data) == 0 {
		return "", false
	}
	// Prefer a coder-class model when the host offers several: this work is
	// reading code and writing small checkers.
	best := body.Data[0].ID
	for _, m := range body.Data {
		if strings.Contains(strings.ToLower(m.ID), "coder") {
			best = m.ID
			break
		}
	}
	return best, true
}

// DockerOffer is what the caller prints when nothing was found. Sizes are stated
// before anything is pulled, and nothing is pulled without an explicit yes.
const DockerOffer = `  no model found.

  I can start one locally in Docker:

      qwen2.5-coder:7b     ~4.7 GB download, ~6 GB RAM     recommended
      qwen2.5-coder:14b    ~9.0 GB download, ~12 GB RAM    better drafts
      llama3.1:8b          ~4.9 GB download, ~8 GB RAM

  This writes to Docker's volume, not to your repo. Nothing is added to
  this project, and ` + "`hullcheck-assist --cleanup`" + ` removes it again.
`

// Complete sends one prompt and returns the reply.
func (p Provider) Complete(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{
		"model": p.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.1,
		"stream":      false,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BaseURL+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %d: %s", p.Name, resp.StatusCode,
			strings.TrimSpace(string(body)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", errors.New("the model returned no choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// Env is the default getenv, split out so tests can supply their own.
func Env(k string) string { return os.Getenv(k) }
