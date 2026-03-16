package main

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/erenalidal/existora2pg/pkg/api"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// Set via ldflags
var debugMode string
var appVersion = "dev"

func main() {
	ensureOracleLibPath()

	logLevel := slog.LevelInfo
	if debugMode == "true" || os.Getenv("EXISTORA_DEBUG") != "" {
		logLevel = slog.LevelDebug
	}
	// Log to both stderr and file
	logFile, _ := os.OpenFile("existora2pg.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	var logWriter io.Writer = os.Stderr
	if logFile != nil {
		defer logFile.Close()
		logWriter = io.MultiWriter(os.Stderr, logFile)
	}
	logger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: logLevel}))
	logger.Info("existora2pg desktop starting", "debug", debugMode == "true", "log_level", logLevel.String())

	srv, err := api.NewServer(api.ServerConfig{
		Port:        9740,
		StateDBPath: "./existora2pg_state.db",
		CORSOrigins: "*",
		Logger:      logger,
		Version:     appVersion,
	})
	if err != nil {
		logger.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	// Also start HTTP server for SSE and external access
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			logger.Error("http server failed", "error", err)
		}
	}()

	// Sub into frontend/dist so Wails sees files at root level
	frontendFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		logger.Error("failed to sub frontend assets", "error", err)
		os.Exit(1)
	}

	app := NewApp(srv, logger)

	err = wails.Run(&options.App{
		Title:     "Existora2PG",
		Width:     1440,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 700,
		AssetServer: &assetserver.Options{
			Assets:  frontendFS,
			Handler: srv.APIHandler(),
		},
		Windows: &windows.Options{
			WebviewGpuIsDisabled: true,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		logger.Error("wails app failed", "error", err)
		fmt.Fprintf(os.Stderr, "\n*** FATAL: %v\n", err)
		fmt.Fprintln(os.Stderr, "Press Enter to exit...")
		fmt.Scanln()
		os.Exit(1)
	}
}
