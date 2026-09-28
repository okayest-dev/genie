package plugin

// Hook circuit breaker (og-9xd). A hook that fails degrades by default — the
// seam skips it, keeps prior contributions, the turn proceeds — which is the
// right default for a transient failure and the wrong one for a deterministic
// one. A plugin broken on response_ready is invoked on every text delta of
// every turn, producing a warning, a stderr line and a subprocess round-trip
// each time, forever.
//
// So each (plugin, event) carries its own breaker. Consecutive failures
// increment that event's counter and a success on the same event clears it; at
// the threshold the event trips and the plugin is no longer called for it. The
// per-occurrence warning is suppressed from then on, replaced by one trip
// notice. After a fixed cooldown a single half-open probe is admitted:
// success closes the event and emits a recovery notice, failure re-trips it.
//
// Liveness is not health. An ErrPluginInactive means the process is gone, not
// that the hook is broken, so it neither increments nor resets a counter, never
// trips, and emits its own one-time notice.
//
// The state lives on *Plugin rather than on a seam because the two seams are
// built separately over the same *Plugin pointers: state held there is shared
// for free, so one plugin tripped on three lifecycle events and two context
// events is one problem and reports one tripped set.

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hook event names, shared by both seams' breaker bookkeeping and reported to
// the user verbatim in trip notices and /help labels.
const (
	HookRequestBuilt  = "request_built"
	HookToolBefore    = "tool_before"
	HookToolAfter     = "tool_after"
	HookResponseReady = "response_ready"
	HookTurnError     = "turn_error"

	HookBeforeRequest = "before_request"
	HookAfterResponse = "after_response"
	HookCompact       = "compact"
	HookCondense      = "condense"
)

// HookBreakerPolicy is the per-(plugin, event) circuit-breaker policy. A
// Threshold of zero disables the breaker entirely (every failure degrades as
// before); a Recovery of zero makes it a one-way door for the process lifetime.
type HookBreakerPolicy struct {
	Threshold int
	Recovery  time.Duration
}

// hookEvent is one (plugin, event) breaker's state. A zero trippedAt means the
// event is closed.
type hookEvent struct {
	failures  int
	trippedAt time.Time
	probing   bool
}

// hookBreaker is a plugin's whole breaker: the policy, the notice sink, and one
// hookEvent per event that has ever been called.
type hookBreaker struct {
	mu     sync.Mutex
	policy HookBreakerPolicy
	notify func(string)
	now    func() time.Time

	events   map[string]*hookEvent
	single   map[string]bool
	liveness bool
}

// initHooks installs the breaker on a loaded plugin. Every plugin gets one: the
// state is per (plugin, event) and shared by both seams.
func (p *Plugin) initHooks(policy HookBreakerPolicy, notify func(string)) {
	p.hooks.policy = policy
	p.hooks.notify = notify
	p.hooks.now = time.Now
	p.hooks.events = make(map[string]*hookEvent)
	p.hooks.single = make(map[string]bool)
}

// markSingleActive records that the plugin is the single-active implementation
// of a single-active event (compact, condense). The built-in takes over while
// the event is tripped, so the trip notice has to say so — a silent fallback
// would replace one silent failure with another.
func (p *Plugin) markSingleActive(event string) {
	p.hooks.mu.Lock()
	defer p.hooks.mu.Unlock()
	p.hooks.singleInit()
	p.hooks.single[event] = true
}

func (b *hookBreaker) singleInit() {
	if b.single == nil {
		b.single = make(map[string]bool)
	}
}

func (b *hookBreaker) eventFor(event string) *hookEvent {
	if b.events == nil {
		b.events = make(map[string]*hookEvent)
	}
	e, ok := b.events[event]
	if !ok {
		e = &hookEvent{}
		b.events[event] = e
	}
	return e
}

// enabled reports whether the breaker is doing anything at all. A threshold of
// zero or less leaves the seams' degrade-by-default behaviour untouched.
func (b *hookBreaker) enabled() bool { return b.policy.Threshold > 0 }

// wouldAdmit reports whether a call on event should go through right now: a
// closed event always; a tripped one only once per cooldown, as a single
// half-open probe. It must be called with b.mu held. AdmitHook claims the
// probe (so concurrent callers of a per-delta event cannot each probe);
// HookAdmitted reads the same verdict without claiming it (so the single-active
// seams can let the built-in cover the whole tripped window except the probe).
func (b *hookBreaker) wouldAdmit(event string) bool {
	if !b.enabled() {
		return true
	}
	e := b.eventFor(event)
	if e.trippedAt.IsZero() {
		return true
	}
	return b.policy.Recovery > 0 && !e.probing && b.now().Sub(e.trippedAt) >= b.policy.Recovery
}

// AdmitHook reports whether a hook call on event should be attempted. A closed
// event is always admitted; a tripped one is admitted once per cooldown, as a
// single half-open probe, under the breaker mutex so concurrent callers of a
// per-delta event cannot each probe.
func (p *Plugin) AdmitHook(event string) bool {
	p.hooks.mu.Lock()
	defer p.hooks.mu.Unlock()
	if !p.hooks.wouldAdmit(event) {
		return false
	}
	// A tripped event that passes wouldAdmit is an admission: claim the
	// half-open probe before returning, so a concurrent per-delta caller sees
	// probing and fails fast instead of double-probing.
	if e := p.hooks.eventFor(event); !e.trippedAt.IsZero() {
		e.probing = true
	}
	return true
}

// HookSucceeded records a healthy hook call: the event's consecutive-failure
// counter resets, and a tripped event closes with one recovery notice. A fatal
// declaration is not a success or a failure — it rides on a healthy result and
// is the plugin's deliberate policy, so the seam never calls either.
func (p *Plugin) HookSucceeded(event string) {
	p.hooks.mu.Lock()
	defer p.hooks.mu.Unlock()
	e := p.hooks.eventFor(event)
	wasTripped := !e.trippedAt.IsZero()
	*e = hookEvent{}
	if wasTripped {
		slog.Info("plugin hook recovered", "plugin", p.Name, "event", event)
		p.hooks.emit(p.recoveryNotice(event))
	}
}

// HookFailed records a failed hook call and returns the degradation message the
// seam should surface, or "" when the breaker suppresses per-occurrence output
// because the trip notice (or the liveness notice) already covers it.
func (p *Plugin) HookFailed(event string, err error) string {
	p.hooks.mu.Lock()
	defer p.hooks.mu.Unlock()

	// Liveness: the process is gone, not the hook. It carries no hook-health
	// information, so it neither increments nor resets a counter and never
	// trips; a one-time notice tells the user their plugin was killed.
	if errors.Is(err, ErrPluginInactive) {
		e := p.hooks.eventFor(event)
		e.probing = false
		if p.hooks.liveness {
			return ""
		}
		p.hooks.liveness = true
		slog.Warn("plugin inactive", "plugin", p.Name, "event", event, "detail", err)
		p.hooks.emit(fmt.Sprintf("plugin %q was killed or stopped responding; it is inactive for the rest of this session (surfaced by %s): %v", p.Name, event, err))
		return ""
	}

	e := p.hooks.eventFor(event)
	if !p.hooks.enabled() {
		return p.degradeMessage(event, err)
	}
	if !e.trippedAt.IsZero() {
		// Either the breaker is holding the event out, or this failure is a
		// re-tripping half-open probe. Either way the trip notice stands.
		if e.probing {
			e.trippedAt = p.hooks.now()
		}
		e.probing = false
		slog.Debug("plugin hook suppressed while tripped",
			"plugin", p.Name, "event", event, "failures", e.failures, "detail", err)
		return ""
	}

	e.failures++
	e.probing = false
	if e.failures < p.hooks.policy.Threshold {
		return p.degradeMessage(event, err)
	}
	e.trippedAt = p.hooks.now()
	e.failures = 0
	slog.Warn("plugin hook tripped", "plugin", p.Name, "event", event, "threshold", p.hooks.policy.Threshold)
	p.hooks.emit(p.tripNotice(event))
	return ""
}

// HookTripped reports whether the breaker has excluded the plugin from event.
func (p *Plugin) HookTripped(event string) bool {
	p.hooks.mu.Lock()
	defer p.hooks.mu.Unlock()
	return p.hooks.enabled() && !p.hooks.eventFor(event).trippedAt.IsZero()
}

// HookAdmitted is the read-only form of AdmitHook: whether a call on event
// would be attempted right now, without claiming the probe. The single-active
// seams use it to decide whether the built-in takes over — the built-in covers
// the whole tripped window except the probe itself, so a tripped
// compact/condense plugin still gets its half-open chance to recover instead of
// being pinned out for the process lifetime.
func (p *Plugin) HookAdmitted(event string) bool {
	p.hooks.mu.Lock()
	defer p.hooks.mu.Unlock()
	return p.hooks.wouldAdmit(event)
}

// TrippedHooks returns the plugin's currently tripped hook events in a stable
// order, spanning both seams. It is what /help labels the plugin's block with.
func (p *Plugin) TrippedHooks() []string {
	p.hooks.mu.Lock()
	defer p.hooks.mu.Unlock()
	if !p.hooks.enabled() {
		return nil
	}
	return p.trippedSet()
}

// degradeMessage is the per-occurrence degradation text. It is logged at Warn
// by the calling seam, so the breaker only has to build the wording.
func (p *Plugin) degradeMessage(event string, err error) string {
	return fmt.Sprintf("%s hook %q: %v", event, p.Name, err)
}

// tripNotice is the single user-visible line that replaces the per-occurrence
// warning. It names the whole tripped set — a plugin can be out of three
// lifecycle events and healthy on two — plus the policy and the keys that
// change it, so the user can act without reading the docs.
func (p *Plugin) tripNotice(justTripped string) string {
	policy := p.hooks.policy
	var b strings.Builder
	fmt.Fprintf(&b, "plugin %q tripped on hooks [%s] after %d consecutive failures; those hooks are disabled",
		p.Name, strings.Join(p.trippedSet(), ", "), policy.Threshold)
	if policy.Recovery <= 0 {
		b.WriteString(" for the lifetime of this process (hook_recovery_seconds = 0)")
	} else {
		fmt.Fprintf(&b, " for %s, then a single re-probe", formatCooldown(policy.Recovery))
	}
	if p.hooks.single[justTripped] {
		b.WriteString("; the built-in implementation has taken over")
	}
	b.WriteString(" — set [plugins] hook_failure_threshold or hook_recovery_seconds to change this")
	return b.String()
}

// recoveryNotice names the events that came back, one line per recovery.
func (p *Plugin) recoveryNotice(event string) string {
	return fmt.Sprintf("plugin %q recovered on hook %q and is participating again", p.Name, event)
}

// trippedSet is the sorted set of events the breaker is currently holding out.
func (p *Plugin) trippedSet() []string {
	var out []string
	for event, e := range p.hooks.events {
		if !e.trippedAt.IsZero() {
			out = append(out, event)
		}
	}
	sort.Strings(out)
	return out
}

// degradeHook records a failed hook call against the plugin's per-(plugin,
// event) circuit breaker and routes the per-occurrence message it asks for to
// the caller's visible sink. A tripped event returns no message: its one trip
// notice already covers the event, and repeating it per occurrence is the noise
// the breaker exists to stop. Both seams call this — the breaker owns the
// counter and the suppression, so the same (plugin, event) degrades identically
// no matter which seam saw the failure.
func degradeHook(degrade func(string), p *Plugin, event string, err error) {
	if msg := p.HookFailed(event, err); msg != "" {
		degrade(msg)
	}
}

// emit delivers a notice to the sink. Notices are emitted while the breaker
// mutex is held, which is what makes them exactly-once per transition no matter
// how many goroutines or seams observe the same trip.
func (b *hookBreaker) emit(msg string) {
	if b.notify != nil {
		b.notify(msg)
	}
}

// formatCooldown renders a cooldown in whole seconds or minutes so the notice
// does not read "1m0s" for the common case.
func formatCooldown(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}
