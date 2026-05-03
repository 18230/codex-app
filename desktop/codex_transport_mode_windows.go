//go:build windows

package main

// usesCodexTCPPort 表示当前平台的 app-server 传输是否依赖本地 TCP 端口。
func usesCodexTCPPort() bool {
	return false
}
