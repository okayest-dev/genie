package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

func mkPlugin(name string, caps Capabilities) *Plugin {
	caps.Version = ProtocolVersion
	return &Plugin{Name: name, Capabilities: caps}
}

func TestContextSeamConfigOrder(t *testing.T) {
	a := mkPlugin("a", Capabilities{BeforeRequest: true})
	b := mkPlugin("b", Capabilities{BeforeRequest: true})
	plugins := []*Plugin{a, b}

	// Order lists "b" first, so the before chain runs b then a.
	seam, err := NewContextSeam(plugins, ContextConfig{Order: []string{"b", "a"}}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if len(seam.before) != 2 {
		t.Fatalf("before chain length = %d, want 2", len(seam.before))
	}
	if seam.before[0].Name != "b" || seam.before[1].Name != "a" {
		t.Errorf("before chain order = [%s %s], want [b a]", seam.before[0].Name, seam.before[1].Name)
	}
}

func TestContextSeamBeforeOrderDefaultsToRegistration(t *testing.T) {
	a := mkPlugin("a", Capabilities{BeforeRequest: true})
	b := mkPlugin("b", Capabilities{BeforeRequest: true})
	plugins := []*Plugin{a, b}

	seam, err := NewContextSeam(plugins, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if seam.before[0].Name != "a" || seam.before[1].Name != "b" {
		t.Errorf("default before order = [%s %s], want [a b]", seam.before[0].Name, seam.before[1].Name)
	}
}

func TestContextSeamSingleCompactAutoResolves(t *testing.T) {
	a := mkPlugin("a", Capabilities{CompactHook: true})
	plugins := []*Plugin{a}

	seam, err := NewContextSeam(plugins, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if seam.compact != a {
		t.Errorf("compact = %v, want plugin a", seam.compact)
	}
}

func TestContextSeamSingleActiveConflict(t *testing.T) {
	a := mkPlugin("a", Capabilities{CompactHook: true})
	b := mkPlugin("b", Capabilities{CompactHook: true})
	plugins := []*Plugin{a, b}

	_, err := NewContextSeam(plugins, ContextConfig{}, nil)
	if err == nil {
		t.Fatal("expected startup error for multiple compact declarants without a choice")
	}
	if !strings.Contains(err.Error(), "active_compact") {
		t.Errorf("error should require active_compact choice, got %q", err)
	}
}

func TestContextSeamActiveExplicitResolution(t *testing.T) {
	a := mkPlugin("a", Capabilities{CompactHook: true})
	b := mkPlugin("b", Capabilities{CompactHook: true})
	plugins := []*Plugin{a, b}

	seam, err := NewContextSeam(plugins, ContextConfig{ActiveCompact: "b"}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if seam.compact != b {
		t.Errorf("compact = %v, want plugin b", seam.compact)
	}
}

func TestContextSeamBuiltinDefaultActive(t *testing.T) {
	// No plugins declare compact/condense: the built-in is the default active,
	// which here is inert (nil) until the compactor/condenser lands.
	seam, err := NewContextSeam(nil, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if seam.compact != nil || seam.condense != nil {
		t.Errorf("expected built-in (nil) compact/condense by default, got compact=%v condense=%v", seam.compact, seam.condense)
	}
	req := llm.Request{Model: "m"}
	out, err := seam.Compact(context.Background(), req)
	if err != nil || out.Model != req.Model {
		t.Errorf("inert built-in compact should pass through, got err=%v req=%+v", err, out)
	}
}

func TestContextSeamBuiltinExplicitOverridesPlugins(t *testing.T) {
	a := mkPlugin("a", Capabilities{CompactHook: true})
	plugins := []*Plugin{a}

	seam, err := NewContextSeam(plugins, ContextConfig{ActiveCompact: BuiltinName}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if seam.compact != nil {
		t.Errorf("explicit builtin should ignore plugin declarants, got compact=%v", seam.compact)
	}
}

func TestContextSeamUnknownActiveErrors(t *testing.T) {
	plugins := []*Plugin{mkPlugin("a", Capabilities{CompactHook: true})}
	_, err := NewContextSeam(plugins, ContextConfig{ActiveCompact: "nope"}, nil)
	if err == nil {
		t.Fatal("expected error for unknown active_compact")
	}
}

func TestContextSeamInactiveCapabilityErrors(t *testing.T) {
	plugins := []*Plugin{mkPlugin("a", Capabilities{Wires: true})}
	_, err := NewContextSeam(plugins, ContextConfig{ActiveCompact: "a"}, nil)
	if err == nil {
		t.Fatal("expected error when active plugin lacks the compact capability")
	}
}

func TestContextSeamAfterOnlyDeclared(t *testing.T) {
	a := mkPlugin("a", Capabilities{AfterResponse: true})
	plugins := []*Plugin{a}
	seam, err := NewContextSeam(plugins, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if len(seam.after) != 1 || seam.after[0].Name != "a" {
		t.Errorf("after chain = %v, want [a]", seam.after)
	}
	if len(seam.before) != 0 {
		t.Errorf("before chain = %v, want empty", seam.before)
	}
}

func TestPluginsInOrderFollowsDiscoveryOrder(t *testing.T) {
	m := &Manager{
		plugins:     map[string]*Plugin{"b": mkPlugin("b", Capabilities{}), "a": mkPlugin("a", Capabilities{})},
		pluginOrder: []string{"b", "a"},
	}
	ordered := m.PluginsInOrder()
	if len(ordered) != 2 || ordered[0].Name != "b" || ordered[1].Name != "a" {
		t.Fatalf("PluginsInOrder = %v, want [b a]", pluginNames2(ordered))
	}
	// A plugin present in the map but not yet in discovery order is appended.
	m.pluginOrder = append(m.pluginOrder, "c")
	m.plugins["c"] = mkPlugin("c", Capabilities{})
	out := m.PluginsInOrder()
	if len(out) != 3 || out[2].Name != "c" {
		t.Fatalf("PluginsInOrder after add = %v, want [b a c]", pluginNames2(out))
	}
}

func TestCapabilitiesPresenceMask(t *testing.T) {
	c := Capabilities{Tools: true, BeforeRequest: true, CompactHook: true}
	mask := c.Mask()
	if mask&PresenceTools == 0 {
		t.Error("mask should include PresenseTools")
	}
	if mask&PresenceBeforeRequest == 0 {
		t.Error("mask should include PresenceBeforeRequest")
	}
	if mask&PresenceCompact == 0 {
		t.Error("mask should include PresenceCompact")
	}
	if mask&PresenceAfterResponse != 0 {
		t.Error("mask should not include PresenceAfterResponse when not declared")
	}
	if mask&PresenceCondense != 0 {
		t.Error("mask should not include PresenceCondense when not declared")
	}
}
