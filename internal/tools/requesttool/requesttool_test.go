package requesttool

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/tools"
)

// fakeNegotiator returns canned responses in order and records the axes/scopes
// it was asked to negotiate.
type fakeNegotiator struct {
	responses []permissions.Response
	seen      []string
	err       error
}

func (f *fakeNegotiator) Negotiate(_ context.Context, axis permissions.Axis, scope string) (permissions.Response, error) {
	f.seen = append(f.seen, string(axis)+" "+scope)
	if f.err != nil {
		return "", f.err
	}
	if len(f.responses) == 0 {
		return "", errors.New("unexpected negotiation")
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return r, nil
}

type trackingSink struct {
	grants []permissions.Grant
	err    error
}

func (s *trackingSink) Sink(g permissions.Grant) error {
	if s.err != nil {
		return s.err
	}
	s.grants = append(s.grants, g)
	return nil
}

func xargs(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestName(t *testing.T) {
	if New(permissions.New(t.TempDir()), nil).Name() != "request_permission" {
		t.Fatal("tool name must be request_permission")
	}
}

func TestDescription(t *testing.T) {
	desc := New(permissions.New(t.TempDir()), nil).Description()
	for _, want := range []string{
		"inline",                  // inline-first rule: deny the inline escalation preference
		"widest anticipated need", // wide-scope guidance
		"Permission granted",      // result line contract
		"Permission rejected",     // result line contract
		"directly",                // call the tool directly otherwise
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q", want)
		}
	}
	if !strings.Contains(desc, "mid-call runtime permission denial") {
		t.Errorf("description missing the runtime-denial trigger")
	}
}

func TestParameters(t *testing.T) {
	schema := New(permissions.New(t.TempDir()), nil).Parameters()
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("required must be a list, got %T", schema["required"])
	}
	if len(required) != 1 || required[0] != "permission" {
		t.Fatalf("permission must be the only required field, got %v", required)
	}
	props := schema["properties"].(map[string]any)
	perm := props["permission"].(map[string]any)
	enum, ok := perm["enum"].([]any)
	if !ok {
		t.Fatalf("permission enum must be []any (matched by ValidateArgs), got %T", perm["enum"])
	}
	want := permissions.AxisNames()
	if len(enum) != len(want) {
		t.Fatalf("enum = %v, want %v", enum, want)
	}
	for i, a := range want {
		if enum[i] != a {
			t.Errorf("enum[%d] = %v, want %q", i, enum[i], a)
		}
	}
	// scope must be optional (absent from required) and string-typed.
	scope, ok := props["scope"].(map[string]any)
	if !ok {
		t.Fatal("scope property missing")
	}
	if scope["type"] != "string" {
		t.Errorf("scope type = %v, want string", scope["type"])
	}
}

// TestValidateArgsRealSchema runs the shared argument validator against the
// tool's own Parameters, so the enum path is exercised on the real schema —
// a fenced-code model expressing request_permission as JSON hits this path.
func TestValidateArgsRealSchema(t *testing.T) {
	schema := New(permissions.New(t.TempDir()), nil).Parameters()

	if err := tools.ValidateArgs(xargs(t, map[string]any{"permission": "write"}), schema); err != nil {
		t.Fatalf("valid blanket args rejected: %v", err)
	}
	if err := tools.ValidateArgs(xargs(t, map[string]any{"permission": "net", "scope": "db:5432"}), schema); err != nil {
		t.Fatalf("valid scoped args rejected: %v", err)
	}
	for _, bad := range []any{1, 2.5, nil} {
		if err := tools.ValidateArgs(xargs(t, map[string]any{"permission": bad}), schema); err == nil {
			t.Errorf("non-string permission accepted: %v", bad)
		}
	}
	if err := tools.ValidateArgs(xargs(t, map[string]any{"permission": "sudo"}), schema); err == nil {
		t.Error("invalid axis accepted by the shared validator")
	}
	if err := tools.ValidateArgs(xargs(t, map[string]any{"scope": "x"}), schema); err == nil {
		t.Error("missing permission accepted by the shared validator")
	}
}

func TestExecuteSessionGrant(t *testing.T) {
	cwd := t.TempDir()
	store := permissions.New(cwd)
	neg := &fakeNegotiator{responses: []permissions.Response{permissions.ResponseSession}}
	tool := New(store, nil)
	tool.SetNegotiator(neg)

	scope := filepath.Join(cwd, "work", "report.md")
	out, err := tool.Execute(xargs(t, map[string]any{"permission": "write", "scope": "work/report.md"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Permission granted: write " + scope; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if len(neg.seen) != 1 {
		t.Fatalf("negotiated %d times, want 1", len(neg.seen))
	}
	if want := "write " + scope; neg.seen[0] != want {
		t.Errorf("negotiated %q, want %q", neg.seen[0], want) // normalized scope, not the raw relative path
	}
	// The grant covers the normalized scope, so a later inline call to the same
	// scope is covered without re-prompting.
	if !store.Covered(permissions.AxisWrite, "work/report.md") {
		t.Error("session grant must cover the requested scope afterward")
	}
	if !store.Covered(permissions.AxisWrite, scope) {
		t.Error("session grant must cover the normalized scope")
	}
}

func TestExecutePermanent(t *testing.T) {
	cwd := t.TempDir()
	store := permissions.New(cwd)
	neg := &fakeNegotiator{responses: []permissions.Response{permissions.ResponsePermanent}}
	sink := &trackingSink{}
	tool := New(store, sink.Sink)
	tool.SetNegotiator(neg)

	out, err := tool.Execute(xargs(t, map[string]any{"permission": "env", "scope": "MY_VAR"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Permission granted: env MY_VAR"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if len(sink.grants) != 1 {
		t.Fatalf("sink called %d times, want 1", len(sink.grants))
	}
	g := sink.grants[0]
	if g.Axis != permissions.AxisEnv || g.Scope != "MY_VAR" || g.Tier != permissions.TierPermanent {
		t.Errorf("sink grant = %+v", g)
	}
	if !store.Covered(permissions.AxisEnv, "MY_VAR") {
		t.Error("permanent grant must cover afterwards")
	}
	if got := store.PermanentGrants(); len(got) != 1 {
		t.Errorf("store permanent grants = %d, want 1", len(got))
	}
}

func TestExecutePermanentNoSink(t *testing.T) {
	cwd := t.TempDir()
	store := permissions.New(cwd)
	neg := &fakeNegotiator{responses: []permissions.Response{permissions.ResponsePermanent}}
	tool := New(store, nil)
	tool.SetNegotiator(neg)

	out, err := tool.Execute(xargs(t, map[string]any{"permission": "net", "scope": "EXAMPLE.COM:443"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Permission granted: net example.com:443"; out != want {
		t.Errorf("out = %q, want %q (net scope must be lowercased by Normalize)", out, want)
	}
	if !store.Covered(permissions.AxisNet, "example.com:443") {
		t.Error("exact host must be covered")
	}
}

func TestExecuteOnceSpent(t *testing.T) {
	cwd := t.TempDir()
	store := permissions.New(cwd)
	neg := &fakeNegotiator{responses: []permissions.Response{permissions.ResponseOnce}}
	tool := New(store, nil)
	tool.SetNegotiator(neg)

	scope := "myscript.sh"
	out, err := tool.Execute(xargs(t, map[string]any{"permission": "run", "scope": scope}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Permission granted: run " + scope; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	// A once tier is call-bound to the request_permission call itself and is
	// spent when that call resolves, so it never survives to a later call.
	if store.Covered(permissions.AxisRun, scope) {
		t.Error("once grant must be spent when the request_permission call resolves")
	}
}

func TestExecuteReject(t *testing.T) {
	cwd := t.TempDir()
	store := permissions.New(cwd)
	neg := &fakeNegotiator{responses: []permissions.Response{permissions.ResponseReject}}
	tool := New(store, nil)
	tool.SetNegotiator(neg)

	out, err := tool.Execute(xargs(t, map[string]any{"permission": "write", "scope": "out.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	want := "Permission rejected: write " + filepath.Join(cwd, "out.txt") + " — consider an alternative"
	if out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if store.Covered(permissions.AxisWrite, "out.txt") {
		t.Error("reject must grant nothing")
	}
}

func TestExecuteDenyAllDefault(t *testing.T) {
	// A tool never wired by the REPL stays DenyAll: headless auto-deny.
	cwd := t.TempDir()
	store := permissions.New(cwd)
	tool := New(store, nil) // no SetNegotiator

	out, err := tool.Execute(xargs(t, map[string]any{"permission": "write", "scope": "out.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Permission rejected: write ") {
		t.Errorf("out = %q, want an auto-deny reject line", out)
	}
	if store.Covered(permissions.AxisWrite, "out.txt") {
		t.Error("headless default must never auto-approve")
	}
}

func TestExecuteApproveAll(t *testing.T) {
	// --approve-all routes the headless ApproveAll negotiator into the tool:
	// pre-negotiation grants the run-scoped session and never persists.
	cwd := t.TempDir()
	store := permissions.New(cwd)
	sink := &trackingSink{}
	tool := New(store, sink.Sink)
	tool.SetNegotiator(permissions.ApproveAll{})

	out, err := tool.Execute(xargs(t, map[string]any{"permission": "write", "scope": "out.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Permission granted: write " + filepath.Join(cwd, "out.txt"); out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if !store.Covered(permissions.AxisWrite, "out.txt") {
		t.Error("approve-all grant must cover the requested scope")
	}
	if len(sink.grants) != 0 {
		t.Errorf("approve-all must never persist; sink=%+v", sink.grants)
	}
}

func TestExecuteBlanketScope(t *testing.T) {
	cwd := t.TempDir()
	store := permissions.New(cwd)
	neg := &fakeNegotiator{responses: []permissions.Response{permissions.ResponseSession}}
	tool := New(store, nil)
	tool.SetNegotiator(neg)

	out, err := tool.Execute(xargs(t, map[string]any{"permission": "write"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Permission granted: write"; out != want {
		t.Errorf("out = %q, want %q (blanket grant omits the scope)", out, want)
	}
	// Blanket access on the axis covers any scope, and the model must not be
	// re-prompted on later calls.
	if !store.Covered(permissions.AxisWrite, filepath.Join(cwd, "any", "where.txt")) {
		t.Error("blanket write grant must cover any write scope")
	}
}

func TestExecuteNormalizeRejectsRelative(t *testing.T) {
	// The store cwd roots normalization; the tool never prompts with a raw
	// relative path.
	cwd := t.TempDir()
	neg := &fakeNegotiator{responses: []permissions.Response{permissions.ResponseSession}}
	tool := New(permissions.New(cwd), nil)
	tool.SetNegotiator(neg)

	if _, err := tool.Execute(xargs(t, map[string]any{"permission": "write", "scope": "../escape.txt"})); err != nil {
		t.Fatal(err)
	}
	got := neg.seen[0]
	if strings.Contains(got, "..") {
		t.Errorf("negotiated scope %q must be normalized clear of ..", got)
	}
}

func TestExecuteErrors(t *testing.T) {
	cwd := t.TempDir()

	t.Run("missing permission", func(t *testing.T) {
		tool := New(permissions.New(cwd), nil)
		_, err := tool.Execute(xargs(t, map[string]any{"scope": "x"}))
		if err == nil || !strings.Contains(err.Error(), "permission") {
			t.Errorf("err = %v, want a missing-permission error", err)
		}
	})
	t.Run("invalid permission", func(t *testing.T) {
		tool := New(permissions.New(cwd), nil)
		_, err := tool.Execute(xargs(t, map[string]any{"permission": "sudo"}))
		if err == nil || !strings.Contains(err.Error(), "one of read, write, net, run, env") {
			t.Errorf("err = %v, want invalid-permission error", err)
		}
	})
	t.Run("malformed args", func(t *testing.T) {
		tool := New(permissions.New(cwd), nil)
		_, err := tool.Execute(json.RawMessage(`{"permission":`))
		if err == nil {
			t.Error("want malformed-args error")
		}
	})
	t.Run("negotiation error", func(t *testing.T) {
		tool := New(permissions.New(cwd), nil)
		tool.SetNegotiator(&fakeNegotiator{err: errors.New("boom")})
		_, err := tool.Execute(xargs(t, map[string]any{"permission": "read", "scope": "x"}))
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("err = %v, want negotiator error to propagate", err)
		}
	})
	t.Run("sink error", func(t *testing.T) {
		store := permissions.New(cwd)
		tool := New(store, (&trackingSink{err: errors.New("disk full")}).Sink)
		tool.SetNegotiator(&fakeNegotiator{responses: []permissions.Response{permissions.ResponsePermanent}})
		_, err := tool.Execute(xargs(t, map[string]any{"permission": "read", "scope": "x"}))
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Errorf("err = %v, want sink error to propagate", err)
		}
	})
}

func TestSetNegotiatorIgnoresNil(t *testing.T) {
	cwd := t.TempDir()
	store := permissions.New(cwd)
	tool := New(store, nil)
	tool.SetNegotiator(nil) // must keep DenyAll
	out, err := tool.Execute(xargs(t, map[string]any{"permission": "write", "scope": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Permission rejected: ") {
		t.Errorf("out = %q, want auto-deny after nil SetNegotiator", out)
	}
}

func TestNotAPermissionedCapability(t *testing.T) {
	// The tool is a negotiation channel, not a capability: it must NOT satisfy
	// tools.Permissioned, so the deny-point gate never evaluates it. Requests
	// that call request_permission with covered args simply fail to satisfy the
	// gate's RequiredPermissions signature and can never be gated.
	cwd := t.TempDir()
	tool := New(permissions.New(cwd), nil)
	if _, ok := any(tool).(tools.Permissioned); ok {
		t.Fatal("request_permission must not implement the Permissioned seam")
	}
}
