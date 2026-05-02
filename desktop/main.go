package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

//go:embed build/tray-icon.png
var trayIcon []byte

// main 使用 Wails v3 创建桌面窗口和系统托盘，并注册网关服务。
func main() {
	service := NewApp()
	app := application.New(application.Options{
		Name:        "CodexMobileGateway",
		Description: "Codex Mobile Gateway desktop controller",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		OnShutdown: func() {
			service.shutdown()
		},
	})
	service.attachApplication(app)

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "CodexMobileGateway",
		Width:            980,
		Height:           720,
		MinWidth:         860,
		MinHeight:        620,
		URL:              "/",
		BackgroundColour: application.NewRGB(255, 255, 255),
	})
	service.attachWindow(window)
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		window.Hide()
		event.Cancel()
	})

	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetLabel("")
	tray.SetTooltip("CodexMobileGateway")
	tray.OnRightClick(func() {
		tray.OpenMenu()
	})

	menu := app.NewMenu()
	menu.Add("显示窗口").OnClick(func(ctx *application.Context) {
		service.ShowWindow()
	})
	menu.Add("启动网关").OnClick(func(ctx *application.Context) {
		_, _ = service.StartGateway()
	})
	menu.Add("停止网关").OnClick(func(ctx *application.Context) {
		_, _ = service.StopGateway()
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(ctx *application.Context) {
		service.QuitApp()
	})
	tray.SetMenu(menu)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
