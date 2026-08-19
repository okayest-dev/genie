package llm_test

import (
	"testing"

	"github.com/okayest-dev/og/internal/llm"
	_ "github.com/okayest-dev/og/internal/llm/openai"
)

func TestNewClientOpenAI(t *testing.T) {
	c, err := llm.NewClient(llm.WireOpenAI, "https://example.com/v1", "key")
	if err != nil {
		t.Fatalf("NewClient(%q): %v", llm.WireOpenAI, err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client")
	}
}

func TestNewClientEmptyDefaultsToOpenAI(t *testing.T) {
	c, err := llm.NewClient("", "https://example.com/v1", "key")
	if err != nil {
		t.Fatalf("NewClient(\"\"): %v", err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client")
	}
}

func TestNewClientUnknownWireReturnsError(t *testing.T) {
	_, err := llm.NewClient("anthropic", "https://example.com/v1", "key")
	if err == nil {
		t.Fatal("NewClient with unknown wire should return error")
	}
}
