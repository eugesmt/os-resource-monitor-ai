package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr              string
	CollectorInterval time.Duration
	HistorySize       int
	MaxProcesses      int
	AIModel           string
	AIBaseURL         string
	AIAPIKey          string
}

func Load() Config {
	interval := 2 * time.Second
	if raw := strings.TrimSpace(os.Getenv("COLLECTOR_INTERVAL_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			interval = time.Duration(ms) * time.Millisecond
		}
	}
	return Config{
		Addr:              EnvOrDefault("ADDR", "127.0.0.1:8080"),
		CollectorInterval: interval,
		HistorySize:       EnvInt("HISTORY_SIZE", 300),
		MaxProcesses:      EnvInt("MAX_PROCESSES", 100),
		AIModel:           EnvOrDefault("DEEPSEEK_MODEL", "deepseek-flash"),
		AIBaseURL:         EnvOrDefault("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		AIAPIKey:          strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")),
	}
}

func EnvInt(key string, fallback int) int {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func EnvOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
