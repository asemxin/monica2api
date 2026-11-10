package monica

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"monica-proxy/internal/config"
	"monica-proxy/internal/types"
	"monica-proxy/internal/utils"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GenerateImage 使用 Monica 的文生图 API 生成图片
func GenerateImage(ctx context.Context, cfg *config.Config, req *types.ImageGenerationRequest) (*types.ImageGenerationResponse, error) {
	// 1. 解析模型与参数
	model, ok := types.ResolveImageModel(req.Model)
	if !ok {
		return nil, fmt.Errorf("unsupported image model: %s", req.Model)
	}
	if req.N <= 0 {
		req.N = 1
	}
	if req.N > 4 {
		req.N = 4
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	// 2. 尺寸 -> 该模型支持的最接近宽高比
	aspectRatio := sizeToAspectRatio(req.Size, model.Ratios)

	// 3. 构建请求体（与 Monica 网页版「AI 画图」一致）
	monicaReq := &types.MonicaImageRequest{
		TaskUID:     uuid.New().String(),
		ImageCount:  req.N,
		Prompt:      req.Prompt,
		ModelType:   model.ID,
		AspectRatio: aspectRatio,
		TaskType:    "text_to_image",
	}
	if model.HasQuality {
		monicaReq.ImageQuality = imageQuality(req.Quality)
	}

	// 4. 发送请求生成图片
	resp, err := utils.RestyDefaultClient.R().
		SetContext(ctx).
		SetBody(monicaReq).
		SetHeader("cookie", cfg.Monica.Cookie).
		Post(types.ImageGenerateURL)

	if err != nil {
		return nil, fmt.Errorf("failed to send image generation request: %v", err)
	}

	// 5. 解析响应
	var monicaResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ImageToolsID int `json:"image_tools_id"`
			ExpectedTime int `json:"expected_time"`
		} `json:"data"`
	}

	if err := json.Unmarshal(resp.Body(), &monicaResp); err != nil {
		return nil, fmt.Errorf("failed to parse image generation response: %v", err)
	}

	if monicaResp.Code != 0 {
		return nil, fmt.Errorf("image generation failed: %s", monicaResp.Msg)
	}

	// 6. 轮询获取生成结果
	imageToolsID := monicaResp.Data.ImageToolsID
	expectedTime := monicaResp.Data.ExpectedTime

	// 设置轮询超时时间为预期时间的2倍
	waitSeconds := expectedTime * 2
	if waitSeconds < 180 {
		waitSeconds = 180
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(waitSeconds)*time.Second)
	defer cancel()

	var generatedImages []types.ImageGenerationData
	for {
		select {
		case <-timeoutCtx.Done():
			return nil, fmt.Errorf("timeout waiting for image generation")
		default:
			var resultData struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
				Data struct {
					Record struct {
						Result struct {
							CDNURLList []string `json:"cdn_url_list"`
						} `json:"result"`
					} `json:"record"`
				} `json:"data"`
			}

			// 查询生成结果
			_, err := utils.RestyDefaultClient.R().
				SetContext(ctx).
				SetBody(map[string]any{
					"image_tools_id": imageToolsID,
				}).
				SetHeader("cookie", cfg.Monica.Cookie).
				SetResult(&resultData).
				Post(types.ImageResultURL)

			if err != nil {
				return nil, fmt.Errorf("failed to get image generation result: %v", err)
			}

			if resultData.Code != 0 {
				return nil, fmt.Errorf("failed to get image result: %s", resultData.Msg)
			}

			// 检查是否有图片生成完成
			if len(resultData.Data.Record.Result.CDNURLList) > 0 {
				// 构建返回数据
				for _, url := range resultData.Data.Record.Result.CDNURLList {
					generatedImages = append(generatedImages, types.ImageGenerationData{
						URL:           url,
						RevisedPrompt: req.Prompt, // Monica 不提供修改后的提示词
					})
				}

				if req.ResponseFormat == "b64_json" {
					for i := range generatedImages {
						b64, err := downloadAsBase64(ctx, generatedImages[i].URL)
						if err != nil {
							return nil, fmt.Errorf("failed to download generated image: %v", err)
						}
						generatedImages[i].B64JSON = b64
						generatedImages[i].URL = ""
					}
				}

				// 返回结果
				return &types.ImageGenerationResponse{
					Created: time.Now().Unix(),
					Data:    generatedImages,
				}, nil
			}

			// 等待一段时间后继续轮询
			time.Sleep(2 * time.Second)
		}
	}
}

// sizeToAspectRatio 把 OpenAI 的 "WxH" 尺寸（或直接给的 "W:H"）映射到模型支持的最接近宽高比
func sizeToAspectRatio(size string, allowed []string) string {
	target := 1.0
	if w, h, ok := parseRatio(size); ok {
		target = w / h
	}
	best, bestDiff := "1:1", math.MaxFloat64
	for _, r := range allowed {
		w, h, ok := parseRatio(r)
		if !ok {
			continue
		}
		if d := math.Abs(math.Log(w/h) - math.Log(target)); d < bestDiff {
			best, bestDiff = r, d
		}
	}
	return best
}

func parseRatio(s string) (float64, float64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	sep := "x"
	if strings.Contains(s, ":") {
		sep = ":"
	}
	parts := strings.Split(s, sep)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.ParseFloat(parts[0], 64)
	h, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// imageQuality 把 OpenAI quality 映射到 Monica 的 low/medium/high，默认 low（最省积分）
func imageQuality(q string) string {
	switch strings.ToLower(q) {
	case "high", "hd":
		return "high"
	case "medium", "standard":
		return "medium"
	default:
		return "low"
	}
}

func downloadAsBase64(ctx context.Context, url string) (string, error) {
	resp, err := utils.RestyDefaultClient.R().SetContext(ctx).Get(url)
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode())
	}
	return base64.StdEncoding.EncodeToString(resp.Body()), nil
}
