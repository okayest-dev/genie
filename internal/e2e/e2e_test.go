// Package e2e drives the compiled og binary as a subprocess against the
// scripted fake provider. The binary seam is the primary test seam: tests
// assert only observable behavior — stdout, stderr, and exit codes.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/og/internal/e2e/fake"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "og-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "make temp dir:", err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "og")
	build := exec.Command("go", "build", "-o", binPath, "github.com/okayest-dev/og/cmd/og")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build og: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// run invokes the binary with the given extra env and args, stdin closed, and
// returns stdout, stderr, and the exit code.
func run(t *testing.T, env []string, args ...string) (string, string, int) {
	return runInDir(t, "", env, args...)
}

// runInDir is like run but sets the working directory for the subprocess.
// An empty dir means use the default (test's working directory).
func runInDir(t *testing.T, dir string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), env...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return out.String(), errBuf.String(), code
}

func TestEmptyPromptUsageError(t *testing.T) {
	stdout, stderr, code := run(t, nil, "-p", "")
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "usage") {
		t.Errorf("stderr = %q, want a usage message", stderr)
	}
}

// providerEnv points the binary at a fake provider with a fixed model and key.
func providerEnv(p *fake.Provider) []string {
	return []string{
		"OG_BASE_URL=" + p.URL,
		"OG_MODEL=test-model",
		"OPENCODE_API_KEY=test-key",
	}
}

// scriptedProvider serves the given behavior and closes when the test ends.
func scriptedProvider(t *testing.T, b fake.Behavior) *fake.Provider {
	t.Helper()
	p := fake.New()
	t.Cleanup(p.Close)
	p.SetBehavior(b)
	return p
}

// assertCleanFailure checks the "open failure" contract: non-zero exit, a
// clear Error: line on stderr, nothing on stdout, and never a stack trace.
func assertCleanFailure(t *testing.T, stdout, stderr string, code int, wantStderr string) {
	t.Helper()
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", stdout)
	}
	if !strings.HasPrefix(stderr, "Error: ") {
		t.Errorf("stderr = %q, want to start with %q", stderr, "Error: ")
	}
	if !strings.Contains(stderr, wantStderr) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, wantStderr)
	}
	if strings.Contains(stderr, "goroutine") || strings.Contains(stderr, "panic") {
		t.Errorf("stderr = %q, want no stack trace", stderr)
	}
}

// assertRequestModel checks the one scripted chat request carried the given
// model and Authorization header.
func assertRequestModel(t *testing.T, p *fake.Provider, wantModel, wantAuth string) {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests received = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != "POST" || req.Path != "/chat/completions" {
		t.Errorf("request = %s %s, want POST /chat/completions", req.Method, req.Path)
	}
	if req.Auth != wantAuth {
		t.Errorf("Authorization = %q, want %q", req.Auth, wantAuth)
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if body.Model != wantModel {
		t.Errorf("request model = %q, want %q", body.Model, wantModel)
	}
}

// assertSystemMessage checks the first message in the request is a system
// message with the given content prefix.
func assertSystemMessage(t *testing.T, p *fake.Provider, wantContent string) {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests received = %d, want 1", len(reqs))
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if len(body.Messages) < 1 {
		t.Fatalf("request has no messages")
	}
	if body.Messages[0].Role != "system" {
		t.Errorf("first message role = %q, want %q", body.Messages[0].Role, "system")
	}
	if body.Messages[0].Content != wantContent {
		t.Errorf("system message content = %q, want %q", body.Messages[0].Content, wantContent)
	}
}

// configDir creates a fresh XDG_CONFIG_HOME with og/config.toml holding the
// given content; content "" leaves the config file absent.
func configDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	ogDir := filepath.Join(dir, "og")
	if err := os.MkdirAll(ogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(ogDir, "config.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestStreamsReply(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.TextDelta("Hello"),
			fake.TextDelta(", "),
			fake.TextDelta("world"),
			fake.Finish("stop"),
			fake.Done,
		},
	})
	stdout, stderr, code := run(t, providerEnv(p), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if stdout != "Hello, world\n" {
		t.Errorf("stdout = %q, want %q", stdout, "Hello, world\n")
	}

	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests received = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != "POST" || req.Path != "/chat/completions" {
		t.Errorf("request = %s %s, want POST /chat/completions", req.Method, req.Path)
	}
	if req.Auth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", req.Auth, "Bearer test-key")
	}
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if body.Model != "test-model" {
		t.Errorf("request model = %q, want %q", body.Model, "test-model")
	}
	assertSystemMessage(t, p, "You are og, a helpful terminal agent.")
	if len(body.Messages) != 2 {
		t.Fatalf("request messages = %+v, want 2 messages (system + user)", body.Messages)
	}
	if body.Messages[1].Role != "user" || body.Messages[1].Content != "hi" {
		t.Errorf("second message = %+v, want user message %q", body.Messages[1], "hi")
	}
	if !body.Stream {
		t.Errorf("request stream = false, want true")
	}
}

func TestReasoningFieldsIgnored(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.ReasoningDelta("Let me think carefully before answering"),
			fake.TextDelta("The answer is 42."),
			fake.Finish("stop"),
			fake.Done,
		},
	})
	stdout, stderr, code := run(t, providerEnv(p), "-p", "what is 6*7?")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "The answer is 42.\n" {
		t.Errorf("stdout = %q, want %q", stdout, "The answer is 42.\n")
	}
	if strings.Contains(stdout, "think carefully") {
		t.Errorf("reasoning text leaked into stdout: %q", stdout)
	}
}

func TestMissingUsageNeverBreaksTurn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
	}{
		{name: "no usage chunk at all", chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done}},
		{name: "usage chunk present", chunks: []string{fake.TextDelta("ok"), fake.Usage(9, 3, 12), fake.Finish("stop"), fake.Done}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := scriptedProvider(t, fake.Behavior{Chunks: tc.chunks})
			stdout, stderr, code := run(t, providerEnv(p), "-p", "hi")
			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
			}
			if stdout != "ok\n" {
				t.Errorf("stdout = %q, want %q", stdout, "ok\n")
			}
		})
	}
}

func TestOpenFailuresReportError(t *testing.T) {
	tests := []struct {
		name       string
		error      *fake.Error
		wantStderr string
	}{
		{
			name:       "auth 401",
			error:      &fake.Error{Status: 401, Body: `{"error":{"message":"invalid api key"}}`},
			wantStderr: "invalid api key",
		},
		{
			name:       "rate limited 429",
			error:      &fake.Error{Status: 429, Body: `{"error":{"message":"rate limit exceeded"}}`},
			wantStderr: "rate limit exceeded",
		},
		{
			name:       "not found 404",
			error:      &fake.Error{Status: 404, Body: `not found`},
			wantStderr: "provider returned 404",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := scriptedProvider(t, fake.Behavior{Error: tc.error})
			stdout, stderr, code := run(t, providerEnv(p), "-p", "hi")
			assertCleanFailure(t, stdout, stderr, code, tc.wantStderr)
		})
	}
}

func TestNetworkDownReportsError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	stdout, stderr, code := run(t, []string{"OG_BASE_URL=" + url}, "-p", "hi")
	assertCleanFailure(t, stdout, stderr, code, "Error: ")
}

func TestConfigFileDrivesWireRequest(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, fmt.Sprintf("base_url = %q\nmodel = \"cfg-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertRequestModel(t, p, "cfg-model", "Bearer test-key")
}

func TestEnvOverridesConfigFile(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, fmt.Sprintf("base_url = %q\nmodel = \"file-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OG_MODEL=env-model",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertRequestModel(t, p, "env-model", "Bearer test-key")
}

func TestMissingConfigFileUsesDefaults(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, "")
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OG_BASE_URL=" + p.URL,
		"OPENCODE_API_KEY=",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertRequestModel(t, p, "big-pickle", "")
}

func TestMalformedConfigFailsFast(t *testing.T) {
	dir := configDir(t, "model =")
	stdout, stderr, code := run(t, []string{"XDG_CONFIG_HOME=" + dir}, "-p", "hi")
	assertCleanFailure(t, stdout, stderr, code, "config")
}

func TestUnknownConfigKeyFailsFast(t *testing.T) {
	dir := configDir(t, "bas_url = \"https://example.com\"")
	stdout, stderr, code := run(t, []string{"XDG_CONFIG_HOME=" + dir}, "-p", "hi")
	assertCleanFailure(t, stdout, stderr, code, "unknown key")
}

func TestAPIKeyReadsFromConfiguredEnvVar(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, fmt.Sprintf("base_url = %q\napi_key_env = \"OG_MY_KEY\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OG_MY_KEY=test-key",
		"OPENCODE_API_KEY=",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertRequestModel(t, p, "big-pickle", "Bearer test-key")
}

func TestDefaultPromptAlwaysInRequest(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, providerEnv(p), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertSystemMessage(t, p, "You are og, a helpful terminal agent.")
}

func TestInstructionFileInRequest(t *testing.T) {
	dir := t.TempDir()
	instFile := filepath.Join(dir, "instructions.md")
	if err := os.WriteFile(instFile, []byte("custom agent rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("base_url = %q\ninstruction_file = %q\n", p.URL, instFile))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OG_MODEL=test-model",
		"OPENCODE_API_KEY=test-key",
		// Work from an empty dir so no AGENTS.md interference
		"HOME=" + dir,
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are og, a helpful terminal agent.\ncustom agent rules"
	assertSystemMessage(t, p, want)
}

func TestAGENTSMDInRequest(t *testing.T) {
	workDir := t.TempDir()
	agentsMD := filepath.Join(workDir, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("project rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("base_url = %q\n", p.URL))
	stdout, stderr, code := runInDir(t, workDir, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OG_MODEL=test-model",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are og, a helpful terminal agent.\nproject rules"
	assertSystemMessage(t, p, want)
}

func TestMissingInstructionFileFailsAtStartup(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("base_url = %q\ninstruction_file = \"/nonexistent/instructions.md\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OG_MODEL=test-model",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	assertCleanFailure(t, stdout, stderr, code, "instruction file")
}

func TestAllThreeSourcesInOrderInRequest(t *testing.T) {
	dir := t.TempDir()
	instFile := filepath.Join(dir, "instructions.md")
	if err := os.WriteFile(instFile, []byte("---config---"), 0o644); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentsMD := filepath.Join(workDir, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("---agents---"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("base_url = %q\ninstruction_file = %q\n", p.URL, instFile))
	stdout, stderr, code := runInDir(t, workDir, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OG_MODEL=test-model",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are og, a helpful terminal agent.\n---config---\n---agents---"
	assertSystemMessage(t, p, want)
}

// TestTextStreamsLive asserts deltas arrive before the stream (and process)
// ends: the fake pauses between chunks, and the first delta must be readable
// from the pipe well before the process exits.
func TestTextStreamsLive(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.TextDelta("first"),
			fake.TextDelta("second"),
			fake.Finish("stop"),
			fake.Done,
		},
		Delay: time.Second,
	})
	cmd := exec.Command(binPath, "-p", "hi")
	cmd.Env = append(os.Environ(), providerEnv(p)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	buf := make([]byte, len("first"))
	if _, err := io.ReadFull(stdout, buf); err != nil {
		t.Fatalf("read first delta: %v", err)
	}
	elapsed := time.Since(start)
	if string(buf) != "first" {
		t.Fatalf("first bytes = %q, want %q", buf, "first")
	}
	if elapsed > 2*time.Second {
		t.Errorf("first delta arrived after %v; expected it to stream live, not only after the process exited", elapsed)
	}

	rest, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("read rest of stream: %v", err)
	}
	if string(buf)+string(rest) != "firstsecond\n" {
		t.Errorf("full stdout = %q, want %q", string(buf)+string(rest), "firstsecond\n")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("process exited non-zero: %v", err)
	}
}
