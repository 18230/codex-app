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
	lastSeenAt time.Time
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

// Stop 停止网关和所有子连接。
func (g *Gateway) Stop() error {
	g.mu.Lock()
	server := g.server
	codex := g.codex
	clients := make([]*websocket.Conn, 0, len(g.clients))
	for client := range g.clients {
		clients = append(clients, client)
	}
	g.server = nil
	g.codex = nil
	g.clients = make(map[*websocket.Conn]*clientState)
	g.activeTurn = ""
	g.mu.Unlock()

	for _, client := range clients {
		_ = client.Close()
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

	if _, err := codex.Request(context.Background(), "thread/resume", JSONObject{
		"threadId":       threadID,
		"cwd":            cwd,
		"approvalPolicy": "never",
		"sandbox":        "danger-full-access",
		"excludeTurns":   true,
	}, 120*time.Second); err != nil {
		g.setError(err)
		return g.Status(), err
	}
	cfg.BoundThreadID = threadID
	_ = g.store.Save(cfg)
	g.mu.Lock()
	g.cfg = cfg
	g.currentID = threadID
	g.mu.Unlock()
	g.emit("gateway:status", g.Status())
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
	g.mu.Lock()
	g.clients[conn] = &clientState{lastSeenAt: time.Now()}
	ready := JSONObject{"type": "ready", "threadId": g.currentID, "cwd": g.currentCWD}
	g.mu.Unlock()
	_ = conn.WriteJSON(ready)

	go func() {
		defer func() {
			g.mu.Lock()
			delete(g.clients, conn)
			g.mu.Unlock()
			_ = conn.Close()
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
			if err := g.handleClientMessage(conn, message); err != nil {
				_ = conn.WriteJSON(JSONObject{"type": "response", "ok": false, "error": err.Error()})
			}
		}
	}()
}

// handleClientMessage 处理 Android 客户端协议。
func (g *Gateway) handleClientMessage(conn *websocket.Conn, message JSONObject) error {
	messageType := stringField(message, "type")
	requestID := stringField(message, "id")

	switch messageType {
	case "ping":
		g.mu.Lock()
		threadID := g.currentID
		g.mu.Unlock()
		return conn.WriteJSON(JSONObject{"type": "pong", "timestamp": time.Now().UnixMilli(), "threadId": threadID})
	case "health.check":
		status := g.Status()
		_ = conn.WriteJSON(JSONObject{"type": "health", "report": status})
		return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": status})
	case "workspace.check":
		cwd, err := validateWorkspacePath(stringField(message, "cwd"))
		if err != nil {
			_ = conn.WriteJSON(JSONObject{"type": "workspace", "ok": false, "error": err.Error()})
			return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": false, "error": err.Error()})
		}
		_ = conn.WriteJSON(JSONObject{"type": "workspace", "ok": true, "cwd": cwd})
		return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": JSONObject{"cwd": cwd}})
	case "thread.list":
		return g.clientThreadList(conn, requestID, stringField(message, "cwd"))
	case "thread.read":
		return g.clientThreadRead(conn, requestID)
	case "thread.start":
		return g.clientThreadStart(conn, requestID, stringField(message, "cwd"))
	case "thread.resume":
		threadID := stringField(message, "threadId")
		cwd := stringField(message, "cwd")
		return g.clientThreadResume(conn, requestID, threadID, cwd)
	case "turn.start":
		return g.clientTurnStart(conn, requestID, message)
	case "turn.steer":
		return g.clientTurnSteer(conn, requestID, message)
	case "turn.interrupt":
		return g.clientTurnInterrupt(conn, requestID, message)
	default:
		return fmt.Errorf("不支持的消息类型: %s", messageType)
	}
}

// clientThreadList 推送线程列表。
func (g *Gateway) clientThreadList(conn *websocket.Conn, requestID string, cwd string) error {
	g.mu.Lock()
	codex := g.codex
	if cwd == "" {
		cwd = g.currentCWD
	}
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	resolved, err := validateWorkspacePath(cwd)
	if err != nil {
		return err
	}
	threads, err := g.listThreads(context.Background(), codex, resolved)
	if err != nil {
		return err
	}
	_ = conn.WriteJSON(JSONObject{"type": "threads", "threads": threads})
	return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": JSONObject{"data": threads}})
}

// clientThreadRead 推送当前线程历史。
func (g *Gateway) clientThreadRead(conn *websocket.Conn, requestID string) error {
	g.mu.Lock()
	threadID := g.currentID
	g.mu.Unlock()
	lines := g.historyLinesFromSessionFile(threadID)
	_ = conn.WriteJSON(JSONObject{"type": "history", "threadId": threadID, "lines": lines})
	return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": JSONObject{"thread": JSONObject{"id": threadID}}})
}

// clientThreadStart 创建新线程。
func (g *Gateway) clientThreadStart(conn *websocket.Conn, requestID string, cwd string) error {
	g.mu.Lock()
	codex := g.codex
	if cwd == "" {
		cwd = g.currentCWD
	}
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	resolved, err := validateWorkspacePath(cwd)
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
	_ = conn.WriteJSON(JSONObject{"type": "thread", "thread": thread})
	_ = conn.WriteJSON(JSONObject{"type": "history", "threadId": threadID, "lines": []HistoryLine{}})
	return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
}

// clientThreadResume 恢复指定线程。
func (g *Gateway) clientThreadResume(conn *websocket.Conn, requestID string, threadID string, cwd string) error {
	if threadID == "" {
		return fmt.Errorf("threadId 不能为空")
	}
	g.mu.Lock()
	codex := g.codex
	if cwd == "" {
		cwd = g.currentCWD
	}
	g.mu.Unlock()
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	resolved, err := validateWorkspacePath(cwd)
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
	_ = conn.WriteJSON(JSONObject{"type": "thread", "thread": readThread(result)})
	_ = conn.WriteJSON(JSONObject{"type": "history", "threadId": threadID, "lines": g.historyLinesFromSessionFile(threadID)})
	return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
}

// clientTurnStart 开始一轮 Codex 对话。
func (g *Gateway) clientTurnStart(conn *websocket.Conn, requestID string, message JSONObject) error {
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
	if cwd == "" {
		cwd = g.currentCWD
	}
	activeTurn := g.activeTurn
	g.mu.Unlock()
	if activeTurn != "" {
		return fmt.Errorf("当前已有 turn 正在执行，请等待完成或先 interrupt")
	}
	if codex == nil {
		return fmt.Errorf("网关尚未启动")
	}
	resolved, err := validateWorkspacePath(cwd)
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
	return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
}

// clientTurnSteer 向正在执行的 turn 追加指令。
func (g *Gateway) clientTurnSteer(conn *websocket.Conn, requestID string, message JSONObject) error {
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
	return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
}

// clientTurnInterrupt 停止当前 turn。
func (g *Gateway) clientTurnInterrupt(conn *websocket.Conn, requestID string, message JSONObject) error {
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
	return conn.WriteJSON(JSONObject{"type": "response", "requestId": requestID, "ok": true, "data": result})
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
		g.broadcast(JSONObject{"type": "error", "message": firstNonEmpty(stringField(params, "message"), "Codex 错误"), "detail": params})
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
				_ = client.Close()
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
	clients := make([]*websocket.Conn, 0, len(g.clients))
	for client := range g.clients {
		clients = append(clients, client)
	}
	g.mu.Unlock()
	for _, client := range clients {
		_ = client.WriteJSON(message)
	}
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
	connectionURL := ""
	if g.cfg.Token != "" {
		connectionURL = fmt.Sprintf("%s?token=%s", strings.TrimRight(g.cfg.LastConnectionBaseURL, "/"), g.cfg.Token)
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
		ConnectionURL:   connectionURL,
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
