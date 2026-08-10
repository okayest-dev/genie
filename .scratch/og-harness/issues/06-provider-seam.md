Type: grilling
Status: open
Blocked by: 01

# 06-provider-seam

## Question

Design the v1 LLM client interface (the seam between the agent loop and the provider) so native Anthropic / OpenAI-responses / Google wires slot in later without rework. What does the agent loop depend on — a stream of text deltas, tool-call deltas, finish reason, usage? What may leak (provider-specific fields, wire quirks)? This is the later-phase multi-provider ticket's blocker — the seam decides how much of that is pre-paid now.
