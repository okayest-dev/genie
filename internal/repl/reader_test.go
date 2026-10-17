package repl

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/prompt"
	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/theme"
)

// promptCharSegment is a minimal prompt_char theme entry whose ascii glyph is
// ">", enough for the bar renderer to emit a visible prompt.
func promptCharSegment() theme.SegmentEntry {
	return theme.SegmentEntry{
		Name: "prompt_char",
		States: map[string]theme.StateStyle{
			"ok": {Glyphs: map[string]string{"ascii": ">"}},
		},
	}
}

func TestStdinReader(t *testing.T) {
	if got := stdinReader(nil); got != nil {
		t.Errorf("stdinReader(nil) = %v, want nil", got)
	}

	buf := bytes.NewBufferString("hi")
	rc := stdinReader(buf)
	if rc == nil {
		t.Fatal("stdinReader(reader) = nil, want a ReadCloser")
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hi" {
		t.Errorf("read %q, want %q", got, "hi")
	}

	orig := io.NopCloser(strings.NewReader("x"))
	if got := stdinReader(orig); got != orig {
		t.Errorf("stdinReader(passthrough) = %v, want the original ReadCloser", got)
	}
}

func TestHistoryFile(t *testing.T) {
	cfg := &Config{SessionDir: "/sessions"}
	if got := cfg.historyFile(); got != "/sessions/history" {
		t.Errorf("historyFile = %q, want /sessions/history", got)
	}

	cfg = &Config{Cfg: testCfg("/cfg-sessions")}
	if got := cfg.historyFile(); got != "/cfg-sessions/history" {
		t.Errorf("historyFile = %q, want /cfg-sessions/history", got)
	}

	if got := (&Config{}).historyFile(); got != "" {
		t.Errorf("historyFile = %q, want empty with no session dir", got)
	}
}

func TestPromptTextDefault(t *testing.T) {
	cfg := &Config{}
	if got := cfg.promptText(); got != defaultPrompt {
		t.Errorf("promptText = %q, want %q", got, defaultPrompt)
	}
}

func TestPromptTextFallsBackWithoutHandle(t *testing.T) {
	cfg := &Config{PromptBar: &prompt.PromptBar{}}
	if got := cfg.promptText(); got != defaultPrompt {
		t.Errorf("promptText = %q, want %q", got, defaultPrompt)
	}
}

func TestPromptTextBarSingleLine(t *testing.T) {
	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{promptCharSegment()}
	profile := &style.RenderProfile{GlyphTier: style.GlyphTierAscii, Width: 80}

	var out bytes.Buffer
	cfg := &Config{
		Run:       newTestHandle(t, nil, "", t.TempDir()),
		Cfg:       testCfg(t.TempDir()),
		Theme:     th,
		PromptBar: prompt.NewPromptBar(th, profile, 80),
		Stdout:    &out,
	}

	got := cfg.promptText()
	if !strings.Contains(got, ">") {
		t.Errorf("promptText = %q, want the bar's prompt character", got)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want no preceding lines for a single-line bar", out.String())
	}
}

func TestPromptTextBarSplitPrintsPrecedingLines(t *testing.T) {
	th := &theme.Theme{}
	th.Data.Segments = []theme.SegmentEntry{{Name: "dir"}, promptCharSegment()}
	th.Data.Prompt.SplitThreshold = 1
	profile := &style.RenderProfile{GlyphTier: style.GlyphTierAscii, Width: 80}

	var out bytes.Buffer
	cfg := &Config{
		Run:       newTestHandle(t, nil, "", t.TempDir()),
		Cfg:       testCfg(t.TempDir()),
		Theme:     th,
		PromptBar: prompt.NewPromptBar(th, profile, 80),
		Stdout:    &out,
	}

	got := cfg.promptText()
	if !strings.Contains(got, ">") {
		t.Errorf("promptText = %q, want the prompt character on the final line", got)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("stdout = %q, want the preceding bar line printed with a newline", out.String())
	}
	if strings.Contains(out.String(), ">") {
		t.Errorf("stdout = %q, want the prompt character kept out of the printed lines", out.String())
	}
}
