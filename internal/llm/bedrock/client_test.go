package bedrock

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"

	"github.com/okayest-dev/genie/internal/llm"
)

// ---------- fakes ----------

// fakeConverser is a converseStreamer that records the request and serves a
// scripted stream handle.
type fakeConverser struct {
	handle *streamHandle
	err    error
	input  *bedrockruntime.ConverseStreamInput
}

func (f *fakeConverser) ConverseStream(_ context.Context, params *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (*streamHandle, error) {
	f.input = params
	if f.err != nil {
		return nil, f.err
	}
	return f.handle, nil
}

// scripted returns a client whose fake converser serves the given events,
// optionally ending in a stream error.
func scripted(c *Client, f *fakeConverser, events []types.ConverseStreamOutput, streamErr error) {
	ch := make(chan types.ConverseStreamOutput, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	f.handle = &streamHandle{
		events: ch,
		err:    func() error { return streamErr },
		close:  func() error { return nil },
	}
	c.converse = f
}

func collect(t *testing.T, seq iter.Seq[llm.Event]) []llm.Event {
	t.Helper()
	var got []llm.Event
	for ev := range seq {
		got = append(got, ev)
	}
	return got
}

// ---------- event builders ----------

func textDelta(idx int32, text string) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberContentBlockDelta{
		Value: types.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(idx),
			Delta:             &types.ContentBlockDeltaMemberText{Value: text},
		},
	}
}

func toolUseStart(idx int32, id, name string) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberContentBlockStart{
		Value: types.ContentBlockStartEvent{
			ContentBlockIndex: aws.Int32(idx),
			Start: &types.ContentBlockStartMemberToolUse{Value: types.ToolUseBlockStart{
				ToolUseId: aws.String(id),
				Name:      aws.String(name),
			}},
		},
	}
}

func toolUseDelta(idx int32, input string) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberContentBlockDelta{
		Value: types.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(idx),
			Delta:             &types.ContentBlockDeltaMemberToolUse{Value: types.ToolUseBlockDelta{Input: aws.String(input)}},
		},
	}
}

func blockStop(idx int32) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberContentBlockStop{
		Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(idx)},
	}
}

func messageStop(reason types.StopReason) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberMessageStop{
		Value: types.MessageStopEvent{StopReason: reason},
	}
}

func metadata(in, out, total int32) types.ConverseStreamOutput {
	return &types.ConverseStreamOutputMemberMetadata{
		Value: types.ConverseStreamMetadataEvent{
			Usage: &types.TokenUsage{
				InputTokens:  aws.Int32(in),
				OutputTokens: aws.Int32(out),
				TotalTokens:  aws.Int32(total),
			},
		},
	}
}

// ---------- Stream ----------

func TestStreamTextFinishUsage(t *testing.T) {
	c := &Client{}
	fc := &fakeConverser{}
	scripted(c, fc, []types.ConverseStreamOutput{
		textDelta(0, "hel"),
		textDelta(0, "lo"),
		messageStop(types.StopReasonEndTurn),
		metadata(3, 5, 8),
	}, nil)

	seq, err := c.Stream(context.Background(), llm.Request{Model: "anthropic.claude-sonnet-4-6"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, seq)

	var kinds []llm.EventKind
	var text string
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == llm.EventText {
			text += ev.Text
		}
	}
	if want := []llm.EventKind{llm.EventText, llm.EventText, llm.EventFinish, llm.EventUsage}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	if text != "hello" {
		t.Errorf("text = %q, want hello", text)
	}
	if events[2].End != llm.FinishStop {
		t.Errorf("finish reason = %q, want stop", events[2].End)
	}
	if want := (llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}); events[3].Usage != want {
		t.Errorf("usage = %+v, want %+v", events[3].Usage, want)
	}
}

func TestStreamToolCall(t *testing.T) {
	c := &Client{}
	fc := &fakeConverser{}
	scripted(c, fc, []types.ConverseStreamOutput{
		toolUseStart(0, "toolu_1", "get_weather"),
		toolUseDelta(0, `{"city":`),
		toolUseDelta(0, `"Boston"}`),
		blockStop(0),
		messageStop(types.StopReasonToolUse),
		metadata(2, 4, 6),
	}, nil)

	seq, err := c.Stream(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, seq)

	if events[0].Kind != llm.EventToolCall {
		t.Fatalf("event 0 kind = %v, want tool call", events[0].Kind)
	}
	tc := events[0].ToolCalls
	if len(tc) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(tc))
	}
	if tc[0].ID != "toolu_1" || tc[0].Name != "get_weather" || tc[0].Arguments != `{"city":"Boston"}` {
		t.Errorf("tool call = %+v, want {toolu_1 get_weather {\"city\":\"Boston\"}}", tc[0])
	}
	if events[1].Kind != llm.EventFinish || events[1].End != llm.FinishToolCalls {
		t.Errorf("event 1 = %+v, want finish tool_calls", events[1])
	}
	if events[2].Usage != (llm.Usage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6}) {
		t.Errorf("usage = %+v", events[2].Usage)
	}
}

func TestStreamInterleavedTextAndToolCall(t *testing.T) {
	c := &Client{}
	fc := &fakeConverser{}
	scripted(c, fc, []types.ConverseStreamOutput{
		textDelta(0, "Let me check"),
		toolUseStart(1, "t1", "bash"),
		toolUseDelta(1, `{"cmd":`),
		toolUseDelta(1, `"ls"}`),
		blockStop(1),
		textDelta(0, " done"),
		messageStop(types.StopReasonToolUse),
	}, nil)

	seq, err := c.Stream(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, seq)

	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (text, tool call, text, finish)", len(events))
	}
	if events[0].Kind != llm.EventText || events[0].Text != "Let me check" {
		t.Errorf("events[0] = %+v", events[0])
	}
	if events[1].Kind != llm.EventToolCall || events[1].ToolCalls[0].Arguments != `{"cmd":"ls"}` {
		t.Errorf("events[1] = %+v", events[1])
	}
	if events[2].Text != " done" {
		t.Errorf("events[2] = %+v", events[2])
	}
	if events[3].End != llm.FinishToolCalls {
		t.Errorf("events[3] = %+v", events[3])
	}
}

func TestStreamMidStreamError(t *testing.T) {
	c := &Client{}
	fc := &fakeConverser{}
	scripted(c, fc, nil, &types.ThrottlingException{Message: aws.String("slow down")})

	seq, err := c.Stream(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, seq)
	if len(events) != 1 || events[0].Kind != llm.EventError {
		t.Fatalf("events = %+v, want one error event", events)
	}
	var pe *llm.ProviderError
	if !errors.As(events[0].Err, &pe) || pe.Kind != llm.KindRateLimit {
		t.Errorf("error = %v, want rate_limit", events[0].Err)
	}
}

func TestStreamOpenError(t *testing.T) {
	c := &Client{converse: &fakeConverser{err: &types.AccessDeniedException{Message: aws.String("nope")}}}
	_, err := c.Stream(context.Background(), llm.Request{Model: "m"})
	var pe *llm.ProviderError
	if !errors.As(err, &pe) || pe.Kind != llm.KindAuth {
		t.Errorf("err = %v, want auth ProviderError", err)
	}
}

func TestStreamInitErr(t *testing.T) {
	c := &Client{initErr: errors.New("failed to get shared config profile")}
	_, err := c.Stream(context.Background(), llm.Request{Model: "m"})
	var pe *llm.ProviderError
	if !errors.As(err, &pe) || pe.Kind != llm.KindAuth {
		t.Errorf("err = %v, want auth ProviderError", err)
	}
	if pe.Message == "" {
		t.Error("ProviderError message should be informative")
	}
}

func TestStreamRequestShape(t *testing.T) {
	c := &Client{}
	fc := &fakeConverser{}
	scripted(c, fc, []types.ConverseStreamOutput{messageStop(types.StopReasonEndTurn)}, nil)

	req := llm.Request{
		Model: "anthropic.claude-sonnet-4-6",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are concise."},
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleUser, Content: "the tool said X", ToolCallID: "toolu_9"},
			{Role: llm.RoleAssistant, Content: "padding", ToolCalls: []llm.ToolCall{{ID: "toolu_9", Name: "get_weather", Arguments: `{"city":"Boston"}`}}},
		},
		Tools: []llm.ToolDef{
			{Name: "get_weather", Description: "Weather for a city", Parameters: map[string]any{"type": "object"}},
		},
	}
	seq, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	collect(t, seq)

	in := fc.input
	if in == nil {
		t.Fatal("no ConverseStreamInput captured")
	}
	if got := aws.ToString(in.ModelId); got != req.Model {
		t.Errorf("ModelId = %q, want %q", got, req.Model)
	}
	if len(in.System) != 1 {
		t.Fatalf("System blocks = %d, want 1", len(in.System))
	}
	if s, ok := in.System[0].(*types.SystemContentBlockMemberText); !ok || s.Value != "You are concise." {
		t.Errorf("system = %#v, want text block", in.System[0])
	}
	if in.InferenceConfig == nil || aws.ToInt32(in.InferenceConfig.MaxTokens) != 4096 {
		t.Error("InferenceConfig MaxTokens = missing; want 4096")
	}
	if len(in.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (user, tool result, assistant w/ tool call)", len(in.Messages))
	}
	if in.Messages[0].Role != types.ConversationRoleUser {
		t.Errorf("messages[0].Role = %v, want user", in.Messages[0].Role)
	}
	if _, ok := in.Messages[1].Content[0].(*types.ContentBlockMemberToolResult); !ok {
		t.Errorf("messages[1] = %#v, want tool_result block", in.Messages[1].Content[0])
	}
	asst := in.Messages[2]
	if asst.Role != types.ConversationRoleAssistant || len(asst.Content) != 2 {
		t.Fatalf("asst = %#v, want assistant with 2 blocks", asst)
	}
	tu, ok := asst.Content[1].(*types.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("asst.Content[1] = %#v, want tool_use block", asst.Content[1])
	}
	if aws.ToString(tu.Value.Name) != "get_weather" || aws.ToString(tu.Value.ToolUseId) != "toolu_9" {
		t.Errorf("tool_use = %#v", tu.Value)
	}

	if in.ToolConfig == nil || len(in.ToolConfig.Tools) != 1 {
		t.Fatalf("ToolConfig = %#v, want 1 tool", in.ToolConfig)
	}
	spec, ok := in.ToolConfig.Tools[0].(*types.ToolMemberToolSpec)
	if !ok || aws.ToString(spec.Value.Name) != "get_weather" {
		t.Errorf("tool = %#v", in.ToolConfig.Tools[0])
	}
	if _, ok := spec.Value.InputSchema.(*types.ToolInputSchemaMemberJson); !ok {
		t.Errorf("input schema = %#v, want json member", spec.Value.InputSchema)
	}
}

func TestStreamNoToolsSkipsToolConfig(t *testing.T) {
	c := &Client{}
	fc := &fakeConverser{}
	scripted(c, fc, []types.ConverseStreamOutput{messageStop(types.StopReasonEndTurn)}, nil)
	seq, err := c.Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	collect(t, seq)
	if fc.input.ToolConfig != nil {
		t.Errorf("ToolConfig = %#v, want nil when no tools", fc.input.ToolConfig)
	}
	if fc.input.System != nil {
		t.Errorf("System = %#v, want nil when no system message", fc.input.System)
	}
}

// ---------- ListModels ----------

func TestListModelsStaticCatalog(t *testing.T) {
	c := &Client{}
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 11 {
		t.Fatalf("catalog has %d models, want 11", len(models))
	}
	want := []string{
		"anthropic.claude-sonnet-4-6",
		"anthropic.claude-opus-4-8",
		"anthropic.claude-haiku-4-5-20251001-v1:0",
		"anthropic.claude-3-5-sonnet-20240620-v1:0",
		"amazon.nova-pro-v1:0",
		"amazon.nova-lite-v1:0",
		"amazon.nova-micro-v1:0",
		"amazon.titan-text-express-v1",
		"amazon.titan-text-lite-v1",
		"meta.llama3-70b-instruct-v1:0",
		"meta.llama3-8b-instruct-v1:0",
	}
	for i, id := range want {
		if models[i].ID != id {
			t.Errorf("catalog[%d] = %q, want %q", i, models[i].ID, id)
		}
	}
}

func TestModelInfoUnknown(t *testing.T) {
	c := &Client{}
	mi, err := c.ModelInfo(context.Background(), "anthropic.claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("ModelInfo: %v", err)
	}
	if mi.ContextLength != 0 {
		t.Errorf("ContextLength = %d, want 0 (unknown)", mi.ContextLength)
	}
}

// ---------- newSDKClient / opts ----------

func TestNewSDKClientRecordsProfileAndRegion(t *testing.T) {
	var got []func(*awscfg.LoadOptions) error
	loader := func(_ context.Context, optFns ...func(*awscfg.LoadOptions) error) (aws.Config, error) {
		got = optFns
		return aws.Config{}, nil
	}
	_ = newSDKClient("work", "eu-west-1", loader)

	var lo awscfg.LoadOptions
	for _, fn := range got {
		if err := fn(&lo); err != nil {
			t.Fatalf("apply option: %v", err)
		}
	}
	if lo.SharedConfigProfile != "work" {
		t.Errorf("SharedConfigProfile = %q, want work", lo.SharedConfigProfile)
	}
	if lo.Region != "eu-west-1" {
		t.Errorf("Region = %q, want eu-west-1", lo.Region)
	}
}

func TestNewSDKClientAbsentOptsUseStandardChain(t *testing.T) {
	var got []func(*awscfg.LoadOptions) error
	loader := func(_ context.Context, optFns ...func(*awscfg.LoadOptions) error) (aws.Config, error) {
		got = optFns
		return aws.Config{}, nil
	}
	_ = newSDKClient("", "", loader)
	if len(got) != 0 {
		t.Errorf("got %d load options, want 0 (standard chain)", len(got))
	}
}

func TestNewSDKClientLoadError(t *testing.T) {
	loader := func(_ context.Context, _ ...func(*awscfg.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, errors.New("shared config profile boom not found")
	}
	c := newSDKClient("boom", "", loader)
	if c.initErr == nil {
		t.Fatal("expected initErr for failed config load")
	}
}

// ---------- conversions ----------

func TestExtractSystem(t *testing.T) {
	system, msgs := extractSystem([]llm.Message{
		{Role: llm.RoleSystem, Content: "be tight"},
		{Role: llm.RoleUser, Content: "hello"},
	})
	if system != "be tight" {
		t.Errorf("system = %q", system)
	}
	if len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Errorf("msgs = %+v", msgs)
	}
	system, msgs = extractSystem([]llm.Message{{Role: llm.RoleUser, Content: "no system"}})
	if system != "" || len(msgs) != 1 {
		t.Errorf("no-system case: system=%q msgs=%d", system, len(msgs))
	}
}

func TestMessagesToWireToolResult(t *testing.T) {
	wire := messagesToWire([]llm.Message{
		{Role: llm.RoleUser, Content: "result data", ToolCallID: "tu_7"},
		{Role: llm.RoleAssistant, Content: "hi", ToolCalls: []llm.ToolCall{{ID: "t1", Name: "f", Arguments: `{"a":1}`}}},
	})
	if len(wire) != 2 {
		t.Fatalf("wire msgs = %d, want 2", len(wire))
	}
	tr, ok := wire[0].Content[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("content = %#v, want tool_result", wire[0].Content[0])
	}
	if aws.ToString(tr.Value.ToolUseId) != "tu_7" {
		t.Errorf("ToolUseId = %v", tr.Value.ToolUseId)
	}
	if len(tr.Value.Content) != 1 {
		t.Fatalf("result content = %d blocks, want 1", len(tr.Value.Content))
	}
	if txt, ok := tr.Value.Content[0].(*types.ToolResultContentBlockMemberText); !ok || txt.Value != "result data" {
		t.Errorf("result content = %#v", tr.Value.Content[0])
	}

	asst := wire[1]
	if asst.Role != types.ConversationRoleAssistant || len(asst.Content) != 2 {
		t.Fatalf("asst = %#v, want assistant with 2 blocks", asst)
	}
	tu, ok := asst.Content[1].(*types.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("asst.Content[1] = %#v, want tool_use", asst.Content[1])
	}
	if aws.ToString(tu.Value.Name) != "f" || aws.ToString(tu.Value.ToolUseId) != "t1" {
		t.Errorf("tool_use = %#v", tu.Value)
	}
}

func TestMessagesToWireText(t *testing.T) {
	wire := messagesToWire([]llm.Message{{Role: llm.RoleUser, Content: "hello"}})
	if len(wire) != 1 {
		t.Fatalf("wire msgs = %d, want 1", len(wire))
	}
	txt, ok := wire[0].Content[0].(*types.ContentBlockMemberText)
	if !ok || txt.Value != "hello" {
		t.Errorf("content = %#v, want text hello", wire[0].Content[0])
	}
}

func TestToolsToWire(t *testing.T) {
	tools := toolsToWire([]llm.ToolDef{
		{Name: "get_weather", Description: "Weather", Parameters: map[string]any{"type": "object"}},
	})
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	spec, ok := tools[0].(*types.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("tool = %#v, want toolSpec", tools[0])
	}
	if aws.ToString(spec.Value.Name) != "get_weather" || aws.ToString(spec.Value.Description) != "Weather" {
		t.Errorf("spec = %#v", spec.Value)
	}
	if _, ok := spec.Value.InputSchema.(*types.ToolInputSchemaMemberJson); !ok {
		t.Errorf("input schema = %#v, want json", spec.Value.InputSchema)
	}
	if toolsToWire(nil) != nil {
		t.Error("toolsToWire(nil) should return nil")
	}
}

// ---------- finish reason & error mapping ----------

func TestCanonicalFinishReason(t *testing.T) {
	tests := []struct {
		in   types.StopReason
		want llm.FinishReason
	}{
		{types.StopReasonEndTurn, llm.FinishStop},
		{types.StopReasonStopSequence, llm.FinishStop},
		{types.StopReasonToolUse, llm.FinishToolCalls},
		{types.StopReasonMaxTokens, llm.FinishLength},
		{types.StopReasonContentFiltered, llm.FinishOther},
		{types.StopReason("mystery"), llm.FinishOther},
	}
	for _, tc := range tests {
		if got := canonicalFinishReason(tc.in); got != tc.want {
			t.Errorf("canonicalFinishReason(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMapConverseError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want llm.ErrorKind
	}{
		{"access denied", &types.AccessDeniedException{Message: aws.String("denied")}, llm.KindAuth},
		{"throttling", &types.ThrottlingException{Message: aws.String("slow")}, llm.KindRateLimit},
		{"quota", &types.ServiceQuotaExceededException{Message: aws.String("full")}, llm.KindRateLimit},
		{"not ready", &types.ModelNotReadyException{Message: aws.String("warming")}, llm.KindRateLimit},
		{"validation", &types.ValidationException{Message: aws.String("bad")}, llm.KindInvalidRequest},
		{"timeout", &types.ModelTimeoutException{Message: aws.String("slow model")}, llm.KindTimeout},
		{"server", &types.InternalServerException{Message: aws.String("boom")}, llm.KindOther},
		{"credentials", errors.New("failed to retrieve credentials to sign this request"), llm.KindAuth},
		{"network", errors.New("connection refused"), llm.KindNetwork},
		{"nil", nil, llm.KindOther},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapConverseError(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("mapConverseError(nil) = %v, want nil", got)
				}
				return
			}
			pe, ok := got.(*llm.ProviderError)
			if !ok {
				t.Fatalf("error = %T, want *llm.ProviderError", got)
			}
			if pe.Kind != tc.want {
				t.Errorf("Kind = %q, want %q", pe.Kind, tc.want)
			}
			if pe.Message == "" {
				t.Error("ProviderError needs a message")
			}
		})
	}
	got := mapConverseError(&smithy.GenericAPIError{Code: "SomethingElse", Message: "weird"})
	if pe, ok := got.(*llm.ProviderError); !ok || pe.Kind != llm.KindOther {
		t.Errorf("generic API error = %v, want KindOther", got)
	}
}

// ---------- wire registration ----------

func TestWireRegistered(t *testing.T) {
	if llm.WireBedrock != "bedrock" {
		t.Errorf("WireBedrock = %q, want bedrock", llm.WireBedrock)
	}
	if !llm.ValidWires["bedrock"] {
		t.Error("bedrock wire not registered with the llm registry")
	}
}
