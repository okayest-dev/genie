package theme

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	glyphTierNerd      = "nerd"
	glyphTierPowerline = "powerline"
	glyphTierAscii     = "ascii"
)

//go:embed presets/classic.toml presets/lean.toml
var presetFS embed.FS

// Theme is the resolved theme data.
type Theme struct {
	Name   string
	Source string // "builtin" or "file"
	Raw    []byte
	Data   ThemeData
}

// ThemeData represents the parsed theme file structure.
type ThemeData struct {
	Version    string               `toml:"version"`
	Schema     string               `toml:"$schema"`
	Roles      map[string]RoleStyle `toml:"roles"`
	Segments   []SegmentEntry       `toml:"segments"`
	Separators SeparatorConfig      `toml:"separators"`
	Prompt     PromptConfig         `toml:"prompt"`
}

// RoleStyle represents a style for an output role.
type RoleStyle struct {
	Foreground string            `toml:"fg"`
	Background string            `toml:"bg"`
	Attributes []string          `toml:"attrs"`
	Glyphs     map[string]string `toml:"glyphs"`
}

// SegmentEntry represents a prompt segment in the layout.
type SegmentEntry struct {
	Name    string                `toml:"name"`
	Style   string                `toml:"style"`
	Options map[string]string     `toml:"options"`
	States  map[string]StateStyle `toml:"states"`
}

// StateStyle represents a style for a segment state.
type StateStyle struct {
	Foreground string            `toml:"fg"`
	Background string            `toml:"bg"`
	Attributes []string          `toml:"attrs"`
	Glyphs     map[string]string `toml:"glyphs"`
}

// SeparatorConfig holds separator configuration.
type SeparatorConfig struct {
	Default   map[string]string `toml:"default"`
	Overrides map[string]string `toml:"overrides"`
}

// PromptConfig holds prompt-specific configuration.
type PromptConfig struct {
	SplitThreshold int `toml:"split_threshold"`
}

// ValidRoles is the closed set of output roles (ADR-0009).
var ValidRoles = map[string]bool{
	"model":          true,
	"tool":           true,
	"error":          true,
	"warning":        true,
	"turn_cancelled": true,
	"banner":         true,
	"log":            true,
	"prompt":         true,
	"pick":           true,
	"slash":          true,
	"plugin":         true,
}

// ValidSegments is the closed set of prompt segments (ADR-0007).
var ValidSegments = map[string]bool{
	"dir":         true,
	"provider":    true,
	"model":       true,
	"agent":       true,
	"session":     true,
	"git":         true,
	"time":        true,
	"tokens":      true,
	"prompt_char": true,
}

// ValidSegmentStates maps segment names to their valid states (ADR-0010).
var ValidSegmentStates = map[string]map[string]bool{
	"prompt_char": {"ok": true, "error": true, "cancelled": true},
	"dir":         {"anchored": true, "shortened": true},
	"git":         {"clean": true, "dirty": true, "unknown": true},
	"provider":    {},
	"model":       {},
	"agent":       {},
	"session":     {},
	"time":        {},
	"tokens":      {},
}

// ValidGlyphTiers is the closed set of glyph tiers (ADR-0006).
var ValidGlyphTiers = map[string]bool{
	glyphTierNerd:      true,
	glyphTierPowerline: true,
	glyphTierAscii:     true,
}

// ValidStyleAttributes is the whitelist of SGR attributes (ADR-0006).
var ValidStyleAttributes = map[string]bool{
	"bold":      true,
	"dim":       true,
	"italic":    true,
	"underline": true,
}

// ValidSeparatorTypes includes the gap (whitespace) as a separator type.
var ValidSeparatorTypes = map[string]bool{
	"gap": true,
}

// ValidPromptOptions is the whitelist of per-segment options (ADR-0007).
var ValidPromptOptions = map[string]bool{
	"time.format": true,
	"dir.max_len": true,
	"session.len": true,
	"always":      true,
}

// Load loads and validates the theme specified by name.
func Load(themeName, userConfigDir string) (*Theme, error) {
	if themeName == "" {
		themeName = "classic"
	}

	// Builtin presets take precedence and cannot be shadowed
	if themeName == "classic" || themeName == "lean" {
		return loadBuiltin(themeName)
	}

	// User theme file
	themesDir := filepath.Join(userConfigDir, "genie", "themes")
	themeFile := filepath.Join(themesDir, themeName+".toml")

	data, err := os.ReadFile(themeFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("theme %q not found (not a builtin preset, no user theme file at %s)", themeName, themeFile)
		}
		return nil, fmt.Errorf("cannot read theme file %s: %w", themeFile, err)
	}

	return parseTheme(themeName, "file", data)
}

// loadBuiltin loads a builtin preset theme.
func loadBuiltin(name string) (*Theme, error) {
	filename := "presets/" + name + ".toml"
	data, err := presetFS.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("builtin theme %q not found: %w", name, err)
	}
	return parseTheme(name, "builtin", data)
}

// parseTheme parses and validates a theme.
func parseTheme(name, source string, data []byte) (*Theme, error) {
	var td ThemeData
	md, err := toml.Decode(string(data), &td)
	if err != nil {
		return nil, fmt.Errorf("theme %q: %w", name, err)
	}

	if unknown := md.Undecoded(); len(unknown) > 0 {
		keys := make([]string, len(unknown))
		for i, k := range unknown {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("theme %q: unknown key(s): %s", name, strings.Join(keys, ", "))
	}

	// Validate required fields
	if td.Version == "" {
		return nil, fmt.Errorf("theme %q: missing version", name)
	}
	if td.Schema == "" {
		return nil, fmt.Errorf("theme %q: missing $schema", name)
	}

	// Validate roles
	for roleName, roleStyle := range td.Roles {
		if !ValidRoles[roleName] {
			return nil, fmt.Errorf("theme %q: unknown role %q", name, roleName)
		}
		if err := validateStyle(roleStyle.Attributes, roleStyle.Glyphs, name, "role "+roleName); err != nil {
			return nil, err
		}
	}

	// Validate segments
	seenSegments := make(map[string]bool)
	for i, seg := range td.Segments {
		if !ValidSegments[seg.Name] {
			return nil, fmt.Errorf("theme %q: segment[%d]: unknown segment %q", name, i, seg.Name)
		}
		if seenSegments[seg.Name] {
			return nil, fmt.Errorf("theme %q: duplicate segment %q", name, seg.Name)
		}
		seenSegments[seg.Name] = true

		if seg.Style != "" {
			// Style reference validation would need the roles map
			// For now, we accept any string; resolution happens later
		}

		// Validate options
		for optName := range seg.Options {
			if !ValidPromptOptions[optName] {
				return nil, fmt.Errorf("theme %q: segment %q: unknown option %q", name, seg.Name, optName)
			}
		}

		// Validate states
		validStates := ValidSegmentStates[seg.Name]
		for stateName, stateStyle := range seg.States {
			if !validStates[stateName] {
				return nil, fmt.Errorf("theme %q: segment %q: unknown state %q", name, seg.Name, stateName)
			}
			if err := validateStyle(stateStyle.Attributes, stateStyle.Glyphs, name, "segment "+seg.Name+" state "+stateName); err != nil {
				return nil, err
			}
		}
	}

	// Validate separators
	if len(td.Separators.Default) > 0 {
		for tier := range td.Separators.Default {
			if !ValidGlyphTiers[tier] {
				return nil, fmt.Errorf("theme %q: separator default: unknown glyph tier %q", name, tier)
			}
		}
	}
	for segName := range td.Separators.Overrides {
		if !ValidSegments[segName] {
			return nil, fmt.Errorf("theme %q: separator override for unknown segment %q", name, segName)
		}
	}

	// Validate prompt config
	if td.Prompt.SplitThreshold < 0 || td.Prompt.SplitThreshold > 100 {
		return nil, fmt.Errorf("theme %q: prompt.split_threshold must be in [0, 100], got %d", name, td.Prompt.SplitThreshold)
	}

	return &Theme{
		Name:   name,
		Source: source,
		Raw:    data,
		Data:   td,
	}, nil
}

// validateStyle validates a style (role or state).
// Both RoleStyle and StateStyle have the same fields, so we extract the relevant parts.
func validateStyle(attrs []string, glyphs map[string]string, themeName, context string) error {
	// Validate attributes
	for _, attr := range attrs {
		if !ValidStyleAttributes[attr] {
			return fmt.Errorf("theme %q: %s: unknown attribute %q", themeName, context, attr)
		}
	}
	// Validate glyphs
	for tier := range glyphs {
		if !ValidGlyphTiers[tier] {
			return fmt.Errorf("theme %q: %s: unknown glyph tier %q", themeName, context, tier)
		}
	}
	return nil
}

// ResolveGlyphTier resolves the glyph tier from a string, validating it.
func ResolveGlyphTier(tier string) (string, error) {
	if tier == "" {
		tier = glyphTierAscii
	}
	if !ValidGlyphTiers[tier] {
		return "", fmt.Errorf("invalid glyph_tier %q: must be one of nerd, powerline, ascii", tier)
	}
	return tier, nil
}

// GetBuiltinNames returns the list of builtin theme names.
func GetBuiltinNames() []string {
	return []string{"classic", "lean"}
}
