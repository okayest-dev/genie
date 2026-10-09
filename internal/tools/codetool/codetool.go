// Package codetool implements the code tool: a TypeScript/JavaScript snippet
// executed in a sandboxed Deno subprocess, whose permission axes the model
// requests per call.
package codetool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/tools"
)

const (
	maxOutputBytes = 1 << 20 // 1 MB — output beyond this is truncated with a spill file
)

// IsDenoAvailable reports whether the `deno` executable is on PATH.
func IsDenoAvailable() bool {
	_, err := exec.LookPath("deno")
	return err == nil
}

// Runner is the interface for executing a Deno snippet. The real
// implementation shells out to `deno run`; tests inject a fake.
type Runner interface {
	Run(ctx context.Context, snippetPath string, flags []string, cwd string) (stdout, stderr string, exitCode int, err error)
}

// RealRunner executes a Deno subprocess.
type RealRunner struct{}

// Run executes the Deno command and returns stdout, stderr, exit code, and error.
func (RealRunner) Run(ctx context.Context, snippetPath string, flags []string, cwd string) (stdout, stderr string, exitCode int, err error) {
	args := append([]string{"run", "--no-prompt"}, flags...)
	args = append(args, snippetPath)

	cmd := exec.CommandContext(ctx, "deno", args...)
	cmd.Dir = cwd
	setSysProcAttr(cmd)

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()

	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, err
}

// Tool executes a snippet under a permission envelope negotiated by the
// deny-point gate. It reads the effective policy to decide which requested
// axes still need escalation.
type Tool struct {
	store  *permissions.Store
	cwd    string
	timeout time.Duration
	runner Runner
}

// New builds a code tool over the effective-policy store.
func New(store *permissions.Store, cwd string, timeout time.Duration) *Tool {
	return &Tool{
		store:  store,
		cwd:    cwd,
		timeout: timeout,
		runner: RealRunner{},
	}
}

// WithRunner sets a custom runner for testing.
func (t *Tool) WithRunner(r Runner) *Tool {
	t.runner = r
	return t
}

// args is the request as far as the permission mapping reads it: the axes the
// model asks for, never scopes.
type args struct {
	Code        string   `json:"code"`
	Permissions []string `json:"permissions"`
	Timeout     int      `json:"timeout"`
}

// parsePermissions unmarshals the raw JSON and returns the set of valid
// permission axes requested, or an error if JSON is invalid or contains
// unknown axes.
func parsePermissions(raw json.RawMessage) (map[permissions.Axis]bool, error) {
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}

	asked := make(map[permissions.Axis]bool, len(a.Permissions))
	for _, name := range a.Permissions {
		if !permissions.IsAxis(name) {
			return nil, fmt.Errorf("invalid permission %q: must be one of read, write, net, run, env", name)
		}
		asked[permissions.Axis(name)] = true
	}
	return asked, nil
}

// Name returns the tool name.
func (t *Tool) Name() string { return "code" }

// Description returns the tool description.
func (t *Tool) Description() string {
	if IsDenoAvailable() {
		return "Execute a TypeScript/JavaScript snippet in a sandboxed Deno subprocess. Request permission axes via the permissions field."
	}
	return "Execute a TypeScript/JavaScript snippet in a sandboxed Deno subprocess (Deno not found on PATH — tool disabled). Request permission axes via the permissions field."
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{
				"type":        "string",
				"description": "TypeScript/JavaScript code to execute",
			},
			"permissions": map[string]any{
				"type":        "array",
				"description": "Permission axes to request (read, write, net, run, env)",
				"items": map[string]any{
					"type": "string",
					"enum": []string{"read", "write", "net", "run", "env"},
				},
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Execution timeout in seconds (overrides configured default)",
			},
		},
		"required": []any{"code"},
	}
}

// RequiredPermissions maps each requested axis onto an axis-only requirement
// under the any-scope-covered envelope: an axis the effective policy already
// covers at any scope is satisfied and omitted; an uncovered axis becomes a
// blanket requirement the gate negotiates through the standard prompt.
func (t *Tool) RequiredPermissions(raw json.RawMessage) ([]tools.Requirement, error) {
	asked, err := parsePermissions(raw)
	if err != nil {
		return nil, err
	}

	var reqs []tools.Requirement
	for _, axis := range permissions.AxisNames() {
		ax := permissions.Axis(axis)
		if !asked[ax] {
			continue
		}
		if t.store.AnyScopeCovered(ax) {
			continue
		}
		reqs = append(reqs, tools.Requirement{Axis: axis})
	}
	return reqs, nil
}

// Execute runs the code snippet in a Deno subprocess.
func (t *Tool) Execute(raw json.RawMessage) (string, error) {
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if a.Code == "" {
		return "", fmt.Errorf("missing required argument: code")
	}

	// Write snippet to temp file
	codeDir := filepath.Join(t.cwd, ".genie-tmp", "code")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		return "", fmt.Errorf("create code temp dir: %v", err)
	}

	// Generate filename: code-<timestamp>-<hash>.ts
	hash := fmt.Sprintf("%x", len(a.Code)) // simple hash based on length
	ts := time.Now().UnixMilli()
	filename := fmt.Sprintf("code-%d-%s.ts", ts, hash)
	snippetPath := filepath.Join(codeDir, filename)

	if err := os.WriteFile(snippetPath, []byte(a.Code), 0o644); err != nil {
		return "", fmt.Errorf("write snippet: %v", err)
	}

	// Build Deno flags from effective policy
	flags := t.buildDenoFlags(raw)

	// Determine timeout
	timeout := t.timeout
	if a.Timeout > 0 {
		timeout = time.Duration(a.Timeout) * time.Second
	}

	// Run with context timeout
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Execute
	stdout, stderr, exitCode, err := t.runner.Run(ctx, snippetPath, flags, t.cwd)

	// Check for Deno permission denial (NotCapable) on stderr
	if nc, ok := permissions.FindAndParseNotCapable(stderr); ok {
		if nc.Code == permissions.NotCapableCode && permissions.IsAxis(nc.Permission) {
			return permissions.RenderNotCapableMarker(nc.Permission, nc.Resource), nil
		}
	}

	// Combine output with exit code
	output := t.formatOutput(stdout, stderr, exitCode)

	// Handle timeout
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return output, fmt.Errorf("code execution timed out")
		}
		// Non-timeout errors are already in output
	}

	// Apply output cap and spill
	if len(output) > maxOutputBytes {
		spillPath, spillErr := t.writeSpill(output)
		if spillErr != nil {
			return "", fmt.Errorf("truncate output: %v", spillErr)
		}
		output = output[:maxOutputBytes] + fmt.Sprintf("\n\n[truncated — full output spilled to %s]", spillPath)
	}

	return output, nil
}

// buildDenoFlags constructs the Deno --allow-* flags from the effective
// covered scopes for each requested axis. It uses the store's CoveredScopes
// view, which includes base, permanent, session, and once-tier grants.
// - A blanket grant (scope "") yields the unrestricted flag form (no =scopes).
// - Scoped grants are rendered comma-separated per axis.
// - An unrequested axis yields no flag.
// - Scopes pass through verbatim; they are not normalized, reordered, or filtered.
func (t *Tool) buildDenoFlags(raw json.RawMessage) []string {
	asked, err := parsePermissions(raw)
	if err != nil {
		return nil
	}

	var flags []string
	for _, axis := range permissions.AxisNames() {
		ax := permissions.Axis(axis)
		if !asked[ax] {
			continue
		}
		scopes := t.store.CoveredScopes(ax)
		if len(scopes) == 0 {
			// Axis requested but no coverage — the gate should have blocked
			// this, but we defensively emit no flag so Deno denies by default.
			continue
		}
		// Blanket grant: scopes contains only "".
		if len(scopes) == 1 && scopes[0] == "" {
			flags = append(flags, fmt.Sprintf("--allow-%s", axis))
			continue
		}
		// Scoped grants: join with commas.
		flags = append(flags, fmt.Sprintf("--allow-%s=%s", axis, strings.Join(scopes, ",")))
	}
	return flags
}

// formatOutput combines stdout, stderr, and exit code into the result string.
func (t *Tool) formatOutput(stdout, stderr string, exitCode int) string {
	var out strings.Builder
	if stdout != "" {
		out.WriteString(stdout)
	}
	if stderr != "" {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString("[stderr]\n")
		out.WriteString(stderr)
	}
	if exitCode != 0 {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(fmt.Sprintf("[exit code %d]", exitCode))
	}
	return out.String()
}

// writeSpill writes full output to a temp file and returns its path.
func (t *Tool) writeSpill(output string) (string, error) {
	dir := filepath.Join(t.cwd, ".genie-spill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "code-output-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(output); err != nil {
		return "", err
	}
	return f.Name(), nil
}