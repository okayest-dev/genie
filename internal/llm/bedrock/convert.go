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
// Tool results (role tool, or user with a tool call id) become user messages
// with tool_result content blocks; assistant messages with tool calls carry
// tool_use content blocks. Consecutive tool results merge into one user
// message so every tool_use is followed immediately by its tool_result.
func messagesToWire(messages []llm.Message) []types.Message {
	out := make([]types.Message, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case llm.RoleUser:
			if m.ToolCallID != "" {
				out = appendToolResult(out, m.ToolCallID, m.Content)
			} else {
				out = append(out, types.Message{
					Role:    types.ConversationRoleUser,
					Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: m.Content}},
				})
			}
		case llm.RoleTool:
			if m.ToolCallID == "" {
				continue
			}
			out = appendToolResult(out, m.ToolCallID, m.Content)
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

// appendToolResult adds a tool_result content block for toolUseID. If the
// previous wire message is already a user message of tool_result blocks
// (parallel tool calls), the block is merged into it so Bedrock sees one
// user turn immediately after the assistant tool_use turn.
func appendToolResult(out []types.Message, toolUseID, content string) []types.Message {
	block := &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
		ToolUseId: aws.String(toolUseID),
		Content: []types.ToolResultContentBlock{
			&types.ToolResultContentBlockMemberText{Value: content},
		},
	}}
	if len(out) > 0 && out[len(out)-1].Role == types.ConversationRoleUser {
		last := out[len(out)-1]
		if _, ok := last.Content[0].(*types.ContentBlockMemberToolResult); ok {
			last.Content = append(last.Content, block)
			out[len(out)-1] = last
			return out
		}
	}
	return append(out, types.Message{
		Role:    types.ConversationRoleUser,
		Content: []types.ContentBlock{block},
	})
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
