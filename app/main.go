package main

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func vaultPath() string {
	if v := os.Getenv("BRAIN_VAULT"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "brain-vault")
}

func main() {
	app := NewApp(vaultPath())

	err := wails.Run(&options.App{
		Title: "brain",
		// Panel-sized, not a window. It is a dropdown, invoked dozens of times
		// a day; it should feel like part of the menubar, not an application.
		// Wider than the original chat-first layout: the sessions tab is a
		// two-pane inspector (checkpoint list beside its full record), and that
		// needs room a 420px popover never had to give it.
		Width:  840,
		Height: 680,
		// Frameless: no OS title bar and no traffic-light buttons on screen.
		// The panel is a clean floating surface; it is dragged by its own header
		// and dismissed with Esc (see the frontend), the way a menubar dropdown
		// behaves rather than an application window.
		Frameless: true,
		// Transparent so the frontend's rounded corners show through instead of
		// square window edges.
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		Assets:           assets,
		OnStartup:        app.startup,
		Bind:             []any{app},
		// Single instance: relaunching the app re-shows the panel instead of
		// spawning a second one. This is what makes Esc-to-hide safe — with no
		// traffic lights, reopening from the launcher is how you get it back.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com.brain.secondbrain",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.Show() },
		},
		Mac: &mac.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "brain",
				Message: "Local-first memory and continuity for AI agents.",
			},
		},
	})
	if err != nil {
		println("fatal:", err.Error())
	}
}
