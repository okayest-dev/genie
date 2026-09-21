package permissions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// golden reads a committed snapshot from the testdata dir, failing when it is
// absent so a renderer change never lands without its golden beside it.
func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("golden %s: %v", name, err)
	}
	return string(b)
}

func TestRenderPromptTerseFrameWriteAxisAndScope(t *testing.T) {
	want := golden(t, "prompt_write.go.golden")
	if got := RenderPrompt(AxisWrite, "/work/x.go"); got != want {
		t.Errorf("prompt frame mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderPromptBlanketOmitsScope(t *testing.T) {
	want := golden(t, "prompt_blanket.golden")
	if got := RenderPrompt(AxisNet, ""); got != want {
		t.Errorf("blanket prompt mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderBlanketScopeExpressionTerse(t *testing.T) {
	want := golden(t, "blanket_scope.golden")
	if got := RenderBlanketScope(AxisWrite); got != want {
		t.Errorf("blanket scope mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderAxesLegendListsAxesAndKeys(t *testing.T) {
	want := golden(t, "axes_legend.golden")
	if got := AxesLegend(); got != want {
		t.Errorf("axes legend mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderUnknownHintReportsChoices(t *testing.T) {
	want := golden(t, "hint_unknown.golden")
	if got := RenderUnknownHint(); got != want {
		t.Errorf("unknown hint mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderGrantLine(t *testing.T) {
	want := golden(t, "grant_line.golden")
	if got := RenderGrant(AxisWrite, "/work/x.go"); got != want {
		t.Errorf("grant line mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderGrantLineBlanketOmitsScope(t *testing.T) {
	want := golden(t, "grant_line_blanket.golden")
	if got := RenderGrant(AxisNet, ""); got != want {
		t.Errorf("blanket grant line mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderRejectLine(t *testing.T) {
	want := golden(t, "reject_line.golden")
	if got := RenderReject(AxisWrite, "/work/x.go"); got != want {
		t.Errorf("reject line mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderGrantedCompositeNoStatusLine(t *testing.T) {
	want := golden(t, "composite_granted.golden")
	grants := []Grant{{Axis: AxisWrite, Scope: "/work/x.go", Tier: TierSession}}
	if got := RenderGrantedComposite(grants, "created /work/x.go"); got != want {
		t.Errorf("granted composite mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderGrantedCompositeNoGrantsIsBareOutput(t *testing.T) {
	if got := RenderGrantedComposite(nil, "created /work/x.go"); got != "created /work/x.go" {
		t.Errorf("composite with no grants = %q, want bare output", got)
	}
}

func TestRenderDeniedCompositeReportsStatusAndHint(t *testing.T) {
	want := golden(t, "composite_denied.golden")
	rejects := []Grant{{Axis: AxisWrite, Scope: "/work/x.go", Tier: TierOnce}}
	if got := RenderDeniedComposite(nil, rejects); got != want {
		t.Errorf("denied composite mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderDeniedCompositeIncludesGrants(t *testing.T) {
	want := golden(t, "composite_denied_with_grant.golden")
	grants := []Grant{{Axis: AxisNet, Scope: "api.github.com:443", Tier: TierSession}}
	rejects := []Grant{{Axis: AxisRun, Scope: "python3", Tier: TierOnce}}
	if got := RenderDeniedComposite(grants, rejects); got != want {
		t.Errorf("denied composite mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderBaseSnapshotFlatPerAxis(t *testing.T) {
	base := map[Axis][]string{
		AxisRead: {".", "/etc"},
		AxisNet:  {"api.openai.com:443"},
	}
	got := RenderBaseSnapshot(base)
	want := "" +
		"Current permissions:\n" +
		"- read: ., /etc\n" +
		"- write: nothing is authorized\n" +
		"- net: api.openai.com:443\n" +
		"- run: nothing is authorized\n" +
		"- env: nothing is authorized"
	if got != want {
		t.Errorf("snapshot mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderBaseSnapshotConstantAxisOrder(t *testing.T) {
	base := map[Axis][]string{
		AxisEnv:   {"HOME"},
		AxisRun:   {"python3"},
		AxisNet:   {"api.openai.com:443"},
		AxisWrite: {"/work"},
		AxisRead:  {"."},
	}
	got := RenderBaseSnapshot(base)
	read := strings.Index(got, "- read:")
	write := strings.Index(got, "- write:")
	net := strings.Index(got, "- net:")
	run := strings.Index(got, "- run:")
	env := strings.Index(got, "- env:")
	if !(read < write && write < net && net < run && run < env) {
		t.Errorf("axes not in fixed order read→write→net→run→env:\n%s", got)
	}
}

func TestRenderPermissionsSectionMatchesGolden(t *testing.T) {
	want := golden(t, "permissions_section.golden")
	base := map[Axis][]string{
		AxisRead: {".", "/etc"},
		AxisNet:  {"api.openai.com:443"},
	}
	if got := RenderPermissionsSection(base); got != want {
		t.Errorf("section mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestMechanismTextZeroTierVocabulary(t *testing.T) {
	lower := strings.ToLower(MechanismText)
	for _, banned := range []string{"once", "session", "permanent", "tier", "base"} {
		if strings.Contains(lower, banned) {
			t.Errorf("mechanism text mentions forbidden tier vocabulary %q: %q", banned, MechanismText)
		}
	}
}

func TestMechanismTextStatesNegotiationMechanism(t *testing.T) {
	need := []string{
		"Permission granted:", "Permission rejected:",
		"prefix", "subdomain wildcard", "exactly",
		"request_permission",
		"materially different alternative",
	}
	for _, frag := range need {
		if !strings.Contains(MechanismText, frag) {
			t.Errorf("mechanism text missing %q:\n%s", frag, MechanismText)
		}
	}
}
