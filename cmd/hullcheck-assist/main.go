// Command hullcheck-assist drafts the gates a repository is missing.
//
// It ships as a separate binary from hullcheck so the core can keep - and prove -
// that it has no network package in its dependency graph. This one talks to a
// model; the core never does.
//
// The model never touches a number. It drafts, names and explains; every score is
// computed by hullcheck itself.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spaceship-alpha-9/hullcheck"
	"github.com/spaceship-alpha-9/hullcheck/assist"
)

var version = "dev"

const usage = `hullcheck-assist - draft the gates you are missing

usage:
  hullcheck-assist plan [flags] [path]    draft a gate for every unenforced rule
  hullcheck-assist name [flags] [path]    name the rules your unlogged gates enforce

flags:
  --model NAME     use this model
  --base-url URL   OpenAI-compatible endpoint (default: a local server)
  --limit N        draft at most N gates (default 10, 0 for all)
  --version

Finding a model, in order, stopping at the first hit:
  1. --model, or HULLCHECK_MODEL / HULLCHECK_BASE_URL
  2. an API key already in your environment (OPENAI_API_KEY, GROQ_API_KEY, ...)
  3. a local server already running (Ollama, LM Studio, vLLM, llama.cpp)
  4. an offer to start one in Docker - interactive only, never in CI, and never
     without you typing y

Output goes to stdout for you to read and redirect. Nothing is written to your
repository, and no credential is ever stored.
`

func main() { os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr)) }

func execute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if args[0] == "--version" {
		fmt.Fprintln(stdout, "hullcheck-assist "+version)
		return 0
	}
	cmd := args[0]
	if cmd != "plan" && cmd != "name" {
		fmt.Fprint(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet("hullcheck-assist "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("model", "", "model name")
	baseURL := fs.String("base-url", "", "OpenAI-compatible endpoint")
	limit := fs.Int("limit", 10, "draft at most N gates")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}

	rep, err := hullcheck.Read(root)
	if err != nil {
		fmt.Fprintf(stderr, "hullcheck-assist: %v\n", err)
		return 2
	}

	ctx := context.Background()
	d := assist.Discover(ctx, *model, *baseURL, assist.Env, assist.ProbeHTTP)
	for _, s := range d.Steps {
		fmt.Fprintf(stderr, "  %s\n", s)
	}
	if d.Provider == nil {
		fmt.Fprint(stderr, "\n"+assist.DockerOffer+
			"\n  Non-interactive, so nothing was pulled. Supply a model with --model,\n"+
			"  an API key in the environment, or a local server, and run again.\n")
		return 2
	}
	fmt.Fprintf(stderr, "  using %s\n\n", d.Provider)

	switch cmd {
	case "plan":
		err = assist.Plan(ctx, *d.Provider, rep, stdout, *limit)
	case "name":
		err = assist.Name(ctx, *d.Provider, rep, func(rel string) (string, error) {
			b, rerr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) //nolint:gosec
			return string(b), rerr
		}, stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "hullcheck-assist: %v\n", err)
		if errors.Is(err, assist.ErrNoModel) {
			return 2
		}
		return 2
	}
	return 0
}
