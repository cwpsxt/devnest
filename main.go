package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"devnest/internal/svchost"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// The same executable doubles as the Windows service host for programs
	// that cannot run as a service on their own (Nginx, Redis, Kafka, ...).
	// The service manager starts it as: DevNest.exe --service <config.json>
	if len(os.Args) >= 3 && os.Args[1] == "--service" {
		if err := svchost.Run(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	app := NewApp()
	err := wails.Run(&options.App{
		Title:            "DevNest 开发环境管家",
		Width:            1280,
		Height:           820,
		MinWidth:         1080,
		MinHeight:        680,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 244, G: 246, B: 250, A: 1},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "devnest-4f6c1d0e-9a2b-4c57-8e31-6b0d7a5c2f19",
			OnSecondInstanceLaunch: app.secondInstance,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
