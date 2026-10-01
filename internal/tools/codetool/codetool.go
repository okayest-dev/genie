// Package codetool implements the code tool: a TypeScript/JavaScript snippet
// executed in a sandboxed Deno subprocess, whose permission axes the model
// requests per call.
package codetool

import (
	"encoding/json"
	"fmt"
	"strings"

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

// parsePermissions unmarshals the raw JSON and returns the set of valid
// permission axes requested, or an error if JSON is invalid or contains
// unknown axes.
func parsePermissions(raw json.RawMessage) (map[permissions.Axis]bool, error) {
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}

	asked := make(map[permissions.Axis]bool, len(a.Permissions))
	for _, name := range a.Permissions {
		if !permissions.IsAxis(name) {
			return nil, fmt.Errorf("invalid permission %q: must be one of read, write, net, run, env", name)
		}
		asked[permissions.Axis(name)] = true
	}
	return asked, nil
}

// RequiredPermissions maps each requested axis onto an axis-only requirement
// under the any-scope-covered envelope: an axis the effective policy already
// covers at any scope is satisfied and omitted; an uncovered axis becomes a
// blanket requirement the gate negotiates through the standard prompt.
func (t *Tool) RequiredPermissions(raw json.RawMessage) ([]tools.Requirement, error) {
	asked, err := parsePermissions(raw)
	if err != nil {
		return nil, err
	}

	var reqs []tools.Requirement
	for _, axis := range permissions.AxisNames() {
		ax := permissions.Axis(axis)
		if !asked[ax] {
			continue
		}
		if t.store.AnyScopeCovered(ax) {
			continue
		}
		reqs = append(reqs, tools.Requirement{Axis: axis})
	}
	return reqs, nil
}

// buildDenoFlags constructs the Deno --allow-* flags from the effective
// covered scopes for each requested axis. It uses the store's CoveredScopes
// view, which includes base, permanent, session, and once-tier grants.
// - A blanket grant (scope "") yields the unrestricted flag form (no =scopes).
// - Scoped grants are rendered comma-separated per axis.
// - An unrequested axis yields no flag.
// - Scopes pass through verbatim; they are not normalized, reordered, or filtered.
func (t *Tool) buildDenoFlags(raw json.RawMessage) []string {
	asked, err := parsePermissions(raw)
	if err != nil {
		return nil
	}

	var flags []string
	for _, axis := range permissions.AxisNames() {
		ax := permissions.Axis(axis)
		if !asked[ax] {
			continue
		}
		scopes := t.store.CoveredScopes(ax)
		if len(scopes) == 0 {
			// Axis requested but no coverage — the gate should have blocked
			// this, but we defensively emit no flag so Deno denies by default.
			continue
		}
		// Blanket grant: scopes contains only "".
		if len(scopes) == 1 && scopes[0] == "" {
			flags = append(flags, fmt.Sprintf("--allow-%s", axis))
			continue
		}
		// Scoped grants: join with commas.
		flags = append(flags, fmt.Sprintf("--allow-%s=%s", axis, strings.Join(scopes, ",")))
	}
	return flags
}
