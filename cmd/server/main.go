package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/server"
	"taskmanager-ai/internal/storage"

	"github.com/joho/godotenv"
)

//go:embed all:web
var webFS embed.FS

type Config struct {
	Addr              string
	CollectorInterval time.Duration
	HistorySize       int
	MaxProcesses      int
	AIModel           string
	AIBaseURL         string
	AIAPIKey          string
}

func loadConfig() Config {
	interval := 2 * time.Second
	if raw := strings.TrimSpace(os.Getenv("COLLECTOR_INTERVAL_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			interval = time.Duration(ms) * time.Millisecond
		}
	}
	return Config{
		Addr:              envOrDefault("ADDR", "127.0.0.1:8080"),
		CollectorInterval: interval,
		HistorySize:       envInt("HISTORY_SIZE", 300),
		MaxProcesses:      envInt("MAX_PROCESSES", 100),
		AIModel:           envOrDefault("DEEPSEEK_MODEL", "deepseek-v4-flash"),
		AIBaseURL:         envOrDefault("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		AIAPIKey:          strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")),
	}
}
func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := godotenv.Load(); err != nil {
		logger.Warn("failed to load .env", "error", err)
	}
	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := collector.New(cfg.MaxProcesses, logger)
	state := server.NewState(c, storage.NewRingBuffer(cfg.HistorySize), logger)
	if cfg.AIAPIKey != "" {
		state.SetAIClient(ai.NewClient(cfg.AIAPIKey, cfg.AIBaseURL, cfg.AIModel))
	}
	state.Start(ctx, cfg.CollectorInterval)
	defer state.Stop()

	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		logger.Error("web fs", "error", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux, state, webRoot)
	srv := &http.Server{Addr: cfg.Addr, Handler: loggingMiddleware(mux, logger), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}

	go func() {
		logger.Info("web server started", "url", "http://"+cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("web server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown failed", "error", err)
	}
}

func loggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}
