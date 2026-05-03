//go:build !windows

package main

// NewCodexClient 创建当前平台的 Codex app-server 客户端。
func NewCodexClient(cfg AppConfig, logger *GatewayLogger) CodexClient {
	return newWebSocketCodexClient(cfg, logger)
}
