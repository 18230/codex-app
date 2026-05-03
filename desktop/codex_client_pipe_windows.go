//go:build windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

const codexWindowsPipeName = `\\.\pipe\codex-ipc`

// namedPipeCodexClient 管理 Windows 上通过命名管道传输的 Codex app-server。
type namedPipeCodexClient struct {
	codexClientBase
	cfg       AppConfig
	child     *exec.Cmd
	conn      net.Conn
	ownsChild bool
}

// newNamedPipeCodexClient 创建 Windows 命名管道客户端。
func newNamedPipeCodexClient(cfg AppConfig, logger *GatewayLogger) *namedPipeCodexClient {
	return &namedPipeCodexClient{
		codexClientBase: newCodexClientBase(logger),
		cfg:             cfg,
	}
}

// Start 连接 Codex 命名管道；不存在时启动 app-server 后等待管道就绪。
func (c *namedPipeCodexClient) Start(ctx context.Context) error {
	c.markStarting()

	conn, err := c.dialPipe(ctx, 800*time.Millisecond)
	if err != nil {
		if startErr := c.startAppServer(ctx); startErr != nil {
			return startErr
		}
		conn, err = c.dialPipe(ctx, 30*time.Second)
	}
	if err != nil {
		_ = c.Stop()
		return fmt.Errorf("无法连接 Codex 命名管道 %s: %w", codexWindowsPipeName, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	go c.readLoop(conn)

	if c.logger != nil {
		c.logger.Run(fmt.Sprintf("Codex app-server 已连接: pipe=%s", codexWindowsPipeName))
	}
	_, err = c.Request(ctx, "initialize", JSONObject{
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
	if err := c.writeJSON(JSONObject{"method": "initialized", "params": JSONObject{}}); err != nil {
		_ = c.Stop()
		return err
	}
	return nil
}

// Stop 关闭命名管道；仅停止由网关拉起的 app-server。
func (c *namedPipeCodexClient) Stop() error {
	c.markStopping()
	c.mu.Lock()
	conn := c.conn
	child := c.child
	ownsChild := c.ownsChild
	c.conn = nil
	c.child = nil
	c.ownsChild = false
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	if ownsChild && child != nil && child.Process != nil {
		_ = child.Process.Kill()
	}
	return nil
}

// IsConnected 返回 Codex 命名管道是否可用。
func (c *namedPipeCodexClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Request 发送 JSON-RPC 请求并等待响应。
func (c *namedPipeCodexClient) Request(ctx context.Context, method string, params any, timeout time.Duration) (any, error) {
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

// startAppServer 启动 Windows Codex app-server，让它创建 codex-ipc 命名管道。
func (c *namedPipeCodexClient) startAppServer(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, c.cfg.CodexBinary, "app-server", "--analytics-default-enabled")
	cmd.Env = os.Environ()
	cmd.Stdout = io.MultiWriter(&c.output, newLogWriter(c.logger, logKindRun, "codex"))
	cmd.Stderr = io.MultiWriter(&c.output, newLogWriter(c.logger, logKindError, "codex"))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Codex app-server 失败: %w", err)
	}
	c.mu.Lock()
	c.child = cmd
	c.ownsChild = true
	c.mu.Unlock()
	go c.waitChild(cmd)
	if c.logger != nil {
		c.logger.Run(fmt.Sprintf("Codex app-server 已启动: pipe=%s", codexWindowsPipeName))
	}
	return nil
}

// dialPipe 在限定时间内等待 Windows 命名管道就绪。
func (c *namedPipeCodexClient) dialPipe(ctx context.Context, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		dialTimeout := 250 * time.Millisecond
		conn, err := winio.DialPipe(codexWindowsPipeName, &dialTimeout)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// writeJSON 通过命名管道写出一条 JSONL 消息。
func (c *namedPipeCodexClient) writeJSON(message JSONObject) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("Codex 命名管道已关闭")
	}
	if _, err := c.conn.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

// readLoop 读取命名管道 JSONL，分发响应和通知。
func (c *namedPipeCodexClient) readLoop(conn net.Conn) {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var message JSONObject
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			if c.logger != nil {
				c.logger.Error(fmt.Sprintf("Codex 命名管道输出解析失败: %v", err))
			}
			continue
		}
		c.handleMessage(message, c.writeJSON)
	}
	if err := scanner.Err(); err != nil {
		c.notifyReadFailure(conn, err)
		return
	}
	c.notifyReadFailure(conn, io.EOF)
}

// notifyReadFailure 处理命名管道断开。
func (c *namedPipeCodexClient) notifyReadFailure(conn net.Conn, err error) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	if c.stopping {
		c.mu.Unlock()
		return
	}
	failure := fmt.Errorf("Codex 命名管道已关闭: %w", err)
	c.rejectAllLocked(failure)
	handlers := append([]lifecycleHandler{}, c.failureHandlers...)
	c.mu.Unlock()
	if c.logger != nil {
		c.logger.Error(failure)
	}
	c.notifyFailureHandlers(handlers, failure)
}

// waitChild 等待由网关启动的 app-server 退出。
func (c *namedPipeCodexClient) waitChild(child *exec.Cmd) {
	err := child.Wait()
	c.mu.Lock()
	if c.child == child {
		c.child = nil
		c.ownsChild = false
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
