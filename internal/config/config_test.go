package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{"ADDR", "COLLECTOR_INTERVAL_MS", "HISTORY_SIZE", "MAX_PROCESSES", "AI_API_KEY", "AI_BASE_URL", "AI_MODEL", "AI_JSON_MODE", "DEEPSEEK_API_KEY", "DEEPSEEK_MODEL", "DEEPSEEK_BASE_URL", "CONTEXT_DB_PATH", "AI_CONTEXT_TOKEN_BUDGET"} {
		t.Setenv(key, "")
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	wantDBPath := filepath.Join("/home/tester", ".local", "share", "system-monitor-ai", "system-monitor-context.db")
	t.Setenv("HOME", "/home/tester")
	if cfg, err := LoadWithError(); err != nil {
		t.Fatal(err)
	} else if cfg.Addr != "127.0.0.1:8080" || cfg.CollectorInterval != 2*time.Second || cfg.HistorySize != 300 || cfg.MaxProcesses != 100 || cfg.AIModel != "" || cfg.AIBaseURL != "" || cfg.AIAPIKey != "" || !cfg.AIJSONMode || cfg.AIConfigured || cfg.ContextDBPath != wantDBPath || cfg.AIContextTokenBudget != 8000 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestDefaultContextDBPathByOS(t *testing.T) {
	tests := []struct {
		name, goos, xdg, home, localAppData, want string
	}{
		{name: "linux xdg", goos: "linux", xdg: "/data", home: "/home/tester", want: filepath.Join("/data", "system-monitor-ai", "system-monitor-context.db")},
		{name: "linux home", goos: "linux", home: "/home/tester", want: filepath.Join("/home/tester", ".local", "share", "system-monitor-ai", "system-monitor-context.db")},
		{name: "windows", goos: "windows", localAppData: `C:\Users\tester\AppData\Local`, want: filepath.Join(`C:\Users\tester\AppData\Local`, "SystemMonitorAI", "system-monitor-context.db")},
		{name: "macos", goos: "darwin", home: "/Users/tester", want: filepath.Join("/Users/tester", "Library", "Application Support", "SystemMonitorAI", "system-monitor-context.db")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := defaultContextDBPath(test.goos, test.xdg, test.home, test.localAppData)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("defaultContextDBPath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDefaultContextDBPathMissingDirectory(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			if _, err := defaultContextDBPath(goos, "", "", ""); err == nil {
				t.Fatal("defaultContextDBPath() succeeded without a user data directory")
			}
		})
	}
}

func TestDefaultContextDBPathRejectsRelativeDirectories(t *testing.T) {
	tests := []struct {
		name, goos, xdg, home, localAppData string
	}{
		{name: "linux xdg", goos: "linux", xdg: "relative/data", home: "/home/tester"},
		{name: "linux home", goos: "linux", home: "relative/home"},
		{name: "macos home", goos: "darwin", home: "relative/home"},
		{name: "windows local app data", goos: "windows", localAppData: `relative\\data`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := defaultContextDBPath(test.goos, test.xdg, test.home, test.localAppData); err == nil {
				t.Fatal("defaultContextDBPath() accepted a relative directory")
			}
		})
	}
}

func TestContextDBPathOverride(t *testing.T) {
	t.Setenv("CONTEXT_DB_PATH", "/custom/context.db")
	t.Setenv("HOME", "")
	cfg, err := LoadWithError()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextDBPath != "/custom/context.db" {
		t.Fatalf("ContextDBPath = %q, want override", cfg.ContextDBPath)
	}
}

func TestLoadConfiguredValues(t *testing.T) {
	for _, key := range []string{"AI_API_KEY", "AI_BASE_URL", "AI_MODEL", "AI_JSON_MODE", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "DEEPSEEK_MODEL"} {
		t.Setenv(key, "")
	}
	t.Setenv("ADDR", "127.0.0.1:9090")
	t.Setenv("COLLECTOR_INTERVAL_MS", "500")
	t.Setenv("HISTORY_SIZE", "20")
	t.Setenv("MAX_PROCESSES", "7")
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("AI_BASE_URL", "http://localhost")
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_JSON_MODE", "false")
	t.Setenv("CONTEXT_DB_PATH", "/tmp/test-context.db")
	t.Setenv("AI_CONTEXT_TOKEN_BUDGET", "4096")
	cfg := Load()
	if cfg.Addr != "127.0.0.1:9090" || cfg.CollectorInterval != 500*time.Millisecond || cfg.HistorySize != 20 || cfg.MaxProcesses != 7 || cfg.AIModel != "test-model" || cfg.AIBaseURL != "http://localhost" || cfg.AIAPIKey != "test-key" || cfg.AIJSONMode || !cfg.AIConfigured || cfg.ContextDBPath != "/tmp/test-context.db" || cfg.AIContextTokenBudget != 4096 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestLoadLegacyDeepSeekKeyOnlyDefaults(t *testing.T) {
	for _, key := range []string{"AI_API_KEY", "AI_BASE_URL", "AI_MODEL", "DEEPSEEK_BASE_URL", "DEEPSEEK_MODEL"} {
		t.Setenv(key, "")
	}
	t.Setenv("DEEPSEEK_API_KEY", "legacy-key")
	cfg := Load()
	if cfg.AIAPIKey != "legacy-key" || cfg.AIBaseURL != "https://api.deepseek.com" || cfg.AIModel != "deepseek-flash" || !cfg.AIConfigured {
		t.Fatalf("legacy config = %#v", cfg)
	}
}

func TestLoadCanonicalValuesWinOverLegacy(t *testing.T) {
	t.Setenv("AI_API_KEY", "generic-key")
	t.Setenv("AI_BASE_URL", "http://generic")
	t.Setenv("AI_MODEL", "generic-model")
	t.Setenv("DEEPSEEK_API_KEY", "legacy-key")
	t.Setenv("DEEPSEEK_BASE_URL", "http://legacy")
	t.Setenv("DEEPSEEK_MODEL", "legacy-model")
	cfg := Load()
	if cfg.AIAPIKey != "generic-key" || cfg.AIBaseURL != "http://generic" || cfg.AIModel != "generic-model" {
		t.Fatalf("canonical config = %#v", cfg)
	}
}

func TestLoadCanonicalEndpointDoesNotLeakLegacyKey(t *testing.T) {
	t.Setenv("AI_BASE_URL", "http://127.0.0.1:11434/v1")
	t.Setenv("AI_MODEL", "llama3.2")
	t.Setenv("AI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "legacy-key-must-not-leak")
	t.Setenv("DEEPSEEK_BASE_URL", "https://api.deepseek.com")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-flash")

	cfg := Load()
	if cfg.AIAPIKey != "" || cfg.AIBaseURL != "http://127.0.0.1:11434/v1" || cfg.AIModel != "llama3.2" || !cfg.AIConfigured {
		t.Fatalf("canonical local config leaked legacy values: %#v", cfg)
	}
}

func TestLoadKeylessLocalProviderIsConfigured(t *testing.T) {
	for _, key := range []string{"AI_API_KEY", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "DEEPSEEK_MODEL"} {
		t.Setenv(key, "")
	}
	t.Setenv("AI_BASE_URL", "http://127.0.0.1:11434/v1")
	t.Setenv("AI_MODEL", "llama3.2")
	cfg := Load()
	if cfg.AIAPIKey != "" || !cfg.AIConfigured {
		t.Fatalf("keyless local config = %#v", cfg)
	}
}

func TestLoadJSONModeInvalidValueDefaultsToEnabled(t *testing.T) {
	t.Setenv("AI_JSON_MODE", "not-a-bool")
	if !Load().AIJSONMode {
		t.Fatal("invalid AI_JSON_MODE should default to true")
	}
}
