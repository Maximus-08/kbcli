package cleaner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/index"
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

func TestCleaner(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kb-cleaner-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		VaultKBPath:  tmpDir,
		CleanupModel: "mock-model",
	}

	err = vault.EnsureStructure(cfg)
	if err != nil {
		t.Fatalf("failed to build vault structure: %v", err)
	}

	// 1. Create a valid article and list it in index
	validWF := frontmatter.WikiFrontmatter{
		Title:   "Deep Neural Networks",
		Summary: "Summary of DNN.",
	}
	validContent, _ := frontmatter.Marshal(validWF, "Some body content")
	err = os.WriteFile(filepath.Join(vault.WikiDir(cfg), "deep-neural-networks.md"), validContent, 0644)
	if err != nil {
		t.Fatalf("failed to write valid file: %v", err)
	}

	indexPath := vault.IndexPath(cfg)
	_ = index.Append(indexPath, index.Entry{Slug: "deep-neural-networks", Summary: "Summary of DNN."})

	// 2. Create an orphan article (not indexed, no incoming links)
	orphanWF := frontmatter.WikiFrontmatter{
		Title:   "Orphan Article",
		Summary: "Summary of orphan.",
	}
	orphanContent, _ := frontmatter.Marshal(orphanWF, "Some other body content")
	orphanPath := filepath.Join(vault.WikiDir(cfg), "orphan-article.md")
	err = os.WriteFile(orphanPath, orphanContent, 0644)
	if err != nil {
		t.Fatalf("failed to write orphan file: %v", err)
	}

	// 3. Create a redundant article (shares title words "neural networks" with the first)
	redundantWF := frontmatter.WikiFrontmatter{
		Title:   "Deep Neural Networks Second",
		Summary: "Summary of second DNN.",
	}
	redundantContent, _ := frontmatter.Marshal(redundantWF, "Shorter body")
	redundantPath := filepath.Join(vault.WikiDir(cfg), "deep-neural-networks-second.md")
	err = os.WriteFile(redundantPath, redundantContent, 0644)
	if err != nil {
		t.Fatalf("failed to write redundant file: %v", err)
	}
	// Add redundant article to index to verify it is NOT classified as orphan, but as redundant
	_ = index.Append(indexPath, index.Entry{Slug: "deep-neural-networks-second", Summary: "Summary of second DNN."})

	// Mock LLM response to confirm redundancy
	mockJSON := `{"redundant": true, "reason": "Almost identical topic coverage."}`
	prov := &mockProvider{response: mockJSON}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := New(cfg, prov, logger)

	candidates, err := c.DetectCandidates(context.Background())
	if err != nil {
		t.Fatalf("DetectCandidates failed: %v", err)
	}

	// We expect:
	// - 1 orphan: orphan-article.md
	// - 1 redundant: deep-neural-networks-second.md (because it has shorter body)
	var hasOrphan, hasRedundant bool
	for _, cand := range candidates {
		if cand.Type == CandidateOrphan && filepath.Base(cand.Path) == "orphan-article.md" {
			hasOrphan = true
		}
		if cand.Type == CandidateRedundant && filepath.Base(cand.Path) == "deep-neural-networks-second.md" {
			hasRedundant = true
		}
	}

	if !hasOrphan {
		t.Errorf("Expected orphan candidate 'orphan-article.md' not found in: %+v", candidates)
	}
	if !hasRedundant {
		t.Errorf("Expected redundant candidate 'deep-neural-networks-second.md' not found in: %+v", candidates)
	}

	// Test TrashFile operation
	trashPath, err := TrashFile(cfg, orphanPath)
	if err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}

	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Errorf("Orphan file should be removed from original path")
	}

	if _, err := os.Stat(trashPath); os.IsNotExist(err) {
		t.Errorf("Orphan file should exist in trash folder at %s", trashPath)
	}
}
