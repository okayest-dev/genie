package llm_test

import (
	"testing"

	"github.com/okayest-dev/og/internal/llm"
	_ "github.com/okayest-dev/og/internal/llm/anthropic"
	_ "github.com/okayest-dev/og/internal/llm/google"
	_ "github.com/okayest-dev/og/internal/llm/openai"
	_ "github.com/okayest-dev/og/internal/llm/responses"
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
	_, err := llm.NewClient("nonexistent", "https://example.com/v1", "key")
	if err == nil {
		t.Fatal("NewClient with unknown wire should return error")
	}
}

func TestNewClientAnthropic(t *testing.T) {
	c, err := llm.NewClient(llm.WireAnthropic, "https://example.com/v1", "key")
	if err != nil {
		t.Fatalf("NewClient(%q): %v", llm.WireAnthropic, err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client")
	}
}

func TestNewClientGoogle(t *testing.T) {
	c, err := llm.NewClient(llm.WireGoogle, "https://example.com/v1", "key")
	if err != nil {
		t.Fatalf("NewClient(%q): %v", llm.WireGoogle, err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client")
	}
}

func TestNewClientOpenAIResponses(t *testing.T) {
	c, err := llm.NewClient(llm.WireOpenAIResponses, "https://example.com/v1", "key")
	if err != nil {
		t.Fatalf("NewClient(%q): %v", llm.WireOpenAIResponses, err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client")
	}
}
