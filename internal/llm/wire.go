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
	WireLiteLLM         Wire = "litellm"
	WireLiteLLMOAuth    Wire = "litellm-oauth"
)

// WireNames returns every bundled wire name, built from the Wire* constants.
func WireNames() []string {
	return []string{WireOpenAI, WireAnthropic, WireOpenAIResponses, WireGoogle, WireCopilot, WireBedrock, WireLiteLLM, WireLiteLLMOAuth}
}
