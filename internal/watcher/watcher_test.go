package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/avnis/kb-system/internal/compiler"
	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/vault"
)

type mockProvider struct {
	response string
}

func (m *mockProvider) Generate(ctx context.Context, model string, prompt string) (string, error) {
	return m.response, nil
}

func (m *mockProvider) Name() string {
	return "MockProvider"
}

func (m *mockProvider) Available(ctx context.Context) (bool, error) {
	return true, nil
}

func TestWatcherPollingMode(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kb-watcher-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		VaultKBPath:        tmpDir,
		CompileModelSingle: "mock-model",
		MultiDocThreshold:  3,
	}

	err = vault.EnsureStructure(cfg)
	if err != nil {
		t.Fatalf("failed to ensure vault structure: %v", err)
	}

	mockJSON := `{"title": "Test Concept", "type": "concept", "tags": ["test"], "provenance": "extracted", "summary": "This is a test concept.", "body": "Body content"}`
	prov := &mockProvider{response: mockJSON}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := compiler.New(cfg, prov, logger)

	// Start in polling mode
	w := New(cfg, c, logger, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- w.Start(ctx)
	}()

	// Write raw file
	rawPath := filepath.Join(vault.RawDir(cfg), "test-input.md")
	sf := frontmatter.SourceFrontmatter{
		Title:  "Test Input",
		Status: "uncompiled",
	}
	content, err := frontmatter.Marshal(sf, "Some body content long enough to pass compilation limits.")
	if err != nil {
		t.Fatalf("failed to marshal frontmatter: %v", err)
	}

	err = os.WriteFile(rawPath, content, 0644)
	if err != nil {
		t.Fatalf("failed to write raw file: %v", err)
	}

	// Sleep to let polling scan it (polls every 2s) and debounce for 2s. Total wait should be > 4.5s
	time.Sleep(5 * time.Second)

	// Check if raw file was updated to compiled
	rawContent, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("failed to read updated raw file: %v", err)
	}

	updatedSf, _, err := frontmatter.ParseSource(rawContent)
	if err != nil {
		t.Fatalf("failed to parse updated raw file: %v", err)
	}

	if updatedSf.Status != "compiled" {
		t.Errorf("expected status 'compiled', got '%s'", updatedSf.Status)
	}

	// Check if wiki article exists
	wikiPath := vault.WikiFilePath(cfg, "test-concept")
	if _, err := os.Stat(wikiPath); os.IsNotExist(err) {
		t.Errorf("expected wiki file to be created at %s, but it does not exist", wikiPath)
	}

	cancel()
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("Watcher returned error on shutdown: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Watcher failed to shut down within 1 second after cancel")
	}
}
