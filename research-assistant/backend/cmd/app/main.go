package main

import (
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"research-assistant-backend/internal/api"
	"research-assistant-backend/internal/apppaths"
	"research-assistant-backend/internal/logging"
	"research-assistant-backend/internal/proxyenv"
	"research-assistant-backend/webui"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var appLogWriter *logging.RotatingWriter

func main() {
	proxyenv.Apply()
	configureFileLogging()

	if strings.TrimSpace(os.Getenv("SANDBOX_URL")) == "" && strings.TrimSpace(os.Getenv("APP_DATA_DIR")) == "" {
		log.Printf("warning: SANDBOX_URL is not set; the desktop supervisor normally injects it. Falling back to http://127.0.0.1:8082 for development")
	}

	r := gin.Default()
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
			"status":  "ResearchAssistant Agent App is running",
		})
	})

	api.SetupRoutes(r)
	registerEmbeddedFrontend(r)

	listenAddr := resolveListenAddr()
	server := &http.Server{
		Addr:         listenAddr,
		Handler:      r,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  10 * time.Minute,
	}

	log.Printf("Starting single-binary app on %s", listenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("failed to start single-binary app: %v", err)
	}
}

func registerEmbeddedFrontend(r *gin.Engine) {
	distFS, err := webui.DistFS()
	if err != nil {
		log.Printf("embedded frontend is unavailable: %v", err)
		return
	}

	fileServer := http.FileServer(http.FS(distFS))
	r.NoRoute(func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		if isAPINotFound(requestPath) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// SPA 场景下优先尝试命中真实静态文件，找不到时统一回退到 index.html。
		assetPath := normalizeAssetPath(requestPath)
		if assetExists(distFS, assetPath) {
			c.Request.URL.Path = "/" + assetPath
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		c.Request.URL.Path = "/index.html"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

func isAPINotFound(requestPath string) bool {
	return requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") || requestPath == "/ping"
}

func normalizeAssetPath(requestPath string) string {
	cleaned := path.Clean("/" + requestPath)
	trimmed := strings.TrimPrefix(cleaned, "/")
	if trimmed == "" || trimmed == "." {
		return "index.html"
	}
	return trimmed
}

func assetExists(distFS fs.FS, assetPath string) bool {
	info, err := fs.Stat(distFS, assetPath)
	return err == nil && !info.IsDir()
}

func resolveListenAddr() string {
	if addr := strings.TrimSpace(os.Getenv("APP_ADDR")); addr != "" {
		return addr
	}

	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		if strings.HasPrefix(port, ":") {
			return port
		}
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

	writer, err := logging.Open(filepath.Join(logDir, "app.log"), 20<<20, 5)
	if err != nil {
		log.Printf("failed to open app log file: %v", err)
		return
	}
	appLogWriter = writer
	multi := io.MultiWriter(os.Stdout, writer)
	log.SetOutput(multi)
	gin.DefaultWriter = multi
	gin.DefaultErrorWriter = multi
	log.Printf("single-binary logs redirected to %s", filepath.Join(logDir, "app.log"))
}
