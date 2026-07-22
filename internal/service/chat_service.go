package service

import (
	"context"
	"monica-proxy/internal/config"
	"monica-proxy/internal/errors"
	"monica-proxy/internal/logger"
	"monica-proxy/internal/monica"
	"monica-proxy/internal/toolshim"
	"monica-proxy/internal/types"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

// ChatService handles chat completion requests.
type ChatService interface {
	HandleChatCompletion(ctx context.Context, req *openai.ChatCompletionRequest) (interface{}, error)
}

type chatService struct {
	config *config.Config
}

// NewChatService creates a chat service instance.
func NewChatService(cfg *config.Config) ChatService {
	return &chatService{config: cfg}
}

// HandleChatCompletion handles chat completion requests.
func (s *chatService) HandleChatCompletion(ctx context.Context, req *openai.ChatCompletionRequest) (interface{}, error) {
	if len(req.Messages) == 0 {
		return nil, errors.NewEmptyMessageError()
	}

	requestedStream := req.Stream
	defer func() { req.Stream = requestedStream }()

	toolShimEnabled := toolshim.ShouldApply(req)
	if toolShimEnabled {
		if toolResponse, ok := toolshim.BuildForcedToolCallResponse(req); ok {
			if requestedStream {
				return toolshim.StreamResponse(toolResponse)
			}
			return toolResponse, nil
		}
		toolshim.Apply(req)
		// The Monica web channel cannot stream native client tool calls. Collect
		// the model output first, then convert a JSON tool-call envelope to the
		// OpenAI-compatible tool_calls response Hermes expects.
		req.Stream = false
	}

	monicaReq, err := types.ChatGPTToMonica(s.config, *req)
	if err != nil {
		logger.Error("failed to convert chat request", zap.Error(err))
		return nil, errors.NewInternalError(err)
	}

	stream, err := monica.SendMonicaRequest(ctx, s.config, monicaReq)
	if err != nil {
		logger.Error("failed to call Monica API", zap.Error(err))
		if appErr, ok := err.(*errors.AppError); ok {
			return nil, appErr
		}
		return nil, errors.NewInternalError(err)
	}

	if req.Stream {
		return stream.RawBody(), nil
	}

	defer stream.RawBody().Close()

	response, err := monica.CollectMonicaSSEToCompletion(req.Model, stream.RawBody())
	if err != nil {
		logger.Error("failed to process Monica response", zap.Error(err))
		return nil, errors.NewInternalError(err)
	}

	if toolShimEnabled {
		if toolResponse, ok := toolshim.BuildToolCallResponse(req.Model, response); ok {
			if requestedStream {
				return toolshim.StreamResponse(toolResponse)
			}
			return toolResponse, nil
		}
		if requestedStream {
			return toolshim.StreamResponse(response)
		}
	}

	return response, nil
}
