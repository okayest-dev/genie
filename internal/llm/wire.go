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

// ValidWires is the set of registered wire names. Populated by RegisterWire.
var ValidWires = map[Wire]bool{}
