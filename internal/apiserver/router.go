package apiserver

import (
	"fmt"
	"io"
	"monica-proxy/internal/config"
	"monica-proxy/internal/errors"
	"monica-proxy/internal/logger"
	"monica-proxy/internal/middleware"
	"monica-proxy/internal/monica"
	"monica-proxy/internal/service"
	"monica-proxy/internal/types"
	"monica-proxy/internal/utils"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

// RegisterRoutes 娉ㄥ唽 Echo 璺敱
func RegisterRoutes(e *echo.Echo, cfg *config.Config) {
	// 璁剧疆鑷畾涔夐敊璇鐞嗗櫒
	e.HTTPErrorHandler = middleware.ErrorHandler()

	api := e.Group("")
	api.Use(middleware.BearerAuth(cfg))
	api.Use(middleware.RequestLogger(cfg))

	// 鍒濆鍖栨湇鍔″疄渚?
	chatService := service.NewChatService(cfg)
	modelService := service.NewModelService(cfg)
	imageService := service.NewImageService(cfg)
	customBotService := service.NewCustomBotService(cfg)
	fileService := service.NewFileService(cfg)

	// ChatGPT 椋庢牸鐨勮姹傝浆鍙戝埌 /v1/chat/completions
	api.POST("/v1/chat/completions", createChatCompletionHandler(chatService, customBotService, cfg))
	// 鑾峰彇鏀寔鐨勬ā鍨嬪垪琛?
	api.GET("/v1/models", createListModelsHandler(modelService))
	api.GET("/v1/usage", createUsageHandler(cfg))
	// DALL-E 椋庢牸鐨勫浘鐗囩敓鎴愯姹?
	api.POST("/v1/images/generations", createImageGenerationHandler(imageService))

	// OpenAI鍏煎鐨勬枃浠剁鐞咥PI
	api.POST("/v1/files", createFileUploadHandler(fileService))
	api.GET("/v1/files/:file_id", createGetFileHandler(fileService))
	api.GET("/v1/files", createListFilesHandler(fileService))
	api.DELETE("/v1/files/:file_id", createDeleteFileHandler(fileService))

	// Custom Bot 娴嬭瘯鎺ュ彛
	api.POST("/v1/chat/custom-bot/:bot_uid", createCustomBotHandler(customBotService, cfg))
	// 鏂板涓嶅甫bot_uid鐨勮矾鐢憋紝浣跨敤鐜鍙橀噺涓殑BOT_UID
	api.POST("/v1/chat/custom-bot", createCustomBotHandler(customBotService, cfg))
}

func createUsageHandler(cfg *config.Config) echo.HandlerFunc {
	type quota struct {
		Module         string `json:"module"`
		Scene          string `json:"scene"`
		ResetFrequency string `json:"resetFrequency"`
		DefaultQuota   int    `json:"defaultQuota"`
		CurrentQuota   int    `json:"currentQuota"`
		LastResetTime  string `json:"lastResetTime"`
		Unlimited      bool   `json:"unlimited"`
	}
	type advancedCredits struct {
		Remaining int    `json:"remaining"`
		Total     int    `json:"total"`
		Reset     string `json:"reset"`
		Available bool   `json:"available"`
	}
	type usageSummary struct {
		Plan            string          `json:"plan"`
		StandardQueries string          `json:"standardQueries"`
		AdvancedQueries string          `json:"advancedQueries"`
		AdvancedCredits advancedCredits `json:"advancedCredits"`
	}

	return func(c echo.Context) error {
		summary := usageSummary{
			Plan:            "Max",
			StandardQueries: "unlimited",
			AdvancedQueries: "unlimited",
		}

		resp, err := utils.GetMonicaQuota(cfg)
		if err != nil {
			return c.JSON(http.StatusBadGateway, map[string]any{
				"error":   err.Error(),
				"summary": summary,
				"quotas":  []quota{},
			})
		}

		quotas := make([]quota, 0)
		for _, module := range resp.Data.ModuleQuotas {
			for _, item := range module.Quotas {
				q := quota{
					Module:         module.Module,
					Scene:          item.Scene,
					ResetFrequency: item.ResetFrequency,
					DefaultQuota:   item.DefaultQuota,
					CurrentQuota:   item.CurrentQuota,
					LastResetTime:  item.LastResetTime,
					Unlimited:      item.CurrentQuota >= 99999 || item.DefaultQuota >= 99999,
				}
				quotas = append(quotas, q)

				if module.Module == "credits" && (!summary.AdvancedCredits.Available || item.DefaultQuota > summary.AdvancedCredits.Total) {
					summary.AdvancedCredits = advancedCredits{
						Remaining: item.CurrentQuota,
						Total:     item.DefaultQuota,
						Reset:     item.ResetFrequency,
						Available: true,
					}
				}
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"summary": summary,
			"quotas":  quotas,
		})
	}
}

// createChatCompletionHandler 鍒涘缓鑱婂ぉ瀹屾垚澶勭悊鍣?
func createChatCompletionHandler(chatService service.ChatService, customBotService service.CustomBotService, cfg *config.Config) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req openai.ChatCompletionRequest
		if err := c.Bind(&req); err != nil {
			return errors.NewBadRequestError("鏃犳晥鐨勮姹傛暟鎹?, err)
		}

		ctx := c.Request().Context()
		var result interface{}
		var err error

		// 妫€鏌ユ槸鍚﹀惎鐢ㄤ簡 Custom Bot 妯″紡
		if cfg.Monica.EnableCustomBotMode {
			// 浣跨敤 Custom Bot Service 澶勭悊璇锋眰
			result, err = customBotService.HandleCustomBotChat(ctx, &req, cfg.Monica.BotUID)
		} else {
			// 浣跨敤鏅€氱殑 Chat Service 澶勭悊璇锋眰
			result, err = chatService.HandleChatCompletion(ctx, &req)
		}

		if err != nil {
			return err
		}

		// 鏍规嵁璇锋眰鍙傛暟鍐冲畾鍝嶅簲鏂瑰紡
		if req.Stream {
			// 瀵逛簬娴佸紡璇锋眰锛宺esult鏄竴涓猧o.ReadCloser
			rawBody, ok := result.(io.Reader)
			if !ok {
				return errors.NewInternalError(nil)
			}

			// 纭繚鍏抽棴鍝嶅簲浣?
			closer, isCloser := rawBody.(io.Closer)
			if isCloser {
				defer closer.Close()
			}

			// 璁剧疆鍝嶅簲澶?
			c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
			c.Response().Header().Set("Cache-Control", "no-cache")
			c.Response().Header().Set("Transfer-Encoding", "chunked")
			c.Response().WriteHeader(http.StatusOK)

			// 娴佸紡澶勭悊鍝嶅簲锛堝甫閰嶇疆鍙傛暟锛?
			if _, ok := result.(interface{ OpenAICompatibleSSE() }); ok {
				_, err = io.Copy(c.Response().Writer, rawBody)
			} else {
				err = monica.StreamMonicaSSEToClientWithConfig(req.Model, c.Response().Writer, rawBody, cfg)
			}
			if err != nil {
				return errors.NewInternalError(err)
			}
			return nil
		} else {
			// 瀵逛簬闈炴祦寮忚姹傦紝鐩存帴杩斿洖JSON鍝嶅簲
			return c.JSON(http.StatusOK, result)
		}
	}
}

// createListModelsHandler 鍒涘缓妯″瀷鍒楄〃澶勭悊鍣?
func createListModelsHandler(modelService service.ModelService) echo.HandlerFunc {
	return func(c echo.Context) error {
		// 璋冪敤鏈嶅姟鑾峰彇妯″瀷鍒楄〃
		models := modelService.GetSupportedModels()

		// 鏋勯€犲搷搴旀牸寮?
		result := make(map[string][]struct {
			Id string `json:"id"`
		})

		result["data"] = make([]struct {
			Id string `json:"id"`
		}, 0)

		for _, model := range models {
			result["data"] = append(result["data"], struct {
				Id string `json:"id"`
			}{
				Id: model,
			})
		}
		return c.JSON(http.StatusOK, result)
	}
}

// createImageGenerationHandler 鍒涘缓鍥剧墖鐢熸垚澶勭悊鍣?
func createImageGenerationHandler(imageService service.ImageService) echo.HandlerFunc {
	return func(c echo.Context) error {
		// 瑙ｆ瀽璇锋眰
		var req types.ImageGenerationRequest
		if err := c.Bind(&req); err != nil {
			return errors.NewBadRequestError("鏃犳晥鐨勮姹傛暟鎹?, err)
		}

		// 璋冪敤鏈嶅姟鐢熸垚鍥剧墖
		resp, err := imageService.GenerateImage(c.Request().Context(), &req)
		if err != nil {
			return err
		}

		// 杩斿洖缁撴灉
		return c.JSON(http.StatusOK, resp)
	}
}

// createCustomBotHandler 鍒涘缓Custom Bot澶勭悊鍣?
func createCustomBotHandler(service service.CustomBotService, cfg *config.Config) echo.HandlerFunc {
	return func(c echo.Context) error {
		// 鑾峰彇bot UID锛屼紭鍏堜粠璺敱鍙傛暟鑾峰彇锛屽鏋滄病鏈夊垯浠庣幆澧冨彉閲忚幏鍙?
		botUID := c.Param("bot_uid")
		if botUID == "" {
			// 浠庨厤缃紙鐜鍙橀噺锛変腑鑾峰彇
			botUID = cfg.Monica.BotUID
			if botUID == "" {
				return errors.NewBadRequestError("bot_uid鍙傛暟涓嶈兘涓虹┖锛岃鍦║RL涓寚瀹氭垨璁剧疆BOT_UID鐜鍙橀噺", nil)
			}
		}

		var req openai.ChatCompletionRequest
		if err := c.Bind(&req); err != nil {
			return errors.NewBadRequestError("璇锋眰浣撹В鏋愬け璐?, err)
		}

		ctx := c.Request().Context()
		result, err := service.HandleCustomBotChat(ctx, &req, botUID)
		if err != nil {
			return err
		}

		// 濡傛灉鏄祦寮忓搷搴?
		if req.Stream {
			// 璁剧疆鍝嶅簲澶?
			c.Response().Header().Set("Content-Type", "text/event-stream")
			c.Response().Header().Set("Cache-Control", "no-cache")
			c.Response().Header().Set("Connection", "keep-alive")
			c.Response().Header().Set("Transfer-Encoding", "chunked")

			// 鑾峰彇鍝嶅簲浣擄紙io.ReadCloser锛?
			stream, ok := result.(io.ReadCloser)
			if !ok {
				return errors.NewInternalError(fmt.Errorf("娴佸紡鍝嶅簲绫诲瀷閿欒"))
			}
			defer stream.Close()

			// 杞崲骞跺啓鍏ュ搷搴旓紙甯﹂厤缃弬鏁帮級
			err := monica.StreamMonicaSSEToClientWithConfig(req.Model, c.Response().Writer, stream, cfg)
			if err != nil {
				logger.Error("娴佸紡鍝嶅簲鍐欏叆澶辫触", zap.Error(err))
				return err
			}

			c.Response().Flush()
			return nil
		}

		// 闈炴祦寮忓搷搴?
		return c.JSON(http.StatusOK, result)
	}
}

// createFileUploadHandler 鍒涘缓鏂囦欢涓婁紶澶勭悊鍣?
func createFileUploadHandler(fileService service.FileService) echo.HandlerFunc {
	return func(c echo.Context) error {
		// 瑙ｆ瀽multipart form
		form, err := c.MultipartForm()
		if err != nil {
			return errors.NewBadRequestError("瑙ｆ瀽multipart form澶辫触", err)
		}
		defer form.RemoveAll()

		// 鑾峰彇涓婁紶鐨勬枃浠?
		files := form.File["file"]
		if len(files) == 0 {
			return errors.NewBadRequestError("鏈壘鍒颁笂浼犵殑鏂囦欢", nil)
		}

		fileHeader := files[0]

		// 鑾峰彇purpose鍙傛暟
		purpose := c.FormValue("purpose")
		if purpose == "" {
			purpose = "assistants" // 榛樿鐢ㄩ€?
		}

		// 涓婁紶鏂囦欢
		fileObject, err := fileService.UploadFile(c.Request().Context(), fileHeader, purpose)
		if err != nil {
			return err
		}

		return c.JSON(http.StatusOK, fileObject)
	}
}

// createGetFileHandler 鍒涘缓鑾峰彇鏂囦欢澶勭悊鍣?
func createGetFileHandler(fileService service.FileService) echo.HandlerFunc {
	return func(c echo.Context) error {
		fileID := c.Param("file_id")
		if fileID == "" {
			return errors.NewBadRequestError("file_id鍙傛暟涓嶈兘涓虹┖", nil)
		}

		fileObject, err := fileService.GetFile(c.Request().Context(), fileID)
		if err != nil {
			return err
		}

		return c.JSON(http.StatusOK, fileObject)
	}
}

// createListFilesHandler 鍒涘缓鏂囦欢鍒楄〃澶勭悊鍣?
func createListFilesHandler(fileService service.FileService) echo.HandlerFunc {
	return func(c echo.Context) error {
		files, err := fileService.ListFiles(c.Request().Context())
		if err != nil {
			return err
		}

		response := types.FileListResponse{
			Object: "list",
			Data:   make([]types.FileObject, len(files)),
		}

		for i, file := range files {
			response.Data[i] = *file
		}

		return c.JSON(http.StatusOK, response)
	}
}

// createDeleteFileHandler 鍒涘缓鍒犻櫎鏂囦欢澶勭悊鍣?
func createDeleteFileHandler(fileService service.FileService) echo.HandlerFunc {
	return func(c echo.Context) error {
		fileID := c.Param("file_id")
		if fileID == "" {
			return errors.NewBadRequestError("file_id鍙傛暟涓嶈兘涓虹┖", nil)
		}

		err := fileService.DeleteFile(c.Request().Context(), fileID)
		if err != nil {
			return err
		}

		response := types.DeleteFileResponse{
			ID:      fileID,
			Object:  "file",
			Deleted: true,
		}

		return c.JSON(http.StatusOK, response)
	}
}

