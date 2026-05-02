package main

// JSONValue 表示 Codex app-server 和手机端协议中的宽松 JSON 结构。
type JSONValue any

// JSONObject 是便于读取字段的 JSON 对象。
type JSONObject map[string]any

// ThreadSummary 是桌面 App 和手机端共享的线程列表条目。
type ThreadSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Preview   string `json:"preview"`
	CWD       string `json:"cwd"`
	UpdatedAt int64  `json:"updatedAt"`
}

// HistoryLine 是手机端历史消息展示的最小结构。
type HistoryLine struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	ItemID string `json:"itemId,omitempty"`
}

// GatewayStatus 是桌面配置页和健康检查使用的网关状态。
type GatewayStatus struct {
	Running               bool   `json:"running"`
	Gateway               string `json:"gateway"`
	AppServer             string `json:"appServer"`
	ThreadID              string `json:"threadId"`
	DefaultThreadID       string `json:"defaultThreadId"`
	CWD                   string `json:"cwd"`
	ActiveTurnID          string `json:"activeTurnId"`
	Error                 string `json:"error"`
	ConfigPath            string `json:"configPath"`
	HTTPRestartCount      int    `json:"httpRestartCount"`
	AppServerRestartCount int    `json:"appServerRestartCount"`
	AppServerRestarting   bool   `json:"appServerRestarting"`
	AppServerNextRestart  int64  `json:"appServerNextRestart"`
	Timestamp             int64  `json:"timestamp"`
}
