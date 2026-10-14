package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/core"
	_ "chloe/internal/driver/mysql"
	_ "chloe/internal/driver/postgres"
	"chloe/internal/store"
	"chloe/services"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// CHLOE_CONFIG_DIR and CHLOE_SECRETS=memory isolate test runs from the user's
	// saved connections and keychain.
	dir := os.Getenv("CHLOE_CONFIG_DIR")
	if dir == "" {
		cfgDir, err := os.UserConfigDir()
		if err != nil {
			log.Fatal(err)
		}
		dir = filepath.Join(cfgDir, "Chloe")
	}
	var secrets store.Secrets = store.Keyring{}
	if os.Getenv("CHLOE_SECRETS") == "memory" {
		secrets = &store.MemorySecrets{}
	}
	st, err := store.Open(dir, secrets)
	if err != nil {
		log.Fatal(err)
	}
	mgr := core.NewManager(st, services.Emitter{}, core.DefaultSettings)
	defer mgr.Close()

	app := application.New(application.Options{
		Name:        "Chloe",
		Description: "Database management for MySQL, PostgreSQL, MongoDB, Redis and more",
		Services: []application.Service{
			application.NewService(&services.ConnectionService{M: mgr}),
			application.NewService(&services.ExplorerService{M: mgr}),
			application.NewService(&services.QueryService{M: mgr}),
			application.NewService(&services.MonitorService{M: mgr}),
			application.NewService(&services.DesignService{M: mgr}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Chloe",
		Width:     1400,
		Height:    880,
		MinWidth:  900,
		MinHeight: 560,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 38,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(30, 31, 34),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
