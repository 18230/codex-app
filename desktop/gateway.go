package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type eventSink func(string, any)

type clientState struct {
	conn       *websocket.Conn
	lastSeenAt time.Time
	send       chan JSONObject
	done       chan struct{}
	closeOnce  sync.Once
	mu         sync.Mutex
	closed     bool
}

// Gateway 负责本地 HTTP/WebSocket 服务、Codex app-server 转发和线程状态。
type Gateway struct {
	store      *ConfigStore
	cfg        AppConfig
	codex      *CodexClient
	server     *http.Server
	clients    map[*websocket.Conn]*clientState
	currentID  string
	currentCWD string
	activeTurn string
	lastError  string
	eventSink  eventSink
	mu         sync.Mutex
}

// NewGateway 创建网关实例。
func NewGateway(store *ConfigStore) *Gateway {
	return &Gateway{
		store:   store,
		clients: make(map[*websocket.Conn]*clientState),
	}
}

// newClientState 创建单个手机端连接的发送队列，保证同一 WebSocket 只有一个写协程。
func newClientState(conn *websocket.Conn) *clientState {
	return &clientState{
		conn:       conn,
		lastSeenAt: time.Now(),
		send:       make(chan JSONObject, 128),
		done:       make(chan struct{}),
	}
}

// sendJSON 将消息放入发送队列，避免请求响应和广播并发写同一个连接。
func (c *clientState) sendJSON(message JSONObject) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.send <- message:
		return true
	default:
		return false
	}
}

// close 关闭连接和写协程；调用方负责从 Gateway.clients 中移除。
func (c *clientState) close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		close(c.done)
		c.mu.Unlock()
		_ = c.conn.Close()
	})
}

// writeLoop 串行写出所有业务消息。
func (c *clientState) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case message := <-c.send:
			if err := c.conn.WriteJSON(message); err != nil {
				c.close()
				return
			}
		}
	}
}

// SetEventSink 设置桌面前端事件推送器。
func (g *Gateway) SetEventSink(sink eventSink) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.eventSink = sink
}

// Start 启动网关和 Codex app-server。
func (g *Gateway) Start() (GatewayStatus, error) {
	g.mu.Lock()
	if g.server != nil {
		status := g.statusLocked()
		g.mu.Unlock()
		return status, nil
	}
	g.mu.Unlock()

	cfg, err := g.store.Load()
	if err != nil {
		return g.Status(), err
	}
	cfg, err = ValidateConfigTargets(cfg)
	if err != nil {
		g.setError(err)
		return g.Status(), err
	}
	if !isTCPPortAvailable(cfg.CodexHost, cfg.CodexPort) {
		port, err := freeTCPPort(cfg.CodexHost)
		if err != nil {
			return g.Status(), err
		}
		cfg.CodexPort = port
		_ = g.store.Save(cfg)
	}

	ctx := context.Background()
	codex := NewCodexClient(cfg)
	codex.OnNotification(g.handleCodexNotification)
	if err := codex.Start(ctx); err != nil {
		g.setError(err)
		return g.Status(), err
	}

	g.mu.Lock()
	g.cfg = cfg
	g.codex = codex
	g.currentCWD = cfg.Workspace
	g.currentID = cfg.BoundThreadID
	g.lastError = ""
	g.mu.Unlock()

	if err := g.initializeThread(ctx); err != nil {
		_ = codex.Stop()
		g.setError(err)
		return g.Status(), err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", g.handleHealth)
	mux.HandleFunc("/ws", g.handleWebSocket)
	mux.HandleFunc("/", g.handleIndex)

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		_ = codex.Stop()
		g.setError(err)
		return g.Status(), fmt.Errorf("监听端口失败: %w", err)
	}

	g.mu.Lock()
	g.server = server
	g.mu.Unlock()
	go g.clientHeartbeat()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			g.setError(err)
		}
	}()
	g.emit("gateway:status", g.Status())
	return g.Status(), nil
}

// isTCPPortAvailable 判断内部 Codex app-server 端口是否可绑定。
func isTCPPortAvailable(host string, port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// freeTCPPort 获取一个空闲内部端口，避免残留 app-server 占用固定端口导致启动失败。
func freeTCPPort(host string) (int, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:0", host))
	if err != nil {
		return 0, fmt.Errorf("分配 Codex app-server 端口失败: %w", err)
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("无法读取 Codex app-server 端口")
	}
	return addr.Port, nil
}

// Stop 停止网关和所有子连接。
func (g *Gateway) Stop() error {
	g.mu.Lock()
	server := g.server
	codex := g.codex
	clients := make([]*clientState, 0, len(g.clients))
	for _, client := range g.clients {
		clients = append(clients, client)
	}
	g.server = nil
	g.codex = nil
	g.clients = make(map[*websocket.Conn]*clientState)
	g.activeTurn = ""
	g.mu.Unlock()

	for _, client := range clients {
		client.close()
	}
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
	}
	if codex != nil {
		_ = codex.Stop()
	}
	g.emit("gateway:status", g.Status())
	return nil
}

// Status 返回当前网关状态。
func (g *Gateway) Status() GatewayStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.statusLocked()
}

// ListThreads 返回当前工作目录下的线程列表。
func (g *Gateway) ListThreads() ([]ThreadSummary, error) {
	g.mu.Lock()
	codex := g.codex
	cwd := g.currentCWD
	g.mu.Unlock()
	if codex == nil {
		return nil, fmt.Errorf("网关尚未启动")
	}
	return g.listThreads(context.Background(), codex, cwd)
}

// BindThread 恢复指定线程并保存绑定关系。
func (g *Gateway) BindThread(threadID string) (GatewayStatus, error) {
	g.mu.Lock()
	codex := g.codex
	cfg := g.cfg
	cwd := g.currentCWD
	g.mu.Unlock()
	if codex == nil {
		return g.Status(), fmt.Errorf("网关尚未启动")
	}

	result, err := codex.Request(context.Background(), "thread/resume", JSONObject{
		"threadId":       threadID,
		"cwd":            cwd,
		"approvalPolicy": "never",
		"sandbox":        "danger-full-access",
		"excludeTurns":   true,
	}, 120*time.Second)
	if err != nil {
		g.setError(err)
		return g.Status(), err
	}
	cfg.BoundThreadID = threadID
	_ = g.store.Save(cfg)
	g.mu.Lock()
	g.cfg = cfg
	g.currentID = threadID
	g.currentCWD = cwd
	g.activeTurn = ""
	g.mu.Unlock()
	g.emit("gateway:status", g.Status())
	g.broadcastBoundThread(threadID, readThread(result))
	g.notifyThreadsChanged("thread-bound")
	return g.Status(), nil
}

// initializeThread 根据配置恢复线程；未绑定时创建默认线程。
func (g *Gateway) initializeThread(ctx context.Context) error {
	g.mu.Lock()
	codex := g.codex
	cfg := g.cfg
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("Codex app-server 尚未启动")
	}

	if cfg.BoundThreadID != "" {
		_, err := codex.Request(ctx, "thread/resume", JSONObject{
			"threadId":       cfg.BoundThreadID,
			"cwd":            cfg.Workspace,
			"approvalPolicy": "never",
			"sandbox":        "danger-full-access",
			"excludeTurns":   true,
		}, 120*time.Second)
		if err != nil {
			return err
		}
		g.mu.Lock()
		g.currentID = cfg.BoundThreadID
		g.currentCWD = cfg.Workspace
		g.mu.Unlock()
		return nil
	}

	result, err := codex.Request(ctx, "thread/start", JSONObject{
		"cwd":                cfg.Workspace,
		"approvalPolicy":     "never",
		"sandbox":            "danger-full-access",
		"ephemeral":          false,
		"sessionStartSource": "clear",
	}, 120*time.Second)
	if err != nil {
		return err
	}
	thread := readThread(result)
	threadID := stringField(thread, "id")
	if threadID == "" {
		return fmt.Errorf("创建默认线程失败：Codex 未返回 thread id")
	}
	g.mu.Lock()
	g.currentID = threadID
	g.currentCWD = cfg.Workspace
	g.mu.Unlock()
	return nil
}

// listThreads 调用 app-server 获取线程列表。
func (g *Gateway) listThreads(ctx context.Context, codex *CodexClient, cwd string) ([]ThreadSummary, error) {
	result, err := codex.Request(ctx, "thread/list", JSONObject{
		"cwd":            cwd,
		"limit":          50,
		"archived":       false,
		"sortKey":        "updated_at",
		"sortDirection":  "desc",
		"useStateDbOnly": false,
	}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	threads := threadSummaries(result)
	sort.SliceStable(threads, func(i, j int) bool {
		return threads[i].UpdatedAt > threads[j].UpdatedAt
	})
	return threads, nil
}

// handleHealth 输出 JSON 健康状态。
func (g *Gateway) handleHealth(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("content-type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(g.Status())
}

// handleIndex 提供一个最小测试页，方便浏览器确认网关在线。
func (g *Gateway) handleIndex(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("content-type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte("<!doctype html><meta charset=utf-8><title>CodexMobileGateway</title><body>CodexMobileGateway is running.</body>"))
}

// handleWebSocket 校验 token 后接入手机端 WebSocket。
func (g *Gateway) handleWebSocket(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/ws" || !g.validToken(request.URL.Query().Get("token")) {
		http.Error(response, "Unauthorized", http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(response, request, nil)
	if err != nil {
		return
	}
	g.attachClient(conn)
}

// attachClient 绑定手机端连接生命周期。
func (g *Gateway) attachClient(conn *websocket.Conn) {
	client := newClientState(conn)
	g.mu.Lock()
	g.clients[conn] = client
	ready := JSONObject{"type": "ready", "threadId": g.currentID, "cwd": g.currentCWD}
	g.mu.Unlock()
	go client.writeLoop()
	client.sendJSON(ready)

	go func() {
		defer func() {
			g.mu.Lock()
			delete(g.clients, conn)
			g.mu.Unlock()
			client.close()
		}()
		for {
			var message JSONObject
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			g.mu.Lock()
			if state := g.clients[conn]; state != nil {
				state.lastSeenAt = time.Now()
			}
			g.mu.Unlock()
			if err := g.handleClientMessage(client, message); err != nil {
				client.sendJSON(JSONObject{"type": "response", "ok": false, "error": redactSecrets(err)})
			}
		}
	}()
}

// handleClientMessage 处理 Android 客户端协议。
func (g *Gateway) handleClientMessage(client *clientState, message JSONObject) error {
	messageType := stringField(message, "type")
	requestID := stringField(message, "id")

	switch messageType {
	case "ping":
		g.mu.Lock()
		threadID := g.currentID
		g.mu.Unlock()
		client.sendJSON(JSONObject{"type": "pong", "timestamp": time.Now().UnixMilli(), "threadId": threadID})
		return nil
	case "health.check":
		status := g.Status()
		client.sendJSON(JSONObject{"type": "health", "report": status})
		client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": status})
		return nil
	case "workspace.check":
		cwd, err := g.resolveClientWorkspace(stringField(message, "cwd"))
		if err != nil {
			client.sendJSON(JSONObject{"type": "workspace", "ok": false, "error": err.Error()})
			client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": false, "error": err.Error()})
			return nil
		}
		client.sendJSON(JSONObject{"type": "workspace", "ok": true, "cwd": cwd})
		client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": JSONObject{"cwd": cwd}})
		return nil
	case "thread.list":
		return g.clientThreadList(client, requestID, stringField(message, "cwd"))
	case "thread.read":
		return g.clientThreadRead(client, requestID)
	case "thread.start":
		return g.clientThreadStart(client, requestID, stringField(message, "cwd"))
	case "thread.resume":
		threadID := stringField(message, "threadId")
		cwd := stringField(message, "cwd")
		return g.clientThreadResume(client, requestID, threadID, cwd)
	case "turn.start":
		return g.clientTurnStart(client, requestID, message)
	case "turn.steer":
		return g.clientTurnSteer(client, requestID, message)
	case "turn.interrupt":
		return g.clientTurnInterrupt(client, requestID, message)
	default:
		return fmt.Errorf("不支持的消息类型: %s", messageType)
	}
}

// clientThreadList 推送线程列表。
func (g *Gateway) clientThreadList(client *clientState, requestID string, cwd string) error {
	g.mu.Lock()
	codex := g.codex
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	resolved, err := g.resolveClientWorkspace(cwd)
	if err != nil {
		return err
	}
	threads, err := g.listThreads(context.Background(), codex, resolved)
	if err != nil {
		return err
	}
	client.sendJSON(JSONObject{"type": "threads", "threads": threads})
	client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": JSONObject{"data": threads}})
	return nil
}

// clientThreadRead 推送当前线程历史。
func (g *Gateway) clientThreadRead(client *clientState, requestID string) error {
	g.mu.Lock()
	threadID := g.currentID
	g.mu.Unlock()
	lines := g.historyLinesFromSessionFile(threadID)
	client.sendJSON(JSONObject{"type": "history", "threadId": threadID, "lines": lines})
	client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": JSONObject{"thread": JSONObject{"id": threadID}}})
	return nil
}

// clientThreadStart 创建新线程。
func (g *Gateway) clientThreadStart(client *clientState, requestID string, cwd string) error {
	g.mu.Lock()
	codex := g.codex
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	resolved, err := g.resolveClientWorkspace(cwd)
	if err != nil {
		return err
	}
	result, err := codex.Request(context.Background(), "thread/start", JSONObject{
		"cwd":                resolved,
		"approvalPolicy":     "never",
		"sandbox":            "danger-full-access",
		"ephemeral":          false,
		"sessionStartSource": "clear",
	}, 120*time.Second)
	if err != nil {
		return err
	}
	thread := readThread(result)
	threadID := stringField(thread, "id")
	g.mu.Lock()
	g.currentID = threadID
	g.currentCWD = resolved
	g.activeTurn = ""
	g.mu.Unlock()
	g.emit("gateway:status", g.Status())
	g.notifyThreadsChanged("thread-started")
	client.sendJSON(JSONObject{"type": "thread", "thread": thread})
	client.sendJSON(JSONObject{"type": "history", "threadId": threadID, "lines": []HistoryLine{}})
	client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
	return nil
}

// clientThreadResume 恢复指定线程。
func (g *Gateway) clientThreadResume(client *clientState, requestID string, threadID string, cwd string) error {
	g.mu.Lock()
	codex := g.codex
	if threadID == "" {
		threadID = g.currentID
	}
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	if threadID == "" {
		return fmt.Errorf("当前没有可恢复的会话，请先新建或绑定一个会话")
	}
	resolved, err := g.resolveClientWorkspace(cwd)
	if err != nil {
		return err
	}
	result, err := codex.Request(context.Background(), "thread/resume", JSONObject{
		"threadId":       threadID,
		"cwd":            resolved,
		"approvalPolicy": "never",
		"sandbox":        "danger-full-access",
		"excludeTurns":   true,
	}, 120*time.Second)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.currentID = threadID
	g.currentCWD = resolved
	g.activeTurn = ""
	g.mu.Unlock()
	g.emit("gateway:status", g.Status())
	g.notifyThreadsChanged("thread-resumed")
	client.sendJSON(JSONObject{"type": "thread", "thread": readThread(result)})
	client.sendJSON(JSONObject{"type": "history", "threadId": threadID, "lines": g.historyLinesFromSessionFile(threadID)})
	client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
	return nil
}

// clientTurnStart 开始一轮 Codex 对话。
func (g *Gateway) clientTurnStart(client *clientState, requestID string, message JSONObject) error {
	text := strings.TrimSpace(stringField(message, "text"))
	if text == "" {
		return fmt.Errorf("消息内容不能为空")
	}
	g.mu.Lock()
	codex := g.codex
	threadID := stringField(message, "threadId")
	if threadID == "" {
		threadID = g.currentID
	}
	cwd := stringField(message, "cwd")
	activeTurn := g.activeTurn
	g.mu.Unlock()
	if activeTurn != "" {
		return fmt.Errorf("当前已有 turn 正在执行，请等待完成或先 interrupt")
	}
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	resolved, err := g.resolveClientWorkspace(cwd)
	if err != nil {
		return err
	}
	result, err := codex.Request(context.Background(), "turn/start", JSONObject{
		"threadId":       threadID,
		"cwd":            resolved,
		"approvalPolicy": "never",
		"sandboxPolicy":  JSONObject{"type": "dangerFullAccess"},
		"input":          []JSONObject{{"type": "text", "text": text, "text_elements": []any{}}},
	}, 120*time.Second)
	if err != nil {
		return err
	}
	turnID := stringField(readTurn(result), "id")
	g.mu.Lock()
	g.currentID = threadID
	g.currentCWD = resolved
	g.activeTurn = turnID
	g.mu.Unlock()
	client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
	return nil
}

// clientTurnSteer 向正在执行的 turn 追加指令。
func (g *Gateway) clientTurnSteer(client *clientState, requestID string, message JSONObject) error {
	text := strings.TrimSpace(stringField(message, "text"))
	if text == "" {
		return fmt.Errorf("追加指令不能为空")
	}
	g.mu.Lock()
	codex := g.codex
	threadID := stringField(message, "threadId")
	if threadID == "" {
		threadID = g.currentID
	}
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	result, err := codex.Request(context.Background(), "turn/steer", JSONObject{
		"threadId":       threadID,
		"expectedTurnId": stringField(message, "expectedTurnId"),
		"input":          []JSONObject{{"type": "text", "text": text, "text_elements": []any{}}},
	}, 120*time.Second)
	if err != nil {
		return err
	}
	client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
	return nil
}

// clientTurnInterrupt 停止当前 turn。
func (g *Gateway) clientTurnInterrupt(client *clientState, requestID string, message JSONObject) error {
	g.mu.Lock()
	codex := g.codex
	threadID := stringField(message, "threadId")
	if threadID == "" {
		threadID = g.currentID
	}
	turnID := stringField(message, "turnId")
	if turnID == "" {
		turnID = g.activeTurn
	}
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	if turnID == "" {
		return fmt.Errorf("当前没有可停止的 turn")
	}
	result, err := codex.Request(context.Background(), "turn/interrupt", JSONObject{"threadId": threadID, "turnId": turnID}, 120*time.Second)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.activeTurn = ""
	g.mu.Unlock()
	client.sendJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
	return nil
}

// handleCodexNotification 把 app-server 通知映射为 Android 协议事件。
func (g *Gateway) handleCodexNotification(message JSONObject) {
	method := stringField(message, "method")
	params := jsonObject(message["params"])
	if params == nil {
		return
	}
	threadID := stringField(params, "threadId")
	g.mu.Lock()
	currentID := g.currentID
	g.mu.Unlock()
	if threadID != "" && currentID != "" && threadID != currentID {
		return
	}

	switch method {
	case "turn/started":
		turn := jsonObject(params["turn"])
		turnID := stringField(turn, "id")
		g.mu.Lock()
		g.activeTurn = turnID
		g.mu.Unlock()
		g.broadcast(JSONObject{"type": "turn.started", "threadId": firstNonEmpty(threadID, currentID), "turnId": turnID})
	case "turn/completed":
		g.mu.Lock()
		g.activeTurn = ""
		g.mu.Unlock()
		g.broadcast(JSONObject{"type": "turn.completed", "threadId": firstNonEmpty(threadID, currentID), "turn": params["turn"]})
		g.notifyThreadsChanged("turn-completed")
	case "item/agentMessage/delta":
		g.broadcast(JSONObject{"type": "delta", "threadId": threadID, "turnId": stringField(params, "turnId"), "itemId": stringField(params, "itemId"), "text": fmt.Sprint(params["delta"])})
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		g.broadcast(JSONObject{"type": "command.delta", "threadId": threadID, "turnId": stringField(params, "turnId"), "itemId": stringField(params, "itemId"), "text": fmt.Sprint(params["delta"])})
	case "command/exec/outputDelta":
		text := base64Text(stringField(params, "deltaBase64"))
		if text == "" {
			text = fmt.Sprint(params["delta"])
		}
		g.broadcast(JSONObject{"type": "command.delta", "threadId": currentID, "turnId": g.Status().ActiveTurnID, "itemId": firstNonEmpty(stringField(params, "processId"), "command-exec"), "text": text})
	case "item/plan/delta":
		g.broadcast(JSONObject{"type": "plan.delta", "threadId": threadID, "turnId": stringField(params, "turnId"), "itemId": stringField(params, "itemId"), "text": fmt.Sprint(params["delta"])})
	case "turn/plan/updated":
		g.broadcast(JSONObject{"type": "plan.updated", "threadId": threadID, "turnId": stringField(params, "turnId"), "explanation": params["explanation"], "plan": params["plan"]})
	case "item/fileChange/patchUpdated":
		g.broadcast(JSONObject{"type": "file.patch", "threadId": threadID, "turnId": stringField(params, "turnId"), "itemId": stringField(params, "itemId"), "changes": params["changes"]})
	case "thread/status/changed":
		g.broadcast(JSONObject{"type": "status", "threadId": threadID, "status": params["status"]})
	case "error", "gateway/error":
		g.broadcast(JSONObject{"type": "error", "message": codexErrorMessage(params), "detail": params})
	}
	g.emit("gateway:status", g.Status())
}

// clientHeartbeat 定期 ping 手机端连接，并关闭半开连接。
func (g *Gateway) clientHeartbeat() {
	ticker := time.NewTicker(time.Duration(defaultPingIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		g.mu.Lock()
		if g.server == nil {
			g.mu.Unlock()
			return
		}
		now := time.Now()
		for client, state := range g.clients {
			if now.Sub(state.lastSeenAt) > time.Duration(defaultIdleTimeoutMs)*time.Millisecond {
				delete(g.clients, client)
				state.close()
				continue
			}
			_ = client.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		}
		g.mu.Unlock()
	}
}

// broadcast 向所有手机端连接发送事件。
func (g *Gateway) broadcast(message JSONObject) {
	g.mu.Lock()
	clients := make([]*clientState, 0, len(g.clients))
	for _, client := range g.clients {
		clients = append(clients, client)
	}
	g.mu.Unlock()
	for _, client := range clients {
		client.sendJSON(message)
	}
}

// broadcastBoundThread 把桌面端绑定的会话同步给所有已连接手机端。
func (g *Gateway) broadcastBoundThread(threadID string, thread JSONObject) {
	if thread == nil {
		thread = JSONObject{}
	}
	if stringField(thread, "id") == "" {
		thread["id"] = threadID
	}
	g.mu.Lock()
	cwd := g.currentCWD
	g.mu.Unlock()
	if stringField(thread, "cwd") == "" && cwd != "" {
		thread["cwd"] = cwd
	}
	g.broadcast(JSONObject{"type": "thread", "thread": thread})
	g.broadcast(JSONObject{"type": "history", "threadId": threadID, "lines": g.historyLinesFromSessionFile(threadID)})
}

// resolveClientWorkspace 限制手机端只能使用桌面网关配置的工作目录。
func (g *Gateway) resolveClientWorkspace(input string) (string, error) {
	input = strings.TrimSpace(input)
	g.mu.Lock()
	allowed := firstNonEmpty(g.currentCWD, g.cfg.Workspace)
	g.mu.Unlock()
	resolvedAllowed, err := validateWorkspacePath(allowed)
	if err != nil {
		return "", err
	}
	if input == "" {
		return resolvedAllowed, nil
	}
	resolvedInput, err := validateWorkspacePath(input)
	if err != nil {
		return "", err
	}
	if resolvedInput != resolvedAllowed {
		return "", fmt.Errorf("手机端只能使用桌面网关配置的工作目录")
	}
	return resolvedInput, nil
}

// validToken 使用固定时间比较校验手机端 token。
func (g *Gateway) validToken(candidate string) bool {
	g.mu.Lock()
	token := g.cfg.Token
	g.mu.Unlock()
	if candidate == "" || token == "" {
		return false
	}
	expected := sha256.Sum256([]byte(token))
	actual := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
}

// statusLocked 在持锁状态下返回状态快照。
func (g *Gateway) statusLocked() GatewayStatus {
	running := g.server != nil
	appServer := "disconnected"
	if g.codex != nil && g.codex.IsConnected() {
		appServer = "connected"
	}
	return GatewayStatus{
		Running:         running,
		Gateway:         mapBool(running, "running", "stopped"),
		AppServer:       appServer,
		ThreadID:        g.currentID,
		DefaultThreadID: g.cfg.BoundThreadID,
		CWD:             g.currentCWD,
		ActiveTurnID:    g.activeTurn,
		Error:           g.lastError,
		ConfigPath:      g.store.Path(),
		Timestamp:       time.Now().UnixMilli(),
	}
}

// setError 记录错误并通知前端。
func (g *Gateway) setError(err error) {
	g.mu.Lock()
	g.lastError = redactSecrets(err)
	g.mu.Unlock()
	g.emit("gateway:status", g.Status())
}

// notifyThreadsChanged 通知桌面端刷新会话列表，覆盖手机端新建、切换和对话完成后的列表更新。
func (g *Gateway) notifyThreadsChanged(reason string) {
	g.emit("gateway:threadsChanged", JSONObject{
		"reason":    reason,
		"threadId":  g.Status().ThreadID,
		"timestamp": time.Now().UnixMilli(),
	})
}

// emit 向 Wails 前端推送事件。
func (g *Gateway) emit(name string, data any) {
	g.mu.Lock()
	sink := g.eventSink
	g.mu.Unlock()
	if sink != nil {
		sink(name, data)
	}
}

// historyLinesFromSessionFile 从本地 session jsonl 中提取纯聊天历史。
func (g *Gateway) historyLinesFromSessionFile(threadID string) []HistoryLine {
	file := findSessionFile(threadID)
	if file == "" {
		return []HistoryLine{}
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return []HistoryLine{}
	}
	lines := []HistoryLine{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record JSONObject
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if stringField(record, "type") != "event_msg" {
			continue
		}
		payload := jsonObject(record["payload"])
		message := strings.TrimSpace(stringField(payload, "message"))
		if message == "" {
			continue
		}
		switch stringField(payload, "type") {
		case "user_message":
			lines = append(lines, HistoryLine{Kind: "user", Text: message, ItemID: fmt.Sprintf("session-%d", len(lines))})
		case "agent_message":
			lines = append(lines, HistoryLine{Kind: "assistant", Text: message, ItemID: fmt.Sprintf("session-%d", len(lines))})
		}
	}
	return lines
}

// findSessionFile 查找 Codex 本地 session jsonl。
func findSessionFile(threadID string) string {
	if threadID == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	sessionsDir := filepath.Join(firstNonEmpty(os.Getenv("CODEX_HOME"), filepath.Join(home, ".codex")), "sessions")
	var matched string
	var matchedMod time.Time
	_ = filepath.WalkDir(sessionsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") || !strings.Contains(entry.Name(), threadID) {
			return nil
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().After(matchedMod) {
			matched = path
			matchedMod = info.ModTime()
		}
		return nil
	})
	return matched
}

// threadSummaries 提取 app-server thread/list 返回。
func threadSummaries(result any) []ThreadSummary {
	object := jsonObject(result)
	data, _ := object["data"].([]any)
	threads := make([]ThreadSummary, 0, len(data))
	for _, item := range data {
		thread := jsonObject(item)
		id := stringField(thread, "id")
		if id == "" {
			continue
		}
		name := firstNonEmpty(stringField(thread, "name"), stringField(thread, "title"), stringField(thread, "preview"), "无标题会话")
		threads = append(threads, ThreadSummary{
			ID:        id,
			Name:      name,
			Preview:   firstNonEmpty(stringField(thread, "preview"), name),
			CWD:       stringField(thread, "cwd"),
			UpdatedAt: int64(numberField(thread, "updatedAt")),
		})
	}
	return threads
}

// readThread 从 Codex 返回中读取 thread 对象。
func readThread(result any) JSONObject {
	object := jsonObject(result)
	return jsonObject(object["thread"])
}

// readTurn 从 Codex 返回中读取 turn 对象。
func readTurn(result any) JSONObject {
	object := jsonObject(result)
	return jsonObject(object["turn"])
}

// codexErrorMessage 从 app-server 错误通知中提取可读信息，避免手机端只看到泛化错误。
func codexErrorMessage(params JSONObject) string {
	message := stringField(params, "message")
	if message == "" {
		message = stringField(jsonObject(params["error"]), "message")
	}
	if message == "" {
		message = stringField(jsonObject(params["detail"]), "message")
	}
	if message == "" {
		message = "Codex 错误"
	}
	code := firstNonEmpty(
		stringField(params, "codexErrorInfo"),
		stringField(jsonObject(params["error"]), "codexErrorInfo"),
		stringField(jsonObject(params["detail"]), "codexErrorInfo"),
	)
	if code != "" && !strings.Contains(message, code) {
		message = fmt.Sprintf("%s (%s)", message, code)
	}
	return redactSecrets(message)
}

// stringField 安全读取字符串字段。
func stringField(object JSONObject, key string) string {
	if object == nil {
		return ""
	}
	if value, ok := object[key].(string); ok {
		return value
	}
	return ""
}

// numberField 安全读取数字字段。
func numberField(object JSONObject, key string) float64 {
	if object == nil {
		return 0
	}
	switch value := object[key].(type) {
	case float64:
		return value
	case int64:
		return float64(value)
	case int:
		return float64(value)
	default:
		return 0
	}
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// mapBool 将布尔值映射为字符串状态。
func mapBool(ok bool, yes string, no string) string {
	if ok {
		return yes
	}
	return no
}
