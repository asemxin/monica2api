package config

import "sync/atomic"

// runtimeCookie 保存运行时更新的 Monica Cookie（通过管理接口更新，无需重启）
var runtimeCookie atomic.Pointer[string]

// MonicaCookie 返回当前生效的 Monica Cookie：优先使用运行时更新的值，否则使用启动配置
func (c *Config) MonicaCookie() string {
	if p := runtimeCookie.Load(); p != nil {
		return *p
	}
	return c.Monica.Cookie
}

// SetMonicaCookie 在运行时替换 Monica Cookie，对之后的所有请求立即生效
func (c *Config) SetMonicaCookie(cookie string) {
	runtimeCookie.Store(&cookie)
}
