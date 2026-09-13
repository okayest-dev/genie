package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/llm"
)

// registryFromConfig bridges the parsed [providers.*] config tables into the
// llm registry's spec shape. It lives here rather than in internal/llm
// because of the package layering: config imports modelinfo, which imports
// llm, so llm cannot reference config types without an import cycle.
func registryFromConfig(cfg *config.Config) *llm.Registry {
	specs := make(map[string]llm.ProviderSpec, len(cfg.Providers))
	for name, p := range cfg.Providers {
		specs[name] = llm.ProviderSpec{
			Wire:      p.Wire,
			BaseURL:   p.BaseURL,
			APIKeyEnv: p.APIKeyEnv,
			Model:     p.Model,
			Models:    p.Models,
			Opts:      p.Opts,
		}
	}
	return llm.NewRegistry(specs)
}

// resolveStartup resolves what the harness boots on: the provider named by
// the resolved selection, its client built through the registry, and its
// default model as the first model. There is no global model to fall back on,
// so every model a turn can start on belongs to the active provider — the
// "runModel mismatch" bug cannot recur. An unknown provider or unregistered
// wire is a startup error naming it.
func resolveStartup(reg *llm.Registry, provider string) (llm.Client, string, error) {
	model, err := reg.DefaultModel(provider)
	if err != nil {
		return nil, "", fmt.Errorf("active provider: %w", err)
	}
	client, err := reg.Client(provider)
	if err != nil {
		return nil, "", fmt.Errorf("active provider: %w", err)
	}
	return client, model, nil
}

// selectStartupProvider resolves the provider the harness boots on. An
// explicitly selected provider wins outright. With no selection, an
// interactive run prompts the user to pick from the declared set; a one-shot
// -p run falls back to the first declared provider (the registry's
// deterministic sorted order) and warns on stderr. With no declared providers
// anywhere, startup fails in both modes with an error naming the requirement.
func selectStartupProvider(reg *llm.Registry, selected string, interactive bool, stdin io.Reader, stdout, stderr io.Writer) (string, error) {
	if selected != "" {
		return selected, nil
	}
	names := reg.Names()
	if len(names) == 0 {
		return "", errors.New("no active provider: no declared providers — declare at least one [providers.*] table in config")
	}
	if interactive {
		return promptProviderChoice(names, stdin, stdout)
	}
	first := names[0]
	fmt.Fprintf(stderr, "warning: no provider selected, using first declared provider %q\n", first)
	return first, nil
}

// promptProviderChoice lists the declared providers on stdout and reads a
// selection from stdin — exactly one line, never past it, because the REPL
// reads stdin next. An exact declared name picks it. An unknown name or an
// empty line re-prompts. EOF aborts with an error: no more input can arrive,
// so there is no point asking again.
func promptProviderChoice(names []string, stdin io.Reader, stdout io.Writer) (string, error) {
	for {
		fmt.Fprintln(stdout, "Available providers:")
		for _, name := range names {
			fmt.Fprintf(stdout, "  %s\n", name)
		}
		fmt.Fprint(stdout, "Select provider: ")
		line, readErr := readPromptLine(stdin)
		choice := strings.TrimSpace(line)
		for _, name := range names {
			if name == choice {
				return choice, nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			if choice == "" {
				return "", fmt.Errorf("no provider selected")
			}
			return "", fmt.Errorf("no such provider: %q", choice)
		}
		if choice != "" {
			fmt.Fprintf(stdout, "no such provider: %s\n", choice)
		}
	}
}

// readPromptLine reads one line of input without buffering past it, so the
// stream stays usable by whatever reads next (the REPL). A trailing newline
// is stripped. Clean EOF returns ("", io.EOF); a partial line ending in EOF
// returns the line and io.EOF so callers can still act on it.
func readPromptLine(r io.Reader) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return sb.String(), nil
			}
			sb.WriteByte(buf[0])
		}
		if err != nil {
			return sb.String(), err
		}
	}
}
