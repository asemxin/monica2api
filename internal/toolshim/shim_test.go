package toolshim

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestApplyInjectsToolInstructionsAndNormalizesToolMessages(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model: "claude-5-sonnet",
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ToolCall{{
					ID:   "toolu_test",
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      "run_cron_job",
						Arguments: `{"job":"morning"}`,
					},
				}},
			},
			{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: "toolu_test",
				Content:    `{"ok":true}`,
			},
		},
		Tools: []openai.Tool{{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "run_cron_job",
				Description: "Run a cron job by name",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job": map[string]any{"type": "string"},
					},
					"required": []string{"job"},
				},
			},
		}},
	}

	Apply(req)

	if len(req.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != openai.ChatMessageRoleAssistant || len(req.Messages[0].ToolCalls) != 0 {
		t.Fatalf("assistant tool_calls were not normalized: %+v", req.Messages[0])
	}
	if !strings.Contains(req.Messages[0].Content, "Assistant requested tool calls") {
		t.Fatalf("assistant tool call context missing: %q", req.Messages[0].Content)
	}
	if req.Messages[1].Role != openai.ChatMessageRoleUser {
		t.Fatalf("tool result role = %q, want user", req.Messages[1].Role)
	}
	if !strings.Contains(req.Messages[1].Content, "Available tools") {
		t.Fatalf("tool instructions not injected into final user message: %q", req.Messages[1].Content)
	}
	if !strings.Contains(req.Messages[1].Content, "toolu_test") || !strings.Contains(req.Messages[1].Content, `{"ok":true}`) {
		t.Fatalf("tool result content not preserved: %q", req.Messages[1].Content)
	}
}

func TestBuildForcedToolCallResponse(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model: "claude-5-sonnet",
		Messages: []openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleUser,
			Content: "Run the morning report job.",
		}},
		Tools: []openai.Tool{{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "run_cron_job",
				Description: "Run a cron job by name",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job": map[string]any{"type": "string"},
					},
					"required": []string{"job"},
				},
			},
		}},
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "run_cron_job",
			},
		},
	}

	toolResp, ok := BuildForcedToolCallResponse(req)
	if !ok {
		t.Fatal("expected forced tool call response")
	}

	encoded, err := json.Marshal(toolResp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"finish_reason":"tool_calls"`) || !strings.Contains(string(encoded), `"content":null`) {
		t.Fatalf("invalid forced tool response: %s", encoded)
	}

	call := toolResp.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "run_cron_job" {
		t.Fatalf("tool name = %q", call.Function.Name)
	}
	if !strings.Contains(call.Function.Arguments, "morning report") {
		t.Fatalf("arguments did not include inferred job: %q", call.Function.Arguments)
	}
}
func TestBuildToolCallResponse(t *testing.T) {
	resp := &openai.ChatCompletionResponse{
		ID:      "chatcmpl_test",
		Object:  "chat.completion",
		Created: 123,
		Choices: []openai.ChatCompletionChoice{
			{
				Index: 0,
				Message: openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: `{"tool_calls":[{"id":"toolu_test","name":"run_cron_job","arguments":{"job":"morning"}}]}`,
				},
				FinishReason: openai.FinishReasonStop,
			},
		},
	}

	toolResp, ok := BuildToolCallResponse("claude-5-sonnet", resp)
	if !ok {
		t.Fatal("expected tool call response")
	}

	encoded, err := json.Marshal(toolResp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"content":null`) {
		t.Fatalf("tool call response should include content:null, got %s", encoded)
	}

	choice := toolResp.Choices[0]
	if choice.FinishReason != string(openai.FinishReasonToolCalls) {
		t.Fatalf("finish reason = %q, want %q", choice.FinishReason, openai.FinishReasonToolCalls)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(choice.Message.ToolCalls))
	}

	call := choice.Message.ToolCalls[0]
	if call.ID != "toolu_test" {
		t.Fatalf("tool call id = %q", call.ID)
	}
	if call.Function.Name != "run_cron_job" {
		t.Fatalf("tool name = %q", call.Function.Name)
	}
	if call.Function.Arguments != `{"job":"morning"}` {
		t.Fatalf("arguments = %q", call.Function.Arguments)
	}
}

func TestBuildToolCallResponseIgnoresNormalText(t *testing.T) {
	resp := &openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: "normal answer",
				},
			},
		},
	}

	if _, ok := BuildToolCallResponse("claude-5-sonnet", resp); ok {
		t.Fatal("normal text should not be converted")
	}
}

func TestStreamResponseEncodesToolCallsAsSSE(t *testing.T) {
	response := newToolCallResponse("gpt-5.5", []openai.ToolCall{{ID: "call_test", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "echo", Arguments: `{"text":"OK"}`}}})
	stream, err := StreamResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	body, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `"object":"chat.completion.chunk"`) {
		t.Fatalf("missing stream chunk: %s", text)
	}
	if !strings.Contains(text, `"tool_calls":[{"index":0,"id":"call_test"`) {
		t.Fatalf("missing tool call: %s", text)
	}
	if !strings.Contains(text, `"finish_reason":"tool_calls"`) {
		t.Fatalf("missing finish reason: %s", text)
	}
	if !strings.HasSuffix(text, "data: [DONE]\n\n") {
		t.Fatalf("missing terminator: %s", text)
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatalf("invalid chunk: %v", err)
		}
	}
}

func TestStreamResponseEncodesCollectedTextAsSSE(t *testing.T) {
	response := &openai.ChatCompletionResponse{ID: "chatcmpl_text", Object: "chat.completion", Created: 123, Model: "gpt-5.5", Choices: []openai.ChatCompletionChoice{{Index: 0, Message: openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "normal answer"}, FinishReason: openai.FinishReasonStop}}}
	stream, err := StreamResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	body, _ := io.ReadAll(stream)
	text := string(body)
	if !strings.Contains(text, `"content":"normal answer"`) {
		t.Fatalf("missing content: %s", text)
	}
	if !strings.Contains(text, `"finish_reason":"stop"`) {
		t.Fatalf("missing finish reason: %s", text)
	}
}
