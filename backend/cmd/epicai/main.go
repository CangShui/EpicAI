// Command epicai runs the OpenAI compatible AI endpoint simulator.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/epicai/epicai/backend/internal/assets"
	"github.com/epicai/epicai/backend/internal/audit"
	"github.com/epicai/epicai/backend/internal/auth"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/events"
	"github.com/epicai/epicai/backend/internal/ratelimit"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/storage"
	sqlitestore "github.com/epicai/epicai/backend/internal/storage/sqlite"
	"github.com/epicai/epicai/backend/internal/vlog"
)

func main() {
	cfg := config.Init()
	vlog.Init("logs")

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Level()}))
	slog.SetDefault(logger)

	store, err := sqlitestore.Open(cfg.Static().DatabaseURL)
	if err != nil {
		logger.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	assetsStore, err := assets.New(store)
	if err != nil {
		logger.Error("failed to initialise asset storage", "err", err)
		os.Exit(1)
	}

	if err := seedDefaults(store); err != nil {
		logger.Error("failed to seed defaults", "err", err)
	}

	bus := events.New()
	limiter := ratelimit.Init()
	ratelimit.ApplyRuntimeConfig()

	manager := sessions.NewManager(store, bus, limiter, logger)
	sessions.SetManagerRef(manager)

	keyMgr := auth.New(store)
	adminAuth := auth.NewAdmin()
	if savedPass, err := store.GetSetting(context.Background(), "admin_password"); err == nil && savedPass != "" {
		adminAuth.SetPassword(savedPass)
	}
	auditLog := audit.New(store)

	wireRoutes := newRouter(routerDeps{
		store: store, manager: manager, bus: bus, logger: logger,
		keyMgr: keyMgr, adminAuth: adminAuth, auditLog: auditLog,
		limiter: limiter, assets: assetsStore, cfg: cfg,
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           wireRoutes,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       time.Duration(maxInt(cfg.Static().ReadTimeoutSec, 30)) * time.Second,
		// No WriteTimeout: infinite streaming sessions are deliberately unbounded.
		WriteTimeout:   0,
		IdleTimeout:    0,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		logger.Info("EpicAI listening", "addr", cfg.Addr(), "admin", "/admin/",
			"models", "/v1/models", "health", "/health")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Ignore(syscall.SIGHUP)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down")
	auditLog.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func seedDefaults(store storage.Store) error {
	if _, err := store.GetModel(context.Background(), "epic-alpha"); err == nil {
		return nil
	}
	now := time.Now()
	m := storage.Model{
		ModelID: "epic-alpha", DisplayName: "Epic Alpha (Infinite Echo)",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
		Behavior: storage.BehaviorInfiniteEcho, EchoIntervalMS: 500,
		ProtocolMode: "openai", Description: "Echoes client input forever until interrupted.",
		EchoContentMode: "message",
	}
	return store.CreateModel(context.Background(), &m)
}

var _ = io.Reader(nil)
