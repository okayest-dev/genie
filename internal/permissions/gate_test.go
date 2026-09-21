package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/tools"
)

// fakeNegotiator replays a scripted response list and records the prompts.
type fakeNegotiator struct {
	responses []Response
	prompts   []string
	err       error
}

func (f *fakeNegotiator) Negotiate(_ context.Context, axis Axis, scope string) (Response, error) {
	f.prompts = append(f.prompts, string(axis)+" "+scope)
	if f.err != nil {
		return "", f.err
	}
	if len(f.responses) == 0 {
		return ResponseReject, nil
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return r, nil
}

// fakeTool declares fixed requirements.
type fakeTool struct {
	reqs []tools.Requirement
	err  error
}

func (f fakeTool) RequiredPermissions(json.RawMessage) ([]tools.Requirement, error) {
	return f.reqs, f.err
}

func noArgs() json.RawMessage { return json.RawMessage(`{}`) }

func TestGateCoveredRequirementSkipsNegotiation(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{}
	gate := NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "read", Scope: "/work/x.go"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatal("covered requirement should allow without negotiation")
	}
	if len(neg.prompts) != 0 {
		t.Errorf("negotiated %v, want none", neg.prompts)
	}
}

func TestGateSessionGrantAllowsAndPersists(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponseSession}}
	gate := NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "write", Scope: "/work/new.txt"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatalf("session grant should allow; denied=%q", d.Denied)
	}
	if d.Granted != "Permission granted: write /work/new.txt" {
		t.Errorf("Granted = %q", d.Granted)
	}
	if !store.Covered(AxisWrite, "/work/new.txt") {
		t.Error("session grant did not persist in the store")
	}
}

func TestGateOnceGrantSpentOnSettle(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponseOnce}}
	gate := NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "write", Scope: "/work/once.txt"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatal("once grant should allow")
	}
	if !store.Covered(AxisWrite, "/work/once.txt") {
		t.Error("once grant should cover for the life of the call")
	}
	gate.Settle(d)
	if store.Covered(AxisWrite, "/work/once.txt") {
		t.Error("once grant carried forward after Settle")
	}
}

func TestGateRejectDoesNotExecuteOrLeakOnce(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponseReject}}
	gate := NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "write", Scope: "/work/no.txt"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Allow {
		t.Fatal("rejected requirement must not allow")
	}
	want := "Permission rejected: write /work/no.txt — consider an alternative\n" +
		"status: call not executed\n" +
		"hint: granted axes remain available — reformulate without the denied axis."
	if d.Denied != want {
		t.Errorf("Denied:\n got: %q\nwant: %q", d.Denied, want)
	}
	if store.Covered(AxisWrite, "/work/no.txt") {
		t.Error("rejected call left a once grant behind")
	}
}

func TestGateChainContinuesAfterRejectAndKeepsGrants(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponseReject, ResponseSession}}
	gate := NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "write", Scope: "/work/no.txt"},
		{Axis: "run", Scope: "python3"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Allow {
		t.Fatal("a rejected axis must deny the call overall")
	}
	if len(neg.prompts) != 2 {
		t.Fatalf("prompts = %v, want 2 (chain continues past reject)", neg.prompts)
	}
	if !store.Covered(AxisRun, "python3") {
		t.Error("session grant from the chain was not kept")
	}
	if store.Covered(AxisWrite, "/work/no.txt") {
		t.Error("rejected write should not be granted")
	}
}

func TestGatePermanentGrantPersistsThroughSink(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponsePermanent}}
	var sunk []Grant
	gate := NewGate(store, neg, func(g Grant) error {
		sunk = append(sunk, g)
		return nil
	})

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "net", Scope: "api.github.com:443"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatal("permanent grant should allow")
	}
	if len(sunk) != 1 || sunk[0].Axis != AxisNet || sunk[0].Scope != "api.github.com:443" || sunk[0].Tier != TierPermanent {
		t.Fatalf("sink got %+v", sunk)
	}
	if !store.Covered(AxisNet, "api.github.com:443") {
		t.Error("permanent grant not in store")
	}
}

func TestGatePermanentSinkErrorAborts(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponsePermanent}}
	gate := NewGate(store, neg, func(Grant) error { return errors.New("disk full") })

	_, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "write", Scope: "/work/x"},
	}}, noArgs())
	if err == nil {
		t.Fatal("sink error should abort the gate")
	}
	if store.Covered(AxisWrite, "/work/x") {
		t.Error("failed persistence should not leave a grant")
	}
}

func TestGateNegotiatesInAxisOrder(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponseOnce, ResponseOnce, ResponseOnce, ResponseOnce}}
	gate := NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "net", Scope: "example.com:443"},
		{Axis: "run", Scope: "git"},
		{Axis: "write", Scope: "/etc/x"},
		{Axis: "read", Scope: "/etc/passwd"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatal("all granted should allow")
	}
	want := []string{
		"read /etc/passwd",
		"write /etc/x",
		"net example.com:443",
		"run git",
	}
	if strings.Join(neg.prompts, "|") != strings.Join(want, "|") {
		t.Errorf("prompt order = %v, want %v", neg.prompts, want)
	}
	// Feedback order follows the same fixed axis order.
	gotLines := strings.Split(d.Granted, "\n")
	if len(gotLines) != 4 || !strings.HasPrefix(gotLines[0], "Permission granted: read") ||
		!strings.HasPrefix(gotLines[3], "Permission granted: run") {
		t.Errorf("grant line order = %q", d.Granted)
	}
}

func TestGateNormalizesGrantFeedbackScope(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponseSession}}
	gate := NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "write", Scope: "sub/x.go"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Granted != "Permission granted: write /work/sub/x.go" {
		t.Errorf("Granted = %q, want normalized scope", d.Granted)
	}
	if !store.Covered(AxisWrite, "sub/x.go") {
		t.Error("normalized grant should cover the relative request")
	}
}

func TestGateBlanketRequirementFeedbackOmitsScope(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponseSession}}
	gate := NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "net", Scope: ""},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Granted != "Permission granted: net" {
		t.Errorf("Granted = %q, want scope omitted", d.Granted)
	}
	if !store.Covered(AxisNet, "") {
		t.Error("blanket grant should cover a blanket request")
	}
}

func TestGateRequirementErrorPropagates(t *testing.T) {
	store := New("/work")
	gate := NewGate(store, &fakeNegotiator{}, nil)
	_, err := gate.Check(context.Background(), "c1", fakeTool{err: errors.New("boom")}, noArgs())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want wrapped requirement error", err)
	}
}

func TestGateNegotiatorErrorAbortsAndDiscardsOnce(t *testing.T) {
	store := New("/work")
	neg := &fakeNegotiator{responses: []Response{ResponseOnce}, err: errors.New("stdin closed")}
	gate := NewGate(store, neg, nil)

	_, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "write", Scope: "/work/x"},
	}}, noArgs())
	if err == nil {
		t.Fatal("negotiator error should abort")
	}
	if store.Covered(AxisWrite, "/work/x") {
		t.Error("aborted negotiation left a grant behind")
	}
}

func TestGateNoRequirementsAllows(t *testing.T) {
	gate := NewGate(New("/work"), &fakeNegotiator{}, nil)
	d, err := gate.Check(context.Background(), "c1", fakeTool{}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatal("tool with no requirements should allow")
	}
}

// TestDenyAllAlwaysRejects: the headless auto-deny negotiator rejects every
// requirement and never errors.
func TestDenyAllAlwaysRejects(t *testing.T) {
	resp, err := DenyAll{}.Negotiate(context.Background(), AxisRun, "ls")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if resp != ResponseReject {
		t.Fatalf("resp = %q, want reject", resp)
	}
}

// TestApproveAllAlwaysApproves: the headless --approve-all negotiator
// approves every requirement as an in-memory session grant (never persisted)
// and never errors.
func TestApproveAllAlwaysApproves(t *testing.T) {
	resp, err := ApproveAll{}.Negotiate(context.Background(), AxisWrite, "/work/x.txt")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if resp != ResponseSession {
		t.Fatalf("resp = %q, want session grant", resp)
	}
}

func TestGateApproveAllGrantsAndNeverPersists(t *testing.T) {
	store := New("/work")
	var sunk []Grant
	gate := NewGate(store, ApproveAll{}, func(g Grant) error {
		sunk = append(sunk, g)
		return nil
	})

	d, err := gate.Check(context.Background(), "c1", fakeTool{reqs: []tools.Requirement{
		{Axis: "write", Scope: "/work/new.txt"},
	}}, noArgs())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatalf("approve-all should allow; denied=%q", d.Denied)
	}
	if d.Granted != "Permission granted: write /work/new.txt" {
		t.Errorf("Granted = %q", d.Granted)
	}
	if !store.Covered(AxisWrite, "/work/new.txt") {
		t.Error("approve-all grant should cover for the run")
	}
	if len(sunk) != 0 {
		t.Errorf("approve-all must never persist through the sink; sunk=%+v", sunk)
	}
}
