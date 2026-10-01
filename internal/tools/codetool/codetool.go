// Package codetool implements the code tool: a TypeScript/JavaScript snippet
// executed in a sandboxed Deno subprocess, whose permission axes the model
// requests per call.
package codetool

import (
	"encoding/json"
	"fmt"

	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/tools"
)

// Tool executes a snippet under a permission envelope negotiated by the
// deny-point gate. It reads the effective policy to decide which requested
// axes still need escalation.
type Tool struct {
	store *permissions.Store
}

// New builds a code tool over the effective-policy store.
func New(store *permissions.Store) *Tool {
	return &Tool{store: store}
}

// args is the request as far as the permission mapping reads it: the axes the
// model asks for, never scopes.
type args struct {
	Permissions []string `json:"permissions"`
}

// RequiredPermissions maps each requested axis onto an axis-only requirement
// under the any-scope-covered envelope: an axis the effective policy already
// covers at any scope is satisfied and omitted; an uncovered axis becomes a
// blanket requirement the gate negotiates through the standard prompt.
func (t *Tool) RequiredPermissions(raw json.RawMessage) ([]tools.Requirement, error) {
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}

	var reqs []tools.Requirement
	asked := make(map[permissions.Axis]bool, len(a.Permissions))
	for _, name := range a.Permissions {
		if !permissions.IsAxis(name) {
			return nil, fmt.Errorf("invalid permission %q: must be one of read, write, net, run, env", name)
		}
		axis := permissions.Axis(name)
		// One requirement per axis: a repeated request is still one escalation.
		if asked[axis] {
			continue
		}
		asked[axis] = true
		if t.store.AnyScopeCovered(axis) {
			continue
		}
		reqs = append(reqs, tools.Requirement{Axis: name})
	}
	return reqs, nil
}
