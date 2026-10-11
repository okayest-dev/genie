package run

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/theme"
)

func displayProfile() *style.RenderProfile {
	return &style.RenderProfile{IsTTY: true, ColourProfile: style.ColourProfileTrueColor, GlyphTier: style.GlyphTierNerd, Width: 80}
}

func asciiProfile() *style.RenderProfile {
	return &style.RenderProfile{IsTTY: true, ColourProfile: style.ColourProfileAscii, GlyphTier: style.GlyphTierAscii, Width: 80}
}

func styledTurn(t *testing.T, th *theme.Theme, outProf, errProf *style.RenderProfile) (string, string) {
	t.Helper()
	src := twoProviderSource()
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: src,
		Provider:       "alpha",
		Stderr:         io.Discard,
		Theme:          th,
		StdoutProfile:  outProf,
		StderrProfile:  errProf,
	})
	var out, errOut bytes.Buffer
	if err := h.Turn(context.Background(), "hi", &out, &errOut); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	return out.String(), errOut.String()
}

func customTheme(roles map[string]theme.RoleStyle) *theme.Theme {
	return &theme.Theme{Data: theme.ThemeData{Roles: roles}}
}

func TestTurnModelRoleStyledOnDisplayProfile(t *testing.T) {
	th := customTheme(map[string]theme.RoleStyle{
		"model": {Foreground: "cyan"},
	})
	out, errOut := styledTurn(t, th, displayProfile(), nil)
	if !strings.Contains(out, "\x1b[36mfake reply") || !strings.Contains(out, "\x1b[0m") {
		t.Errorf("model stream should render through the cyan model role: got out = %q", out)
	}
	if !strings.Contains(out, "fake reply") {
		t.Errorf("model reply missing text: %q", out)
	}
	if strings.Contains(errOut, "\x1b[") || strings.Contains(errOut, "fake reply") {
		t.Errorf("stderr should stay plain without a stderr profile: %q", errOut)
	}
}

func TestTurnClassicModelRoleStaysPlain(t *testing.T) {
	th, err := theme.Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}
	out, _ := styledTurn(t, th, displayProfile(), nil)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("classic model role should stay plain: %q", out)
	}
	if out != "fake reply\n" {
		t.Errorf("classic turn output = %q, want byte-identical %q", out, "fake reply\n")
	}
}

func TestTurnModelRolePlainWithoutStyling(t *testing.T) {
	out, _ := styledTurn(t, nil, nil, nil)
	if out != "fake reply\n" {
		t.Errorf("unstyled turn output = %q, want byte-identical %q", out, "fake reply\n")
	}
}

func TestTurnAsciiProfileIsByteIdenticalToPlain(t *testing.T) {
	out, _ := styledTurn(t, nil, asciiProfile(), nil)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ascii profile leaked escape bytes: %q", out)
	}
	if out != "fake reply\n" {
		t.Errorf("ascii turn output = %q, want byte-identical %q", out, "fake reply\n")
	}
}

// stagedClient scripts one distinct stream per invocation, so the harness can
// drive a realistic tool-call-then-reply round trip without looping.
type stagedClient struct {
	streams [][]llm.Event
	n       int
}

func (c *stagedClient) ListModels(context.Context) ([]llm.Model, error) { return nil, nil }

func (c *stagedClient) Stream(_ context.Context, _ llm.Request) (iter.Seq[llm.Event], error) {
	if c.n >= len(c.streams) {
		return nil, io.ErrUnexpectedEOF
	}
	evs := c.streams[c.n]
	c.n++
	return func(yield func(llm.Event) bool) {
		for _, e := range evs {
			if !yield(e) {
				return
			}
		}
	}, nil
}

type stagedSource struct {
	client llm.Client
}

func (s *stagedSource) Names() []string { return []string{"alpha"} }

func (s *stagedSource) DefaultModel(name string) (string, error) {
	return "alpha-model", nil
}

func (s *stagedSource) Client(name string) (llm.Client, error) {
	if name != "alpha" {
		return nil, io.ErrUnexpectedEOF
	}
	return s.client, nil
}

func (s *stagedSource) Catalog(context.Context, string) ([]llm.Model, error) {
	return []llm.Model{{ID: "alpha-model"}}, nil
}

func stagingTurn(t *testing.T, th *theme.Theme, outProf, errProf *style.RenderProfile) (string, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("secret line"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]string{"path": file})
	if err != nil {
		t.Fatal(err)
	}
	client := &stagedClient{streams: [][]llm.Event{
		{
			{Kind: llm.EventToolCall, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Arguments: string(args)}}},
			{Kind: llm.EventFinish, End: llm.FinishToolCalls},
		},
		{{Kind: llm.EventText, Text: "done"}, {Kind: llm.EventFinish, End: llm.FinishStop}},
	}}
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: &stagedSource{client: client},
		Provider:       "alpha",
		Stderr:         io.Discard,
		Theme:          th,
		StdoutProfile:  outProf,
		StderrProfile:  errProf,
	})
	h.SetNegotiator(&recNegotiator{resp: permissions.ResponseSession})
	var out, errOut bytes.Buffer
	if err := h.Turn(context.Background(), "read my notes", &out, &errOut); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	return out.String(), errOut.String()
}

func TestTurnToolFrameRoleStyled(t *testing.T) {
	th, err := theme.Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}
	out, errOut := stagingTurn(t, th, displayProfile(), displayProfile())
	if !strings.Contains(errOut, "\x1b[36m── read ") || !strings.Contains(errOut, "── read ") {
		t.Errorf("tool frame should render through the cyan tool role: stderr = %q", errOut)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("model reply missing: out = %q", out)
	}
}

func TestTurnToolFramePlainWithoutStderrProfile(t *testing.T) {
	th, err := theme.Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}
	out, errOut := stagingTurn(t, th, displayProfile(), nil)
	if strings.Contains(errOut, "\x1b[") {
		t.Errorf("tool frame should stay plain without a stderr profile: %q", errOut)
	}
	if !strings.Contains(errOut, "── read ") {
		t.Errorf("tool frame missing from stderr: %q", errOut)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("reply missing: %q", out)
	}
}

func TestTurnToolFrameAsciiDropsColour(t *testing.T) {
	th, err := theme.Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}
	_, errOut := stagingTurn(t, th, nil, asciiProfile())
	if strings.Contains(errOut, "\x1b[") {
		t.Errorf("ascii stderr leaked escape bytes: %q", errOut)
	}
	if !strings.Contains(errOut, "── read ") {
		t.Errorf("tool frame missing from stderr: %q", errOut)
	}
}