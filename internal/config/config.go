package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	VaultKBPath         string
	CompileModelSingle  string
	CompileModelMulti   string
	LintModel           string
	QueryModel          string
	CleanupModel        string
	OllamaBaseURL       string
	OllamaCloudBaseURL  string
	OllamaCloudAPIKey   string
	GroqAPIKey          string
	GeminiAPIKey        string
	OpenRouterAPIKey    string
	WatcherPollFallback bool
	MultiDocThreshold   int
	LogLevel            string
}

func Load(vaultFlag string) (*Config, error) {
	// Attempt to load .env. Ignore error if file doesn't exist.
	_ = godotenv.Load()

	cfg := &Config{
		CompileModelSingle:  getEnv("COMPILE_MODEL_SINGLE", "gemma4:e4b"),
		CompileModelMulti:   getEnv("COMPILE_MODEL_MULTI", "llama-4-scout"),
		LintModel:           getEnv("LINT_MODEL", "llama-4-scout"),
		QueryModel:          getEnv("QUERY_MODEL", "llama-4-scout"),
		CleanupModel:        getEnv("CLEANUP_MODEL", "llama-4-scout"),
		OllamaBaseURL:       getEnv("OLLAMA_BASE_URL", "http://localhost:11434"),
		OllamaCloudBaseURL:  os.Getenv("OLLAMA_CLOUD_BASE_URL"),
		OllamaCloudAPIKey:   os.Getenv("OLLAMA_CLOUD_API_KEY"),
		GroqAPIKey:          os.Getenv("GROQ_API_KEY"),
		GeminiAPIKey:        os.Getenv("GEMINI_API_KEY"),
		OpenRouterAPIKey:    os.Getenv("OPENROUTER_API_KEY"),
		WatcherPollFallback: getEnvBool("WATCHER_POLL_FALLBACK", false),
		MultiDocThreshold:   getEnvInt("MULTI_DOC_THRESHOLD", 5),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
	}

	// Resolve Vault path using the priority chain:
	// 1. --vault flag
	// 2. VAULT_KB_PATH from .env
	// 3. Auto-detect from cwd walking upwards
	var vaultPath string
	if vaultFlag != "" {
		vaultPath = vaultFlag
	} else if envPath := os.Getenv("VAULT_KB_PATH"); envPath != "" {
		vaultPath = envPath
	} else {
		// Auto-detect
		cwd, err := os.Getwd()
		if err == nil {
			detected, ok := autoDetectVault(cwd)
			if ok {
				vaultPath = detected
			}
		}
	}

	if vaultPath == "" {
		return nil, fmt.Errorf("VAULT_KB_PATH not set, and vault structure not detected in current path or parent directories")
	}

	// Clean and check directory existence
	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return nil, fmt.Errorf("invalid vault path: %v", err)
	}
	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("vault directory does not exist or is not a directory: %s", absPath)
	}
	cfg.VaultKBPath = absPath

	// Validate other values
	if _, err := url.Parse(cfg.OllamaBaseURL); err != nil {
		return nil, fmt.Errorf("invalid OLLAMA_BASE_URL: %v", err)
	}

	logLevel := strings.ToLower(cfg.LogLevel)
	if logLevel != "debug" && logLevel != "info" && logLevel != "warn" && logLevel != "error" {
		cfg.LogLevel = "info"
	}

	return cfg, nil
}

func autoDetectVault(startDir string) (string, bool) {
	current := startDir
	for {
		rawDir := filepath.Join(current, "sources", "raw")
		wikiDir := filepath.Join(current, "wiki")

		rawStat, errRaw := os.Stat(rawDir)
		wikiStat, errWiki := os.Stat(wikiDir)

		if errRaw == nil && rawStat.IsDir() && errWiki == nil && wikiStat.IsDir() {
			return current, true
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", false
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return b
}

func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return i
}
