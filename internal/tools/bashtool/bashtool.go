// Package bashtool implements the bash tool: shell command execution with
// confirm gate, timeout, output truncation with spill files, and merged
// stdout+stderr.
package bashtool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
)

const (
	maxOutputBytes = 1 << 20 // 1 MB — output beyond this is truncated with a spill file
)

// Tool executes shell commands. Permissions are governed by the deny-point
// escalation gate (net + run axes), not a local Confirmer.
type Tool struct {
	cwd     string
	timeout time.Duration
}

// New creates a bash tool rooted at cwd. timeout controls the per-command
// kill deadline (0 means no timeout).
func New(cwd string, timeout time.Duration) *Tool {
	return &Tool{cwd: cwd, timeout: timeout}
}

func (t *Tool) Name() string        { return "bash" }
func (t *Tool) Description() string { return "Run a shell command. Merges stdout and stderr." }

func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute",
			},
		},
		"required": []any{"command"},
	}
}

type bashArgs struct {
	Command string `json:"command"`
}

// execRe matches the leading executable of a shell command: the first word
// before any whitespace or shell metacharacter.
var execRe = regexp.MustCompile(`^\s*([^\s|&;()<>]+)`)

// urlRe matches the host[:port] part of an http/https URL, excluding shell
// metacharacters, quotes, and the path.
var urlRe = regexp.MustCompile(`https?://([^\s/"'()<>|&;]+)`)

// RequiredPermissions extracts permission requirements from the shell command.
// Best-effort extraction:
// - run axis: the leading executable (first word before space, pipe, redirect, &&, ||, ;)
// - net axis: host[:port] from any URL in the command (http://, https://)
// When a scope cannot be determined, the requirement is axis-only (blanket).
func (t *Tool) RequiredPermissions(raw json.RawMessage) ([]tools.Requirement, error) {
	var args bashArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}
	if args.Command == "" {
		return nil, fmt.Errorf("missing required argument: command")
	}

	var reqs []tools.Requirement

	// Extract run axis: leading executable
	if exe := extractExecutable(args.Command); exe != "" {
		reqs = append(reqs, tools.Requirement{Axis: "run", Scope: exe})
	} else {
		reqs = append(reqs, tools.Requirement{Axis: "run", Scope: ""})
	}

	// Extract net axis: hosts from URLs
	hosts := extractURLHosts(args.Command)
	for _, h := range hosts {
		reqs = append(reqs, tools.Requirement{Axis: "net", Scope: h})
	}

	return reqs, nil
}

// extractExecutable returns the leading executable from a shell command.
// It handles simple commands, pipelines, redirects, and compound commands.
func extractExecutable(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// Split on shell metacharacters to get the first command
	// Metacharacters: | & ; ( ) < > \n
	// We look for the first word before any of these
	m := execRe.FindStringSubmatch(cmd)
	if len(m) < 2 {
		return ""
	}
	first := m[1]
	// If it's a path, take the basename; otherwise keep as-is
	if strings.Contains(first, "/") {
		return filepath.Base(first)
	}
	return first
}

// extractURLHosts finds host[:port] from http/https URLs in the command string.
func extractURLHosts(cmd string) []string {
	var hosts []string
	// Match http:// or https:// URLs
	matches := urlRe.FindAllStringSubmatch(cmd, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		// Parse to extract host:port cleanly
		u, err := url.Parse("http://" + m[1]) // scheme doesn't matter for host extraction
		if err != nil {
			continue
		}
		host := u.Host
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func (t *Tool) Execute(raw json.RawMessage) (string, error) {
	var args bashArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if args.Command == "" {
		return "", fmt.Errorf("missing required argument: command")
	}

	cmd := exec.Command("sh", "-c", args.Command)
	cmd.Dir = t.cwd
	// Create a new process group so we can kill the entire tree on timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Merge stdout+stderr into a single buffer.
	var out []byte
	var err error
	done := make(chan struct{})
	if t.timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
		defer cancel()
		go func() {
			out, err = cmd.CombinedOutput()
			close(done)
		}()
		select {
		case <-done:
			// Command finished before timeout.
		case <-ctx.Done():
			// Kill the entire process group.
			if cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			<-done
			return string(out), fmt.Errorf("command timed out")
		}
	} else {
		out, err = cmd.CombinedOutput()
	}

	// Truncate if output exceeds the cap.
	output := string(out)
	if len(output) > maxOutputBytes {
		spillPath, spillErr := t.writeSpill(output)
		if spillErr != nil {
			return "", fmt.Errorf("truncate output: %v", spillErr)
		}
		output = output[:maxOutputBytes] + fmt.Sprintf("\n\n[truncated — full output spilled to %s]", spillPath)
	}

	// Handle non-zero exit.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return output, fmt.Errorf("exit code %d", exitErr.ExitCode())
		}
		return output, fmt.Errorf("command failed: %v", err)
	}

	return output, nil
}

// writeSpill writes full output to a temp file and returns its path.
func (t *Tool) writeSpill(output string) (string, error) {
	dir := filepath.Join(t.cwd, ".genie-spill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "bash-output-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(output); err != nil {
		return "", err
	}
	return f.Name(), nil
}
