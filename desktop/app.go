package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// App 是 Wails 暴露给前端的入口，负责配置读写、网关生命周期和桌面交互。
type App struct {
	app     *application.App
	window  *application.WebviewWindow
	store   *ConfigStore
	gateway *Gateway
	mu      sync.Mutex
}

// NewApp 创建桌面应用实例。
func NewApp() *App {
	store := NewConfigStore()
	return &App{
		store:   store,
		gateway: NewGateway(store),
	}
}

// attachApplication 注入 Wails v3 应用实例，供事件、Dialog 和退出逻辑使用。
func (a *App) attachApplication(app *application.App) {
	a.app = app
	a.gateway.SetEventSink(func(name string, data any) {
		if a.app != nil {
			a.app.Event.Emit(name, data)
		}
	})
	_ = a.store.Ensure()
}

// attachWindow 注入主窗口实例，供托盘菜单和前端方法控制显示。
func (a *App) attachWindow(window *application.WebviewWindow) {
	a.window = window
}

// shutdown 退出应用前停止网关和 Codex 子进程。
func (a *App) shutdown() {
	_ = a.gateway.Stop()
}

// ServiceStartup 符合 Wails v3 服务生命周期，确保直接测试服务时也有默认配置。
func (a *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	return a.store.Ensure()
}

// ServiceShutdown 符合 Wails v3 服务生命周期，退出前停止网关。
func (a *App) ServiceShutdown() error {
	return a.gateway.Stop()
}

// GetConfig 返回当前配置和状态快照。
func (a *App) GetConfig() (AppSnapshot, error) {
	cfg, err := a.store.Load()
	if err != nil {
		return AppSnapshot{}, err
	}
	return AppSnapshot{Config: cfg, Status: a.gateway.Status()}, nil
}

// SaveConfig 保存配置；工作目录变化且网关运行时自动重启，让手机端重连后拿到新目录。
func (a *App) SaveConfig(cfg AppConfig) (AppSnapshot, error) {
	previous, _ := a.store.Load()
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return AppSnapshot{}, err
	}
	normalized, err = ValidateConfigTargets(normalized)
	if err != nil {
		return AppSnapshot{}, err
	}
	workspaceChanged := previous.Workspace != "" && previous.Workspace != normalized.Workspace
	if workspaceChanged {
		normalized.BoundThreadID = ""
	}
	if err := a.store.Save(normalized); err != nil {
		return AppSnapshot{}, err
	}
	status := a.gateway.Status()
	if workspaceChanged && status.Running {
		if err := a.gateway.Stop(); err != nil {
			return AppSnapshot{Config: normalized, Status: a.gateway.Status()}, err
		}
		restartedStatus, err := a.gateway.Start()
		if err != nil {
			return AppSnapshot{Config: normalized, Status: restartedStatus}, err
		}
		status = restartedStatus
	}
	return AppSnapshot{Config: normalized, Status: status}, nil
}

// GenerateToken 生成 32 字节随机 token，用于手机端连接鉴权。
func (a *App) GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 token 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// DetectCodexBinary 自动查找本机 Codex 可执行文件。
func (a *App) DetectCodexBinary(input string) (string, error) {
	return ResolveCodexBinary(input)
}

// SelectWorkspace 打开系统目录选择框，并返回用户选择的工作目录。
func (a *App) SelectWorkspace() (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("窗口尚未初始化")
	}
	return a.app.Dialog.OpenFile().
		SetTitle("选择 Codex 工作目录").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
}

// StartGateway 启动本机 Codex Mobile Gateway。
func (a *App) StartGateway() (GatewayStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gateway.Start()
}

// StopGateway 停止本机 Codex Mobile Gateway。
func (a *App) StopGateway() (GatewayStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.gateway.Stop(); err != nil {
		return a.gateway.Status(), err
	}
	return a.gateway.Status(), nil
}

// HealthCheck 返回网关、Codex app-server 和当前线程状态。
func (a *App) HealthCheck() GatewayStatus {
	return a.gateway.Status()
}

// ListThreads 读取当前工作目录下的 Codex 线程列表。
func (a *App) ListThreads() ([]ThreadSummary, error) {
	return a.gateway.ListThreads()
}

// BindThread 把网关当前线程切换到指定会话。
func (a *App) BindThread(threadID string) (GatewayStatus, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return a.gateway.Status(), fmt.Errorf("threadId 不能为空")
	}
	return a.gateway.BindThread(threadID)
}

// ConnectionURL 根据当前配置生成手机端连接地址。
func (a *App) ConnectionURL(host string) (string, error) {
	cfg, err := a.store.Load()
	if err != nil {
		return "", err
	}
	host = strings.TrimSpace(host)
	if host == "" {
		host = "https://xxx.com"
	}
	host = strings.TrimRight(host, "/")
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "https://" + host
	}
	return fmt.Sprintf("%s?token=%s", host, cfg.Token), nil
}

// ShowWindow 从托盘菜单恢复主窗口。
func (a *App) ShowWindow() {
	if a.window != nil {
		a.window.Show()
		a.window.Center()
		a.window.Focus()
	}
}

// QuitApp 从托盘菜单退出应用。
func (a *App) QuitApp() {
	if a.app != nil {
		a.app.Quit()
	}
}

// ShortPath 返回用于界面显示的短路径，完整路径仍保存在配置中。
func (a *App) ShortPath(path string) string {
	clean := filepath.Clean(path)
	if len(clean) <= 64 {
		return clean
	}
	return "..." + clean[len(clean)-61:]
}
