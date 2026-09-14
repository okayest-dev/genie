// Package bedrock implements the llm seam over Amazon Bedrock's Converse
// stream API (ADR-0004). Auth goes through the AWS SDK standard credential
// chain — env vars, shared config, SSO, assume-role, credential_process —
// never a genie-owned credential store; genie only selects the profile and
// region.
package bedrock

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"

	"github.com/okayest-dev/genie/internal/llm"
)

func init() {
	llm.RegisterWire(llm.WireBedrock, func(_, _ string, opts map[string]any) llm.Client {
		profile, _ := opts["profile"].(string)
		region, _ := opts["region"].(string)
		return NewClient(profile, region)
	})
}

// converseStreamer opens a ConverseStream request and returns a handle over
// its event stream. The SDK adapter (*sdkStreamer) satisfies it; tests inject
// a fake that scripts a streamHandle directly, bypassing the wire protocol.
type converseStreamer interface {
	ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*streamHandle, error)
}

// streamHandle is the piece of a ConverseStream response Stream consumes.
type streamHandle struct {
	events <-chan types.ConverseStreamOutput
	err    func() error
	close  func() error
}

// sdkStreamer adapts the SDK bedrockruntime client to the converseStreamer
// seam.
type sdkStreamer struct {
	client *bedrockruntime.Client
}

func (s *sdkStreamer) ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*streamHandle, error) {
	output, err := s.client.ConverseStream(ctx, params, optFns...)
	if err != nil {
		return nil, err
	}
	stream := output.GetStream()
	return &streamHandle{
		events: stream.Events(),
		err:    stream.Err,
		close:  stream.Close,
	}, nil
}

// awsConfigLoader is the seam for resolving the SDK config. LoadDefaultConfig
// from the config package satisfies it; tests inject a fake.
type awsConfigLoader func(ctx context.Context, optFns ...func(*awscfg.LoadOptions) error) (aws.Config, error)

// Client is a Bedrock Converse wire client.
type Client struct {
	converse converseStreamer
	initErr  error
}

// NewClient builds a Bedrock wire client over the AWS SDK credential chain.
// A non-empty profile/region map to WithSharedConfigProfile / WithRegion;
// when either is absent the SDK's standard chain resolves it (env vars,
// shared config, SSO, assume-role, credential_process; region from
// AWS_REGION/AWS_DEFAULT_REGION or the shared config).
func NewClient(profile, region string) *Client {
	return newSDKClient(profile, region, awscfg.LoadDefaultConfig)
}

// newSDKClient builds the real SDK-backed client. load is injectable so tests
// can hold the Client together without the SDK.
func newSDKClient(profile, region string, load awsConfigLoader) *Client {
	var opts []func(*awscfg.LoadOptions) error
	if profile != "" {
		opts = append(opts, awscfg.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, awscfg.WithRegion(region))
	}
	cfg, err := load(context.Background(), opts...)
	if err != nil {
		return &Client{initErr: err}
	}
	return &Client{converse: &sdkStreamer{client: bedrockruntime.NewFromConfig(cfg)}}
}

// Stream sends one Converse request and returns the normalized event stream,
// adapting the SDK's ConverseStream event stream into the llm stream contract.
// Open failures — credential resolution, a 4xx/5xx from Bedrock, network
// down — surface as the returned error; mid-stream failures as a terminal
// error event.
func (c *Client) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	if c.initErr != nil {
		return nil, &llm.ProviderError{
			Kind:    llm.KindAuth,
			Message: fmt.Sprintf("bedrock: aws config: %v", c.initErr),
		}
	}

	system, msgs := extractSystem(req.Messages)
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(req.Model),
		Messages:        messagesToWire(msgs),
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(4096)},
	}
	if system != "" {
		input.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: system},
		}
	}
	if tools := toolsToWire(req.Tools); tools != nil {
		input.ToolConfig = &types.ToolConfiguration{
			Tools:      tools,
			ToolChoice: &types.ToolChoiceMemberAuto{Value: types.AutoToolChoice{}},
		}
	}

	output, err := c.converse.ConverseStream(ctx, input)
	if err != nil {
		return nil, mapConverseError(err)
	}

	return func(yield func(llm.Event) bool) {
		defer output.close()

		// Tool call accumulation keyed by content block index, so deltas from
		// interleaved blocks assemble in place and emit on block stop.
		toolCalls := make(map[int32]*toolAcc)

		for evt := range output.events {
			if ctx.Err() != nil {
				return
			}
			switch e := evt.(type) {
			case *types.ConverseStreamOutputMemberMessageStart:
				// Role only; nothing to normalize.

			case *types.ConverseStreamOutputMemberContentBlockStart:
				if s, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
					toolCalls[blockIndex(e.Value.ContentBlockIndex)] = &toolAcc{
						id:   derefString(s.Value.ToolUseId),
						name: derefString(s.Value.Name),
					}
				}

			case *types.ConverseStreamOutputMemberContentBlockDelta:
				switch d := e.Value.Delta.(type) {
				case *types.ContentBlockDeltaMemberText:
					if d.Value != "" {
						if !yield(llm.Event{Kind: llm.EventText, Text: d.Value}) {
							return
						}
					}
				case *types.ContentBlockDeltaMemberToolUse:
					if acc := toolCalls[blockIndex(e.Value.ContentBlockIndex)]; acc != nil {
						acc.input.WriteString(derefString(d.Value.Input))
					}
				}

			case *types.ConverseStreamOutputMemberContentBlockStop:
				if acc := toolCalls[blockIndex(e.Value.ContentBlockIndex)]; acc != nil {
					calls := []llm.ToolCall{{ID: acc.id, Name: acc.name, Arguments: acc.input.String()}}
					slog.Debug("bedrock tool call accumulated", "id", acc.id, "name", acc.name)
					if !yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: calls}) {
						return
					}
					delete(toolCalls, blockIndex(e.Value.ContentBlockIndex))
				}

			case *types.ConverseStreamOutputMemberMessageStop:
				if !yield(llm.Event{Kind: llm.EventFinish, End: canonicalFinishReason(e.Value.StopReason)}) {
					return
				}

			case *types.ConverseStreamOutputMemberMetadata:
				if usage := e.Value.Usage; usage != nil {
					if !yield(llm.Event{Kind: llm.EventUsage, Usage: llm.Usage{
						PromptTokens:     int(intVal(usage.InputTokens)),
						CompletionTokens: int(intVal(usage.OutputTokens)),
						TotalTokens:      int(intVal(usage.TotalTokens)),
					}}) {
						return
					}
				}
			}
		}

		if err := output.err(); err != nil && ctx.Err() == nil {
			slog.Debug("bedrock stream ended with error", "error", err)
			yield(llm.Event{Kind: llm.EventError, Err: mapConverseError(err)})
		}
	}, nil
}

// ListModels returns the static 11-model catalog. Bedrock's model listing is
// a control-plane call (ListFoundationModels) gated by extra IAM permissions,
// so the wire ships a curated catalog instead — no network call, no extra
// policy.
func (c *Client) ListModels(_ context.Context) ([]llm.Model, error) {
	out := make([]llm.Model, 0, len(modelCatalog))
	for _, id := range modelCatalog {
		out = append(out, llm.Model{ID: id})
	}
	return out, nil
}

// ModelInfo implements the optional llm.ModelInfoProvider seam. The static
// catalog carries no authoritative context windows, so it reports unknown;
// callers fall back to an explicit context.windows override.
func (c *Client) ModelInfo(_ context.Context, _ string) (*llm.ModelInfo, error) {
	return &llm.ModelInfo{}, nil
}

// modelCatalog is the shipped 11-model static catalog: the flagship chat
// models across Anthropic Claude, Amazon Nova, Amazon Titan and Meta Llama.
var modelCatalog = []string{
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

// toolAcc accumulates one tool call's partial JSON input across deltas.
type toolAcc struct {
	id, name string
	input    strings.Builder
}

// canonicalFinishReason maps a Bedrock stop reason to the canonical set.
func canonicalFinishReason(r types.StopReason) llm.FinishReason {
	switch r {
	case types.StopReasonEndTurn, types.StopReasonStopSequence:
		return llm.FinishStop
	case types.StopReasonToolUse:
		return llm.FinishToolCalls
	case types.StopReasonMaxTokens:
		return llm.FinishLength
	default:
		return llm.FinishOther
	}
}

// mapConverseError maps a Bedrock failure to the normalized ProviderError
// surface. Smithy API errors (AccessDenied, Throttling, Validation,
// ModelTimeout) map to their kinds; credential-resolution and transport
// failures fall through to auth/network.
func mapConverseError(err error) error {
	if err == nil {
		return nil
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		kind := llm.KindOther
		switch apiErr.ErrorCode() {
		case "AccessDeniedException":
			kind = llm.KindAuth
		case "ThrottlingException", "ServiceQuotaExceededException", "ModelNotReadyException":
			kind = llm.KindRateLimit
		case "ValidationException":
			kind = llm.KindInvalidRequest
		case "ModelTimeoutException":
			kind = llm.KindTimeout
		}
		msg := apiErr.ErrorMessage()
		if msg == "" {
			msg = apiErr.ErrorCode()
		}
		return &llm.ProviderError{Kind: kind, Message: msg}
	}

	// The SDK surfaces credential-resolution failure (no profile set, missing
	// env vars, dead SSO cache) as a transport-adjacent signing error, not an
	// API error — read "no credentials" as an auth problem.
	if strings.Contains(err.Error(), "credentials") || strings.Contains(err.Error(), "Credential") {
		return &llm.ProviderError{Kind: llm.KindAuth, Message: err.Error()}
	}
	return &llm.ProviderError{Kind: llm.KindNetwork, Message: err.Error()}
}

func blockIndex(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func intVal(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
