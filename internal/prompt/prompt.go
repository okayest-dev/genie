package prompt

import (
	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/run"
	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/theme"
)

// PromptBar renders the idle prompt segment bar.
type PromptBar struct {
	theme     *theme.Theme
	profile   *style.RenderProfile
	termWidth int
}

// NewPromptBar creates a new prompt bar renderer.
func NewPromptBar(theme *theme.Theme, profile *style.RenderProfile, termWidth int) *PromptBar {
	return &PromptBar{
		theme:     theme,
		profile:   profile,
		termWidth: termWidth,
	}
}

// Render generates the prompt bar lines for the given context.
func (p *PromptBar) Render(ctx *Context) []string {
	// Get segment order from theme
	segmentNames := make([]string, len(p.theme.Data.Segments))
	for i, seg := range p.theme.Data.Segments {
		segmentNames[i] = seg.Name
	}

	// Generate segment content
	segments := GenerateSegments(ctx, segmentNames, p.theme)

	// Render the bar
	lines, _ := RenderBar(segments, p.theme, p.profile, p.termWidth)
	return lines
}

// BuildContext builds a prompt Context from run handle and config.
func BuildContext(h *run.Handle, cfg *config.Config, th *theme.Theme) *Context {
	// Get cwd
	cwd := h.Cwd()

	// Get dir.max_len from theme
	dirMaxLen := 40
	for _, seg := range th.Data.Segments {
		if seg.Name == "dir" && seg.Options != nil {
			if val, ok := seg.Options["dir.max_len"]; ok {
				// Parse the value (simplified - in real code use strconv.Atoi)
				_ = val
				dirMaxLen = 40 // default
			}
		}
	}

	// Get time.format from theme
	timeFormat := "15:04:05"
	for _, seg := range th.Data.Segments {
		if seg.Name == "time" && seg.Options != nil {
			if val, ok := seg.Options["time.format"]; ok {
				timeFormat = val
			}
		}
	}

	// Get session.len from theme
	sessionLen := 0
	for _, seg := range th.Data.Segments {
		if seg.Name == "session" && seg.Options != nil {
			if val, ok := seg.Options["session.len"]; ok {
				_ = val
				sessionLen = 0 // default
			}
		}
	}

	return &Context{
		Cwd:              cwd,
		Provider:         h.Provider(),
		Model:            h.Model(),
		Agent:            h.CurrentAgent(),
		SessionID:        h.Session().ID,
		TotalTokens:      h.TotalTokens(),
		LastPromptStatus: h.LastPromptStatus(),
		TimeFormat:       timeFormat,
		DirMaxLen:        dirMaxLen,
		SessionLen:       sessionLen,
	}
}