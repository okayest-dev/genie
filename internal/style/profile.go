package style

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// ColourProfile represents the colour capability of a terminal.
type ColourProfile int

const (
	// ColourProfileAscii - no colour, no glyphs, ASCII only
	ColourProfileAscii ColourProfile = iota
	// ColourProfileANSI - 16 colours
	ColourProfileANSI
	// ColourProfileANSI256 - 256 colours
	ColourProfileANSI256
	// ColourProfileTrueColor - 24-bit true colour
	ColourProfileTrueColor
)

// GlyphTier represents the glyph capability of a terminal.
type GlyphTier int

const (
	// GlyphTierAscii - ASCII glyphs only
	GlyphTierAscii GlyphTier = iota
	// GlyphTierPowerline - Powerline glyphs (e.g., )
	GlyphTierPowerline
	// GlyphTierNerd - Nerd Font glyphs
	GlyphTierNerd
)

// RenderProfile is a per-writer, per-process snapshot of terminal capabilities.
type RenderProfile struct {
	// IsTTY indicates whether the writer is a TTY
	IsTTY bool
	// ColourProfile is the detected colour capability
	ColourProfile ColourProfile
	// GlyphTier is the configured glyph tier
	GlyphTier GlyphTier
	// Width is the terminal width in columns (0 if unknown)
	Width int
}

// Style represents a styling specification for text output.
type Style struct {
	// Foreground colour (optional)
	Foreground string
	// Background colour (optional)
	Background string
	// SGR attributes (subset of: bold, dim, italic, underline)
	Attributes []string
	// Glyphs per tier (optional)
	Glyphs map[GlyphTier]string
}

// ProbeRenderProfile probes the terminal capabilities for a writer.
// env is a map of environment variables (typically os.Environ() parsed).
// glyphTierStr is the configured glyph tier from config ("nerd", "powerline", "ascii").
func ProbeRenderProfile(env map[string]string, glyphTierStr string, writer *os.File) (*RenderProfile, error) {
	// Check if writer is a TTY
	isTTY := term.IsTerminal(int(writer.Fd()))

	// Probe terminal width
	width := 0
	if isTTY {
		w, _, err := term.GetSize(int(writer.Fd()))
		if err == nil {
			width = w
		}
	}

	// Determine colour profile
	colourProfile := probeColourProfile(env, isTTY)

	// Parse glyph tier
	glyphTier, err := parseGlyphTier(glyphTierStr)
	if err != nil {
		return nil, err
	}

	return &RenderProfile{
		IsTTY:         isTTY,
		ColourProfile: colourProfile,
		GlyphTier:     glyphTier,
		Width:         width,
	}, nil
}

// probeColourProfile determines the colour profile from environment and TTY status.
func probeColourProfile(env map[string]string, isTTY bool) ColourProfile {
	// NO_COLOR, TERM=dumb, or CI present → Ascii profile (no colour)
	if v := env["NO_COLOR"]; v != "" {
		return ColourProfileAscii
	}
	if v := env["TERM"]; v == "dumb" {
		return ColourProfileAscii
	}
	if v := env["CI"]; v != "" {
		return ColourProfileAscii
	}

	// Not a TTY → Ascii profile
	if !isTTY {
		return ColourProfileAscii
	}

	// Check for TrueColor indicators
	colorTerm := strings.ToLower(env["COLORTERM"])
	if colorTerm == "truecolor" || colorTerm == "24bit" {
		return ColourProfileTrueColor
	}

	// Check for 256-color indicators
	if colorTerm == "256color" || colorTerm == "256" {
		return ColourProfileANSI256
	}

	// Check TERM for 256color
	termVal := strings.ToLower(env["TERM"])
	if strings.Contains(termVal, "256color") || strings.Contains(termVal, "256") {
		return ColourProfileANSI256
	}

	// Check for other TrueColor indicators
	if env["WT_SESSION"] != "" || env["ConEmuANSI"] != "" || env["GOOGLE_CLOUD_SHELL"] != "" {
		return ColourProfileTrueColor
	}

	// Default to ANSI (16 colours) for TTY
	return ColourProfileANSI
}

// parseGlyphTier parses a glyph tier string.
func parseGlyphTier(s string) (GlyphTier, error) {
	switch strings.ToLower(s) {
	case "nerd":
		return GlyphTierNerd, nil
	case "powerline":
		return GlyphTierPowerline, nil
	case "ascii", "":
		return GlyphTierAscii, nil
	default:
		return GlyphTierAscii, fmt.Errorf("unknown glyph tier: %s", s)
	}
}

// String returns a string representation of the colour profile.
func (c ColourProfile) String() string {
	switch c {
	case ColourProfileAscii:
		return "ascii"
	case ColourProfileANSI:
		return "ansi"
	case ColourProfileANSI256:
		return "ansi256"
	case ColourProfileTrueColor:
		return "truecolor"
	default:
		return "unknown"
	}
}

// String returns a string representation of the glyph tier.
func (g GlyphTier) String() string {
	switch g {
	case GlyphTierAscii:
		return "ascii"
	case GlyphTierPowerline:
		return "powerline"
	case GlyphTierNerd:
		return "nerd"
	default:
		return "unknown"
	}
}
