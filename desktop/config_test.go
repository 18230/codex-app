package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

// TestValidateConfigTargetsChecksCodexBinary 验证保存和启动前会检查 Codex 可执行文件真实可用。
func TestValidateConfigTargetsChecksCodexBinary(t *testing.T) {
	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "codex-test")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write temp executable: %v", err)
	}
	cfg, err := ValidateConfigTargets(AppConfig{Workspace: tempDir, CodexBinary: binaryPath})
	if err != nil {
		t.Fatalf("ValidateConfigTargets returned error: %v", err)
	}
	if cfg.CodexBinary != binaryPath {
		t.Fatalf("expected codex binary %q, got %q", binaryPath, cfg.CodexBinary)
	}
	if _, err := ValidateConfigTargets(AppConfig{Workspace: tempDir, CodexBinary: filepath.Join(tempDir, "missing")}); err == nil {
		t.Fatalf("expected missing codex binary to be rejected")
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

// TestGatewayStatusDoesNotExposeConnectionURL 验证状态快照不会回显带 token 的连接地址。
func TestGatewayStatusDoesNotExposeConnectionURL(t *testing.T) {
	tempDir := t.TempDir()
	gateway := NewGateway(&ConfigStore{path: filepath.Join(tempDir, "config.json")})
	gateway.cfg = AppConfig{
		Workspace:             tempDir,
		Token:                 "1234567890abcdef",
		LastConnectionBaseURL: "https://example.com/ws",
	}
	status := gateway.Status()
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "1234567890abcdef") || strings.Contains(text, "connectionUrl") {
		t.Fatalf("status leaked connection detail: %s", text)
	}
}

// TestRedactSecretsMasksTokenQuery 验证错误脱敏会清理 URL query 中的 token。
func TestRedactSecretsMasksTokenQuery(t *testing.T) {
	redacted := redactSecrets("connect wss://example.com/ws?token=1234567890abcdef&x=1 failed")
	if strings.Contains(redacted, "1234567890abcdef") {
		t.Fatalf("token was not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "token=<redacted>") {
		t.Fatalf("expected redacted marker, got: %s", redacted)
	}
}

// TestResolveClientWorkspaceRestrictsToConfiguredWorkspace 验证手机端只能使用桌面配置目录。
func TestResolveClientWorkspaceRestrictsToConfiguredWorkspace(t *testing.T) {
	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	gateway := NewGateway(&ConfigStore{path: filepath.Join(workspace, "config.json")})
	gateway.cfg = AppConfig{Workspace: workspace}
	gateway.currentCWD = workspace

	resolved, err := gateway.resolveClientWorkspace("")
	if err != nil {
		t.Fatalf("resolve empty workspace: %v", err)
	}
	if resolved != workspace {
		t.Fatalf("expected default workspace %q, got %q", workspace, resolved)
	}
	if _, err := gateway.resolveClientWorkspace(workspace); err != nil {
		t.Fatalf("expected configured workspace to pass: %v", err)
	}
	if _, err := gateway.resolveClientWorkspace(otherWorkspace); err == nil {
		t.Fatalf("expected other workspace to be rejected")
	}
}

// TestClientStateSerializesConcurrentSends 验证同一连接的并发发送会经过队列串行写出。
func TestClientStateSerializesConcurrentSends(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverConn := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConn <- conn
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer clientConn.Close()

	var conn *websocket.Conn
	select {
	case conn = <-serverConn:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for server websocket")
	}
	state := newClientState(conn)
	go state.writeLoop()
	defer state.close()

	const messages = 50
	var wg sync.WaitGroup
	wg.Add(messages)
	for i := 0; i < messages; i++ {
		go func(index int) {
			defer wg.Done()
			if !state.sendJSON(JSONObject{"type": "test", "index": index}) {
				t.Errorf("sendJSON returned false for %d", index)
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < messages; i++ {
		var message JSONObject
		if err := clientConn.ReadJSON(&message); err != nil {
			t.Fatalf("read message %d: %v", i, err)
		}
		if stringField(message, "type") != "test" {
			t.Fatalf("unexpected message: %#v", message)
		}
	}
}
