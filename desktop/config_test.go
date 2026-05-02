package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestNormalizeConfigDefaults 验证配置默认值和工作目录解析，避免迁移后端口或心跳落空。
func TestNormalizeConfigDefaults(t *testing.T) {
	tempDir := t.TempDir()
	cfg, err := NormalizeConfig(AppConfig{
		Workspace:   tempDir,
		Token:       "1234567890abcdef",
		CodexBinary: "codex",
	})
	if err != nil {
		t.Fatalf("NormalizeConfig returned error: %v", err)
	}
	if cfg.Port != defaultPort {
		t.Fatalf("expected default port %d, got %d", defaultPort, cfg.Port)
	}
	if cfg.ClientPingIntervalMs != defaultPingIntervalMs {
		t.Fatalf("expected default ping interval")
	}
	if cfg.ClientIdleTimeoutMs != defaultIdleTimeoutMs {
		t.Fatalf("expected default idle timeout")
	}
	if cfg.Workspace != tempDir {
		t.Fatalf("expected workspace %q, got %q", tempDir, cfg.Workspace)
	}
}

// TestNormalizeConfigRejectsInvalidWorkspace 验证工作目录必须存在且必须是目录。
func TestNormalizeConfigRejectsInvalidWorkspace(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_, err := NormalizeConfig(AppConfig{
		Workspace:   filePath,
		Token:       "1234567890abcdef",
		CodexBinary: "codex",
	})
	if err == nil {
		t.Fatalf("expected error for file workspace")
	}
}

// TestResolveCodexBinaryAcceptsExecutablePath 验证显式可执行路径会被保留，避免 GUI 环境 PATH 缺失时无法启动。
func TestResolveCodexBinaryAcceptsExecutablePath(t *testing.T) {
	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "codex-test")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write temp executable: %v", err)
	}
	resolved, err := ResolveCodexBinary(binaryPath)
	if err != nil {
		t.Fatalf("ResolveCodexBinary returned error: %v", err)
	}
	if resolved != binaryPath {
		t.Fatalf("expected %q, got %q", binaryPath, resolved)
	}
}

// TestFreeTCPPort 验证内部 app-server 端口冲突时可以分配新的可用端口。
func TestFreeTCPPort(t *testing.T) {
	port, err := freeTCPPort("127.0.0.1")
	if err != nil {
		t.Fatalf("freeTCPPort returned error: %v", err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("expected allocated port to be reusable: %v", err)
	}
	_ = listener.Close()
}

// TestThreadSummaries 验证 thread/list 返回能转换为桌面列表结构。
func TestThreadSummaries(t *testing.T) {
	threads := threadSummaries(JSONObject{
		"data": []any{
			JSONObject{"id": "thread-1", "name": "会话一", "cwd": "/tmp/a", "updatedAt": float64(10)},
			JSONObject{"id": "thread-2", "preview": "预览二", "cwd": "/tmp/b", "updatedAt": float64(20)},
		},
	})
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(threads))
	}
	if threads[0].Name != "会话一" {
		t.Fatalf("unexpected first name: %s", threads[0].Name)
	}
	if threads[1].Name != "预览二" {
		t.Fatalf("unexpected fallback name: %s", threads[1].Name)
	}
}
