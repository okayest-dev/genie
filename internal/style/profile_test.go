package style

import (
	"os"
	"testing"
)

func TestProbeRenderProfile(t *testing.T) {
	env := map[string]string{
		"TERM":      "xterm-256color",
		"COLORTERM": "truecolor",
	}

	// Test with /dev/null (not a TTY) to get predictable results
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	defer f.Close()

	profile, err := ProbeRenderProfile(env, "nerd", f)
	if err != nil {
		t.Fatalf("ProbeRenderProfile: %v", err)
	}
	if profile.GlyphTier != GlyphTierNerd {
		t.Errorf("GlyphTier = %v, want nerd", profile.GlyphTier)
	}
	// /dev/null is not a TTY, so colour profile should be Ascii
	if profile.ColourProfile != ColourProfileAscii {
		t.Errorf("ColourProfile = %v, want ascii (not TTY)", profile.ColourProfile)
	}
	if profile.IsTTY {
		t.Errorf("IsTTY = true, want false for /dev/null")
	}
}

func TestProbeRenderProfileNO_COLOR(t *testing.T) {
	env := map[string]string{
		"NO_COLOR": "1",
		"TERM":     "xterm-256color",
	}

	profile, err := ProbeRenderProfile(env, "nerd", os.Stdout)
	if err != nil {
		t.Fatalf("ProbeRenderProfile: %v", err)
	}
	if profile.ColourProfile != ColourProfileAscii {
		t.Errorf("ColourProfile = %v, want ascii (NO_COLOR)", profile.ColourProfile)
	}
}

func TestProbeRenderProfileTERMdumb(t *testing.T) {
	env := map[string]string{
		"TERM": "dumb",
	}

	profile, err := ProbeRenderProfile(env, "nerd", os.Stdout)
	if err != nil {
		t.Fatalf("ProbeRenderProfile: %v", err)
	}
	if profile.ColourProfile != ColourProfileAscii {
		t.Errorf("ColourProfile = %v, want ascii (TERM=dumb)", profile.ColourProfile)
	}
}

func TestProbeRenderProfileCI(t *testing.T) {
	env := map[string]string{
		"CI": "true",
	}

	profile, err := ProbeRenderProfile(env, "nerd", os.Stdout)
	if err != nil {
		t.Fatalf("ProbeRenderProfile: %v", err)
	}
	if profile.ColourProfile != ColourProfileAscii {
		t.Errorf("ColourProfile = %v, want ascii (CI)", profile.ColourProfile)
	}
}

func TestProbeRenderProfileNotTTY(t *testing.T) {
	env := map[string]string{
		"TERM": "xterm-256color",
	}

	// /dev/null is not a TTY
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	defer f.Close()

	profile, err := ProbeRenderProfile(env, "nerd", f)
	if err != nil {
		t.Fatalf("ProbeRenderProfile: %v", err)
	}
	if profile.ColourProfile != ColourProfileAscii {
		t.Errorf("ColourProfile = %v, want ascii (not TTY)", profile.ColourProfile)
	}
	if profile.IsTTY {
		t.Errorf("IsTTY = true, want false")
	}
}

func TestParseGlyphTier(t *testing.T) {
	tests := []struct {
		input    string
		expected GlyphTier
		wantErr  bool
	}{
		{"nerd", GlyphTierNerd, false},
		{"powerline", GlyphTierPowerline, false},
		{"ascii", GlyphTierAscii, false},
		{"", GlyphTierAscii, false},
		{"NERD", GlyphTierNerd, false},
		{"invalid", GlyphTierAscii, true},
	}
	for _, tc := range tests {
		got, err := parseGlyphTier(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseGlyphTier(%q): want error, got %v", tc.input, got)
			}
		} else {
			if err != nil {
				t.Errorf("parseGlyphTier(%q): unexpected error: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Errorf("parseGlyphTier(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		}
	}
}
