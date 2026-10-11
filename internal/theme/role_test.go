package theme

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/okayest-dev/genie/internal/style"
)

// TestClassicRoleDefaults pins the preset's non-prompt role ladder (ADR-0009):
// content plain, tool cyan, problems red, cancellation dim.
func TestClassicRoleDefaults(t *testing.T) {
	th, err := Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for role, want := range map[string]style.Style{
		"model":          {},
		"tool":           {Foreground: "cyan"},
		"error":          {Foreground: "red"},
		"turn_cancelled": {Attributes: []string{"dim"}},
	} {
		got := th.RoleStyle(role)
		if got.Foreground != want.Foreground {
			t.Errorf("%s: foreground = %q, want %q", role, got.Foreground, want.Foreground)
		}
		if len(got.Attributes) != len(want.Attributes) || (len(want.Attributes) > 0 && got.Attributes[0] != want.Attributes[0]) {
			t.Errorf("%s: attributes = %v, want %v", role, got.Attributes, want.Attributes)
		}
	}
}

func TestLeanRoleDefaultsMatchClassic(t *testing.T) {
	classic, err := Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("Load classic: %v", err)
	}
	lean, err := Load("lean", "/tmp")
	if err != nil {
		t.Fatalf("Load lean: %v", err)
	}
	for _, role := range []string{"model", "tool", "error", "turn_cancelled"} {
		cGot, lGot := classic.RoleStyle(role), lean.RoleStyle(role)
		if !reflect.DeepEqual(cGot, lGot) {
			t.Errorf("%s: classic %+v != lean %+v", role, cGot, lGot)
		}
	}
}

func TestRoleStyleUnmappedRoleIsZero(t *testing.T) {
	th, err := Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := th.RoleStyle("nope"); !reflect.DeepEqual(got, style.Style{}) {
		t.Errorf("unmapped role style = %+v, want zero", got)
	}
}

func TestRoleStyleNilThemeIsZero(t *testing.T) {
	var th *Theme
	if got := th.RoleStyle("error"); !reflect.DeepEqual(got, style.Style{}) {
		t.Errorf("nil theme style = %+v, want zero", got)
	}
}

func TestRoleStyleGlyphTiersMapped(t *testing.T) {
	s := roleStyleToStyle(RoleStyle{
		Glyphs: map[string]string{"nerd": "❯", "powerline": "❯", "ascii": ">"},
	})
	if s.Glyphs[style.GlyphTierNerd] != "❯" {
		t.Errorf("nerd glyph = %q, want ❯", s.Glyphs[style.GlyphTierNerd])
	}
	if s.Glyphs[style.GlyphTierPowerline] != "❯" {
		t.Errorf("powerline glyph = %q, want ❯", s.Glyphs[style.GlyphTierPowerline])
	}
	if s.Glyphs[style.GlyphTierAscii] != ">" {
		t.Errorf("ascii glyph = %q, want >", s.Glyphs[style.GlyphTierAscii])
	}
}

func TestRoleWriterRendersThroughProfile(t *testing.T) {
	th, err := Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	profile := &style.RenderProfile{IsTTY: true, ColourProfile: style.ColourProfileTrueColor, GlyphTier: style.GlyphTierNerd, Width: 80}
	rw := th.RoleWriter(&buf, profile, "tool")
	if _, err := rw.Write([]byte("── read ──")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := buf.String(), "\x1b[36m── read ──\x1b[0m"; got != want {
		t.Errorf("tool role output = %q, want %q", got, want)
	}
}

func TestRoleWriterUnmappedRolePlain(t *testing.T) {
	th, err := Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	profile := &style.RenderProfile{IsTTY: true, ColourProfile: style.ColourProfileTrueColor, GlyphTier: style.GlyphTierNerd, Width: 80}
	rw := th.RoleWriter(&buf, profile, "nope")
	if _, err := rw.Write([]byte("raw")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "raw" {
		t.Errorf("unmapped role output = %q, want plain", got)
	}
}

func TestRoleWriterAsciiProfileDropsColour(t *testing.T) {
	th, err := Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	profile := &style.RenderProfile{IsTTY: true, ColourProfile: style.ColourProfileAscii, GlyphTier: style.GlyphTierAscii, Width: 80}
	rw := th.RoleWriter(&buf, profile, "tool")
	if _, err := rw.Write([]byte("── read ──")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "── read ──" {
		t.Errorf("ascii tool output = %q, want byte-identical plain", got)
	}
}