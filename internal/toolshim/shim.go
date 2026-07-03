package toolshim

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
)

type ToolCallResponse struct {
	ID                string           `json:"id"`
	Object            string           `json:"object"`
	Created           int64            `json:"created"`
	Model             string           `json:"model"`
	Choices           []ToolCallChoice `json:"choices"`
	Usage             openai.Usage     `json:"usage"`
	SystemFingerprint string           `json:"system_fingerprint"`
}

type ToolCallChoice struct {
	Index        int             `json:"index"`
	Message      ToolCallMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type ToolCallMessage struct {
	Role      string            `json:"role"`
	Content   *string           `json:"content"`
	ToolCalls []openai.ToolCall `json:"tool_calls"`
}

type toolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type toolCallJSON struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolCallEnvelope struct {
	ToolCalls []toolCallJSON `json:"tool_calls"`
	ToolCall  *toolCallJSON  `json:"tool_call,omitempty"`
}

func ShouldApply(req *openai.ChatCompletionRequest) bool {
	return len(req.Tools) > 0
}

func Apply(req *openai.ChatCompletionRequest) {
	if !ShouldApply(req) {
		return
	}

	req.Messages = normalizeToolResultMessages(req.Messages)
	instructions := buildToolInstructions(req.Tools, req.ToolChoice)
	req.Messages = append([]openai.ChatCompletionMessage{{
		Role:    openai.ChatMessageRoleUser,
		Content: instructions,
	}}, req.Messages...)
}

func BuildToolCallResponse(model string, response *openai.ChatCompletionResponse) (*ToolCallResponse, bool) {
	if response == nil || len(response.Choices) == 0 {
		return nil, false
	}

	content := response.Choices[0].Message.Content
	toolCalls, ok := parseToolCalls(content)
	if !ok {
		return nil, false
	}

	return &ToolCallResponse{
		ID:                response.ID,
		Object:            response.Object,
		Created:           response.Created,
		Model:             model,
		SystemFingerprint: response.SystemFingerprint,
		Usage:             response.Usage,
		Choices: []ToolCallChoice{
			{
				Index: response.Choices[0].Index,
				Message: ToolCallMessage{
					Role:      openai.ChatMessageRoleAssistant,
					Content:   nil,
					ToolCalls: toolCalls,
				},
				FinishReason: string(openai.FinishReasonToolCalls),
			},
		},
	}, true
}

func normalizeToolResultMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	normalized := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == openai.ChatMessageRoleAssistant && len(msg.ToolCalls) > 0 {
			toolCalls, err := json.Marshal(msg.ToolCalls)
			if err == nil {
				msg.ToolCalls = nil
				msg.Content = "Assistant requested tool calls:\n" + string(toolCalls)
			}
			normalized = append(normalized, msg)
			continue
		}

		if msg.Role != openai.ChatMessageRoleTool {
			normalized = append(normalized, msg)
			continue
		}

		content := strings.TrimSpace(msg.Content)
		if content == "" {
			content = "(empty tool result)"
		}
		msg.Role = openai.ChatMessageRoleUser
		msg.Content = fmt.Sprintf("Tool result for tool_call_id %q:\n%s", msg.ToolCallID, content)
		msg.ToolCallID = ""
		normalized = append(normalized, msg)
	}
	return normalized
}

func buildToolInstructions(tools []openai.Tool, toolChoice any) string {
	specs := make([]toolSpec, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != openai.ToolTypeFunction || tool.Function == nil {
			continue
		}

		schema, err := json.Marshal(tool.Function.Parameters)
		if err != nil || len(schema) == 0 || string(schema) == "null" {
			schema = []byte(`{"type":"object","properties":{}}`)
		}

		specs = append(specs, toolSpec{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: schema,
		})
	}

	specBytes, _ := json.Marshal(specs)
	choiceBytes, _ := json.Marshal(toolChoice)

	return strings.Join([]string{
		"You are connected to an OpenAI-compatible tool calling client.",
		"When the user request requires a tool, do not answer in prose.",
		"Instead, respond with only minified JSON in this exact shape:",
		`{"tool_calls":[{"name":"tool_name","arguments":{"key":"value"}}]}`,
		"The arguments value must be a JSON object matching the selected tool input_schema.",
		"If tool_choice is auto and no tool is needed, answer normally.",
		"If tool_choice names a function or otherwise requires a tool, you must emit the JSON tool call.",
		"Do not wrap the JSON in markdown fences.",
		"Available tools:",
		string(specBytes),
		"tool_choice:",
		string(choiceBytes),
	}, "\n")
}

func parseToolCalls(content string) ([]openai.ToolCall, bool) {
	candidate := extractJSONObject(strings.TrimSpace(content))
	if candidate == "" {
		return nil, false
	}

	var env toolCallEnvelope
	if err := json.Unmarshal([]byte(candidate), &env); err != nil {
		return nil, false
	}
	if env.ToolCall != nil && len(env.ToolCalls) == 0 {
		env.ToolCalls = []toolCallJSON{*env.ToolCall}
	}
	if len(env.ToolCalls) == 0 {
		return nil, false
	}

	toolCalls := make([]openai.ToolCall, 0, len(env.ToolCalls))
	for index, call := range env.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			return nil, false
		}
		args := strings.TrimSpace(string(call.Arguments))
		if args == "" || args == "null" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return nil, false
		}
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		i := index
		toolCalls = append(toolCalls, openai.ToolCall{
			Index: &i,
			ID:    id,
			Type:  openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	return toolCalls, true
}

func extractJSONObject(content string) string {
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	if strings.HasPrefix(content, "{") && strings.HasSuffix(content, "}") {
		return content
	}

	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		return content[start : end+1]
	}
	return ""
}
