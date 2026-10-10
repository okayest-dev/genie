package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBuiltinClassic(t *testing.T) {
	th, err := Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if th.Name != "classic" {
		t.Errorf("Name = %q, want classic", th.Name)
	}
	if th.Source != "builtin" {
		t.Errorf("Source = %q, want builtin", th.Source)
	}
	if th.Data.Version == "" {
		t.Error("Version should not be empty")
	}
	if th.Data.Schema == "" {
		t.Error("$schema should not be empty")
	}
}

func TestLoadBuiltinLean(t *testing.T) {
	th, err := Load("lean", "/tmp")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if th.Name != "lean" {
		t.Errorf("Name = %q, want lean", th.Name)
	}
	if th.Source != "builtin" {
		t.Errorf("Source = %q, want builtin", th.Source)
	}
}

func TestLoadUnknownBuiltin(t *testing.T) {
	_, err := Load("nonexistent", "/tmp")
	if err == nil {
		t.Fatal("Load should fail for unknown builtin")
	}
}

func TestLoadUserTheme(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }
tool = { fg = "cyan", bg = "", attrs = [], glyphs = {} }
error = { fg = "red", bg = "", attrs = [], glyphs = {} }
warning = { fg = "yellow", bg = "", attrs = [], glyphs = {} }
turn_cancelled = { fg = "", bg = "", attrs = ["dim"], glyphs = {} }
banner = { fg = "", bg = "", attrs = ["dim"], glyphs = {} }
log = { fg = "bright-black", bg = "", attrs = [], glyphs = {} }
prompt = { fg = "", bg = "", attrs = ["bold"], glyphs = {} }
pick = { fg = "", bg = "", attrs = [], glyphs = {} }
slash = { fg = "", bg = "", attrs = [], glyphs = {} }
plugin = { fg = "", bg = "", attrs = [], glyphs = {} }

[[segments]]
name = "dir"
style = "dir"
options = { "dir.max_len" = "40" }

[[segments]]
name = "prompt_char"
style = "prompt_char"
options = { always = "true" }
  [segments.states]
  ok = { fg = "white", bg = "", attrs = [], glyphs = { nerd = ">", powerline = ">", ascii = ">" } }

[separators]
default = { nerd = " ", powerline = " ", ascii = " " }

[prompt]
split_threshold = 50
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	th, err := Load("custom", tmpDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if th.Name != "custom" {
		t.Errorf("Name = %q, want custom", th.Name)
	}
	if th.Source != "file" {
		t.Errorf("Source = %q, want file", th.Source)
	}
}

func TestLoadUserThemeMissingFile(t *testing.T) {
	_, err := Load("nonexistent", "/tmp/nonexistent")
	if err == nil {
		t.Fatal("Load should fail for missing user theme")
	}
	if !contains(err.Error(), "not found") {
		t.Errorf("error = %q, want not found error", err)
	}
}

func TestLoadUserThemeUnreadableDir(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Make themes dir unreadable
	if err := os.Chmod(themesDir, 0000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	defer os.Chmod(themesDir, 0755)

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unreadable themes dir")
	}
	// The error could be about the directory or the file, both indicate unreadable
	if !contains(err.Error(), "cannot read themes directory") && !contains(err.Error(), "permission denied") {
		t.Errorf("error = %q, want unreadable error", err)
	}
}

func TestResolveGlyphTier(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"nerd", "nerd", false},
		{"powerline", "powerline", false},
		{"ascii", "ascii", false},
		{"", "ascii", false}, // default
		{"invalid", "", true},
	}
	for _, tc := range tests {
		got, err := ResolveGlyphTier(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ResolveGlyphTier(%q): want error, got %q", tc.input, got)
			}
		} else {
			if err != nil {
				t.Errorf("ResolveGlyphTier(%q): unexpected error: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Errorf("ResolveGlyphTier(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		}
	}
}

func TestGetBuiltinNames(t *testing.T) {
	names := GetBuiltinNames()
	if len(names) != 2 {
		t.Fatalf("GetBuiltinNames = %v, want 2 names", names)
	}
	if names[0] != "classic" || names[1] != "lean" {
		t.Errorf("GetBuiltinNames = %v, want [classic lean]", names)
	}
}

func TestLoadUserThemeMalformedTOML(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `version = "1" invalid toml`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for malformed TOML")
	}
	if !contains(err.Error(), "theme") || !contains(err.Error(), "custom") {
		t.Errorf("error = %q, want theme error", err)
	}
}

func TestLoadUserThemeUnknownKey(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"
unknown_key = "value"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown key")
	}
	if !contains(err.Error(), "unknown key") {
		t.Errorf("error = %q, want unknown key error", err)
	}
}

func TestLoadUserThemeUnknownRole(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
unknown_role = { fg = "red", bg = "", attrs = [], glyphs = {} }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown role")
	}
	if !contains(err.Error(), "unknown role") {
		t.Errorf("error = %q, want unknown role error", err)
	}
}

func TestLoadUserThemeUnknownSegment(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }

[[segments]]
name = "unknown_segment"
style = "unknown_segment"
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown segment")
	}
	if !contains(err.Error(), "unknown segment") {
		t.Errorf("error = %q, want unknown segment error", err)
	}
}

func TestLoadUserThemeDuplicateSegment(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }

[[segments]]
name = "dir"
style = "dir"

[[segments]]
name = "dir"
style = "dir"
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for duplicate segment")
	}
	if !contains(err.Error(), "duplicate segment") {
		t.Errorf("error = %q, want duplicate segment error", err)
	}
}

func TestLoadUserThemeUnknownOption(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }

[[segments]]
name = "dir"
style = "dir"
options = { "unknown_option" = "value" }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown option")
	}
	if !contains(err.Error(), "unknown option") {
		t.Errorf("error = %q, want unknown option error", err)
	}
}

func TestLoadUserThemeUnknownState(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }

[[segments]]
name = "prompt_char"
style = "prompt_char"
options = { always = "true" }
  [segments.states]
  unknown_state = { fg = "white", bg = "", attrs = [], glyphs = {} }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown state")
	}
	if !contains(err.Error(), "unknown state") {
		t.Errorf("error = %q, want unknown state error", err)
	}
}

func TestLoadUserThemeUnknownAttribute(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = ["blink"], glyphs = {} }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown attribute")
	}
	if !contains(err.Error(), "unknown attribute") {
		t.Errorf("error = %q, want unknown attribute error", err)
	}
}

func TestLoadUserThemeUnknownGlyphTier(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = { unknown_tier = ">" } }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown glyph tier")
	}
	if !contains(err.Error(), "unknown glyph tier") {
		t.Errorf("error = %q, want unknown glyph tier error", err)
	}
}

func TestLoadUserThemeMissingVersion(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for missing version")
	}
	if !contains(err.Error(), "missing version") {
		t.Errorf("error = %q, want missing version error", err)
	}
}

func TestLoadUserThemeMissingSchema(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for missing $schema")
	}
	if !contains(err.Error(), "missing $schema") {
		t.Errorf("error = %q, want missing $schema error", err)
	}
}

func TestLoadUserThemeInvalidSplitThreshold(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }

[[segments]]
name = "dir"
style = "dir"

[separators]
default = { nerd = " ", powerline = " ", ascii = " " }

[prompt]
split_threshold = 150
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for invalid split_threshold")
	}
	if !contains(err.Error(), "split_threshold") {
		t.Errorf("error = %q, want split_threshold error", err)
	}
}

func TestLoadUserThemeUnknownSeparatorGlyphTier(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }

[[segments]]
name = "dir"
style = "dir"

[separators]
default = { unknown_tier = " " }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown separator glyph tier")
	}
	if !contains(err.Error(), "unknown glyph tier") {
		t.Errorf("error = %q, want unknown glyph tier error", err)
	}
}

func TestLoadUserThemeUnknownSeparatorOverrideSegment(t *testing.T) {
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }

[[segments]]
name = "dir"
style = "dir"

[separators]
default = { nerd = " ", powerline = " ", ascii = " " }
overrides = { unknown_segment = " " }
`
	themeFile := filepath.Join(themesDir, "custom.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load("custom", tmpDir)
	if err == nil {
		t.Fatal("Load should fail for unknown separator override segment")
	}
	if !contains(err.Error(), "separator override for unknown segment") {
		t.Errorf("error = %q, want unknown separator override segment error", err)
	}
}

func TestLoadBuiltinShadowingFails(t *testing.T) {
	// Create a user theme file named "classic" - should not be loadable since
	// builtin names are reserved and cannot be shadowed
	tmpDir := t.TempDir()
	themesDir := filepath.Join(tmpDir, "genie", "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	themeContent := `
version = "1"
"$schema" = "https://genie.dev/schema/theme-v1.json"

[roles]
model = { fg = "", bg = "", attrs = [], glyphs = {} }
`
	themeFile := filepath.Join(themesDir, "classic.toml")
	if err := os.WriteFile(themeFile, []byte(themeContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load should return the builtin, not the user file
	th, err := Load("classic", tmpDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if th.Source != "builtin" {
		t.Errorf("Source = %q, want builtin (user file should not shadow)", th.Source)
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
