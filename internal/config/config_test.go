package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{"ADDR", "COLLECTOR_INTERVAL_MS", "HISTORY_SIZE", "MAX_PROCESSES", "DEEPSEEK_MODEL", "DEEPSEEK_BASE_URL"} {
		t.Setenv(key, "")
	}
	cfg := Load()
	if cfg.Addr != "127.0.0.1:8080" || cfg.CollectorInterval != 2*time.Second || cfg.HistorySize != 300 || cfg.MaxProcesses != 100 || cfg.AIModel != "deepseek-flash" || cfg.AIBaseURL != "https://api.deepseek.com" {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestLoadConfiguredValues(t *testing.T) {
	t.Setenv("ADDR", "127.0.0.1:9090")
	t.Setenv("COLLECTOR_INTERVAL_MS", "500")
	t.Setenv("HISTORY_SIZE", "20")
	t.Setenv("MAX_PROCESSES", "7")
	t.Setenv("DEEPSEEK_MODEL", "test-model")
	t.Setenv("DEEPSEEK_BASE_URL", "http://localhost")
	cfg := Load()
	if cfg.Addr != "127.0.0.1:9090" || cfg.CollectorInterval != 500*time.Millisecond || cfg.HistorySize != 20 || cfg.MaxProcesses != 7 || cfg.AIModel != "test-model" || cfg.AIBaseURL != "http://localhost" {
		t.Fatalf("config = %#v", cfg)
	}
}
