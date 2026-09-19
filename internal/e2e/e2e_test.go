// Package e2e drives the compiled genie binary as a subprocess against the
// scripted fake provider. The binary seam is the primary test seam: tests
// assert only observable behavior — stdout, stderr, and exit codes.
package e2e

import (
	"bufio"
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

	"github.com/okayest-dev/genie/internal/e2e/fake"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "genie-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "make temp dir:", err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "genie")
	build := exec.Command("go", "build", "-o", binPath, "github.com/okayest-dev/genie/cmd/genie")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build genie: %v\n%s", err, out)
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

// runWithStdin runs the binary with the given stdin content, env, and args,
// and returns stdout, stderr, and the exit code.
func runWithStdin(t *testing.T, stdin string, env []string, args ...string) (string, string, int) {
	return runInDirWithStdin(t, "", stdin, env, args...)
}

// runInDirWithStdin is like runInDir but sets stdin for the subprocess.
func runInDirWithStdin(t *testing.T, dir, stdin string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
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

// providerConfigAt returns a config-file body that declares zen as the active
// provider, overriding the shipped zen table's base_url and model so the
// binary talks to a test server instead of the real endpoint.
func providerConfigAt(baseURL, model string) string {
	return fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\nmodel = %q\n", baseURL, model)
}

// providerConfig returns the config body for a scripted fake provider.
func providerConfig(p *fake.Provider) string {
	return providerConfigAt(p.URL, "test-model")
}

// providerBaseURLConfig points the shipped zen provider at the given base
// URL, leaving its model, key env and everything else on the shipped
// defaults — for tests that assert those defaults hold.
func providerBaseURLConfig(baseURL string) string {
	return fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\n", baseURL)
}

// providerEnv points the binary at a config dir declaring the fake as the
// active zen provider, with the key it reads (zen's shipped openai key)
// set. Provider selection now lives in the config, so tests never drive
// startup through the flat GENIE_BASE_URL/GENIE_MODEL env keys (og-z1m.3).
func providerEnv(t *testing.T, p *fake.Provider) []string {
	t.Helper()
	dir := configDir(t, providerConfig(p))
	return []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + t.TempDir(),
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

// chatRequest returns the POST request from the recorded requests.
// It handles the google wire's lazy model-info probe (GET) preceding the chat POST.
func chatRequest(reqs []fake.Request) *fake.Request {
	for i := range reqs {
		if reqs[i].Method == "POST" {
			return &reqs[i]
		}
	}
	return nil
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

// configDir creates a fresh XDG_CONFIG_HOME with genie/config.toml holding the
// given content; content "" leaves the config file absent.
func configDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	genieDir := filepath.Join(dir, "genie")
	if err := os.MkdirAll(genieDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(genieDir, "config.toml"), []byte(content), 0o644); err != nil {
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
	stdout, stderr, code := run(t, providerEnv(t, p), "-p", "hi")
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
	assertSystemMessage(t, p, "You are genie, a helpful terminal agent.")
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
	stdout, stderr, code := run(t, providerEnv(t, p), "-p", "what is 6*7?")
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
			stdout, stderr, code := run(t, providerEnv(t, p), "-p", "hi")
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
			stdout, stderr, code := run(t, providerEnv(t, p), "-p", "hi")
			assertCleanFailure(t, stdout, stderr, code, tc.wantStderr)
		})
	}
}

func TestNetworkDownReportsError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	dir := configDir(t, providerBaseURLConfig(url))
	stdout, stderr, code := run(t, []string{"XDG_CONFIG_HOME=" + dir, "OPENCODE_API_KEY=test-key"}, "-p", "hi")
	assertCleanFailure(t, stdout, stderr, code, "Error: ")
}

func TestConfigFileDrivesWireRequest(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\nmodel = \"cfg-model\"\n", p.URL))
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

// TestEnvBeatsConfigFileProviderSelection is the env > file precedence rule
// at the provider seam: the config file selects zen, GENIE_PROVIDER selects
// "other", and the env selection wins.
func TestEnvBeatsConfigFileProviderSelection(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfg := fmt.Sprintf("provider = \"zen\"\n\n"+
		"[providers.zen]\nbase_url = %q\nmodel = \"zen-file\"\n\n"+
		"[providers.other]\nwire = \"openai\"\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"other-env\"\n",
		p.URL, p.URL)
	dir := configDir(t, cfg)
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"GENIE_PROVIDER=other",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertRequestModel(t, p, "other-env", "Bearer test-key")
}

// TestMinimalConfigUsesProviderDefaults pins that a minimal config overlay
// — only the base_url to reach a test server — boots the shipped zen default:
// its default model and the absence of a key mean no Authorization header.
func TestMinimalConfigUsesProviderDefaults(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, providerBaseURLConfig(p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
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

// twoProviderConfigAt returns a config body declaring two openai-wire
// providers (aaa, zzz), both pointed at the scripted fake, with no top-level
// provider selector. Sorted, aaa is the first declared provider; picking a
// provider in tests below is deterministic.
func twoProviderConfigAt(url string) string {
	return fmt.Sprintf(
		"[providers.aaa]\nwire = \"openai\"\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"aaa-model\"\n\n"+
			"[providers.zzz]\nwire = \"openai\"\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"zzz-model\"\n",
		url, url)
}

// TestOneShotFallbackFirstDeclaredProvider is the og-z1m.4 acceptance at the
// binary seam for -p: with no provider selected anywhere, the run boots on the
// first declared provider and warns on stderr.
func TestOneShotFallbackFirstDeclaredProvider(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, twoProviderConfigAt(p.URL))
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
	if !strings.Contains(stderr, "warning: no provider selected") || !strings.Contains(stderr, "aaa") {
		t.Errorf("stderr = %q, want a fallback warning naming the first declared provider", stderr)
	}
	assertRequestModel(t, p, "aaa-model", "Bearer test-key")
}

// TestInteractivePromptSelectsProvider is the og-z1m.4 acceptance at the
// binary seam for the REPL: with no provider selected, startup lists the
// declared providers, prompts, and boots on the pick.
func TestInteractivePromptSelectsProvider(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, twoProviderConfigAt(p.URL))
	stdin := "zzz\nhi\n/quit\n"
	stdout, stderr, code := runWithStdin(t, stdin, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=test-key",
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{"Available providers:", "  aaa", "  zzz", "Select provider:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	assertRequestModel(t, p, "zzz-model", "Bearer test-key")
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
	dir := configDir(t, fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\napi_key_env = \"GENIE_MY_KEY\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"GENIE_MY_KEY=test-key",
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
	stdout, stderr, code := run(t, providerEnv(t, p), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertSystemMessage(t, p, "You are genie, a helpful terminal agent.")
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
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\ninstruction_file = %q\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", instFile, p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
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
	want := "You are genie, a helpful terminal agent.\ncustom agent rules"
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
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := runInDir(t, workDir, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + t.TempDir(),
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are genie, a helpful terminal agent.\nproject rules"
	assertSystemMessage(t, p, want)
}

func TestMissingInstructionFileFailsAtStartup(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\ninstruction_file = \"/nonexistent/instructions.md\"\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
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
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\ninstruction_file = %q\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", instFile, p.URL))
	stdout, stderr, code := runInDir(t, workDir, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + t.TempDir(),
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are genie, a helpful terminal agent.\n---config---\n---agents---"
	assertSystemMessage(t, p, want)
}

// writeSkill creates <dir>/<name>/SKILL.md with the given front matter and
// body, the standard layout skill discovery expects.
func writeSkill(t *testing.T, dir, name, description, body string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n", name, description)
	if body != "" {
		content += body + "\n"
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSKILLMDInjectedIntoSystemMessage asserts a SKILL.md dropped into a
// discovery dir is automatically loaded, parsed, and injected into the
// system message between the instruction file and AGENTS.md (og-uem.11).
func TestSKILLMDInjectedIntoSystemMessage(t *testing.T) {
	skillDir := t.TempDir()
	writeSkill(t, skillDir, "alpha", "Handles alpha tasks.", "Alpha body.")
	writeSkill(t, skillDir, "beta", "Handles beta tasks.", "Beta body.")

	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + skillDir,
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are genie, a helpful terminal agent.\n" +
		"## Skills\n" +
		"Available skills — engage a skill when its description matches the current task:\n" +
		"- alpha: Handles alpha tasks.\n" +
		"- beta: Handles beta tasks.\n" +
		"### Skill: alpha\n" +
		"Alpha body.\n" +
		"### Skill: beta\n" +
		"Beta body.\n"
	assertSystemMessage(t, p, want)
}

// TestEmptySkillPoolNoLayer asserts an empty discovery dir injects no skill
// layer, so an agent with no available skills gets the bare default prompt.
func TestEmptySkillPoolNoLayer(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, providerEnv(t, p), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertSystemMessage(t, p, "You are genie, a helpful terminal agent.")
}

// TestSKILLMDOrderInInstruction asserts the skill layer sits between the
// instruction file and AGENTS.md in the system message.
func TestSKILLMDOrderInInstruction(t *testing.T) {
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
	skillDir := filepath.Join(dir, "skills")
	writeSkill(t, skillDir, "alpha", "Handles alpha tasks.", "Alpha body.")

	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\ninstruction_file = %q\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", instFile, p.URL))
	stdout, stderr, code := runInDir(t, workDir, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + skillDir,
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are genie, a helpful terminal agent.\n---config---\n" +
		"## Skills\n" +
		"Available skills — engage a skill when its description matches the current task:\n" +
		"- alpha: Handles alpha tasks.\n" +
		"### Skill: alpha\n" +
		"Alpha body.\n\n" +
		"---agents---"
	assertSystemMessage(t, p, want)
}

// TestConfigSkillsEnableAllowlist asserts [skills] enable limits the injected
// layer to the named skills only.
func TestConfigSkillsEnableAllowlist(t *testing.T) {
	skillDir := t.TempDir()
	writeSkill(t, skillDir, "alpha", "Handles alpha tasks.", "Alpha body.")
	writeSkill(t, skillDir, "beta", "Handles beta tasks.", "Beta body.")

	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\n\n[skills]\nenable = [\"alpha\"]\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + skillDir,
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are genie, a helpful terminal agent.\n" +
		"## Skills\n" +
		"Available skills — engage a skill when its description matches the current task:\n" +
		"- alpha: Handles alpha tasks.\n" +
		"### Skill: alpha\n" +
		"Alpha body.\n"
	assertSystemMessage(t, p, want)
}

// TestAgentSkillsSubset asserts an agent declaring skills binds only what it
// names, via a local .genie/agents definition (og-uem.11).
func TestAgentSkillsSubset(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills")
	writeSkill(t, skillDir, "alpha", "Handles alpha tasks.", "Alpha body.")
	writeSkill(t, skillDir, "beta", "Handles beta tasks.", "Beta body.")

	agentsDir := filepath.Join(dir, ".genie", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentToml := "model = \"test-model\"\nskills = [\"beta\"]\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "parser.toml"), []byte(agentToml), 0o644); err != nil {
		t.Fatal(err)
	}

	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := runInDir(t, dir, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + skillDir,
	}, "-a", "parser", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are genie, a helpful terminal agent.\n" +
		"## Skills\n" +
		"Available skills — engage a skill when its description matches the current task:\n" +
		"- beta: Handles beta tasks.\n" +
		"### Skill: beta\n" +
		"Beta body.\n"
	assertSystemMessage(t, p, want)
}

// TestAgentUnknownSkillFailsStartup asserts an agent naming a skill that is
// not in the discovered pool fails startup with a clean error.
func TestAgentUnknownSkillFailsStartup(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills")
	writeSkill(t, skillDir, "alpha", "Handles alpha tasks.", "Alpha body.")

	agentsDir := filepath.Join(dir, ".genie", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentToml := "model = \"test-model\"\nskills = [\"nope\"]\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "parser.toml"), []byte(agentToml), 0o644); err != nil {
		t.Fatal(err)
	}

	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := runInDir(t, dir, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + skillDir,
	}, "-a", "parser", "-p", "hi")
	assertCleanFailure(t, stdout, stderr, code, "unknown skill")
}

// TestConfigSkillsDisableDenylist asserts [skills] disable suppresses a skill
// from the injected layer even when it exists on disk.
func TestConfigSkillsDisableDenylist(t *testing.T) {
	skillDir := t.TempDir()
	writeSkill(t, skillDir, "alpha", "Handles alpha tasks.", "Alpha body.")
	writeSkill(t, skillDir, "beta", "Handles beta tasks.", "Beta body.")

	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfgDir := configDir(t, fmt.Sprintf("provider = \"zen\"\n\n[skills]\ndisable = [\"beta\"]\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + cfgDir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SKILL_DIR=" + skillDir,
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	want := "You are genie, a helpful terminal agent.\n" +
		"## Skills\n" +
		"Available skills — engage a skill when its description matches the current task:\n" +
		"- alpha: Handles alpha tasks.\n" +
		"### Skill: alpha\n" +
		"Alpha body.\n"
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
	cmd.Env = append(os.Environ(), providerEnv(t, p)...)
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
	stdout, stderr, code := run(t, providerEnv(t, p), "-v", "-p", "hi")
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
	stdout, stderr, code := run(t, providerEnv(t, p), "-d", "-p", "hi")
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
	stdout, stderr, code := run(t, providerEnv(t, p), "-p", "hi")
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
	stdout, stderr, code := run(t, append(providerEnv(t, p), "GENIE_DEBUG=true"), "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if !strings.Contains(stderr, "debug mode enabled") {
		t.Errorf("stderr = %q, want it to contain the debug banner with GENIE_DEBUG=true", stderr)
	}
}

func TestDebugOutputIncludesHTTPDetails(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := run(t, append(providerEnv(t, p), "GENIE_DEBUG=1"), "-p", "hi")
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
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + configDir(t, providerConfig(p)),
		"OPENCODE_API_KEY=" + apiKey,
		"GENIE_DEBUG=1",
	}, "-p", "hi")
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
	stdout, stderr, code := run(t, append(providerEnv(t, p), "GENIE_DEBUG=false"), "-d", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if !strings.Contains(stderr, "debug mode enabled") {
		t.Errorf("stderr = %q, want debug banner even with GENIE_DEBUG=false when -d is set", stderr)
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
		stdout, _, code := run(t, providerEnv(t, p), args...)
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
	_, stderr, code := run(t, providerEnv(t, p), "-v", "-p", "hi")
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
	_, stderr, code := run(t, providerEnv(t, p), "-d", "-p", "hi")
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
	stdout, stderr, code := run(t, append(providerEnv(t, p), "GENIE_SESSION_DIR="+sessionDir), "-p", "hi")
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
	if !strings.Contains(transcript, `"content":"You are genie, a helpful terminal agent."`) {
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

// TestREPLCrossTurnHistory drives the REPL across two turns and asserts that
// the second turn's request to the provider carries the first turn's messages,
// proving history injection: the model can reference earlier context.
func TestREPLCrossTurnHistory(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("turn reply"), fake.Finish("stop"), fake.Done},
	})

	// Two REPL turns then quit. Each turn produces one chat request.
	_, stderr, code := runWithStdin(t, "first question\nsecond question\n/quit\n", providerEnv(t, p))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}

	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests received = %d, want 2", len(reqs))
	}

	firstTurn := decodeTurnMessages(t, reqs[0].Body)
	if len(firstTurn) != 2 {
		t.Fatalf("first turn messages = %d, want 2", len(firstTurn))
	}
	if firstTurn[1].Role != "user" || firstTurn[1].Content != "first question" {
		t.Errorf("first turn user = %+v, want user 'first question'", firstTurn[1])
	}

	secondTurn := decodeTurnMessages(t, reqs[1].Body)

	// The second turn's request must include the first turn's messages so the
	// model remembers prior context.
	var priorUser, priorAssistant, currentUser bool
	for _, m := range secondTurn {
		switch {
		case m.Role == "user" && m.Content == "first question":
			priorUser = true
		case m.Role == "assistant" && m.Content == "turn reply":
			priorAssistant = true
		case m.Role == "user" && m.Content == "second question":
			currentUser = true
		}
	}
	if !priorUser {
		t.Errorf("second turn request missing first turn user message: %+v", secondTurn)
	}
	if !priorAssistant {
		t.Errorf("second turn request missing first turn assistant message: %+v", secondTurn)
	}
	if !currentUser {
		t.Errorf("second turn request missing current user message: %+v", secondTurn)
	}

	// The current turn must not be doubled: exactly one "second question".
	count := 0
	for _, m := range secondTurn {
		if m.Role == "user" && m.Content == "second question" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("current user message appears %d times, want 1", count)
	}
}

// TestREPLContextTurnsWindow verifies the configurable context_turns window is
// honoured end-to-end: with GENIE_CONTEXT_TURNS=1, the third turn's request to
// the provider carries only the immediately preceding turn, not the first.
func TestREPLContextTurnsWindow(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("turn reply"), fake.Finish("stop"), fake.Done},
	})

	env := append(providerEnv(t, p), "GENIE_CONTEXT_TURNS=1")
	_, stderr, code := runWithStdin(t, "first question\nsecond question\nthird question\n/quit\n", env)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}

	reqs := p.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests received = %d, want 3", len(reqs))
	}

	third := decodeTurnMessages(t, reqs[2].Body)
	for _, m := range third {
		if m.Content == "first question" {
			t.Errorf("third turn request carried a dropped-out turn: %+v", third)
		}
	}

	// With a window of 1, the second turn and the current (third) turn must
	// still be present.
	for _, want := range []string{"second question", "third question"} {
		found := false
		for _, m := range third {
			if m.Role == "user" && m.Content == want {
				found = true
			}
		}
		if !found {
			t.Errorf("third turn request missing %q: %+v", want, third)
		}
	}
}

// decodeTurnMessages unmarshals the messages array of a chat request body.
func decodeTurnMessages(t *testing.T, body string) []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
} {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	return req.Messages
}

// TestREPLNewResetsHistory verifies that /new starts a fresh session: a turn
// after /new must not carry messages from the previous session.
func TestREPLNewResetsHistory(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("turn reply"), fake.Finish("stop"), fake.Done},
	})

	_, stderr, code := runWithStdin(t, "first question\n/new\nsecond question\n/quit\n", providerEnv(t, p))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}

	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests received = %d, want 2", len(reqs))
	}

	second := decodeTurnMessages(t, reqs[1].Body)
	for _, m := range second {
		if m.Content == "first question" {
			t.Errorf("turn after /new carried previous session message: %+v", second)
		}
	}

	// The new session's current user message must still be present.
	found := false
	for _, m := range second {
		if m.Role == "user" && m.Content == "second question" {
			found = true
		}
	}
	if !found {
		t.Errorf("turn after /new missing current user message: %+v", second)
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

	dir := configDir(t, providerConfigAt(srv.URL, "test-model"))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
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
	stdout, stderr, code := run(t, providerEnv(t, p), "-p", "hi")
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
	stdout, stderr, code := run(t, append(providerEnv(t, p), "GENIE_SESSION_DIR="+sessionDir), "-p", "hi")
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

	// Use config to disable the read tool while declaring the provider.
	dir := configDir(t, fmt.Sprintf("%s\n[tools]\nread = false\n", providerConfigAt(srv.URL, "test-model")))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
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
	dir := configDir(t, fmt.Sprintf("provider = \"anthropic\"\n\n[providers.anthropic]\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
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
	dir := configDir(t, fmt.Sprintf("provider = \"responses\"\n\n[providers.responses]\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
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
		ContextWindow: 1_000_000,
	})
	dir := configDir(t, fmt.Sprintf("provider = \"google\"\n\n[providers.google]\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"gemini-test\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "Hello from Google\n" {
		t.Errorf("stdout = %q, want %q", stdout, "Hello from Google\n")
	}
	reqs := p.Requests()
	// Two requests: the lazy model-info probe (GET, context window lookup)
	// followed by the chat POST.
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2 (model-info probe + chat)", len(reqs))
	}
	chat := chatRequest(reqs)
	if chat == nil {
		t.Fatalf("no chat POST among requests: %+v", reqs)
	}
	if !strings.HasPrefix(chat.Path, "/models/") {
		t.Errorf("chat path = %q, want /models/...", chat.Path)
	}
}

// TestClaudeModelOnDeclaredAnthropicProvider: a claude-* model no longer
// implies a wire — the declared anthropic provider's own wire serves /messages
// (this replaces the old model-prefix auto-detection test).
func TestClaudeModelOnDeclaredAnthropicProvider(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.AnthropicMessageStart(),
			fake.AnthropicTextDelta("detected"),
			fake.AnthropicMessageDelta("end_turn"),
			fake.Done,
		},
	})
	dir := configDir(t, fmt.Sprintf("provider = \"anthropic\"\n\n[providers.anthropic]\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"claude-3-sonnet\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
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
		t.Errorf("request path = %q, want /messages (declared anthropic provider)", reqs[0].Path)
	}
}

// TestGeminiModelOnDeclaredGoogleProvider: a gemini-* model now reaches the
// google wire only because the provider declares it — no model-prefix guess.
func TestGeminiModelOnDeclaredGoogleProvider(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.GoogleTextDelta("detected"),
			fake.GoogleFinish("STOP"),
			fake.Done,
		},
		ContextWindow: 1_000_000,
	})
	dir := configDir(t, fmt.Sprintf("provider = \"google\"\n\n[providers.google]\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"gemini-flash\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "detected\n" {
		t.Errorf("stdout = %q, want %q", stdout, "detected\n")
	}
	reqs := p.Requests()
	// Two requests: the lazy model-info probe (GET, context window lookup)
	// followed by the chat POST.
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2 (model-info probe + chat)", len(reqs))
	}
	chat := chatRequest(reqs)
	if chat == nil {
		t.Fatalf("no chat POST among requests: %+v", reqs)
	}
	if !strings.HasPrefix(chat.Path, "/models/") {
		t.Errorf("chat path = %q, want /models/... (declared google provider)", chat.Path)
	}
}

// TestDeclaredWireWinsOverModelPrefix: the declared provider's wire serves the
// request even when the model's prefix suggests a different wire. The old
// GENIE_WIRE config override (and model-prefix auto-detection) is gone.
func TestDeclaredWireWinsOverModelPrefix(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{
			fake.AnthropicMessageStart(),
			fake.AnthropicTextDelta("from config"),
			fake.AnthropicMessageDelta("end_turn"),
			fake.Done,
		},
	})
	dir := configDir(t, fmt.Sprintf("provider = \"anthropic\"\n\n[providers.anthropic]\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"gpt-4o\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
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

// TestEnvProviderSelectsProvider: GENIE_PROVIDER names a declared provider and
// boots on it (env > file for the selection key, mirroring the old env-override
// role of GENIE_PROVIDER when it routed through plugins).
func TestEnvProviderSelectsProvider(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	dir := configDir(t, fmt.Sprintf("[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n", p.URL))
	stdout, stderr, code := run(t, []string{
		"XDG_CONFIG_HOME=" + dir,
		"GENIE_PROVIDER=zen",
		"OPENCODE_API_KEY=test-key",
	}, "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	assertRequestModel(t, p, "test-model", "Bearer test-key")
}

// TestUnknownProviderFailsStartup: a provider that no table declares fails at
// startup with an error naming it — no silent routing, no plugin fallback.
// The name must not be a shipped default; "copilot" became one with the
// in-tree bundled wire.
func TestUnknownProviderFailsStartup(t *testing.T) {
	dir := configDir(t, "provider = \"no-such-provider\"\n")
	stdout, stderr, code := run(t, []string{"XDG_CONFIG_HOME=" + dir}, "-p", "hi")
	assertCleanFailure(t, stdout, stderr, code, "no-such-provider")
}

// --- Non-interactive completion (genie-dea) ---

func TestStdinPipingReadsPrompt(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("got it"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := runWithStdin(t, "what is 2+2?", providerEnv(t, p), "-p")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "got it\n" {
		t.Errorf("stdout = %q, want %q", stdout, "got it\n")
	}
	// Verify the prompt was read from stdin by checking the request.
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Messages) < 2 {
		t.Fatalf("messages = %d, want >= 2", len(body.Messages))
	}
	if body.Messages[1].Role != "user" || body.Messages[1].Content != "what is 2+2?" {
		t.Errorf("user message = %+v, want user with content %q", body.Messages[1], "what is 2+2?")
	}
}

func TestStdinPipingMultiline(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	stdout, stderr, code := runWithStdin(t, "line one\nline two\nline three", providerEnv(t, p), "-p")
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
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Messages) < 2 {
		t.Fatalf("messages = %d, want >= 2", len(body.Messages))
	}
	want := "line one\nline two\nline three"
	if body.Messages[1].Content != want {
		t.Errorf("user message = %q, want %q", body.Messages[1].Content, want)
	}
}

func TestEmptyStdinExits3(t *testing.T) {
	stdout, stderr, code := runWithStdin(t, "", nil, "-p")
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

func TestStdinPipingAnswerToStdoutToolFramingToStderr(t *testing.T) {
	// Script a tool call followed by text.
	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if reqCount == 1 {
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
			for _, chunk := range []string{
				fake.TextDelta("file contents here"),
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

	stdout, stderr, code := runWithStdin(t, "read test.txt", []string{
		"XDG_CONFIG_HOME=" + configDir(t, providerConfigAt(srv.URL, "test-model")),
		"OPENCODE_API_KEY=test-key",
	}, "-p")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "file contents here") {
		t.Errorf("stdout = %q, want it to contain 'file contents here'", stdout)
	}
	// Tool framing should be on stderr, not stdout.
	if !strings.Contains(stderr, "── read ") {
		t.Errorf("stderr missing tool framing: %q", stderr)
	}
}

func TestLedgerPersistedInHeadlessMode(t *testing.T) {
	// Script a write tool call followed by text.
	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if reqCount == 1 {
			for _, chunk := range []string{
				fake.ToolCallDelta(0, "call_1", "write", `{"path":"output.txt","content":"hello world"}`),
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
				fake.TextDelta("wrote the file"),
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

	sessionDir := t.TempDir()
	workDir := t.TempDir()
	stdout, stderr, code := runInDir(t, workDir, []string{
		// Under the restrictive default write is unauthorized and headless
		// auto-denies; this test is about ledger recording, so authorize
		// writes up front.
		"XDG_CONFIG_HOME=" + configDir(t, providerConfigAt(srv.URL, "test-model")+"\n[permissions]\nwrite = [\".\"]\n"),
		"OPENCODE_API_KEY=test-key",
		"GENIE_SESSION_DIR=" + sessionDir,
	}, "-p", "write output.txt")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "wrote the file") {
		t.Errorf("stdout = %q, want it to contain 'wrote the file'", stdout)
	}

	// Check that a ledger file was created.
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var ledgerFile string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".changes.jsonl") {
			ledgerFile = e.Name()
			break
		}
	}
	if ledgerFile == "" {
		t.Fatalf("no ledger file in %v; files: %v", sessionDir, entries)
	}

	// Verify the ledger contains a batch with the write mutation.
	data, err := os.ReadFile(filepath.Join(sessionDir, ledgerFile))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "output.txt") {
		t.Errorf("ledger = %q, want it to reference output.txt", string(data))
	}
	if !strings.Contains(string(data), `"ops":"create"`) {
		t.Errorf("ledger = %q, want ops=create for new file", string(data))
	}
}

func TestExitCode0OnSuccess(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	_, _, code := run(t, providerEnv(t, p), "-p", "hi")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestExitCode1OnProviderError(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Error: &fake.Error{Status: 500, Body: `{"error":{"message":"internal error"}}`},
	})
	_, _, code := run(t, providerEnv(t, p), "-p", "hi")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestExitCode3OnUsageError(t *testing.T) {
	_, _, code := run(t, nil, "-p", "")
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestStdinPipingWithExplicitDash(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("piped"), fake.Finish("stop"), fake.Done},
	})
	// -p - is treated as -p (read from stdin); the "-" is not a value.
	stdout, _, code := runWithStdin(t, "hello via dash", providerEnv(t, p), "-p", "-")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stdout != "piped\n" {
		t.Errorf("stdout = %q, want %q", stdout, "piped\n")
	}
}

func TestStdinPipingEmptyDashExits3(t *testing.T) {
	// -p - with empty stdin reads stdin (empty) → exit 3.
	stdout, stderr, code := runWithStdin(t, "", nil, "-p", "-")
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

// TestContextUsageLoggedAgainstBudget is the genie-br8 acceptance at the binary
// seam: with a per-model context-window override, the debug log reports the
// outgoing request's token count against the authoritative window and the
// default 75% budget.
func TestContextUsageLoggedAgainstBudget(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfg := fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n\n[context.windows]\n\"test-model\" = 1000\n", p.URL)
	dir := configDir(t, cfg)
	env := []string{"XDG_CONFIG_HOME=" + dir, "OPENCODE_API_KEY=test-key"}
	stdout, stderr, code := run(t, env, "-d", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ok\n")
	}
	if !strings.Contains(stderr, "context usage") {
		t.Errorf("stderr = %q, want a context usage debug line", stderr)
	}
	if !strings.Contains(stderr, "window=1000") {
		t.Errorf("stderr = %q, want window=1000 from the config override", stderr)
	}
	// Default budget: 75% of the 1000-token window.
	if !strings.Contains(stderr, "budget=750") {
		t.Errorf("stderr = %q, want budget=750 (75%% of window)", stderr)
	}
	// The outgoing request's token count is reported and non-zero.
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "context usage") {
			if !strings.Contains(line, "tokens=") {
				t.Errorf("context usage line %q missing tokens=", line)
			}
			break
		}
	}
}

// TestContextBudgetTokensOverrideWins is the genie-br8 acceptance that an
// absolute budget_tokens config wins over the percent-of-window derivation.
func TestContextBudgetTokensOverrideWins(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfg := fmt.Sprintf("provider = \"zen\"\n\n[providers.zen]\nbase_url = %q\nmodel = \"test-model\"\n\n[context]\nbudget_tokens = 123\n\n[context.windows]\n\"test-model\" = 1000\n", p.URL)
	dir := configDir(t, cfg)
	env := []string{"XDG_CONFIG_HOME=" + dir, "OPENCODE_API_KEY=test-key"}
	_, stderr, code := run(t, env, "-d", "-p", "hi")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "budget=123") {
		t.Errorf("stderr = %q, want budget=123 (absolute budget_tokens wins)", stderr)
	}
}

// TestStartsOnActiveProvidersDefaultModel is the og-z1m.3 acceptance at the
// binary seam: of two declared providers, the selected one's own default model
// is what starts — never another provider's model, never a global fallback.
func TestStartsOnActiveProvidersDefaultModel(t *testing.T) {
	p := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("ok"), fake.Finish("stop"), fake.Done},
	})
	cfg := fmt.Sprintf("provider = \"other\"\n\n"+
		"[providers.zen]\nbase_url = %q\nmodel = \"zen-model\"\n\n"+
		"[providers.other]\nwire = \"openai\"\nbase_url = %q\napi_key_env = \"OPENCODE_API_KEY\"\nmodel = \"other-model\"\n",
		p.URL, p.URL)
	dir := configDir(t, cfg)
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
	assertRequestModel(t, p, "other-model", "Bearer test-key")
}

// TestProviderSwitchMidSessionKeepsTranscript verifies a mid-session /provider
// switch: the client is rebuilt against the new provider (key, default model),
// the same session and transcript continue, and an unknown name names the
// available set without touching the session.
func TestProviderSwitchMidSessionKeepsTranscript(t *testing.T) {
	zen := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("zen reply"), fake.Finish("stop"), fake.Done},
	})
	other := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("other reply"), fake.Finish("stop"), fake.Done},
	})

	dir := configDir(t, fmt.Sprintf(`
provider = "zen"

[providers.zen]
base_url = %q
model = "zen-model"

[providers.openai]
base_url = %q
api_key_env = "OTHER_KEY"
model = "other-model"
`, zen.URL, other.URL))

	stdout, stderr, code := runInDirWithStdin(t, dir, "hello zen\n/provider openai\nhello other\n/quit\n", []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=zen-key",
		"OTHER_KEY=other-key",
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "provider: openai (model: other-model)") {
		t.Errorf("stdout = %q, want switch message", stdout)
	}
	if !strings.Contains(stdout, "other reply") {
		t.Errorf("stdout = %q, want the post-switch turn streamed", stdout)
	}

	// The post-switch turn went to the new provider with its key and default
	// model; the old provider saw only its pre-switch turn.
	oreqs := other.Requests()
	if len(oreqs) != 1 {
		t.Fatalf("openai (other) requests = %d, want 1", len(oreqs))
	}
	if oreqs[0].Auth != "Bearer other-key" {
		t.Errorf("other auth = %q, want the new provider's key", oreqs[0].Auth)
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(oreqs[0].Body), &body); err != nil {
		t.Fatalf("decode other request: %v", err)
	}
	if body.Model != "other-model" {
		t.Errorf("other request model = %q, want the new provider's default other-model", body.Model)
	}
	if got := len(zen.Requests()); got != 1 {
		t.Errorf("zen requests = %d, want 1 (only the pre-switch turn)", got)
	}

	// The transcript continues across the switch: the new provider's turn
	// request carries the whole conversation, both prompts and the zen reply.
	msgs := decodeTurnMessages(t, oreqs[0].Body)
	var sawZenTurn, sawZenReply, sawOtherTurn bool
	for _, m := range msgs {
		switch {
		case m.Role == "user" && m.Content == "hello zen":
			sawZenTurn = true
		case m.Role == "assistant" && m.Content == "zen reply":
			sawZenReply = true
		case m.Role == "user" && m.Content == "hello other":
			sawOtherTurn = true
		}
	}
	if !sawZenTurn || !sawZenReply || !sawOtherTurn {
		t.Errorf("other turn request does not carry the whole transcript: %+v", msgs)
	}
}

// TestProviderUnknownNamesAvailableSet verifies an unknown /provider name
// prints an error naming the available set and a later turn still runs on the
// boot provider.
func TestProviderUnknownNamesAvailableSet(t *testing.T) {
	zen := scriptedProvider(t, fake.Behavior{
		Chunks: []string{fake.TextDelta("zen reply"), fake.Finish("stop"), fake.Done},
	})

	dir := configDir(t, fmt.Sprintf(`
provider = "zen"

[providers.zen]
base_url = %q
model = "zen-model"
`, zen.URL))

	stdout, stderr, code := runInDirWithStdin(t, dir, "/provider nosuch\nhi\n/quit\n", []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=k",
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "no such provider: nosuch") || !strings.Contains(stdout, "(available: anthropic, bedrock, copilot, google, openai, responses, zen)") {
		t.Errorf("stdout = %q, want an error naming the available set", stdout)
	}
	if !strings.Contains(stdout, "zen reply") {
		t.Errorf("stdout = %q, want the turn to run after the failed switch", stdout)
	}
	if got := len(zen.Requests()); got != 1 {
		t.Errorf("zen requests = %d, want 1 (failed switch must not rebuild the client)", got)
	}
}

// TestProviderAndModelCommands drives the /provider and /model surface
// end-to-end against declared catalogs (no provider server involved): the
// listing marks the current provider, /model shows only the active provider's
// catalog, and after a switch the current marker follows the new provider's
// default model.
func TestProviderAndModelCommands(t *testing.T) {
	dir := configDir(t, `
provider = "zen"

[providers.zen]
base_url = "http://127.0.0.1:1"
model = "zen-default"
models = ["zen-default", "zen-1"]

[providers.openai]
base_url = "http://127.0.0.1:1"
model = "openai-default"
models = ["openai-default", "openai-1"]
`)

	stdout, stderr, code := runInDirWithStdin(t, dir, "/provider\n/model\n/provider openai\n/model\n/quit\n", []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=k",
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}

	out := stdout
	if !strings.Contains(out, "Available providers:") {
		t.Errorf("stdout = %q, want providers listing", out)
	}
	if !strings.Contains(out, "* zen") || !strings.Contains(out, "  openai") {
		t.Errorf("stdout = %q, want zen marked current and openai unmarked", out)
	}
	if !strings.Contains(out, "Current: zen") {
		t.Errorf("stdout = %q, want 'Current: zen'", out)
	}

	// The pre-switch /model lists only zen's catalog.
	preSwitch := out[:strings.Index(out, "provider: openai")]
	if !strings.Contains(preSwitch, "zen-1") {
		t.Errorf("stdout = %q, want zen's catalog on the first /model", out)
	}
	if strings.Contains(preSwitch, "openai-1") {
		t.Errorf("stdout = %q, must not list openai's catalog before the switch", out)
	}

	if !strings.Contains(out, "provider: openai (model: openai-default)") {
		t.Errorf("stdout = %q, want switch message", out)
	}

	// After the switch, /model lists only openai's catalog and marks openai's
	// default — the active provider's model, never a stale global.
	idx := strings.Index(out, "provider: openai")
	after := out[idx:]
	if !strings.Contains(after, "openai-1") {
		t.Errorf("post-switch /model = %q, want openai's catalog", after)
	}
	if !strings.Contains(after, "* openai-default") {
		t.Errorf("post-switch /model = %q, want openai's default marked current", after)
	}
	if strings.Contains(after, "zen-") {
		t.Errorf("post-switch /model = %q, must not list the old provider's catalog", after)
	}
}

// writeToolThenText starts a provider that answers odd requests with a write
// tool call and even requests with a text reply, recording request bodies.
func writeToolThenText(t *testing.T, path, content, text string) (*httptest.Server, *[]string) {
	t.Helper()
	return toolCallThenText(t, "write", map[string]string{"path": path, "content": content}, text)
}

// toolCallThenText starts a provider that answers odd requests with one tool
// call and even requests with a text reply, recording request bodies.
func toolCallThenText(t *testing.T, toolName string, args map[string]string, text string) (*httptest.Server, *[]string) {
	t.Helper()
	return toolCallsThenText(t, toolName, []map[string]string{args}, text)
}

// toolCallsThenText starts a provider that answers the i-th odd request with
// the i-th tool call (cycling when exhausted) and even requests with a text
// reply, recording request bodies. callID is deterministic so grants and the
// request tracking in tests stay comparable.
func toolCallsThenText(t *testing.T, toolName string, calls []map[string]string, text string) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	rawCalls := make([][]byte, len(calls))
	for i, args := range calls {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		rawCalls[i] = raw
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		var chunks []string
		if len(bodies)%2 == 1 {
			raw := rawCalls[(len(bodies)-1)/2%len(rawCalls)]
			chunks = []string{
				fake.ToolCallDelta(0, "call_1", toolName, string(raw)),
				fake.Finish("tool_calls"),
				fake.Done,
			}
		} else {
			chunks = []string{fake.TextDelta(text), fake.Finish("stop"), fake.Done}
		}
		for _, chunk := range chunks {
			io.WriteString(w, "data: "+chunk+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

// TestInteractiveWriteEscalationSessionGrant drives the tracer end-to-end:
// under the restrictive default a write escalates, the user grants it for the
// session, the call executes, and a second write to the same scope does not
// re-prompt.
func TestInteractiveWriteEscalationSessionGrant(t *testing.T) {
	srv, bodies := writeToolThenText(t, "output.txt", "hello world", "wrote the file")
	workDir := t.TempDir()

	stdout, stderr, code := runInDirWithStdin(t, workDir, "write output.txt\ns\nwrite output.txt\n/quit\n", []string{
		"XDG_CONFIG_HOME=" + configDir(t, providerConfigAt(srv.URL, "test-model")),
		"OPENCODE_API_KEY=test-key",
		"GENIE_SESSION_DIR=" + t.TempDir(),
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "allow write ") || !strings.Contains(stdout, "output.txt? (o)nce/(s)ession/(p)ermanent/(r)eject:") {
		t.Errorf("stdout = %q, want the escalation prompt", stdout)
	}
	if !strings.Contains(stdout, "wrote the file") {
		t.Errorf("stdout = %q, want the model's follow-up text", stdout)
	}
	if n := strings.Count(stdout, "allow write "); n != 1 {
		t.Errorf("stdout prompted %d times, want 1 (session grant covers the second write)", n)
	}
	got, err := os.ReadFile(filepath.Join(workDir, "output.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("file = %q, want %q", got, "hello world")
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "Permission granted: write") {
		t.Errorf("follow-up request did not carry the grant line; bodies=%v", *bodies)
	}
}

// TestHeadlessWriteAutoDenied: with no one to ask, -p denies the uncovered
// write, never runs it, and feeds the model the composite denied result.
func TestHeadlessWriteAutoDenied(t *testing.T) {
	srv, bodies := writeToolThenText(t, "output.txt", "hello world", "done")
	workDir := t.TempDir()

	_, stderr, code := runInDir(t, workDir, []string{
		"XDG_CONFIG_HOME=" + configDir(t, providerConfigAt(srv.URL, "test-model")),
		"OPENCODE_API_KEY=test-key",
		"GENIE_SESSION_DIR=" + t.TempDir(),
	}, "-p", "write output.txt")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(workDir, "output.txt")); !os.IsNotExist(err) {
		t.Errorf("output.txt exists (err=%v), want it never created", err)
	}
	if len(*bodies) < 2 {
		t.Fatalf("requests = %d, want the denied result fed back", len(*bodies))
	}
	if !strings.Contains((*bodies)[1], "status: call not executed") {
		t.Errorf("follow-up request missing denied status; body=%s", (*bodies)[1])
	}
	if !strings.Contains((*bodies)[1], "hint: granted axes remain available") {
		t.Errorf("follow-up request missing deny hint; body=%s", (*bodies)[1])
	}
}

// TestPermanentGrantPersistsAcrossRuns: answering "p" writes a permanent grant
// to config; a later run honors it with no prompt.
func TestPermanentGrantPersistsAcrossRuns(t *testing.T) {
	srv, _ := writeToolThenText(t, "output.txt", "hello world", "wrote the file")
	workDir := t.TempDir()
	dir := configDir(t, providerConfigAt(srv.URL, "test-model"))
	env := []string{
		"XDG_CONFIG_HOME=" + dir,
		"OPENCODE_API_KEY=test-key",
		"GENIE_SESSION_DIR=" + t.TempDir(),
	}

	stdout, stderr, code := runInDirWithStdin(t, workDir, "write output.txt\np\n/quit\n", env)
	if code != 0 {
		t.Fatalf("run 1: exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "allow write ") {
		t.Errorf("run 1 stdout = %q, want an escalation prompt", stdout)
	}

	// The grant was appended to the config file.
	data, err := os.ReadFile(filepath.Join(dir, "genie", "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile config: %v", err)
	}
	if !strings.Contains(string(data), "[[permissions.permanent]]") {
		t.Errorf("config = %q, want a persisted permanent grant", data)
	}

	// Second run: no prompt and no grant input; the write is covered.
	os.Remove(filepath.Join(workDir, "output.txt"))
	stdout2, stderr2, code := runInDirWithStdin(t, workDir, "write output.txt\n/quit\n", env)
	if code != 0 {
		t.Fatalf("run 2: exit code = %d, want 0; stderr=%q", code, stderr2)
	}
	if strings.Contains(stdout2, "allow write ") {
		t.Errorf("run 2 stdout = %q, must not re-prompt for a permanent grant", stdout2)
	}
	if _, err := os.Stat(filepath.Join(workDir, "output.txt")); err != nil {
		t.Errorf("output.txt not written on run 2: %v", err)
	}

	// Third run is headless (-p, auto-deny): the persisted permanent grant is
	// the only reason the write executes.
	os.Remove(filepath.Join(workDir, "output.txt"))
	_, stderr3, code := runInDir(t, workDir, env, "-p", "write output.txt")
	if code != 0 {
		t.Fatalf("run 3: exit code = %d, want 0; stderr=%q", code, stderr3)
	}
	if _, err := os.Stat(filepath.Join(workDir, "output.txt")); err != nil {
		t.Errorf("output.txt not written on headless run 3: %v", err)
	}
}

// TestInteractiveWriteOnceSpentPromptsAgain: a once grant is consumed by the
// call it authorized, so the same scope prompts again on the next write.
func TestInteractiveWriteOnceSpentPromptsAgain(t *testing.T) {
	srv, _ := writeToolThenText(t, "output.txt", "hello world", "wrote the file")
	workDir := t.TempDir()

	stdout, stderr, code := runInDirWithStdin(t, workDir, "write output.txt\no\nwrite output.txt\ns\n/quit\n", []string{
		"XDG_CONFIG_HOME=" + configDir(t, providerConfigAt(srv.URL, "test-model")),
		"OPENCODE_API_KEY=test-key",
		"GENIE_SESSION_DIR=" + t.TempDir(),
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if n := strings.Count(stdout, "allow write "); n != 2 {
		t.Errorf("stdout prompted %d times, want 2 (once is spent)", n)
	}
}

// runSIGINTAtPrompt starts the binary, sends initial on stdin, waits until
// marker appears on stdout, sends SIGINT, waits for followup, then quits and
// returns stdout, stderr and the exit code. Stdin is deliberately left open
// until followup so the signal alone resolves the escalation prompt; closing
// it in the same instant as the signal lets the negotiator reject on EOF first
// and misroutes the in-flight SIGINT to the turn.
func runSIGINTAtPrompt(t *testing.T, dir string, env []string, initial, marker, followup string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binPath)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := io.WriteString(in, initial); err != nil {
		t.Fatalf("write initial stdin: %v", err)
	}

	var outBuf bytes.Buffer
	signaled := false
	quit := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(stdoutPipe)
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return
			}
			outBuf.WriteByte(b)
			if !signaled && strings.Contains(outBuf.String(), marker) {
				signaled = true
				cmd.Process.Signal(os.Interrupt)
			}
			if signaled && !quit && strings.Contains(outBuf.String(), followup) {
				quit = true
				io.WriteString(in, "/quit\n")
				in.Close()
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("timed out waiting for the process to exit; stdout=%q stderr=%q", outBuf.String(), stderrBuf.String())
	}
	err = cmd.Wait()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("Wait: %v", err)
		}
	}
	return outBuf.String(), stderrBuf.String(), code
}

// TestInteractiveCtrlCRejectsAxis: ^C while the escalation prompt is live
// rejects the current axis without cancelling the turn; the call never runs
// and the model gets the denied composite.
func TestInteractiveCtrlCRejectsAxis(t *testing.T) {
	srv, bodies := writeToolThenText(t, "output.txt", "hello world", "no file written")
	workDir := t.TempDir()

	stdout, stderr, code := runSIGINTAtPrompt(t, workDir, []string{
		"XDG_CONFIG_HOME=" + configDir(t, providerConfigAt(srv.URL, "test-model")),
		"OPENCODE_API_KEY=test-key",
		"GENIE_SESSION_DIR=" + t.TempDir(),
	}, "write output.txt\n", "? (o)nce/(s)ession/(p)ermanent/(r)eject: ", "no file written")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "allow write ") {
		t.Errorf("stdout = %q, want the escalation prompt", stdout)
	}
	if strings.Contains(stderr, "[turn cancelled]") {
		t.Errorf("stderr = %q, want the turn to survive the ^C", stderr)
	}
	if !strings.Contains(stdout, "no file written") {
		t.Errorf("stdout = %q, want the model's follow-up text after the rejection", stdout)
	}
	if _, err := os.Stat(filepath.Join(workDir, "output.txt")); !os.IsNotExist(err) {
		t.Errorf("output.txt exists (err=%v), want it never created", err)
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "Permission rejected: write") {
		t.Errorf("follow-up request missing the reject line; bodies=%v", *bodies)
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "status: call not executed") {
		t.Errorf("follow-up request missing denied status; bodies=%v", *bodies)
	}
}

// permissionEnv builds the env for a permission e2e: a config dir pointing at
// the scripted provider and a scratch session dir.
func permissionEnv(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	return []string{
		"XDG_CONFIG_HOME=" + configDir(t, providerConfigAt(srv.URL, "test-model")),
		"OPENCODE_API_KEY=test-key",
		"GENIE_SESSION_DIR=" + t.TempDir(),
	}
}

// TestReadInsideBaseScopeRunsSilently: reads under the working tree are inside
// the restrictive default base (read=["."]), so an in-tree read never pauses
// the turn for negotiation.
func TestReadInsideBaseScopeRunsSilently(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "in.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, _ := toolCallThenText(t, "read", map[string]string{"path": "in.txt"}, "read the file")

	stdout, stderr, code := runInDirWithStdin(t, workDir, "read in.txt\n/quit\n", permissionEnv(t, srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if strings.Contains(stdout, "allow read ") {
		t.Errorf("stdout = %q, want no escalation prompt for an in-tree read", stdout)
	}
	if !strings.Contains(stdout, "read the file") {
		t.Errorf("stdout = %q, want the model's follow-up text", stdout)
	}
}

// TestReadOutsideBaseScopeEscalates: a read outside the read base (deliberately
// ungated before og-uy5.2) escalates with the terse prompt, like a write does.
func TestReadOutsideBaseScopeEscalates(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(outside, []byte("outside data"), 0o644); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	srv, _ := toolCallThenText(t, "read", map[string]string{"path": outside}, "read the outside file")

	stdout, stderr, code := runInDirWithStdin(t, workDir, "read "+outside+"\ns\n/quit\n", permissionEnv(t, srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "allow read "+outside+"? (o)nce/(s)ession/(p)ermanent/(r)eject:") {
		t.Errorf("stdout = %q, want the terse read escalation prompt for the outside scope", stdout)
	}
	if !strings.Contains(stdout, "read the outside file") {
		t.Errorf("stdout = %q, want the model's follow-up text after the grant", stdout)
	}
}

// TestReadSessionGrantReusedNotReprompted: a session-tier read grant covers a
// later read of the same scope — the granted scope is reusable and not
// re-prompted (reads now respect the effective policy: base + grants).
func TestReadSessionGrantReusedNotReprompted(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(outside, []byte("outside data"), 0o644); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	srv, _ := toolCallThenText(t, "read", map[string]string{"path": outside}, "read the outside file")

	stdout, stderr, code := runInDirWithStdin(t, workDir, "read "+outside+"\ns\nread "+outside+"\n/quit\n", permissionEnv(t, srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if n := strings.Count(stdout, "allow read "); n != 1 {
		t.Errorf("stdout prompted %d times, want 1 (session grant covers the second read)", n)
	}
	if !strings.Contains(stdout, "read the outside file") {
		t.Errorf("stdout = %q, want the model's follow-up text", stdout)
	}
}

// TestHeadlessReadOutsideBaseAutoDenied: with no one to ask, -p denies an
// uncovered read and feeds the model the composite denied result — reads are
// no longer silently ungated.
func TestHeadlessReadOutsideBaseAutoDenied(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	srv, bodies := toolCallThenText(t, "read", map[string]string{"path": outside}, "done")

	_, stderr, code := runInDir(t, workDir, permissionEnv(t, srv), "-p", "read the outside file")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if len(*bodies) < 2 {
		t.Fatalf("requests = %d, want the denied read fed back", len(*bodies))
	}
	if !strings.Contains((*bodies)[1], "Permission rejected: read "+outside) {
		t.Errorf("follow-up request missing the read reject line; body=%s", (*bodies)[1])
	}
	if !strings.Contains((*bodies)[1], "status: call not executed") {
		t.Errorf("follow-up request missing denied status; body=%s", (*bodies)[1])
	}
}

// TestEditOutsideBasePromptsBothAxesInOrder: an edit outside both the read and
// write base escalates the chained per-axis flow (read → write) within the
// single call, and runs only when both axes are granted.
func TestEditOutsideBasePromptsBothAxesInOrder(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	srv, _ := toolCallThenText(t, "edit", map[string]string{
		"path": outside, "oldText": "original", "newText": "edited",
	}, "edited the file")

	stdout, stderr, code := runInDirWithStdin(t, workDir, "edit "+outside+"\ns\ns\n/quit\n", permissionEnv(t, srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	readAt := strings.Index(stdout, "allow read ")
	writeAt := strings.Index(stdout, "allow write ")
	if readAt < 0 {
		t.Errorf("stdout = %q, want a read escalation prompt", stdout)
	}
	if writeAt < 0 {
		t.Errorf("stdout = %q, want a write escalation prompt", stdout)
	}
	if readAt >= 0 && writeAt >= 0 && readAt > writeAt {
		t.Errorf("stdout = %q, want read prompted before write (fixed axis order)", stdout)
	}
	if !strings.Contains(stdout, "edited the file") {
		t.Errorf("stdout = %q, want the model's follow-up text", stdout)
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "edited" {
		t.Errorf("file = %q, want %q (edit ran when both axes were granted)", got, "edited")
	}
}

// TestEditInBasePromptsOnlyWrite: the read axis is already covered by the base
// for an in-tree file, so an edit escalates only the uncovered write axis.
func TestEditInBasePromptsOnlyWrite(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "notes.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, _ := toolCallThenText(t, "edit", map[string]string{
		"path": "notes.txt", "oldText": "original", "newText": "edited",
	}, "edited the file")

	stdout, stderr, code := runInDirWithStdin(t, workDir, "edit notes.txt\ns\n/quit\n", permissionEnv(t, srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if strings.Contains(stdout, "allow read ") {
		t.Errorf("stdout = %q, want no read prompt (base already covers it)", stdout)
	}
	if n := strings.Count(stdout, "allow write "); n != 1 {
		t.Errorf("stdout prompted write %d times, want 1", n)
	}
	got, err := os.ReadFile(filepath.Join(workDir, "notes.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "edited" {
		t.Errorf("file = %q, want %q", got, "edited")
	}
}

// TestEditFullyCoveredRunsSilently: once both axes on a scope are granted, a
// later edit of that scope runs without any prompt.
func TestEditFullyCoveredRunsSilently(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "notes.txt"), []byte("a b"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, _ := toolCallsThenText(t, "edit", []map[string]string{
		{"path": "notes.txt", "oldText": "a", "newText": "A"},
		{"path": "notes.txt", "oldText": "b", "newText": "B"},
	}, "edited the file")

	// The first edit grants session-tier write (read is base-covered); the
	// second edit is fully covered and must not prompt.
	stdout, stderr, code := runInDirWithStdin(t, workDir, "edit notes.txt\ns\nedit notes.txt\n/quit\n", permissionEnv(t, srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if n := strings.Count(stdout, "allow write "); n != 1 {
		t.Errorf("stdout prompted write %d times, want 1", n)
	}
	if strings.Contains(stdout, "allow read ") {
		t.Errorf("stdout = %q, want no read prompt", stdout)
	}
	got, err := os.ReadFile(filepath.Join(workDir, "notes.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "A B" {
		t.Errorf("file = %q, want %q (both edits applied)", got, "A B")
	}
}

// TestEditRejectedOnWriteAxisDoesNotExecute: rejecting the write axis on an
// in-tree edit denies the call overall — the file is untouched and the model
// gets the composite denied result.
func TestEditRejectedOnWriteAxisDoesNotExecute(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "notes.txt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, bodies := toolCallThenText(t, "edit", map[string]string{
		"path": "notes.txt", "oldText": "original", "newText": "edited",
	}, "no edit")

	// Read is base-covered, so the single prompt is the write axis; rejecting
	// it must never reach the edit tool.
	stdout, stderr, code := runInDirWithStdin(t, workDir, "edit notes.txt\nr\n/quit\n", permissionEnv(t, srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "allow write ") {
		t.Errorf("stdout = %q, want the write escalation prompt", stdout)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("file = %q, want %q (edit must not execute on rejection)", got, "original")
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "Permission rejected: write "+filepath.Join(workDir, "notes.txt")) {
		t.Errorf("follow-up request missing the write reject line; bodies=%v", *bodies)
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "status: call not executed") {
		t.Errorf("follow-up request missing denied status; bodies=%v", *bodies)
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "hint: granted axes remain available") {
		t.Errorf("follow-up request missing deny hint; bodies=%v", *bodies)
	}
}

// TestEditRejectedOnReadAxisDoesNotExecute: the "either axis" counterpart — an
// edit outside the read base, read rejected. The chain continues to write
// (granted) but the call stays denied: the composite carries the granted write
// and the rejected read, and the tool never runs.
func TestEditRejectedOnReadAxisDoesNotExecute(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	srv, bodies := toolCallThenText(t, "edit", map[string]string{
		"path": outside, "oldText": "original", "newText": "edited",
	}, "no edit")

	stdout, stderr, code := runInDirWithStdin(t, workDir, "edit "+outside+"\nr\ns\n/quit\n", permissionEnv(t, srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "allow read ") {
		t.Errorf("stdout = %q, want the read escalation prompt", stdout)
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("file = %q, want %q (edit must not execute when read is rejected)", got, "original")
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "Permission rejected: read "+outside) {
		t.Errorf("follow-up request missing the read reject line; bodies=%v", *bodies)
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "Permission granted: write "+outside) {
		t.Errorf("follow-up request missing the granted write line; bodies=%v", *bodies)
	}
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], "status: call not executed") {
		t.Errorf("follow-up request missing denied status; bodies=%v", *bodies)
	}
}
