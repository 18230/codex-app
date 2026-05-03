package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// websocketCodexClient 管理 Codex app-server 子进程和 JSON-RPC WebSocket。
type websocketCodexClient struct {
	codexClientBase
	cfg   AppConfig
	child *exec.Cmd
	conn  *websocket.Conn
}

// NewCodexClient 创建 macOS WebSocket 传输客户端。
func NewCodexClient(cfg AppConfig, logger *GatewayLogger) CodexClient {
	return newWebSocketCodexClient(cfg, logger)
}

// newWebSocketCodexClient 创建 WebSocket 传输客户端，供 macOS 桌面网关使用。
func newWebSocketCodexClient(cfg AppConfig, logger *GatewayLogger) *websocketCodexClient {
	return &websocketCodexClient{
		codexClientBase: newCodexClientBase(logger),
		cfg:             cfg,
	}
}

// Start 启动 app-server 并完成 initialize 握手。
func (c *websocketCodexClient) Start(ctx context.Context) error {
	c.markStarting()

	listenURL := fmt.Sprintf("ws://%s:%d", c.cfg.CodexHost, c.cfg.CodexPort)
	cmd := exec.CommandContext(ctx, c.cfg.CodexBinary, "app-server", "--listen", listenURL)
	cmd.Env = os.Environ()
	cmd.Stdout = io.MultiWriter(&c.output, newLogWriter(c.logger, logKindRun, "codex"))
	cmd.Stderr = io.MultiWriter(&c.output, newLogWriter(c.logger, logKindError, "codex"))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Codex app-server 失败: %w", err)
	}
	if c.logger != nil {
		c.logger.Run(fmt.Sprintf("Codex app-server 已启动: %s", listenURL))
	}
	c.child = cmd
	go c.waitChild(cmd)

	if err := c.connectWithRetry(ctx); err != nil {
		_ = c.Stop()
		return err
	}
	_, err := c.Request(ctx, "initialize", JSONObject{
		"clientInfo": JSONObject{
			"name":    "codex-mobile-gateway-desktop",
			"title":   "Codex Mobile Gateway",
			"version": "0.1.0",
		},
		"capabilities": JSONObject{
			"experimentalApi":           true,
			"optOutNotificationMethods": nil,
		},
	}, 30*time.Second)
	if err != nil {
		_ = c.Stop()
		return err
	}
	return nil
}

// Stop 关闭 app-server 和 WebSocket。
func (c *websocketCodexClient) Stop() error {
	c.markStopping()
	c.mu.Lock()
	conn := c.conn
	child := c.child
	c.conn = nil
	c.child = nil
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if child != nil && child.Process != nil {
		_ = child.Process.Kill()
	}
	return nil
}

// IsConnected 返回 app-server WebSocket 是否可用。
func (c *websocketCodexClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && c.child != nil
}

// Request 发送 JSON-RPC 请求并等待响应。
func (c *websocketCodexClient) Request(ctx context.Context, method string, params any, timeout time.Duration) (any, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("Codex app-server 尚未连接")
	}

	id, ch := c.registerRequest(timeout)
	payload := JSONObject{"id": id, "method": method, "params": params}
	startedAt := time.Now()
	if err := c.writeJSON(payload); err != nil {
		c.unregisterRequest(id)
		if c.logger != nil {
			c.logger.Error(fmt.Sprintf("Codex 请求发送失败: method=%s error=%v", method, err))
		}
		return nil, err
	}
	return c.waitResponse(ctx, method, startedAt, ch)
}

// writeJSON 串行写出 WebSocket JSON 消息。
func (c *websocketCodexClient) writeJSON(message JSONObject) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("Codex app-server 连接已关闭")
	}
	return c.conn.WriteJSON(message)
}

// connectWithRetry 等待 app-server 监听端口就绪。
func (c *websocketCodexClient) connectWithRetry(ctx context.Context) error {
	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("%s:%d", c.cfg.CodexHost, c.cfg.CodexPort)}
	var lastErr error
	for i := 0; i < 40; i++ {
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
		if err == nil {
			c.mu.Lock()
			c.conn = conn
			c.mu.Unlock()
			go c.readLoop(conn)
			return nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("无法连接 Codex app-server: %w", lastErr)
}

// readLoop 处理 WebSocket JSON-RPC 响应、通知和审批请求。
func (c *websocketCodexClient) readLoop(conn *websocket.Conn) {
	for {
		var message JSONObject
		if err := conn.ReadJSON(&message); err != nil {
			c.mu.Lock()
			notifyFailure := false
			failure := fmt.Errorf("Codex app-server 连接已关闭: %w", err)
			if c.conn == conn {
				c.conn = nil
			}
			if !c.stopping {
				notifyFailure = true
				c.rejectAllLocked(failure)
			}
			handlers := append([]lifecycleHandler{}, c.failureHandlers...)
			c.mu.Unlock()
			if notifyFailure {
				c.notifyFailureHandlers(handlers, failure)
			}
			return
		}
		c.handleMessage(message, c.writeJSON)
	}
}

// waitChild 等待子进程退出并清理等待中的请求。
func (c *websocketCodexClient) waitChild(child *exec.Cmd) {
	err := child.Wait()
	c.mu.Lock()
	if c.child == child {
		c.child = nil
	}
	notifyFailure := false
	var failure error
	if !c.stopping {
		if c.conn != nil {
			_ = c.conn.Close()
			c.conn = nil
		}
		message := fmt.Sprintf("Codex app-server 已退出: %s", redactSecrets(err))
		if detail := strings.TrimSpace(c.output.String()); detail != "" {
			message = fmt.Sprintf("%s: %s", message, redactSecrets(detail))
		}
		failure = fmt.Errorf("%s", message)
		notifyFailure = true
		c.rejectAllLocked(failure)
	}
	handlers := append([]lifecycleHandler{}, c.failureHandlers...)
	c.mu.Unlock()
	if notifyFailure {
		if c.logger != nil {
			c.logger.Error(failure)
		}
		c.notifyFailureHandlers(handlers, failure)
	}
}
