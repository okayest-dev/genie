package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/okayest-dev/genie/internal/tools"
)

// Response is the user's lifetime choice for one uncovered requirement, or a
// flat rejection. It mirrors the four terse prompt keys (o/s/p/r).
type Response string

const (
	ResponseOnce      Response = "once"
	ResponseSession   Response = "session"
	ResponsePermanent Response = "permanent"
	ResponseReject    Response = "reject"
)

// Negotiator resolves one uncovered requirement. Implementations own the
// prompt surface: the interactive REPL renders the terse line and reads stdin
// while owning SIGINT for the duration; the headless negotiator auto-denies or
// approves. A plain rejection is not an error.
type Negotiator interface {
	Negotiate(ctx context.Context, axis Axis, scope string) (Response, error)
}

// PermanentSink persists a permanent grant. The harness wires it to the config
// writer; a nil sink keeps permanent grants in-memory only.
type PermanentSink func(g Grant) error

// DenyAll is the headless negotiator: it rejects every uncovered requirement,
// so a non-interactive run never escalates. A denial is not an error.
type DenyAll struct{}

// Negotiate rejects the requirement.
func (DenyAll) Negotiate(context.Context, Axis, string) (Response, error) {
	return ResponseReject, nil
}

// ApproveAll is the headless --approve-all negotiator: it approves every
// uncovered requirement as an in-memory session grant, so a trusted one-shot
// run never blocks. Grants live only for that single turn; nothing is
// persisted.
type ApproveAll struct{}

// Negotiate grants the requirement for the run.
func (ApproveAll) Negotiate(context.Context, Axis, string) (Response, error) {
	return ResponseSession, nil
}

// Gate is the deny-point escalation evaluator: it turns a tool's declared
// requirements into an allow/deny decision, negotiating each uncovered axis
// one at a time.
type Gate struct {
	store *Store
	neg   Negotiator
	sink  PermanentSink
}

// NewGate builds a Gate over store, negotiating uncovered requirements through
// neg. sink persists permanent-tier grants and may be nil.
func NewGate(store *Store, neg Negotiator, sink PermanentSink) *Gate {
	return &Gate{store: store, neg: neg, sink: sink}
}

// Decision is the gate's verdict for one tool call. On Allow, Granted is the
// newly-negotiated grant lines (empty when nothing was negotiated) to place
// before the tool output. On deny, Denied is the full composite result.
type Decision struct {
	Allow   bool
	Granted string
	Denied  string

	call string
}

// Settle releases an allowed decision's once-tier binding after the tool ran.
// It is a no-op for a call that negotiated no once grants.
func (g *Gate) Settle(d Decision) {
	if d.call != "" {
		g.store.SpendOnce(d.call)
	}
}

// Check evaluates a tool call against the effective policy. It binds callID as
// the once-tier call owner, negotiates every uncovered requirement in fixed
// axis order (read → write → net → run → env), and allows the call only when
// every requirement is granted. The gate never runs the tool.
func (g *Gate) Check(ctx context.Context, callID string, p tools.Permissioned, args json.RawMessage) (Decision, error) {
	reqs, err := p.RequiredPermissions(args)
	if err != nil {
		return Decision{}, fmt.Errorf("permissions: required permissions: %w", err)
	}
	if len(reqs) == 0 {
		return Decision{Allow: true}, nil
	}

	// Required set R = requirements minus those already covered. Snapshotted
	// before negotiation, so each requirement prompts exactly once.
	var uncovered []tools.Requirement
	for _, r := range reqs {
		if g.store.Covered(Axis(r.Axis), r.Scope) {
			continue
		}
		uncovered = append(uncovered, r)
	}
	if len(uncovered) == 0 {
		return Decision{Allow: true}, nil
	}

	sortByAxis(uncovered)
	// Bind the call so once-tier grants participate in coverage for its life.
	g.store.Once(callID)

	var granted, rejected []Grant
	for _, r := range uncovered {
		axis := Axis(r.Axis)
		// The prompt and the grant must be the same expression, so normalize
		// the scope before showing it.
		scope := g.store.Normalize(axis, r.Scope)
		resp, err := g.neg.Negotiate(ctx, axis, scope)
		if err != nil {
			g.store.DiscardOnce(callID)
			return Decision{}, err
		}
		switch resp {
		case ResponseOnce:
			g.store.GrantOnce(callID, axis, scope)
			granted = append(granted, Grant{Axis: axis, Scope: scope, Tier: TierOnce})
		case ResponseSession:
			g.store.GrantSession(axis, scope)
			granted = append(granted, Grant{Axis: axis, Scope: scope, Tier: TierSession})
		case ResponsePermanent:
			grant := Grant{Axis: axis, Scope: scope, Tier: TierPermanent}
			if g.sink != nil {
				if err := g.sink(grant); err != nil {
					g.store.DiscardOnce(callID)
					return Decision{}, err
				}
			}
			if err := g.store.GrantPermanent(grant); err != nil {
				g.store.DiscardOnce(callID)
				return Decision{}, err
			}
			granted = append(granted, grant)
		case ResponseReject:
			// Denial does not stop the chain; the user judges each axis.
			rejected = append(rejected, Grant{Axis: axis, Scope: scope, Tier: TierOnce})
		default:
			g.store.DiscardOnce(callID)
			return Decision{}, fmt.Errorf("permissions: unknown negotiation response %q", resp)
		}
	}

	if len(rejected) > 0 {
		// The call is denied overall: discard once grants, keep session and
		// permanent grants from the chain.
		g.store.DiscardOnce(callID)
		return Decision{
			Allow:  false,
			Denied: RenderDeniedComposite(granted, rejected),
		}, nil
	}
	return Decision{
		Allow:   true,
		Granted: RenderGrantLines(granted),
		call:    callID,
	}, nil
}

// sortByAxis orders requirements by the fixed axis order, preserving
// declaration order within an axis (stable sort).
func sortByAxis(reqs []tools.Requirement) {
	rank := make(map[string]int, len(allAxes))
	for i, a := range allAxes {
		rank[string(a)] = i
	}
	sort.SliceStable(reqs, func(i, j int) bool {
		return rank[reqs[i].Axis] < rank[reqs[j].Axis]
	})
}
