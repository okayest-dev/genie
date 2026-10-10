package prompt

import (
	"strings"

	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/theme"
)

// RenderedSegment represents a segment with its rendered content and styling.
type RenderedSegment struct {
	Name      string
	Content   string
	State     string
	Style     style.Style
	Separator string
	Always    bool
	DisplayWidth int
}

// RenderBar renders the prompt segment bar according to the theme.
// Returns the bar lines (1 or 2 lines if split) and whether it was split.
func RenderBar(
	segments []SegmentContent,
	theme *theme.Theme,
	profile *style.RenderProfile,
	termWidth int,
) ([]string, bool) {
	if len(segments) == 0 {
		return []string{""}, false
	}

	// Build rendered segments with styles and separators
	rendered := buildRenderedSegments(segments, theme, profile)

	// Apply collapsing rules: empty content collapses segment and adjacent separators
	rendered = collapseSegments(rendered)

	// Build the bar lines with separators
	barLines := buildBarLines(rendered, theme, profile)

	// Check if we need to split
	if termWidth > 0 && len(barLines) == 1 {
		barWidth := displayWidth(barLines[0])
		threshold := theme.Data.Prompt.SplitThreshold
		if threshold <= 0 {
			threshold = 50
		}
		// Split if bar width exceeds threshold% of terminal width
		if barWidth*100 > threshold*termWidth {
			// Split: move prompt_char to next line
			return splitBar(barLines[0], rendered, theme, profile), true
		}
	}

	return barLines, false
}

// segmentData holds the data we need from a theme segment entry.
type segmentData struct {
	Name    string
	Style   string
	Options map[string]string
	States  map[string]theme.StateStyle
}

// buildRenderedSegments applies styles and separators to segments.
func buildRenderedSegments(
	segments []SegmentContent,
	theme *theme.Theme,
	profile *style.RenderProfile,
) []RenderedSegment {
	renderer := style.NewRenderer(profile)

	var result []RenderedSegment
	for i, seg := range segments {
		// Find segment entry in theme
		var segEntry *segmentData
		for j := range theme.Data.Segments {
			if theme.Data.Segments[j].Name == seg.Name {
				segEntry = &segmentData{
					Name:    theme.Data.Segments[j].Name,
					Style:   theme.Data.Segments[j].Style,
					Options: theme.Data.Segments[j].Options,
					States:  theme.Data.Segments[j].States,
				}
				break
			}
		}

		// Get base style (role reference)
		baseStyle := style.Style{}
		if segEntry != nil && segEntry.Style != "" {
			if roleStyle, ok := theme.Data.Roles[segEntry.Style]; ok {
				baseStyle = roleStyleToStyle(roleStyle)
			}
		}

		// Get state style override
		stateStyle := style.Style{}
		if segEntry != nil && seg.State != "" {
			if ss, ok := segEntry.States[seg.State]; ok {
				stateStyle = stateStyleToStyle(ss)
			}
		}

		// Merge styles: state overrides base, base falls back to plain
		mergedStyle := mergeStyles(baseStyle, stateStyle)

		// Get glyph for prompt_char state
		content := seg.Content
		if seg.Name == "prompt_char" && segEntry != nil {
			if ss, ok := segEntry.States[seg.State]; ok {
				if glyph := renderer.Glyph(map[style.GlyphTier]string{
					style.GlyphTierNerd:      ss.Glyphs["nerd"],
					style.GlyphTierPowerline: ss.Glyphs["powerline"],
					style.GlyphTierAscii:     ss.Glyphs["ascii"],
				}); glyph != "" {
					content = glyph
				}
			}
		}

		// Get separator for this segment
		separator := getSeparator(seg.Name, theme, profile, i, len(segments))

		displayWidth := style.DisplayWidth(content)

		result = append(result, RenderedSegment{
			Name:         seg.Name,
			Content:      content,
			State:        seg.State,
			Style:        mergedStyle,
			Separator:    separator,
			Always:       seg.Always,
			DisplayWidth: displayWidth,
		})
	}

	return result
}

// collapseSegments applies collapsing rules: empty content collapses segment and adjacent separators.
func collapseSegments(segments []RenderedSegment) []RenderedSegment {
	if len(segments) == 0 {
		return segments
	}

	// First pass: mark segments with empty content (and not always) as collapsed
	collapsed := make([]bool, len(segments))
	for i := range segments {
		if segments[i].Content == "" && !segments[i].Always {
			collapsed[i] = true
		}
	}

	// Second pass: collapse adjacent separators of collapsed segments
	// We do this by clearing the separator of the collapsed segment and the previous segment
	for i := range collapsed {
		if collapsed[i] {
			segments[i].Separator = ""
			if i > 0 {
				segments[i-1].Separator = ""
			}
		}
	}

	// Build result excluding collapsed segments, but keep their separator logic
	var result []RenderedSegment
	for i := range segments {
		if !collapsed[i] {
			result = append(result, segments[i])
		}
	}

	// Ensure no leading/trailing separators
	if len(result) > 0 {
		// Remove trailing separator from last segment
		result[len(result)-1].Separator = ""
	}

// Ensure no double separators (shouldn't happen with our model, but verify)
	// Only collapse identical non-space separators (space is a gap, not a connector)
	for i := 1; i < len(result); i++ {
		if result[i-1].Separator != "" && result[i].Separator != "" && result[i-1].Separator == result[i].Separator && result[i-1].Separator != " " {
			// This would be a double connector, clear the first one
			result[i-1].Separator = ""
		}
	}

	return result
}

// getSeparator returns the separator string for a segment.
func getSeparator(segName string, theme *theme.Theme, profile *style.RenderProfile, index, total int) string {
	// Last segment has no separator
	if index == total-1 {
		return ""
	}

	// Check for per-segment override
	if theme.Data.Separators.Overrides != nil {
		if sep, ok := theme.Data.Separators.Overrides[segName]; ok {
			return sep
		}
	}

	// Use theme default for the glyph tier
	if theme.Data.Separators.Default != nil {
		tierKey := profile.GlyphTier.String()
		if sep, ok := theme.Data.Separators.Default[tierKey]; ok {
			return sep
		}
		// Fallback to ascii
		if sep, ok := theme.Data.Separators.Default["ascii"]; ok {
			return sep
		}
	}

	return ""
}

// buildBarLines builds the bar lines from rendered segments.
func buildBarLines(
	segments []RenderedSegment,
	theme *theme.Theme,
	profile *style.RenderProfile,
) []string {
	if len(segments) == 0 {
		return []string{""}
	}

	renderer := style.NewRenderer(profile)
	var sb strings.Builder

	for _, seg := range segments {
		// Render segment content with style
		styled := renderer.Render(seg.Style, seg.Content)
		sb.WriteString(styled)

		// Render separator
		if seg.Separator != "" {
			// For Classic theme with connectors, the separator is a connector glyph
			// that connects this segment's background to the next segment's background
			sb.WriteString(seg.Separator)
		}
	}

	return []string{sb.String()}
}

// splitBar splits the bar at the prompt_char segment.
func splitBar(
	bar string,
	segments []RenderedSegment,
	theme *theme.Theme,
	profile *style.RenderProfile,
) []string {
	// Find the prompt_char segment
	promptCharIdx := -1
	for i, seg := range segments {
		if seg.Name == "prompt_char" {
			promptCharIdx = i
			break
		}
	}

	if promptCharIdx == -1 {
		// No prompt_char, return as single line
		return []string{bar}
	}

	renderer := style.NewRenderer(profile)

	// Build line 1: everything before prompt_char
	var line1 strings.Builder
	for i := 0; i < promptCharIdx; i++ {
		seg := segments[i]
		styled := renderer.Render(seg.Style, seg.Content)
		line1.WriteString(styled)
		if seg.Separator != "" {
			line1.WriteString(seg.Separator)
		}
	}

	// Build line 2: prompt_char with its content
	var line2 strings.Builder
	if promptCharIdx < len(segments) {
		seg := segments[promptCharIdx]
		styled := renderer.Render(seg.Style, seg.Content)
		line2.WriteString(styled)
	}

	return []string{line1.String(), line2.String()}
}

// displayWidth returns the display width of a string with ANSI codes stripped.
func displayWidth(s string) int {
	// Strip ANSI escape codes
	inEscape := false
	var clean strings.Builder
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == 'm' {
				inEscape = false
			}
			continue
		}
		clean.WriteRune(r)
	}
	return style.DisplayWidth(clean.String())
}

// roleStyleToStyle converts theme.RoleStyle to style.Style.
func roleStyleToStyle(rs theme.RoleStyle) style.Style {
	glyphs := make(map[style.GlyphTier]string)
	for k, v := range rs.Glyphs {
		switch k {
		case "nerd":
			glyphs[style.GlyphTierNerd] = v
		case "powerline":
			glyphs[style.GlyphTierPowerline] = v
		case "ascii":
			glyphs[style.GlyphTierAscii] = v
		}
	}
	return style.Style{
		Foreground: rs.Foreground,
		Background: rs.Background,
		Attributes: rs.Attributes,
		Glyphs:     glyphs,
	}
}

// stateStyleToStyle converts theme.StateStyle to style.Style.
func stateStyleToStyle(ss theme.StateStyle) style.Style {
	glyphs := make(map[style.GlyphTier]string)
	for k, v := range ss.Glyphs {
		switch k {
		case "nerd":
			glyphs[style.GlyphTierNerd] = v
		case "powerline":
			glyphs[style.GlyphTierPowerline] = v
		case "ascii":
			glyphs[style.GlyphTierAscii] = v
		}
	}
	return style.Style{
		Foreground: ss.Foreground,
		Background: ss.Background,
		Attributes: ss.Attributes,
		Glyphs:     glyphs,
	}
}

// mergeStyles merges base and state styles. State overrides base.
func mergeStyles(base, state style.Style) style.Style {
	result := base
	if state.Foreground != "" {
		result.Foreground = state.Foreground
	}
	if state.Background != "" {
		result.Background = state.Background
	}
	if len(state.Attributes) > 0 {
		result.Attributes = state.Attributes
	}
	if len(state.Glyphs) > 0 {
		if result.Glyphs == nil {
			result.Glyphs = state.Glyphs
		} else {
			for k, v := range state.Glyphs {
				result.Glyphs[k] = v
			}
		}
	}
	return result
}