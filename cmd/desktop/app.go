package main

import (
	"context"
	"log/slog"

	"github.com/erenalidal/existora2pg/pkg/api"
)

// App struct provides Wails bindings.
type App struct {
	ctx    context.Context
	server *api.Server
	logger *slog.Logger
}

func NewApp(server *api.Server, logger *slog.Logger) *App {
	return &App{
		server: server,
		logger: logger,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.logger.Info("desktop app started")
}

func (a *App) shutdown(ctx context.Context) {
	a.logger.Info("desktop app shutting down")
	a.server.Shutdown(ctx)
}
