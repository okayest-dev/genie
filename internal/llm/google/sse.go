package google

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/okayest-dev/og/internal/llm"
)

// parseChunk converts one Google generateContent wire chunk into normalized
// events. It is a pure function so the parsing rules are unit-testable at
// the SSE seam.
func parseChunk(ch chunk) []llm.Event {
	if ch.Error != nil {
		msg := ch.Error.Message
		if msg == "" {
			msg = "provider error"
		}
		return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: msg}}}
	}
	var events []llm.Event
	// Emit usage regardless of whether candidates are present. Google
	// commonly sends usage in the final chunk alongside the last candidate.
	if ch.UsageMetadata != nil {
		events = append(events, llm.Event{
			Kind: llm.EventUsage,
			Usage: llm.Usage{
				PromptTokens:     ch.UsageMetadata.PromptTokenCount,
				CompletionTokens: ch.UsageMetadata.CandidatesTokenCount,
				TotalTokens:      ch.UsageMetadata.TotalTokenCount,
			},
		})
	}
	for _, cand := range ch.Candidates {
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				events = append(events, llm.Event{Kind: llm.EventText, Text: part.Text})
			}
			if part.FunctionCall.Name != "" {
				args, _ := json.Marshal(part.FunctionCall.Args)
				events = append(events, llm.Event{
					Kind: llm.EventToolCall,
					ToolCalls: []llm.ToolCall{{
						Name:      part.FunctionCall.Name,
						Arguments: string(args),
					}},
				})
			}
		}
		if cand.FinishReason != "" {
			events = append(events, llm.Event{Kind: llm.EventFinish, End: canonicalFinishReason(cand.FinishReason)})
		}
	}
	return events
}

// canonicalFinishReason maps a Google wire finishReason to the canonical
// set, folding anything unknown into FinishOther.
func canonicalFinishReason(reason string) llm.FinishReason {
	switch reason {
	case "STOP":
		return llm.FinishStop
	case "MAX_TOKENS":
		return llm.FinishLength
	default:
		return llm.FinishOther
	}
}

// newLineScanner scans an SSE body line by line, with headroom for long
// data lines.
func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	return sc
}
