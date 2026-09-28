package plugin

// Unit tests for the per-(plugin, event) hook circuit breaker (og-9xd). These
// drive the breaker directly with a fake clock; the seam-level behaviour (call
// counts, notice counts through onDegrade) is asserted in lifecycle_test.go and
// context_test.go.

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// breakerFake is a plugin wired to a fake clock and a counting notice sink.
type breakerFake struct {
	t       *testing.T
	p       *Plugin
	now     time.Time
	notices []string
}

func newBreakerFake(t *testing.T, threshold int, recovery time.Duration) *breakerFake {
	t.Helper()
	f := &breakerFake{t: t, now: time.Unix(1_700_000_000, 0), p: &Plugin{Name: "tmpl-broken"}}
	f.p.initHooks(HookBreakerPolicy{Threshold: threshold, Recovery: recovery}, func(msg string) {
		f.notices = append(f.notices, msg)
	})
	f.p.hooks.now = func() time.Time { return f.now }
	return f
}

func (f *breakerFake) advance(d time.Duration) { f.now = f.now.Add(d) }

// fail drives one failed call on event, returning the degradation message the
// seam should surface ("" means the breaker suppressed it).
func (f *breakerFake) fail(event string) string {
	f.t.Helper()
	if !f.p.AdmitHook(event) {
		return ""
	}
	return f.p.HookFailed(event, errors.New("boom"))
}

func (f *breakerFake) ok(event string) {
	f.t.Helper()
	if !f.p.AdmitHook(event) {
		return
	}
	f.p.HookSucceeded(event)
}

func TestBreakerTripsAtThresholdAndStopsCalling(t *testing.T) {
	f := newBreakerFake(t, 3, time.Minute)
	for i := 1; i <= 2; i++ {
		if msg := f.fail(HookResponseReady); msg == "" {
			t.Fatalf("failure %d below threshold should still degrade", i)
		}
	}
	if msg := f.fail(HookResponseReady); msg != "" {
		t.Errorf("tripping failure should not also degrade, got %q", msg)
	}
	for i := 0; i < 20; i++ {
		if f.p.AdmitHook(HookResponseReady) {
			t.Fatal("tripped event admitted a call")
		}
	}
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d (%v), want exactly 1 trip notice", len(f.notices), f.notices)
	}
}

func TestBreakerTripNoticeContent(t *testing.T) {
	f := newBreakerFake(t, 3, 90*time.Second)
	f.fail(HookToolBefore)
	f.fail(HookToolBefore)
	f.fail(HookToolBefore)
	got := f.notices[0]
	for _, want := range []string{"tmpl-broken", "tool_before", "3 consecutive", "90s", "hook_failure_threshold", "hook_recovery_seconds"} {
		if !strings.Contains(got, want) {
			t.Errorf("trip notice %q missing %q", got, want)
		}
	}
}

func TestBreakerTripNoticeNamesWholeTrippedSet(t *testing.T) {
	f := newBreakerFake(t, 1, time.Minute)
	f.fail(HookToolBefore)
	f.fail(HookToolAfter)
	f.fail(HookTurnError)
	if len(f.notices) != 3 {
		t.Fatalf("notices = %d, want one per trip transition", len(f.notices))
	}
	// The last notice reports the full set, so the user sees every hook the
	// plugin is out of, not just the one that just tripped.
	got := f.notices[2]
	for _, want := range []string{HookToolBefore, HookToolAfter, HookTurnError} {
		if !strings.Contains(got, want) {
			t.Errorf("trip notice %q missing tripped event %q", got, want)
		}
	}
}

func TestBreakerEventsAreCountedIndependently(t *testing.T) {
	f := newBreakerFake(t, 3, time.Minute)
	for i := 0; i < 5; i++ {
		f.fail(HookToolBefore)
	}
	if !f.p.HookTripped(HookToolBefore) {
		t.Error("tool_before should be tripped")
	}
	if f.p.HookTripped(HookToolAfter) {
		t.Error("tool_after must be counted separately, so it is still healthy")
	}
	if !f.p.AdmitHook(HookToolAfter) {
		t.Error("a healthy event must still be called after another trips")
	}
}

func TestBreakerSuccessResetsCounter(t *testing.T) {
	f := newBreakerFake(t, 3, time.Minute)
	f.fail(HookRequestBuilt)
	f.fail(HookRequestBuilt)
	f.ok(HookRequestBuilt)
	f.fail(HookRequestBuilt)
	f.fail(HookRequestBuilt)
	if f.p.HookTripped(HookRequestBuilt) {
		t.Error("two failures, a success, two more failures must not trip")
	}
	if len(f.notices) != 0 {
		t.Errorf("notices = %v, want none", f.notices)
	}
}

func TestBreakerSuccessOnAnotherEventDoesNotReset(t *testing.T) {
	f := newBreakerFake(t, 3, time.Minute)
	f.fail(HookRequestBuilt)
	f.fail(HookRequestBuilt)
	f.ok(HookToolAfter)
	f.fail(HookRequestBuilt)
	if !f.p.HookTripped(HookRequestBuilt) {
		t.Error("a success on a different event must not clear this event's counter")
	}
}

func TestBreakerThresholdOneTripsOnFirstFailure(t *testing.T) {
	f := newBreakerFake(t, 1, time.Minute)
	if msg := f.fail(HookRequestBuilt); msg != "" {
		t.Errorf("threshold 1 should trip on the first failure, got %q", msg)
	}
	if !f.p.HookTripped(HookRequestBuilt) {
		t.Error("want tripped")
	}
}

func TestBreakerZeroThresholdNeverTrips(t *testing.T) {
	f := newBreakerFake(t, 0, time.Minute)
	for i := 0; i < 50; i++ {
		if msg := f.fail(HookRequestBuilt); msg == "" {
			t.Fatalf("failure %d degraded silently with the breaker disabled", i)
		}
		if !f.p.AdmitHook(HookRequestBuilt) {
			t.Fatalf("failure %d: disabled breaker admitted no call", i)
		}
	}
}

func TestBreakerSuppressesWhileTripped(t *testing.T) {
	f := newBreakerFake(t, 1, time.Minute)
	f.fail(HookResponseReady)
	before := len(f.notices)
	for i := 0; i < 10; i++ {
		if msg := f.fail(HookResponseReady); msg != "" {
			t.Fatalf("call %d: tripped event produced degradation %q", i, msg)
		}
	}
	if len(f.notices) != before {
		t.Errorf("tripped event emitted %d extra notices, want 0", len(f.notices)-before)
	}
}

func TestBreakerAdmitsOneProbeAfterCooldown(t *testing.T) {
	f := newBreakerFake(t, 1, 30*time.Second)
	f.fail(HookResponseReady)
	f.advance(29 * time.Second)
	if f.p.AdmitHook(HookResponseReady) {
		t.Error("probe admitted before the cooldown elapsed")
	}
	f.advance(time.Second)
	if !f.p.AdmitHook(HookResponseReady) {
		t.Fatal("probe not admitted after the cooldown")
	}
	// Single-flight: response_ready fires per delta, so only the first caller
	// in the window gets the probe.
	for i := 0; i < 20; i++ {
		if f.p.AdmitHook(HookResponseReady) {
			t.Fatal("second probe admitted within the same cooldown window")
		}
	}
}

func TestBreakerProbeSuccessClosesAndNotifies(t *testing.T) {
	f := newBreakerFake(t, 1, 30*time.Second)
	f.fail(HookResponseReady)
	f.advance(30 * time.Second)
	if !f.p.AdmitHook(HookResponseReady) {
		t.Fatal("probe not admitted")
	}
	f.p.HookSucceeded(HookResponseReady)
	if f.p.HookTripped(HookResponseReady) {
		t.Error("successful probe should close the event")
	}
	if !f.p.AdmitHook(HookResponseReady) {
		t.Error("closed event must be called again")
	}
	if len(f.notices) != 2 {
		t.Fatalf("notices = %v, want trip + recovery", f.notices)
	}
	if !strings.Contains(f.notices[1], "response_ready") {
		t.Errorf("recovery notice %q should name the recovered event", f.notices[1])
	}
}

func TestBreakerProbeFailureRetripsWithoutNewNotice(t *testing.T) {
	f := newBreakerFake(t, 1, 30*time.Second)
	f.fail(HookResponseReady)
	f.advance(30 * time.Second)
	if !f.p.AdmitHook(HookResponseReady) {
		t.Fatal("probe not admitted")
	}
	if msg := f.p.HookFailed(HookResponseReady, errors.New("boom")); msg != "" {
		t.Errorf("re-tripping probe should not degrade, got %q", msg)
	}
	if !f.p.HookTripped(HookResponseReady) {
		t.Error("failed probe should leave the event tripped")
	}
	if len(f.notices) != 1 {
		t.Errorf("notices = %v, want the original trip notice only", f.notices)
	}
	// No further call until the next cooldown.
	if f.p.AdmitHook(HookResponseReady) {
		t.Error("admitted a call right after a failed probe")
	}
	f.advance(30 * time.Second)
	if !f.p.AdmitHook(HookResponseReady) {
		t.Error("next cooldown should admit a probe")
	}
}

func TestBreakerZeroRecoveryIsOneWayDoor(t *testing.T) {
	f := newBreakerFake(t, 1, 0)
	f.fail(HookRequestBuilt)
	for i := 0; i < 10; i++ {
		f.advance(time.Hour)
		if f.p.AdmitHook(HookRequestBuilt) {
			t.Fatalf("probe admitted with hook_recovery_seconds = 0 (attempt %d)", i)
		}
	}
}

func TestBreakerLivenessIsNotHealth(t *testing.T) {
	f := newBreakerFake(t, 2, time.Minute)
	for i := 0; i < 5; i++ {
		f.advance(time.Hour)
		if msg := f.p.HookFailed(HookRequestBuilt, ErrPluginInactive); msg != "" {
			t.Errorf("liveness failure degraded with %q, want a one-time notice instead", msg)
		}
	}
	if f.p.HookTripped(HookRequestBuilt) {
		t.Error("a liveness failure must not trip the breaker")
	}
	if len(f.notices) != 1 {
		t.Fatalf("notices = %v, want exactly one liveness notice", f.notices)
	}
	for _, want := range []string{"tmpl-broken", "request_built", "killed"} {
		if !strings.Contains(f.notices[0], want) {
			t.Errorf("liveness notice %q missing %q", f.notices[0], want)
		}
	}
}

func TestBreakerLivenessDoesNotResetHealthCounter(t *testing.T) {
	f := newBreakerFake(t, 2, time.Minute)
	if msg := f.fail(HookRequestBuilt); msg == "" {
		t.Fatal("first health failure should degrade")
	}
	f.p.HookFailed(HookRequestBuilt, ErrPluginInactive)
	if msg := f.fail(HookRequestBuilt); msg != "" {
		t.Error("a liveness failure carries no hook-health information and must not clear the counter")
	}
}

func TestBreakerSingleActiveTripNoticeNamesFallback(t *testing.T) {
	f := newBreakerFake(t, 1, time.Minute)
	f.p.markSingleActive(HookCompact)
	f.fail(HookCompact)
	if !strings.Contains(f.notices[0], "built-in") {
		t.Errorf("trip notice %q should say the built-in took over for a single-active hook", f.notices[0])
	}
}

func TestBreakerSingleActiveNoticeStaysQuietForChainedEvents(t *testing.T) {
	f := newBreakerFake(t, 1, time.Minute)
	f.fail(HookBeforeRequest)
	if strings.Contains(f.notices[0], "built-in") {
		t.Errorf("a chained event has no built-in fallback; notice %q should not claim one", f.notices[0])
	}
}

func TestBreakerTrippedHooksSorted(t *testing.T) {
	f := newBreakerFake(t, 1, time.Minute)
	f.fail(HookTurnError)
	f.fail(HookRequestBuilt)
	got := f.p.TrippedHooks()
	want := []string{HookRequestBuilt, HookTurnError}
	if len(got) != len(want) {
		t.Fatalf("TrippedHooks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TrippedHooks[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBreakerProbeIsSingleFlight(t *testing.T) {
	f := newBreakerFake(t, 1, 30*time.Second)
	f.fail(HookResponseReady)
	f.advance(30 * time.Second)

	const callers = 32
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.p.AdmitHook(HookResponseReady) {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := admitted.Load(); got != 1 {
		t.Errorf("admitted %d probes under concurrency, want exactly 1", got)
	}
}

// TestBreakerHookAdmittedMatchesProbeWindow pins the coupling between the
// read-only HookAdmitted verdict and the claiming AdmitHook. They must agree,
// because the single-active seams route on HookAdmitted and AdmitHook consumes
// the probe: if one drifted, a tripped compact/condense plugin would either
// get the built-in forever (no probe, no recovery) or be probed on every call.
func TestBreakerHookAdmittedMatchesProbeWindow(t *testing.T) {
	f := newBreakerFake(t, 1, time.Minute)
	if !f.p.HookAdmitted(HookCompact) {
		t.Fatal("healthy event must be admitted")
	}
	if msg := f.fail(HookCompact); msg != "" {
		t.Fatalf("tripping failure should not degrade, got %q", msg)
	}
	if f.p.HookAdmitted(HookCompact) {
		t.Fatal("tripped event reported admitted before the cooldown")
	}
	if f.p.AdmitHook(HookCompact) {
		t.Fatal("tripped event admitted a call before the cooldown")
	}

	f.advance(time.Minute)
	if !f.p.HookAdmitted(HookCompact) {
		t.Fatal("probe-due event must be reported admitted")
	}
	if !f.p.AdmitHook(HookCompact) {
		t.Fatal("probe not admitted once the cooldown elapsed")
	}
	if f.p.HookAdmitted(HookCompact) {
		t.Fatal("probing event must be reported excluded: a second caller cannot get the probe too")
	}
	if f.p.AdmitHook(HookCompact) {
		t.Fatal("second caller double-probed")
	}

	if msg := f.p.HookFailed(HookCompact, errors.New("boom")); msg != "" {
		t.Fatalf("a re-tripping probe should not degrade, got %q", msg)
	}
	if f.p.HookAdmitted(HookCompact) {
		t.Fatal("re-tripped event reported admitted")
	}
}

// TestBreakerSingleActiveRecoversThroughProbe walks the whole single-active
// lifetime through the seam's own built-in switch: healthy runs the plugin, a
// trip hands the job to the built-in for the cooldown, the cooldown's one probe
// goes back to the plugin, and a probe success hands the job back. This is the
// regression test for "tripped compact/condense plugin can never recover".
func TestBreakerSingleActiveRecoversThroughProbe(t *testing.T) {
	f := newBreakerFake(t, 1, time.Minute)
	f.p.Capabilities = Capabilities{Version: ProtocolVersion, CompactHook: true}
	seam, err := NewContextSeam([]*Plugin{f.p}, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if seam.CompactBuiltin() {
		t.Fatal("built-in active while the plugin is healthy")
	}

	if msg := f.fail(HookCompact); msg != "" {
		t.Fatalf("tripping failure should not degrade, got %q", msg)
	}
	if !seam.CompactBuiltin() {
		t.Fatal("built-in must cover the cooldown, or the context window is unmanaged")
	}

	f.advance(time.Minute)
	if seam.CompactBuiltin() {
		t.Fatal("built-in must stand aside for the half-open probe")
	}
	if !f.p.AdmitHook(HookCompact) {
		t.Fatal("probe not admitted when due")
	}
	if !seam.CompactBuiltin() {
		t.Fatal("built-in must cover while the probe is in flight, so a second compaction cannot prob")
	}

	f.p.HookSucceeded(HookCompact)
	if seam.CompactBuiltin() {
		t.Fatal("a recovered single-active plugin must run itself again")
	}
}
