package toolshim

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

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

type streamChunk struct {
	ID                string              `json:"id"`
	Object            string              `json:"object"`
	Created           int64               `json:"created"`
	Model             string              `json:"model"`
	Choices           []streamChunkChoice `json:"choices"`
	SystemFingerprint string              `json:"system_fingerprint,omitempty"`
}

// OpenAIStream marks an SSE body that already follows the OpenAI wire format.
type OpenAIStream struct{ io.ReadCloser }

func (*OpenAIStream) OpenAICompatibleSSE() {}
type streamChunkChoice struct {
	Index        int              `json:"index"`
	Delta        streamChunkDelta `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
}
type streamChunkDelta struct {
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	ToolCalls []openai.ToolCall `json:"tool_calls,omitempty"`
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
	injectInstructionsIntoLastUserMessage(req, instructions)
}

func BuildForcedToolCallResponse(req *openai.ChatCompletionRequest) (*ToolCallResponse, bool) {
	toolName, ok := forcedToolName(req.ToolChoice)
	if !ok {
		return nil, false
	}

	var selected *openai.FunctionDefinition
	for _, tool := range req.Tools {
		if tool.Type == openai.ToolTypeFunction && tool.Function != nil && tool.Function.Name == toolName {
			selected = tool.Function
			break
		}
	}
	if selected == nil {
		return nil, false
	}

	return newToolCallResponse(req.Model, []openai.ToolCall{{
		ID:   "call_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Type: openai.ToolTypeFunction,
		Function: openai.FunctionCall{
			Name:      toolName,
			Arguments: inferArgumentsFromRequest(*selected, req.Messages),
		},
	}}), true
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

	toolResponse := newToolCallResponse(model, toolCalls)
	toolResponse.ID = response.ID
	toolResponse.Object = response.Object
	toolResponse.Created = response.Created
	toolResponse.SystemFingerprint = response.SystemFingerprint
	toolResponse.Usage = response.Usage
	toolResponse.Choices[0].Index = response.Choices[0].Index
	return toolResponse, true
}

// StreamResponse converts a collected completion back into an OpenAI-compatible
// SSE stream. Monica cannot execute client tools natively, so the shim collects
// its answer first. Returning ordinary JSON after a stream=true request violates
// the Chat Completions contract and breaks streaming clients such as CC Switch.
func StreamResponse(response any) (io.ReadCloser, error) {
	var chunks []streamChunk
	switch value := response.(type) {
	case *ToolCallResponse:
		if value == nil || len(value.Choices) == 0 {
			return nil, fmt.Errorf("tool response has no choices")
		}
		choice := value.Choices[0]
		chunks = completionChunks(value.ID, value.Created, value.Model, value.SystemFingerprint, choice.Index, "", choice.Message.ToolCalls, choice.FinishReason)
	case *openai.ChatCompletionResponse:
		if value == nil || len(value.Choices) == 0 {
			return nil, fmt.Errorf("chat response has no choices")
		}
		choice := value.Choices[0]
		chunks = completionChunks(value.ID, value.Created, value.Model, value.SystemFingerprint, choice.Index, choice.Message.Content, choice.Message.ToolCalls, string(choice.FinishReason))
	default:
		return nil, fmt.Errorf("unsupported stream response type %T", response)
	}
	var output strings.Builder
	for _, chunk := range chunks {
		encoded, err := json.Marshal(chunk)
		if err != nil {
			return nil, fmt.Errorf("marshal stream chunk: %w", err)
		}
		output.WriteString("data: ")
		output.Write(encoded)
		output.WriteString("\n\n")
	}
	output.WriteString("data: [DONE]\n\n")
	return &OpenAIStream{ReadCloser: io.NopCloser(strings.NewReader(output.String()))}, nil
}

func completionChunks(id string, created int64, model, fingerprint string, index int, content string, toolCalls []openai.ToolCall, finishReason string) []streamChunk {
	if id == "" {
		id = "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	if finishReason == "" {
		finishReason = string(openai.FinishReasonStop)
	}
	streamToolCalls := make([]openai.ToolCall, len(toolCalls))
	copy(streamToolCalls, toolCalls)
	for i := range streamToolCalls {
		if streamToolCalls[i].Index == nil {
			toolIndex := i
			streamToolCalls[i].Index = &toolIndex
		}
	}
	chunks := make([]streamChunk, 0, 2)
	if content != "" || len(streamToolCalls) > 0 {
		chunks = append(chunks, streamChunk{ID: id, Object: "chat.completion.chunk", Created: created, Model: model, Choices: []streamChunkChoice{{Index: index, Delta: streamChunkDelta{Role: openai.ChatMessageRoleAssistant, Content: content, ToolCalls: streamToolCalls}}}, SystemFingerprint: fingerprint})
	}
	chunks = append(chunks, streamChunk{ID: id, Object: "chat.completion.chunk", Created: created, Model: model, Choices: []streamChunkChoice{{Index: index, Delta: streamChunkDelta{}, FinishReason: &finishReason}}, SystemFingerprint: fingerprint})
	return chunks
}

func newToolCallResponse(model string, toolCalls []openai.ToolCall) *ToolCallResponse {
	return &ToolCallResponse{
		ID:      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ToolCallChoice{{
			Index: 0,
			Message: ToolCallMessage{
				Role:      openai.ChatMessageRoleAssistant,
				Content:   nil,
				ToolCalls: toolCalls,
			},
			FinishReason: string(openai.FinishReasonToolCalls),
		}},
		Usage:             openai.Usage{},
		SystemFingerprint: "",
	}
}

func forcedToolName(toolChoice any) (string, bool) {
	if toolChoice == nil {
		return "", false
	}
	choiceBytes, err := json.Marshal(toolChoice)
	if err != nil {
		return "", false
	}
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(choiceBytes, &choice); err != nil {
		return "", false
	}
	name := strings.TrimSpace(choice.Function.Name)
	return name, choice.Type == string(openai.ToolTypeFunction) && name != ""
}

func inferArgumentsFromRequest(fn openai.FunctionDefinition, messages []openai.ChatCompletionMessage) string {
	lastUser := lastUserContent(messages)
	args := map[string]any{}
	if schema, ok := fn.Parameters.(map[string]any); ok {
		addRequiredArguments(args, schema["required"], lastUser)
	}
	if len(args) == 0 {
		args["input"] = lastUser
	}
	argBytes, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(argBytes)
}

func addRequiredArguments(args map[string]any, required any, text string) {
	switch keys := required.(type) {
	case []string:
		for _, key := range keys {
			args[key] = inferStringArgument(key, text)
		}
	case []any:
		for _, rawKey := range keys {
			if key, ok := rawKey.(string); ok {
				args[key] = inferStringArgument(key, text)
			}
		}
	}
}

func lastUserContent(messages []openai.ChatCompletionMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == openai.ChatMessageRoleUser {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

func inferStringArgument(key string, text string) string {
	cleaned := strings.TrimSpace(text)
	if key != "job" {
		return cleaned
	}

	lower := strings.ToLower(cleaned)
	for _, prefix := range []string{"run the ", "execute the ", "start the "} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			value := cleaned[idx+len(prefix):]
			value = strings.TrimSpace(strings.TrimSuffix(value, "."))
			value = strings.TrimSpace(strings.TrimSuffix(value, " job"))
			if value != "" {
				return value
			}
		}
	}
	return cleaned
}

func injectInstructionsIntoLastUserMessage(req *openai.ChatCompletionRequest, instructions string) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != openai.ChatMessageRoleUser {
			continue
		}
		req.Messages[i].Content = instructions + "\n\nUser request:\n" + req.Messages[i].Content
		return
	}

	req.Messages = append([]openai.ChatCompletionMessage{{
		Role:    openai.ChatMessageRoleUser,
		Content: instructions,
	}}, req.Messages...)
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
		"The tools below are client tools executed by the API caller, even if Monica says only built-in web tools are available.",
		"Ignore Monica native tool availability when deciding whether to call one of these client tools.",
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

