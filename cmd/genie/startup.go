package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/theme"
)

// selectStartupProvider resolves the provider the harness boots on. An
// explicitly selected provider wins outright. With no selection, an
// interactive run prompts the user to pick from the declared set; a one-shot
// -p run falls back to the first declared provider (the registry's
// deterministic sorted order) and warns on stderr. With no declared providers
// anywhere, startup fails in both modes with an error naming the requirement.
func selectStartupProvider(reg *llm.Registry, selected string, interactive bool, stdin io.Reader, stdout, stderr io.Writer, th *theme.Theme, stdoutProfile, stderrProfile *style.RenderProfile) (string, error) {
	if selected != "" {
		return selected, nil
	}
	names := reg.Names()
	if len(names) == 0 {
		return "", errors.New("no active provider: no declared providers — declare at least one [providers.*] table in config")
	}
	if interactive {
		return promptProviderChoice(names, stdin, stdout, th, stdoutProfile)
	}
	first := names[0]
	// warning role for the fallback warning
	warnOut := th.RoleWriter(stderr, stderrProfile, "warning")
	fmt.Fprintf(warnOut, "warning: no provider selected, using first declared provider %q\n", first)
	return first, nil
}

// promptProviderChoice lists the declared providers on stdout and reads a
// selection from stdin — exactly one line, never past it, because the REPL
// reads stdin next. An exact declared name picks it. An unknown name or an
// empty line re-prompts. EOF aborts with an error: no more input can arrive,
// so there is no point asking again.
func promptProviderChoice(names []string, stdin io.Reader, stdout io.Writer, th *theme.Theme, stdoutProfile *style.RenderProfile) (string, error) {
	// pick role for the provider selection dialog
	pickOut := th.RoleWriter(stdout, stdoutProfile, "pick")
	for {
		fmt.Fprintln(pickOut, "Available providers:")
		for _, name := range names {
			fmt.Fprintf(pickOut, "  %s\n", name)
		}
		fmt.Fprint(pickOut, "Select provider: ")
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
			fmt.Fprintf(pickOut, "no such provider: %s\n", choice)
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
