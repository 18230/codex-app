package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是 Wails 暴露给前端的入口，负责配置读写、网关生命周期和桌面交互。
type App struct {
	ctx     context.Context
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

// startup 保存 Wails 上下文，并让配置文件在首次启动时落盘。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.gateway.SetEventSink(func(name string, data any) {
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, name, data)
		}
	})
	_ = a.store.Ensure()
}

// beforeClose 把关闭窗口改为隐藏窗口，保持托盘常驻语义。
func (a *App) beforeClose(ctx context.Context) bool {
	wailsRuntime.WindowHide(ctx)
	return true
}

// shutdown 退出应用前停止网关和 Codex 子进程。
func (a *App) shutdown(ctx context.Context) {
	_ = a.gateway.Stop()
}

// GetConfig 返回当前配置和状态快照。
func (a *App) GetConfig() (AppSnapshot, error) {
	cfg, err := a.store.Load()
	if err != nil {
		return AppSnapshot{}, err
	}
	return AppSnapshot{Config: cfg, Status: a.gateway.Status()}, nil
}

// SaveConfig 保存配置；如果网关正在运行，要求用户手动重启以避免半配置状态。
func (a *App) SaveConfig(cfg AppConfig) (AppSnapshot, error) {
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return AppSnapshot{}, err
	}
	if err := a.store.Save(normalized); err != nil {
		return AppSnapshot{}, err
	}
	return AppSnapshot{Config: normalized, Status: a.gateway.Status()}, nil
}

// GenerateToken 生成 32 字节随机 token，用于手机端连接鉴权。
func (a *App) GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 token 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// SelectWorkspace 打开系统目录选择框，并返回用户选择的工作目录。
func (a *App) SelectWorkspace() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("窗口尚未初始化")
	}
	return wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择 Codex 工作目录",
	})
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
	if a.ctx != nil {
		wailsRuntime.WindowShow(a.ctx)
		wailsRuntime.WindowCenter(a.ctx)
	}
}

// QuitApp 从托盘菜单退出应用。
func (a *App) QuitApp() {
	if a.ctx != nil {
		wailsRuntime.Quit(a.ctx)
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
