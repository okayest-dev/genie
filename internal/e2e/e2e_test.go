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
	t.Helper()
	cmd := exec.Command(binPath, args...)
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
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "hi" {
		t.Errorf("request messages = %+v, want single user message %q", body.Messages, "hi")
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
