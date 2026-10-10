package prompt

import (
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/theme"
)

func TestRenderBar_Basic(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects", State: "anchored"},
		{Name: "provider", Content: "openai"},
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "provider"},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": " ",
	}

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     80,
	}

	lines, split := RenderBar(segments, th, profile, 80)
	if split {
		t.Error("expected no split")
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "~/projects") {
		t.Errorf("line missing dir content: %q", lines[0])
	}
	if !strings.Contains(lines[0], "openai") {
		t.Errorf("line missing provider content: %q", lines[0])
	}
	if !strings.Contains(lines[0], ">") {
		t.Errorf("line missing prompt_char: %q", lines[0])
	}
}

func TestRenderBar_CollapsesEmptySegments(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects", State: "anchored"},
		{Name: "agent", Content: "", State: ""}, // empty, no agent
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "agent"},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": " ",
	}

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     80,
	}

	lines, split := RenderBar(segments, th, profile, 80)
	if split {
		t.Error("expected no split")
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
	// Empty agent segment should be collapsed, so no double space
	if strings.Contains(lines[0], "  ") {
		t.Errorf("line has double space from collapsed segment: %q", lines[0])
	}
}

func TestRenderBar_AlwaysSegmentsNotCollapsed(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects", State: "anchored"},
		{Name: "agent", Content: "", State: "", Always: true}, // empty but always
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "agent", Options: map[string]string{"always": "true"}},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": " ",
	}

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     80,
	}

	lines, split := RenderBar(segments, th, profile, 80)
	if split {
		t.Error("expected no split")
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
	// Always segment should not be collapsed, so it should still have separator
	if !strings.Contains(lines[0], "  ") {
		t.Errorf("line missing separator from always segment: %q", lines[0])
	}
}

func TestRenderBar_SplitAtThreshold(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects/very/long/path/that/exceeds/threshold", State: "shortened"},
		{Name: "provider", Content: "openai"},
		{Name: "model", Content: "gpt-4o"},
		{Name: "agent", Content: "coder"},
		{Name: "session", Content: "abc123"},
		{Name: "time", Content: "12:34:56"},
		{Name: "tokens", Content: "12k"},
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "provider"},
		{Name: "model"},
		{Name: "agent"},
		{Name: "session"},
		{Name: "time"},
		{Name: "tokens"},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": " ",
	}
	th.Data.Prompt.SplitThreshold = 50

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     80,
	}

	lines, split := RenderBar(segments, th, profile, 80)
	// Bar width should exceed 50% of 80 = 40, so it should split
	if !split {
		t.Error("expected split at threshold")
	}
	if len(lines) != 2 {
		t.Errorf("expected 2 lines after split, got %d", len(lines))
	}
	// First line should have everything except prompt_char
	if strings.Contains(lines[0], ">") {
		t.Errorf("first line should not contain prompt_char: %q", lines[0])
	}
	// Second line should have prompt_char
	if !strings.Contains(lines[1], ">") {
		t.Errorf("second line should contain prompt_char: %q", lines[1])
	}
}

func TestRenderBar_NoSplitAtUnknownWidth(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects", State: "anchored"},
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": " ",
	}
	th.Data.Prompt.SplitThreshold = 50

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     0, // unknown width
	}

	lines, split := RenderBar(segments, th, profile, 0)
	if split {
		t.Error("expected no split with unknown width")
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
}

func TestRenderBar_SplitPast100Percent(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects", State: "anchored"},
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": " ",
	}
	th.Data.Prompt.SplitThreshold = 150 // > 100%

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     80,
	}

	_, split := RenderBar(segments, th, profile, 80)
	// Even at >100%, the bar should split if it exceeds the threshold
	// Since bar width is small, it won't exceed 150% of 80 = 120
	if split {
		t.Error("expected no split at >100% threshold")
	}
}

func TestRenderBar_CustomSeparators(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects", State: "anchored"},
		{Name: "provider", Content: "openai"},
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "provider"},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": "|",
	}
	th.Data.Separators.Overrides = map[string]string{
		"dir": "::",
	}

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     80,
	}

	lines, _ := RenderBar(segments, th, profile, 80)
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
	// dir should have :: separator
	if !strings.Contains(lines[0], "~/projects::") {
		t.Errorf("dir separator should be :: : %q", lines[0])
	}
	// provider should have default | separator
	if !strings.Contains(lines[0], "openai|") {
		t.Errorf("provider separator should be | : %q", lines[0])
	}
	// prompt_char should have no separator
	if strings.HasSuffix(strings.TrimSpace(lines[0]), "|") || strings.HasSuffix(strings.TrimSpace(lines[0]), "::") {
		t.Errorf("prompt_char should not have trailing separator: %q", lines[0])
	}
}

func TestRenderBar_NoLeadingTrailingSeparators(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects", State: "anchored"},
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": " ",
	}

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     80,
	}

	lines, _ := RenderBar(segments, th, profile, 80)
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
	// No leading separator
	if strings.HasPrefix(lines[0], " ") {
		t.Errorf("line should not have leading separator: %q", lines[0])
	}
	// No trailing separator (prompt_char should not have one)
	if strings.HasSuffix(strings.TrimSpace(lines[0]), " ") {
		t.Errorf("line should not have trailing separator: %q", lines[0])
	}
}

func TestRenderBar_NoDoubleSeparators(t *testing.T) {
	segments := []SegmentContent{
		{Name: "dir", Content: "~/projects", State: "anchored"},
		{Name: "provider", Content: "openai"},
		{Name: "prompt_char", Content: ">", State: "ok"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir"},
		{Name: "provider"},
		{Name: "prompt_char"},
	}
	th.Data.Separators.Default = map[string]string{
		"ascii": " ",
	}

	profile := &style.RenderProfile{
		GlyphTier: style.GlyphTierAscii,
		Width:     80,
	}

	lines, _ := RenderBar(segments, th, profile, 80)
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
	// No double spaces
	if strings.Contains(lines[0], "  ") {
		t.Errorf("line should not have double separators: %q", lines[0])
	}
}