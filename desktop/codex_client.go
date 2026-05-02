package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type pendingRequest struct {
	resolve chan rpcResponse
	timer   *time.Timer
}

type rpcResponse struct {
	result any
	err    error
}

type notificationHandler func(JSONObject)
type lifecycleHandler func(error)

// CodexClient 管理 Codex app-server 子进程和 JSON-RPC WebSocket。
type CodexClient struct {
	cfg             AppConfig
	child           *exec.Cmd
	output          bytes.Buffer
	conn            *websocket.Conn
	nextID          int
	stopping        bool
	mu              sync.Mutex
	pending         map[int]pendingRequest
	handlers        []notificationHandler
	failureHandlers []lifecycleHandler
}

// NewCodexClient 创建 Codex 客户端。
func NewCodexClient(cfg AppConfig) *CodexClient {
	return &CodexClient{
		cfg:     cfg,
		nextID:  1,
		pending: make(map[int]pendingRequest),
	}
}

// OnNotification 注册 app-server 通知监听器。
func (c *CodexClient) OnNotification(handler notificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, handler)
}

// OnFailure 注册 app-server 异常退出或连接断开的监听器。
func (c *CodexClient) OnFailure(handler lifecycleHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failureHandlers = append(c.failureHandlers, handler)
}

// Start 启动 app-server 并完成 initialize 握手。
func (c *CodexClient) Start(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()

	listenURL := fmt.Sprintf("ws://%s:%d", c.cfg.CodexHost, c.cfg.CodexPort)
	cmd := exec.CommandContext(ctx, c.cfg.CodexBinary, "app-server", "--listen", listenURL)
	cmd.Env = os.Environ()
	cmd.Stdout = &c.output
	cmd.Stderr = &c.output
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Codex app-server 失败: %w", err)
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
func (c *CodexClient) Stop() error {
	c.mu.Lock()
	c.stopping = true
	conn := c.conn
	child := c.child
	c.conn = nil
	c.child = nil
	c.rejectAllLocked(fmt.Errorf("CodexClient 已关闭"))
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
func (c *CodexClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && c.child != nil
}

// Request 发送 JSON-RPC 请求并等待响应。
func (c *CodexClient) Request(ctx context.Context, method string, params any, timeout time.Duration) (any, error) {
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("Codex app-server 尚未连接")
	}
	id := c.nextID
	c.nextID++
	ch := make(chan rpcResponse, 1)
	timer := time.AfterFunc(timeout, func() {
		c.mu.Lock()
		if pending, ok := c.pending[id]; ok {
			delete(c.pending, id)
			pending.resolve <- rpcResponse{err: fmt.Errorf("Codex 请求超时: %s", method)}
		}
		c.mu.Unlock()
	})
	c.pending[id] = pendingRequest{resolve: ch, timer: timer}
	payload := JSONObject{"id": id, "method": method, "params": params}
	err := c.conn.WriteJSON(payload)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case response := <-ch:
		return response.result, response.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// connectWithRetry 等待 app-server 监听端口就绪。
func (c *CodexClient) connectWithRetry(ctx context.Context) error {
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

// readLoop 处理 JSON-RPC 响应、通知和审批请求。
func (c *CodexClient) readLoop(conn *websocket.Conn) {
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
		c.handleMessage(message)
	}
}

// handleMessage 分发 app-server 消息。
func (c *CodexClient) handleMessage(message JSONObject) {
	if idFloat, ok := message["id"].(float64); ok {
		id := int(idFloat)
		if method, hasMethod := message["method"].(string); hasMethod {
			c.answerServerRequest(id, method)
			return
		}
		c.mu.Lock()
		pending, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
			pending.timer.Stop()
		}
		c.mu.Unlock()
		if ok {
			if rawErr, exists := message["error"]; exists {
				pending.resolve <- rpcResponse{err: fmt.Errorf("%s", redactSecrets(rawErr))}
			} else {
				pending.resolve <- rpcResponse{result: message["result"]}
			}
		}
		return
	}

	if _, ok := message["method"].(string); ok {
		c.mu.Lock()
		handlers := append([]notificationHandler{}, c.handlers...)
		c.mu.Unlock()
		for _, handler := range handlers {
			handler(message)
		}
	}
}

// answerServerRequest 自动接受 app-server 的权限审批请求。
func (c *CodexClient) answerServerRequest(id int, method string) {
	var result any
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
		result = JSONObject{"decision": "acceptForSession"}
	case "execCommandApproval", "applyPatchApproval":
		result = JSONObject{"decision": "approved_for_session"}
	default:
		result = nil
	}
	if result == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.WriteJSON(JSONObject{"id": id, "result": result})
	}
}

// waitChild 等待子进程退出并清理等待中的请求。
func (c *CodexClient) waitChild(child *exec.Cmd) {
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
		c.notifyFailureHandlers(handlers, failure)
	}
}

// notifyFailureHandlers 在锁外广播生命周期异常，避免重启路径反向等待 CodexClient 锁。
func (c *CodexClient) notifyFailureHandlers(handlers []lifecycleHandler, err error) {
	for _, handler := range handlers {
		handler(err)
	}
}

// rejectAllLocked 拒绝所有等待中的请求。调用方必须持有锁。
func (c *CodexClient) rejectAllLocked(err error) {
	for id, pending := range c.pending {
		delete(c.pending, id)
		pending.timer.Stop()
		pending.resolve <- rpcResponse{err: err}
	}
}

// base64Text 解码 Codex 命令输出里的 base64 delta。
func base64Text(input string) string {
	if input == "" {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return ""
	}
	return string(data)
}

// jsonObject 尝试把任意 JSON 值转换为对象。
func jsonObject(value any) JSONObject {
	if value == nil {
		return nil
	}
	if object, ok := value.(map[string]any); ok {
		return object
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var object JSONObject
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	return object
}
