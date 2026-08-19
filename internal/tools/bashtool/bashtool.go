// Package bashtool implements the bash tool: shell command execution with
// confirm gate, timeout, output truncation with spill files, and merged
// stdout+stderr.
package bashtool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/okayest-dev/og/internal/tools"
)

const (
	maxOutputBytes = 1 << 20 // 1 MB — output beyond this is truncated with a spill file
)

// Tool executes shell commands.
type Tool struct {
	cwd      string
	confirmer tools.Confirmer
	timeout  time.Duration
}

// New creates a bash tool rooted at cwd. Commands go through confirmer;
// timeout controls the per-command kill deadline (0 means no timeout).
func New(cwd string, confirmer tools.Confirmer, timeout time.Duration) *Tool {
	return &Tool{cwd: cwd, confirmer: confirmer, timeout: timeout}
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

func (t *Tool) Execute(raw json.RawMessage) (string, error) {
	var args bashArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if args.Command == "" {
		return "", fmt.Errorf("missing required argument: command")
	}

	// Confirm gate — every command must be approved.
	if !t.confirmer.Confirm(fmt.Sprintf("run command: %s?", args.Command)) {
		return "", fmt.Errorf("command denied by user")
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
	dir := filepath.Join(t.cwd, ".og-spill")
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
