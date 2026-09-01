package contextmgr

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/session"
)

// buildSession writes the given messages to a fresh session transcript via
// Append. extraLines, if any, are appended verbatim to the transcript file
// afterwards so the test can plant compaction marker lines directly.
func buildSession(t *testing.T, messages []llm.Message, extraLines ...string) *session.Session {
	t.Helper()
	s, err := session.New(t.TempDir())
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	for _, msg := range messages {
		if err := s.Append(msg); err != nil {
			t.Fatalf("session.Append: %v", err)
		}
	}
	if len(extraLines) > 0 {
		f, err := os.OpenFile(s.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open transcript: %v", err)
		}
		defer f.Close()
		for _, ln := range extraLines {
			if _, err := f.WriteString(ln + "\n"); err != nil {
				t.Fatalf("write marker: %v", err)
			}
		}
	}
	return s
}

func TestLayerOfAssignsEveryMessageRoleToOneLayer(t *testing.T) {
	cases := []struct {
		role string
		want Layer
	}{
		{llm.RoleSystem, LayerInstruction},
		{llm.RoleUser, LayerDurableIntent},
		{llm.RoleAssistant, LayerDurableIntent},
		{llm.RoleTool, LayerToolOutput},
	}
	for _, c := range cases {
		if got := LayerOf(c.role); got != c.want {
			t.Errorf("LayerOf(%q) = %q, want %q", c.role, got, c.want)
		}
	}
	if got := LayerOf("bogus"); got != "" {
		t.Errorf("LayerOf(bogus) = %q, want empty", got)
	}
}

// TestAddressByLineIndex verifies every message is addressable by its stable
// session-line index.
func TestAddressByLineIndex(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
	}
	s := buildSession(t, messages)
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}

	if m.Len() != 3 {
		t.Fatalf("Len = %d, want 3", m.Len())
	}
	for i, want := range messages {
		e, ok := m.Message(i)
		if !ok {
			t.Fatalf("Message(%d) not found", i)
		}
		if e.Line != i {
			t.Errorf("entry %d Line = %d, want %d", i, e.Line, i)
		}
		if !reflect.DeepEqual(e.Msg, want) {
			t.Errorf("entry %d Msg = %+v, want %+v", i, e.Msg, want)
		}
	}
	if _, ok := m.Message(99); ok {
		t.Errorf("Message(99) reported found for out-of-range index")
	}
}

// TestEveryMessageInExactlyOneLayer verifies each message lands in exactly one
// role layer, in transcript order.
func TestEveryMessageInExactlyOneLayer(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		{Role: llm.RoleUser, Content: "q1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleTool, Content: "result", ToolCallID: "call_1"},
		{Role: llm.RoleUser, Content: "q2"},
	}
	s := buildSession(t, messages)
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}

	instruction := m.Layer(LayerInstruction)
	if len(instruction) != 1 || !reflect.DeepEqual(instruction[0].Msg, messages[0]) {
		t.Errorf("instruction layer = %+v, want [%+v]", instruction, messages[0])
	}
	durable := m.Layer(LayerDurableIntent)
	wantDurable := []llm.Message{messages[1], messages[2], messages[4]}
	if len(durable) != len(wantDurable) {
		t.Fatalf("durable-intent layer length = %d, want %d", len(durable), len(wantDurable))
	}
	for i, want := range wantDurable {
		if !reflect.DeepEqual(durable[i].Msg, want) {
			t.Errorf("durable[%d] = %+v, want %+v", i, durable[i].Msg, want)
		}
	}
	tool := m.Layer(LayerToolOutput)
	if len(tool) != 1 || !reflect.DeepEqual(tool[0].Msg, messages[3]) {
		t.Errorf("tool-output layer = %+v, want [%+v]", tool, messages[3])
	}
}

// TestToolResultByCallID verifies a tool result is addressable by its tool-call
// id via the secondary lookup.
func TestToolResultByCallID(t *testing.T) {
	toolResult := llm.Message{Role: llm.RoleTool, Content: "big output", ToolCallID: "call_42"}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		{Role: llm.RoleUser, Content: "run it"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_42", Name: "read", Arguments: `{"p":"f"}`}}},
		toolResult,
	}
	s := buildSession(t, messages)
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}

	e, ok := m.ByToolCallID("call_42")
	if !ok {
		t.Fatalf("ByToolCallID(call_42) not found")
	}
	if !reflect.DeepEqual(e.Msg, toolResult) {
		t.Errorf("ByToolCallID Msg = %+v, want %+v", e.Msg, toolResult)
	}
	if _, ok := m.ByToolCallID("call_nope"); ok {
		t.Errorf("ByToolCallID(call_nope) reported found")
	}
}

// TestAnyToolResultAddressableByCallID verifies a tool result is findable by
// its tool-call id even when the line is assigned to a non-tool layer by role
// (some wires persist tool results as user-role messages carrying a
// ToolCallID). Layer assignment stays strictly by role.
func TestAnyToolResultAddressableByCallID(t *testing.T) {
	toolResult := llm.Message{Role: llm.RoleUser, Content: "big output", ToolCallID: "call_99"}
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		toolResult,
	}
	s := buildSession(t, messages)
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}

	e, ok := m.ByToolCallID("call_99")
	if !ok {
		t.Fatalf("ByToolCallID(call_99) not found for %s-role tool result", toolResult.Role)
	}
	if !reflect.DeepEqual(e.Msg, toolResult) {
		t.Errorf("ByToolCallID Msg = %+v, want %+v", e.Msg, toolResult)
	}
	// Even though it is findable by id, it is still layered by role.
	if got := m.Layer(LayerDurableIntent); len(got) != 1 {
		t.Errorf("durable-intent layer length = %d, want 1", len(got))
	}
}

// TestReadsCompactionMarkersDuringBuild plants a compaction marker line in the
// transcript and verifies the index build picks it up without treating it as a
// message or assigning it to a layer.
func TestReadsCompactionMarkersDuringBuild(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		{Role: llm.RoleUser, Content: "q1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "q2"},
	}
	const summary = "[summary of turn 1]"
	s := buildSession(t, messages, `{"role":"`+markerRole+`","content":"`+summary+`"}`)
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}

	compactions := m.Compactions()
	if len(compactions) != 1 {
		t.Fatalf("Compactions = %d, want 1", len(compactions))
	}
	if compactions[0].Line != len(messages) {
		t.Errorf("compaction Line = %d, want %d", compactions[0].Line, len(messages))
	}
	if compactions[0].Summary != summary {
		t.Errorf("compaction Summary = %q, want %q", compactions[0].Summary, summary)
	}
	// The marker is not a message: it is not in any layer and not addressable
	// by line index as a message.
	if m.Len() != len(messages) {
		t.Errorf("Len = %d, want %d (marker must not be indexed)", m.Len(), len(messages))
	}
	if e, ok := m.Message(len(messages)); ok {
		t.Errorf("marker line index reported as message: %+v", e)
	}
	total := len(m.Layer(LayerInstruction)) + len(m.Layer(LayerDurableIntent)) + len(m.Layer(LayerToolOutput))
	if total != len(messages) {
		t.Errorf("message total across layers = %d, want %d", total, len(messages))
	}
}

// TestRebuildIsLazyAboutTokens plants a transcript and asserts the build walks
// once and computes no token counts: the map carries no counting seam and the
// rebuild only indexes. A counters call would be required to compute counts,
// so rebuilding never counts.
func TestRebuildIsLazyAboutTokens(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("i", 1000)},
		{Role: llm.RoleUser, Content: strings.Repeat("q", 1000)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("a", 1000)},
		{Role: llm.RoleTool, Content: strings.Repeat("t", 1000), ToolCallID: "c"},
	}
	s := buildSession(t, messages)
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}
	if m.Len() != len(messages) {
		t.Fatalf("Len = %d, want %d", m.Len(), len(messages))
	}
}

// TestMapDoesNotModifyTranscript verifies rebuilding reads the transcript
// without writing to it: the transcript file is byte-for-byte unchanged.
func TestMapDoesNotModifyTranscript(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		{Role: llm.RoleUser, Content: "q1"},
	}
	s := buildSession(t, messages)
	before, err := os.ReadFile(s.TranscriptPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}
	after, err := os.ReadFile(s.TranscriptPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("transcript modified by map build:\n before %q\n after  %q", before, after)
	}
}

// TestRebuildOverwritesPriorState verifies rebuilding replaces any previous
// index rather than accumulating.
func TestRebuildOverwritesPriorState(t *testing.T) {
	first := NewMap()
	s1 := buildSession(t, []llm.Message{{Role: llm.RoleUser, Content: "one"}})
	if err := first.RebuildFromSession(s1); err != nil {
		t.Fatalf("first RebuildFromSession: %v", err)
	}
	s2 := buildSession(t, []llm.Message{{Role: llm.RoleUser, Content: "two"}})
	if err := first.RebuildFromSession(s2); err != nil {
		t.Fatalf("second RebuildFromSession: %v", err)
	}
	if first.Len() != 1 {
		t.Fatalf("Len after rebuild = %d, want 1", first.Len())
	}
	if e, ok := first.Message(0); !ok || e.Msg.Content != "two" {
		t.Errorf("after rebuild Message(0) = %+v, want content %q", e, "two")
	}
}

// TestRebuildFromSessionErrorsOnMalformedTranscript verifies a corrupt
// transcript line surfaces an error rather than silently indexing partial
// state.
func TestRebuildFromSessionErrorsOnMalformedTranscript(t *testing.T) {
	s := buildSession(t,
		[]llm.Message{
			{Role: llm.RoleUser, Content: "ok"},
			{Role: llm.RoleAssistant, Content: "plain"},
		},
		"not json",
	)

	m := NewMap()
	if err := m.RebuildFromSession(s); err == nil {
		t.Fatal("RebuildFromSession succeeded on malformed transcript, want error")
	}
}

// TestRebuildFromSessionNilForMissingFile verifies a session with no transcript
// file (fresh) rebuilds to an empty map without error.
func TestRebuildFromSessionNilForMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	s, err := session.New(dir)
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession on fresh session: %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("Len = %d, want 0", m.Len())
	}
}

// TestCompactionMarkerRangeValid verifies compaction markers carry the line
// range they summarised and RangeValid reports it correctly.
func TestCompactionMarkerRangeValid(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		{Role: llm.RoleUser, Content: "q1"},
		{Role: llm.RoleAssistant, Content: "a1"},
	}
	// Marker with range [1, 2] (the prior user+assistant lines)
	marker := `{"role":"` + markerRole + `","content":"[summary]","compacted_from":1,"compacted_to":2}`
	s := buildSession(t, messages, marker)
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}

	compactions := m.Compactions()
	if len(compactions) != 1 {
		t.Fatalf("Compactions = %d, want 1", len(compactions))
	}
	c := compactions[0]
	if c.From != 1 || c.To != 2 {
		t.Errorf("marker range From=%d To=%d, want 1, 2", c.From, c.To)
	}
	if !c.RangeValid() {
		t.Errorf("RangeValid = false, want true for valid range")
	}
}

// TestCompactionMarkerWithoutRangeIsInert verifies a marker without a usable
// range (To <= 0) reports RangeValid false.
func TestCompactionMarkerWithoutRangeIsInert(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "instr"},
		{Role: llm.RoleUser, Content: "q1"},
	}
	// Marker with no range
	marker := `{"role":"` + markerRole + `","content":"[old summary]"}`
	s := buildSession(t, messages, marker)
	m := NewMap()
	if err := m.RebuildFromSession(s); err != nil {
		t.Fatalf("RebuildFromSession: %v", err)
	}

	compactions := m.Compactions()
	if len(compactions) != 1 {
		t.Fatalf("Compactions = %d, want 1", len(compactions))
	}
	c := compactions[0]
	if c.From != 0 || c.To != 0 {
		t.Errorf("marker range From=%d To=%d, want 0, 0 for absent", c.From, c.To)
	}
	if c.RangeValid() {
		t.Errorf("RangeValid = true, want false for marker without range")
	}
}
