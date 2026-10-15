package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"chloe/internal/core"
	"chloe/internal/mcpserver"
	_ "chloe/internal/driver/mysql"
	_ "chloe/internal/driver/postgres"
	_ "chloe/internal/driver/sqlite"
	"chloe/internal/store"
	"chloe/services"
)

//go:embed all:frontend/dist
var assets embed.FS

// icon is shown in the dock and About box when running outside an app bundle.
//
//go:embed build/appicon.png
var icon []byte

func main() {
	if err := checkServerMode(); err != nil {
		log.Fatal(err)
	}
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
	if why := st.Locked(); why != "" {
		log.Print(why)
	}
	mgr := core.NewManager(st, services.Emitter{}, core.DefaultSettings)
	defer mgr.Close()
	approver := &services.MCPApprover{}
	mcpSrv := mcpserver.New(mgr, approver, services.MCPEvents{})
	defer mcpSrv.Close()

	app := application.New(application.Options{
		Name:        "Chloe",
		Description: "Database management for MySQL, PostgreSQL, SQLite, MongoDB and Redis",
		Icon:        icon,
		Services: []application.Service{
			application.NewService(&services.ConnectionService{M: mgr}),
			application.NewService(&services.ExplorerService{M: mgr}),
			application.NewService(&services.QueryService{M: mgr}),
			application.NewService(&services.MonitorService{M: mgr}),
			application.NewService(&services.DesignService{M: mgr}),
			application.NewService(&services.TransferService{M: mgr}),
			application.NewService(&services.LayoutService{M: mgr}),
			application.NewService(&services.ModelService{M: mgr}),
			application.NewService(&services.RedisService{M: mgr}),
			application.NewService(&services.MongoService{M: mgr}),
			application.NewService(&services.UserService{M: mgr}),
			application.NewService(&services.MCPService{S: mcpSrv, Approver: approver}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Menu.Set(appMenu())
	// Wails sets the icon before AppKit finishes launching, which can reset it to the
	// generic executable icon; set it again once launched.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		app.SetIcon(icon)
	})
	// The MCP server starts with the app when it's switched on.
	if err := mcpSrv.Load(); err != nil {
		log.Printf("MCP server: %v", err)
	}

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

// appMenu is the standard macOS menu minus the shortcuts Chloe uses itself:
// View > Reload (⌘R runs/refreshes) and File > Close Window (⌘W closes a tab).
func appMenu() *application.Menu {
	menu := application.NewMenu()
	menu.AddRole(application.AppMenu)
	menu.AddRole(application.EditMenu)
	view := menu.AddSubmenu("View")
	view.AddRole(application.ResetZoom)
	view.AddRole(application.ZoomIn)
	view.AddRole(application.ZoomOut)
	view.AddSeparator()
	view.AddRole(application.ToggleFullscreen)
	tools := menu.AddSubmenu("Tools")
	tools.Add("Data Transfer…").OnClick(func(*application.Context) { application.Get().Event.Emit("menu:data-transfer") })
	tools.Add("Structure Synchronization…").OnClick(func(*application.Context) { application.Get().Event.Emit("menu:structure-sync") })
	tools.Add("Data Synchronization…").OnClick(func(*application.Context) { application.Get().Event.Emit("menu:data-sync") })
	tools.AddSeparator()
	tools.Add("History Log").OnClick(func(*application.Context) { application.Get().Event.Emit("menu:history-log") })
	tools.AddSeparator()
	tools.Add("MCP Server…").OnClick(func(*application.Context) { application.Get().Event.Emit("menu:mcp") })
	menu.AddRole(application.WindowMenu)
	return menu
}
