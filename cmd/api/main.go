package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	// stdhttp "net/http"

	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/config"
	"github.com/ssinghl/couchbase-go/internal/couchbase"
	httpserver "github.com/ssinghl/couchbase-go/internal/http"
	"github.com/ssinghl/couchbase-go/internal/logger"
)

// @title Couchbase Go API
// @version 1.0
// @description REST API with Couchbase (gocb v2), chi router, zap logging.
// @BasePath /

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	// Build logger
	logFactory, err := logger.New(cfg.AppEnv, cfg.LogLevel)
	if err != nil {
		panic(err)
	}
	defer logFactory.Sync()
	log := logFactory.Base
	log = log.With(zap.String("service", "couchbase-go"), zap.String("env", cfg.AppEnv))

	// Initialize Couchbase client
	cbClient, err := couchbase.NewClient(cfg, log)
	if err != nil {
		log.Fatal("failed to connect to Couchbase", zap.Error(err))
	}
	defer func() {
		_ = cbClient.Close(context.Background())
	}()

	// Swagger removed

	// Router and HTTP server
	router := httpserver.NewRouter(cfg, log)
	httpserver.RegisterRoutes(router, cbClient, log)

	server := httpserver.NewServer(cfg, router)
	server.Start()
	log.Info("server started", zap.Int("port", cfg.ServerPort))

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Error("server shutdown error", zap.Error(err))
	}
}
