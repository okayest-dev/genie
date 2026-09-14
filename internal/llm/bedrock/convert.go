package bedrock

import (
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/okayest-dev/genie/internal/llm"
)

// extractSystem pulls the system instruction out of the first system message
// and returns it alongside the remaining messages.
func extractSystem(messages []llm.Message) (string, []llm.Message) {
	if len(messages) == 0 || messages[0].Role != llm.RoleSystem {
		return "", messages
	}
	return messages[0].Content, messages[1:]
}

// messagesToWire maps canonical messages to the Bedrock Converse wire format.
// Tool results become user messages with tool_result content blocks;
// assistant messages with tool calls carry tool_use content blocks.
func messagesToWire(messages []llm.Message) []types.Message {
	out := make([]types.Message, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case llm.RoleUser:
			if m.ToolCallID != "" {
				out = append(out, types.Message{
					Role: types.ConversationRoleUser,
					Content: []types.ContentBlock{
						&types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
							ToolUseId: aws.String(m.ToolCallID),
							Content: []types.ToolResultContentBlock{
								&types.ToolResultContentBlockMemberText{Value: m.Content},
							},
						}},
					},
				})
			} else {
				out = append(out, types.Message{
					Role:    types.ConversationRoleUser,
					Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: m.Content}},
				})
			}
		case llm.RoleAssistant:
			blocks := []types.ContentBlock{}
			if m.Content != "" {
				blocks = append(blocks, &types.ContentBlockMemberText{Value: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String(tc.ID),
					Name:      aws.String(tc.Name),
					Input:     toolInput(tc.Arguments),
				}})
			}
			if len(blocks) == 0 {
				continue
			}
			out = append(out, types.Message{
				Role:    types.ConversationRoleAssistant,
				Content: blocks,
			})
		}
	}
	return out
}

// toolInput wraps a tool call's raw JSON arguments into a Bedrock document.
// Invalid or empty JSON degrades to an empty object rather than failing the
// request.
func toolInput(arguments string) document.Interface {
	if arguments == "" {
		return document.NewLazyDocument(map[string]any{})
	}
	var v any
	if err := json.Unmarshal([]byte(arguments), &v); err != nil {
		return document.NewLazyDocument(map[string]any{})
	}
	return document.NewLazyDocument(v)
}

// toolsToWire maps tool definitions to the Bedrock tools array. Returns nil
// for empty tools (omitting the field from the request).
func toolsToWire(tools []llm.ToolDef) []types.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]types.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
			Name:        aws.String(t.Name),
			Description: aws.String(t.Description),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: document.NewLazyDocument(t.Parameters),
			},
		}})
	}
	return out
}
