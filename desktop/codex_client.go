package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
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

// CodexClient 抽象 Codex app-server 通信，当前桌面网关仅支持 macOS WebSocket 模式。
type CodexClient interface {
	OnNotification(notificationHandler)
	OnFailure(lifecycleHandler)
	Start(context.Context) error
	Stop() error
	IsConnected() bool
	Request(context.Context, string, any, time.Duration) (any, error)
}

type codexClientBase struct {
	output          bytes.Buffer
	nextID          int
	stopping        bool
	mu              sync.Mutex
	pending         map[int]pendingRequest
	handlers        []notificationHandler
	failureHandlers []lifecycleHandler
	logger          *GatewayLogger
}

// newCodexClientBase 创建跨传输共享的 JSON-RPC 状态。
func newCodexClientBase(logger *GatewayLogger) codexClientBase {
	return codexClientBase{
		nextID:  1,
		pending: make(map[int]pendingRequest),
		logger:  logger,
	}
}

// OnNotification 注册 app-server 通知监听器。
func (c *codexClientBase) OnNotification(handler notificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, handler)
}

// OnFailure 注册 app-server 异常退出或连接断开的监听器。
func (c *codexClientBase) OnFailure(handler lifecycleHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failureHandlers = append(c.failureHandlers, handler)
}

// markStarting 标记一次新的 app-server 生命周期开始。
func (c *codexClientBase) markStarting() {
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()
}

// markStopping 标记客户端正在主动停止，并拒绝所有等待中的请求。
func (c *codexClientBase) markStopping() {
	c.mu.Lock()
	c.stopping = true
	c.rejectAllLocked(fmt.Errorf("CodexClient 已关闭"))
	c.mu.Unlock()
}

// registerRequest 注册一个等待中的 JSON-RPC 请求。
func (c *codexClientBase) registerRequest(timeout time.Duration) (int, chan rpcResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	ch := make(chan rpcResponse, 1)
	timer := time.AfterFunc(timeout, func() {
		c.mu.Lock()
		if pending, ok := c.pending[id]; ok {
			delete(c.pending, id)
			pending.resolve <- rpcResponse{err: fmt.Errorf("Codex 请求超时")}
		}
		c.mu.Unlock()
	})
	c.pending[id] = pendingRequest{resolve: ch, timer: timer}
	return id, ch
}

// unregisterRequest 移除未发送成功的请求。
func (c *codexClientBase) unregisterRequest(id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pending, ok := c.pending[id]; ok {
		delete(c.pending, id)
		pending.timer.Stop()
	}
}

// waitResponse 统一记录请求耗时和错误。
func (c *codexClientBase) waitResponse(ctx context.Context, method string, startedAt time.Time, ch chan rpcResponse) (any, error) {
	select {
	case response := <-ch:
		if c.logger != nil {
			duration := time.Since(startedAt).Round(time.Millisecond)
			if response.err != nil {
				c.logger.Request(fmt.Sprintf("codex method=%s status=error duration=%s", method, duration))
				c.logger.Error(fmt.Sprintf("Codex 请求失败: method=%s error=%v", method, response.err))
			} else {
				c.logger.Request(fmt.Sprintf("codex method=%s status=ok duration=%s", method, duration))
			}
		}
		return response.result, response.err
	case <-ctx.Done():
		if c.logger != nil {
			c.logger.Request(fmt.Sprintf("codex method=%s status=context_done duration=%s", method, time.Since(startedAt).Round(time.Millisecond)))
			c.logger.Error(fmt.Sprintf("Codex 请求被取消: method=%s error=%v", method, ctx.Err()))
		}
		return nil, ctx.Err()
	}
}

// handleMessage 分发 app-server 消息。
func (c *codexClientBase) handleMessage(message JSONObject, respond func(JSONObject) error) {
	if idFloat, ok := message["id"].(float64); ok {
		id := int(idFloat)
		if method, hasMethod := message["method"].(string); hasMethod {
			c.answerServerRequest(id, method, respond)
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
func (c *codexClientBase) answerServerRequest(id int, method string, respond func(JSONObject) error) {
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
	_ = respond(JSONObject{"id": id, "result": result})
}

// notifyFailureHandlers 在锁外广播生命周期异常，避免重启路径反向等待 CodexClient 锁。
func (c *codexClientBase) notifyFailureHandlers(handlers []lifecycleHandler, err error) {
	for _, handler := range handlers {
		handler(err)
	}
}

// rejectAllLocked 拒绝所有等待中的请求。调用方必须持有锁。
func (c *codexClientBase) rejectAllLocked(err error) {
	for id, pending := range c.pending {
		delete(c.pending, id)
		pending.timer.Stop()
		pending.resolve <- rpcResponse{err: err}
	}
}

type logWriter struct {
	logger *GatewayLogger
	kind   string
	prefix string
}

// newLogWriter 把子进程输出逐行写入网关日志。
func newLogWriter(logger *GatewayLogger, kind string, prefix string) io.Writer {
	return &logWriter{logger: logger, kind: kind, prefix: prefix}
}

// Write 实现 io.Writer，用于接收 Codex app-server 标准输出和错误输出。
func (w *logWriter) Write(p []byte) (int, error) {
	if w.logger == nil {
		return len(p), nil
	}
	for _, line := range strings.Split(string(p), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		message := line
		if w.prefix != "" {
			message = w.prefix + " " + message
		}
		if w.kind == logKindError {
			w.logger.Error(message)
		} else {
			w.logger.Run(message)
		}
	}
	return len(p), nil
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
