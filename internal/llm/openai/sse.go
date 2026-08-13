package openai

import (
	"bufio"
	"io"

	"github.com/okayest-dev/og/internal/llm"
)

// parseChunk converts one wire chunk into normalized events. It is a pure
// function so the accumulation rules are unit-testable at the SSE seam.
func parseChunk(ch chunk) []llm.Event {
	if ch.Error != nil {
		msg := ch.Error.Message
		if msg == "" {
			msg = "provider error"
		}
		return []llm.Event{{Kind: llm.EventError, Err: &llm.ProviderError{Kind: llm.KindOther, Message: msg}}}
	}
	if ch.Usage != nil && len(ch.Choices) == 0 {
		return []llm.Event{{
			Kind:  llm.EventUsage,
			Usage: llm.Usage{PromptTokens: ch.Usage.PromptTokens, CompletionTokens: ch.Usage.CompletionTokens, TotalTokens: ch.Usage.TotalTokens},
		}}
	}
	var events []llm.Event
	for _, choice := range ch.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			events = append(events, llm.Event{Kind: llm.EventText, Text: *choice.Delta.Content})
		}
		if choice.FinishReason != nil {
			events = append(events, llm.Event{Kind: llm.EventFinish, End: canonicalFinishReason(*choice.FinishReason)})
		}
	}
	return events
}

// canonicalFinishReason maps a wire finish_reason to the canonical set,
// folding anything unknown into FinishOther.
func canonicalFinishReason(reason string) llm.FinishReason {
	switch reason {
	case string(llm.FinishStop):
		return llm.FinishStop
	case string(llm.FinishToolCalls):
		return llm.FinishToolCalls
	case string(llm.FinishLength):
		return llm.FinishLength
	default:
		return llm.FinishOther
	}
}

// newSSEScanner scans an SSE body line by line, with headroom for long
// data lines.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	return sc
}
