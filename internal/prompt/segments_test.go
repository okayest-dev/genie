package prompt

import (
	"os"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/run"
	"github.com/okayest-dev/genie/internal/theme"
)

func TestGenerateDir_HomeShortened(t *testing.T) {
	home, _ := os.UserHomeDir()
	ctx := &Context{
		Cwd:       home + "/projects/genie",
		DirMaxLen: 40,
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir", Options: map[string]string{"dir.max_len": "40"}},
	}

	segments := GenerateSegments(ctx, []string{"dir"}, th)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]
	if seg.Name != "dir" {
		t.Errorf("segment name = %q, want dir", seg.Name)
	}
	if !strings.HasPrefix(seg.Content, "~") {
		t.Errorf("dir content = %q, want ~ prefix", seg.Content)
	}
	if seg.State != "anchored" {
		t.Errorf("state = %q, want anchored", seg.State)
	}
}

func TestGenerateDir_TailTruncated(t *testing.T) {
	ctx := &Context{
		Cwd:       "/very/long/path/that/exceeds/the/maximum/length/allowed",
		DirMaxLen: 40,
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "dir", Options: map[string]string{"dir.max_len": "40"}},
	}

	segments := GenerateSegments(ctx, []string{"dir"}, th)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]
	if seg.State != "shortened" {
		t.Errorf("state = %q, want shortened", seg.State)
	}
	if len(seg.Content) > 40 {
		t.Errorf("content length = %d, want <= 40", len(seg.Content))
	}
	if !strings.HasPrefix(seg.Content, "...") {
		t.Errorf("content = %q, want ... prefix", seg.Content)
	}
}

func TestGenerateProvider(t *testing.T) {
	ctx := &Context{
		Provider: "openai",
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "provider"},
	}

	segments := GenerateSegments(ctx, []string{"provider"}, th)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]
	if seg.Content != "openai" {
		t.Errorf("content = %q, want openai", seg.Content)
	}
}

func TestGenerateModel(t *testing.T) {
	ctx := &Context{
		Model: "gpt-4o",
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "model"},
	}

	segments := GenerateSegments(ctx, []string{"model"}, th)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]
	if seg.Content != "gpt-4o" {
		t.Errorf("content = %q, want gpt-4o", seg.Content)
	}
}

func TestGenerateAgent_WithAgent(t *testing.T) {
	ctx := &Context{
		Agent: &config.ResolvedAgent{Name: "coder"},
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "agent"},
	}

	segments := GenerateSegments(ctx, []string{"agent"}, th)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]
	if seg.Content != "coder" {
		t.Errorf("content = %q, want coder", seg.Content)
	}
}

func TestGenerateAgent_NoAgent(t *testing.T) {
	ctx := &Context{
		Agent: nil,
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "agent"},
	}

	segments := GenerateSegments(ctx, []string{"agent"}, th)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]
	if seg.Content != "" {
		t.Errorf("content = %q, want empty", seg.Content)
	}
}

func TestGenerateSession_TrailingSuffix(t *testing.T) {
	ctx := &Context{
		SessionID: "20260102-150405-abcdef12",
		SessionLen: 8,
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "session", Options: map[string]string{"session.len": "8"}},
	}

	segments := GenerateSegments(ctx, []string{"session"}, th)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]
	// Should show trailing suffix after last '-'
	if seg.Content != "abcdef12" {
		t.Errorf("content = %q, want abcdef12", seg.Content)
	}
}

func TestGenerateTime(t *testing.T) {
	ctx := &Context{
		TimeFormat: "15:04:05",
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "time", Options: map[string]string{"time.format": "15:04:05"}},
	}

	segments := GenerateSegments(ctx, []string{"time"}, th)
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}

	seg := segments[0]
	// Time should be in HH:MM:SS format
	if len(seg.Content) != 8 || seg.Content[2] != ':' || seg.Content[5] != ':' {
		t.Errorf("content = %q, want HH:MM:SS format", seg.Content)
	}
}

func TestGenerateTokens_CompactFormat(t *testing.T) {
	tests := []struct {
		name     string
		tokens   int
		expected string
	}{
		{"zero", 0, "0"},
		{"single", 5, "5"},
		{"hundred", 100, "100"},
		{"thousand", 1000, "1k"},
		{"thousand_point", 1500, "1.5k"},
		{"ten_thousand", 10000, "10k"},
		{"million", 1000000, "1M"},
		{"million_point", 1500000, "1.5M"},
		{"twelve_k", 12000, "12k"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &Context{
				TotalTokens: tt.tokens,
			}

			th := &theme.Theme{}
			th.Data.Segments = []theme.SegmentEntry{
				{Name: "tokens"},
			}

			segments := GenerateSegments(ctx, []string{"tokens"}, th)
			if len(segments) != 1 {
				t.Fatalf("expected 1 segment, got %d", len(segments))
			}

			seg := segments[0]
			if seg.Content != tt.expected {
				t.Errorf("tokens = %q, want %q", seg.Content, tt.expected)
			}
		})
	}
}

func TestGeneratePromptChar_States(t *testing.T) {
	tests := []struct {
		name      string
		status    run.PromptStatus
		expected  string
	}{
		{"ok", run.PromptStatusOK, "ok"},
		{"error", run.PromptStatusError, "error"},
		{"cancelled", run.PromptStatusCancelled, "cancelled"},
		{"unknown", run.PromptStatusUnknown, "ok"}, // defaults to ok
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &Context{
				LastPromptStatus: tt.status,
			}

			th := &theme.Theme{}
			th.Data.Segments = []theme.SegmentEntry{
				{Name: "prompt_char"},
			}

			segments := GenerateSegments(ctx, []string{"prompt_char"}, th)
			if len(segments) != 1 {
				t.Fatalf("expected 1 segment, got %d", len(segments))
			}

			seg := segments[0]
			if seg.State != tt.expected {
				t.Errorf("state = %q, want %q", seg.State, tt.expected)
			}
			if seg.Content != "" {
				t.Errorf("content = %q, want empty (glyph from theme)", seg.Content)
			}
		})
	}
}

func TestGenerateSegments_AlwaysOption(t *testing.T) {
	ctx := &Context{
		Provider: "openai",
		Agent:    nil, // agent will be empty
	}

	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{
		{Name: "provider", Options: map[string]string{"always": "true"}},
		{Name: "agent", Options: map[string]string{"always": "true"}},
	}

	segments := GenerateSegments(ctx, []string{"provider", "agent"}, th)
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}

	if !segments[0].Always {
		t.Error("provider should have always=true")
	}
	if !segments[1].Always {
		t.Error("agent should have always=true")
	}
}