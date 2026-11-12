// Package cookiestate 记录 Monica Cookie 最近一次的有效性检查结果
package cookiestate

import (
	"strings"
	"sync"
	"time"
)

// Status 是 Cookie 的状态快照（不包含 Cookie 本身）
type Status struct {
	State     string    `json:"state"` // valid / invalid / unknown
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"` // Cookie 最近一次通过管理接口更新的时间
}

var (
	mu     sync.RWMutex
	status = Status{State: "unknown"}
)

// Get 返回当前状态
func Get() Status {
	mu.RLock()
	defer mu.RUnlock()
	return status
}

// Set 记录一次检查结果
func Set(state, message string) {
	mu.Lock()
	defer mu.Unlock()
	status.State = state
	status.Message = message
	status.CheckedAt = time.Now()
}

// MarkUpdated 记录 Cookie 已被更新且验证有效
func MarkUpdated() {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	status = Status{State: "valid", CheckedAt: now, UpdatedAt: now}
}

// LooksLikeAuthError 判断 Monica 的错误信息是否意味着 Cookie 失效
func LooksLikeAuthError(msg string) bool {
	m := strings.ToLower(msg)
	for _, k := range []string{"permission", "unauthorized", "not login", "login required", "please login", "token", "401", "403"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// UpdateHint 返回提示用户更新 Cookie 的文案
func UpdateHint(baseURL string) string {
	if baseURL == "" {
		return "Monica Cookie 可能已失效，请打开代理网页更新 Cookie"
	}
	return "Monica Cookie 可能已失效，请打开 " + baseURL + " 更新 Cookie"
}
