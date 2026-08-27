// Package contextmgr implements ContextManager: a transparent llm.Client
// wrapper that sits outermost in the client chain (around RoutingClient) and
// owns history injection. From the caller's perspective it is indistinguishable
// from any other llm.Client; on Stream it injects prior-turn history from the
// session so a later turn's request carries earlier turns' messages.
package contextmgr

import (
	"context"
	"iter"

	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/session"
)

// ContextManager is a pure llm.Client decorator holding a session reference.
// It implements exactly the llm.Client surface (Stream + ListModels) and
// nothing more, so callers cannot distinguish it from any other client.
type ContextManager struct {
	inner llm.Client
	sess  *session.Session
}

// New wraps inner so that Stream requests gain the session's prior history.
func New(inner llm.Client, sess *session.Session) *ContextManager {
	return &ContextManager{inner: inner, sess: sess}
}

// Stream injects prior-turn history from the session into the request and
// forwards it to the inner client. It returns a wrapping iterator that
// re-yields inner events unchanged, so the caller observes a transparent
// stream. Session state updates on usage/tool/finish events are owned by the
// agent loop (which persists assistant and tool messages); this decorator
// only re-serials the request so callers stay unaware of context management.
func (m *ContextManager) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	req = m.injectHistory(req)

	stream, err := m.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	return func(yield func(llm.Event) bool) {
		for ev := range stream {
			if !yield(ev) {
				return
			}
		}
	}, nil
}

// ListModels passes through to the inner client untouched; it is never
// intercepted.
func (m *ContextManager) ListModels(ctx context.Context) ([]llm.Model, error) {
	return m.inner.ListModels(ctx)
}

// injectHistory builds the message list the inner client receives: the current
// turn's agent instruction, followed by prior turns' messages from the
// session, followed by the rest of the current turn.
//
// The session is the source of truth for the conversation. Every turn appends
// exactly one agent-instruction (system-role) message as its first message, so
// the last system-role message in the session history marks the start of the
// current turn. Everything before it is prior-turn history (its system-role
// messages dropped); everything from it onward is the current turn, which the
// agent loop has already appended (its instruction + user before streaming,
// then assistant and tool results as the tool loop runs). Reconstructing from
// the session rather than req.Messages keeps the tool loop correct: an
// assistant tool-call message lives in the session but is never placed on
// req.Messages, so matching the request tail against history would miscount
// and duplicate the current turn.
func (m *ContextManager) injectHistory(req llm.Request) llm.Request {
	if m.sess == nil {
		return req
	}
	history := m.sess.History()

	start := -1
	for i, msg := range history {
		if msg.Role == llm.RoleSystem {
			start = i
		}
	}
	// No system message yet (fresh session awaiting its first append): pass the
	// request through untouched.
	if start < 0 {
		return req
	}

	var prior []llm.Message
	for _, msg := range history[:start] {
		if msg.Role == llm.RoleSystem {
			continue
		}
		prior = append(prior, msg)
	}

	current := history[start:]
	messages := make([]llm.Message, 0, len(prior)+len(current))
	messages = append(messages, current[0:1]...)
	messages = append(messages, prior...)
	messages = append(messages, current[1:]...)

	req.Messages = messages
	return req
}
