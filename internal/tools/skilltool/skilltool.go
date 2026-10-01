// Package skilltool implements the skill tool: the model's channel for pulling
// a skill's instructions into context on demand.
//
// The tool returns the body as its result, which puts it in the conversation
// like any other tool output — available in this turn and carried in the
// transcript afterwards. Nothing is held resident: the skill layer itself is
// index-only, so a skill costs context once the model has actually reached for
// it rather than from the moment it is discovered.
//
// The one thing to know about a body in the tool-output layer is that a single
// oversized result can be narrowed in an outgoing request (context
// condense_size, off by default; net_drop can drop it entirely). That trades
// the body's full text for a bounded excerpt on requests that would otherwise
// overflow, which is the same bargain every tool result makes.
//
// The tool resolves names against the *bound* pool, so a skill the active
// agent did not bind is unreachable however it is spelled in config.
package skilltool

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/okayest-dev/genie/internal/skill"
)

// ToolName is the registry name of the skill tool.
const ToolName = "skill"

// Pool is the per-turn view of the skills bound to the active agent. The run
// implements it over the agent's resolved bound set, which is re-derived on
// every turn and so reflects both config filtering and agent binding.
type Pool interface {
	// Bound returns the skills currently bound to the active agent, in
	// discovery order. The slice is read-only to the caller.
	Bound() []skill.ParsedSkill
}

// Args mirrors the JSON Schema shape: one required skill name.
type Args struct {
	Name string `json:"name"`
}

// Tool serves skill bodies from a pool. It holds no state of its own: every
// call reads the pool the run resolved for this turn, so an agent switch or an
// edited SKILL.md takes effect on the next call with nothing to invalidate.
type Tool struct {
	pool Pool
}

// New builds a skill tool over a pool that resolves against the active agent.
func New(pool Pool) *Tool {
	return &Tool{pool: pool}
}

// Name returns the registry name.
func (t *Tool) Name() string { return ToolName }

// Description states the trigger condition. The body comes back in the result,
// so there is no lag to warn about and no reason to call twice.
func (t *Tool) Description() string {
	return "Engage a skill: returns its full instructions for use in this conversation. " +
		"Call this when a skill's description in the ## Skills index matches the current task. " +
		"The result is the skill's instructions verbatim — follow them for the rest of the conversation, " +
		"and do not call it again for work the returned instructions already cover."
}

// Parameters declares the JSON Schema: name is required.
func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name from the ## Skills index.",
			},
		},
		"required": []any{"name"},
	}
}

// Execute returns the named skill's body. An unknown name is an error naming
// the available set — the model gets a correction it can act on rather than a
// silent no-op that looks like success.
func (t *Tool) Execute(raw json.RawMessage) (string, error) {
	var args Args
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	bound := t.pool.Bound()
	var match *skill.ParsedSkill
	for i := range bound {
		if bound[i].Name == args.Name {
			match = &bound[i]
			break
		}
	}
	if match == nil {
		available := make([]string, 0, len(bound))
		for _, s := range bound {
			available = append(available, s.Name)
		}
		sort.Strings(available)
		if len(available) == 0 {
			return "", fmt.Errorf("unknown skill %q: no skills are available to this agent", args.Name)
		}
		return "", fmt.Errorf("unknown skill %q: available: %s", args.Name, strings.Join(available, ", "))
	}

	if match.Body == "" {
		return fmt.Sprintf("Skill %q has no instructions body; its description above is all there is.", match.Name), nil
	}
	return "# Skill: " + match.Name + "\n\n" + match.Body, nil
}
