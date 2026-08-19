package llm

import "strings"

// Wire is a named wire protocol implementation.
type Wire = string

const (
	WireOpenAI          Wire = "openai"
	WireAnthropic       Wire = "anthropic"
	WireOpenAIResponses Wire = "responses"
	WireGoogle          Wire = "google"
)

// DetectWire returns the wire for a given model ID by prefix matching.
// claude-* → anthropic, gpt-* → responses, gemini-* → google, else → openai.
func DetectWire(modelID string) Wire {
	switch {
	case strings.HasPrefix(modelID, "claude-"):
		return WireAnthropic
	case strings.HasPrefix(modelID, "gpt-"):
		return WireOpenAIResponses
	case strings.HasPrefix(modelID, "gemini-"):
		return WireGoogle
	default:
		return WireOpenAI
	}
}

// ValidWires is the set of registered wire names. Populated by RegisterWire.
var ValidWires = map[Wire]bool{}
