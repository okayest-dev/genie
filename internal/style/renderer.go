package style

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidAttributes is the whitelist of SGR attributes (ADR-0006).
var ValidAttributes = map[string]bool{
	"bold":      true,
	"dim":       true,
	"italic":    true,
	"underline": true,
}

// ANSI colour constants
const (
	// ANSI foreground codes (30-37, 90-97)
	ansiFgOffset     = 30
	ansiBgOffset     = 40
	ansiBrightOffset = 60
)

// Renderer renders styled text through a render profile.
type Renderer struct {
	profile *RenderProfile
}

// NewRenderer creates a new renderer for the given profile.
func NewRenderer(profile *RenderProfile) *Renderer {
	return &Renderer{profile: profile}
}

// Render applies a style to the given text and returns the styled output.
// If the profile is Ascii or colour is disabled, returns plain text (attributes still apply if not colour).
func (r *Renderer) Render(style Style, text string) string {
	// Under Ascii profile, drop colours but keep attributes
	if r.profile.ColourProfile == ColourProfileAscii {
		return r.renderPlain(style, text)
	}

	var sb strings.Builder

	// Build SGR sequence
	sgr := r.buildSGR(style)
	if sgr != "" {
		sb.WriteString("\x1b[")
		sb.WriteString(sgr)
		sb.WriteString("m")
	}

	sb.WriteString(text)

	// Reset
	if sgr != "" {
		sb.WriteString("\x1b[0m")
	}

	return sb.String()
}

// renderPlain renders text with attributes only (no colours).
func (r *Renderer) renderPlain(style Style, text string) string {
	if len(style.Attributes) == 0 {
		return text
	}

	var sb strings.Builder
	sb.WriteString("\x1b[")

	for i, attr := range style.Attributes {
		if i > 0 {
			sb.WriteString(";")
		}
		switch attr {
		case "bold":
			sb.WriteString("1")
		case "dim":
			sb.WriteString("2")
		case "italic":
			sb.WriteString("3")
		case "underline":
			sb.WriteString("4")
		}
	}

	sb.WriteString("m")
	sb.WriteString(text)
	sb.WriteString("\x1b[0m")

	return sb.String()
}

// buildSGR builds the SGR parameter string for a style.
func (r *Renderer) buildSGR(style Style) string {
	var parts []string

	// Foreground
	if style.Foreground != "" {
		fg := r.convertColour(style.Foreground, true)
		if fg != "" {
			parts = append(parts, fg)
		}
	}

	// Background
	if style.Background != "" {
		bg := r.convertColour(style.Background, false)
		if bg != "" {
			parts = append(parts, bg)
		}
	}

	// Attributes
	for _, attr := range style.Attributes {
		switch attr {
		case "bold":
			parts = append(parts, "1")
		case "dim":
			parts = append(parts, "2")
		case "italic":
			parts = append(parts, "3")
		case "underline":
			parts = append(parts, "4")
		}
	}

	return strings.Join(parts, ";")
}

// convertColour converts a colour string to an SGR code based on the colour profile.
func (r *Renderer) convertColour(colour string, isFg bool) string {
	// Handle named ANSI colours
	if ansiCode := parseANSINamedColour(colour, isFg); ansiCode != "" {
		return ansiCode
	}

	// Handle hex colours (#RRGGBB or RRGGBB)
	if strings.HasPrefix(colour, "#") || (len(colour) == 6 && isHex(colour)) {
		hex := strings.TrimPrefix(colour, "#")
		return r.convertHexColour(hex, isFg)
	}

	// Handle RGB colours (r,g,b)
	if strings.Contains(colour, ",") {
		return r.convertRGBColour(colour, isFg)
	}

	return ""
}

// parseANSINamedColour parses named ANSI colours.
func parseANSINamedColour(colour string, isFg bool) string {
	offset := ansiFgOffset
	if !isFg {
		offset = ansiBgOffset
	}

	// Standard colours (0-7)
	standard := map[string]int{
		"black": 0, "red": 1, "green": 2, "yellow": 3,
		"blue": 4, "magenta": 5, "cyan": 6, "white": 7,
	}
	// Bright colours (8-15)
	bright := map[string]int{
		"bright-black": 0, "bright-red": 1, "bright-green": 2, "bright-yellow": 3,
		"bright-blue": 4, "bright-magenta": 5, "bright-cyan": 6, "bright-white": 7,
	}

	if code, ok := standard[colour]; ok {
		return strconv.Itoa(offset + code)
	}
	if code, ok := bright[colour]; ok {
		return strconv.Itoa(offset + ansiBrightOffset + code)
	}

	return ""
}

// isHex checks if a string is valid hex.
func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// convertHexColour converts a hex colour to the appropriate SGR code.
func (r *Renderer) convertHexColour(hex string, isFg bool) string {
	if len(hex) != 6 {
		return ""
	}

	rVal, _ := strconv.ParseInt(hex[0:2], 16, 0)
	gVal, _ := strconv.ParseInt(hex[2:4], 16, 0)
	bVal, _ := strconv.ParseInt(hex[4:6], 16, 0)

	return r.convertRGB(int(rVal), int(gVal), int(bVal), isFg)
}

// convertRGBColour converts an rgb(r,g,b) string to SGR code.
func (r *Renderer) convertRGBColour(colour string, isFg bool) string {
	parts := strings.Split(strings.TrimSpace(colour), ",")
	if len(parts) != 3 {
		return ""
	}

	rVal, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	gVal, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	bVal, err3 := strconv.Atoi(strings.TrimSpace(parts[2]))

	if err1 != nil || err2 != nil || err3 != nil {
		return ""
	}

	return r.convertRGB(rVal, gVal, bVal, isFg)
}

// convertRGB converts RGB values to the appropriate SGR code based on colour profile.
func (r *Renderer) convertRGB(rVal, gVal, bVal int, isFg bool) string {
	// Clamp values
	rVal = clamp(rVal, 0, 255)
	gVal = clamp(gVal, 0, 255)
	bVal = clamp(bVal, 0, 255)

	switch r.profile.ColourProfile {
	case ColourProfileTrueColor:
		return r.trueColorSGR(rVal, gVal, bVal, isFg)
	case ColourProfileANSI256:
		return r.ansi256SGR(rVal, gVal, bVal, isFg)
	case ColourProfileANSI:
		return r.ansi16SGR(rVal, gVal, bVal, isFg)
	default:
		return ""
	}
}

// trueColorSGR generates true color SGR codes (38/48;2;r;g;b).
func (r *Renderer) trueColorSGR(rVal, gVal, bVal int, isFg bool) string {
	prefix := "38"
	if !isFg {
		prefix = "48"
	}
	return fmt.Sprintf("%s;2;%d;%d;%d", prefix, rVal, gVal, bVal)
}

// ansi256SGR generates 256-color SGR codes (38/48;5;n).
func (r *Renderer) ansi256SGR(rVal, gVal, bVal int, isFg bool) string {
	prefix := "38"
	if !isFg {
		prefix = "48"
	}
	index := rgbToAnsi256(rVal, gVal, bVal)
	return fmt.Sprintf("%s;5;%d", prefix, index)
}

// ansi16SGR generates 16-color SGR codes (30-37, 40-47, 90-97, 100-107).
func (r *Renderer) ansi16SGR(rVal, gVal, bVal int, isFg bool) string {
	offset := ansiFgOffset
	if !isFg {
		offset = ansiBgOffset
	}

	// Map to closest ANSI colour
	index := rgbToAnsi16(rVal, gVal, bVal)

	// Check if it's a bright colour (8-15)
	if index >= 8 {
		return strconv.Itoa(offset + ansiBrightOffset + (index - 8))
	}
	return strconv.Itoa(offset + index)
}

// rgbToAnsi256 converts RGB to the closest 256-colour index (6x6x6 cube + grayscale).
func rgbToAnsi256(r, g, b int) int {
	// Check grayscale first
	if r == g && g == b {
		if r < 8 {
			return 16 // black
		}
		if r > 248 {
			return 231 // white
		}
		// Grayscale ramp: 232-255 (24 steps)
		// Map r in [8, 248] to [232, 255] using standard formula
		return 232 + (r-8)*24/247
	}

	// 6x6x6 colour cube: 16-231
	// Each component 0-5: 16 + 36*r + 6*g + b
	ri := r * 6 / 256
	gi := g * 6 / 256
	bi := b * 6 / 256

	if ri > 5 {
		ri = 5
	}
	if gi > 5 {
		gi = 5
	}
	if bi > 5 {
		bi = 5
	}

	return 16 + 36*ri + 6*gi + bi
}

// ANSI 16-colour palette (standard xterm values).
// Indices 0-7: standard; 8-15: bright.
var ansiPalette = [16][3]int{
	{0, 0, 0},       // 0: black
	{205, 0, 0},     // 1: red
	{0, 205, 0},     // 2: green
	{205, 205, 0},   // 3: yellow
	{0, 0, 238},     // 4: blue
	{205, 0, 205},   // 5: magenta
	{0, 205, 205},   // 6: cyan
	{229, 229, 229}, // 7: white
	{127, 127, 127}, // 8: bright black (gray)
	{255, 0, 0},     // 9: bright red
	{0, 255, 0},     // 10: bright green
	{255, 255, 0},   // 11: bright yellow
	{92, 92, 255},   // 12: bright blue
	{255, 0, 255},   // 13: bright magenta
	{0, 255, 255},   // 14: bright cyan
	{255, 255, 255}, // 15: bright white
}

// rgbToAnsi16 converts RGB to the closest 16-colour index (0-15).
func rgbToAnsi16(r, g, b int) int {
	minDist := 255*255*3 + 1
	bestIdx := 0
	for i, c := range ansiPalette {
		dr := r - c[0]
		dg := g - c[1]
		db := b - c[2]
		dist := dr*dr + dg*dg + db*db
		if dist < minDist {
			minDist = dist
			bestIdx = i
		}
	}
	return bestIdx
}

// clamp clamps a value between min and max.
func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// Glyph returns the glyph for the effective tier, falling back to ascii.
func (r *Renderer) Glyph(glyphs map[GlyphTier]string) string {
	if glyphs == nil {
		return ""
	}

	// Try exact tier first
	if glyph, ok := glyphs[r.profile.GlyphTier]; ok {
		return glyph
	}

	// Fallback to ascii
	if glyph, ok := glyphs[GlyphTierAscii]; ok {
		return glyph
	}

	return ""
}

// DisplayWidth returns the display width of a string (handles wide characters).
func DisplayWidth(s string) int {
	width := 0
	for _, r := range s {
		w := runeWidth(r)
		if w > 0 {
			width += w
		}
	}
	return width
}

// runeWidth returns the display width of a rune (1 or 2, 0 for control chars).
func runeWidth(r rune) int {
	// Control characters have width 0
	if r < 32 || (r >= 127 && r < 160) {
		return 0
	}

	// East Asian Wide characters (simplified)
	// This is a minimal implementation; a full one would use unicode tables
	if r >= 0x1100 && (r <= 0x115F || r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE19) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x20000 && r <= 0x2FFFD) ||
		(r >= 0x30000 && r <= 0x3FFFD)) {
		return 2
	}

	// Emoji and symbols (commonly rendered as double-width)
	if (r >= 0x1F300 && r <= 0x1F5FF) || // Misc Symbols and Pictographs
		(r >= 0x1F600 && r <= 0x1F64F) || // Emoticons
		(r >= 0x1F680 && r <= 0x1F6FF) || // Transport and Map Symbols
		(r >= 0x1F900 && r <= 0x1F9FF) || // Supplemental Symbols and Pictographs
		(r >= 0x1FA70 && r <= 0x1FAFF) || // Symbols and Pictographs Extended-A
		(r >= 0x2600 && r <= 0x26FF) ||   // Miscellaneous Symbols
		(r >= 0x2700 && r <= 0x27BF) {    // Dingbats
		return 2
	}

	return 1
}

// ValidateStyle validates a style against the whitelist.
func ValidateStyle(style Style) error {
	for _, attr := range style.Attributes {
		if !ValidAttributes[attr] {
			return fmt.Errorf("invalid attribute: %s", attr)
		}
	}
	return nil
}
