package style

import (
	"testing"
)

func TestRendererBasic(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	style := Style{
		Foreground: "red",
		Attributes: []string{"bold"},
	}
	output := r.Render(style, "hello")
	if output == "hello" {
		t.Error("expected styled output, got plain")
	}
	if !contains(output, "hello") {
		t.Errorf("output missing text: %s", output)
	}
}

func TestRendererAsciiProfile(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileAscii,
		GlyphTier:     GlyphTierAscii,
		Width:         80,
	}
	r := NewRenderer(profile)

	// Colours should be dropped, attributes should remain
	style := Style{
		Foreground: "red",
		Attributes: []string{"bold"},
	}
	output := r.Render(style, "hello")
	if output == "hello" {
		t.Error("expected attribute-styled output")
	}
	// Should have bold (1) but not red colour
	if !contains(output, "1m") && !contains(output, "\x1b[1m") {
		t.Errorf("expected bold attribute in output: %s", output)
	}
	// Should not have colour codes (31 for red)
	if contains(output, "31") {
		t.Errorf("unexpected colour code in ascii profile: %s", output)
	}
}

func TestRendererAttributesOnly(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	style := Style{
		Attributes: []string{"bold", "underline"},
	}
	output := r.Render(style, "text")
	if !contains(output, "1") || !contains(output, "4") {
		t.Errorf("expected bold and underline: %s", output)
	}
}

func TestRendererFgBg(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	style := Style{
		Foreground: "#ff0000",
		Background: "#0000ff",
	}
	output := r.Render(style, "text")
	// Should have both fg (38;2;...) and bg (48;2;...)
	if !contains(output, "38;2") || !contains(output, "48;2") {
		t.Errorf("expected truecolor fg and bg: %s", output)
	}
}

func TestRendererHexColour(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	style := Style{
		Foreground: "#ff0000",
	}
	output := r.Render(style, "text")
	if !contains(output, "38;2;255;0;0") {
		t.Errorf("expected truecolor hex red: %s", output)
	}
}

func TestRendererRGBColour(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	style := Style{
		Foreground: "255,0,0",
	}
	output := r.Render(style, "text")
	if !contains(output, "38;2;255;0;0") {
		t.Errorf("expected truecolor rgb red: %s", output)
	}
}

func TestRendererColourDownconversion(t *testing.T) {
	tests := []struct {
		name          string
		profile       ColourProfile
		fg            string
		expectedCodes []string
		notExpected   []string
	}{
		{
			name:          "truecolor to truecolor",
			profile:       ColourProfileTrueColor,
			fg:            "#ff0000",
			expectedCodes: []string{"38;2;255;0;0"},
		},
		{
			name:          "truecolor to ansi256",
			profile:       ColourProfileANSI256,
			fg:            "#ff0000",
			expectedCodes: []string{"38;5;"},
		},
		{
			name:          "truecolor to ansi16",
			profile:       ColourProfileANSI,
			fg:            "#cd0000",      // ANSI red (not bright)
			expectedCodes: []string{"31"}, // ANSI red
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := &RenderProfile{
				IsTTY:         true,
				ColourProfile: tc.profile,
				GlyphTier:     GlyphTierNerd,
				Width:         80,
			}
			r := NewRenderer(profile)

			style := Style{Foreground: tc.fg}
			output := r.Render(style, "text")

			for _, code := range tc.expectedCodes {
				if !contains(output, code) {
					t.Errorf("%s: expected code %q in output: %s", tc.name, code, output)
				}
			}
			for _, code := range tc.notExpected {
				if contains(output, code) {
					t.Errorf("%s: unexpected code %q in output: %s", tc.name, code, output)
				}
			}
		})
	}
}

func TestRendererNamedColours(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	colours := map[string]string{
		"black":   "30",
		"red":     "31",
		"green":   "32",
		"yellow":  "33",
		"blue":    "34",
		"magenta": "35",
		"cyan":    "36",
		"white":   "37",
	}

	for name, expected := range colours {
		style := Style{Foreground: name}
		output := r.Render(style, "text")
		if !contains(output, expected) {
			t.Errorf("colour %s: expected code %s in output: %s", name, expected, output)
		}
	}
}

func TestRendererBrightColours(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	colours := map[string]string{
		"bright-black":   "90",
		"bright-red":     "91",
		"bright-green":   "92",
		"bright-yellow":  "93",
		"bright-blue":    "94",
		"bright-magenta": "95",
		"bright-cyan":    "96",
		"bright-white":   "97",
	}

	for name, expected := range colours {
		style := Style{Foreground: name}
		output := r.Render(style, "text")
		if !contains(output, expected) {
			t.Errorf("bright colour %s: expected code %s in output: %s", name, expected, output)
		}
	}
}

func TestRendererBackgroundColours(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	style := Style{
		Background: "red",
	}
	output := r.Render(style, "text")
	// Background red = 41
	if !contains(output, "41") {
		t.Errorf("expected background red (41): %s", output)
	}
}

func TestGlyphSelection(t *testing.T) {
	tests := []struct {
		name      string
		glyphTier GlyphTier
		glyphs    map[GlyphTier]string
		expected  string
	}{
		{
			name:      "exact tier match",
			glyphTier: GlyphTierNerd,
			glyphs:    map[GlyphTier]string{GlyphTierNerd: "❯", GlyphTierAscii: ">"},
			expected:  "❯",
		},
		{
			name:      "fallback to ascii",
			glyphTier: GlyphTierNerd,
			glyphs:    map[GlyphTier]string{GlyphTierPowerline: "", GlyphTierAscii: ">"},
			expected:  ">",
		},
		{
			name:      "ascii tier exact",
			glyphTier: GlyphTierAscii,
			glyphs:    map[GlyphTier]string{GlyphTierNerd: "❯", GlyphTierAscii: ">"},
			expected:  ">",
		},
		{
			name:      "nil glyphs",
			glyphTier: GlyphTierNerd,
			glyphs:    nil,
			expected:  "",
		},
		{
			name:      "empty glyphs",
			glyphTier: GlyphTierNerd,
			glyphs:    map[GlyphTier]string{},
			expected:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := &RenderProfile{
				IsTTY:         true,
				ColourProfile: ColourProfileTrueColor,
				GlyphTier:     tc.glyphTier,
				Width:         80,
			}
			r := NewRenderer(profile)
			got := r.Glyph(tc.glyphs)
			if got != tc.expected {
				t.Errorf("Glyph: got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestDisplayWidth(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"hello", 5},
		{"", 0},
		{"こんにちは", 10}, // Japanese chars are wide (2 each)
		{"aあ", 3},     // mixed
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := DisplayWidth(tc.input)
			if got != tc.expected {
				t.Errorf("DisplayWidth(%q) = %d, want %d", tc.input, got, tc.expected)
			}
		})
	}
}

func TestValidateStyle(t *testing.T) {
	tests := []struct {
		name    string
		style   Style
		wantErr bool
	}{
		{
			name:    "valid attributes",
			style:   Style{Attributes: []string{"bold", "italic"}},
			wantErr: false,
		},
		{
			name:    "invalid attribute",
			style:   Style{Attributes: []string{"blink"}},
			wantErr: true,
		},
		{
			name:    "empty",
			style:   Style{},
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStyle(tc.style)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error for invalid attribute")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestRGBToAnsi256(t *testing.T) {
	tests := []struct {
		r, g, b  int
		expected int
	}{
		{0, 0, 0, 16},        // black (in cube)
		{255, 255, 255, 231}, // white (in cube)
		{255, 0, 0, 196},     // red
		{0, 255, 0, 46},      // green
		{0, 0, 255, 21},      // blue
		{128, 128, 128, 243}, // gray (in grayscale ramp: 232 + (128-8)*24/247 = 243)
	}
	for _, tc := range tests {
		got := rgbToAnsi256(tc.r, tc.g, tc.b)
		if got != tc.expected {
			t.Errorf("rgbToAnsi256(%d,%d,%d) = %d, want %d", tc.r, tc.g, tc.b, got, tc.expected)
		}
	}
}

func TestRGBToAnsi16(t *testing.T) {
	// Just verify it returns a valid index
	for r := 0; r <= 255; r += 51 {
		for g := 0; g <= 255; g += 51 {
			for b := 0; b <= 255; b += 51 {
				idx := rgbToAnsi16(r, g, b)
				if idx < 0 || idx > 15 {
					t.Errorf("rgbToAnsi16(%d,%d,%d) = %d, want 0-15", r, g, b, idx)
				}
			}
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestColourProfileString(t *testing.T) {
	tests := []struct {
		profile    ColourProfile
		expected   string
	}{
		{ColourProfileAscii, "ascii"},
		{ColourProfileANSI, "ansi"},
		{ColourProfileANSI256, "ansi256"},
		{ColourProfileTrueColor, "truecolor"},
		{ColourProfile(99), "unknown"},
	}
	for _, tc := range tests {
		got := tc.profile.String()
		if got != tc.expected {
			t.Errorf("ColourProfile.String(%v) = %q, want %q", tc.profile, got, tc.expected)
		}
	}
}

func TestGlyphTierString(t *testing.T) {
	tests := []struct {
		tier      GlyphTier
		expected  string
	}{
		{GlyphTierAscii, "ascii"},
		{GlyphTierPowerline, "powerline"},
		{GlyphTierNerd, "nerd"},
		{GlyphTier(99), "unknown"},
	}
	for _, tc := range tests {
		got := tc.tier.String()
		if got != tc.expected {
			t.Errorf("GlyphTier.String(%v) = %q, want %q", tc.tier, got, tc.expected)
		}
	}
}

func TestProbeColourProfile(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		isTTY      bool
		expected   ColourProfile
	}{
		{
			name:       "NO_COLOR set",
			env:        map[string]string{"NO_COLOR": "1", "TERM": "xterm-256color"},
			isTTY:      true,
			expected:   ColourProfileAscii,
		},
		{
			name:       "TERM=dumb",
			env:        map[string]string{"TERM": "dumb"},
			isTTY:      true,
			expected:   ColourProfileAscii,
		},
		{
			name:       "CI set",
			env:        map[string]string{"CI": "true", "TERM": "xterm-256color"},
			isTTY:      true,
			expected:   ColourProfileAscii,
		},
		{
			name:       "not a TTY",
			env:        map[string]string{"TERM": "xterm-256color"},
			isTTY:      false,
			expected:   ColourProfileAscii,
		},
		{
			name:       "COLORTERM=truecolor",
			env:        map[string]string{"COLORTERM": "truecolor", "TERM": "xterm"},
			isTTY:      true,
			expected:   ColourProfileTrueColor,
		},
		{
			name:       "COLORTERM=24bit",
			env:        map[string]string{"COLORTERM": "24bit", "TERM": "xterm"},
			isTTY:      true,
			expected:   ColourProfileTrueColor,
		},
		{
			name:       "COLORTERM=256color",
			env:        map[string]string{"COLORTERM": "256color", "TERM": "xterm"},
			isTTY:      true,
			expected:   ColourProfileANSI256,
		},
		{
			name:       "TERM contains 256color",
			env:        map[string]string{"TERM": "xterm-256color"},
			isTTY:      true,
			expected:   ColourProfileANSI256,
		},
		{
			name:       "WT_SESSION set (Windows Terminal)",
			env:        map[string]string{"WT_SESSION": "1", "TERM": "xterm"},
			isTTY:      true,
			expected:   ColourProfileTrueColor,
		},
		{
			name:       "ConEmuANSI set",
			env:        map[string]string{"ConEmuANSI": "ON", "TERM": "xterm"},
			isTTY:      true,
			expected:   ColourProfileTrueColor,
		},
		{
			name:       "GOOGLE_CLOUD_SHELL set",
			env:        map[string]string{"GOOGLE_CLOUD_SHELL": "1", "TERM": "xterm"},
			isTTY:      true,
			expected:   ColourProfileTrueColor,
		},
		{
			name:       "default ANSI for TTY",
			env:        map[string]string{"TERM": "xterm"},
			isTTY:      true,
			expected:   ColourProfileANSI,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := probeColourProfile(tc.env, tc.isTTY)
			if got != tc.expected {
				t.Errorf("probeColourProfile: got %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestIsHex(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"000000", true},
		{"ffffff", true},
		{"FF00FF", true},
		{"abcdef", true},
		{"ABCDEF", true},
		{"123456", true},
		{"gggggg", false},
		{"12345", true},  // valid hex chars, length checked elsewhere
		{"1234567", true}, // valid hex chars, length checked elsewhere
		{"", true},       // empty string has no invalid chars
		{"12 456", false},
	}
	for _, tc := range tests {
		got := isHex(tc.input)
		if got != tc.expected {
			t.Errorf("isHex(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestRenderPlainWithBackground(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileAscii,
		GlyphTier:     GlyphTierAscii,
		Width:         80,
	}
	r := NewRenderer(profile)

	// Background should be dropped in Ascii profile, only attributes remain
	style := Style{
		Background: "red",
		Attributes: []string{"bold"},
	}
	output := r.Render(style, "text")
	// Should have bold (1) but not background colour
	if !contains(output, "1m") && !contains(output, "\x1b[1m") {
		t.Errorf("expected bold attribute in output: %s", output)
	}
}

func TestConvertRGBColour(t *testing.T) {
	tests := []struct {
		name         string
		profile      ColourProfile
		colour       string
		expectedCode string
	}{
		{
			name:         "truecolor rgb",
			profile:      ColourProfileTrueColor,
			colour:       "255,0,0",
			expectedCode: "38;2;255;0;0",
		},
		{
			name:         "ansi256 rgb",
			profile:      ColourProfileANSI256,
			colour:       "255,0,0",
			expectedCode: "38;5;",
		},
		{
			name:         "ansi16 rgb",
			profile:      ColourProfileANSI,
			colour:       "205,0,0", // ANSI red
			expectedCode: "31",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := &RenderProfile{
				IsTTY:         true,
				ColourProfile: tc.profile,
				GlyphTier:     GlyphTierNerd,
				Width:         80,
			}
			r := NewRenderer(profile)

			style := Style{Foreground: tc.colour}
			output := r.Render(style, "text")
			if !contains(output, tc.expectedCode) {
				t.Errorf("expected code %q in output: %s", tc.expectedCode, output)
			}
		})
	}
}

func TestAnsi16SGREdgeCases(t *testing.T) {
	profile := &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileANSI,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
	r := NewRenderer(profile)

	// Test bright colours (index >= 8)
	style := Style{Foreground: "#ff0000"} // bright red
	output := r.Render(style, "text")
	if !contains(output, "91") { // 91 = bright red
		t.Errorf("expected bright red (91) in output: %s", output)
	}

	// Test background bright colours
	style = Style{Background: "#ff0000"}
	output = r.Render(style, "text")
	if !contains(output, "101") { // 101 = bright red background
		t.Errorf("expected bright red background (101) in output: %s", output)
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		v, min, max, expected int
	}{
		{100, 0, 255, 100},
		{-10, 0, 255, 0},
		{300, 0, 255, 255},
		{0, 0, 255, 0},
		{255, 0, 255, 255},
		{128, -100, 100, 100},
		{-128, -100, 100, -100},
	}
	for _, tc := range tests {
		got := clamp(tc.v, tc.min, tc.max)
		if got != tc.expected {
			t.Errorf("clamp(%d, %d, %d) = %d, want %d", tc.v, tc.min, tc.max, got, tc.expected)
		}
	}
}

func TestRuneWidth(t *testing.T) {
	tests := []struct {
		r        rune
		expected int
	}{
		{'a', 1},
		{' ', 1},
		{'\n', 0}, // control char
		{'\t', 0}, // control char
		{127, 0},  // DEL control char
		{0x3000, 2}, // ideographic space
		{0x4E00, 2}, // CJK unified ideograph
		{0xFF01, 2}, // fullwidth exclamation mark
		{0x20, 1},   // regular space
		// Emoji U+1F600 is not in the East Asian Wide range in this implementation
		{0x1F600, 1},
	}
	for _, tc := range tests {
		got := runeWidth(tc.r)
		if got != tc.expected {
			t.Errorf("runeWidth(%U) = %d, want %d", tc.r, got, tc.expected)
		}
	}
}

func TestDisplayWidthMore(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"abc", 3},
		{"あいう", 6},
		{"aあb", 4},
		{"", 0},
		{"\n\t", 0}, // control chars
		{"hello world", 11},
	}
	for _, tc := range tests {
		got := DisplayWidth(tc.input)
		if got != tc.expected {
			t.Errorf("DisplayWidth(%q) = %d, want %d", tc.input, got, tc.expected)
		}
	}
}
