package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"research-assistant-backend/internal/api"
	"research-assistant-backend/internal/apppaths"
	"research-assistant-backend/internal/logging"
	"research-assistant-backend/internal/proxyenv"
)

var backendLogWriter *logging.RotatingWriter

func main() {
	proxyenv.Apply()
	configureFileLogging()

	r := gin.Default()

	// Health check endpoint
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
			"status":  "ResearchAssistant Agent Backend is running",
		})
	})

	// Setup API routes
	api.SetupRoutes(r)

	// Configure a custom HTTP server with generous timeouts for Agent execution
	addr := serverAddr()
	server := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute, // SSE needs long write timeout
		IdleTimeout:  10 * time.Minute,
	}

	// Graceful shutdown: release the memory/wiki file locks and flush snapshots
	// before exiting. Without this a SIGTERM leaves a stale lock and the next
	// start falls back to an in-memory store. Main waits for shutdownDone so the
	// process cannot exit between ListenAndServe returning and the store Close.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		<-signals
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		api.CloseRuntimeStores()
	}()

	log.Printf("Starting server on %s with 5min timeouts...", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
	<-shutdownDone
}

func serverAddr() string {
	if addr := strings.TrimSpace(os.Getenv("APP_ADDR")); addr != "" {
		return addr
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

func configureFileLogging() {
	logDir := apppaths.LogDir()
	if err := apppaths.EnsureDir(logDir); err != nil {
		log.Printf("failed to create log dir %s: %v", logDir, err)
		return
	}

	writer, err := logging.Open(filepath.Join(logDir, "backend.log"), 20<<20, 5)
	if err != nil {
		log.Printf("failed to open backend log file: %v", err)
		return
	}
	backendLogWriter = writer
	multi := io.MultiWriter(os.Stdout, writer)
	log.SetOutput(multi)
	gin.DefaultWriter = multi
	gin.DefaultErrorWriter = multi
}
