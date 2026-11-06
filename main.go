package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := newRootCommand(NewMonitorService(), launchDesktop).Execute(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func launchDesktop() error {
	frontend, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return err
	}
	chat := NewChatService(nil)
	workspace := NewWorkspaceService()
	chat.workspace = workspace
	comparison := &ComparisonService{chat: chat, workspace: workspace}
	app := application.New(application.Options{
		Name:        "Local AI Dashboard",
		Description: "Your local inference workspace",
		Services:    []application.Service{application.NewService(NewMonitorService()), application.NewService(chat), application.NewService(workspace), application.NewService(comparison)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(frontend)},
	})
	chat.emit = func(event ChatEvent) { app.Event.Emit("chat:chunk", event) }
	comparison.emit = func(event ComparisonEvent) { app.Event.Emit("comparison:chunk", event) }
	workspace.chooseExport = func(name string) (string, error) {
		return app.Dialog.SaveFile().SetFilename(name).CanCreateDirectories(true).PromptForSingleSelection()
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Local AI Dashboard", Width: 1240, Height: 860,
		MinWidth: 900, MinHeight: 650, URL: "/",
	})
	return app.Run()
}
