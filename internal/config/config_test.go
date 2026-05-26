package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetEnv(t *testing.T) {
	key := "TEST_ENV_KEY_XYZ"
	defer os.Unsetenv(key)

	// Test default
	if val := getEnv(key, "default"); val != "default" {
		t.Errorf("expected 'default', got '%s'", val)
	}

	// Test value set
	os.Setenv(key, "myval")
	if val := getEnv(key, "default"); val != "myval" {
		t.Errorf("expected 'myval', got '%s'", val)
	}
}

func TestGetEnvBool(t *testing.T) {
	key := "TEST_ENV_BOOL_XYZ"
	defer os.Unsetenv(key)

	// Test default when unset
	if val := getEnvBool(key, true); val != true {
		t.Errorf("expected true, got %v", val)
	}

	// Test valid true value
	os.Setenv(key, "true")
	if val := getEnvBool(key, false); val != true {
		t.Errorf("expected true, got %v", val)
	}

	// Test valid false value
	os.Setenv(key, "false")
	if val := getEnvBool(key, true); val != false {
		t.Errorf("expected false, got %v", val)
	}

	// Test invalid value
	os.Setenv(key, "not-a-bool")
	if val := getEnvBool(key, true); val != true {
		t.Errorf("expected default true on invalid value, got %v", val)
	}
}

func TestGetEnvInt(t *testing.T) {
	key := "TEST_ENV_INT_XYZ"
	defer os.Unsetenv(key)

	// Test default when unset
	if val := getEnvInt(key, 42); val != 42 {
		t.Errorf("expected 42, got %d", val)
	}

	// Test valid integer
	os.Setenv(key, "100")
	if val := getEnvInt(key, 42); val != 100 {
		t.Errorf("expected 100, got %d", val)
	}

	// Test invalid integer
	os.Setenv(key, "not-an-int")
	if val := getEnvInt(key, 42); val != 42 {
		t.Errorf("expected default 42 on invalid value, got %d", val)
	}
}

func TestAutoDetectVault(t *testing.T) {
	// Create a temp directory
	tempDir, err := os.MkdirTemp("", "kb_vault_detect_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create expected folder structure
	rawDir := filepath.Join(tempDir, "sources", "raw")
	wikiDir := filepath.Join(tempDir, "wiki")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		t.Fatalf("failed to create raw dir: %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0755); err != nil {
		t.Fatalf("failed to create wiki dir: %v", err)
	}

	// Test detection from the root tempDir
	detected, ok := autoDetectVault(tempDir)
	if !ok {
		t.Errorf("expected to detect vault structure in %s", tempDir)
	}

	detectedAbs, _ := filepath.Abs(detected)
	expectedAbs, _ := filepath.Abs(tempDir)
	if detectedAbs != expectedAbs {
		t.Errorf("expected detected path %s, got %s", expectedAbs, detectedAbs)
	}

	// Test detection from a sub-directory under rawDir
	subDir := filepath.Join(rawDir, "nested", "folder")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create nested subdir: %v", err)
	}

	detected, ok = autoDetectVault(subDir)
	if !ok {
		t.Errorf("expected to detect vault structure from sub-directory %s", subDir)
	}
	detectedAbs, _ = filepath.Abs(detected)
	if detectedAbs != expectedAbs {
		t.Errorf("expected detected path %s from subdir, got %s", expectedAbs, detectedAbs)
	}

	// Test non-vault directory
	nonVaultDir, err := os.MkdirTemp("", "non_vault_test")
	if err == nil {
		defer os.RemoveAll(nonVaultDir)
		_, ok := autoDetectVault(nonVaultDir)
		if ok {
			t.Errorf("expected false for non-vault directory")
		}
	}
}

func TestLoad(t *testing.T) {
	// Set up environment values
	os.Setenv("COMPILE_MODEL_SINGLE", "test-model-single")
	os.Setenv("COMPILE_MODEL_MULTI", "test-model-multi")
	os.Setenv("OLLAMA_BASE_URL", "http://test-ollama:11434")
	os.Setenv("LOG_LEVEL", "debug")
	defer func() {
		os.Unsetenv("COMPILE_MODEL_SINGLE")
		os.Unsetenv("COMPILE_MODEL_MULTI")
		os.Unsetenv("OLLAMA_BASE_URL")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("VAULT_KB_PATH")
	}()

	// Create temp directory for a valid vault
	tempDir, err := os.MkdirTemp("", "kb_vault_load_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	rawDir := filepath.Join(tempDir, "sources", "raw")
	wikiDir := filepath.Join(tempDir, "wiki")
	_ = os.MkdirAll(rawDir, 0755)
	_ = os.MkdirAll(wikiDir, 0755)

	// Test load with vaultFlag (priority 1)
	cfg, err := Load(tempDir)
	if err != nil {
		t.Fatalf("Load with valid flag failed: %v", err)
	}

	if cfg.CompileModelSingle != "test-model-single" {
		t.Errorf("expected test-model-single, got %s", cfg.CompileModelSingle)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected debug, got %s", cfg.LogLevel)
	}

	// Test priority 2: VAULT_KB_PATH env
	os.Setenv("VAULT_KB_PATH", tempDir)
	cfg2, err := Load("")
	if err != nil {
		t.Fatalf("Load with valid env failed: %v", err)
	}
	if cfg2.VaultKBPath != cfg.VaultKBPath {
		t.Errorf("expected path %s, got %s", cfg.VaultKBPath, cfg2.VaultKBPath)
	}
	os.Unsetenv("VAULT_KB_PATH")

	// Test invalid vault path (fails validation)
	_, err = Load("invalid_path_does_not_exist_xyz")
	if err == nil {
		t.Error("expected error for non-existent vault path, got nil")
	}

	// Test invalid Ollama URL
	os.Setenv("OLLAMA_BASE_URL", "http://localhost\n")
	_, err = Load(tempDir)
	if err == nil {
		t.Error("expected error for invalid OLLAMA_BASE_URL, got nil")
	}
}
