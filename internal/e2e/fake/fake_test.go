package fake

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// post streams one chat-completions request and returns the SSE body.
func post(t *testing.T, p *Provider, body string) string {
	t.Helper()
	resp, err := http.Post(p.URL+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	all, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(all)
}

// TestSetBehaviorsServedInOrderThenSticks pins the queue semantics: each
// request consumes the next behavior, and once the queue exhausts the last
// behavior keeps serving.
func TestSetBehaviorsServedInOrderThenSticks(t *testing.T) {
	p := New()
	defer p.Close()
	p.SetBehaviors(
		Behavior{Chunks: []string{TextDelta("first"), Finish("stop"), Done}},
		Behavior{Chunks: []string{TextDelta("second"), Finish("stop"), Done}},
		Behavior{Chunks: []string{TextDelta("third"), Finish("stop"), Done}},
	)

	for turn, want := range []string{"first", "second", "third", "third"} {
		got := post(t, p, `{"messages":[{"role":"user","content":"q"}]}`)
		if !strings.Contains(got, want) {
			t.Errorf("request %d body = %q, want it to contain %q", turn+1, got, want)
		}
	}

	reqs := p.Requests()
	if len(reqs) != 4 {
		t.Errorf("requests recorded = %d, want 4", len(reqs))
	}
}

// TestSetBehaviorSticky pins back-compat: a single behavior serves every
// request without being consumed.
func TestSetBehaviorSticky(t *testing.T) {
	p := New()
	defer p.Close()
	p.SetBehavior(Behavior{Chunks: []string{TextDelta("same"), Finish("stop"), Done}})

	for i := 0; i < 3; i++ {
		got := post(t, p, `{"messages":[{"role":"user","content":"q"}]}`)
		if !strings.Contains(got, "same") {
			t.Errorf("request %d body = %q, want it to contain \"same\"", i+1, got)
		}
	}
}

// TestNoBehaviorServesEmpty covers an un-scripted provider: a request before
// any SetBehavior yields a bare 200 with no streamed chunks.
func TestNoBehaviorServesEmpty(t *testing.T) {
	p := New()
	defer p.Close()
	got := post(t, p, `{"messages":[{"role":"user","content":"q"}]}`)
	if got != "" {
		t.Errorf("body = %q, want empty", got)
	}
}
