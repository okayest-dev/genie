package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/e2e/fake"
)

func TestDebugProvider(t *testing.T) {
	p := fake.New()
	defer p.Close()
	
	p.SetBehaviors(
		fake.Behavior{ContextWindow: 128000},
		fake.Behavior{
			Chunks: []string{
				fake.ToolCallDelta(0, "call_1", "task_claim", `{"task_id":"og-1"}`),
				fake.Finish("tool_calls"),
				fake.Done,
			},
		},
		fake.Behavior{
			Chunks: []string{
				fake.TextDelta("done"),
				fake.Finish("stop"),
				fake.Done,
			},
		},
	)
	
	fmt.Printf("Provider URL: %s\n", p.URL)
	
	// Test GET /models
	resp, err := http.Get(p.URL + "/models/test-model")
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
	defer resp.Body.Close()
	fmt.Printf("GET /models status: %d\n", resp.StatusCode)
	
	// Test POST /chat/completions
	body := `{"model":"test-model","messages":[{"role":"user","content":"test"}],"stream":true}`
	req, _ := http.NewRequest("POST", p.URL+"/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	
	client := &http.Client{Timeout: 5 * time.Second}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
	defer resp2.Body.Close()
	fmt.Printf("POST /chat/completions status: %d\n", resp2.StatusCode)
	
	// Read response
	buf := make([]byte, 4096)
	n, _ := resp2.Body.Read(buf)
	fmt.Printf("Response: %s\n", string(buf[:n]))
}
