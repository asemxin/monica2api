package apiserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"monica-proxy/internal/config"
	"monica-proxy/internal/cookiestate"
	"monica-proxy/internal/logger"
	"monica-proxy/internal/utils"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

// 管理接口：在线更新 Monica Cookie。
// 需要在环境变量里设置 ADMIN_PASSWORD；未设置时接口关闭。
// 可选：设置 RENDER_API_KEY 后，新 Cookie 会同步写回 Render 的 MONICA_COOKIE 环境变量。

const (
	maxFailsPerIP   = 5
	ipLockDuration  = 15 * time.Minute
	maxFailsGlobal  = 20
	globalWindow    = time.Hour
	cookieCheckTick = 30 * time.Minute
)

type failRecord struct {
	count       int
	lockedUntil time.Time
	first       time.Time
}

type adminLimiter struct {
	mu          sync.Mutex
	perIP       map[string]*failRecord
	globalFails []time.Time
}

var limiter = &adminLimiter{perIP: map[string]*failRecord{}}

// blocked 返回该 IP 是否被锁定以及剩余时间
func (l *adminLimiter) blocked(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if r, ok := l.perIP[ip]; ok && now.Before(r.lockedUntil) {
		return true, r.lockedUntil.Sub(now)
	}
	// 全局：一小时内失败次数过多，暂停所有尝试
	cut := now.Add(-globalWindow)
	kept := l.globalFails[:0]
	for _, t := range l.globalFails {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	l.globalFails = kept
	if len(kept) >= maxFailsGlobal {
		return true, kept[0].Add(globalWindow).Sub(now)
	}
	return false, 0
}

func (l *adminLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	r, ok := l.perIP[ip]
	if !ok || now.Sub(r.first) > ipLockDuration {
		r = &failRecord{first: now}
		l.perIP[ip] = r
	}
	r.count++
	if r.count >= maxFailsPerIP {
		r.lockedUntil = now.Add(ipLockDuration)
		r.count = 0
		r.first = now
	}
	l.globalFails = append(l.globalFails, now)
}

func (l *adminLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.perIP, ip)
}

func passwordMatches(given, expected string) bool {
	g := sha256.Sum256([]byte(given))
	e := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(g[:], e[:]) == 1
}

// normalizeCookie 接受完整的 Cookie 串、带 "cookie:" 前缀的串，或只有 session_id 的值
func normalizeCookie(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(s), "cookie:") {
		s = strings.TrimSpace(s[len("cookie:"):])
	}
	s = strings.NewReplacer("\r", "", "\n", "").Replace(s)
	if s != "" && !strings.Contains(s, "=") {
		s = "session_id=" + s
	}
	return s
}

// checkCookie 用 Monica 额度接口验证 Cookie；返回 state（valid/invalid/unknown）和说明
func checkCookie(cfg *config.Config, cookie string) (string, string) {
	if cookie == "" {
		return "invalid", "未配置 Cookie"
	}
	if _, err := utils.GetMonicaQuotaWithCookie(cfg, cookie); err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "API错误") || strings.Contains(msg, "HTTP错误: 401") || strings.Contains(msg, "HTTP错误: 403") || cookiestate.LooksLikeAuthError(msg) {
			return "invalid", msg
		}
		return "unknown", msg
	}
	return "valid", ""
}

// persistToRender 把新 Cookie 写回 Render 的 MONICA_COOKIE 环境变量（需要 RENDER_API_KEY）
func persistToRender(ctx context.Context, cookie string) (bool, string) {
	apiKey := os.Getenv("RENDER_API_KEY")
	serviceID := os.Getenv("RENDER_SERVICE_ID") // Render 会自动注入
	if apiKey == "" || serviceID == "" {
		return false, "未配置 RENDER_API_KEY，新 Cookie 仅在本次运行期间有效，服务重启后会恢复为 Render 环境变量里的旧值"
	}
	body, _ := json.Marshal(map[string]string{"value": cookie})
	url := fmt.Sprintf("https://api.render.com/v1/services/%s/env-vars/MONICA_COOKIE", serviceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return false, "写回 Render 失败: " + err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return false, "写回 Render 失败: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return false, fmt.Sprintf("写回 Render 失败: HTTP %d", resp.StatusCode)
	}
	return true, "已同步写回 Render 的 MONICA_COOKIE，服务重启后依然有效"
}

func createCookieStatusHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		st := cookiestate.Get()
		return c.JSON(http.StatusOK, map[string]any{
			"status":        st,
			"adminEnabled":  os.Getenv("ADMIN_PASSWORD") != "",
			"renderPersist": os.Getenv("RENDER_API_KEY") != "" && os.Getenv("RENDER_SERVICE_ID") != "",
		})
	}
}

func createCookieCheckHandler(cfg *config.Config) echo.HandlerFunc {
	var mu sync.Mutex
	var last time.Time
	return func(c echo.Context) error {
		mu.Lock()
		recent := time.Since(last) < 30*time.Second
		if !recent {
			last = time.Now()
		}
		mu.Unlock()
		if !recent {
			state, msg := checkCookie(cfg, cfg.MonicaCookie())
			cookiestate.Set(state, msg)
		}
		return c.JSON(http.StatusOK, map[string]any{"status": cookiestate.Get()})
	}
}

func createCookieUpdateHandler(cfg *config.Config) echo.HandlerFunc {
	return func(c echo.Context) error {
		adminPassword := os.Getenv("ADMIN_PASSWORD")
		if adminPassword == "" {
			return c.JSON(http.StatusForbidden, map[string]any{"ok": false, "error": "管理接口未启用：请先在 Render 环境变量里设置 ADMIN_PASSWORD"})
		}
		ip := c.RealIP()
		if blocked, wait := limiter.blocked(ip); blocked {
			return c.JSON(http.StatusTooManyRequests, map[string]any{"ok": false, "error": fmt.Sprintf("密码错误次数过多，请 %d 分钟后再试", int(wait.Minutes())+1)})
		}

		var req struct {
			Password string `json:"password"`
			Cookie   string `json:"cookie"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "请求格式错误"})
		}
		if !passwordMatches(req.Password, adminPassword) {
			limiter.fail(ip)
			logger.Warn("Cookie 管理接口密码错误", zap.String("ip", ip))
			return c.JSON(http.StatusUnauthorized, map[string]any{"ok": false, "error": "管理员密码错误"})
		}
		limiter.success(ip)

		cookie := normalizeCookie(req.Cookie)
		if !strings.Contains(cookie, "session_id=") {
			return c.JSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "Cookie 里没有 session_id，请确认已登录 Monica 并复制了完整的 Cookie"})
		}
		state, msg := checkCookie(cfg, cookie)
		if state != "valid" {
			return c.JSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "新 Cookie 验证失败，未替换: " + msg})
		}

		cfg.SetMonicaCookie(cookie)
		cookiestate.MarkUpdated()
		logger.Info("Monica Cookie 已通过管理接口更新", zap.String("ip", ip))

		ctx, cancel := context.WithTimeout(c.Request().Context(), 25*time.Second)
		defer cancel()
		persisted, note := persistToRender(ctx, cookie)
		return c.JSON(http.StatusOK, map[string]any{
			"ok":        true,
			"message":   "Cookie 已验证并立即生效",
			"persisted": persisted,
			"note":      note,
			"status":    cookiestate.Get(),
		})
	}
}

// StartCookieMonitor 启动后台定期检查 Cookie 是否有效
func StartCookieMonitor(cfg *config.Config) {
	go func() {
		time.Sleep(10 * time.Second)
		for {
			state, msg := checkCookie(cfg, cfg.MonicaCookie())
			cookiestate.Set(state, msg)
			if state == "invalid" {
				logger.Warn("Monica Cookie 已失效", zap.String("reason", msg))
			}
			time.Sleep(cookieCheckTick)
		}
	}()
}
