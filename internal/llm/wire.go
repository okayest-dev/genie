package llm

// Wire is a named wire protocol implementation.
type Wire = string

const (
	WireOpenAI          Wire = "openai"
	WireAnthropic       Wire = "anthropic"
	WireOpenAIResponses Wire = "responses"
	WireGoogle          Wire = "google"
	WireCopilot         Wire = "copilot"
	WireBedrock         Wire = "bedrock"
)

// WireNames returns every bundled wire name, built from the Wire* constants.
// It is the one list the config validates a provider's wire against and the
// one a new wire joins, alongside its constant — so the harness's wire
// vocabulary has a single home. A wire reaches this list when it ships, not
// when a subpackage happens to register it.
func WireNames() []string {
	return []string{WireOpenAI, WireAnthropic, WireOpenAIResponses, WireGoogle, WireCopilot, WireBedrock}
}
