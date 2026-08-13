package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/okayest-dev/og/internal/agent"
	"github.com/okayest-dev/og/internal/llm/openai"
)

const (
	defaultModel   = "big-pickle"
	defaultBaseURL = "https://opencode.ai/zen/v1"
)

const usage = `usage: og [-p prompt]

og is a minimal terminal agent harness.

Flags:
  -p prompt   run a single prompt, print the reply to stdout, and exit
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("og", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	prompt := fs.String("p", "", "run a single prompt")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	if *prompt == "" {
		fs.Usage()
		return 3
	}

	client := openai.NewClient(envOr("OG_BASE_URL", defaultBaseURL), os.Getenv("OPENCODE_API_KEY"))
	if err := agent.RunTurn(context.Background(), client, envOr("OG_MODEL", defaultModel), *prompt, stdout); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
