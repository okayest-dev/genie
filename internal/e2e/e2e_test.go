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
	// stderr may contain the session id line, but no other output
	if strings.Contains(stderr, "Error:") || strings.Contains(stderr, "verbose") {
		t.Errorf("stderr = %q, want no errors or verbose output", stderr)
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

func TestVerboseFlagBanner(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, providerEnv(p), "-v", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if !strings.Contains(stderr, "verbose mode enabled") {
		t.Errorf("stderr = %q, want it to contain the verbose banner", stderr)
	}
}

func TestDebugFlagBanner(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, providerEnv(p), "-d", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if !strings.Contains(stderr, "debug mode enabled") {
		t.Errorf("stderr = %q, want it to contain the debug banner", stderr)
	}
}

func TestNoFlagsStderrEmpty(t *testing.T) {
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
	// stderr may contain the session id line, but no other output
	if strings.Contains(stderr, "Error:") || strings.Contains(stderr, "verbose") {
		t.Errorf("stderr = %q, want no errors or verbose output when no -v or -d flag", stderr)
	}
}

func TestOGDebugEnvEnablesDebug(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_DEBUG=true"), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if !strings.Contains(stderr, "debug mode enabled") {
		t.Errorf("stderr = %q, want it to contain the debug banner with OG_DEBUG=true", stderr)
	}
}

func TestDebugOutputIncludesHTTPDetails(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_DEBUG=1"), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	for _, want := range []string{"/chat/completions", "Bearer <redacted>", "status="} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestAPIKeyNeverAppearsInDebugOutput(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	apiKey := "super-secret-api-key-abcdef"
	stdout, stderr, code := run(t, append(providerEnv(p), "OPENCODE_API_KEY="+apiKey, "OG_DEBUG=1"), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if strings.Contains(stderr, apiKey) {
		t.Errorf("stderr contains the API key — must be redacted:\n%s", stderr)
	}
}

func TestDebugFlagOverridesFalseOGDebug(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_DEBUG=false"), "-d", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if !strings.Contains(stderr, "debug mode enabled") {
		t.Errorf("stderr = %q, want debug banner even with OG_DEBUG=false when -d is set", stderr)
	}
}

func TestStdoutUnaffectedByFlags(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("hello"), fake.Finish("stop"), fake.Done},
	})
	want := "hello\n"
	for _, args := range [][]string{
		{"-p", "hi"},
		{"-v", "-p", "hi"},
		{"-d", "-p", "hi"},
	} {
		stdout, _, code := run(t, providerEnv(p), args...)
		if code != 0 {
			t.Fatalf("args %v: exit code = %d", args, code)
		}
		if stdout != want {
			t.Errorf("args %v: stdout = %q, want %q", args, stdout, want)
		}
	}
}

func TestVerboseShowsInfoMessages(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Usage(5, 3, 8), fake.Done},
	})
	_, stderr, code := run(t, providerEnv(p), "-v", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{"config loaded", "turn started", "turn completed", "finish_reason=stop", "total_tokens=8"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q with -v:\n%s", want, stderr)
		}
	}
}

func TestDebugShowsDebugMessages(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	_, stderr, code := run(t, providerEnv(p), "-d", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{"http request", "sse chunk", "sse stream complete"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q with -d:\n%s", want, stderr)
		}
	}
}

func TestSessionPersistence(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("hello"), fake.Finish("stop"), fake.Done},
	})
	sessionDir := t.TempDir()
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_SESSION_DIR="+sessionDir), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}

	// Verify session id is printed to stderr
	if !strings.Contains(stderr, "session:") {
		t.Errorf("stderr missing session id: %q", stderr)
	}

	// Check that a session file was created
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("session dir has %d entries, want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".jsonl") {
		t.Errorf("session file = %q, want .jsonl suffix", entries[0].Name())
	}

	// Read and verify the transcript content
	data, err := os.ReadFile(filepath.Join(sessionDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	transcript := string(data)

	// Verify system message
	if !strings.Contains(transcript, `"role":"system"`) {
		t.Errorf("transcript missing system message")
	}
	if !strings.Contains(transcript, `"content":"You are og, a helpful terminal agent."`) {
		t.Errorf("transcript missing system prompt content")
	}

	// Verify user message
	if !strings.Contains(transcript, `"role":"user"`) {
		t.Errorf("transcript missing user message")
	}
	if !strings.Contains(transcript, `"content":"hi"`) {
		t.Errorf("transcript missing user message content")
	}

	// Verify assistant message
	if !strings.Contains(transcript, `"role":"assistant"`) {
		t.Errorf("transcript missing assistant message")
	}
	if !strings.Contains(transcript, `"content":"hello"`) {
		t.Errorf("transcript missing assistant message content")
	}

	// Verify message order (system before user, user before assistant)
	sysIdx := strings.Index(transcript, `"role":"system"`)
	userIdx := strings.Index(transcript, `"role":"user"`)
	assistantIdx := strings.Index(transcript, `"role":"assistant"`)
	if sysIdx >= userIdx || userIdx >= assistantIdx {
		t.Errorf("messages out of order: system=%d, user=%d, assistant=%d", sysIdx, userIdx, assistantIdx)
	}
}

func TestToolCallExecutedAndResultFedBack(t *testing.T) {
	// Script: first response is a tool call for "read", second response is text.
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.ToolCallDelta(0, "call_1", "read", `{"path":"test.txt"}`),
			fake.Finish("tool_calls"),
			fake.Done,
		},
	})
	// After the tool result, the provider should get a second request.
	// We need to script the second response. Use a multi-behavior approach.
	p.Close()

	// Use a custom handler that serves two responses in sequence.
	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reqCount++
		if reqCount == 1 {
			// First request: return tool call.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			for _, chunk := range []string{
				fake.ToolCallDelta(0, "call_1", "read", `{"path":"test.txt"}`),
				fake.Finish("tool_calls"),
				fake.Done,
			} {
				io.WriteString(w, "data: "+chunk+"\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		} else {
			// Second request: return text.
			var bodyMap struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			json.Unmarshal(body, &bodyMap)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			for _, chunk := range []string{
				fake.TextDelta("file content here"),
				fake.Finish("stop"),
				fake.Done,
			} {
				io.WriteString(w, "data: "+chunk+"\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}))
	defer srv.Close()

	stdout, stderr, code := run(t, []string{
		"OG_BASE_URL=" + srv.URL,
		"OG_MODEL=test-model",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "read test.txt")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "file content here") {
		t.Errorf("stdout = %q, want it to contain 'file content here'", stdout)
	}
	// Check tool framing on stderr.
	if !strings.Contains(stderr, "── read ") {
		t.Errorf("stderr missing tool framing: %q", stderr)
	}
	if reqCount != 2 {
		t.Errorf("requests = %d, want 2 (tool call + final)", reqCount)
	}
}

func TestToolCallRequestIncludesTools(t *testing.T) {
	// Verify the first request includes a tools array.
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

	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	var body struct {
		Tools []any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Tools) == 0 {
		t.Error("request missing tools array")
	}
}

func TestToolCallResultPersistsToTranscript(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.TextDelta("done"),
			fake.Finish("stop"),
			fake.Done,
		},
	})
	sessionDir := t.TempDir()
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_SESSION_DIR="+sessionDir), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "done\n" {
		t.Errorf("stdout = %q, want %q", stdout, "done\n")
	}

	// Check session file exists.
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("session dir has %d entries, want 1", len(entries))
	}
}

func TestToolCallDisabledToolError(t *testing.T) {
	// Use config to disable the read tool.
	dir := configDir(t, "[tools]\nread = false\n")
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.ToolCallDelta(0, "call_1", "read", `{"path":"x"}`),
			fake.Finish("tool_calls"),
			fake.Done,
		},
	})
	// Need a second response for after the error.
	p.Close()

	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if reqCount == 1 {
			for _, chunk := range []string{
				fake.ToolCallDelta(0, "call_1", "read", `{"path":"x"}`),
				fake.Finish("tool_calls"),
				fake.Done,
			} {
				io.WriteString(w, "data: "+chunk+"\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		} else {
			for _, chunk := range []string{
				fake.TextDelta("tool is disabled"),
				fake.Finish("stop"),
				fake.Done,
			} {
				io.WriteString(w, "data: "+chunk+"\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}))
	defer srv.Close()

	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OG_BASE_URL=" + srv.URL,
		"OG_MODEL=test-model",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "read x")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "tool is disabled") {
		t.Errorf("stdout = %q, want it to contain 'tool is disabled'", stdout)
	}
}

// --- Multi-wire E2E tests ---

func TestAnthropicWire(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.AnthropicMessageStart(),
			fake.AnthropicTextDelta("Hello from Anthropic"),
			fake.AnthropicMessageDelta("end_turn"),
			fake.Done,
		},
	})
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_WIRE=anthropic"), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "Hello from Anthropic\n" {
		t.Errorf("stdout = %q, want %q", stdout, "Hello from Anthropic\n")
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Path != "/messages" {
		t.Errorf("request path = %q, want /messages", reqs[0].Path)
	}
}

func TestResponsesAPIWire(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.ResponsesTextDelta("Hello from Responses"),
			fake.ResponsesCompleted(),
			fake.Done,
		},
	})
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_WIRE=responses"), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "Hello from Responses\n" {
		t.Errorf("stdout = %q, want %q", stdout, "Hello from Responses\n")
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Path != "/responses" {
		t.Errorf("request path = %q, want /responses", reqs[0].Path)
	}
}

func TestGoogleWire(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.GoogleTextDelta("Hello from Google"),
			fake.GoogleFinish("STOP"),
			fake.Done,
		},
	})
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_WIRE=google", "OG_MODEL=gemini-test"), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "Hello from Google\n" {
		t.Errorf("stdout = %q, want %q", stdout, "Hello from Google\n")
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if !strings.HasPrefix(reqs[0].Path, "/models/") {
		t.Errorf("request path = %q, want /models/...", reqs[0].Path)
	}
}

func TestWireAutoDetectionClaudeToAnthropic(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.AnthropicMessageStart(),
			fake.AnthropicTextDelta("detected"),
			fake.AnthropicMessageDelta("end_turn"),
			fake.Done,
		},
	})
	stdout, stderr, code := run(t, []string{
		"OG_BASE_URL=" + p.URL,
		"OG_MODEL=claude-3-sonnet",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "detected\n" {
		t.Errorf("stdout = %q, want %q", stdout, "detected\n")
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Path != "/messages" {
		t.Errorf("request path = %q, want /messages (auto-detected anthropic)", reqs[0].Path)
	}
}

func TestWireAutoDetectionGeminiToGoogle(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.GoogleTextDelta("detected"),
			fake.GoogleFinish("STOP"),
			fake.Done,
		},
	})
	stdout, stderr, code := run(t, []string{
		"OG_BASE_URL=" + p.URL,
		"OG_MODEL=gemini-flash",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "detected\n" {
		t.Errorf("stdout = %q, want %q", stdout, "detected\n")
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if !strings.HasPrefix(reqs[0].Path, "/models/") {
		t.Errorf("request path = %q, want /models/... (auto-detected google)", reqs[0].Path)
	}
}

func TestExplicitWireOverrideInConfig(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.AnthropicMessageStart(),
			fake.AnthropicTextDelta("from config"),
			fake.AnthropicMessageDelta("end_turn"),
			fake.Done,
		},
	})
	dir := configDir(t, fmt.Sprintf("base_url = %q\nwire = \"anthropic\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OG_MODEL=test-model",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "from config\n" {
		t.Errorf("stdout = %q, want %q", stdout, "from config\n")
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Path != "/messages" {
		t.Errorf("request path = %q, want /messages", reqs[0].Path)
	}
}

func TestUnknownModelFallsBackToOpenAI(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, []string{
		"OG_BASE_URL=" + p.URL,
		"OG_MODEL=unknown-model",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Path != "/chat/completions" {
		t.Errorf("request path = %q, want /chat/completions (fallback)", reqs[0].Path)
	}
}

func TestOGProviderEnvRoutesThroughPlugin(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.AnthropicMessageStart(),
			fake.AnthropicTextDelta("via env"),
			fake.AnthropicMessageDelta("end_turn"),
			fake.Done,
		},
	})
	// OG_PROVIDER would normally name a loaded plugin, but with no plugins
	// installed it should error.
	stdout, stderr, code := run(t, append(providerEnv(p), "OG_PROVIDER=copilot"), "-p", "hi")
	assertCleanFailure(t, stdout, stderr, code, "provider")
}
