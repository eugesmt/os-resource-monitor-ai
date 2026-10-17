package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                 string
	CollectorInterval    time.Duration
	HistorySize          int
	MaxProcesses         int
	AIModel              string
	AIBaseURL            string
	AIAPIKey             string
	AIJSONMode           bool
	AIConfigured         bool
	ContextDBPath        string
	AIContextTokenBudget int
}

// Load returns the process configuration. Call LoadWithError when startup
// should report an unresolved per-user data directory to the user.
func Load() Config {
	cfg, _ := LoadWithError()
	return cfg
}

// LoadWithError loads configuration and resolves the default per-user
// database location. CONTEXT_DB_PATH remains authoritative when set.
func LoadWithError() (Config, error) {
	interval := 2 * time.Second
	if raw := strings.TrimSpace(os.Getenv("COLLECTOR_INTERVAL_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			interval = time.Duration(ms) * time.Millisecond
		}
	}
	aiBaseURL, canonicalBaseURL := envValue("AI_BASE_URL")
	aiModel, canonicalModel := envValue("AI_MODEL")
	canonicalMode := canonicalBaseURL || canonicalModel
	aiAPIKey := ""
	aiConfigured := false
	if canonicalMode {
		// A canonical endpoint/model selects the generic mode as a whole. Do
		// not let an old DEEPSEEK_API_KEY leak into a local/provider setup;
		// LookupEnv also preserves an explicitly empty canonical key.
		if raw, ok := os.LookupEnv("AI_API_KEY"); ok {
			aiAPIKey = strings.TrimSpace(raw)
		}
		aiConfigured = aiBaseURL != "" && aiModel != ""
	} else {
		legacyKey, legacyKeySet := envValue("DEEPSEEK_API_KEY")
		legacyBaseURL, legacyBaseURLSet := envValue("DEEPSEEK_BASE_URL")
		legacyModel, legacyModelSet := envValue("DEEPSEEK_MODEL")
		legacyConfigured := legacyKeySet || legacyBaseURLSet || legacyModelSet
		aiAPIKey = legacyKey
		aiBaseURL = legacyBaseURL
		aiModel = legacyModel
		// Keep the old DEEPSEEK_API_KEY-only setup working, while not
		// enabling the application merely because generic defaults exist.
		if legacyConfigured {
			if aiBaseURL == "" {
				aiBaseURL = "https://api.deepseek.com"
			}
			if aiModel == "" {
				aiModel = "deepseek-flash"
			}
		}
		aiConfigured = legacyConfigured && aiBaseURL != "" && aiModel != ""
	}
	jsonMode := true
	if raw, ok := envValue("AI_JSON_MODE"); ok {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			jsonMode = parsed
		}
	}

	contextDBPath, err := resolveContextDBPath()
	if err != nil {
		return Config{}, err
	}
	return Config{
		Addr:                 EnvOrDefault("ADDR", "127.0.0.1:8080"),
		CollectorInterval:    interval,
		HistorySize:          EnvInt("HISTORY_SIZE", 300),
		MaxProcesses:         EnvInt("MAX_PROCESSES", 100),
		AIModel:              aiModel,
		AIBaseURL:            aiBaseURL,
		AIAPIKey:             aiAPIKey,
		AIJSONMode:           jsonMode,
		AIConfigured:         aiConfigured,
		ContextDBPath:        contextDBPath,
		AIContextTokenBudget: EnvInt("AI_CONTEXT_TOKEN_BUDGET", 8000),
	}, nil
}

func resolveContextDBPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("CONTEXT_DB_PATH")); path != "" {
		return path, nil
	}
	return defaultContextDBPath(runtime.GOOS, os.Getenv("XDG_DATA_HOME"), os.Getenv("HOME"), os.Getenv("LOCALAPPDATA"))
}

func defaultContextDBPath(goos, xdgDataHome, home, localAppData string) (string, error) {
	xdgDataHome = strings.TrimSpace(xdgDataHome)
	home = strings.TrimSpace(home)
	localAppData = strings.TrimSpace(localAppData)
	switch goos {
	case "windows":
		if localAppData == "" {
			return "", errors.New("cannot resolve Windows data directory: set LOCALAPPDATA or CONTEXT_DB_PATH")
		}
		if !isAbsolutePath(goos, localAppData) {
			return "", errors.New("LOCALAPPDATA must be an absolute path; set CONTEXT_DB_PATH for an explicit relative path")
		}
		return filepath.Join(localAppData, "SystemMonitorAI", "system-monitor-context.db"), nil
	case "darwin":
		if home == "" {
			return "", errors.New("cannot resolve macOS data directory: set HOME or CONTEXT_DB_PATH")
		}
		if !isAbsolutePath(goos, home) {
			return "", errors.New("HOME must be an absolute path; set CONTEXT_DB_PATH for an explicit relative path")
		}
		return filepath.Join(home, "Library", "Application Support", "SystemMonitorAI", "system-monitor-context.db"), nil
	default:
		if xdgDataHome != "" {
			if !isAbsolutePath(goos, xdgDataHome) {
				return "", errors.New("XDG_DATA_HOME must be an absolute path; set CONTEXT_DB_PATH for an explicit relative path")
			}
		} else {
			if home == "" {
				return "", errors.New("cannot resolve Linux data directory: set XDG_DATA_HOME, HOME, or CONTEXT_DB_PATH")
			}
			if !isAbsolutePath(goos, home) {
				return "", errors.New("HOME must be an absolute path; set CONTEXT_DB_PATH for an explicit relative path")
			}
			xdgDataHome = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(xdgDataHome, "system-monitor-ai", "system-monitor-context.db"), nil
	}
}

func isAbsolutePath(goos, value string) bool {
	if goos != "windows" {
		return filepath.IsAbs(value)
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, `\\`) {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func envValue(key string) (string, bool) {
	value := strings.TrimSpace(os.Getenv(key))
	return value, value != ""
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
