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
	"syscall"
	"time"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/config"
	"taskmanager-ai/internal/server"
	"taskmanager-ai/internal/storage"

	"github.com/joho/godotenv"
)

//go:embed all:web
var webFS embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := godotenv.Load(); err != nil {
		logger.Warn("failed to load .env", "error", err)
	}
	cfg := config.Load()
	conversationStore, err := storage.NewConversationStore(cfg.ContextDBPath)
	if err != nil {
		logger.Error("conversation store unavailable", "error", err, "path", cfg.ContextDBPath)
		os.Exit(1)
	}
	defer conversationStore.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := collector.New(cfg.MaxProcesses, logger)
	runtime := server.NewRuntime(c, storage.NewRingBuffer(cfg.HistorySize), logger)
	runtime.SetHistoryStore(conversationStore)
	if err := runtime.SetMemoryStore(ctx, conversationStore); err != nil {
		logger.Error("memory store unavailable", "error", err)
		os.Exit(1)
	}
	runtime.SetProfileStore(conversationStore)
	if cfg.AIAPIKey != "" {
		agent := ai.NewAgent(ai.NewClient(cfg.AIAPIKey, cfg.AIBaseURL, cfg.AIModel), ai.DefaultSystemPrompt, conversationStore, cfg.AIContextTokenBudget)
		agent.SetLogger(logger)
		runtime.SetAIAgent(agent)
	}
	runtime.Start(ctx, cfg.CollectorInterval)
	defer runtime.Stop()

	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		logger.Error("web fs", "error", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux, runtime, webRoot)
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
