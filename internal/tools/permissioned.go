package tools

import "encoding/json"

// Requirement declares what a single tool call needs on one permission axis.
// An empty Scope is the blanket (axis-only) requirement — used when the tool
// cannot determine a concrete scope (best-effort). Axis names follow the
// permission model: read, write, net, run, env.
type Requirement struct {
	Axis  string
	Scope string
}

// Permissioned is implemented by tools whose calls can require permission
// on an axis. The deny-point gate calls RequiredPermissions before Execute to
// learn what the call needs; tools that do not implement Permissioned run
// ungated.
type Permissioned interface {
	RequiredPermissions(args json.RawMessage) ([]Requirement, error)
}
