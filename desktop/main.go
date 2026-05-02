package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	appMenu := menu.NewMenu()
	gatewayMenu := appMenu.AddSubmenu("网关")
	gatewayMenu.AddText("显示窗口", nil, func(_ *menu.CallbackData) { app.ShowWindow() })
	gatewayMenu.AddText("启动网关", nil, func(_ *menu.CallbackData) { _, _ = app.StartGateway() })
	gatewayMenu.AddText("停止网关", nil, func(_ *menu.CallbackData) { _, _ = app.StopGateway() })
	gatewayMenu.AddSeparator()
	gatewayMenu.AddText("退出", nil, func(_ *menu.CallbackData) { app.QuitApp() })

	err := wails.Run(&options.App{
		Title:             "CodexMobileGateway",
		Width:             980,
		Height:            720,
		MinWidth:          860,
		MinHeight:         620,
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Menu:             appMenu,
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 1},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
