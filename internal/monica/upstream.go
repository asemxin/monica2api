package monica

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"monica-proxy/internal/cookiestate"
	"monica-proxy/internal/errors"
	"os"
	"strings"

	"github.com/go-resty/resty/v2"
)

type peekedBody struct {
	io.Reader
	io.Closer
}

// validateUpstreamSSE makes sure Monica actually answered with an SSE stream.
// When Monica rejects a request (expired cookie, permission error, quota ...)
// it may answer with a non-2xx status or a plain JSON error body. Without this
// check the SSE parser silently skips those lines and the client receives an
// empty 200 response, which OpenAI SDKs report as finish_reason "other".
func validateUpstreamSSE(resp *resty.Response) error {
	if resp == nil || resp.RawResponse == nil || resp.RawResponse.Body == nil {
		return errors.NewRequestFailedError("Monica 返回了空响应", nil)
	}
	body := resp.RawResponse.Body

	if resp.StatusCode() >= 400 {
		data, _ := io.ReadAll(io.LimitReader(body, 4096))
		body.Close()
		msg := withCookieHint(fmt.Sprintf("Monica HTTP %d: %s", resp.StatusCode(), upstreamErrorMessage(data)))
		return errors.NewRequestFailedError(msg, nil)
	}

	br := bufio.NewReaderSize(body, bufferSize)
	var consumed bytes.Buffer
	for consumed.Len() < 64*1024 {
		line, err := br.ReadBytes('\n')
		consumed.Write(line)
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			if isSSELine(trimmed) {
				break
			}
			if trimmed[0] == '{' || trimmed[0] == '[' {
				rest, _ := io.ReadAll(io.LimitReader(br, 4096))
				body.Close()
				return errors.NewRequestFailedError(withCookieHint("Monica 错误: "+upstreamErrorMessage(append(consumed.Bytes(), rest...))), nil)
			}
			break
		}
		if err != nil {
			if consumed.Len() == 0 {
				body.Close()
				return errors.NewRequestFailedError(forceCookieHint("Monica 返回了空响应"), nil)
			}
			break
		}
	}

	resp.RawResponse.Body = peekedBody{Reader: io.MultiReader(bytes.NewReader(consumed.Bytes()), br), Closer: body}
	return nil
}

func isSSELine(line []byte) bool {
	for _, p := range []string{"data:", "event:", "id:", "retry:", ":"} {
		if bytes.HasPrefix(line, []byte(p)) {
			return true
		}
	}
	return false
}

func upstreamErrorMessage(data []byte) string {
	var raw map[string]any
	if json.Unmarshal(bytes.TrimSpace(data), &raw) == nil {
		for _, k := range []string{"msg", "message", "error", "detail", "error_msg"} {
			switch v := raw[k].(type) {
			case string:
				if v != "" {
					return v
				}
			case map[string]any:
				if m, ok := v["message"].(string); ok && m != "" {
					return m
				}
			}
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	if s == "" {
		s = "empty body"
	}
	return s
}

// withCookieHint 如果错误看起来是 Cookie 失效，记录状态并附上更新提示
func withCookieHint(msg string) string {
	if !cookiestate.LooksLikeAuthError(msg) {
		return msg
	}
	cookiestate.Set("invalid", msg)
	return msg + "。" + cookiestate.UpdateHint(os.Getenv("RENDER_EXTERNAL_URL"))
}

func forceCookieHint(msg string) string {
	cookiestate.Set("invalid", msg)
	return msg + "。" + cookiestate.UpdateHint(os.Getenv("RENDER_EXTERNAL_URL"))
}
