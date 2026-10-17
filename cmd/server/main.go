package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"system-monitor-ai/internal/ai"
	"system-monitor-ai/internal/collector"
	"system-monitor-ai/internal/config"
	"system-monitor-ai/internal/mcpserver"
	"system-monitor-ai/internal/metrics"
	"system-monitor-ai/internal/server"
	"system-monitor-ai/internal/storage"

	"github.com/joho/godotenv"
)

//go:embed all:web
var webFS embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("failed to load .env", "error", err)
	}
	cfg, err := config.LoadWithError()
	if err != nil {
		logger.Error("configuration unavailable", "error", err)
		os.Exit(1)
	}
	if err := storage.MigrateConversationDB(ctx, cfg.ContextDBPath); err != nil {
		logger.Error("conversation database migration failed", "error", err, "path", cfg.ContextDBPath)
		os.Exit(1)
	}
	conversationStore, err := storage.NewConversationStore(cfg.ContextDBPath)
	if err != nil {
		logger.Error("conversation store unavailable", "error", err, "path", cfg.ContextDBPath)
		os.Exit(1)
	}
	defer conversationStore.Close()
	c := collector.New(cfg.MaxProcesses, logger)
	runtime := server.NewRuntime(c, storage.NewRingBuffer(cfg.HistorySize), logger)
	runtime.SetMetricsStore(conversationStore)
	runtime.SetHistoryStore(conversationStore)
	runtime.SetTaskStateStore(conversationStore)
	if err := runtime.SetMemoryStore(ctx, conversationStore); err != nil {
		logger.Error("memory store unavailable", "error", err)
		os.Exit(1)
	}
	runtime.SetProfileStore(conversationStore)
	if err := runtime.SetInvariantStore(ctx, conversationStore); err != nil {
		logger.Error("invariant store unavailable", "error", err)
		os.Exit(1)
	}
	if cfg.AIConfigured {
		agent := ai.NewAgent(ai.NewClientWithOptions(ai.ClientOptions{APIKey: cfg.AIAPIKey, BaseURL: cfg.AIBaseURL, Model: cfg.AIModel, JSONMode: cfg.AIJSONMode}), ai.DefaultSystemPrompt, conversationStore, cfg.AIContextTokenBudget)
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
	registerRoutes(mux, runtime, webRoot, cfg.Addr, systemMonitorMCPReportDirectory())
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

func systemMonitorMCPReportDirectory() string {
	return os.Getenv("SYSTEM_MONITOR_MCP_REPORT_DIR")
}

func registerRoutes(mux *http.ServeMux, runtime *server.Runtime, webRoot fs.FS, address, reportDirectory string) {
	server.RegisterRoutes(mux, runtime, webRoot)
	reportDirectory = strings.TrimSpace(reportDirectory)
	mcpOptions := []mcpserver.ServerOption{mcpserver.WithReportDirectory(reportDirectory)}
	if reportDirectory != "" {
		if baseURL, ok := reportDownloadBaseURL(address); ok {
			mcpOptions = append(mcpOptions, mcpserver.WithReportDownloadBaseURL(baseURL))
		}
		mux.Handle("/reports/", mcpserver.NewReportDownloadHandler(reportDirectory))
	}
	mux.Handle("/mcp", mcpserver.NewStreamableHTTPHandler(runtimeDataSource{runtime: runtime}, mcpOptions...))
}

func reportDownloadBaseURL(address string) (string, bool) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || host == "" || port == "" {
		return "", false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return "", false
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/reports"}
	return u.String(), true
}

type runtimeDataSource struct {
	runtime *server.Runtime
}

func (s runtimeDataSource) Snapshot(ctx context.Context) (collector.SystemSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return collector.SystemSnapshot{}, err
	}
	return s.runtime.GetSnapshot()
}

func (s runtimeDataSource) History(ctx context.Context, samples int) ([]collector.SystemSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if samples < 1 || samples > 120 {
		return nil, fmt.Errorf("history samples must be between 1 and 120")
	}
	return s.runtime.GetHistory(samples), nil
}

func (s runtimeDataSource) MetricsSummary(ctx context.Context, from, to time.Time) (metrics.Summary, error) {
	return s.runtime.MetricsSummary(ctx, from, to)
}

func loggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}
