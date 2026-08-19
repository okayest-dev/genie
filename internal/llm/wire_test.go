package llm

import "testing"

func TestDetectWire(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		want    string
	}{
		{name: "claude prefix", modelID: "claude-sonnet-4-20250514", want: "anthropic"},
		{name: "claude prefix shorter", modelID: "claude-3-haiku", want: "anthropic"},
		{name: "gpt prefix", modelID: "gpt-4o", want: "responses"},
		{name: "gpt prefix older", modelID: "gpt-3.5-turbo", want: "responses"},
		{name: "gemini prefix", modelID: "gemini-2.0-flash", want: "google"},
		{name: "gemini prefix pro", modelID: "gemini-2.5-pro", want: "google"},
		{name: "unknown falls back to openai", modelID: "big-pickle", want: "openai"},
		{name: "empty falls back to openai", modelID: "", want: "openai"},
		{name: "llama falls back to openai", modelID: "llama-3.1-405b", want: "openai"},
		{name: "mistral falls back to openai", modelID: "mistral-large", want: "openai"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectWire(tc.modelID)
			if got != tc.want {
				t.Errorf("DetectWire(%q) = %q, want %q", tc.modelID, got, tc.want)
			}
		})
	}
}
