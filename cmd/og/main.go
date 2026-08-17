package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/okayest-dev/og/internal/agent"
	"github.com/okayest-dev/og/internal/config"
	"github.com/okayest-dev/og/internal/instruct"
	"github.com/okayest-dev/og/internal/llm/openai"
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

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	instruction, err := instruct.Load(cfg, cwd)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	client := openai.NewClient(cfg.BaseURL, cfg.APIKey)
	if err := agent.RunTurn(context.Background(), client, cfg.Model, instruction, *prompt, stdout); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}
