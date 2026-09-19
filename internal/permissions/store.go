// Package permissions implements the per-axis interactive permission
// escalation flow (ADR 0005): an effective-policy store of tier-tagged
// grants, a deny-point escalation gate in the agent loop, and a terse
// interactive/passive negotiator that owns the escalation prompt.
//
// Effective policy is the union over tiers, each tier a tier-tagged grant:
//
//	base ∪ permanent ∪ session ∪ once
//
// Axes: read, write, net, run, env. Scope matching is most-specific-first
// (exact > prefix > wildcard; a blanket grant covers every request on its
// axis). Tiers:
// once (single call; spent on execute, discarded on deny, never carried
// forward), session (rest of REPL session), permanent (persisted to
// [[permissions.permanent]] and honored after restart).
//
// Restrictive no-config default: no `[permissions]` section means read is
// covered only within the cwd tree (".") and write/net/run/env are uncovered,
// so every write escalates.
package permissions

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Axis is a permission axis the escalation model gates.
type Axis string

const (
	AxisRead  Axis = "read"
	AxisWrite Axis = "write"
	AxisNet   Axis = "net"
	AxisRun   Axis = "run"
	AxisEnv   Axis = "env"
)

// allAxes lists axes in rendering order.
var allAxes = []Axis{AxisRead, AxisWrite, AxisNet, AxisRun, AxisEnv}

// Tier tags the lifetime of a grant.
type Tier string

const (
	TierOnce      Tier = "once"
	TierSession   Tier = "session"
	TierPermanent Tier = "permanent"
)

// Grant is a tier-tagged entry in the store.
type Grant struct {
	Axis    Axis
	Scope   string // normalized scope; "" = blanket (axis-only)
	Tier    Tier
	Granted time.Time
}

// Store is the effective-policy store: tier-tagged grants with
// most-specific-first coverage and restrictive no-config default.
type Store struct {
	cwd string
	// base is the resolved config surface (restrictive no-config default:
	// read=["./"], write/net/run/env empty).
	base      map[Axis][]string
	permanent []Grant
	session   []Grant
	// once is keyed by call ID (once-tier lifecycle is call-bound).
	once map[string][]Grant
}

// New creates a Store rooted at cwd with the restrictive no-config default.
func New(cwd string) *Store {
	return &Store{
		cwd:  cwd,
		base: map[Axis][]string{AxisRead: {"."}},
		once: map[string][]Grant{},
	}
}

// Covered reports whether (axis, scope) is within the effective policy. The
// requested scope is normalized against the store cwd before matching.
func (s *Store) Covered(axis Axis, scope string) bool {
	want := s.normalize(axis, scope)
	return s.baseCovered(axis, want) || s.grantsCovered(s.permanent, axis, want, false) ||
		s.grantsCovered(s.session, axis, want, false) || s.onceCovered(axis, want)
}

// SetBase replaces the base config surface (per-agent replace-not-merge).
func (s *Store) SetBase(b map[Axis][]string) {
	s.base = make(map[Axis][]string, len(b))
	for a, scopes := range b {
		s.base[a] = append([]string(nil), scopes...)
	}
}

// GrantSession adds a session-tier grant covering scope.
func (s *Store) GrantSession(axis Axis, scope string) {
	g := Grant{Axis: axis, Scope: s.normalize(axis, scope), Tier: TierSession, Granted: time.Now().UTC()}
	s.session = append(s.session, g)
}

// GrantPermanent adds a permanent-tier grant (persisted to
// [[permissions.permanent]] and honored after restart). It returns an error
// if the grant names an unknown axis.
func (s *Store) GrantPermanent(g Grant) error {
	valid := false
	for _, a := range allAxes {
		if a == g.Axis {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("permissions: GrantPermanent: unknown axis %q", g.Axis)
	}
	g.Scope = s.normalize(g.Axis, g.Scope)
	s.permanent = append(s.permanent, g)
	return nil
}

// PermanentGrants returns the permanent-tier grants (for persistence and
// snapshot; callers get a copy).
func (s *Store) PermanentGrants() []Grant {
	out := append([]Grant(nil), s.permanent...)
	return out
}

// BaseSnapshot returns the resolved base surface scopes per axis (flat;
// tier-free). Used by the instruction snapshot and renderer.
func (s *Store) BaseSnapshot() map[Axis][]string {
	out := make(map[Axis][]string, len(s.base))
	for a, scopes := range s.base {
		out[a] = append([]string(nil), scopes...)
	}
	return out
}

// BeginCall binds a new tool call, returning its call ID for once-tier
// lifecycle ownership.
func (s *Store) BeginCall(scope string) string {
	id := nextCallID()
	s.once[id] = nil
	return id
}

// Once marks the call as in progress so its once-tier grants participate in
// coverage. A call is normally bound by BeginCall; Once makes the "resolving
// now" bind explicit. Grants stay live until the call resolves (SpendOnce) or
// is denied (DiscardOnce).
func (s *Store) Once(call string) {
	if _, ok := s.once[call]; !ok {
		s.once[call] = nil
	}
}

// GrantOnce adds a once-tier grant for the call. A scope is covered for the
// life of the call only.
func (s *Store) GrantOnce(call string, axis Axis, scope string) {
	g := Grant{Axis: axis, Scope: s.normalize(axis, scope), Tier: TierOnce, Granted: time.Now().UTC()}
	s.once[call] = append(s.once[call], g)
}

// SpendOnce spends (removes) the call's once grants after the call resolves
// and executes. Once grants never carry forward.
func (s *Store) SpendOnce(call string) { delete(s.once, call) }

// DiscardOnce discards the call's once grants when the call is denied; they
// are never spent forward.
func (s *Store) DiscardOnce(call string) { delete(s.once, call) }

// EndSession clears session-tier grants (once today; permanent survives).
func (s *Store) EndSession() { s.session = nil }

func (s *Store) onceCovered(axis Axis, want string) bool {
	for _, grants := range s.once {
		if s.grantsCovered(grants, axis, want, true) {
			return true
		}
	}
	return false
}

// grantsCovered reports whether any grant covers (axis, want). Once-grants
// (the call's own) also cover the exact call scope, handled by the caller.
func (s *Store) grantsCovered(grants []Grant, axis Axis, want string, _ bool) bool {
	for _, g := range grants {
		if g.Axis != axis {
			continue
		}
		if scopeCovers(axis, g.Scope, want) {
			return true
		}
	}
	return false
}

// baseCovered reports whether a base scope covers (axis, want).
func (s *Store) baseCovered(axis Axis, want string) bool {
	for _, sc := range s.base[axis] {
		n := s.normalize(axis, sc)
		if scopeCovers(axis, n, want) {
			return true
		}
	}
	return false
}

// scopeCovers reports whether grant scope g covers requested scope want on
// axis. "" grant scope is blanket: it covers any request on its axis
// (including a blanket request). A blank requested scope is covered only by a
// blanket grant. Axis semantics: read/write = path prefix (no glob); net =
// wildcard subdomains or exact host[:port]; run/env = exact.
func scopeCovers(axis Axis, g, want string) bool {
	if want == "" {
		return g == ""
	}
	if g == "" {
		return true // blanket grant covers any request on its axis
	}
	switch axis {
	case AxisRead, AxisWrite:
		return pathPrefix(g, want)
	case AxisNet:
		return netScope(g, want)
	default:
		return g == want
	}
}

// pathPrefix reports whether want is g or below g in the tree (Go's
// filepath.Rel semantics).
func pathPrefix(g, want string) bool {
	if g == want {
		return true
	}
	rel, err := filepath.Rel(g, want)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..")
}

// netScope matches host[:port]: "*.example.com:443" matches
// "api.example.com:443"; a bare hostname matches exactly.
func netScope(g, want string) bool {
	if strings.HasPrefix(g, "*.") {
		rest := strings.TrimPrefix(g, "*.")
		return strings.HasSuffix(want, "."+rest) || want == rest
	}
	return g == want
}

// normalize produces the canonical scope expression for an axis within the
// store cwd: read/write paths become clean absolute paths; net hosts keep
// host[:port]; run/env stay literal.
func (s *Store) normalize(axis Axis, scope string) string {
	switch axis {
	case AxisRead, AxisWrite:
		if scope == "" {
			return ""
		}
		if scope == "." {
			return s.cwd
		}
		p := scope
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.cwd, p)
		}
		return filepath.Clean(p)
	case AxisNet:
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return strings.TrimSpace(scope)
	}
}

var nextID int

func nextCallID() string {
	nextID++
	return "call-" + strconv.Itoa(nextID)
}
