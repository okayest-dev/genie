package permissions

import (
	"os"
	"path/filepath"
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
