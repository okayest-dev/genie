// Package requesttool implements the request_permission tool: the always-on
// pre-negotiation channel through which the model asks the user for access
// ahead of a call it expects to be denied. It routes through the same terse
// prompt renderer and effective-policy store as inline escalation, and its
// results are single grant/reject lines. The tool is deliberately ungated
// (it never implements the Permissioned seam): it is a negotiation channel,
// not a capability.
package requesttool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/okayest-dev/genie/internal/permissions"
)

// ToolName is the registry name of the pre-negotiation tool.
const ToolName = "request_permission"

// Args mirrors the JSON Schema shape: one required permission axis, optional
// scope (omitted = blanket access on that axis).
type Args struct {
	Permission string `json:"permission"`
	Scope      string `json:"scope"`
}

// Tool negotiates one permission axis with the user. It needs the same three
// dependencies as the deny-point gate: the policy store (to persist grants),
// a negotiator (interactive in the REPL, auto-deny headless), and an optional
// permanent sink (to persist permanent-tier grants to the config file).
type Tool struct {
	store *permissions.Store
	neg   permissions.Negotiator
	sink  permissions.PermanentSink
}

// New builds a request_permission tool over store. sink persists permanent
// grants and may be nil. The negotiator defaults to DenyAll — the headless
// behaviour — and is replaced by the interactive negotiator when the REPL
// wires it (SetNegotiator), so pre-negotiation is never auto-approved.
func New(store *permissions.Store, sink permissions.PermanentSink) *Tool {
	return &Tool{store: store, neg: permissions.DenyAll{}, sink: sink}
}

// SetNegotiator replaces the negotiator behind the tool. The REPL calls this
// at start with its interactive negotiator; the headless default stays DenyAll.
func (t *Tool) SetNegotiator(neg permissions.Negotiator) {
	if neg != nil {
		t.neg = neg
	}
}

// Name returns the registry name.
func (t *Tool) Name() string { return ToolName }

// Description embeds the inline-first usage rule and the widest-anticipated-
// need guidance so the model reads the negotiation contract from the tool
// itself (og-73l.5).
func (t *Tool) Description() string {
	return "Ask the user for permission ahead of a call you expect to be denied, or after a mid-call runtime permission denial. " +
		"Otherwise call the tool directly — a denied call is escalated inline without this tool. " +
		"A grant covers exactly the scope you request, so request your widest anticipated need. " +
		"The user decides; the result is a \"Permission granted:\" or \"Permission rejected:\" line. " +
		"On rejection, pursue an alternative before re-requesting."
}

// Parameters declares the JSON Schema: permission is required and constrained
// to the five axes; scope is optional (omitted = blanket access on that axis).
// The enum mirrors permissions.AxisNames so the schema validator and the
// runtime check never drift.
func (t *Tool) Parameters() map[string]any {
	axes := permissions.AxisNames()
	enum := make([]any, 0, len(axes))
	for _, a := range axes {
		enum = append(enum, a)
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"permission": map[string]any{
				"type":        "string",
				"enum":        enum,
				"description": "Permission axis to escalate: read, write, net, run, or env.",
			},
			"scope": map[string]any{
				"type":        "string",
				"description": "Resource scope per axis — filesystem path (read/write), host[:port] (net), executable (run), env var name (env); omit for blanket access on that axis.",
			},
		},
		"required": []any{"permission"},
	}
}

// PermissionAxis returns the resolved axis for a valid permission argument.
func PermissionAxis(name string) (permissions.Axis, error) {
	if !permissions.IsAxis(name) {
		return "", fmt.Errorf("invalid permission %q: must be one of read, write, net, run, env", name)
	}
	return permissions.Axis(name), nil
}

// Execute negotiates the requested axis through the same terse prompt renderer
// as inline escalation, applies the user's chosen tier to the effective-policy
// store, and returns exactly one grant or reject line — never a status/hint
// composite. A rejected axis grants nothing and is not an error.
func (t *Tool) Execute(raw json.RawMessage) (string, error) {
	var args Args
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if args.Permission == "" {
		return "", fmt.Errorf("missing required argument: permission")
	}
	axis, err := PermissionAxis(args.Permission)
	if err != nil {
		return "", err
	}

	// Normalize before prompting so the prompt, the grant, and the store all
	// quote the same expression.
	scope := t.store.Normalize(axis, args.Scope)
	resp, err := t.neg.Negotiate(context.Background(), axis, scope)
	if err != nil {
		return "", fmt.Errorf("request_permission: %w", err)
	}

	switch resp {
	case permissions.ResponseSession:
		t.store.GrantSession(axis, scope)
	case permissions.ResponsePermanent:
		g := permissions.Grant{Axis: axis, Scope: scope, Tier: permissions.TierPermanent}
		if t.sink != nil {
			if err := t.sink(g); err != nil {
				return "", fmt.Errorf("request_permission: persist: %w", err)
			}
		}
		if err := t.store.GrantPermanent(g); err != nil {
			return "", fmt.Errorf("request_permission: %w", err)
		}
	case permissions.ResponseOnce:
		// A once grant is call-bound: bound to this request_permission call and
		// spent when the call resolves — the same lifecycle inline escalation
		// gives a once grant. Pre-negotiated once access therefore never
		// survives to a later call; durability is discovered by re-prompting.
		call := t.store.BeginCall("request_permission")
		t.store.GrantOnce(call, axis, scope)
		t.store.SpendOnce(call)
	case permissions.ResponseReject:
		return permissions.RenderReject(axis, scope), nil
	default:
		return "", fmt.Errorf("request_permission: unknown negotiation response %q", resp)
	}

	return permissions.RenderGrant(axis, scope), nil
}
