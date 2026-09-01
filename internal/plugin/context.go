// Context seam: turns loaded plugins that declare granular context-hook
// capabilities into a contextmgr.Hooks the ContextManager invokes around each
// Stream. before_request and after_response form a deterministic ordered filter
// chain; compact and condense are single-active, with a hard startup error when
// multiple plugins claim the same seam without an explicit choice. The built-in
// compactor/condenser is the default active registrant when no plugin is
// chosen.
package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/okayest-dev/genie/internal/contextmgr"
	"github.com/okayest-dev/genie/internal/llm"
)

// BuiltinName is the name of the built-in compactor/condenser registrant.
// Selecting it as active_compact/active_condense explicitly chooses the
// harness's own implementation.
const BuiltinName = "builtin"

// ContextConfig is the resolved [context.plugins] selection passed to the seam
// builder.
type ContextConfig struct {
	// Order is the deterministic chain order for before_request/after_response
	// hooks (plugin names first, in order; any plugin not listed is appended in
	// registration order). Empty means registration order for all.
	Order []string
	// ActiveCompact and ActiveCondense name the single-active implementation.
	// BuiltinName selects the built-in; empty means auto-resolve (error when
	// ambiguous).
	ActiveCompact  string
	ActiveCondense string
}

// ContextSeam implements contextmgr.Hooks by dispatching to loaded plugins. It
// is built once at startup and is safe for concurrent use.
type ContextSeam struct {
	before    []*Plugin
	after     []*Plugin
	compact   *Plugin
	condense  *Plugin
	onDegrade func(msg string)
}

var _ contextmgr.Hooks = (*ContextSeam)(nil)

// NewContextSeam resolves the loaded plugins against the [context.plugins]
// selection and returns a ContextSeam, or a hard error when a single-active
// seam is (or is selected to be) ambiguous. plugins must be in registration
// (discovery) order, which is the default chain order. onDegrade surfaces a
// visible degradation message to the terminal when a hook fails; it may be nil.
func NewContextSeam(plugins []*Plugin, cfg ContextConfig, onDegrade func(msg string)) (*ContextSeam, error) {
	seam := &ContextSeam{onDegrade: onDegrade}
	byName := make(map[string]*Plugin, len(plugins))
	for _, p := range plugins {
		byName[p.Name] = p
	}

	seam.before = orderedPlugins(byName, plugins, cfg.Order, func(p *Plugin) bool { return p.Capabilities.BeforeRequest })
	seam.after = orderedPlugins(byName, plugins, cfg.Order, func(p *Plugin) bool { return p.Capabilities.AfterResponse })

	var err error
	if seam.compact, err = resolveSingleActive(byName, plugins, cfg.ActiveCompact, "compact", func(p *Plugin) bool { return p.Capabilities.CompactHook }); err != nil {
		return nil, err
	}
	if seam.condense, err = resolveSingleActive(byName, plugins, cfg.ActiveCondense, "condense", func(p *Plugin) bool { return p.Capabilities.CondenseHook }); err != nil {
		return nil, err
	}

	return seam, nil
}

// orderedPlugins filters plugins by a capability predicate and orders them by
// cfg.Order first, then registration (discovery) order for any plugin not
// listed.
func orderedPlugins(byName map[string]*Plugin, plugins []*Plugin, order []string, pred func(*Plugin) bool) []*Plugin {
	inOrder := make(map[string]bool)
	var out []*Plugin
	for _, name := range order {
		p, ok := byName[name]
		if !ok || !pred(p) {
			continue
		}
		inOrder[name] = true
		out = append(out, p)
	}
	for _, p := range plugins {
		if inOrder[p.Name] {
			continue
		}
		if pred(p) {
			out = append(out, p)
		}
	}
	return out
}

// resolveSingleActive resolves the single-active compact/condense implementation:
//
//   - explicit choice wins (errors if the named plugin is absent / lacks the seam,
//     or the choice is the built-in);
//   - zero declarants defaults to the built-in;
//   - one declarant defaults to it;
//   - several declarants without an explicit choice is a hard startup error.
func resolveSingleActive(byName map[string]*Plugin, plugins []*Plugin, active, seam string, pred func(*Plugin) bool) (*Plugin, error) {
	var declarants []*Plugin
	for _, p := range plugins {
		if pred(p) {
			declarants = append(declarants, p)
		}
	}

	if active == BuiltinName {
		if len(declarants) > 0 {
			slog.Info("context seam: built-in active over plugin declarants", "seam", seam, "ignored", pluginNames2(declarants))
		}
		return nil, nil
	}
	if active != "" {
		p, ok := byName[active]
		if !ok {
			return nil, fmt.Errorf("context seam: active_%s %q not found among loaded plugins", seam, active)
		}
		if !pred(p) {
			return nil, fmt.Errorf("context seam: plugin %q does not declare the %s capability", active, seam)
		}
		return p, nil
	}

	switch len(declarants) {
	case 0:
		// Built-in default.
		return nil, nil
	case 1:
		return declarants[0], nil
	default:
		names := pluginNames2(declarants)
		return nil, fmt.Errorf("context seam: multiple plugins declare %s (%s); set active_%s explicitly", seam, names, seam)
	}
}

func pluginNames2(ps []*Plugin) string {
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

// BeforeRequest runs the ordered before_request chain; each hook sees the
// previous hook's result. A failing hook is skipped (its contribution dropped,
// prior contributions kept) and surfaced as degradation; the request proceeds.
func (s *ContextSeam) BeforeRequest(ctx context.Context, req llm.Request) (llm.Request, error) {
	cur := req
	for _, p := range s.before {
		out, err := p.CallContextBefore(ctx, cur)
		if err != nil {
			s.degrade("before_request hook %q: %v", p.Name, err)
			continue
		}
		cur = out
	}
	return cur, nil
}

// AfterResponse runs the ordered after_response chain over the completed turn's
// usage. Failing hooks are skipped and surfaced; history is never rewritten.
func (s *ContextSeam) AfterResponse(ctx context.Context, req llm.Request, usage llm.Usage) error {
	for _, p := range s.after {
		if _, err := p.CallContextAfter(ctx, req, usage); err != nil {
			s.degrade("after_response hook %q: %v", p.Name, err)
		}
	}
	return nil
}

// CompactBuiltin reports whether the harness's built-in compactor is the
// active single-active implementation (no plugin selected): the ContextManager
// then runs its own compactor instead of calling this seam. An external Hooks
// implementation without this marker is treated as supplying its own.
func (s *ContextSeam) CompactBuiltin() bool { return s.compact == nil }

// CondenseBuiltin mirrors CompactBuiltin for the condense seam.
func (s *ContextSeam) CondenseBuiltin() bool { return s.condense == nil }

// Compact invokes the single-active compact implementation, or returns the
// request unchanged when the built-in default is active (the ContextManager
// then runs the built-in).
func (s *ContextSeam) Compact(ctx context.Context, req llm.Request) (llm.Request, error) {
	if s.compact == nil {
		return req, nil
	}
	return s.compact.CallContextCompact(ctx, req)
}

// Condense invokes the single-active condense implementation, or returns the
// request unchanged when the built-in default is active.
func (s *ContextSeam) Condense(ctx context.Context, req llm.Request) (llm.Request, error) {
	if s.condense == nil {
		return req, nil
	}
	return s.condense.CallContextCondense(ctx, req)
}

// degrade surfaces a visible degradation message to the terminal (via onDegrade)
// and logs it. A failed hook never fails the request.
func (s *ContextSeam) degrade(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	slog.Warn("context degradation", "detail", msg)
	if s.onDegrade != nil {
		s.onDegrade(msg)
	}
}
