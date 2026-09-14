package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
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
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/server"
	"taskmanager-ai/internal/storage"

	"github.com/joho/godotenv"
)

//go:embed all:web
var webFS embed.FS

type Config struct {
	Addr                     string
	CollectorInterval        time.Duration
	HistorySize              int
	MaxProcesses             int
	AIModel                  string
	AIBaseURL                string
	AIAPIKey                 string
	AIPrompt                 string
	ChatDBPath               string
	AIMetrics                ai.UsageConfig
	AIRecentMessagesLimit    int
	AIContextStrategy        domain.ContextStrategyName
	AISummarizationBatchSize int
	AIMaxSummaryTokens       int
	AISummarizationModel     string
}

func loadConfig() (Config, error) {
	interval := 2 * time.Second
	if raw := strings.TrimSpace(os.Getenv("COLLECTOR_INTERVAL_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			interval = time.Duration(ms) * time.Millisecond
		}
	}
	metrics, err := loadAIMetricsConfig()
	if err != nil {
		return Config{}, err
	}
	recent, err := contextRecentLimit()
	if err != nil {
		return Config{}, err
	}
	strategy, err := contextStrategy()
	if err != nil {
		return Config{}, err
	}
	batch, err := positiveEvenEnv("AI_SUMMARIZATION_BATCH_SIZE", 10)
	if err != nil {
		return Config{}, err
	}
	maxSummary, err := positiveEnv("AI_MAX_SUMMARY_TOKENS", 800)
	if err != nil {
		return Config{}, err
	}
	mainModel := envOrDefault("DEEPSEEK_MODEL", "deepseek-v4-flash")
	return Config{
		Addr:                  envOrDefault("ADDR", "127.0.0.1:8080"),
		CollectorInterval:     interval,
		HistorySize:           envInt("HISTORY_SIZE", 300),
		MaxProcesses:          envInt("MAX_PROCESSES", 100),
		AIModel:               mainModel,
		AIRecentMessagesLimit: recent, AIContextStrategy: strategy, AISummarizationBatchSize: batch, AIMaxSummaryTokens: maxSummary,
		AISummarizationModel: envOrDefault("AI_SUMMARIZATION_MODEL", mainModel),
		AIBaseURL:            envOrDefault("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		AIAPIKey:             strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")),
		AIPrompt:             strings.TrimSpace(os.Getenv("AI_SYSTEM_PROMPT")),
		ChatDBPath:           envOrDefault("CHAT_DB_PATH", "taskmanager.db"),
		AIMetrics:            metrics,
	}, nil
}

func contextRecentLimit() (int, error) {
	modern := strings.TrimSpace(os.Getenv("AI_CONTEXT_RECENT_MESSAGES_LIMIT"))
	legacy := strings.TrimSpace(os.Getenv("AI_RECENT_MESSAGES_LIMIT"))
	if modern != "" && legacy != "" && modern != legacy {
		return 0, fmt.Errorf("AI_CONTEXT_RECENT_MESSAGES_LIMIT and AI_RECENT_MESSAGES_LIMIT differ")
	}
	key := "AI_CONTEXT_RECENT_MESSAGES_LIMIT"
	if modern == "" {
		key = "AI_RECENT_MESSAGES_LIMIT"
	}
	return positiveEvenEnv(key, 20)
}

func contextStrategy() (domain.ContextStrategyName, error) {
	raw := strings.TrimSpace(os.Getenv("AI_CONTEXT_STRATEGY"))
	if raw == "" {
		return domain.ContextStrategySlidingWindow, nil
	}
	name := domain.ContextStrategyName(raw)
	switch name {
	case domain.ContextStrategySlidingWindow, domain.ContextStrategyStickyFacts, domain.ContextStrategyBranching:
		return name, nil
	default:
		return "", fmt.Errorf("AI_CONTEXT_STRATEGY must be sliding_window, sticky_facts, or branching")
	}
}

func positiveEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return n, nil
}
func positiveEvenEnv(key string, fallback int) (int, error) {
	n, err := positiveEnv(key, fallback)
	if err == nil && n%2 != 0 {
		return 0, fmt.Errorf("%s must be a positive even integer", key)
	}
	return n, err
}

func loadAIMetricsConfig() (ai.UsageConfig, error) {
	var config ai.UsageConfig
	if raw := strings.TrimSpace(os.Getenv("AI_CONTEXT_WINDOW_TOKENS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return ai.UsageConfig{}, fmt.Errorf("AI_CONTEXT_WINDOW_TOKENS must be a non-negative integer")
		}
		config.ContextWindowTokens = n
	}
	input, inputSet, err := optionalDecimalEnv("AI_INPUT_PRICE_PER_MILLION_TOKENS")
	if err != nil {
		return ai.UsageConfig{}, err
	}
	output, outputSet, err := optionalDecimalEnv("AI_OUTPUT_PRICE_PER_MILLION_TOKENS")
	if err != nil {
		return ai.UsageConfig{}, err
	}
	if inputSet && outputSet {
		config.InputPricePerMillionTokens = input
		config.OutputPricePerMillionTokens = output
		config.PricingConfigured = true
	}
	return config, nil
}

func optionalDecimalEnv(key string) (float64, bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || !finiteNonNegative(v) {
		return 0, false, fmt.Errorf("%s must be a non-negative decimal", key)
	}
	return v, true, nil
}

func finiteNonNegative(v float64) bool { return v >= 0 && v < 1e308 }
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
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := collector.New(cfg.MaxProcesses, logger)
	state := server.NewState(c, storage.NewRingBuffer(cfg.HistorySize), logger)
	chatHistory, err := storage.OpenChatHistory(cfg.ChatDBPath)
	if err != nil {
		logger.Error("chat database", "error", err)
		os.Exit(1)
	}
	defer chatHistory.Close()
	state.SetAIHistory(chatHistory)
	if cfg.AIAPIKey != "" {
		client := ai.NewClient(cfg.AIAPIKey, cfg.AIBaseURL, cfg.AIModel, ai.WithLogger(logger), ai.WithUsageConfig(cfg.AIMetrics))
		manager := newContextManager(cfg.AIContextStrategy, chatHistory, cfg.AIRecentMessagesLimit, client)
		state.SetAIAgent(ai.NewAgentWithContextManager(client, cfg.AIPrompt, chatHistory, manager))
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

func newContextManager(name domain.ContextStrategyName, store *storage.ChatHistory, limit int, client *ai.Client) ai.ContextManager {
	switch name {
	case domain.ContextStrategyStickyFacts:
		return ai.NewStickyFacts(store, store.ListFacts, limit).WithExtractor(ai.NewFactExtractor(client, envOrDefault("AI_FACT_MODEL", ""), envInt("AI_FACT_MAX_ITEMS", 32)))
	case domain.ContextStrategyBranching:
		return ai.NewBranching(store)
	default:
		return ai.NewSlidingWindow(store, limit)
	}
}

func loggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}
