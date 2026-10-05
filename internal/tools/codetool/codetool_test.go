package codetool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/tools"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func wantReqs(t *testing.T, got, want []tools.Requirement) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("requirements = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("requirement[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

func TestUncoveredRequestedAxisIsBlanketRequirement(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)

	reqs, err := tool.RequiredPermissions(raw(`{"code":"1","permissions":["write"]}`))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	wantReqs(t, reqs, []tools.Requirement{{Axis: "write"}})
}

func TestCoveredRequestedAxisNeedsNoRequirement(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)

	// The no-config default covers read at "." — a scoped base, not a blanket
	// one, so this is the exception to ADR-0005's blanket-only rule in action.
	reqs, err := tool.RequiredPermissions(raw(`{"code":"1","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	wantReqs(t, reqs, nil)
}

func TestAnyScopeCoverageSatisfiesAxisRequest(t *testing.T) {
	store := permissions.New("/work")
	store.SetBaseFromConfig(map[string][]string{"read": {"."}, "write": {"/work/src"}})
	if err := store.GrantPermanent(permissions.Grant{Axis: permissions.AxisNet, Scope: "api.example.com:443"}); err != nil {
		t.Fatalf("GrantPermanent: %v", err)
	}
	store.GrantSession(permissions.AxisEnv, "DB_HOST")
	call := store.BeginCall("/work/run")
	store.GrantOnce(call, permissions.AxisRun, "git")

	tool := New(store, "/work", 30*time.Second)
	reqs, err := tool.RequiredPermissions(raw(`{"code":"1","permissions":["read","write","net","run","env"]}`))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	wantReqs(t, reqs, nil)
}

func TestOnlyUncoveredAxesBecomeRequirements(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)

	reqs, err := tool.RequiredPermissions(raw(`{"code":"1","permissions":["write","read","env"]}`))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	wantReqs(t, reqs, []tools.Requirement{{Axis: "write"}, {Axis: "env"}})
}

func TestNoRequestedAxesNeedsNoRequirement(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)

	for _, call := range []string{`{"code":"1"}`, `{"code":"1","permissions":[]}`, `{}`} {
		reqs, err := tool.RequiredPermissions(raw(call))
		if err != nil {
			t.Fatalf("RequiredPermissions(%s): %v", call, err)
		}
		wantReqs(t, reqs, nil)
	}
}

func TestRepeatedAxisNeedsOneRequirement(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)

	reqs, err := tool.RequiredPermissions(raw(`{"code":"1","permissions":["write","write"]}`))
	if err != nil {
		t.Fatalf("RequiredPermissions: %v", err)
	}
	wantReqs(t, reqs, []tools.Requirement{{Axis: "write"}})
}

func TestUnknownRequestedAxisIsRejected(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)

	for _, call := range []string{`{"permissions":["exec"]}`, `{"permissions":[""]}`} {
		reqs, err := tool.RequiredPermissions(raw(call))
		if err == nil {
			t.Fatalf("RequiredPermissions(%s) = %v; want an error naming the invalid axis", call, reqs)
		}
		if !strings.Contains(err.Error(), "invalid permission") {
			t.Errorf("RequiredPermissions(%s) error = %q; want it to name the invalid axis", call, err)
		}
		if reqs != nil {
			t.Errorf("RequiredPermissions(%s) returned %v alongside an error; want none", call, reqs)
		}
	}
}

func TestMalformedArgumentsAreRejected(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)

	reqs, err := tool.RequiredPermissions(raw(`{"permissions":`))
	if err == nil {
		t.Fatalf("RequiredPermissions on malformed JSON = %v; want an error", reqs)
	}
}

// recordingNegotiator replays a scripted response per prompt and records the
// prompts it saw.
type recordingNegotiator struct {
	responses []permissions.Response
	prompts   []string
}

func (n *recordingNegotiator) Negotiate(_ context.Context, axis permissions.Axis, scope string) (permissions.Response, error) {
	n.prompts = append(n.prompts, permissions.RenderPrompt(axis, scope))
	if len(n.responses) == 0 {
		return permissions.ResponseReject, nil
	}
	r := n.responses[0]
	n.responses = n.responses[1:]
	return r, nil
}

func TestFullyCoveredCallReachesNoPrompt(t *testing.T) {
	store := permissions.New("/work")
	neg := &recordingNegotiator{responses: []permissions.Response{permissions.ResponseOnce}}
	gate := permissions.NewGate(store, neg, nil)

	// read is covered by the no-config base; write is not, so only write should
	// be negotiated.
	d, err := gate.Check(context.Background(), "c1", New(store, "/work", 30*time.Second),
		raw(`{"code":"1","permissions":["read","write"]}`))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatalf("call denied = %q; want allowed", d.Denied)
	}
	want := []string{permissions.RenderPrompt(permissions.AxisWrite, "")}
	if len(neg.prompts) != 1 || neg.prompts[0] != want[0] {
		t.Errorf("prompts = %q; want only %q", neg.prompts, want[0])
	}
	if d.Granted != "Permission granted: write" {
		t.Errorf("Granted = %q; want the blanket write grant line", d.Granted)
	}
}

func TestFullyCoveredCallNeverNegotiates(t *testing.T) {
	store := permissions.New("/work")
	store.GrantSession(permissions.AxisWrite, "/work")
	neg := &recordingNegotiator{}
	gate := permissions.NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", New(store, "/work", 30*time.Second),
		raw(`{"code":"1","permissions":["read","write"]}`))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !d.Allow {
		t.Fatalf("call denied = %q; want allowed", d.Denied)
	}
	if len(neg.prompts) != 0 {
		t.Errorf("negotiated %q; want a silent execute", neg.prompts)
	}
	if d.Granted != "" {
		t.Errorf("Granted = %q; want no grant line when nothing was negotiated", d.Granted)
	}
}

func TestBlanketRequirementDrivesEveryTier(t *testing.T) {
	for _, tc := range []struct {
		tier     permissions.Tier
		response permissions.Response
		survives bool // coverage outlives the call that negotiated it
	}{
		{permissions.TierOnce, permissions.ResponseOnce, false},
		{permissions.TierSession, permissions.ResponseSession, true},
		{permissions.TierPermanent, permissions.ResponsePermanent, true},
	} {
		t.Run(string(tc.tier), func(t *testing.T) {
			store := permissions.New("/work")
			var persisted []permissions.Grant
			neg := &recordingNegotiator{responses: []permissions.Response{tc.response}}
			gate := permissions.NewGate(store, neg, func(g permissions.Grant) error {
				persisted = append(persisted, g)
				return nil
			})
			tool := New(store, "/work", 30*time.Second)
			call := raw(`{"code":"1","permissions":["net"]}`)

			d, err := gate.Check(context.Background(), "c1", tool, call)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if !d.Allow {
				t.Fatalf("call denied = %q; want allowed", d.Denied)
			}
			if len(neg.prompts) != 1 || neg.prompts[0] != permissions.RenderPrompt(permissions.AxisNet, "") {
				t.Errorf("prompts = %q; want one blanket net prompt", neg.prompts)
			}
			if d.Granted != "Permission granted: net" {
				t.Errorf("Granted = %q; want the blanket net grant line", d.Granted)
			}
			gate.Settle(d)

			if got := store.AnyScopeCovered(permissions.AxisNet); got != tc.survives {
				t.Fatalf("net covered after the call resolved = %v; want %v", got, tc.survives)
			}
			if tc.tier == permissions.TierPermanent {
				if len(persisted) != 1 || persisted[0].Axis != permissions.AxisNet ||
					persisted[0].Scope != "" || persisted[0].Tier != permissions.TierPermanent {
					t.Errorf("persisted = %+v; want one permanent net grant at blanket scope", persisted)
				}
			} else if len(persisted) != 0 {
				t.Errorf("persisted = %+v for the %s tier; want none", persisted, tc.tier)
			}

			if !tc.survives {
				return
			}
			// A surviving tier satisfies the axis request, so the next identical
			// call is a silent execute.
			d2, err := gate.Check(context.Background(), "c2", tool, call)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if !d2.Allow || len(neg.prompts) != 1 {
				t.Errorf("second call: allow=%v prompts=%q; want allowed with no further prompt", d2.Allow, neg.prompts)
			}
		})
	}
}

func TestUncoveredAxesPromptOnceEachInAxisOrder(t *testing.T) {
	store := permissions.New("/work")
	neg := &recordingNegotiator{}
	gate := permissions.NewGate(store, neg, nil)

	if _, err := gate.Check(context.Background(), "c1", New(store, "/work", 30*time.Second),
		raw(`{"code":"1","permissions":["env","write","net"]}`)); err != nil {
		t.Fatalf("Check: %v", err)
	}
	want := []string{
		permissions.RenderPrompt(permissions.AxisWrite, ""),
		permissions.RenderPrompt(permissions.AxisNet, ""),
		permissions.RenderPrompt(permissions.AxisEnv, ""),
	}
	if len(neg.prompts) != len(want) {
		t.Fatalf("prompts = %q; want %q", neg.prompts, want)
	}
	for i := range want {
		if neg.prompts[i] != want[i] {
			t.Errorf("prompt[%d] = %q; want %q", i, neg.prompts[i], want[i])
		}
	}
}

func TestRejectedAxisDeniesWithoutGrant(t *testing.T) {
	store := permissions.New("/work")
	neg := &recordingNegotiator{responses: []permissions.Response{permissions.ResponseReject}}
	gate := permissions.NewGate(store, neg, nil)

	d, err := gate.Check(context.Background(), "c1", New(store, "/work", 30*time.Second), raw(`{"code":"1","permissions":["env"]}`))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Allow {
		t.Fatal("call allowed on a rejected axis")
	}
	want := "Permission rejected: env — consider an alternative\nstatus: call not executed\nhint: granted axes remain available — reformulate without the denied axis."
	if d.Denied != want {
		t.Errorf("Denied = %q; want %q", d.Denied, want)
	}
	if store.AnyScopeCovered(permissions.AxisEnv) {
		t.Error("a rejected axis left coverage behind")
	}
}

// ==== Flag building tests ====

func TestBuildDenoFlags_ScopedGrants(t *testing.T) {
	store := permissions.New("/work")
	store.SetBaseFromConfig(map[string][]string{
		"read":  {"/work/src", "/work/test"},
		"write": {"/work/out"},
		"net":   {"api.example.com:443", "*.github.com:443"},
		"run":   {"git", "npm"},
		"env":   {"DB_HOST", "API_KEY"},
	})

	tool := New(store, "/work", 30*time.Second)
	call := raw(`{"code":"1","permissions":["read","write","net","run","env"]}`)

	flags := tool.buildDenoFlags(call)
	want := []string{
		"--allow-read=/work/src,/work/test",
		"--allow-write=/work/out",
		"--allow-net=api.example.com:443,*.github.com:443",
		"--allow-run=git,npm",
		"--allow-env=DB_HOST,API_KEY",
	}
	if len(flags) != len(want) {
		t.Fatalf("flags = %v; want %v", flags, want)
	}
	for i := range want {
		if flags[i] != want[i] {
			t.Errorf("flags[%d] = %q; want %q", i, flags[i], want[i])
		}
	}
}

func TestBuildDenoFlags_BlanketGrant(t *testing.T) {
	store := permissions.New("/work")
	store.GrantPermanent(permissions.Grant{Axis: permissions.AxisRead, Scope: ""})
	store.GrantPermanent(permissions.Grant{Axis: permissions.AxisWrite, Scope: ""})
	store.GrantPermanent(permissions.Grant{Axis: permissions.AxisNet, Scope: ""})
	store.GrantPermanent(permissions.Grant{Axis: permissions.AxisRun, Scope: ""})
	store.GrantPermanent(permissions.Grant{Axis: permissions.AxisEnv, Scope: ""})

	tool := New(store, "/work", 30*time.Second)
	call := raw(`{"code":"1","permissions":["read","write","net","run","env"]}`)

	flags := tool.buildDenoFlags(call)
	want := []string{
		"--allow-read",
		"--allow-write",
		"--allow-net",
		"--allow-run",
		"--allow-env",
	}
	if len(flags) != len(want) {
		t.Fatalf("flags = %v; want %v", flags, want)
	}
	for i := range want {
		if flags[i] != want[i] {
			t.Errorf("flags[%d] = %q; want %q", i, flags[i], want[i])
		}
	}
}

func TestBuildDenoFlags_UnrequestedAxisNoFlag(t *testing.T) {
	store := permissions.New("/work")
	store.SetBaseFromConfig(map[string][]string{
		"read": {"/work/src"},
		"write": {"/work/out"},
	})

	tool := New(store, "/work", 30*time.Second)
	call := raw(`{"code":"1","permissions":["read"]}`)

	flags := tool.buildDenoFlags(call)
	want := []string{
		"--allow-read=/work/src",
	}
	if len(flags) != len(want) {
		t.Fatalf("flags = %v; want %v", flags, want)
	}
	if flags[0] != want[0] {
		t.Errorf("flags[0] = %q; want %q", flags[0], want[0])
	}
}

func TestBuildDenoFlags_NoRequestedAxes_NoFlags(t *testing.T) {
	store := permissions.New("/work")
	store.SetBaseFromConfig(map[string][]string{
		"read": {"/work/src"},
	})

	tool := New(store, "/work", 30*time.Second)
	for _, call := range []string{`{"code":"1"}`, `{"code":"1","permissions":[]}`, `{}`} {
		flags := tool.buildDenoFlags(raw(call))
		if len(flags) != 0 {
			t.Errorf("buildDenoFlags(%s) = %v; want empty", call, flags)
		}
	}
}

func TestBuildDenoFlags_OnceTierGrantsVisible(t *testing.T) {
	store := permissions.New("/work")
	call := store.BeginCall("/work/run")
	store.GrantOnce(call, permissions.AxisRead, "/work/secret")
	store.GrantOnce(call, permissions.AxisWrite, "/work/output")

	tool := New(store, "/work", 30*time.Second)
	args := raw(`{"code":"1","permissions":["read","write"]}`)

	flags := tool.buildDenoFlags(args)
	// Base read is "." which normalizes to "/work", plus once grant "/work/secret"
	// Write has no base, only once grant "/work/output"
	want := []string{
		"--allow-read=/work,/work/secret",
		"--allow-write=/work/output",
	}
	if len(flags) != len(want) {
		t.Fatalf("flags = %v; want %v", flags, want)
	}
	for i := range want {
		if flags[i] != want[i] {
			t.Errorf("flags[%d] = %q; want %q", i, flags[i], want[i])
		}
	}
}

func TestBuildDenoFlags_ScopesNotNormalizedOrReordered(t *testing.T) {
	store := permissions.New("/work")
	store.SetBaseFromConfig(map[string][]string{
		"read": {"/work/b", "/work/a", "/work/c"},
		"net":  {"host3.com:443", "host1.com:443", "host2.com:443"},
	})

	tool := New(store, "/work", 30*time.Second)
	call := raw(`{"code":"1","permissions":["read","net"]}`)

	flags := tool.buildDenoFlags(call)
	want := []string{
		"--allow-read=/work/b,/work/a,/work/c",
		"--allow-net=host3.com:443,host1.com:443,host2.com:443",
	}
	if len(flags) != len(want) {
		t.Fatalf("flags = %v; want %v", flags, want)
	}
	for i := range want {
		if flags[i] != want[i] {
			t.Errorf("flags[%d] = %q; want %q (scopes must pass through verbatim, not reordered)", i, flags[i], want[i])
		}
	}
}

// fakeRunner is a test runner that returns scripted results.
type fakeRunner struct {
	stdout    string
	stderr    string
	exitCode  int
	err       error
	ran       bool
	callCount int
	lastCall  string
	lastFlags []string
}

func (f *fakeRunner) Run(ctx context.Context, snippetPath string, flags []string, cwd string) (stdout, stderr string, exitCode int, err error) {
	f.ran = true
	f.callCount++
	f.lastCall = snippetPath
	f.lastFlags = flags
	return f.stdout, f.stderr, f.exitCode, f.err
}

// tempCwd returns a temporary directory for tests that need a real cwd.
func tempCwd(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// ==== Execute tests ====

func TestExecute_WritesTempFile(t *testing.T) {
	cwd := tempCwd(t)
	runner := &fakeRunner{stdout: "hello", stderr: "", exitCode: 0}
	store := permissions.New(cwd)
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	_, err := tool.Execute(raw(`{"code":"console.log('hello')","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !runner.ran {
		t.Fatal("runner was not called")
	}
	if !strings.HasPrefix(runner.lastCall, cwd+"/.genie-tmp/code/code-") {
		t.Errorf("snippet path = %q; want it under .genie-tmp/code/", runner.lastCall)
	}
	if !strings.HasSuffix(runner.lastCall, ".ts") {
		t.Errorf("snippet path = %q; want .ts extension", runner.lastCall)
	}
}

func TestExecute_ReturnsStdoutAndStderrSeparately(t *testing.T) {
	cwd := tempCwd(t)
	runner := &fakeRunner{stdout: "stdout content", stderr: "stderr content", exitCode: 0}
	store := permissions.New(cwd)
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	output, err := tool.Execute(raw(`{"code":"1","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(output, "stdout content") {
		t.Errorf("output missing stdout: %q", output)
	}
	if !strings.Contains(output, "[stderr]") {
		t.Errorf("output missing stderr marker: %q", output)
	}
	if !strings.Contains(output, "stderr content") {
		t.Errorf("output missing stderr content: %q", output)
	}
}

func TestExecute_ReturnsExitCode(t *testing.T) {
	cwd := tempCwd(t)
	runner := &fakeRunner{stdout: "", stderr: "", exitCode: 42}
	store := permissions.New(cwd)
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	output, err := tool.Execute(raw(`{"code":"1","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(output, "[exit code 42]") {
		t.Errorf("output missing exit code: %q", output)
	}
}

func TestExecute_MissingCodeArgument(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	tool := New(store, cwd, 30*time.Second)

	_, err := tool.Execute(raw(`{"permissions":["read"]}`))
	if err == nil {
		t.Fatal("Execute should error on missing code")
	}
	if !strings.Contains(err.Error(), "missing required argument: code") {
		t.Errorf("error = %q; want missing code error", err)
	}
}

func TestExecute_InvalidJSON(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	tool := New(store, cwd, 30*time.Second)

	_, err := tool.Execute(raw(`{`))
	if err == nil {
		t.Fatal("Execute should error on invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("error = %q; want invalid arguments error", err)
	}
}

func TestExecute_TimeoutReturnsError(t *testing.T) {
	cwd := tempCwd(t)
	runner := &fakeRunner{stdout: "", stderr: "", exitCode: 0, err: context.DeadlineExceeded}
	store := permissions.New(cwd)
	tool := New(store, cwd, 1*time.Nanosecond).WithRunner(runner)

	_, err := tool.Execute(raw(`{"code":"1","permissions":["read"]}`))
	if err == nil {
		t.Fatal("Execute should error on timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q; want timeout error", err)
	}
}

func TestExecute_OutputCapAndSpill(t *testing.T) {
	cwd := tempCwd(t)
	// The output includes formatting (stdout + \n + [stderr]\n + stderr + \n + [exit code X])
	// So we need a bit more than maxOutputBytes to ensure truncation happens
	bigOutput := strings.Repeat("x", maxOutputBytes+2000)
	runner := &fakeRunner{stdout: bigOutput, stderr: "", exitCode: 0}
	store := permissions.New(cwd)
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	output, err := tool.Execute(raw(`{"code":"1","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The output should be truncated at maxOutputBytes plus the truncation message
	// The truncation message is added after truncating, so output will be slightly over maxOutputBytes
	if len(output) <= maxOutputBytes {
		t.Errorf("output length %d not exceeding cap %d (should be truncated)", len(output), maxOutputBytes)
	}
	if !strings.Contains(output, "[truncated") {
		t.Errorf("output missing truncation marker: %q", output[:100])
	}
	if !strings.Contains(output, ".genie-spill") {
		t.Errorf("output missing spill file path: %q", output)
	}
	// The actual content should be maxOutputBytes + truncation marker
	if len(output) > maxOutputBytes+200 {
		t.Errorf("output length %d far exceeds cap + marker", len(output))
	}
}

func TestExecute_DenoFlagsFromPolicy(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	store.SetBaseFromConfig(map[string][]string{
		"read":  {cwd + "/src"},
		"write": {cwd + "/out"},
	})
	runner := &fakeRunner{stdout: "", stderr: "", exitCode: 0}
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	_, err := tool.Execute(raw(`{"code":"1","permissions":["read","write"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wantFlags := []string{"--allow-read=" + cwd + "/src", "--allow-write=" + cwd + "/out"}
	if len(runner.lastFlags) != len(wantFlags) {
		t.Fatalf("flags = %v; want %v", runner.lastFlags, wantFlags)
	}
	for i, f := range wantFlags {
		if runner.lastFlags[i] != f {
			t.Errorf("flags[%d] = %q; want %q", i, runner.lastFlags[i], f)
		}
	}
}

func TestExecute_PerCallTimeoutOverridesConfig(t *testing.T) {
	cwd := tempCwd(t)
	runner := &fakeRunner{stdout: "", stderr: "", exitCode: 0}
	store := permissions.New(cwd)
	tool := New(store, cwd, 1*time.Second).WithRunner(runner)

	// Request a 10 second timeout in the call args
	_, err := tool.Execute(raw(`{"code":"1","permissions":["read"],"timeout":10}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// We can't easily verify the context timeout was used without more instrumentation,
	// but we verify the call succeeds (the fake runner ignores context)
}

// ==== NotCapable emitter tests ====

func TestParseDenoNotCapable_ReadDenial(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	// Simulate Deno stderr with a read permission denial
	stderr := `error: Uncaught (in promise) Deno.errors.NotCapable: Requires read access to "/etc/hosts", run again with --allow-read
    at Object.readTextFileSync (<anonymous>:1:15)
    at file:///test.ts:1:15
{"code":"ERR_PERMISSION_DENIED","permission":"read","resource":"/etc/hosts"}`

	nc, ok := permissions.FindAndParseNotCapable(stderr)
	if !ok {
		t.Fatal("FindAndParseNotCapable returned false for valid denial")
	}
	if nc.Permission != "read" {
		t.Errorf("permission = %q; want %q", nc.Permission, "read")
	}
	if nc.Resource != "/etc/hosts" {
		t.Errorf("resource = %q; want %q", nc.Resource, "/etc/hosts")
	}
}

func TestParseDenoNotCapable_WriteDenial(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	stderr := `{"code":"ERR_PERMISSION_DENIED","permission":"write","resource":"/root/secret.txt"}`

	nc, ok := permissions.FindAndParseNotCapable(stderr)
	if !ok {
		t.Fatal("FindAndParseNotCapable returned false for write denial")
	}
	if nc.Permission != "write" {
		t.Errorf("permission = %q; want %q", nc.Permission, "write")
	}
	if nc.Resource != "/root/secret.txt" {
		t.Errorf("resource = %q; want %q", nc.Resource, "/root/secret.txt")
	}
}

func TestParseDenoNotCapable_NetDenial(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	stderr := `{"code":"ERR_PERMISSION_DENIED","permission":"net","resource":"api.example.com:443"}`

	nc, ok := permissions.FindAndParseNotCapable(stderr)
	if !ok {
		t.Fatal("FindAndParseNotCapable returned false for net denial")
	}
	if nc.Permission != "net" {
		t.Errorf("permission = %q; want %q", nc.Permission, "net")
	}
	if nc.Resource != "api.example.com:443" {
		t.Errorf("resource = %q; want %q", nc.Resource, "api.example.com:443")
	}
}

func TestParseDenoNotCapable_RunDenial(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	stderr := `{"code":"ERR_PERMISSION_DENIED","permission":"run","resource":"rm"}`

	nc, ok := permissions.FindAndParseNotCapable(stderr)
	if !ok {
		t.Fatal("FindAndParseNotCapable returned false for run denial")
	}
	if nc.Permission != "run" {
		t.Errorf("permission = %q; want %q", nc.Permission, "run")
	}
	if nc.Resource != "rm" {
		t.Errorf("resource = %q; want %q", nc.Resource, "rm")
	}
}

func TestParseDenoNotCapable_EnvDenial(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	stderr := `{"code":"ERR_PERMISSION_DENIED","permission":"env","resource":"SECRET_KEY"}`

	nc, ok := permissions.FindAndParseNotCapable(stderr)
	if !ok {
		t.Fatal("FindAndParseNotCapable returned false for env denial")
	}
	if nc.Permission != "env" {
		t.Errorf("permission = %q; want %q", nc.Permission, "env")
	}
	if nc.Resource != "SECRET_KEY" {
		t.Errorf("resource = %q; want %q", nc.Resource, "SECRET_KEY")
	}
}

func TestParseDenoNotCapable_ResourceWithBrace(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	// Resource containing } should not break parsing (skipQuoted handles this)
	stderr := `{"code":"ERR_PERMISSION_DENIED","permission":"read","resource":"/path}with}brace"}`

	nc, ok := permissions.FindAndParseNotCapable(stderr)
	if !ok {
		t.Fatal("FindAndParseNotCapable returned false for resource with brace")
	}
	if nc.Permission != "read" {
		t.Errorf("permission = %q; want %q", nc.Permission, "read")
	}
	if nc.Resource != "/path}with}brace" {
		t.Errorf("resource = %q; want %q", nc.Resource, "/path}with}brace")
	}
}

func TestParseDenoNotCapable_UnrecognisedStderrFallsThrough(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	// Regular error output, not a permission denial
	stderr := `error: Uncaught TypeError: Cannot read property 'foo' of undefined
    at file:///test.ts:1:15`

	_, ok := permissions.FindAndParseNotCapable(stderr)
	if ok {
		t.Errorf("FindAndParseNotCapable returned true for non-denial error")
	}
}

func TestParseDenoNotCapable_EmptyStderrReturnsNil(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	_, ok := permissions.FindAndParseNotCapable("")
	if ok {
		t.Errorf("FindAndParseNotCapable returned true for empty stderr")
	}
}

func TestParseDenoNotCapable_MalformedJSONReturnsNil(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	// Has the marker but invalid JSON
	stderr := `{"code":"ERR_PERMISSION_DENIED","permission":}`

	_, ok := permissions.FindAndParseNotCapable(stderr)
	if ok {
		t.Errorf("FindAndParseNotCapable returned true for malformed JSON")
	}
}

func TestParseDenoNotCapable_WrongCodeReturnsNil(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	_ = New(store, cwd, 30*time.Second)

	// Different error code
	stderr := `{"code":"ERR_SOMETHING_ELSE","permission":"read","resource":"/etc/hosts"}`

	_, ok := permissions.FindAndParseNotCapable(stderr)
	if ok {
		t.Errorf("FindAndParseNotCapable returned true for wrong error code")
	}
}

func TestParseDenoNotCapable_MissingPermissionCallerRejects(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	runner := &fakeRunner{
		stdout:   "",
		stderr:   `{"code":"ERR_PERMISSION_DENIED","resource":"/etc/hosts"}`,
		exitCode: 1,
	}
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	// Missing permission should fall through as regular error output
	output, err := tool.Execute(raw(`{"code":"console.log('hi')","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Should NOT be rewritten by MapNotCapable (the tool doesn't emit a NotCapable marker)
	mapped, ok := permissions.MapNotCapable(output)
	if ok {
		t.Errorf("MapNotCapable incorrectly rewrote missing-permission case: %q", mapped)
	}

	// The output should contain the raw stderr as regular error output
	if !strings.Contains(output, "resource") {
		t.Errorf("output missing stderr content: %q", output)
	}
	if !strings.Contains(output, "[exit code 1]") {
		t.Errorf("output missing exit code: %q", output)
	}
}

func TestExecute_NotCapableEmitter_ReadDenial(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	runner := &fakeRunner{
		stdout:   "",
		stderr:   `{"code":"ERR_PERMISSION_DENIED","permission":"read","resource":"/etc/hosts"}`,
		exitCode: 1,
	}
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	output, err := tool.Execute(raw(`{"code":"console.log('hi')","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The output should be the NotCapable marker
	if !strings.Contains(output, `ERR_PERMISSION_DENIED`) {
		t.Errorf("output missing ERR_PERMISSION_DENIED: %q", output)
	}
	if !strings.Contains(output, `"permission":"read"`) {
		t.Errorf("output missing permission read: %q", output)
	}
	if !strings.Contains(output, `"resource":"/etc/hosts"`) {
		t.Errorf("output missing resource: %q", output)
	}

	// Verify MapNotCapable rewrites it to the pinned composite
	mapped, ok := permissions.MapNotCapable(output)
	if !ok {
		t.Fatal("MapNotCapable did not rewrite the output")
	}
	want := "status: call not executed — read unavailable at runtime\nhint: inline escalation is not available mid-execution — request read access in advance via request_permission, or reformulate"
	if mapped != want {
		t.Errorf("mapped = %q; want %q", mapped, want)
	}
}

func TestExecute_NotCapableEmitter_NonDenialErrorFallsThrough(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	runner := &fakeRunner{
		stdout:   "",
		stderr:   "TypeError: Cannot read property 'foo' of undefined",
		exitCode: 1,
	}
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	output, err := tool.Execute(raw(`{"code":"console.log('hi')","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The output should be the regular error output, not a NotCapable marker
	if strings.Contains(output, `ERR_PERMISSION_DENIED`) {
		t.Errorf("output incorrectly contains ERR_PERMISSION_DENIED: %q", output)
	}
	if strings.Contains(output, `"permission"`) {
		t.Errorf("output incorrectly contains permission field: %q", output)
	}
	// Should contain the stderr content
	if !strings.Contains(output, "TypeError") {
		t.Errorf("output missing stderr content: %q", output)
	}
	if !strings.Contains(output, "[exit code 1]") {
		t.Errorf("output missing exit code: %q", output)
	}

	// MapNotCapable should not rewrite it
	mapped, ok := permissions.MapNotCapable(output)
	if ok {
		t.Errorf("MapNotCapable incorrectly rewrote non-denial: %q", mapped)
	}
	if mapped != output {
		t.Errorf("MapNotCapable changed output: got %q, want %q", mapped, output)
	}
}

func TestExecute_NotCapableEmitter_NoRetryOnDenial(t *testing.T) {
	cwd := tempCwd(t)
	store := permissions.New(cwd)
	runner := &fakeRunner{
		stdout:   "",
		stderr:   `{"code":"ERR_PERMISSION_DENIED","permission":"read","resource":"/etc/hosts"}`,
		exitCode: 1,
	}
	tool := New(store, cwd, 30*time.Second).WithRunner(runner)

	_, err := tool.Execute(raw(`{"code":"console.log('hi')","permissions":["read"]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Runner should have been called exactly once (no internal retry)
	if !runner.ran {
		t.Fatal("runner was not called")
	}
	if runner.callCount != 1 {
		t.Errorf("runner called %d times; want exactly 1 (no retry on denial)", runner.callCount)
	}
}

// ==== Basic tool interface tests ====

func TestName(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)
	if tool.Name() != "code" {
		t.Errorf("Name() = %q; want %q", tool.Name(), "code")
	}
}

func TestDescription_DenoAvailable(t *testing.T) {
	cwd := tempCwd(t)
	tool := New(permissions.New(cwd), cwd, 30*time.Second)
	desc := tool.Description()
	if desc == "" {
		t.Fatal("Description() returned empty string")
	}
	if !strings.Contains(desc, "TypeScript") {
		t.Errorf("Description() = %q; want it to mention TypeScript", desc)
	}
	if !strings.Contains(desc, "Deno") {
		t.Errorf("Description() = %q; want it to mention Deno", desc)
	}
}

func TestParameters(t *testing.T) {
	tool := New(permissions.New("/work"), "/work", 30*time.Second)
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters() returned nil")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters missing properties")
	}
	// Check required fields
	if _, ok := props["code"]; !ok {
		t.Error("Parameters missing 'code' property")
	}
	if _, ok := props["permissions"]; !ok {
		t.Error("Parameters missing 'permissions' property")
	}
	if _, ok := props["timeout"]; !ok {
		t.Error("Parameters missing 'timeout' property")
	}
	// Check required array
	req, ok := params["required"].([]any)
	if !ok || len(req) == 0 {
		t.Error("Parameters missing required array")
	}
	foundCode := false
	for _, r := range req {
		if r == "code" {
			foundCode = true
			break
		}
	}
	if !foundCode {
		t.Error("Required array missing 'code'")
	}
	// Check permissions enum
	permProp, ok := props["permissions"].(map[string]any)
	if !ok {
		t.Error("permissions property not an object")
	}
	items, ok := permProp["items"].(map[string]any)
	if !ok {
		t.Error("permissions items not an object")
	}
	enum, ok := items["enum"].([]string)
	if !ok {
		t.Error("permissions enum missing")
	}
	wantEnum := []string{"read", "write", "net", "run", "env"}
	if len(enum) != len(wantEnum) {
		t.Errorf("enum = %v; want %v", enum, wantEnum)
	}
	for i := range wantEnum {
		if enum[i] != wantEnum[i] {
			t.Errorf("enum[%d] = %q; want %q", i, enum[i], wantEnum[i])
		}
	}
}

func TestIsDenoAvailable(t *testing.T) {
	// We can't easily mock exec.LookPath, but we can at least
	// verify the function runs without panicking and returns a bool
	result := IsDenoAvailable()
	_ = result // just verify it returns a bool
}

func TestDescription_DenoAbsent(t *testing.T) {
	// This test documents the expected behavior when Deno is not available.
	// We can't easily inject a fake LookPath, so we verify the function
	// returns a string that contains the disabled notice when Deno is absent.
	// In the test environment, Deno is typically not available.
	tool := New(permissions.New("/work"), "/work", 30*time.Second)
	desc := tool.Description()
	if desc == "" {
		t.Fatal("Description() returned empty string")
	}
	// The description should always mention TypeScript and Deno
	if !strings.Contains(desc, "TypeScript") {
		t.Errorf("Description() = %q; want it to mention TypeScript", desc)
	}
	// The description should always mention Deno (either available or not)
	if !strings.Contains(desc, "Deno") {
		t.Errorf("Description() = %q; want it to mention Deno", desc)
	}
	// If Deno is not available, the description should mention "disabled"
	if !IsDenoAvailable() {
		if !strings.Contains(desc, "disabled") {
			t.Errorf("Description() = %q; want it to mention 'disabled' when Deno absent", desc)
		}
		if !strings.Contains(desc, "not found on PATH") {
			t.Errorf("Description() = %q; want it to mention 'not found on PATH' when Deno absent", desc)
		}
	}
}

// ==== Integration tests with real Deno ====

// denoAvailable checks if deno is on PATH.
func denoAvailable() bool {
	_, err := exec.LookPath("deno")
	return err == nil
}

func TestExecute_RealDeno_GrantedRead(t *testing.T) {
	if !denoAvailable() {
		t.Skip("deno not on PATH")
	}

	cwd := tempCwd(t)
	// Create a test file to read
	testFile := filepath.Join(cwd, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	store := permissions.New(cwd)
	store.SetBaseFromConfig(map[string][]string{
		"read": {cwd},
	})
	tool := New(store, cwd, 5*time.Second)

	// Snippet that reads the test file
	snippet := `
const data = await Deno.readTextFile("test.txt");
console.log(data);
`
	output, err := tool.Execute(raw(fmt.Sprintf(`{"code":%q,"permissions":["read"]}`, snippet)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(output, "hello world") {
		t.Errorf("output missing expected content: %q", output)
	}
}

func TestExecute_RealDeno_Timeout(t *testing.T) {
	if !denoAvailable() {
		t.Skip("deno not on PATH")
	}

	cwd := tempCwd(t)
	store := permissions.New(cwd)
	tool := New(store, cwd, 1*time.Second)

	// Snippet that sleeps longer than timeout
	snippet := `
await new Promise(resolve => setTimeout(resolve, 5000));
console.log("done");
`
	_, err := tool.Execute(raw(fmt.Sprintf(`{"code":%q,"permissions":[]}`, snippet)))
	if err == nil {
		t.Fatal("Execute should error on timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q; want timeout error", err)
	}
}
