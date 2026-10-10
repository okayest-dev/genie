package prompt

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/run"
	"github.com/okayest-dev/genie/internal/theme"
)

// SegmentContent holds the generated content and state for a prompt segment.
type SegmentContent struct {
	Name    string
	Content string
	State   string
	Always  bool
}

// Generator is a function that generates a segment's content from resident data.
type Generator func(*Context) SegmentContent

// Context holds all the resident data needed to generate prompt segments.
type Context struct {
	Cwd              string
	Provider         string
	Model            string
	Agent            *config.ResolvedAgent
	SessionID        string
	TotalTokens      int
	LastPromptStatus run.PromptStatus
	TimeFormat       string
	DirMaxLen        int
	SessionLen       int
}

// GenerateSegments generates content for all segments in the given order.
func GenerateSegments(ctx *Context, segmentNames []string, theme *theme.Theme) []SegmentContent {
	generators := map[string]Generator{
		"dir":         generateDir,
		"provider":    generateProvider,
		"model":       generateModel,
		"agent":       generateAgent,
		"session":     generateSession,
		"time":        generateTime,
		"tokens":      generateTokens,
		"prompt_char": generatePromptChar,
	}

	var segments []SegmentContent
	for _, name := range segmentNames {
		if gen, ok := generators[name]; ok {
			seg := gen(ctx)
			// Check if segment has "always" option in theme
			for _, segEntry := range theme.Data.Segments {
				if segEntry.Name == name {
					if segEntry.Options != nil {
						if alwaysStr, ok := segEntry.Options["always"]; ok {
							seg.Always = alwaysStr == "true"
						}
					}
					break
				}
			}
			segments = append(segments, seg)
		}
	}
	return segments
}

// generateDir generates the directory segment.
// $HOME shortened to ~, deep paths tail-truncated to max_len.
func generateDir(ctx *Context) SegmentContent {
	dir := ctx.Cwd
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(dir, home) {
		dir = "~" + dir[len(home):]
	}

	maxLen := ctx.DirMaxLen
	if maxLen <= 0 {
		maxLen = 40
	}

	state := "anchored"
	if len(dir) > maxLen {
		state = "shortened"
		// Tail-truncate: keep the last maxLen-3 chars and prepend "..."
		if maxLen > 3 {
			dir = "..." + dir[len(dir)-(maxLen-3):]
		} else {
			dir = dir[:maxLen]
		}
	}

	return SegmentContent{
		Name:    "dir",
		Content: dir,
		State:   state,
	}
}

// generateProvider generates the provider segment.
func generateProvider(ctx *Context) SegmentContent {
	return SegmentContent{
		Name:    "provider",
		Content: ctx.Provider,
		State:   "",
	}
}

// generateModel generates the model segment.
func generateModel(ctx *Context) SegmentContent {
	return SegmentContent{
		Name:    "model",
		Content: ctx.Model,
		State:   "",
	}
}

// generateAgent generates the agent segment.
// Collapses on the default flow (no agent active).
func generateAgent(ctx *Context) SegmentContent {
	if ctx.Agent == nil {
		return SegmentContent{
			Name:    "agent",
			Content: "",
			State:   "",
		}
	}
	return SegmentContent{
		Name:    "agent",
		Content: ctx.Agent.Name,
		State:   "",
	}
}

// generateSession generates the session segment.
// Shows trailing suffix only (after the last '-').
func generateSession(ctx *Context) SegmentContent {
	sessionID := ctx.SessionID
	// Extract trailing suffix after last '-'
	if idx := strings.LastIndex(sessionID, "-"); idx >= 0 && idx < len(sessionID)-1 {
		sessionID = sessionID[idx+1:]
	}

	sessionLen := ctx.SessionLen
	if sessionLen > 0 && len(sessionID) > sessionLen {
		sessionID = sessionID[:sessionLen]
	}

	return SegmentContent{
		Name:    "session",
		Content: sessionID,
		State:   "",
	}
}

// generateTime generates the time segment.
func generateTime(ctx *Context) SegmentContent {
	format := ctx.TimeFormat
	if format == "" {
		format = "15:04:05"
	}
	return SegmentContent{
		Name:    "time",
		Content: time.Now().Format(format),
		State:   "",
	}
}

// generateTokens generates the tokens segment.
// Human-compact format (e.g., 12k, 1.2M), renders 0 before first completed turn.
func generateTokens(ctx *Context) SegmentContent {
	return SegmentContent{
		Name:    "tokens",
		Content: formatTokens(ctx.TotalTokens),
		State:   "",
	}
}

// formatTokens formats token count in human-compact format (e.g., 12k, 1.2M, 0).
func formatTokens(n int) string {
	if n >= 1000000 {
		f := float64(n) / 1000000
		if f == float64(int(f)) {
			return fmt.Sprintf("%dM", int(f))
		}
		return fmt.Sprintf("%.1fM", f)
	}
	if n >= 1000 {
		f := float64(n) / 1000
		if f == float64(int(f)) {
			return fmt.Sprintf("%dk", int(f))
		}
		return fmt.Sprintf("%.1fk", f)
	}
	return fmt.Sprintf("%d", n)
}

// generatePromptChar generates the prompt_char segment.
// Carries the exit-status state: ok/error/cancelled.
func generatePromptChar(ctx *Context) SegmentContent {
	state := "ok"
	switch ctx.LastPromptStatus {
	case run.PromptStatusOK:
		state = "ok"
	case run.PromptStatusError:
		state = "error"
	case run.PromptStatusCancelled:
		state = "cancelled"
	default:
		state = "ok" // Default to ok for unknown/initial state
	}

	// The glyph is determined by the theme state style
	return SegmentContent{
		Name:    "prompt_char",
		Content: "", // Glyph comes from theme state
		State:   state,
	}
}